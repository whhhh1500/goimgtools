package deflate

import (
	"bytes"
	"runtime"
	"testing"
)

// TestBytes: what New allocates is what Params.Bytes says (the memory limit
// counts on it), for every level, with and without MaxInput; and a
// compressor made for less input than it is given still compresses it, the
// same as one made for any input.
func TestBytes(t *testing.T) {
	params := map[string]Params{
		"lazy5": LazyLevel5, "lazy7": LazyLevel7, "lazy9": LazyLevel9,
		"10": Level10, "12": Level12,
	}
	for name, p := range params {
		for _, maxIn := range []int{0, 1000, 46 << 10, 256 << 10} {
			p.MaxInput = maxIn
			var a, b runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&a)
			c := New(p)
			runtime.ReadMemStats(&b)
			got, want := int64(b.TotalAlloc-a.TotalAlloc), p.Bytes()
			// The allocator rounds each allocation up a little.
			if got > want+want/50+4096 || got < want-want/50-4096 {
				t.Errorf("%s, MaxInput %d: New allocates %d bytes, Bytes says %d", name, maxIn, got, want)
			}
			runtime.KeepAlive(c)
		}
	}
	in := samples()["mixed"]
	small := LazyLevel7
	small.MaxInput = 1000
	a := New(LazyLevel7).Compress(nil, in, 0, true)
	if b := New(small).Compress(nil, in, 0, true); !bytes.Equal(a, b) {
		t.Error("a compressor made for less input compresses differently")
	}
}
