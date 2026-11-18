package tests

import (
	"testing"

	. "github.com/whhhh1500/goimgtools"
)

// TestResizePillow: MethodPillow is Pillow's Image.resize, to the byte. The
// sums are of the pixels of Pillow 12.1.1's im.resize((width, height),
// filter), after im.transpose(ROTATE_270) for a turn first and after
// im.draft(im.mode, (width, height)) for a JPEG decoded reduced: every
// filter reducing and enlarging, both at once, with transparency, in grey,
// sides of one pixel.
func TestResizePillow(t *testing.T) {
	for _, c := range []struct {
		in, pre string
		w, h    int
		k       Kernel
		sum     string
	}{
		{"pillow/source.png", "", 60, 41, KernelNearest, "05ed2401f430edba"},
		{"pillow/source.png", "", 30, 20, KernelNearest, "cdf9227804fb9910"},
		{"pillow/source.png", "", 300, 206, KernelNearest, "3bc9dc1ad2cc0523"},
		{"pillow/source.png", "", 200, 80, KernelNearest, "dc22bae2b2ac7dc1"},
		{"pillow/source.png", "", 90, 150, KernelNearest, "ac4dd9b6e62d00a7"},
		{"pillow/median-rgba-source.png", "", 60, 41, KernelNearest, "742c40d7e7370b40"},
		{"pillow/median-rgba-source.png", "", 190, 130, KernelNearest, "ff63f5f664dcb932"},
		{"pillow/source.png", "", 60, 41, KernelBox, "330411a83eae6e19"},
		{"pillow/source.png", "", 30, 20, KernelBox, "abfb6fac450152bd"},
		{"pillow/source.png", "", 300, 206, KernelBox, "3bc9dc1ad2cc0523"},
		{"pillow/source.png", "", 200, 80, KernelBox, "8a3e47372b5c5278"},
		{"pillow/source.png", "", 90, 150, KernelBox, "1786b846a79be5e1"},
		{"pillow/median-rgba-source.png", "", 60, 41, KernelBox, "266ca355c6a17d4c"},
		{"pillow/median-rgba-source.png", "", 190, 130, KernelBox, "8ff278f705e35fef"},
		{"pillow/source.png", "", 60, 41, KernelLinear, "df95b1acd169dcc9"},
		{"pillow/source.png", "", 30, 20, KernelLinear, "5f3029c38b34cca5"},
		{"pillow/source.png", "", 300, 206, KernelLinear, "631ae0274b6bcc97"},
		{"pillow/source.png", "", 200, 80, KernelLinear, "d34bf37056fd63fb"},
		{"pillow/source.png", "", 90, 150, KernelLinear, "55100a314db58407"},
		{"pillow/median-rgba-source.png", "", 60, 41, KernelLinear, "1d137e1c28dbb647"},
		{"pillow/median-rgba-source.png", "", 190, 130, KernelLinear, "ab232ba77c604eff"},
		{"pillow/source.png", "", 60, 41, KernelHamming, "dc810a287859710e"},
		{"pillow/source.png", "", 30, 20, KernelHamming, "2a945ff04e4927c6"},
		{"pillow/source.png", "", 300, 206, KernelHamming, "61316c4745d4e2cd"},
		{"pillow/source.png", "", 200, 80, KernelHamming, "8a269032844beac9"},
		{"pillow/source.png", "", 90, 150, KernelHamming, "c608411956aa03bc"},
		{"pillow/median-rgba-source.png", "", 60, 41, KernelHamming, "7840e35490222717"},
		{"pillow/median-rgba-source.png", "", 190, 130, KernelHamming, "05dfe94d7a9afd40"},
		{"pillow/source.png", "", 60, 41, KernelCubic, "c8e2bc7d8d44d318"},
		{"pillow/source.png", "", 30, 20, KernelCubic, "8148a8fa44f7d684"},
		{"pillow/source.png", "", 300, 206, KernelCubic, "25e6ca617c423315"},
		{"pillow/source.png", "", 200, 80, KernelCubic, "6f542d4f2033c18e"},
		{"pillow/source.png", "", 90, 150, KernelCubic, "25482518bb0daed8"},
		{"pillow/median-rgba-source.png", "", 60, 41, KernelCubic, "fc09f9b860aff48d"},
		{"pillow/median-rgba-source.png", "", 190, 130, KernelCubic, "b9b42fda2968a095"},
		{"pillow/source.png", "", 60, 41, KernelLanczos3, "fa1cd5980224a972"},
		{"pillow/source.png", "", 30, 20, KernelLanczos3, "8fe8504cbf68ebb1"},
		{"pillow/source.png", "", 300, 206, KernelLanczos3, "4bbdf3db8734d1f1"},
		{"pillow/source.png", "", 200, 80, KernelLanczos3, "b4473b1c2605a63d"},
		{"pillow/source.png", "", 90, 150, KernelLanczos3, "b54d47fccbf6ac72"},
		{"pillow/median-rgba-source.png", "", 60, 41, KernelLanczos3, "eca6c08bb9c70f33"},
		{"pillow/median-rgba-source.png", "", 190, 130, KernelLanczos3, "d3af0f92bd2c0232"},
		{"pillow/gray.png", "", 60, 41, KernelCubic, "eaa6618a9f757cd9"},
		{"pillow/gray.png", "", 60, 41, KernelLanczos3, "9cf1c2938fa071ab"},
		{"pillow/gray.png", "", 60, 41, KernelLinear, "4a691fba25b5d5ce"},
		{"pillow/source.png", "", 1, 41, KernelCubic, "df82950c1b88a985"},
		{"pillow/source.png", "", 150, 1, KernelLanczos3, "b84a26826317d899"},
		{"pillow/source.png", "rotate90", 50, 70, KernelCubic, "896688be2d7b6061"},
		{"pillow/median-rgba-source.png", "rotate90", 220, 330, KernelLanczos3, "80508e76ce8ae57b"},
		{"sharp/resize-420.jpg", "draft", 70, 50, KernelCubic, "0d3282fbad352258"},
		{"sharp/resize-420.jpg", "draft", 38, 26, KernelCubic, "101cbf6cca05b0ac"},
		{"sharp/resize-422.jpg", "draft", 70, 50, KernelCubic, "2594766abb6f1fec"},
		{"sharp/resize-422.jpg", "draft", 38, 26, KernelCubic, "879c02bc037bb69c"},
		{"sharp/resize-444p.jpg", "draft", 70, 50, KernelCubic, "2a13ac0e0c2d84b0"},
		{"sharp/resize-444p.jpg", "draft", 38, 26, KernelCubic, "03b4a3c2fcb01ba2"},
		{"sharp/resize-grayp.jpg", "draft", 70, 50, KernelCubic, "36edc10611b3a64f"},
		{"sharp/resize-grayp.jpg", "draft", 38, 26, KernelCubic, "ad63ca4c49306b60"},
	} {
		for _, threads := range []int{1, 3} {
			m := FromBytes(sourcePNG(t, c.in)).Threads(threads)
			if c.pre == "rotate90" {
				m.Rotate(90)
			}
			o := ResizeOptions{Fit: FitFill, Kernel: c.k, Method: MethodPillow, FastShrinkOnLoad: c.pre == "draft"}
			out, err := m.Resize(c.w, c.h, o).PNG().ToBytes()
			if err != nil {
				t.Errorf("%s %s %dx%d %v: %v", c.in, c.pre, c.w, c.h, c.k, err)
				break
			}
			if got := rgbaSum(t, decodePNG(t, out)); got != c.sum {
				t.Errorf("%s %s %dx%d kernel %v, %d threads: %s, Pillow's is %s", c.in, c.pre, c.w, c.h, c.k, threads, got, c.sum)
				break
			}
		}
	}
}
