package tgastream

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// TestPackRow: Pillow's packets: two equal pixels start a run, a literal
// stops before them, 128 pixels at most a packet.
func TestPackRow(t *testing.T) {
	for _, c := range []struct{ row, want string }{
		{"ABBC", "\x00A\x81B\x00C"},
		{"A", "\x00A"},
		{"AA", "\x81A"},
		{"ABCD", "\x03ABCD"},
		{"ABCCCD", "\x01AB\x82C\x00D"},
	} {
		if got := string(packRow(nil, []byte(c.row), 1)); got != c.want {
			t.Errorf("%q: %q, want %q", c.row, got, c.want)
		}
	}
	long := bytes.Repeat([]byte{9}, 300)
	if got := packRow(nil, long, 1); !bytes.Equal(got, []byte{0xff, 9, 0xff, 9, 0xab, 9}) {
		t.Errorf("300 of a pixel: % x", got)
	}
	lit := make([]byte, 200)
	for i := range lit {
		lit[i] = byte(i)
	}
	if got := packRow(nil, lit, 1); got[0] != 127 || got[129] != 71 || len(got) != 202 {
		t.Errorf("200 literals: % x", got[:4])
	}
}

// FuzzOpen: any file is read or refused, without a panic, within the
// memory it reserves.
func FuzzOpen(f *testing.F) {
	for _, m := range testutil.Images(7, 5) {
		var b bytes.Buffer
		if err := Encode(&b, testutil.Chopped(m, 2)); err == nil {
			f.Add(b.Bytes())
		}
	}
	h := make([]byte, 18)
	h[2], h[16], h[17] = 10, 24, 0 // compressed, bottom up
	binary.LittleEndian.PutUint16(h[12:], 3)
	binary.LittleEndian.PutUint16(h[14:], 2)
	f.Add(append(h, 0x85, 1, 2, 3))
	dir := f.TempDir() // one for every input: making one each is slow
	f.Fuzz(func(t *testing.T, data []byte) {
		env := pipeline.NewEnv(4<<20, dir)
		src, err := Open(bytes.NewReader(data), int64(len(data)), env)
		if err != nil {
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
