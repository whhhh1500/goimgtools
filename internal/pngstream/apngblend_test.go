package pngstream

import (
	"bytes"
	"encoding/binary"
	"image"
	"io"
	"math/rand/v2"
	"testing"

	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// TestAPNGBlendChosen: a frame that changes two far-apart pixels of a
// noisy picture is written over it (blend_op OVER), the pixels between
// them cleared, not as the noise again.
func TestAPNGBlendChosen(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	a := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	for i := range a.Pix {
		a.Pix[i] = uint8(rng.IntN(256))
		if i%4 == 3 {
			a.Pix[i] = 255
		}
	}
	a.Pix[3] = 0 // a transparent pixel, so that the file has alpha
	b := image.NewNRGBA(a.Rect)
	copy(b.Pix, a.Pix)
	for _, p := range [][2]int{{1, 1}, {62, 62}} {
		o := b.PixOffset(p[0], p[1])
		b.Pix[o], b.Pix[o+1], b.Pix[o+2], b.Pix[o+3] = ^b.Pix[o], ^b.Pix[o+1], ^b.Pix[o+2], 255
	}
	frames := []*image.NRGBA{a, b}
	var out bytes.Buffer
	env := pipeline.NewEnv(0, "")
	n := 0
	err := EncodeAnimation(&out, env, AnimOptions{}, func() (pipeline.Source, int, error) {
		if n == len(frames) {
			return nil, 0, io.EOF
		}
		n++
		return testutil.ChoppedEnv(frames[n-1], 9, env), 100, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	p := out.Bytes()
	var fctl [][]byte
	for pos := 8; pos+12 <= len(p); {
		l := int(binary.BigEndian.Uint32(p[pos:]))
		if string(p[pos+4:pos+8]) == "fcTL" {
			fctl = append(fctl, p[pos+8:pos+8+l])
		}
		pos += 12 + l
	}
	if len(fctl) != 2 {
		t.Fatalf("%d fcTL chunks", len(fctl))
	}
	f := fctl[1]
	w, h := binary.BigEndian.Uint32(f[4:]), binary.BigEndian.Uint32(f[8:])
	x, y := binary.BigEndian.Uint32(f[12:]), binary.BigEndian.Uint32(f[16:])
	if w != 62 || h != 62 || x != 1 || y != 1 || f[25] != 1 {
		t.Errorf("second frame %dx%d at %d,%d, blend_op %d; want 62x62 at 1,1, OVER", w, h, x, y, f[25])
	}
}
