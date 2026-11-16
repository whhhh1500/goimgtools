package deflate

import (
	"bytes"
	"compress/flate"
	"io"
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
	for _, p := range []Params{Level10, Level12, {SearchDepth: 8, NiceLen: 32, Passes: 1, BlockLen: 20000, CachePerBlock: 1}} {
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
			t.Logf("%-6s depth %3d: %7d -> %7d (flate 9: %7d)", name, p.SearchDepth, len(data), len(z), std.Len())
		}
	}
}

// TestPieces compresses data in pieces, each with the data before as its
// dictionary and a sync flush after all but the last, as zBlocks does.
func TestPieces(t *testing.T) {
	c := New(Level10)
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
	c := New(Params{SearchDepth: 20, NiceLen: 64, Passes: 3, MinImprove: 1, MinNonfinal: 1, MaxStaticLen: 1000, BlockLen: 6000, CachePerBlock: 2})
	f.Fuzz(func(t *testing.T, data []byte, start int) {
		if start < 0 || start > len(data) {
			start = 0
		}
		z := c.Compress(nil, data, start, true)
		if got := inflate(t, data[:start], z); !bytes.Equal(got, data[start:]) {
			t.Fatal("round trip differs")
		}
	})
}
