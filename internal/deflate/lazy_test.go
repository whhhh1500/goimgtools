package deflate

import (
	"bytes"
	"fmt"
	"hash/fnv"
	"testing"
)

var lazyLevels = []Params{LazyLevel5, LazyLevel6, LazyLevel7, LazyLevel8, LazyLevel9}

// TestLazyLibdeflate checks the lazy levels against libdeflate 1.25's
// output for the samples (libdeflate_deflate_compress at levels 5 to 9,
// which stores inputs of up to 55 bytes, so the short samples are left
// out): sizes and FNV-1a hashes, the same on every processor.
func TestLazyLibdeflate(t *testing.T) {
	want := map[string][5]string{
		"mixed":  {"52894 013b4d1d919341d0", "52318 7958ba331972d4c3", "51445 f7031ab480e60d89", "51084 49541e62063f84a8", "51019 1e8ac1158f5d683b"},
		"random": {"204820 b43aa60890924040", "204820 b43aa60890924040", "204820 b43aa60890924040", "204820 b43aa60890924040", "204820 b43aa60890924040"},
		"rows":   {"16083 c33f8c1276804529", "15107 f7580391504903fd", "14246 08fbd5de471aefe0", "13782 98ae05a7e34491aa", "13782 98ae05a7e34491aa"},
		"text":   {"7794 fc6f36a3754e15c2", "7939 fa7edf6313d8f502", "6724 c959bf8deb2fac87", "6479 7a0576f8a00ece55", "6214 e4c32cc573cfd337"},
		"zeros":  {"736 6ba77320284a8a48", "736 6ba77320284a8a48", "736 6ba77320284a8a48", "736 6ba77320284a8a48", "736 6ba77320284a8a48"},
	}
	s := samples()
	for name, w := range want {
		for i, p := range lazyLevels {
			z := New(p).Compress(nil, s[name], 0, true)
			h := fnv.New64a()
			h.Write(z)
			if got := fmt.Sprintf("%d %016x", len(z), h.Sum64()); got != w[i] {
				t.Errorf("%s at level %d: %s, libdeflate's %s", name, 5+i, got, w[i])
			}
		}
	}
}

func TestLazyRoundTrip(t *testing.T) {
	for _, p := range lazyLevels {
		c := New(p)
		for name, data := range samples() {
			z := c.Compress(nil, data, 0, true)
			if got := inflate(t, nil, z); !bytes.Equal(got, data) {
				t.Fatalf("%s (%+v): round trip differs", name, p)
			}
		}
	}
}

// TestLazyPieces compresses data in pieces, each with the data before as
// its dictionary and a sync flush after all but the last, as zBlocks does;
// the pieces of 256KB cross the window's moving on with the dictionary in
// it.
func TestLazyPieces(t *testing.T) {
	for _, p := range []Params{LazyLevel6, LazyLevel8} {
		c := New(p)
		for name, data := range samples() {
			for _, piece := range []int{1000, 32 << 10, 256 << 10} {
				var z []byte
				for start := 0; ; start += piece {
					end := min(start+piece, len(data))
					z = c.Compress(z, data[:end], start, end == len(data))
					if end == len(data) {
						break
					}
				}
				if got := inflate(t, nil, z); !bytes.Equal(got, data) {
					t.Fatalf("%s in pieces of %d (lazy %d): round trip differs", name, piece, p.Lazy)
				}
			}
		}
	}
}

// TestLazyDictionary checks that the dictionary is used and that output
// does not depend on what the compressor did before.
func TestLazyDictionary(t *testing.T) {
	s := samples()
	data := s["text"][:100000]
	c := New(LazyLevel7)
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
	if fresh := New(LazyLevel7).Compress(nil, data, 50000, true); !bytes.Equal(fresh, with) {
		t.Error("output differs with a new compressor")
	}
}
