package tests

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"math/rand"
	"testing"

	. "github.com/whhhh1500/goimgtools"
)

// noisyPhoto is a picture with gradients, edges and noise, and with alpha
// it is a cut-out: transparent outside a disc, noise under the
// transparency.
func noisyPhoto(w, h int, cut bool) *image.NRGBA {
	r := rand.New(rand.NewSource(11))
	m := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := x*200/w + y*55/h
			if (x/37+y/23)%3 == 0 {
				v = 255 - v
			}
			n := r.Intn(9)
			c := color.NRGBA{uint8(v + n), uint8(v*3/4 + n), uint8(255 - v + r.Intn(5)), 255}
			if cut {
				dx, dy := x-w/2, y-h/2
				if dx*dx+dy*dy > (h*2/5)*(h*2/5) {
					c = color.NRGBA{uint8(r.Intn(256)), uint8(r.Intn(256)), uint8(r.Intn(256)), 0}
				}
			}
			m.SetNRGBA(x, y, c)
		}
	}
	return m
}

func worstDiff(t *testing.T, a, b *image.NRGBA) int {
	t.Helper()
	if a.Rect.Size() != b.Rect.Size() {
		t.Fatalf("size %v, want %v", b.Rect.Size(), a.Rect.Size())
	}
	worst := 0
	for i := 0; i < len(a.Pix); i += 4 {
		k0 := 0
		if a.Pix[i+3] == 0 || b.Pix[i+3] == 0 {
			k0 = 3 // a colour that cannot be seen
		}
		for k := k0; k < 4; k++ {
			worst = max(worst, int(a.Pix[i+k])-int(b.Pix[i+k]), int(b.Pix[i+k])-int(a.Pix[i+k]))
		}
	}
	return worst
}

func decodeNRGBA(t *testing.T, b []byte) *image.NRGBA {
	t.Helper()
	m, err := FromBytes(b).ToImage()
	if err != nil {
		t.Fatal(err)
	}
	return nrgbaOf(m)
}

// TestWebPNearLossless: Quality 100 is the lossless file; below it every
// channel comes back within the bound of its level, the file is smaller
// than lossless and the same with any number of threads;
// animations take it too; MaxFileSize lowers the level; a quality out of
// range is an error.
func TestWebPNearLossless(t *testing.T) {
	photo := noisyPhoto(320, 240, false)
	in := pngBytes(t, photo)
	ll := must(FromBytes(in).WebP(WebPOptions{Lossless: true}).ToBytes())
	if b := must(FromBytes(in).WebP(WebPOptions{NearLossless: true, Quality: 100}).ToBytes()); !bytes.Equal(b, ll) {
		t.Error("NearLossless at quality 100 is not the lossless file")
	}
	if b := must(FromBytes(in).WebP(WebPOptions{NearLossless: true, Lossless: true, Quality: 60}).ToBytes()); bytes.Equal(b, ll) {
		t.Error("NearLossless with Lossless gave the lossless file")
	}
	for _, c := range []struct{ q, lim int }{{0, 2}, {90, 1}, {60, 2}, {50, 4}, {30, 8}, {1, 16}} {
		b := must(FromBytes(in).WebP(WebPOptions{NearLossless: true, Quality: c.q}).ToBytes())
		if d := worstDiff(t, photo, decodeNRGBA(t, b)); d > c.lim {
			t.Errorf("quality %d: a channel %d off, more than %d", c.q, d, c.lim)
		}
		// Coarser is not always smaller (on noise it can be larger), but it
		// is smaller than lossless.
		if len(b) >= len(ll) {
			t.Errorf("quality %d: %d bytes, lossless %d", c.q, len(b), len(ll))
		}
		if b4 := must(FromBytes(in).Threads(4).WebP(WebPOptions{NearLossless: true, Quality: c.q}).ToBytes()); !bytes.Equal(b4, b) {
			t.Errorf("quality %d: another file with 4 threads", c.q)
		}
	}
	// Animated: every frame within the bound.
	pal := color.Palette{}
	for i := 0; i < 256; i++ {
		pal = append(pal, color.RGBA{uint8(i), uint8(255 - i), uint8(i * 7), 255})
	}
	g := &gif.GIF{Config: image.Config{Width: 120, Height: 90}}
	for f := 0; f < 3; f++ {
		p := image.NewPaletted(image.Rect(0, 0, 120, 90), pal)
		for i := range p.Pix {
			p.Pix[i] = uint8((i%120)*2 + i/120 + f*40 + i%7*9)
		}
		g.Image, g.Delay, g.Disposal = append(g.Image, p), append(g.Delay, 10), append(g.Disposal, gif.DisposalNone)
	}
	src := encodeAll(t, g)
	anim := must(FromBytes(src).Animated().WebP(WebPOptions{NearLossless: true, Quality: 60}).ToBytes())
	animLL := must(FromBytes(src).Animated().WebP(WebPOptions{Lossless: true}).ToBytes())
	if len(anim) > len(animLL) {
		t.Errorf("animation: %d bytes, lossless %d", len(anim), len(animLL))
	}
	shown, _, _ := playWebP(t, anim)
	want := coalesce(g)
	if len(shown) != len(want) {
		t.Fatalf("animation: %d frames, want %d", len(shown), len(want))
	}
	for i := range want {
		if d := worstDiff(t, nrgbaOf(want[i]), shown[i]); d > 2 {
			t.Errorf("animation frame %d: a channel %d off", i, d)
		}
	}
	// MaxFileSize goes down the levels.
	limit := int64(len(ll)) * 85 / 100
	b, err := FromBytes(in).WebP(WebPOptions{NearLossless: true}).MaxFileSize(limit).ToBytes()
	if err != nil || int64(len(b)) > limit {
		t.Errorf("MaxFileSize(%d): %d bytes, %v", limit, len(b), err)
	}
	for _, q := range []int{-1, 101} {
		if _, err := FromBytes(in).WebP(WebPOptions{NearLossless: true, Quality: q}).ToBytes(); err == nil {
			t.Errorf("quality %d: no error", q)
		}
	}
}

// TestWebPExact: by default the colour under fully transparent pixels is
// not kept, which makes a cut-out much smaller, and everything visible is;
// with Exact every pixel is kept, lossless, and lossy output keeps more.
func TestWebPExact(t *testing.T) {
	cut := noisyPhoto(320, 240, true)
	in := pngBytes(t, cut)
	def := must(FromBytes(in).WebP(WebPOptions{Lossless: true}).ToBytes())
	exact := must(FromBytes(in).WebP(WebPOptions{Lossless: true, Exact: true}).ToBytes())
	if len(def) > len(exact)*3/4 {
		t.Errorf("lossless: %d bytes, %d with Exact", len(def), len(exact))
	}
	if d := worstDiff(t, cut, decodeNRGBA(t, def)); d != 0 {
		t.Errorf("lossless: a visible channel %d off", d)
	}
	got := decodeNRGBA(t, exact)
	if !bytes.Equal(got.Pix, cut.Pix) {
		t.Error("lossless with Exact: pixels changed")
	}
	lossy := must(FromBytes(in).WebP(WebPOptions{Quality: 80}).ToBytes())
	lossyExact := must(FromBytes(in).WebP(WebPOptions{Quality: 80, Exact: true}).ToBytes())
	if len(lossy) >= len(lossyExact) {
		t.Errorf("lossy: %d bytes, %d with Exact", len(lossy), len(lossyExact))
	}
	// Opaque images are the same either way.
	op := pngBytes(t, noisyPhoto(200, 100, false))
	for _, o := range []WebPOptions{{Lossless: true}, {Quality: 80}} {
		a := must(FromBytes(op).WebP(o).ToBytes())
		o.Exact = true
		if b := must(FromBytes(op).WebP(o).ToBytes()); !bytes.Equal(a, b) {
			t.Errorf("%+v: Exact changed an opaque image", o)
		}
	}
}

// TestWebPAlphaQuality: the transparency of lossy output is reduced to
// fewer levels, fully transparent and fully opaque pixels kept, for a
// smaller file; 0 and 100 keep it exactly; out of range is an error.
func TestWebPAlphaQuality(t *testing.T) {
	m := noisyPhoto(320, 240, false)
	for y := 0; y < 240; y++ {
		for x := 0; x < 320; x++ {
			dx, dy := float64(x-160)/150, float64(y-120)/110
			a := (1 - (dx*dx + dy*dy)) * 400
			m.Pix[m.PixOffset(x, y)+3] = uint8(max(0, min(255, a)))
		}
	}
	in := pngBytes(t, m)
	exact := must(FromBytes(in).WebP(WebPOptions{Quality: 80}).ToBytes())
	if b := must(FromBytes(in).WebP(WebPOptions{Quality: 80, AlphaQuality: 100}).ToBytes()); !bytes.Equal(b, exact) {
		t.Error("AlphaQuality 100 is not the default")
	}
	low := must(FromBytes(in).WebP(WebPOptions{Quality: 80, AlphaQuality: 30}).ToBytes())
	if len(low) >= len(exact) {
		t.Errorf("AlphaQuality 30: %d bytes, %d exact", len(low), len(exact))
	}
	got := decodeNRGBA(t, low)
	levels := map[uint8]bool{}
	for i := 3; i < len(m.Pix); i += 4 {
		levels[got.Pix[i]] = true
		if a := m.Pix[i]; (a == 0 || a == 255) && got.Pix[i] != a {
			t.Fatalf("alpha %d came back %d", a, got.Pix[i])
		}
	}
	if len(levels) > 8 { // 2 + 30/5
		t.Errorf("%d levels of alpha", len(levels))
	}
	if _, err := FromBytes(in).WebP(WebPOptions{AlphaQuality: 101}).ToBytes(); err == nil {
		t.Error("AlphaQuality 101: no error")
	}
}
