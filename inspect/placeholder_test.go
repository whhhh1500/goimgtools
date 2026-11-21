package inspect

import (
	"encoding/base64"
	"image"
	"image/color"
	"testing"
)

// phImages are small pictures for the placeholders: a colour gradient, the
// same in a soft-edged transparent window, a tall and a wide one.
func phImages() map[string]*image.NRGBA {
	mk := func(w, h int, alpha bool) *image.NRGBA {
		m := image.NewNRGBA(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				a := 255
				if alpha {
					dx, dy := float64(2*x-w)/float64(w), float64(2*y-h)/float64(h)
					a = max(0, min(255, int((1-(dx*dx+dy*dy))*400)))
				}
				m.SetNRGBA(x, y, color.NRGBA{uint8(x * 255 / w), uint8(y * 255 / h), uint8((x * y) % 256), uint8(a)})
			}
		}
		return m
	}
	return map[string]*image.NRGBA{"gradient": mk(60, 40, false), "window": mk(60, 40, true), "tall": mk(17, 100, false), "wide": mk(100, 9, true)}
}

// TestPlaceholders: the codes of these pictures, the same on every
// processor. On photographs and icons they are the reference encoders'
// (ThumbHash's JavaScript, thumbhash 0.1.1: the same bytes for 10;
// BlurHash's as woltapp's C encoder scales the components, by the largest
// in magnitude, which its TypeScript one does by the largest value: 8 of 10
// the same, the other two whose largest is negative). On these synthetic
// gradients some components fall on a rounding boundary, where the
// reference's cosines, off by their rounding, can tip a 4-bit value the
// other way (2 of the 4 ThumbHashes differ in one).
func TestPlaceholders(t *testing.T) {
	want := map[string][2]string{
		"gradient": {"LzHB}B2U$2Scl]axjrf6gGfhfRfk", "XggKJZpwdndwd4d3iGiHd7CQC/h4"},
		"window":   {"LzHB}B2U$2Scl]axjrf6gGfhfRfk", "4AeGE4g4gHyAe7iAgAj4eAdXh4iAeFg="},
		"tall":     {"L#Gu~@7EwtkRhkaefPe=gZfifSfl", "XfgNIRpgcHeHhod30qAL+Ig="},
		"wide":     {"L#HL9w2T$1Scu.S1o3a$fefSfTfS", "H/iFCYg4cHuAi7nBwQ/4SQdXhIiAeGg="},
	}
	for name, m := range phImages() {
		b, th := blurHash(m, 4, 3), base64.StdEncoding.EncodeToString(thumbHash(m))
		if b != want[name][0] || th != want[name][1] {
			t.Errorf("%s: BlurHash %s ThumbHash %s, want %s %s", name, b, th, want[name][0], want[name][1])
		}
	}
	for _, c := range [][2]int{{1, 1}, {9, 9}, {1, 9}} {
		if got, n := blurHash(phImages()["gradient"], c[0], c[1]), 4+2*c[0]*c[1]; len(got) != n {
			t.Errorf("%dx%d components: %d characters, want %d", c[0], c[1], len(got), n)
		}
	}
}

// TestSRGBTables: every 8-bit value comes back from its linear light.
func TestSRGBTables(t *testing.T) {
	for v := 0; v < 256; v++ {
		if got := linearToSRGB(srgbLinear[v]); got != v {
			t.Fatalf("%d -> %v -> %d", v, srgbLinear[v], got)
		}
	}
	if linearToSRGB(-1) != 0 || linearToSRGB(2) != 255 {
		t.Error("out of range")
	}
}
