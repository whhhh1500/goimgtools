package tests

import (
	"crypto/sha256"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"testing"

	. "github.com/whhhh1500/goimgtools"
)

// rgbaSum is the first 16 hex digits of the SHA-256 of an image's RGBA
// pixels (colours not multiplied by alpha), row after row.
func rgbaSum(t *testing.T, m image.Image) string {
	t.Helper()
	n := image.NewNRGBA(m.Bounds())
	draw.Draw(n, n.Rect, m, m.Bounds().Min, draw.Src)
	return fmt.Sprintf("%x", sha256.Sum256(n.Pix))[:16]
}

// TestResizeLibvips: MethodLibvips is sharp's resize, to the byte. The sums
// are of the pixels of sharp 0.35.5 (libvips 8.18.7) for
// sharp(input)[.rotate(90)|.flop()|.rotate()].resize(width || null,
// height || null, options): every kernel reducing (with and without box
// shrinks, KernelNearest subsampling), enlarging, both at once, with
// transparency and in grey; the fits, positions and backgrounds (sharp
// pads an image with transparency while its colours are multiplied by
// alpha), one side, withoutEnlargement; turns asked for first (done first)
// and the EXIF turn (done after); tall images whose rows libvips places in
// runs of 10; and JPEGs that sharp shrinks while they are decoded (4:2:0,
// 4:2:2, progressive 4:4:4 and grey, by 2, 4 and 8, fastShrinkOnLoad or
// not), with libjpeg's scaled decoding and the cut libvips makes.
func TestResizeLibvips(t *testing.T) {
	for _, c := range []struct {
		in, pre string
		w, h    int
		o       ResizeOptions
		sum     string
	}{
		{"pillow/source.png", "", 60, 41, ResizeOptions{Kernel: KernelNearest, Fit: FitFill}, "05ed2401f430edba"},
		{"pillow/source.png", "", 30, 20, ResizeOptions{Kernel: KernelNearest, Fit: FitFill}, "0c431da0940423a3"},
		{"pillow/source.png", "", 13, 9, ResizeOptions{Kernel: KernelNearest, Fit: FitFill}, "7ee47fd32a686bae"},
		{"pillow/source.png", "", 300, 206, ResizeOptions{Kernel: KernelNearest, Fit: FitFill}, "3bc9dc1ad2cc0523"},
		{"pillow/source.png", "", 200, 80, ResizeOptions{Kernel: KernelNearest, Fit: FitFill}, "f4850f0a330c114f"},
		{"pillow/source.png", "", 90, 150, ResizeOptions{Kernel: KernelNearest, Fit: FitFill}, "ca290b6b405f7792"},
		{"pillow/median-rgba-source.png", "", 60, 41, ResizeOptions{Kernel: KernelNearest, Fit: FitFill}, "faaf7f3078eb4e2e"},
		{"pillow/median-rgba-source.png", "", 30, 20, ResizeOptions{Kernel: KernelNearest, Fit: FitFill}, "2f21022ff21cfd42"},
		{"pillow/median-rgba-source.png", "", 190, 130, ResizeOptions{Kernel: KernelNearest, Fit: FitFill}, "e7bc50a09a867225"},
		{"pillow/source.png", "", 60, 41, ResizeOptions{Kernel: KernelLinear, Fit: FitFill}, "c60748d756d13707"},
		{"pillow/source.png", "", 30, 20, ResizeOptions{Kernel: KernelLinear, Fit: FitFill}, "1c7ef2144cfd69c5"},
		{"pillow/source.png", "", 13, 9, ResizeOptions{Kernel: KernelLinear, Fit: FitFill}, "462e2a9e5bf4d2f5"},
		{"pillow/source.png", "", 300, 206, ResizeOptions{Kernel: KernelLinear, Fit: FitFill}, "2533049dbb541488"},
		{"pillow/source.png", "", 200, 80, ResizeOptions{Kernel: KernelLinear, Fit: FitFill}, "db397b80abb947ae"},
		{"pillow/source.png", "", 90, 150, ResizeOptions{Kernel: KernelLinear, Fit: FitFill}, "25d556cc35f373f6"},
		{"pillow/median-rgba-source.png", "", 60, 41, ResizeOptions{Kernel: KernelLinear, Fit: FitFill}, "587ed49b9f7ebf9f"},
		{"pillow/median-rgba-source.png", "", 30, 20, ResizeOptions{Kernel: KernelLinear, Fit: FitFill}, "9f0bd77d708cc526"},
		{"pillow/median-rgba-source.png", "", 190, 130, ResizeOptions{Kernel: KernelLinear, Fit: FitFill}, "9240a1acd87281ce"},
		{"pillow/source.png", "", 60, 41, ResizeOptions{Kernel: KernelCubic, Fit: FitFill}, "b58ef52811a92f11"},
		{"pillow/source.png", "", 30, 20, ResizeOptions{Kernel: KernelCubic, Fit: FitFill}, "39319c1c7a8bc0b0"},
		{"pillow/source.png", "", 13, 9, ResizeOptions{Kernel: KernelCubic, Fit: FitFill}, "88000299e9651a38"},
		{"pillow/source.png", "", 300, 206, ResizeOptions{Kernel: KernelCubic, Fit: FitFill}, "23e480c1a6ceaada"},
		{"pillow/source.png", "", 200, 80, ResizeOptions{Kernel: KernelCubic, Fit: FitFill}, "1ba12cfbdd2f8827"},
		{"pillow/source.png", "", 90, 150, ResizeOptions{Kernel: KernelCubic, Fit: FitFill}, "2d5001eb8959b957"},
		{"pillow/median-rgba-source.png", "", 60, 41, ResizeOptions{Kernel: KernelCubic, Fit: FitFill}, "dd18adfb8359d928"},
		{"pillow/median-rgba-source.png", "", 30, 20, ResizeOptions{Kernel: KernelCubic, Fit: FitFill}, "f2b1aa8a222841dd"},
		{"pillow/median-rgba-source.png", "", 190, 130, ResizeOptions{Kernel: KernelCubic, Fit: FitFill}, "d8d012997f012fa1"},
		{"pillow/source.png", "", 60, 41, ResizeOptions{Kernel: KernelMitchell, Fit: FitFill}, "66230f3ba09d7406"},
		{"pillow/source.png", "", 30, 20, ResizeOptions{Kernel: KernelMitchell, Fit: FitFill}, "0c19bb6d094ab752"},
		{"pillow/source.png", "", 13, 9, ResizeOptions{Kernel: KernelMitchell, Fit: FitFill}, "ffe97fd6527faf7b"},
		{"pillow/source.png", "", 300, 206, ResizeOptions{Kernel: KernelMitchell, Fit: FitFill}, "23e480c1a6ceaada"},
		{"pillow/source.png", "", 200, 80, ResizeOptions{Kernel: KernelMitchell, Fit: FitFill}, "2b6449809f189f29"},
		{"pillow/source.png", "", 90, 150, ResizeOptions{Kernel: KernelMitchell, Fit: FitFill}, "6d98e9b38d5410ad"},
		{"pillow/median-rgba-source.png", "", 60, 41, ResizeOptions{Kernel: KernelMitchell, Fit: FitFill}, "2ea6ae9cb9a97dab"},
		{"pillow/median-rgba-source.png", "", 30, 20, ResizeOptions{Kernel: KernelMitchell, Fit: FitFill}, "50ac3a79de8bbde7"},
		{"pillow/median-rgba-source.png", "", 190, 130, ResizeOptions{Kernel: KernelMitchell, Fit: FitFill}, "d8d012997f012fa1"},
		{"pillow/source.png", "", 60, 41, ResizeOptions{Kernel: KernelLanczos2, Fit: FitFill}, "1af1b655331ad76f"},
		{"pillow/source.png", "", 30, 20, ResizeOptions{Kernel: KernelLanczos2, Fit: FitFill}, "f7d1e0cc8d672686"},
		{"pillow/source.png", "", 13, 9, ResizeOptions{Kernel: KernelLanczos2, Fit: FitFill}, "edc265fd8f77a88d"},
		{"pillow/source.png", "", 300, 206, ResizeOptions{Kernel: KernelLanczos2, Fit: FitFill}, "23e480c1a6ceaada"},
		{"pillow/source.png", "", 200, 80, ResizeOptions{Kernel: KernelLanczos2, Fit: FitFill}, "c3e45ae0744541cc"},
		{"pillow/source.png", "", 90, 150, ResizeOptions{Kernel: KernelLanczos2, Fit: FitFill}, "c1b59615e5ce6b40"},
		{"pillow/median-rgba-source.png", "", 60, 41, ResizeOptions{Kernel: KernelLanczos2, Fit: FitFill}, "05a7ce6c3fadc46a"},
		{"pillow/median-rgba-source.png", "", 30, 20, ResizeOptions{Kernel: KernelLanczos2, Fit: FitFill}, "7e15f1bd732f8288"},
		{"pillow/median-rgba-source.png", "", 190, 130, ResizeOptions{Kernel: KernelLanczos2, Fit: FitFill}, "d8d012997f012fa1"},
		{"pillow/source.png", "", 60, 41, ResizeOptions{Kernel: KernelLanczos3, Fit: FitFill}, "01526519e0efce29"},
		{"pillow/source.png", "", 30, 20, ResizeOptions{Kernel: KernelLanczos3, Fit: FitFill}, "dfc41346ba8b85ae"},
		{"pillow/source.png", "", 13, 9, ResizeOptions{Kernel: KernelLanczos3, Fit: FitFill}, "7bbb62a26aa255bf"},
		{"pillow/source.png", "", 300, 206, ResizeOptions{Kernel: KernelLanczos3, Fit: FitFill}, "23e480c1a6ceaada"},
		{"pillow/source.png", "", 200, 80, ResizeOptions{Kernel: KernelLanczos3, Fit: FitFill}, "761d080be5ea297b"},
		{"pillow/source.png", "", 90, 150, ResizeOptions{Kernel: KernelLanczos3, Fit: FitFill}, "5795998513336667"},
		{"pillow/median-rgba-source.png", "", 60, 41, ResizeOptions{Kernel: KernelLanczos3, Fit: FitFill}, "82ed6221ae6f7aee"},
		{"pillow/median-rgba-source.png", "", 30, 20, ResizeOptions{Kernel: KernelLanczos3, Fit: FitFill}, "36d6fa0a28eeeb09"},
		{"pillow/median-rgba-source.png", "", 190, 130, ResizeOptions{Kernel: KernelLanczos3, Fit: FitFill}, "d8d012997f012fa1"},
		{"pillow/source.png", "", 60, 41, ResizeOptions{Kernel: KernelMKS2013, Fit: FitFill}, "7d0be80640b35c74"},
		{"pillow/source.png", "", 30, 20, ResizeOptions{Kernel: KernelMKS2013, Fit: FitFill}, "a104b7054af79c10"},
		{"pillow/source.png", "", 13, 9, ResizeOptions{Kernel: KernelMKS2013, Fit: FitFill}, "5780909dbd968bf6"},
		{"pillow/source.png", "", 300, 206, ResizeOptions{Kernel: KernelMKS2013, Fit: FitFill}, "23e480c1a6ceaada"},
		{"pillow/source.png", "", 200, 80, ResizeOptions{Kernel: KernelMKS2013, Fit: FitFill}, "82f87e0d47cb5554"},
		{"pillow/source.png", "", 90, 150, ResizeOptions{Kernel: KernelMKS2013, Fit: FitFill}, "c9c2cb8cb64a56f2"},
		{"pillow/median-rgba-source.png", "", 60, 41, ResizeOptions{Kernel: KernelMKS2013, Fit: FitFill}, "13aa176dabe5044b"},
		{"pillow/median-rgba-source.png", "", 30, 20, ResizeOptions{Kernel: KernelMKS2013, Fit: FitFill}, "4a2197b9600e0dcd"},
		{"pillow/median-rgba-source.png", "", 190, 130, ResizeOptions{Kernel: KernelMKS2013, Fit: FitFill}, "d8d012997f012fa1"},
		{"pillow/source.png", "", 60, 41, ResizeOptions{Kernel: KernelMKS2021, Fit: FitFill}, "063bb854d69c67f5"},
		{"pillow/source.png", "", 30, 20, ResizeOptions{Kernel: KernelMKS2021, Fit: FitFill}, "350cc6d56252d28a"},
		{"pillow/source.png", "", 13, 9, ResizeOptions{Kernel: KernelMKS2021, Fit: FitFill}, "3c5c301c081bcf8d"},
		{"pillow/source.png", "", 300, 206, ResizeOptions{Kernel: KernelMKS2021, Fit: FitFill}, "23e480c1a6ceaada"},
		{"pillow/source.png", "", 200, 80, ResizeOptions{Kernel: KernelMKS2021, Fit: FitFill}, "a1970112eeac0b40"},
		{"pillow/source.png", "", 90, 150, ResizeOptions{Kernel: KernelMKS2021, Fit: FitFill}, "8e419d25496dbeca"},
		{"pillow/median-rgba-source.png", "", 60, 41, ResizeOptions{Kernel: KernelMKS2021, Fit: FitFill}, "35ecb4327d0cd635"},
		{"pillow/median-rgba-source.png", "", 30, 20, ResizeOptions{Kernel: KernelMKS2021, Fit: FitFill}, "6c95f81e943b1071"},
		{"pillow/median-rgba-source.png", "", 190, 130, ResizeOptions{Kernel: KernelMKS2021, Fit: FitFill}, "d8d012997f012fa1"},
		{"pillow/gray.png", "", 60, 41, ResizeOptions{Kernel: KernelLanczos3, Fit: FitFill}, "cdc5b76d8fc189fd"},
		{"pillow/gray.png", "", 30, 20, ResizeOptions{Kernel: KernelCubic, Fit: FitFill}, "6800fd3de2d8f98e"},
		{"pillow/gray.png", "", 200, 140, ResizeOptions{Kernel: KernelLinear, Fit: FitFill}, "29feab0d4a3a7dc5"},
		{"pillow/source.png", "", 60, 60, ResizeOptions{Kernel: KernelLanczos3, Fit: FitCover}, "6d1556b6be140409"},
		{"pillow/median-rgba-source.png", "", 60, 60, ResizeOptions{Kernel: KernelLanczos3, Fit: FitCover, Background: color.NRGBA{210, 100, 37, 77}}, "c49bbc9cd2bdf6fe"},
		{"pillow/source.png", "", 60, 60, ResizeOptions{Kernel: KernelLanczos3, Fit: FitContain}, "2cbe7ed58b2a4135"},
		{"pillow/median-rgba-source.png", "", 60, 60, ResizeOptions{Kernel: KernelLanczos3, Fit: FitContain, Background: color.NRGBA{210, 100, 37, 77}}, "7971b735b32fd566"},
		{"pillow/source.png", "", 60, 60, ResizeOptions{Kernel: KernelLanczos3}, "722377b9fbbe0b01"},
		{"pillow/median-rgba-source.png", "", 60, 60, ResizeOptions{Kernel: KernelLanczos3, Background: color.NRGBA{210, 100, 37, 77}}, "e4ecc63bcb785099"},
		{"pillow/source.png", "", 60, 60, ResizeOptions{Kernel: KernelLanczos3, Fit: FitOutside}, "638f955f3d502775"},
		{"pillow/median-rgba-source.png", "", 60, 60, ResizeOptions{Kernel: KernelLanczos3, Fit: FitOutside, Background: color.NRGBA{210, 100, 37, 77}}, "0a61f4f3a2f0144b"},
		{"pillow/source.png", "", 60, 60, ResizeOptions{Kernel: KernelLanczos3, Fit: FitCover, Position: North}, "6d1556b6be140409"},
		{"pillow/median-rgba-source.png", "", 70, 50, ResizeOptions{Kernel: KernelLanczos3, Fit: FitContain, Position: SouthEast, Background: color.NRGBA{10, 200, 30, 255}}, "c21c090290d818c0"},
		{"pillow/source.png", "", 97, 0, ResizeOptions{Kernel: KernelLanczos3}, "96b0a97e0cffb168"},
		{"pillow/source.png", "", 0, 50, ResizeOptions{Kernel: KernelLanczos3}, "086af919b503b199"},
		{"pillow/source.png", "", 300, 300, ResizeOptions{Kernel: KernelLanczos3, WithoutEnlargement: true}, "83598e618cfcad33"},
		{"pillow/source.png", "", 200, 200, ResizeOptions{Kernel: KernelLanczos3, Fit: FitCover}, "cb4aed69df53b991"},
		{"pillow/source.png", "rotate90", 50, 70, ResizeOptions{Kernel: KernelLanczos3, Fit: FitFill}, "c022d00f7d5daa22"},
		{"pillow/source.png", "flop", 70, 50, ResizeOptions{Kernel: KernelLanczos3, Fit: FitFill}, "03d931a8062f1bd2"},
		{"sharp/resize-tall-205.png", "", 8, 192, ResizeOptions{Kernel: KernelLanczos3, Fit: FitFill}, "5b2f358dbc9132a2"},
		{"sharp/resize-tall-233.png", "", 8, 192, ResizeOptions{Kernel: KernelMitchell, Fit: FitFill}, "2ff49aab4c0b3665"},
		{"sharp/resize-420.jpg", "", 200, 0, ResizeOptions{Kernel: KernelLanczos3, FastShrinkOnLoad: true}, "039242346ed5aa5f"},
		{"sharp/resize-420.jpg", "", 150, 0, ResizeOptions{Kernel: KernelLanczos3, FastShrinkOnLoad: true}, "9273fa5af6530ab2"},
		{"sharp/resize-420.jpg", "", 60, 0, ResizeOptions{Kernel: KernelLanczos3, FastShrinkOnLoad: true}, "09c60281e5ff31f1"},
		{"sharp/resize-420.jpg", "", 30, 0, ResizeOptions{Kernel: KernelLanczos3, FastShrinkOnLoad: true}, "6620b425bf5bb760"},
		{"sharp/resize-420.jpg", "", 60, 0, ResizeOptions{Kernel: KernelLanczos3}, "55f26fb80dd85135"},
		{"sharp/resize-422.jpg", "", 200, 0, ResizeOptions{Kernel: KernelLanczos3, FastShrinkOnLoad: true}, "14c725c10babd114"},
		{"sharp/resize-422.jpg", "", 150, 0, ResizeOptions{Kernel: KernelLanczos3, FastShrinkOnLoad: true}, "61c27a7ab54a1380"},
		{"sharp/resize-422.jpg", "", 60, 0, ResizeOptions{Kernel: KernelLanczos3, FastShrinkOnLoad: true}, "a479ee4c59699477"},
		{"sharp/resize-422.jpg", "", 30, 0, ResizeOptions{Kernel: KernelLanczos3, FastShrinkOnLoad: true}, "06114d43f1632658"},
		{"sharp/resize-422.jpg", "", 60, 0, ResizeOptions{Kernel: KernelLanczos3}, "ab9970d3b42c7f0a"},
		{"sharp/resize-444p.jpg", "", 200, 0, ResizeOptions{Kernel: KernelLanczos3, FastShrinkOnLoad: true}, "29497ce34de134f1"},
		{"sharp/resize-444p.jpg", "", 150, 0, ResizeOptions{Kernel: KernelLanczos3, FastShrinkOnLoad: true}, "2c8bb4427a27d4d6"},
		{"sharp/resize-444p.jpg", "", 60, 0, ResizeOptions{Kernel: KernelLanczos3, FastShrinkOnLoad: true}, "2012ca19e2168375"},
		{"sharp/resize-444p.jpg", "", 30, 0, ResizeOptions{Kernel: KernelLanczos3, FastShrinkOnLoad: true}, "94c16ffe4db0dc58"},
		{"sharp/resize-444p.jpg", "", 60, 0, ResizeOptions{Kernel: KernelLanczos3}, "1996de2e37136bec"},
		{"sharp/resize-grayp.jpg", "", 200, 0, ResizeOptions{Kernel: KernelLanczos3, FastShrinkOnLoad: true}, "8535f56ce973b035"},
		{"sharp/resize-grayp.jpg", "", 150, 0, ResizeOptions{Kernel: KernelLanczos3, FastShrinkOnLoad: true}, "f3e0959616e5d839"},
		{"sharp/resize-grayp.jpg", "", 60, 0, ResizeOptions{Kernel: KernelLanczos3, FastShrinkOnLoad: true}, "a149371d715f82f0"},
		{"sharp/resize-grayp.jpg", "", 30, 0, ResizeOptions{Kernel: KernelLanczos3, FastShrinkOnLoad: true}, "f2315af0cbadfdde"},
		{"sharp/resize-grayp.jpg", "", 60, 0, ResizeOptions{Kernel: KernelLanczos3}, "3267cce147b4e441"},
		{"sharp/resize-exif6.jpg", "auto", 90, 60, ResizeOptions{Kernel: KernelLanczos3, Fit: FitFill, FastShrinkOnLoad: true}, "9ccb5d56b1e25615"},
		{"sharp/resize-exif6.jpg", "auto", 50, 50, ResizeOptions{Kernel: KernelLanczos3, Fit: FitCover, FastShrinkOnLoad: true}, "f625a500d93a9dc1"},
	} {
		c.o.Method = MethodLibvips
		for _, threads := range []int{1, 3} {
			m := FromBytes(sourcePNG(t, c.in)).Threads(threads)
			switch c.pre {
			case "rotate90":
				m.Rotate(90)
			case "flop":
				m.Flop()
			case "auto":
				m.AutoOrient()
			}
			out, err := m.Resize(c.w, c.h, c.o).PNG().ToBytes()
			if err != nil {
				t.Errorf("%s %s %dx%d %+v: %v", c.in, c.pre, c.w, c.h, c.o, err)
				break
			}
			if got := rgbaSum(t, decodePNG(t, out)); got != c.sum {
				t.Errorf("%s %s %dx%d %+v, %d threads: %s, sharp's is %s", c.in, c.pre, c.w, c.h, c.o, threads, got, c.sum)
				break
			}
		}
	}
}
