package vp8enc

import (
	"bytes"
	"image"
	"math"
	"math/rand/v2"
	"testing"

	"github.com/whhhh1500/goimgtools/internal/testutil"
)

// alphaImages are planes the filters and the lossless encoder have to get
// right: a smooth mask, a hard edged one, noise, and odd sizes with one row
// or one column.
func alphaImages() map[string]*image.NRGBA {
	r := rand.New(rand.NewPCG(8, 9))
	mk := func(w, h int, a func(x, y int) uint8) *image.NRGBA {
		m := image.NewNRGBA(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				copy(m.Pix[m.PixOffset(x, y):], []uint8{uint8(x * 3), uint8(y * 5), uint8(x ^ y), a(x, y)})
			}
		}
		return m
	}
	return map[string]*image.NRGBA{
		"smooth": mk(97, 61, func(x, y int) uint8 {
			d := math.Hypot(float64(x-48), float64(y-30)) / 40
			return uint8(max(0, min(255, (1.2-d)*400)))
		}),
		"hard":  mk(70, 45, func(x, y int) uint8 { return uint8(255 * ((x/7 + y/5) & 1)) }),
		"noise": mk(33, 29, func(x, y int) uint8 { return uint8(r.IntN(256)) }),
		"row":   mk(50, 1, func(x, y int) uint8 { return uint8(x * 5) }),
		"col":   mk(1, 40, func(x, y int) uint8 { return uint8(y * 6) }),
	}
}

// TestAlphaFilters codes each plane with each filter of the ALPH chunk and
// checks that it decodes to the same values.
func TestAlphaFilters(t *testing.T) {
	for name, m := range alphaImages() {
		b := m.Bounds()
		for method := 0; method < 4; method++ {
			data := mustEncode(t, testutil.Chopped(m, 7), 70, &debug{alphaMethod: method + 1})
			d := decodeX(t, data)
			if d.alpha == nil {
				t.Fatalf("%s filter %d: no alpha decoded", name, method)
			}
			for y := 0; y < b.Dy(); y++ {
				for x := 0; x < b.Dx(); x++ {
					if got, want := d.alpha[y*b.Dx()+x], m.Pix[m.PixOffset(x, y)+3]; got != want {
						t.Fatalf("%s filter %d: alpha at %d,%d is %d, want %d", name, method, x, y, got, want)
					}
				}
			}
			// (Noise is stored as it is: coded, it would be larger.)
			if f := data[bytes.Index(data, []byte("ALPH"))+8]; f != 0 && (int(f>>2&3) != method || f&3 != 1) {
				t.Fatalf("%s filter %d: ALPH header %#x", name, method, f)
			}
		}
	}
}

// TestCleanTransparent: what cannot be seen is made cheap. A picture with
// a transparent border full of noise codes much smaller, and its visible
// part as well as without.
func TestCleanTransparent(t *testing.T) {
	r := rand.New(rand.NewPCG(10, 11))
	m := photoLikeAlpha(160, 120, r)
	var size [2]int
	var psnr [2]float64
	for i, dbg := range []*debug{{noClean: true}, nil} {
		data := mustEncode(t, testutil.Chopped(m, 16), 75, dbg)
		size[i] = len(data)
		psnr[i] = visiblePSNR(m, toRGB(decodeX(t, data)))
	}
	t.Logf("kept: %d bytes, %.2f dB; cleaned: %d bytes, %.2f dB", size[0], psnr[0], size[1], psnr[1])
	if size[1] >= size[0]*4/5 {
		t.Errorf("cleaning saves too little: %d bytes against %d", size[1], size[0])
	}
	if psnr[1] < psnr[0]-0.3 {
		t.Errorf("visible pixels: %.2f dB cleaned, %.2f dB kept", psnr[1], psnr[0])
	}
}

// photoLikeAlpha is photoLike in a rounded window, outside of which the
// pixels are transparent and their colours noise.
func photoLikeAlpha(w, h int, r *rand.Rand) *image.NRGBA {
	m := photoLike(w, h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			o := m.PixOffset(x, y)
			if dx, dy := x-w/2, y-h/2; dx*dx*h*h+dy*dy*w*w > w*w*h*h/6 {
				m.Pix[o], m.Pix[o+1], m.Pix[o+2], m.Pix[o+3] = uint8(r.IntN(256)), uint8(r.IntN(256)), uint8(r.IntN(256)), 0
			}
		}
	}
	return m
}

// visiblePSNR is the RGB PSNR over the pixels of m that are not wholly
// transparent.
func visiblePSNR(m *image.NRGBA, rgb []uint8) float64 {
	b := m.Bounds()
	var sum float64
	n := 0
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			o := m.PixOffset(x, y)
			if m.Pix[o+3] == 0 {
				continue
			}
			for c := 0; c < 3; c++ {
				e := float64(m.Pix[o+c]) - float64(rgb[3*(y*b.Dx()+x)+c])
				sum += e * e
				n++
			}
		}
	}
	return 10 * math.Log10(255*255*float64(n)/sum)
}
