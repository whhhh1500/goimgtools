package tests

import (
	"image/color"
	"math"
	"testing"

	. "github.com/whhhh1500/goimgtools"
)

// TestPillowOps: Posterize, Solarize, Equalize, AutoContrast and Enhance
// are Pillow's ImageOps and ImageEnhance, to the byte. The sums are of the
// pixels of Pillow 12.1.1's results (ImageOps on an RGBA image: on its RGB,
// the alpha put back, as this library keeps it).
func TestPillowOps(t *testing.T) {
	for _, c := range []struct {
		in, op string
		args   []string
		sum    string
	}{
		{"pillow/source.png", "posterize", []string{"1"}, "720340b41bc0bda6"},
		{"pillow/source.png", "posterize", []string{"3"}, "b0d88432fd876483"},
		{"pillow/source.png", "posterize", []string{"6"}, "729d4e22926519d4"},
		{"pillow/source.png", "solarize", []string{"0"}, "5cec5b7e9f36f3f3"},
		{"pillow/source.png", "solarize", []string{"100"}, "add91a76cdd9e497"},
		{"pillow/source.png", "solarize", []string{"200"}, "809e27fd4be3a441"},
		{"pillow/source.png", "equalize", []string{}, "bdd23a9c41cfcb43"},
		{"pillow/source.png", "autocontrast", []string{"0", "0", "channels"}, "83598e618cfcad33"},
		{"pillow/source.png", "autocontrast", []string{"2", "5", "channels"}, "54d1211a3bb542b6"},
		{"pillow/source.png", "autocontrast", []string{"10", "10", "tone"}, "9e44db78a889d501"},
		{"pillow/source.png", "autocontrast", []string{"1.5", "0.5", "tone", "3", "250"}, "e6b639e1021a1f51"},
		{"pillow/source.png", "enhance", []string{"color", "0.3"}, "f414d5eff698c16d"},
		{"pillow/source.png", "enhance", []string{"color", "1.7"}, "af0a97c093129ef0"},
		{"pillow/source.png", "enhance", []string{"color", "-0.5"}, "7ce0938df3f49722"},
		{"pillow/source.png", "enhance", []string{"contrast", "0.3"}, "19d5a9fc693eeb10"},
		{"pillow/source.png", "enhance", []string{"contrast", "1.7"}, "fee6ae72092c5732"},
		{"pillow/source.png", "enhance", []string{"contrast", "-0.5"}, "e7e007423df5db89"},
		{"pillow/source.png", "enhance", []string{"brightness", "0.3"}, "1004a724be9c5591"},
		{"pillow/source.png", "enhance", []string{"brightness", "1.7"}, "dc131f31b34c220c"},
		{"pillow/source.png", "enhance", []string{"brightness", "-0.5"}, "87129cb2302721f8"},
		{"pillow/source.png", "enhance", []string{"sharpness", "0.3"}, "5bc1772f536a7944"},
		{"pillow/source.png", "enhance", []string{"sharpness", "1.7"}, "0dc9b3d9619d388b"},
		{"pillow/source.png", "enhance", []string{"sharpness", "-0.5"}, "089b22df661d2853"},
		{"pillow/gray.png", "posterize", []string{"1"}, "8ddb951d3c9006b4"},
		{"pillow/gray.png", "posterize", []string{"3"}, "ffb1f1c0475cf24a"},
		{"pillow/gray.png", "posterize", []string{"6"}, "0d76ad24d35bf048"},
		{"pillow/gray.png", "solarize", []string{"0"}, "781eb39853fdca75"},
		{"pillow/gray.png", "solarize", []string{"100"}, "f63668a8ad1ff9b1"},
		{"pillow/gray.png", "solarize", []string{"200"}, "6ea5647e3069d04a"},
		{"pillow/gray.png", "equalize", []string{}, "f78a7b7e798b41ff"},
		{"pillow/gray.png", "autocontrast", []string{"0", "0", "channels"}, "e7bfcd46dbe42135"},
		{"pillow/gray.png", "autocontrast", []string{"2", "5", "channels"}, "069cedb18467c304"},
		{"pillow/gray.png", "autocontrast", []string{"10", "10", "tone"}, "6dd1d6a0f5e6c478"},
		{"pillow/gray.png", "autocontrast", []string{"1.5", "0.5", "tone", "3", "250"}, "94667b98ea748b31"},
		{"pillow/gray.png", "enhance", []string{"color", "0.3"}, "e7bfcd46dbe42135"},
		{"pillow/gray.png", "enhance", []string{"color", "1.7"}, "e7bfcd46dbe42135"},
		{"pillow/gray.png", "enhance", []string{"color", "-0.5"}, "e7bfcd46dbe42135"},
		{"pillow/gray.png", "enhance", []string{"contrast", "0.3"}, "b24ddd8246f205ef"},
		{"pillow/gray.png", "enhance", []string{"contrast", "1.7"}, "d14579988a855d73"},
		{"pillow/gray.png", "enhance", []string{"contrast", "-0.5"}, "872f60758560760c"},
		{"pillow/gray.png", "enhance", []string{"brightness", "0.3"}, "3bfe4a2cc77cdfcf"},
		{"pillow/gray.png", "enhance", []string{"brightness", "1.7"}, "425fa9788ba89d3c"},
		{"pillow/gray.png", "enhance", []string{"brightness", "-0.5"}, "87129cb2302721f8"},
		{"pillow/gray.png", "enhance", []string{"sharpness", "0.3"}, "b65c49edd87c8d52"},
		{"pillow/gray.png", "enhance", []string{"sharpness", "1.7"}, "19aadcc29fdd6605"},
		{"pillow/gray.png", "enhance", []string{"sharpness", "-0.5"}, "e93c409ad35ef8a1"},
		{"pillow/median-rgba-source.png", "posterize", []string{"1"}, "afa45e283f53cf87"},
		{"pillow/median-rgba-source.png", "posterize", []string{"3"}, "76a290a2a7da8cab"},
		{"pillow/median-rgba-source.png", "posterize", []string{"6"}, "15e3ef7c8a127c1c"},
		{"pillow/median-rgba-source.png", "solarize", []string{"0"}, "63d4f1ca26bf8010"},
		{"pillow/median-rgba-source.png", "solarize", []string{"100"}, "8b2de671be789631"},
		{"pillow/median-rgba-source.png", "solarize", []string{"200"}, "230e9fd8fed4f760"},
		{"pillow/median-rgba-source.png", "equalize", []string{}, "918433f24c3614e6"},
		{"pillow/median-rgba-source.png", "autocontrast", []string{"0", "0", "channels"}, "04598fdf7ccb5e43"},
		{"pillow/median-rgba-source.png", "autocontrast", []string{"2", "5", "channels"}, "44c87baa9b8039d4"},
		{"pillow/median-rgba-source.png", "autocontrast", []string{"10", "10", "tone"}, "50c70b1e42373f40"},
		{"pillow/median-rgba-source.png", "autocontrast", []string{"1.5", "0.5", "tone", "3", "250"}, "0fed4494f4217102"},
		{"pillow/median-rgba-source.png", "enhance", []string{"color", "0.3"}, "d87694ab652711cc"},
		{"pillow/median-rgba-source.png", "enhance", []string{"color", "1.7"}, "7caf88c529085ebf"},
		{"pillow/median-rgba-source.png", "enhance", []string{"color", "-0.5"}, "d1a1139faf143443"},
		{"pillow/median-rgba-source.png", "enhance", []string{"contrast", "0.3"}, "5b5109bacf3b2fc1"},
		{"pillow/median-rgba-source.png", "enhance", []string{"contrast", "1.7"}, "55af45e07d654730"},
		{"pillow/median-rgba-source.png", "enhance", []string{"contrast", "-0.5"}, "403df1da9325fb85"},
		{"pillow/median-rgba-source.png", "enhance", []string{"brightness", "0.3"}, "f8a6c7e2fbf4631d"},
		{"pillow/median-rgba-source.png", "enhance", []string{"brightness", "1.7"}, "94486ac5f536c072"},
		{"pillow/median-rgba-source.png", "enhance", []string{"brightness", "-0.5"}, "dd6ddd13d6f6d72b"},
		{"pillow/median-rgba-source.png", "enhance", []string{"sharpness", "0.3"}, "2702bf4844bc7f32"},
		{"pillow/median-rgba-source.png", "enhance", []string{"sharpness", "1.7"}, "5ef2956503aac8f7"},
		{"pillow/median-rgba-source.png", "enhance", []string{"sharpness", "-0.5"}, "3a5eeebd092e056b"},
	} {
		for _, threads := range []int{1, 3} {
			out, err := FromBytes(sourcePNG(t, c.in)).Threads(threads).Do(c.op, c.args...).PNG().ToBytes()
			if err != nil {
				t.Errorf("%s %s %v: %v", c.in, c.op, c.args, err)
				break
			}
			if got := rgbaSum(t, decodePNG(t, out)); got != c.sum {
				t.Errorf("%s %s %v, %d threads: %s, Pillow's is %s", c.in, c.op, c.args, threads, got, c.sum)
				break
			}
		}
	}
	rgb := sourcePNG(t, "pillow/source.png")
	same := func(name string, a, b *Image) {
		t.Helper()
		if x, y := must(a.PNG().ToBytes()), must(b.PNG().ToBytes()); string(x) != string(y) {
			t.Errorf("%s: by method another image than by name", name)
		}
	}
	same("posterize", FromBytes(rgb).Posterize(3), FromBytes(rgb).Do("posterize", "3"))
	same("solarize", FromBytes(rgb).Solarize(128), FromBytes(rgb).Do("solarize"))
	same("equalize", FromBytes(rgb).Equalize(), FromBytes(rgb).Do("equalize"))
	same("autocontrast", FromBytes(rgb).AutoContrast(AutoContrastOptions{CutoffLow: 1.5, CutoffHigh: 0.5, PreserveTone: true, Ignore: []int{3, 250}}),
		FromBytes(rgb).Do("autocontrast", "1.5", "0.5", "tone", "3", "250"))
	same("autocontrast default", FromBytes(rgb).AutoContrast(), FromBytes(rgb).Do("autocontrast"))
	same("enhance", FromBytes(rgb).Enhance(EnhanceSharpness, 1.7), FromBytes(rgb).Do("enhance", "sharpness", "1.7"))
	for i, m := range []*Image{FromBytes(rgb).Posterize(0), FromBytes(rgb).Posterize(9), FromBytes(rgb).Solarize(-1),
		FromBytes(rgb).Solarize(257), FromBytes(rgb).Enhance(4, 1), FromBytes(rgb).Enhance(EnhanceColor, math.NaN()),
		FromBytes(rgb).AutoContrast(AutoContrastOptions{CutoffLow: -1}), FromBytes(rgb).AutoContrast(AutoContrastOptions{Ignore: []int{256}}),
		FromBytes(rgb).Do("enhance", "vividness", "2"), FromBytes(rgb).Do("autocontrast", "1", "1", "both")} {
		if _, err := m.PNG().ToBytes(); err == nil {
			t.Errorf("bad arguments %d accepted", i)
		}
	}
	// A grey image stays grey; tiny images are no trouble.
	if g := decodePNG(t, must(FromBytes(sourcePNG(t, "pillow/gray.png")).Enhance(EnhanceSharpness, 2).PNG().ToBytes())); g.ColorModel() != color.GrayModel {
		t.Errorf("grey: %T", g)
	}
	for _, sz := range [][2]int{{1, 1}, {2, 5}, {3, 3}} {
		small := FromBytes(rgb).Resize(sz[0], sz[1], ResizeOptions{Fit: FitFill})
		for _, e := range []Enhancement{EnhanceColor, EnhanceContrast, EnhanceBrightness, EnhanceSharpness} {
			if _, err := small.Clone().Enhance(e, 1.5).Equalize().AutoContrast().PNG().ToBytes(); err != nil {
				t.Errorf("%v: %v", sz, err)
			}
		}
	}
}
