package tests

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	. "github.com/whhhh1500/goimgtools"
)

// TestICO: an image written as an icon reads back the same; several sizes
// are each the image fitted to them, smallest first; the format follows the
// extension; what an icon cannot hold is an error.
func TestICO(t *testing.T) {
	small := noisyPhoto(64, 48, true)
	b, err := FromImage(small).ICO().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	if info, err := FromBytes(b).Metadata(); err != nil || info.Format != "ico" || info.Width != 64 || info.Height != 48 || !info.HasAlpha {
		t.Errorf("Metadata %+v, %v", info, err)
	}
	if d := worstDiff(t, small, decodeNRGBA(t, b)); d != 0 {
		t.Errorf("read back a channel %d off", d)
	}
	big := noisyPhoto(1200, 900, true)
	b, err = FromImage(big).ICO(ICOOptions{Sizes: []int{48, 16, 256, 32, 32}}).ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	n := int(binary.LittleEndian.Uint16(b[4:]))
	var sides [][2]int
	for i := 0; i < n; i++ {
		e := b[6+16*i:]
		w, h := int(e[0]), int(e[1])
		if w == 0 {
			w = 256
		}
		sides = append(sides, [2]int{w, h})
	}
	want := [][2]int{{16, 12}, {32, 24}, {48, 36}, {256, 192}}
	if len(sides) != len(want) {
		t.Fatalf("entries %v", sides)
	}
	for i := range want {
		if sides[i] != want[i] {
			t.Fatalf("entries %v, want %v", sides, want)
		}
	}
	if info, _ := FromBytes(b).Metadata(); info.Width != 256 || info.Height != 192 {
		t.Errorf("read: %dx%d, want the largest", info.Width, info.Height)
	}
	p := filepath.Join(t.TempDir(), "favicon.ico")
	if err := FromImage(small).ToFile(p); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(p); !bytes.HasPrefix(data, []byte{0, 0, 1, 0, 1, 0}) {
		t.Error("not an icon by its extension")
	}
	for _, bad := range []*Image{FromImage(big).ICO(), FromImage(small).ICO(ICOOptions{Sizes: []int{0}}), FromImage(small).ICO(ICOOptions{Sizes: []int{300}})} {
		if _, err := bad.ToBytes(); err == nil {
			t.Error("no error")
		}
	}
}
