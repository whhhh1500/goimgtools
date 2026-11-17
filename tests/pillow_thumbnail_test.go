package tests

import (
	"testing"

	. "github.com/whhhh1500/goimgtools"
)

// TestPillowThumbnail: ResizePillow with ReducingGap is Pillow's
// Image.thumbnail (FitInside, WithoutEnlargement, FastShrinkOnLoad: the
// size by the nearest aspect ratio, a JPEG drafted for twice the box, the
// box of the reduced image, Image.reduce first) and Image.resize with
// reducing_gap (FitFill), to the byte. The sums are of Pillow 12.1.1's
// pixels.
func TestPillowThumbnail(t *testing.T) {
	for _, c := range []struct {
		in, mode string
		w, h     int
		k        Kernel
		gap      float64
		sum      string
	}{
		{"sharp/resize-420.jpg", "thumb", 70, 70, KernelCubic, 2, "6704ea7a95a19eaf"},
		{"sharp/resize-420.jpg", "thumb", 38, 100, KernelLanczos3, 2, "a8342eaa40f3692c"},
		{"sharp/resize-420.jpg", "thumb", 100, 30, KernelHamming, 2, "444a11975c640b1a"},
		{"sharp/resize-420.jpg", "thumb", 61, 61, KernelBox, 2, "a3ad4ba64807e8dd"},
		{"sharp/resize-422.jpg", "thumb", 70, 70, KernelCubic, 2, "6d2c4919340ae95c"},
		{"sharp/resize-422.jpg", "thumb", 38, 100, KernelLanczos3, 2, "d79177b4009e9370"},
		{"sharp/resize-422.jpg", "thumb", 100, 30, KernelHamming, 2, "59c7958eecb6d270"},
		{"sharp/resize-422.jpg", "thumb", 61, 61, KernelBox, 2, "62db15aaabfdff3e"},
		{"sharp/resize-444p.jpg", "thumb", 70, 70, KernelCubic, 2, "b7da218f630a4d8a"},
		{"sharp/resize-444p.jpg", "thumb", 38, 100, KernelLanczos3, 2, "3ad0e0af9863cc5d"},
		{"sharp/resize-444p.jpg", "thumb", 100, 30, KernelHamming, 2, "b2a8ad5a89f4029a"},
		{"sharp/resize-444p.jpg", "thumb", 61, 61, KernelBox, 2, "2bee1b77079767e6"},
		{"sharp/resize-grayp.jpg", "thumb", 70, 70, KernelCubic, 2, "00195f3363788217"},
		{"sharp/resize-grayp.jpg", "thumb", 38, 100, KernelLanczos3, 2, "c43388e546f2bccc"},
		{"sharp/resize-grayp.jpg", "thumb", 100, 30, KernelHamming, 2, "5082bf60dc3540cc"},
		{"sharp/resize-grayp.jpg", "thumb", 61, 61, KernelBox, 2, "ca51a1bb84871e14"},
		{"pillow/source.png", "thumb", 70, 70, KernelCubic, 2, "b75e12c87cc11a4f"},
		{"pillow/source.png", "thumb", 38, 100, KernelLanczos3, 2, "cafeed854d8394d2"},
		{"pillow/source.png", "thumb", 100, 30, KernelHamming, 2, "453fd3610cfc518a"},
		{"pillow/source.png", "thumb", 61, 61, KernelBox, 2, "46ed865047b12aff"},
		{"pillow/median-rgba-source.png", "thumb", 70, 70, KernelCubic, 2, "69122b234c887b27"},
		{"pillow/median-rgba-source.png", "thumb", 38, 100, KernelLanczos3, 2, "70a9ef3a6aaa9b43"},
		{"pillow/median-rgba-source.png", "thumb", 100, 30, KernelHamming, 2, "98d4e06bdd289eb6"},
		{"pillow/median-rgba-source.png", "thumb", 61, 61, KernelBox, 2, "4bae9e0e35da0b0c"},
		{"pillow/gray.png", "thumb", 70, 70, KernelCubic, 2, "050ab49898d380cc"},
		{"pillow/gray.png", "thumb", 38, 100, KernelLanczos3, 2, "95b645f04f59ec29"},
		{"pillow/gray.png", "thumb", 100, 30, KernelHamming, 2, "1276288db412ce8e"},
		{"pillow/gray.png", "thumb", 61, 61, KernelBox, 2, "1d66bc8ac45e446a"},
		{"pillow/source.png", "gap", 30, 20, KernelCubic, 1.5, "35b3ce2acc4e9752"},
		{"pillow/source.png", "gap", 13, 50, KernelLanczos3, 2.0, "09da310e7e20147d"},
		{"pillow/source.png", "gap", 70, 9, KernelLinear, 3.0, "609a3a12fe156846"},
		{"pillow/gray.png", "gap", 30, 20, KernelCubic, 1.5, "a046e720a060a994"},
		{"pillow/gray.png", "gap", 13, 50, KernelLanczos3, 2.0, "34ec0bdd593ee479"},
		{"pillow/gray.png", "gap", 70, 9, KernelLinear, 3.0, "4b843ebbf5075409"},
	} {
		o := ResizeOptions{Kernel: c.k, Method: ResizePillow, ReducingGap: c.gap, Fit: FitFill}
		if c.mode == "thumb" {
			o.Fit, o.WithoutEnlargement, o.FastShrinkOnLoad = FitInside, true, true
		}
		for _, threads := range []int{1, 3} {
			out, err := FromBytes(sourcePNG(t, c.in)).Threads(threads).Resize(c.w, c.h, o).PNG().ToBytes()
			if err != nil {
				t.Errorf("%s %s %dx%d: %v", c.in, c.mode, c.w, c.h, err)
				break
			}
			if got := rgbaSum(t, decodePNG(t, out)); got != c.sum {
				t.Errorf("%s %s %dx%d kernel %v gap %v, %d threads: %s, Pillow's is %s", c.in, c.mode, c.w, c.h, c.k, c.gap, threads, got, c.sum)
				break
			}
		}
	}
	rgb := sourcePNG(t, "pillow/source.png")
	for _, o := range []ResizeOptions{{ReducingGap: 2}, {ReducingGap: 0.5, Method: ResizePillow}, {ReducingGap: -1, Method: ResizePillow},
		{ReducingGap: 2, Method: ResizeLibvips}} {
		if _, err := FromBytes(rgb).Resize(60, 41, o).PNG().ToBytes(); err == nil {
			t.Errorf("Resize(%+v) accepted", o)
		}
	}
	a := must(FromBytes(rgb).Resize(30, 20, ResizeOptions{Fit: FitFill, Method: ResizePillow, ReducingGap: 1.5}).PNG().ToBytes())
	if b := must(FromBytes(rgb).Do("resize", "30", "20", "fill", "cubic", "center", "srgb", "pillow", "1.5").PNG().ToBytes()); string(a) != string(b) {
		t.Error("by name: another image")
	}
	// A side of 0 follows the other; a box the image fits in leaves it.
	for _, c := range []struct{ w, h, ww, wh int }{{61, 0, 61, 42}, {0, 50, 73, 50}, {500, 500, 150, 103}} {
		m := decodePNG(t, must(FromBytes(rgb).Resize(c.w, c.h, ResizeOptions{Method: ResizePillow, ReducingGap: 2, WithoutEnlargement: true}).PNG().ToBytes()))
		if b := m.Bounds(); b.Dx() != c.ww || b.Dy() != c.wh {
			t.Errorf("%dx%d: %v, want %dx%d", c.w, c.h, b, c.ww, c.wh)
		}
	}
}
