package zopfli

import (
	"bytes"
	"compress/flate"
	"crypto/sha256"
	"fmt"
	"io"
	"math/rand/v2"
	"runtime"
	"slices"
	"sync"
	"testing"
)

// testInputs are made the same on every machine: text from a small Markov
// source, rows of a smooth picture after the Paeth filter, a run, noise.
func testInputs() map[string][]byte {
	r := rand.New(rand.NewPCG(1, 2))
	words := []string{"the ", "image ", "of ", "a ", "pixel ", "row ", "and ", "deflate ", "block ", "code ", "\n", "zopfli ", "length "}
	var text []byte
	for len(text) < 40000 {
		text = append(text, words[r.IntN(len(words))]...)
	}
	const w, h = 300, 120
	var img []byte
	prev := make([]byte, w*3)
	for y := 0; y < h; y++ {
		cur := make([]byte, w*3)
		for x := range cur {
			cur[x] = byte((x/3)*2 + y*3 + int(r.IntN(6)) + 40*(x%3))
		}
		img = append(img, 4)
		for x := range cur {
			var a, b, c byte
			if x >= 3 {
				a, c = cur[x-3], prev[x-3]
			}
			b = prev[x]
			p := int(a) + int(b) - int(c)
			pa, pb, pc := abs(p-int(a)), abs(p-int(b)), abs(p-int(c))
			pred := c
			if pa <= pb && pa <= pc {
				pred = a
			} else if pb <= pc {
				pred = b
			}
			img = append(img, cur[x]-pred)
		}
		prev = cur
	}
	noise := make([]byte, 5000)
	for i := range noise {
		noise[i] = byte(r.Uint32())
	}
	run := bytes.Repeat([]byte{7}, 3000)
	// "short" is a dynamic block, "tiny" a fixed one.
	return map[string][]byte{"text": text, "image": img, "noise": noise, "run": append(run, text[:2000]...), "short": text[:300], "tiny": text[:40]}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// The SHA-256 (first 16 hex digits) of the C zopfli's output for each
// input, with its default options (15 iterations, block splitting into 15
// blocks at most): zopfli --deflate --i15.
var wantSums = map[string]string{
	"text":  "29f3aa477df6ffe7", // 6181 bytes
	"image": "d2fa5b6cfa527667", // 48264 bytes
	"noise": "e63268e4b3ed578b", // 5005 bytes
	"run":   "69f0c0943eb6f1cd", // 471 bytes
	"short": "5b9aad2f4162ee6a", // 116 bytes
	"tiny":  "72976005820ea523", // 37 bytes
}

func TestSameAsZopfli(t *testing.T) {
	for name, data := range testInputs() {
		got := Deflate(nil, DefaultOptions(), data)
		back, err := io.ReadAll(flate.NewReader(bytes.NewReader(got)))
		if err != nil || !bytes.Equal(back, data) {
			t.Errorf("%s: does not inflate to the data (%v)", name, err)
		}
		sum := fmt.Sprintf("%x", sha256.Sum256(got))[:16]
		if sum != wantSums[name] {
			t.Errorf("%s: %d bytes, sum %s, want %s", name, len(got), sum, wantSums[name])
		}
	}
}

// TestCompressParts: data compressed in parts by one Compressor, each with
// the data before as its dictionary, ending in a sync flush but for the
// last, inflates to the data; a part on its own is what Zopfli writes for
// it (the first part's sum is that of Deflate). The Compressor is made for
// less than a part and grows.
func TestCompressParts(t *testing.T) {
	for name, data := range testInputs() {
		for _, part := range []int{len(data), 7000, 40000} {
			c := New(DefaultOptions(), 100)
			var out []byte
			for s := 0; s < len(data); s += part {
				e := min(len(data), s+part)
				n := len(out)
				out = c.Compress(out, data[:e], s, e == len(data))
				if s == 0 && e == len(data) && fmt.Sprintf("%x", sha256.Sum256(out[n:]))[:16] != wantSums[name] {
					t.Errorf("%s: Compress is not Deflate", name)
				}
				if e < len(data) && !bytes.HasSuffix(out, []byte{0, 0, 0xff, 0xff}) {
					t.Errorf("%s: part %d does not end in a sync flush", name, s/part)
				}
			}
			back, err := io.ReadAll(flate.NewReader(bytes.NewReader(out)))
			if err != nil || !bytes.Equal(back, data) {
				t.Errorf("%s in parts of %d: does not inflate to the data (%v)", name, part, err)
			}
		}
	}
}

// TestBytes: what a Compressor holds is what Bytes says, near enough.
func TestBytes(t *testing.T) {
	for _, n := range []int{1, 1000, 256 << 10} {
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		c := New(DefaultOptions(), n)
		runtime.ReadMemStats(&after)
		got := int64(after.TotalAlloc - before.TotalAlloc)
		// The allocator rounds each slice up a little.
		if want := Bytes(n); got > want+want/100+8<<10 || got < want*9/10 {
			t.Errorf("%d bytes: made %d, Bytes says %d", n, got, want)
		}
		runtime.KeepAlive(c)
	}
}

// TestCompressBits: blocks compressed with CompressBits, each after the
// data before, and joined bit by bit are the stream of DeflateBlocks with
// master blocks of their length; a stored block's data starts on a byte of
// the stream (its header is padded there), and it is found where
// CompressBits says.
func TestCompressBits(t *testing.T) {
	inputs := testInputs()
	// Text with noise in between, which is stored, after blocks that end
	// anywhere in a byte.
	var mixed []byte
	seed := uint32(5)
	for k := 0; k < 4; k++ {
		mixed = append(mixed, bytes.Repeat([]byte(fmt.Sprintf("row %d of some text, ", k)), 300+97*k)...)
		for i := 0; i < 9000+1234*k; i++ {
			seed = seed*1664525 + 1013904223
			mixed = append(mixed, byte(seed>>24))
		}
	}
	inputs["text and noise"] = mixed
	shifted := 0 // stored blocks after bits pending
	for name, data := range inputs {
		for _, part := range []int{7000, 40000} {
			c := New(DefaultOptions(), part)
			var out []byte
			var pend byte
			pendN := 0
			for s := 0; s < len(data); s += part {
				e := min(len(data), s+part)
				blk, bits, stored := c.CompressBits(nil, data[:e], s, e == len(data))
				if len(blk) == 0 {
					continue
				}
				if stored >= 0 {
					bit := func(i int) byte { return blk[i/8] >> (i % 8) & 1 }
					if bit(stored+1) != 0 || bit(stored+2) != 0 {
						t.Errorf("%s in blocks of %d: no stored block at bit %d", name, part, stored)
					}
					if pendN != 0 {
						shifted++
					}
				}
				// The block's bits after the pendN pending ones.
				n := 8*(len(blk)-1) + bits
				for i := 0; i < n; i++ {
					if stored >= 0 && i == stored+3 {
						// The stored block's data starts on a byte.
						if pendN != 0 {
							out, pend, pendN = append(out, pend), 0, 0
						}
						if i = (i + 7) &^ 7; i == n {
							break
						}
					}
					pend |= (blk[i/8] >> (i % 8) & 1) << pendN
					if pendN++; pendN == 8 {
						out, pend, pendN = append(out, pend), 0, 0
					}
				}
			}
			if pendN != 0 {
				out = append(out, pend)
			}
			if want := DeflateBlocks(nil, DefaultOptions(), data, part); !bytes.Equal(out, want) {
				t.Errorf("%s in blocks of %d: joined, not the stream of DeflateBlocks", name, part)
			}
		}
	}
	if shifted == 0 {
		t.Error("no stored block was joined after bits pending")
	}
}

// goWorkers runs the parts on goroutines of their own, lending memory for
// at most room bytes of workers.
type goWorkers struct {
	mu         sync.Mutex
	room, used int64
}

func (g *goWorkers) Parallel(n int, fn func(i int)) {
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fn(i)
		}()
	}
	wg.Wait()
}

func (g *goWorkers) Reserve(n int64) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.used+n > g.room {
		return false
	}
	g.used += n
	return true
}

func (g *goWorkers) Release(n int64) {
	g.mu.Lock()
	g.used -= n
	g.mu.Unlock()
}

// TestWorkers: with the parts of each block parsed at the same time, with
// memory for many workers, for one, or for none, the bytes are Zopfli's,
// and the memory lent is given back.
func TestWorkers(t *testing.T) {
	for _, room := range []int64{1 << 40, workerBytes(minWorker), 0} {
		g := &goWorkers{room: room}
		c := New(DefaultOptions(), 1000)
		c.SetWorkers(g)
		for name, data := range testInputs() {
			out := c.Compress(nil, data, 0, true)
			if sum := fmt.Sprintf("%x", sha256.Sum256(out))[:16]; sum != wantSums[name] {
				t.Errorf("room %d, %s: sum %s, want %s", room, name, sum, wantSums[name])
			}
		}
		c.Release()
		if g.used != 0 {
			t.Errorf("room %d: %d bytes not given back", room, g.used)
		}
	}
}

// TestMoreOptions: with Ultra, MoreCodes, FineSplit and no limit on the
// blocks the data comes back, and is no larger than Zopfli makes it on
// these inputs.
func TestMoreOptions(t *testing.T) {
	more := DefaultOptions()
	more.Ultra, more.MoreCodes, more.FineSplit, more.BlockSplittingMax = true, true, true, 0
	for name, data := range testInputs() {
		got := Deflate(nil, more, data)
		back, err := io.ReadAll(flate.NewReader(bytes.NewReader(got)))
		if err != nil || !bytes.Equal(back, data) {
			t.Fatalf("%s: does not inflate to the data (%v)", name, err)
		}
		if want := Deflate(nil, DefaultOptions(), data); len(got) > len(want) {
			t.Errorf("%s: %d bytes, Zopfli %d", name, len(got), len(want))
		}
	}
}

// TestBrotliEven: Brotli's evening out of the counts gives what ECT's C
// code (OptimizeHuffmanCountsForRle in its deflate.cpp) gives for them.
func TestBrotliEven(t *testing.T) {
	var e, eWant [32]uint64
	for i := 0; i < 30; i++ {
		e[i] = uint64((i*37)%11 + 4)
		if i > 20 {
			e[i] -= 4
		}
	}
	eWant = [32]uint64{8, 8, 8, 8, 8, 13, 10, 10, 10, 10, 10, 8, 8, 8, 8, 8, 13, 9, 9, 9, 9, 9, 4, 4, 4, 4, 4, 9, 2, 6, 0, 0}
	for _, c := range []struct{ in, want []uint64 }{
		{[]uint64{0, 0, 0}, []uint64{0, 0, 0}},
		{[]uint64{5, 5, 6, 5, 5, 4, 0, 0, 9}, []uint64{5, 5, 5, 5, 5, 5, 0, 0, 9}},
		{[]uint64{100, 101, 99, 100, 3, 3, 3, 3, 3, 3, 3, 1}, []uint64{100, 100, 100, 100, 3, 3, 3, 3, 3, 3, 3, 1}},
		{[]uint64{7, 9, 8, 8, 7, 0, 0, 0, 0, 0, 0, 2, 2, 2, 3, 2, 2, 50, 1}, []uint64{8, 8, 8, 8, 8, 0, 0, 0, 0, 0, 0, 2, 2, 2, 2, 2, 2, 50, 1}},
		{e[:], eWant[:]},
	} {
		got := append([]uint64{}, c.in...)
		brotliEvenForRle(len(got), got)
		if !slices.Equal(got, c.want) {
			t.Errorf("%v: %v, want %v", c.in, got, c.want)
		}
	}
}

// TestFineSplit: a stretch unlike the data before it, at its end, as an
// icon's transparent rows are, is split off by fineSplit, to blocks
// estimated smaller; fineSplit keeps the points it is given, and adds only
// what makes the estimate smaller; with FineSplit the data comes back, no
// larger.
func TestFineSplit(t *testing.T) {
	var data []byte
	rng := rand.New(rand.NewPCG(7, 8))
	for len(data) < 60000 {
		data = append(data, fmt.Sprintf("%d,%d;", rng.IntN(1000), rng.IntN(50))...)
	}
	edge := len(data)
	data = append(data, bytes.Repeat([]byte{0, 0, 0, 0, 9, 0, 0, 0}, 750)...)
	opts := DefaultOptions()
	c := New(opts, len(data))
	s := &c.whole
	s.reset(data)
	c.own.blockState(&c.opts, 0, len(data), false).greedy(data, 0, len(data), s, c.own.h)
	near := func(split []int) bool {
		for _, at := range split {
			if p := s.byteRange(0, at); p > edge-64 && p < edge+64 {
				return true
			}
		}
		return false
	}
	cost := func(split []int) float64 {
		bounds := append(append([]int{0}, split...), s.size())
		total := 0.0
		for i := 0; i+1 < len(bounds); i++ {
			total += calculateBlockSizeAutoType(s, bounds[i], bounds[i+1])
		}
		return total
	}
	if fs := fineSplit(s, nil); !near(fs) || cost(fs) >= cost(nil) {
		t.Errorf("fineSplit %v (%.0f bits, %.0f unsplit): no split near byte %d", fs, cost(fs), cost(nil), edge)
	}
	zs := c.blockSplitLZ77(s, 0)
	fs := fineSplit(s, zs)
	for _, at := range zs {
		if !slices.Contains(fs, at) {
			t.Errorf("fineSplit %v leaves out %d of %v", fs, at, zs)
		}
	}
	if cost(fs) > cost(zs) {
		t.Errorf("fineSplit %v estimated %.0f bits, Zopfli's %v %.0f", fs, cost(fs), zs, cost(zs))
	}
	fine := opts
	fine.FineSplit = true
	got, want := Deflate(nil, fine, data), Deflate(nil, opts, data)
	back, err := io.ReadAll(flate.NewReader(bytes.NewReader(got)))
	if err != nil || !bytes.Equal(back, data) {
		t.Fatalf("does not inflate to the data (%v)", err)
	}
	if len(got) > len(want) {
		t.Errorf("%d bytes, %d without FineSplit", len(got), len(want))
	}
}
