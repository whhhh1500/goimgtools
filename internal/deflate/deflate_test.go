package deflate

import (
	"bytes"
	"compress/flate"
	"io"
	"math"
	"math/rand/v2"
	"testing"
)

// samples are data of the kinds deflate meets: random, runs, text, rows
// of an image, and mixtures that make the block splitting work.
func samples() map[string][]byte {
	r := rand.New(rand.NewPCG(1, 2))
	random := make([]byte, 200<<10)
	for i := range random {
		random[i] = byte(r.Uint32())
	}
	text := bytes.Repeat([]byte("the quick brown fox jumps over the lazy dog; 0123456789\n"), 4000)
	for i := 0; i < len(text); i += 97 {
		text[i] = byte('a' + r.IntN(26))
	}
	rows := make([]byte, 0, 300<<10)
	for y := 0; len(rows) < 300<<10; y++ {
		rows = append(rows, byte(y%5))
		for x := 0; x < 600; x++ {
			v := byte(x/7 + y/3)
			if r.IntN(50) == 0 {
				v = byte(r.Uint32())
			}
			rows = append(rows, v, v/2, 255-v)
		}
	}
	mixed := append(append(append([]byte{}, text[:60000]...), random[:40000]...), rows[:200000]...)
	return map[string][]byte{
		"empty":  nil,
		"one":    {7},
		"short":  []byte("abcabcabcabcabcabd"),
		"zeros":  make([]byte, 700<<10),
		"random": random,
		"text":   text,
		"rows":   rows,
		"mixed":  mixed,
	}
}

func inflate(t *testing.T, dict, z []byte) []byte {
	t.Helper()
	got, err := io.ReadAll(flate.NewReaderDict(bytes.NewReader(z), dict))
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestRoundTrip(t *testing.T) {
	split := Level12
	split.NoSkip, split.SplitByCost, split.SplitPasses, split.BlockLen, split.CachePerBlock = true, true, 2, 128<<10, 3
	entropy := split
	entropy.EntropyCosts = true
	for _, p := range []Params{Level10, Level12, split, entropy, {SearchDepth: 8, NiceLen: 32, Passes: 1, BlockLen: 20000, CachePerBlock: 1}} {
		c := New(p)
		for name, data := range samples() {
			z := c.Compress(nil, data, 0, true)
			if got := inflate(t, nil, z); !bytes.Equal(got, data) {
				t.Fatalf("%s (%+v): round trip differs", name, p)
			}
			std := new(bytes.Buffer)
			fw, _ := flate.NewWriter(std, 9)
			fw.Write(data)
			fw.Close()
			t.Logf("%-6s depth %3d split %v: %7d -> %7d (flate 9: %7d)", name, p.SearchDepth, p.SplitByCost, len(data), len(z), std.Len())
		}
	}
}

// TestPieces compresses data in pieces, each with the data before as its
// dictionary and a sync flush after all but the last, as zBlocks does.
func TestPieces(t *testing.T) {
	split := Level10
	split.NoSkip, split.SplitByCost = true, true
	for _, c := range []*Compressor{New(Level10), New(split)} {
		for name, data := range samples() {
			for _, piece := range []int{1000, 32 << 10, 100 << 10} {
				var z []byte
				for start := 0; ; start += piece {
					end := min(start+piece, len(data))
					z = c.Compress(z, data[:end], start, end == len(data))
					if end == len(data) {
						break
					}
				}
				if got := inflate(t, nil, z); !bytes.Equal(got, data) {
					t.Fatalf("%s in pieces of %d: round trip differs", name, piece)
				}
			}
		}
	}
}

// TestDictionary checks that the dictionary is used and that output does
// not depend on what the compressor did before.
func TestDictionary(t *testing.T) {
	s := samples()
	data := s["text"][:100000]
	c := New(Level12)
	with := c.Compress(nil, data, 50000, true)
	if got := inflate(t, data[:50000], with); !bytes.Equal(got, data[50000:]) {
		t.Fatal("with a dictionary: round trip differs")
	}
	without := c.Compress(nil, data[50000:], 0, true)
	if len(with) >= len(without) {
		t.Errorf("the dictionary did not help: %d bytes with, %d without", len(with), len(without))
	}
	c.Compress(nil, s["rows"], 0, true)
	if again := c.Compress(nil, data, 50000, true); !bytes.Equal(again, with) {
		t.Error("output depends on what was compressed before")
	}
	if fresh := New(Level12).Compress(nil, data, 50000, true); !bytes.Equal(fresh, with) {
		t.Error("output differs with a new compressor")
	}
}

func TestStaticCodes(t *testing.T) {
	for i, l := range staticCodes.litlenLens {
		want := uint8(8)
		switch {
		case i >= 144 && i < 256:
			want = 9
		case i >= 256 && i < 280:
			want = 7
		}
		if l != want {
			t.Fatalf("static litlen %d: length %d, want %d", i, l, want)
		}
	}
	for i, l := range staticCodes.offsetLens {
		if l != 5 {
			t.Fatalf("static offset %d: length %d", i, l)
		}
	}
}

func TestOffsetSlot(t *testing.T) {
	for o := uint32(1); o <= 32768; o++ {
		s := offsetSlot(o)
		if o < offsetSlotBase[s] || (s+1 < uint32(len(offsetSlotBase)) && o >= offsetSlotBase[s+1]) {
			t.Fatalf("offset %d: slot %d", o, s)
		}
	}
}

func FuzzRoundTrip(f *testing.F) {
	for _, d := range samples() {
		f.Add(d[:min(len(d), 3000)], 0)
	}
	p := Params{SearchDepth: 20, NiceLen: 64, Passes: 3, MinImprove: 1, MinNonfinal: 1, MaxStaticLen: 1000, BlockLen: 6000, CachePerBlock: 2}
	c := New(p)
	p.NoSkip, p.SplitByCost, p.SplitPasses = true, true, 1
	split := New(p)
	lazy, lazy2 := New(LazyLevel6), New(LazyLevel9)
	f.Fuzz(func(t *testing.T, data []byte, start int) {
		if start < 0 || start > len(data) {
			start = 0
		}
		for _, c := range []*Compressor{c, split, lazy, lazy2} {
			z := c.Compress(nil, data, start, true)
			if got := inflate(t, data[:start], z); !bytes.Equal(got, data[start:]) {
				t.Fatalf("round trip differs (lazy %d)", c.p.Lazy)
			}
		}
	})
}

// TestEvenForRLE: evening out the counts never takes a code away from a
// symbol in use, nor gives one to the symbols after the last in use.
func TestEvenForRLE(t *testing.T) {
	r := rand.New(rand.NewPCG(5, 6))
	for i := 0; i < 20000; i++ {
		n := 1 + r.IntN(300)
		c := make([]uint32, n)
		for j := range c {
			switch r.IntN(4) {
			case 0:
			case 1:
				c[j] = uint32(r.IntN(3))
			default:
				c[j] = uint32(r.IntN(1 << uint(r.IntN(16))))
			}
		}
		orig := append([]uint32(nil), c...)
		evenForRLE(c)
		last := -1
		for j, v := range orig {
			if v != 0 {
				last = j
			}
		}
		for j := range c {
			if orig[j] != 0 && c[j] == 0 {
				t.Fatalf("count %d at %d became 0", orig[j], j)
			}
			if j > last && c[j] != 0 {
				t.Fatalf("trailing zero at %d became %d", j, c[j])
			}
		}
	}
}

// TestLog2Fix: the fixed-point logarithm is within 1/180 bit of log2.
func TestLog2Fix(t *testing.T) {
	for _, v := range []uint32{1, 2, 3, 255, 256, 257, 511, 512, 1000, 65535, 1 << 20, 123456789, 1<<32 - 1} {
		got := float64(log2Fix(v)) / 1024
		if want := math.Log2(float64(v)); math.Abs(got-want) > 1.0/180 {
			t.Errorf("log2(%d): %.5f, want %.5f", v, got, want)
		}
	}
}

// TestCompressShared: inputs with the same dictionary and length, each
// tail different, compressed with CompressShared are what Compress writes
// for them (the dictionary entered once), with dictionaries short and
// long (beyond the window), tails too short to share and long enough, and
// a first call that is not first in the run of calls.
func TestCompressShared(t *testing.T) {
	s := samples()
	r := rand.New(rand.NewPCG(3, 4))
	for _, p := range []Params{Level10, Level12} {
		c, ref := New(p), New(p)
		for _, src := range []string{"rows", "text", "mixed"} {
			data := s[src]
			for _, dict := range []int{0, 1000, 32 << 10, 50 << 10} {
				for _, after := range []int{100, sharedAfter - 1, sharedAfter, 3000, 70 << 10} {
					for _, tail := range []int{1, 500, 12000} {
						if dict+after+tail > len(data) {
							continue
						}
						base := data[:dict+after]
						for k := 0; k < 4; k++ {
							in := append(append([]byte{}, base...), data[dict+after:dict+after+tail]...)
							for i := range in[dict+after:] { // another tail each time
								if r.IntN(3) == 0 {
									in[dict+after+i] = byte(r.Uint32())
								}
							}
							want := ref.Compress(nil, in, dict, k%2 == 0)
							got, _ := c.CompressShared(nil, in, dict, k%2 == 0, k == 0)
							if !bytes.Equal(got, want) {
								t.Fatalf("%s depth %d dict %d after %d tail %d, call %d: not what Compress writes", src, p.SearchDepth, dict, after, tail, k)
							}
						}
					}
				}
			}
		}
		// Without a first call there is nothing to share: the dictionary
		// is entered.
		c = New(p)
		in := s["rows"][:40000]
		if got, _ := c.CompressShared(nil, in, 30000, true, false); !bytes.Equal(got, ref.Compress(nil, in, 30000, true)) {
			t.Fatalf("depth %d: without a first call, not what Compress writes", p.SearchDepth)
		}
	}
}

// TestEnterShared: the dictionary entered once with EnterShared, then
// taken by other compressors with TakeShared, each compressing its own
// tail: each output is what Compress writes.
func TestEnterShared(t *testing.T) {
	data := samples()["rows"]
	r := rand.New(rand.NewPCG(5, 6))
	for _, p := range []Params{Level10, Level12} {
		ref, first := New(p), New(p)
		others := []*Compressor{New(p), New(p), New(p)}
		for _, dict := range []int{1000, 32 << 10, 50 << 10} {
			for _, tail := range []int{1, 4000, 30000} {
				base := data[:dict+sharedAfter] // the same in all
				in := func(k int) []byte {
					b := append(append([]byte{}, base...), data[dict+sharedAfter:dict+sharedAfter+tail]...)
					for i := len(base); i < len(b); i++ {
						if r.IntN(4) == k%4 {
							b[i] = byte(r.Uint32())
						}
					}
					return b
				}
				ins := [][]byte{in(0), in(1), in(2), in(3)}
				first.EnterShared(ins[0], dict)
				for _, o := range others {
					o.TakeShared(first)
				}
				for k, c := range append([]*Compressor{first}, others...) {
					got, _ := c.CompressShared(nil, ins[k], dict, true, false)
					if want := ref.Compress(nil, ins[k], dict, true); !bytes.Equal(got, want) {
						t.Fatalf("depth %d dict %d tail %d, compressor %d: not what Compress writes", p.SearchDepth, dict, tail, k)
					}
				}
			}
		}
	}
}
