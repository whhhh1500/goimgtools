package tests

import (
	"bytes"
	"math"
	"testing"

	. "github.com/whhhh1500/goimgtools"
)

// TestPillowFilters: Filter and Convolve with MethodPillow are Pillow's
// ImageFilter built-ins and ImageFilter.Kernel, to the byte. The sums are
// of the pixels of Pillow 12.1.1's im.filter(...) (with transparency alpha
// is filtered too, as Pillow does).
func TestPillowFilters(t *testing.T) {
	for _, c := range []struct {
		in, op string
		args   []string
		sum    string
	}{
		{"pillow/source.png", "filter", []string{"blur"}, "79fa440b7f2cc59a"},
		{"pillow/source.png", "filter", []string{"contour"}, "e998f3a04f2646cd"},
		{"pillow/source.png", "filter", []string{"detail"}, "36cbd68540350d7b"},
		{"pillow/source.png", "filter", []string{"edge-enhance"}, "26ad8eaa5806ce8d"},
		{"pillow/source.png", "filter", []string{"edge-enhance-more"}, "f5831c7e97004c5a"},
		{"pillow/source.png", "filter", []string{"emboss"}, "6db12b1621deab97"},
		{"pillow/source.png", "filter", []string{"find-edges"}, "a2466f9b7f35b49c"},
		{"pillow/source.png", "filter", []string{"sharpen"}, "519b2f4a8b8782fb"},
		{"pillow/source.png", "filter", []string{"smooth"}, "6850f70e41e6e29a"},
		{"pillow/source.png", "filter", []string{"smooth-more"}, "2e0bd4c58d7435b3"},
		{"pillow/source.png", "convolve", []string{"3", "3", "0", "-1", "0", "-1", "5", "-1", "0", "-1", "0", "1.0", "0.4", "pillow"}, "9d941dbb0c3c03df"},
		{"pillow/source.png", "convolve", []string{"5", "5", "-12", "-11", "-10", "-9", "-8", "-7", "-6", "-5", "-4", "-3", "-2", "-1", "0", "1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11", "12", "7.5", "33.0", "pillow"}, "54a6c9042f80a59c"},
		{"pillow/gray.png", "filter", []string{"blur"}, "34159536979268e3"},
		{"pillow/gray.png", "filter", []string{"contour"}, "01bf28d3610749fc"},
		{"pillow/gray.png", "filter", []string{"detail"}, "bbc61a00dbad0503"},
		{"pillow/gray.png", "filter", []string{"edge-enhance"}, "d4e2fdc4626798e5"},
		{"pillow/gray.png", "filter", []string{"edge-enhance-more"}, "1d0f860a0feaddea"},
		{"pillow/gray.png", "filter", []string{"emboss"}, "1aa45f184370b3e5"},
		{"pillow/gray.png", "filter", []string{"find-edges"}, "fe3b899e21be94c1"},
		{"pillow/gray.png", "filter", []string{"sharpen"}, "3d3dc8ceaad28535"},
		{"pillow/gray.png", "filter", []string{"smooth"}, "c83f95fde55e786f"},
		{"pillow/gray.png", "filter", []string{"smooth-more"}, "436fcc5ea8df997a"},
		{"pillow/gray.png", "convolve", []string{"3", "3", "0", "-1", "0", "-1", "5", "-1", "0", "-1", "0", "1.0", "0.4", "pillow"}, "2b886d912672b171"},
		{"pillow/gray.png", "convolve", []string{"5", "5", "-12", "-11", "-10", "-9", "-8", "-7", "-6", "-5", "-4", "-3", "-2", "-1", "0", "1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11", "12", "7.5", "33.0", "pillow"}, "2f06ad6b3009ea23"},
		{"pillow/median-rgba-source.png", "filter", []string{"blur"}, "f2c77eaf24a8894b"},
		{"pillow/median-rgba-source.png", "filter", []string{"contour"}, "090e3fb4d668cced"},
		{"pillow/median-rgba-source.png", "filter", []string{"detail"}, "c3d562d383528616"},
		{"pillow/median-rgba-source.png", "filter", []string{"edge-enhance"}, "8de22f81494bcafe"},
		{"pillow/median-rgba-source.png", "filter", []string{"edge-enhance-more"}, "7c74d0f4052a247f"},
		{"pillow/median-rgba-source.png", "filter", []string{"emboss"}, "0a0bcd6ff240a58f"},
		{"pillow/median-rgba-source.png", "filter", []string{"find-edges"}, "00bf1db2316da6fb"},
		{"pillow/median-rgba-source.png", "filter", []string{"sharpen"}, "99afc40966458394"},
		{"pillow/median-rgba-source.png", "filter", []string{"smooth"}, "79381bd0978d2402"},
		{"pillow/median-rgba-source.png", "filter", []string{"smooth-more"}, "4aa87b8719d25168"},
		{"pillow/median-rgba-source.png", "convolve", []string{"3", "3", "0", "-1", "0", "-1", "5", "-1", "0", "-1", "0", "1.0", "0.4", "pillow"}, "641d5a343e45d65c"},
		{"pillow/median-rgba-source.png", "convolve", []string{"5", "5", "-12", "-11", "-10", "-9", "-8", "-7", "-6", "-5", "-4", "-3", "-2", "-1", "0", "1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11", "12", "7.5", "33.0", "pillow"}, "2eab62f3f71c0f3f"},
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
	a := must(FromBytes(rgb).Filter(FilterEmboss).PNG().ToBytes())
	if b := must(FromBytes(rgb).Do("filter", "emboss").PNG().ToBytes()); string(a) != string(b) {
		t.Error("by name: another image")
	}
	k := Convolution{Width: 3, Height: 3, Weights: []float64{0, -1, 0, -1, 5, -1, 0, -1, 0}, Scale: 1, Offset: 0.4, Method: MethodPillow}
	if b := must(FromBytes(rgb).Convolve(k).PNG().ToBytes()); string(b) != string(must(FromBytes(rgb).Do("convolve", "3", "3", "0", "-1", "0", "-1", "5", "-1", "0", "-1", "0", "1", "0.4", "pillow").PNG().ToBytes())) {
		t.Error("convolve by name: another image")
	}
	for i, m := range []*Image{FromBytes(rgb).Filter(-1), FromBytes(rgb).Filter(10), FromBytes(rgb).Do("filter", "glow"),
		FromBytes(rgb).Convolve(Convolution{Width: 7, Height: 7, Weights: make([]float64, 49), Method: MethodPillow}),
		FromBytes(rgb).Convolve(Convolution{Width: 3, Height: 5, Weights: make([]float64, 15), Method: MethodPillow}),
		FromBytes(rgb).Convolve(Convolution{Width: 3, Height: 3, Weights: make([]float64, 9), Method: 3})} {
		if _, err := m.PNG().ToBytes(); err == nil {
			t.Errorf("bad arguments %d accepted", i)
		}
	}
}

// TestPillowBoxBlur: BoxBlur is Pillow's ImageFilter.BoxBlur, to the byte,
// for whole and fractional radii, in colour and grey, and the same with
// helpers; 0 leaves the image alone. The sums are of Pillow 12.1.1's pixels.
func TestPillowBoxBlur(t *testing.T) {
	for _, c := range []struct {
		in     string
		radius float64
		sum    string
	}{
		{"pillow/source.png", 0.5, "1eab8545fcefae9f"},
		{"pillow/source.png", 1, "297ff2e6d6447ffd"},
		{"pillow/source.png", 1.3, "a10c5f99598f5939"},
		{"pillow/source.png", 2.7, "5abb2e9a81ac6461"},
		{"pillow/source.png", 10, "a8559f29ab92aef3"},
		{"pillow/source.png", 40, "585861d231fdc359"},
		{"pillow/source.png", 200, "97883072bf6a29eb"},
		{"pillow/gray.png", 0.5, "2090791b03aa7105"},
		{"pillow/gray.png", 1, "3c512b08fab9cf9b"},
		{"pillow/gray.png", 1.3, "a5332c01faf0e685"},
		{"pillow/gray.png", 2.7, "ee147cbae957eef6"},
		{"pillow/gray.png", 10, "eb46fa4caaa105bf"},
		{"pillow/gray.png", 40, "2360f6cb92a1fc56"},
		{"pillow/gray.png", 200, "4b21abc3b39bc616"},
	} {
		for _, threads := range []int{1, 4} {
			out, err := FromBytes(sourcePNG(t, c.in)).Threads(threads).BoxBlur(c.radius).PNG().ToBytes()
			if err != nil {
				t.Fatal(err)
			}
			if got := rgbaSum(t, decodePNG(t, out)); got != c.sum {
				t.Errorf("%s radius %v, %d threads: %s, Pillow's is %s", c.in, c.radius, threads, got, c.sum)
			}
		}
	}
	in := sourcePNG(t, "pillow/source.png")
	if a, b := must(FromBytes(in).BoxBlur(0).PNG().ToBytes()), must(FromBytes(in).PNG().ToBytes()); !bytes.Equal(a, b) {
		t.Error("BoxBlur(0) changed the image")
	}
	if b := must(FromBytes(in).Do("boxblur", "2.5").PNG().ToBytes()); !bytes.Equal(b, must(FromBytes(in).BoxBlur(2.5).PNG().ToBytes())) {
		t.Error("boxblur by name: another image")
	}
	for _, r := range []float64{-1, 1001, math.NaN()} {
		if _, err := FromBytes(in).BoxBlur(r).PNG().ToBytes(); err == nil {
			t.Errorf("BoxBlur(%v) accepted", r)
		}
	}
}
