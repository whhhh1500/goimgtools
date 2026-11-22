package tests

import (
	"bytes"
	"image"
	"image/color/palette"
	"image/draw"
	"image/gif"
	"testing"

	. "github.com/whhhh1500/goimgtools"
)

// TestGIFEffort: GIFOptions.Effort 9 is what no effort given writes, to
// the byte; lower efforts write a file that differs, of about the size,
// whose frames are within a few levels of effort 9's on average; for a
// still image too; an effort beyond 1 to 9 is refused.
func TestGIFEffort(t *testing.T) {
	// An animation of photographs: six frames, the picture moving.
	photo := photoLike(260, 180)
	g := &gif.GIF{}
	for k := 0; k < 6; k++ {
		p := image.NewPaletted(image.Rect(0, 0, 200, 150), palette.Plan9)
		draw.FloydSteinberg.Draw(p, p.Rect, photo, image.Pt(10*k, 5*k))
		g.Image = append(g.Image, p)
		g.Delay = append(g.Delay, 8)
	}
	var in bytes.Buffer
	if err := gif.EncodeAll(&in, g); err != nil {
		t.Fatal(err)
	}
	anim := func(o GIFOptions) []byte {
		t.Helper()
		return must(FromBytes(in.Bytes()).Animated().Resize(120, 0).GIF(o).ToBytes())
	}
	// meanDiff is how far the frames of two animations are from each
	// other on average, in levels of a channel.
	meanDiff := func(a, b []byte) float64 {
		t.Helper()
		ga, ea := gif.DecodeAll(bytes.NewReader(a))
		gb, eb := gif.DecodeAll(bytes.NewReader(b))
		if ea != nil || eb != nil || len(ga.Image) != len(gb.Image) {
			t.Fatalf("frames: %v, %v", ea, eb)
		}
		fa, fb := coalesce(ga), coalesce(gb)
		var sum, n float64
		for i := range fa {
			for j := 0; j < len(fa[i].Pix); j += 4 {
				for c := 0; c < 3; c++ {
					d := float64(fa[i].Pix[j+c]) - float64(fb[i].Pix[j+c])
					if d < 0 {
						d = -d
					}
					sum += d
					n++
				}
			}
		}
		return sum / n
	}
	full := anim(GIFOptions{})
	if nine := anim(GIFOptions{Effort: 9}); !bytes.Equal(nine, full) {
		t.Error("effort 9 is not what no effort writes")
	}
	prev := full
	for _, e := range []int{8, 5, 1} {
		out := anim(GIFOptions{Effort: e})
		if bytes.Equal(out, prev) {
			t.Errorf("effort %d writes what the effort above writes", e)
		}
		// (Not always larger: another palette codes a little differently.)
		if r := float64(len(out)) / float64(len(full)); r < 0.95 || r > 1.05 {
			t.Errorf("effort %d: %d bytes, effort 9 writes %d", e, len(out), len(full))
		}
		if d := meanDiff(out, full); d > 3 {
			t.Errorf("effort %d: its frames are %.2f levels from effort 9's on average", e, d)
		}
		prev = out
	}

	// A still image: the palette's refining is all an effort changes.
	still := func(o GIFOptions) []byte { return must(FromImage(photo).GIF(o).ToBytes()) }
	s9 := still(GIFOptions{})
	if !bytes.Equal(still(GIFOptions{Effort: 9}), s9) || !bytes.Equal(still(GIFOptions{Effort: 7}), s9) {
		t.Error("a still image at efforts 7 and 9 is not what no effort writes")
	}
	s5, s1 := still(GIFOptions{Effort: 5}), still(GIFOptions{Effort: 1})
	if bytes.Equal(s5, s9) || bytes.Equal(s1, s5) {
		t.Error("a still image is the same at efforts 9, 5 and 1")
	}
	for _, s := range [][]byte{s5, s1} {
		if _, err := gif.Decode(bytes.NewReader(s)); err != nil {
			t.Errorf("a still image at a low effort: %v", err)
		}
	}
	// A fixed palette has nothing to refine.
	if !bytes.Equal(still(GIFOptions{FixedPalette: true, Effort: 1}), still(GIFOptions{FixedPalette: true})) {
		t.Error("the effort changed a file with the fixed palette")
	}

	for _, e := range []int{-1, 10} {
		if _, err := FromImage(photo).GIF(GIFOptions{Effort: e}).ToBytes(); err == nil {
			t.Errorf("a still image at effort %d: written", e)
		}
		if _, err := FromBytes(in.Bytes()).Animated().GIF(GIFOptions{Effort: e}).ToBytes(); err == nil {
			t.Errorf("an animation at effort %d: written", e)
		}
	}
}
