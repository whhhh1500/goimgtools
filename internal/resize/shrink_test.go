package resize_test

import (
	"image"
	"testing"

	"github.com/whhhh1500/goimgtools/internal/resize"
	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// boxRef averages s × s cells of a w × h plane the obvious way.
func boxRef(src []uint8, stride, w, h, s int) []uint8 {
	dw, dh := (w+s-1)/s, (h+s-1)/s
	out := make([]uint8, dw*dh)
	for j := 0; j < dh; j++ {
		for i := 0; i < dw; i++ {
			sum, n := 0, 0
			for y := j * s; y < min((j+1)*s, h); y++ {
				for x := i * s; x < min((i+1)*s, w); x++ {
					sum += int(src[y*stride+x])
					n++
				}
			}
			out[j*dw+i] = uint8((sum + n/2) / n)
		}
	}
	return out
}

func samePlane(t *testing.T, what string, got []uint8, stride int, want []uint8, w, h int) {
	t.Helper()
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if got[y*stride+x] != want[y*w+x] {
				t.Fatalf("%s: (%d,%d) = %d, want %d", what, x, y, got[y*stride+x], want[y*w+x])
			}
		}
	}
}

// TestShrink compares Shrink with a direct average of every plane, for
// sizes that are and are not multiples of the factor, with strips of one
// and of several MCU rows.
func TestShrink(t *testing.T) {
	for _, size := range [][2]int{{160, 96}, {67, 131}, {1, 1}, {17, 9}, {250, 33}} {
		w, h := size[0], size[1]
		imgs := testutil.Images(w, h)
		for _, name := range []string{"gray", "ycbcr444", "ycbcr422", "ycbcr420", "ycbcr440"} {
			for _, s := range []int{2, 4, 8} {
				for _, rows := range []int{16, 48} {
					env := pipeline.NewEnv(0, "")
					src, err := resize.Shrink(testutil.ChoppedEnv(imgs[name], rows, env), s)
					if err != nil {
						t.Fatal(err)
					}
					dw, dh := (w+s-1)/s, (h+s-1)/s
					if src.Bounds() != image.Rect(0, 0, dw, dh) {
						t.Fatalf("%s %dx%d /%d: bounds %v", name, w, h, s, src.Bounds())
					}
					got, err := pipeline.Collect(src)
					if err != nil {
						t.Fatalf("%s %dx%d /%d: %v", name, w, h, s, err)
					}
					src.Close()
					if env.Budget.Used() != 0 {
						t.Fatalf("%d bytes still reserved", env.Budget.Used())
					}
					what := name
					switch m := imgs[name].(type) {
					case *image.Gray:
						g := got.(*image.Gray)
						samePlane(t, what, g.Pix, g.Stride, boxRef(m.Pix, m.Stride, w, h, s), dw, dh)
					case *image.YCbCr:
						g := got.(*image.YCbCr)
						if g.SubsampleRatio != m.SubsampleRatio {
							t.Fatalf("%s: ratio changed", what)
						}
						samePlane(t, what+" Y", g.Y, g.YStride, boxRef(m.Y, m.YStride, w, h, s), dw, dh)
						cw, ch := m.CStride, len(m.Cb)/m.CStride
						samePlane(t, what+" Cb", g.Cb, g.CStride, boxRef(m.Cb, m.CStride, cw, ch, s), (cw+s-1)/s, (ch+s-1)/s)
						samePlane(t, what+" Cr", g.Cr, g.CStride, boxRef(m.Cr, m.CStride, cw, ch, s), (cw+s-1)/s, (ch+s-1)/s)
					}
				}
			}
		}
	}
	// What it cannot do is refused, not done wrongly.
	m := testutil.Images(64, 64)
	if _, err := resize.Shrink(testutil.Chopped(m["gray"], 16), 3); err == nil {
		t.Error("factor 3 accepted")
	}
	for _, c := range []struct {
		name string
		rows int
	}{{"nrgba", 16}, {"ycbcr411", 16}, {"ycbcr420", 7}} {
		src, _ := resize.Shrink(testutil.Chopped(m[c.name], c.rows), 2)
		if _, err := pipeline.Collect(src); err == nil {
			t.Errorf("%s in strips of %d rows: no error", c.name, c.rows)
		}
	}
}
