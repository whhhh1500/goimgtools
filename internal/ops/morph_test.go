package ops

import (
	"image"
	"image/color"
	"math/rand/v2"
	"testing"

	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// TestMorphSquare: Morph against the AND (OR) of every pixel of the square
// looked up one by one, the edges repeated, for squares narrower and wider
// than the image, of every width up to past where the blocks take over,
// odd and even image widths.
func TestMorphSquare(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 8))
	for i := range 300 {
		w, h := 1+rng.IntN(37), 1+rng.IntN(19)
		width := 1 + i%13
		if i%25 == 0 {
			width = 100 + rng.IntN(200)
		}
		m := image.NewRGBA(image.Rect(0, 0, w, h))
		for k := range m.Pix {
			// Few bits set or few clear, so that the squares do not all
			// come to 0 or 255.
			v := uint8(rng.Uint32() & rng.Uint32() & rng.Uint32())
			if i%2 == 0 {
				v = ^v
			}
			m.Pix[k] = v
			if k%4 == 3 {
				m.Pix[k] = 255
			}
		}
		for _, dilate := range []bool{true, false} {
			env := pipeline.NewEnv(0, "")
			src, err := Morph(opaque{testutil.ChoppedEnv(m, 1+i%5, env)}, width, dilate)
			if err != nil {
				t.Fatal(err)
			}
			got := collect(t, src, env)
			for y := 0; y < h; y++ {
				for x := 0; x < w; x++ {
					var want [4]uint8
					for c := range want {
						v := uint8(0)
						if dilate {
							v = 255
						}
						for yy := y - width; yy <= y+width; yy++ {
							for xx := x - width; xx <= x+width; xx++ {
								p := m.Pix[m.PixOffset(min(max(xx, 0), w-1), min(max(yy, 0), h-1))+c]
								if dilate {
									v &= p
								} else {
									v |= p
								}
							}
						}
						want[c] = v
					}
					want[3] = 255
					g := color.RGBAModel.Convert(got.At(x, y)).(color.RGBA)
					if [4]uint8{g.R, g.G, g.B, g.A} != want {
						t.Fatalf("%dx%d width %d dilate %v at %d,%d: %v, want %v", w, h, width, dilate, x, y, g, want)
					}
				}
			}
		}
	}
}
