package zopfli

import (
	"bytes"
	"compress/flate"
	"crypto/sha256"
	"fmt"
	"io"
	"math/rand/v2"
	"runtime"
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
// master blocks of their length.
func TestCompressBits(t *testing.T) {
	for name, data := range testInputs() {
		for _, part := range []int{7000, 40000} {
			c := New(DefaultOptions(), part)
			var out []byte
			var pend byte
			pendN := 0
			for s := 0; s < len(data); s += part {
				e := min(len(data), s+part)
				blk, bits := c.CompressBits(nil, data[:e], s, e == len(data))
				if len(blk) == 0 {
					continue
				}
				// The block's bits after the pendN pending ones.
				for i := 0; i < 8*(len(blk)-1)+bits; i++ {
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
}
