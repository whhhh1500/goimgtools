package tests

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"math"
	"testing"

	. "github.com/whhhh1500/goimgtools"
)

// ditheredAnimation makes an opaque animation of n whole frames like a
// dithered banner: a picture of fine noise over a gradient, which stays,
// with two patches that change in every frame, one in each corner. The
// rectangle that changes is nearly the whole picture; most of what is in
// it does not.
func ditheredAnimation(w, h, n int) *gif.GIF {
	pal := make(color.Palette, 0, 64)
	for i := 0; i < 64; i++ {
		pal = append(pal, color.RGBA{uint8(4 * i), uint8(3*i + 40), uint8(255 - 3*i), 255})
	}
	noise := func(x, y, k int) int {
		v := uint32(x)*2654435761 ^ uint32(y)*40503 ^ uint32(k)*2246822519
		v ^= v >> 13
		v *= 0x5bd1e995
		return int(v >> 15 & 7)
	}
	g := &gif.GIF{Config: image.Config{Width: w, Height: h, ColorModel: pal}}
	a, b := image.Rect(4, 4, 44, 36), image.Rect(w-52, h-40, w-6, h-6)
	for k := 0; k < n; k++ {
		m := image.NewPaletted(image.Rect(0, 0, w, h), pal)
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				v := 8 + 40*x/w + noise(x, y, 0)
				if p := image.Pt(x, y); p.In(a) || p.In(b) {
					v = 4 + (3*k+x/4+y/4)%40 + noise(x, y, k+1)
				}
				m.Pix[y*m.Stride+x] = uint8(v)
			}
		}
		g.Image = append(g.Image, m)
		g.Delay = append(g.Delay, 4)
		g.Disposal = append(g.Disposal, gif.DisposalNone)
	}
	return g
}

// rgbaOf copies a shown frame for framePSNR.
func rgbaOf(m *image.NRGBA) *image.RGBA {
	out := image.NewRGBA(m.Rect)
	for y := m.Rect.Min.Y; y < m.Rect.Max.Y; y++ {
		for x := m.Rect.Min.X; x < m.Rect.Max.X; x++ {
			out.Set(x, y, m.At(x, y))
		}
	}
	return out
}

// frameKinds tells, for each frame of an animated WebP, what its image is:
// "VP8 " (lossy), "ALPH" (lossy with transparency) or "VP8L" (lossless).
func frameKinds(frames []webpFrame) []string {
	var kinds []string
	for _, f := range frames {
		kinds = append(kinds, string(f.chunks[:4]))
	}
	return kinds
}

// TestAnimatedWebPLossyBlocks: a lossy frame leaves out the blocks that
// stay as they are: they are transparent in it, and it is blended onto the
// picture. What is shown is as near the source as a whole frame is, what
// did not change is not touched, and the file is a fraction of the one
// that codes every rectangle whole.
func TestAnimatedWebPLossyBlocks(t *testing.T) {
	g := ditheredAnimation(240, 160, 10)
	in := encodeAll(t, g)
	want := coalesce(g)
	out, err := FromBytes(in).Animated().WebP(WebPOptions{Quality: 75}).ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, frames := readWebPAnimation(t, out)
	shown, _, _ := playWebP(t, out)
	if len(frames) != len(want) {
		t.Fatalf("%d frames, want %d", len(frames), len(want))
	}
	first := len(frames[0].chunks)
	if frames[0].blend || string(frames[0].chunks[:4]) != "VP8 " {
		t.Errorf("the first frame: blend %v, %q", frames[0].blend, frames[0].chunks[:4])
	}
	for i, f := range frames[1:] {
		// Blended, with the mask of what it leaves out, and far smaller
		// than the whole picture though its rectangle is most of it.
		if !f.blend || string(f.chunks[:4]) != "ALPH" || f.rect.Dx() < 200 || f.rect.Dy() < 120 || len(f.chunks) > first/3 {
			t.Errorf("frame %d: %v, blend %v, %q, %d bytes (the first frame %d)", i+1, f.rect, f.blend, f.chunks[:4], len(f.chunks), first)
		}
	}
	p0 := framePSNR(rgbaOf(shown[0]), want[0])
	still := image.Rect(64, 48, 176, 112) // away from both patches
	for i := range shown {
		if p := framePSNR(rgbaOf(shown[i]), want[i]); p < p0-1 {
			t.Errorf("frame %d: PSNR %.1f, the first frame %.1f", i, p, p0)
		}
		for y := still.Min.Y; y < still.Max.Y; y++ {
			for x := still.Min.X; x < still.Max.X; x++ {
				if shown[i].NRGBAAt(x, y) != shown[0].NRGBAAt(x, y) {
					t.Fatalf("frame %d: the pixel at %d,%d, which never changes, was coded again", i, x, y)
				}
			}
		}
	}
	t.Logf("%d bytes for %d frames, the first %d; PSNR of the first %.1f", len(out), len(frames), first, p0)
	if len(out) > 3*first {
		t.Errorf("%d bytes for %d frames, the first %d", len(out), len(frames), first)
	}

	// The same file whatever the memory limit and the number of threads.
	for _, c := range []struct {
		limit   int64
		threads int
	}{{8 << 20, 1}, {0, 4}} {
		got, err := FromBytes(in).MemoryLimit(c.limit).TempDir(t.TempDir()).Threads(c.threads).Animated().WebP(WebPOptions{Quality: 75}).ToBytes()
		if err != nil || !bytes.Equal(got, out) {
			t.Errorf("%+v: another file (%v)", c, err)
		}
	}
	// From effort 7 every frame is written each way; the result plays
	// alike and is no larger.
	all, err := FromBytes(in).Animated().WebP(WebPOptions{Quality: 75, Effort: 7}).ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	shown, _, _ = playWebP(t, all)
	for i := range shown {
		if p := framePSNR(rgbaOf(shown[i]), want[i]); p < p0-1 {
			t.Errorf("effort 7 frame %d: PSNR %.1f", i, p)
		}
	}
	if len(all) > len(out) {
		t.Errorf("effort 7: %d bytes, the default %d", len(all), len(out))
	}

	// A pan over a picture of smooth colour: everything moves, a few
	// blocks here and there stay near enough. Every frame is written, each
	// the way it came out smallest (whole, mostly: leaving out scattered
	// blocks of a picture that costs little does not pay), and plays right.
	pan := &gif.GIF{Config: image.Config{Width: 200, Height: 120}}
	pal := make(color.Palette, 256)
	for i := range pal {
		pal[i] = color.RGBA{uint8(i), uint8(i), uint8(255 - i/2), 255}
	}
	for k := 0; k < 6; k++ {
		m := image.NewPaletted(image.Rect(0, 0, 200, 120), pal)
		for y := 0; y < 120; y++ {
			for x := 0; x < 200; x++ {
				m.Pix[y*m.Stride+x] = uint8(128 + 90*math.Sin(float64(x+10*k)/23)*math.Cos(float64(y)/17))
			}
		}
		pan.Image = append(pan.Image, m)
		pan.Delay = append(pan.Delay, 5)
		pan.Disposal = append(pan.Disposal, gif.DisposalNone)
	}
	out, err = FromBytes(encodeAll(t, pan)).Animated().WebP(WebPOptions{Quality: 75}).ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, frames = readWebPAnimation(t, out)
	if len(frames) != 6 {
		t.Fatalf("pan: %d frames", len(frames))
	}
	t.Logf("pan: frames %v", frameKinds(frames))
	shown, _, _ = playWebP(t, out)
	for i, w := range coalesce(pan) {
		if p := framePSNR(rgbaOf(shown[i]), w); p < 36 {
			t.Errorf("pan frame %d: PSNR %.1f", i, p)
		}
	}
}

// TestAnimatedWebPLossyDrift: a picture that changes a little in every
// frame, each time by less than a lossy frame takes for a change, is not
// left to drift from what is shown: what a frame is compared with is the
// picture as it was last coded, not the frame before. A fade of a level a
// frame is written every few frames, and so it is when a busy patch has
// every frame written: the rest of the picture, left out of those frames,
// is still coded again once it is too far off. (Compared with the frame
// before it never would be, and the last frame would be shown 39 levels
// off.)
func TestAnimatedWebPLossyDrift(t *testing.T) {
	const w, h, n = 160, 96, 40
	pal := make(color.Palette, 256)
	for i := range pal {
		pal[i] = color.RGBA{uint8(i), uint8(i), uint8(i), 255}
	}
	patch := image.Rect(120, 8, 152, 40)
	fade := func(busy bool) []byte {
		g := &gif.GIF{Config: image.Config{Width: w, Height: h, ColorModel: pal}}
		for k := 0; k < n; k++ {
			m := image.NewPaletted(image.Rect(0, 0, w, h), pal)
			for y := 0; y < h; y++ {
				for x := 0; x < w; x++ {
					v := 60 + 100*x/w + k // a level brighter each frame
					if busy && image.Pt(x, y).In(patch) {
						v = 40 + (37*k+(x/4)*29+(y/4)*53)%160
					}
					m.Pix[y*m.Stride+x] = uint8(v)
				}
			}
			g.Image = append(g.Image, m)
			g.Delay = append(g.Delay, 4)
			g.Disposal = append(g.Disposal, gif.DisposalNone)
		}
		return encodeAll(t, g)
	}
	for _, busy := range []bool{false, true} {
		in := fade(busy)
		for _, o := range []WebPOptions{{Quality: 75}, {Quality: 75, Effort: 7}, {Quality: 75, Mixed: true}, {Quality: 30}} {
			what := fmt.Sprintf("busy %v, quality %d, effort %d, mixed %v", busy, o.Quality, o.Effort, o.Mixed)
			out, err := FromBytes(in).Animated().WebP(o).ToBytes()
			if err != nil {
				t.Fatal(err)
			}
			shown, ms, _ := playWebP(t, out)
			// At quality 75 a channel may be 5 levels off before it counts
			// as changed (15 at quality 30): the fade alone is written
			// every few frames, with the patch every frame is.
			total := 0
			for _, d := range ms {
				total += d
			}
			switch {
			case total != 40*n:
				t.Fatalf("%s: %d ms, want %d", what, total, 40*n)
			case busy && len(shown) != n:
				t.Fatalf("%s: %d frames, want %d", what, len(shown), n)
			case !busy && (len(shown) < n/20 || len(shown) > n/2):
				t.Fatalf("%s: %d frames, want a frame every few of %d", what, len(shown), n)
			}
			allow := 5
			if o.Quality == 30 {
				allow = 15
			}
			at, end, most := 0, ms[0], 0
			for k := 0; k < n; k++ {
				for 40*k >= end {
					at++
					end += ms[at]
				}
				worst := 0
				for y := 0; y < h; y++ {
					for x := 0; x < w; x++ {
						// (Not the patch and what is next to it, which
						// are as far off as coding a pattern takes them.)
						if busy && image.Pt(x, y).In(patch.Inset(-8)) {
							continue
						}
						d := int(shown[at].NRGBAAt(x, y).G) - (60 + 100*x/w + k)
						worst = max(worst, d, -d)
					}
				}
				// What is shown is at most the allowance behind, less
				// what coding it took off (a smooth picture: little).
				if worst > allow+6 {
					t.Errorf("%s: frame %d is shown %d levels off", what, k, worst)
				}
				most = max(most, worst)
			}
			t.Logf("%s: %d frames written, at most %d levels off", what, len(shown), most)
		}
	}
}

// TestAnimatedWebPMixed: with Mixed, the frames of a lossy animation are
// lossless where that is smaller: a drawing of flat colours, which lossy
// frames code badly, comes out smaller and exact, and a dithered picture
// keeps its lossy frames.
func TestAnimatedWebPMixed(t *testing.T) {
	// A drawing: flat areas, a bar that grows, a box that moves.
	pal := color.Palette{color.RGBA{250, 250, 250, 255}, color.RGBA{30, 60, 200, 255}, color.RGBA{220, 40, 40, 255}, color.RGBA{20, 20, 20, 255}, color.RGBA{240, 200, 40, 255}}
	g := &gif.GIF{Config: image.Config{Width: 200, Height: 120, ColorModel: pal}}
	for k := 0; k < 8; k++ {
		m := image.NewPaletted(image.Rect(0, 0, 200, 120), pal)
		for y := 0; y < 120; y++ {
			for x := 0; x < 200; x++ {
				var v uint8
				switch {
				case y < 16:
					v = 1
				case y >= 100 && y < 108 && x >= 10 && x < 10+22*(k+1):
					v = 2
				case x >= 20+15*k && x < 60+15*k && y >= 40 && y < 80:
					v = 4
					if x == 20+15*k || x == 59+15*k || y == 40 || y == 79 {
						v = 3
					}
				}
				m.Pix[y*m.Stride+x] = v
			}
		}
		g.Image = append(g.Image, m)
		g.Delay = append(g.Delay, 8)
		g.Disposal = append(g.Disposal, gif.DisposalNone)
	}
	in := encodeAll(t, g)
	want := coalesce(g)
	lossy, err := FromBytes(in).Animated().WebP(WebPOptions{Quality: 75}).ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	mixed, err := FromBytes(in).Animated().WebP(WebPOptions{Quality: 75, Mixed: true}).ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, frames := readWebPAnimation(t, mixed)
	for i, kind := range frameKinds(frames) {
		if kind != "VP8L" {
			t.Errorf("drawing: frame %d is %q", i, kind)
		}
	}
	t.Logf("drawing: %d bytes mixed, %d lossy", len(mixed), len(lossy))
	if len(mixed) > len(lossy)/2 {
		t.Errorf("drawing: %d bytes mixed, %d lossy", len(mixed), len(lossy))
	}
	shown, ms, _ := playWebP(t, mixed)
	if len(shown) != len(want) || fmt.Sprint(ms) != fmt.Sprint([]int{80, 80, 80, 80, 80, 80, 80, 80}) {
		t.Fatalf("drawing: %d frames, %v ms", len(shown), ms)
	}
	for i := range shown {
		sameImage(t, fmt.Sprintf("drawing frame %d", i), shown[i], want[i])
	}

	// Lossless output is what it is without Mixed.
	a, err := FromBytes(in).Animated().WebP(WebPOptions{Lossless: true}).ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	b, err := FromBytes(in).Animated().WebP(WebPOptions{Lossless: true, Mixed: true}).ToBytes()
	if err != nil || !bytes.Equal(a, b) {
		t.Errorf("Mixed changed a lossless animation (%v)", err)
	}

	// A dithered picture: lossy frames stay where they are smaller, and
	// the file is no larger than without Mixed at the effort that tries
	// every way as Mixed does.
	d := ditheredAnimation(240, 160, 6)
	in = encodeAll(t, d)
	want = coalesce(d)
	all, err := FromBytes(in).Animated().WebP(WebPOptions{Quality: 75, Effort: 7}).ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	mixed, err = FromBytes(in).Animated().WebP(WebPOptions{Quality: 75, Effort: 7, Mixed: true}).ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	if len(mixed) > len(all) {
		t.Errorf("dithered: %d bytes mixed, %d lossy", len(mixed), len(all))
	}
	_, _, _, frames = readWebPAnimation(t, mixed)
	t.Logf("dithered: frames %v, %d bytes mixed, %d lossy", frameKinds(frames), len(mixed), len(all))
	shown, _, _ = playWebP(t, mixed)
	for i := range shown {
		if p := framePSNR(rgbaOf(shown[i]), want[i]); p < 25 {
			t.Errorf("dithered frame %d: PSNR %.1f", i, p)
		}
	}
}
