package vp8lenc

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"math/rand"
	"testing"

	"golang.org/x/image/webp"

	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// cutOut is photoLike with an alpha channel: transparent around a soft
// edged disc, noise under the transparent pixels (as photographs cut out
// in an editor keep it).
func cutOut(w, h int) *image.NRGBA {
	m := photoLike(w, h)
	r := rand.New(rand.NewSource(8))
	cx, cy, rad := float64(w)/2, float64(h)/2, float64(min(w, h))*0.4
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dx, dy := float64(x)-cx, float64(y)-cy
			d := rad - (dx*dx+dy*dy)/rad // > 0 inside
			a := 255
			switch {
			case d <= 0:
				a = 0
			case d < 12:
				a = int(d * 255 / 12)
			}
			i := m.PixOffset(x, y)
			m.Pix[i+3] = uint8(a)
			if a == 0 {
				m.Pix[i], m.Pix[i+1], m.Pix[i+2] = uint8(r.Intn(256)), uint8(r.Intn(256)), uint8(r.Intn(256))
			}
		}
	}
	return m
}

func decodeNRGBA(t *testing.T, data []byte) *image.NRGBA {
	t.Helper()
	got, err := webp.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	g := image.NewNRGBA(got.Bounds())
	draw.Draw(g, g.Bounds(), got, got.Bounds().Min, draw.Src)
	return g
}

// TestNearLossless: every channel comes back within half the largest
// rounding; alpha 0 and 255 exactly; with Clean the colour of what is
// transparent after decoding is free, without it the colour of what was
// transparent is kept; gray stays gray; and the file is smaller.
func TestNearLossless(t *testing.T) {
	gray := image.NewGray(image.Rect(0, 0, 211, 157))
	p := photoLike(211, 157)
	for i := range gray.Pix {
		gray.Pix[i] = p.Pix[4*i+1]
	}
	imgs := map[string]image.Image{"photo": photoLike(301, 233), "cut-out": cutOut(287, 241), "gray": gray}
	for _, name := range sorted(imgs) {
		m := imgs[name]
		src := want(m)
		lossless := len(encodeBytes(t, testutil.Chopped(m, 37), &Options{Clean: true}))
		for bits := 1; bits <= 5; bits++ {
			for _, clean := range []bool{false, true} {
				data := encodeBytes(t, testutil.Chopped(m, 37), &Options{NearLossless: bits, Clean: clean})
				g := decodeNRGBA(t, data)
				lim := 1 << bits >> 1
				worst := 0
				for i := 0; i < len(src.Pix); i += 4 {
					s, d := src.Pix[i:i+4:i+4], g.Pix[i:i+4:i+4]
					if a := s[3]; (a == 0 || a == 255) && d[3] != a {
						t.Fatalf("%s bits %d: alpha %d came back %d", name, bits, a, d[3])
					}
					if s[3] == 0 && !clean && !bytes.Equal(s, d) {
						t.Fatalf("%s bits %d: transparent %v came back %v without Clean", name, bits, s, d)
					}
					if name == "gray" && (d[0] != d[1] || d[1] != d[2]) {
						t.Fatalf("%s bits %d: %v is not gray", name, bits, d)
					}
					k0 := 0
					if d[3] == 0 && clean {
						k0 = 3 // its colour cannot be seen
					}
					for k := k0; k < 4; k++ {
						worst = max(worst, int(s[k])-int(d[k]), int(d[k])-int(s[k]))
					}
				}
				if worst > lim {
					t.Errorf("%s bits %d clean %v: a channel %d off, more than %d", name, bits, clean, worst, lim)
				}
				if clean && bits >= 2 && len(data) >= lossless {
					t.Errorf("%s bits %d: %d bytes, lossless %d", name, bits, len(data), lossless)
				}
			}
		}
	}
}

// TestNearLosslessSmooth: where no pixel differs from a neighbour by more
// than 2 levels (flat areas, gentle gradients), nothing changes.
func TestNearLosslessSmooth(t *testing.T) {
	m := image.NewNRGBA(image.Rect(0, 0, 300, 200))
	for y := 0; y < 200; y++ {
		for x := 0; x < 300; x++ {
			m.SetNRGBA(x, y, color.NRGBA{uint8(x / 3), uint8(100 + y/4), uint8((x + y) / 5), 255})
		}
	}
	data := encodeBytes(t, testutil.Chopped(m, 23), &Options{NearLossless: 5, Clean: true})
	checkFile(t, "smooth", data, m)
}

// TestClean: with Clean the colour under transparent pixels is not kept,
// everything visible is, and the file is much smaller when that colour is
// noise; without it every pixel is kept.
func TestClean(t *testing.T) {
	m := cutOut(320, 240)
	exact := roundTrip(t, "exact", m, 29, nil)
	data := encodeBytes(t, testutil.Chopped(m, 29), &Options{Clean: true})
	g := decodeNRGBA(t, data)
	for i := 0; i < len(m.Pix); i += 4 {
		if m.Pix[i+3] != 0 && !bytes.Equal(m.Pix[i:i+4], g.Pix[i:i+4]) {
			t.Fatalf("visible pixel %v came back %v", m.Pix[i:i+4], g.Pix[i:i+4])
		}
		if m.Pix[i+3] == 0 && g.Pix[i+3] != 0 {
			t.Fatalf("transparent pixel came back with alpha %d", g.Pix[i+3])
		}
	}
	if len(data) > len(exact)*3/4 {
		t.Errorf("clean %d bytes, exact %d", len(data), len(exact))
	}
	// Opaque images do not change.
	p := photoLike(200, 150)
	if a, b := encodeBytes(t, testutil.Chopped(p, 7), nil), encodeBytes(t, testutil.Chopped(p, 7), &Options{Clean: true}); !bytes.Equal(a, b) {
		t.Error("Clean changed an opaque image")
	}
}

// TestNearLosslessSame: the file does not depend on how the source is cut
// into strips, on the helpers, or on the memory, within what fits.
func TestNearLosslessSame(t *testing.T) {
	imgs := map[string]image.Image{"photo": photoLike(700, 523), "cut-out": cutOut(413, 377)}
	for _, name := range sorted(imgs) {
		m := imgs[name]
		for _, o := range []*Options{{NearLossless: 2, Clean: true}, {NearLossless: 3}, {Clean: true}, {NearLossless: 1, Clean: true, Effort: 9}} {
			ref := encodeBytes(t, testutil.Chopped(m, 16), o)
			for _, helpers := range []int{1, 3, 7} {
				env := pipeline.NewEnv(0, t.TempDir())
				env.SetHelpers(helpers)
				if got := encodeBytes(t, testutil.ChoppedEnv(m, 11, env), o); !bytes.Equal(got, ref) {
					t.Errorf("%s %+v with %d helpers: another file", name, *o, helpers)
				}
				if env.Budget.Used() != 0 {
					t.Errorf("%s: %d bytes reserved", name, env.Budget.Used())
				}
			}
		}
	}
	// A small budget: within the limit, and as near (the blocks of the
	// predictor may be larger: other pixels).
	const limit = 2 << 20
	m := photoLike(1500, 1500)
	o := &Options{NearLossless: 2, Clean: true}
	env := pipeline.NewEnv(limit, t.TempDir())
	got := decodeNRGBA(t, encodeBytes(t, testutil.ChoppedEnv(m, 32, env), o))
	if p := env.Budget.Peak(); p > limit {
		t.Fatalf("peak %d above the limit %d", p, limit)
	}
	if env.Budget.Used() != 0 {
		t.Fatalf("%d bytes of budget still reserved", env.Budget.Used())
	}
	leftovers(t, env.TempDir)
	for i := range m.Pix {
		if d := int(m.Pix[i]) - int(got.Pix[i]); d > 2 || d < -2 {
			t.Fatalf("small budget: a channel %d off", d)
		}
	}
}

// TestNearLosslessComponent: the rounding never crosses the boundary, ends
// on a multiple of q or halfway when it would have, and is at most q/2 off.
func TestNearLosslessComponent(t *testing.T) {
	for _, q := range []int{2, 4, 8, 16, 32} {
		for v := 0; v < 256; v++ {
			for p := 0; p < 256; p++ {
				res := nearLosslessComponent(uint8(v), uint8(p), 0xff, q)
				got := int(uint8(p) + res)
				if d := got - v; d > q/2 || d < -q/2 {
					t.Fatalf("q %d value %d predict %d: %d, off by %d", q, v, p, got, d)
				}
				if int(res)%(q/2) != 0 {
					t.Fatalf("q %d value %d predict %d: residual %d", q, v, p, res)
				}
			}
		}
	}
	// Errors are bounded for whole pixels too, with subtract green.
	r := rand.New(rand.NewSource(1))
	for range 100000 {
		v, p := r.Uint32(), r.Uint32()
		for _, sg := range []bool{false, true} {
			val, pred := v, p
			if sg {
				val, pred = subGreen(v), subGreen(p)
			}
			rec := addPixels(pred, nearLossless(val, pred, 8, 100, sg, false))
			if sg {
				rec = addGreen(rec)
			}
			if d := maxDiff(rec, v); d > 4 {
				t.Fatalf("%08x predicted %08x (sg %v): %08x, %d off", v, p, sg, rec, d)
			}
		}
	}
}

// TestNearLosslessPattern: on a pattern whose residuals repeat exactly,
// which rounding would break (86 bytes against 1176), the lossless variant
// that runs beside the near-lossless ones wins.
func TestNearLosslessPattern(t *testing.T) {
	pal := color.Palette{}
	for i := 0; i < 256; i++ {
		pal = append(pal, color.RGBA{uint8(i), uint8(255 - i), uint8(i * 7), 255})
	}
	p := image.NewPaletted(image.Rect(0, 0, 120, 90), pal)
	for i := range p.Pix {
		p.Pix[i] = uint8((i%120)*2 + i/120 + i%7*9)
	}
	ll := encodeBytes(t, testutil.Chopped(p, 16), &Options{Clean: true})
	nl := encodeBytes(t, testutil.Chopped(p, 16), &Options{NearLossless: 2, Clean: true})
	if len(nl) > len(ll) {
		t.Errorf("near-lossless %d bytes, lossless %d", len(nl), len(ll))
	}
}
