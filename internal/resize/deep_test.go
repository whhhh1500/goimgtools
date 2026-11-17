package resize

import (
	"image"
	"image/color"
	"reflect"
	"testing"

	"golang.org/x/image/draw"

	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// TestScaleDeep: a source of 16-bit samples (with and without alpha, and
// grey) is scaled to 16-bit strips holding what x/image's Scale gives an
// RGBA64 (or Gray16) destination, with helpers too.
func TestScaleDeep(t *testing.T) {
	w, h := 97, 61
	n64 := image.NewNRGBA64(image.Rect(0, 0, w, h))
	g16 := image.NewGray16(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			n64.SetNRGBA64(x, y, color.NRGBA64{uint16(x * 677), uint16(y * 1031), uint16((x*y)*97 + 5), uint16(0xffff - x*301)})
			g16.SetGray16(x, y, color.Gray16{uint16(x*601 + y*77)})
		}
	}
	opaque := image.NewRGBA64(image.Rect(0, 0, w, h))
	draw.Draw(opaque, opaque.Rect, n64, image.Point{}, draw.Src)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := opaque.RGBA64At(x, y)
			c.A = 0xffff
			opaque.SetRGBA64(x, y, c)
		}
	}
	for name, src := range map[string]image.Image{"nrgba64": n64, "rgba64": opaque, "gray16": g16} {
		for _, sz := range [][2]int{{40, 25}, {150, 90}, {97, 30}} {
			var want draw.Image = image.NewRGBA64(image.Rect(0, 0, sz[0], sz[1]))
			if name == "gray16" {
				want = image.NewGray16(image.Rect(0, 0, sz[0], sz[1]))
			}
			draw.CatmullRom.Scale(want, want.Bounds(), src, src.Bounds(), draw.Src, nil)
			for _, helpers := range []int{0, 3} {
				env := pipeline.NewEnv(0, "")
				env.SetHelpers(helpers)
				sc, err := Scale(testutil.ChoppedEnv(src, 7, env), sz[0], sz[1], draw.CatmullRom)
				if err != nil {
					t.Fatal(err)
				}
				got, err := pipeline.Collect(sc)
				if err != nil {
					t.Fatal(err)
				}
				if reflect.TypeOf(got) != reflect.TypeOf(want) || !reflect.DeepEqual(pix(got), pix(want)) {
					t.Errorf("%s to %v, %d helpers: %T differs from x/image's %T", name, sz, helpers, got, want)
				}
			}
		}
	}
}

func pix(m image.Image) []uint8 {
	switch p := m.(type) {
	case *image.RGBA64:
		return p.Pix
	case *image.Gray16:
		return p.Pix
	}
	return nil
}
