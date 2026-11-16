package resize

import (
	"image"
	"image/color"
	"math"
	"math/rand/v2"
	"testing"

	"golang.org/x/image/draw"

	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// photoYCbCr is a w × h YCbCr image of ratio r: smooth chroma, luma with
// gradients and noise, as a photograph's.
func photoYCbCr(w, h int, r image.YCbCrSubsampleRatio) *image.YCbCr {
	m := image.NewYCbCr(image.Rect(0, 0, w, h), r)
	rng := rand.New(rand.NewPCG(3, 4))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := 128 + 90*math.Sin(float64(x)/23+float64(y)/37) + float64(rng.IntN(21)-10)
			m.Y[m.YOffset(x, y)] = uint8(max(0, min(255, v)))
			c := m.COffset(x, y)
			m.Cb[c] = uint8(128 + 60*math.Cos(float64(x)/41))
			m.Cr[c] = uint8(128 + 60*math.Sin(float64(y)/53))
		}
	}
	return m
}

// refPlane scales a plane in floating point with the weights of newDistrib:
// what ScalePlanes computes in fixed point.
func refPlane(pix []byte, stride, sw, sh, dw, dh int, ew, eh float64) []byte {
	hz := newDistrib(draw.CatmullRom, int32(dw), int32(sw), ew)
	vt := newDistrib(draw.CatmullRom, int32(dh), int32(sh), eh)
	tmp := make([]float64, dw*sh)
	for y := 0; y < sh; y++ {
		for x, s := range hz.sources {
			var v float64
			for _, c := range hz.contribs[s.i:s.j] {
				v += float64(pix[y*stride+int(c.coord)]) * c.weight
			}
			tmp[y*dw+x] = v * s.invTotalWeight
		}
	}
	out := make([]byte, dw*dh)
	for y, s := range vt.sources {
		for x := 0; x < dw; x++ {
			var v float64
			for _, c := range vt.contribs[s.i:s.j] {
				v += tmp[int(c.coord)*dw+x] * c.weight
			}
			out[y*dw+x] = uint8(max(0, min(255, math.Round(v*s.invTotalWeight))))
		}
	}
	return out
}

type testPlane struct {
	name            string
	got, src        []byte
	gstride, stride int
	sw, sh, dw, dh  int
	ew, eh          float64
}

// TestScalePlanes checks every plane scaled in fixed point against the same
// scale in floating point (within 1), for every ratio, gray, odd sizes and
// a partial extent; and, on an image within the RGB gamut, the result
// against the RGBA scale of the same image.
func TestScalePlanes(t *testing.T) {
	for _, r := range []image.YCbCrSubsampleRatio{image.YCbCrSubsampleRatio444, image.YCbCrSubsampleRatio422,
		image.YCbCrSubsampleRatio420, image.YCbCrSubsampleRatio440, -1} {
		for _, sz := range [][4]int{{301, 203, 151, 77}, {640, 480, 123, 92}, {97, 61, 200, 130}, {64, 64, 64, 64}, {33, 17, 1, 1}} {
			sw, sh, dw, dh := sz[0], sz[1], sz[2], sz[3]
			var src image.Image
			gray := r < 0
			ycc := photoYCbCr(sw, sh, max(r, 0))
			if gray {
				g := image.NewGray(image.Rect(0, 0, sw, sh))
				for y := 0; y < sh; y++ {
					copy(g.Pix[y*g.Stride:], ycc.Y[y*ycc.YStride:y*ycc.YStride+sw])
				}
				src = g
			} else {
				src = ycc
			}
			ew, eh := float64(sw)-0.5, float64(sh)-0.25
			env := pipeline.NewEnv(0, "")
			got, err := ScalePlanes(testutil.ChoppedEnv(src, 16, env), dw, dh, draw.CatmullRom, ew, eh, max(r, 0), gray)
			if err != nil {
				t.Fatal(err)
			}
			gm, err := pipeline.Collect(got)
			got.Close()
			if err != nil {
				t.Fatalf("ratio %v %v: %v", r, sz, err)
			}
			if gm.Bounds() != image.Rect(0, 0, dw, dh) {
				t.Fatalf("ratio %v %v: bounds %v", r, sz, gm.Bounds())
			}
			var ps []testPlane
			if gray {
				g, ok := gm.(*image.Gray)
				if !ok {
					t.Fatalf("gray: %T", gm)
				}
				ps = append(ps, testPlane{"gray", g.Pix, ycc.Y, g.Stride, ycc.YStride, sw, sh, dw, dh, ew, eh})
			} else {
				y, ok := gm.(*image.YCbCr)
				if !ok || y.SubsampleRatio != image.YCbCrSubsampleRatio444 {
					t.Fatalf("ratio %v: %T", r, gm)
				}
				hd, vd, _ := subsampling(r)
				csw, csh, cdw, cdh := (sw+hd-1)/hd, (sh+vd-1)/vd, dw, dh
				cew, ceh := ew/float64(hd), eh/float64(vd)
				ps = append(ps, testPlane{"Y", y.Y, ycc.Y, y.YStride, ycc.YStride, sw, sh, dw, dh, ew, eh},
					testPlane{"Cb", y.Cb, ycc.Cb, y.CStride, ycc.CStride, csw, csh, cdw, cdh, cew, ceh},
					testPlane{"Cr", y.Cr, ycc.Cr, y.CStride, ycc.CStride, csw, csh, cdw, cdh, cew, ceh})
			}
			for _, p := range ps {
				want := refPlane(p.src, p.stride, p.sw, p.sh, p.dw, p.dh, p.ew, p.eh)
				for yy := 0; yy < p.dh; yy++ {
					for xx := 0; xx < p.dw; xx++ {
						g, w := int(p.got[yy*p.gstride+xx]), int(want[yy*p.dw+xx])
						if g-w > 1 || w-g > 1 {
							t.Fatalf("ratio %v %v plane %s (%d,%d): %d, want %d", r, sz, p.name, xx, yy, g, w)
						}
					}
				}
			}
			if env.Budget.Used() != 0 {
				t.Errorf("ratio %v %v: %d bytes still reserved", r, sz, env.Budget.Used())
			}
		}
	}

	// Within the gamut, against the RGBA scale (x/image's arithmetic).
	m := image.NewYCbCr(image.Rect(0, 0, 400, 300), image.YCbCrSubsampleRatio444)
	for y := 0; y < 300; y++ {
		for x := 0; x < 400; x++ {
			m.Y[m.YOffset(x, y)] = uint8(128 + 50*math.Sin(float64(x)/13+float64(y)/29))
			c := m.COffset(x, y)
			m.Cb[c] = uint8(128 + 15*math.Cos(float64(x)/41))
			m.Cr[c] = uint8(128 + 15*math.Sin(float64(y)/53))
		}
	}
	got, err := ScalePlanes(testutil.Chopped(m, 16), 150, 111, draw.CatmullRom, 400, 300, image.YCbCrSubsampleRatio444, false)
	if err != nil {
		t.Fatal(err)
	}
	gm, err := pipeline.Collect(got)
	if err != nil {
		t.Fatal(err)
	}
	want, err := Scale(testutil.Chopped(m, 16), 150, 111, draw.CatmullRom)
	if err != nil {
		t.Fatal(err)
	}
	wm, err := pipeline.Collect(want)
	if err != nil {
		t.Fatal(err)
	}
	var sum float64
	for y := 0; y < 111; y++ {
		for x := 0; x < 150; x++ {
			g := color.RGBAModel.Convert(gm.At(x, y)).(color.RGBA)
			w := color.RGBAModel.Convert(wm.At(x, y)).(color.RGBA)
			sum += math.Abs(float64(g.R)-float64(w.R)) + math.Abs(float64(g.G)-float64(w.G)) + math.Abs(float64(g.B)-float64(w.B))
		}
	}
	if mean := sum / (3 * 150 * 111); mean > 1 {
		t.Errorf("against the RGBA scale: mean difference %.2f", mean)
	}
}
