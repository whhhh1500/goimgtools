package tests

import (
	"image/color"
	"testing"

	. "github.com/whhhh1500/goimgtools"
)

// TestPillowThumbnail: MethodPillow with ReducingGap is Pillow's
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
		o := ResizeOptions{Kernel: c.k, Method: MethodPillow, ReducingGap: c.gap, Fit: FitFill}
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
	for _, o := range []ResizeOptions{{ReducingGap: 2}, {ReducingGap: 0.5, Method: MethodPillow}, {ReducingGap: -1, Method: MethodPillow},
		{ReducingGap: 2, Method: MethodLibvips}} {
		if _, err := FromBytes(rgb).Resize(60, 41, o).PNG().ToBytes(); err == nil {
			t.Errorf("Resize(%+v) accepted", o)
		}
	}
	a := must(FromBytes(rgb).Resize(30, 20, ResizeOptions{Fit: FitFill, Method: MethodPillow, ReducingGap: 1.5}).PNG().ToBytes())
	if b := must(FromBytes(rgb).Do("resize", "30", "20", "fill", "cubic", "center", "srgb", "pillow", "1.5").PNG().ToBytes()); string(a) != string(b) {
		t.Error("by name: another image")
	}
	// A side of 0 follows the other; a box the image fits in leaves it.
	for _, c := range []struct{ w, h, ww, wh int }{{61, 0, 61, 42}, {0, 50, 73, 50}, {500, 500, 150, 103}} {
		m := decodePNG(t, must(FromBytes(rgb).Resize(c.w, c.h, ResizeOptions{Method: MethodPillow, ReducingGap: 2, WithoutEnlargement: true}).PNG().ToBytes()))
		if b := m.Bounds(); b.Dx() != c.ww || b.Dy() != c.wh {
			t.Errorf("%dx%d: %v, want %dx%d", c.w, c.h, b, c.ww, c.wh)
		}
	}
}

// TestPillowFits: with MethodPillow the fits are Pillow's ImageOps:
// FitInside contain, FitOutside cover, FitContain pad (with its centering
// and background) and FitCover fit (the part of the box's aspect ratio
// resized), to the byte. The sums are of Pillow 12.1.1's pixels.
func TestPillowFits(t *testing.T) {
	for _, c := range []struct {
		in   string
		w, h int
		k    Kernel
		fit  Fit
		pos  Position
		sum  string
	}{
		{"pillow/source.png", 60, 60, KernelCubic, FitInside, Center, "c8e2bc7d8d44d318"},
		{"pillow/source.png", 60, 60, KernelCubic, FitOutside, Center, "b99af88fa12422fd"},
		{"pillow/source.png", 60, 60, KernelCubic, FitContain, Center, "4f27777e2ce7845e"},
		{"pillow/source.png", 60, 60, KernelCubic, FitCover, Center, "eee8e1991fe1c829"},
		{"pillow/source.png", 97, 31, KernelLanczos3, FitInside, NorthWest, "8e0e7c1994e2b710"},
		{"pillow/source.png", 97, 31, KernelLanczos3, FitOutside, NorthWest, "228940dcd1645cc1"},
		{"pillow/source.png", 97, 31, KernelLanczos3, FitContain, NorthWest, "371ac8ccbb251c18"},
		{"pillow/source.png", 97, 31, KernelLanczos3, FitCover, NorthWest, "a18df7f7d83a1593"},
		{"pillow/source.png", 41, 200, KernelLinear, FitInside, SouthEast, "6da3f121f3970a34"},
		{"pillow/source.png", 41, 200, KernelLinear, FitOutside, SouthEast, "e5e64bc9565f0e19"},
		{"pillow/source.png", 41, 200, KernelLinear, FitContain, SouthEast, "f919ce6dac72f2b6"},
		{"pillow/source.png", 41, 200, KernelLinear, FitCover, SouthEast, "0256ec51c092af12"},
		{"pillow/source.png", 157, 99, KernelCubic, FitInside, North, "f946a0a6855b3a60"},
		{"pillow/source.png", 157, 99, KernelCubic, FitOutside, North, "42f934b12e032c42"},
		{"pillow/source.png", 157, 99, KernelCubic, FitContain, North, "5d75caa5d642d211"},
		{"pillow/source.png", 157, 99, KernelCubic, FitCover, North, "e2a8dbb549fb124f"},
		{"pillow/median-rgba-source.png", 60, 60, KernelCubic, FitInside, Center, "fc09f9b860aff48d"},
		{"pillow/median-rgba-source.png", 60, 60, KernelCubic, FitOutside, Center, "92c7586efd43d70a"},
		{"pillow/median-rgba-source.png", 60, 60, KernelCubic, FitContain, Center, "5a16885dd2e750d2"},
		{"pillow/median-rgba-source.png", 60, 60, KernelCubic, FitCover, Center, "d6141dfd6218ec30"},
		{"pillow/median-rgba-source.png", 97, 31, KernelLanczos3, FitInside, NorthWest, "e0509e4989351a7c"},
		{"pillow/median-rgba-source.png", 97, 31, KernelLanczos3, FitOutside, NorthWest, "2d64b8c58320a41a"},
		{"pillow/median-rgba-source.png", 97, 31, KernelLanczos3, FitContain, NorthWest, "100f18d97ab6091c"},
		{"pillow/median-rgba-source.png", 97, 31, KernelLanczos3, FitCover, NorthWest, "18ad980121775d17"},
		{"pillow/median-rgba-source.png", 41, 200, KernelLinear, FitInside, SouthEast, "414537593c40e66f"},
		{"pillow/median-rgba-source.png", 41, 200, KernelLinear, FitOutside, SouthEast, "766d6ef30882b956"},
		{"pillow/median-rgba-source.png", 41, 200, KernelLinear, FitContain, SouthEast, "5d19c3d8f5eacffb"},
		{"pillow/median-rgba-source.png", 41, 200, KernelLinear, FitCover, SouthEast, "48148068053110ed"},
		{"pillow/median-rgba-source.png", 157, 99, KernelCubic, FitInside, North, "808d493058b01e5e"},
		{"pillow/median-rgba-source.png", 157, 99, KernelCubic, FitOutside, North, "697d4531aa5e5975"},
		{"pillow/median-rgba-source.png", 157, 99, KernelCubic, FitContain, North, "281d3e16dca2291d"},
		{"pillow/median-rgba-source.png", 157, 99, KernelCubic, FitCover, North, "97b73f77c3d20ab2"},
	} {
		o := ResizeOptions{Kernel: c.k, Method: MethodPillow, Fit: c.fit, Position: c.pos, Background: color.NRGBA{10, 200, 30, 255}}
		out, err := FromBytes(sourcePNG(t, c.in)).Resize(c.w, c.h, o).PNG().ToBytes()
		if err != nil {
			t.Errorf("%s %dx%d: %v", c.in, c.w, c.h, err)
			continue
		}
		if got := rgbaSum(t, decodePNG(t, out)); got != c.sum {
			t.Errorf("%s %dx%d fit %v position %v: %s, Pillow's is %s", c.in, c.w, c.h, c.fit, c.pos, got, c.sum)
		}
	}
}
