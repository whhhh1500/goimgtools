package psdstream

import (
	"bytes"
	"testing"

	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// TestUnpackBits: rows that do not fill their width, or overflow it, are
// refused; what follows a full row is ignored.
func TestUnpackBits(t *testing.T) {
	dst := make([]byte, 5)
	for _, c := range []struct {
		src  []byte
		ok   bool
		want string
	}{
		{[]byte{0xfe, 7, 1, 8, 9}, true, "\x07\x07\x07\x08\x09"},
		{[]byte{0xfe, 7, 1, 8, 9, 0x80, 0, 3}, true, "\x07\x07\x07\x08\x09"},
		{[]byte{0x80, 0xfc, 4}, true, "\x04\x04\x04\x04\x04"},
		{[]byte{0xfe, 7}, false, ""},
		{[]byte{0xfb, 7}, false, ""},
		{[]byte{5, 1, 2, 3, 4, 5, 6}, false, ""},
		{[]byte{3, 1, 2}, false, ""},
	} {
		clear(dst)
		if ok := unpackBits(dst, c.src); ok != c.ok || ok && string(dst) != c.want {
			t.Errorf("%x: %v %x", c.src, ok, dst)
		}
	}
}

// FuzzOpen: any file is read or refused, without a panic, within the
// memory it reserves.
func FuzzOpen(f *testing.F) {
	px := func(n, size int) [][]byte {
		out := make([][]byte, n)
		for c := range out {
			for i := 0; i < size; i++ {
				out[c] = append(out[c], uint8(i*(c+3)))
			}
		}
		return out
	}
	f.Add(testutil.PSD(testutil.PSDFile{RLE: true, Mode: 3, Depth: 8, W: 5, H: 3, Channels: px(4, 15), Layers: -1}))
	f.Add(testutil.PSD(testutil.PSDFile{PSB: true, Mode: 1, Depth: 16, W: 3, H: 2, Channels: px(1, 12), Tagged: map[string]bool{"Mtrn": true}}))
	f.Add(testutil.PSD(testutil.PSDFile{Mode: 2, Depth: 8, W: 4, H: 2, Channels: px(1, 8), Palette: make([]byte, 768), Res: map[int][]byte{1047: {0, 1}}}))
	f.Add(testutil.PSD(testutil.PSDFile{RLE: true, Mode: 4, Depth: 16, W: 2, H: 2, Channels: px(5, 8)}))
	f.Add(testutil.PSD(testutil.PSDFile{Mode: 0, Depth: 1, W: 9, H: 2, Channels: px(1, 4)}))
	f.Add(testutil.PSD(testutil.PSDFile{Mode: 9, Depth: 8, W: 2, H: 1, Channels: px(3, 2)}))
	f.Fuzz(func(t *testing.T, data []byte) {
		env := pipeline.NewEnv(4<<20, t.TempDir())
		src, err := Open(bytes.NewReader(data), int64(len(data)), env)
		if err != nil {
			return
		}
		if b := src.Bounds(); int64(b.Dx())*int64(b.Dy()) > 1<<22 {
			src.Close()
			return
		}
		for {
			if _, err := src.Next(); err != nil {
				break
			}
		}
		src.Close()
		if env.Budget.Used() != 0 {
			t.Fatalf("%d bytes reserved", env.Budget.Used())
		}
	})
}
