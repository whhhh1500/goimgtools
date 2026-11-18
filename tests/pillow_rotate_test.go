package tests

import (
	"bytes"
	"image/color"
	"testing"

	. "github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// TestPillowRotate: Rotate with MethodPillow is Pillow's Image.rotate (of
// the opposite angle), to the byte: colour, with transparency and grey;
// nearest, bilinear and bicubic; with and without expand; the corners
// left as Pillow fills them or in a colour; quarter turns of an image that
// is not square. The sums are of Pillow 12.1.1's pixels.
func TestPillowRotate(t *testing.T) {
	for _, c := range []struct {
		in     string
		deg    float64
		interp Interpolator
		expand bool
		bg     color.Color
		sum    string
	}{
		{"pillow/source.png", 30, InterpolateNearest, false, nil, "391c8af0055a67ac"},
		{"pillow/source.png", 30, InterpolateBilinear, false, color.NRGBA{10, 200, 30, 128}, "f51424c02fa4f18e"},
		{"pillow/source.png", 30, InterpolateBilinear, true, color.NRGBA{10, 200, 30, 128}, "ed5c66c36c6eaf6a"},
		{"pillow/source.png", 30, InterpolateBicubic, false, nil, "eee40dba54e37c06"},
		{"pillow/source.png", 30, InterpolateBicubic, false, color.NRGBA{10, 200, 30, 128}, "0aa9ad1004b31ecf"},
		{"pillow/source.png", 30, InterpolateBicubic, true, color.NRGBA{10, 200, 30, 128}, "09dcc36eec145c31"},
		{"pillow/source.png", -17.5, InterpolateBilinear, false, nil, "efdd469ca12d4183"},
		{"pillow/source.png", -17.5, InterpolateBicubic, true, nil, "d52d3b6ecab914f3"},
		{"pillow/source.png", 45, InterpolateBilinear, false, nil, "42a68e66ad2df9c5"},
		{"pillow/source.png", 45, InterpolateBicubic, true, nil, "2fac6e57a202dfc3"},
		{"pillow/source.png", 90, InterpolateNearest, false, nil, "4389d14cd7649e22"},
		{"pillow/source.png", 90, InterpolateNearest, true, nil, "abc89322a98d37a1"},
		{"pillow/source.png", 90, InterpolateBilinear, false, nil, "fbaded8461d51fd6"},
		{"pillow/source.png", 90, InterpolateBilinear, true, nil, "abc89322a98d37a1"},
		{"pillow/source.png", 90, InterpolateBicubic, false, nil, "f69f4f8317082dce"},
		{"pillow/source.png", 90, InterpolateBicubic, true, nil, "abc89322a98d37a1"},
		{"pillow/source.png", 200, InterpolateBilinear, false, nil, "af07416960cfe33a"},
		{"pillow/source.png", 200, InterpolateBicubic, true, nil, "486ccea44456f975"},
		{"pillow/source.png", 0.5, InterpolateBilinear, false, nil, "c09503505a998a1a"},
		{"pillow/source.png", 0.5, InterpolateBicubic, true, nil, "15c5f9d76b9cdd94"},
		{"pillow/source.png", 359.99, InterpolateBilinear, false, nil, "2f86d9e3b6ba4e2c"},
		{"pillow/source.png", 359.99, InterpolateBicubic, true, nil, "36b0815dacec7a80"},
		{"pillow/source.png", -123.4, InterpolateBilinear, false, nil, "bdd67a15293ddfe9"},
		{"pillow/source.png", -123.4, InterpolateBilinear, false, color.NRGBA{10, 200, 30, 128}, "2b7023d79dc45fc9"},
		{"pillow/source.png", -123.4, InterpolateBilinear, true, color.NRGBA{10, 200, 30, 128}, "67cb19ec2e27454e"},
		{"pillow/source.png", -123.4, InterpolateBicubic, false, color.NRGBA{10, 200, 30, 128}, "cabf0fd9d11af430"},
		{"pillow/source.png", -123.4, InterpolateBicubic, true, nil, "297eb45144e43841"},
		{"pillow/source.png", -123.4, InterpolateBicubic, true, color.NRGBA{10, 200, 30, 128}, "5e434abb954905a0"},
		{"pillow/median-rgba-source.png", 30, InterpolateNearest, true, nil, "b7d5520ac77c8077"},
		{"pillow/median-rgba-source.png", 30, InterpolateBilinear, false, color.NRGBA{10, 200, 30, 128}, "3a47a5747113f062"},
		{"pillow/median-rgba-source.png", 30, InterpolateBilinear, true, nil, "214a6aa0431982af"},
		{"pillow/median-rgba-source.png", 30, InterpolateBilinear, true, color.NRGBA{10, 200, 30, 128}, "611d320262c60da5"},
		{"pillow/median-rgba-source.png", 30, InterpolateBicubic, false, color.NRGBA{10, 200, 30, 128}, "80e677aff1fd8046"},
		{"pillow/median-rgba-source.png", 30, InterpolateBicubic, true, color.NRGBA{10, 200, 30, 128}, "2d667a267f5cc3b1"},
		{"pillow/median-rgba-source.png", -17.5, InterpolateNearest, false, nil, "0fd0f744fa4c6227"},
		{"pillow/median-rgba-source.png", -17.5, InterpolateBilinear, true, nil, "dbd6d520e9c760b8"},
		{"pillow/median-rgba-source.png", 45, InterpolateNearest, false, nil, "43d4c8dfdaed13b7"},
		{"pillow/median-rgba-source.png", 45, InterpolateBilinear, true, nil, "5d2d2a83333e3b9c"},
		{"pillow/median-rgba-source.png", 90, InterpolateNearest, false, nil, "72eece478c355cbf"},
		{"pillow/median-rgba-source.png", 90, InterpolateNearest, true, nil, "bc5339e8fa553b75"},
		{"pillow/median-rgba-source.png", 90, InterpolateBilinear, false, nil, "ce482cd683536fad"},
		{"pillow/median-rgba-source.png", 90, InterpolateBilinear, true, nil, "bc5339e8fa553b75"},
		{"pillow/median-rgba-source.png", 90, InterpolateBicubic, false, nil, "3c991c69569a446c"},
		{"pillow/median-rgba-source.png", 90, InterpolateBicubic, true, nil, "bc5339e8fa553b75"},
		{"pillow/median-rgba-source.png", 200, InterpolateNearest, false, nil, "f19ddf5c53b5d91e"},
		{"pillow/median-rgba-source.png", 200, InterpolateBilinear, true, nil, "328da87d5332a58c"},
		{"pillow/median-rgba-source.png", 0.5, InterpolateNearest, false, nil, "59742034e1bece2b"},
		{"pillow/median-rgba-source.png", 0.5, InterpolateBilinear, true, nil, "05af21a0531dda0c"},
		{"pillow/median-rgba-source.png", 359.99, InterpolateNearest, false, nil, "04598fdf7ccb5e43"},
		{"pillow/median-rgba-source.png", 359.99, InterpolateBilinear, true, nil, "aa5af9390ef174cf"},
		{"pillow/median-rgba-source.png", -123.4, InterpolateNearest, false, nil, "2533a82992438496"},
		{"pillow/median-rgba-source.png", -123.4, InterpolateBilinear, false, color.NRGBA{10, 200, 30, 128}, "4b59d4e64dac34ba"},
		{"pillow/median-rgba-source.png", -123.4, InterpolateBilinear, true, color.NRGBA{10, 200, 30, 128}, "37e4735e0bd3972a"},
		{"pillow/median-rgba-source.png", -123.4, InterpolateBicubic, false, nil, "2f59358364dde44f"},
		{"pillow/median-rgba-source.png", -123.4, InterpolateBicubic, false, color.NRGBA{10, 200, 30, 128}, "d657901ca984e3f7"},
		{"pillow/median-rgba-source.png", -123.4, InterpolateBicubic, true, color.NRGBA{10, 200, 30, 128}, "e31f998649d4fde6"},
		{"pillow/gray.png", 30, InterpolateBilinear, false, nil, "2422576d02150cb3"},
		{"pillow/gray.png", 30, InterpolateBilinear, false, color.Gray{120}, "6f434bab36c0b4cf"},
		{"pillow/gray.png", 30, InterpolateBilinear, true, color.Gray{120}, "a03085ac374d620e"},
		{"pillow/gray.png", 30, InterpolateBicubic, false, color.Gray{120}, "abee7463c79c07de"},
		{"pillow/gray.png", 30, InterpolateBicubic, true, nil, "b8165682be427579"},
		{"pillow/gray.png", 30, InterpolateBicubic, true, color.Gray{120}, "8d0c47a2123fb035"},
		{"pillow/gray.png", -17.5, InterpolateNearest, true, nil, "d7f99ac236096f9c"},
		{"pillow/gray.png", -17.5, InterpolateBicubic, false, nil, "3c033e788d0ff5f3"},
		{"pillow/gray.png", 45, InterpolateNearest, true, nil, "efc122988cfdbebf"},
		{"pillow/gray.png", 45, InterpolateBicubic, false, nil, "2bf7bdbe55947165"},
		{"pillow/gray.png", 90, InterpolateNearest, false, nil, "8d9f39ff334d44a9"},
		{"pillow/gray.png", 90, InterpolateNearest, true, nil, "1940c60a042bb1e1"},
		{"pillow/gray.png", 90, InterpolateBilinear, false, nil, "4e55150a5b382ca7"},
		{"pillow/gray.png", 90, InterpolateBilinear, true, nil, "1940c60a042bb1e1"},
		{"pillow/gray.png", 90, InterpolateBicubic, false, nil, "377340167f5db852"},
		{"pillow/gray.png", 90, InterpolateBicubic, true, nil, "1940c60a042bb1e1"},
		{"pillow/gray.png", 200, InterpolateNearest, true, nil, "32bc64f40803e2be"},
		{"pillow/gray.png", 200, InterpolateBicubic, false, nil, "5c281c1a5415555f"},
		{"pillow/gray.png", 0.5, InterpolateNearest, true, nil, "353cc6fdc5ac1bc6"},
		{"pillow/gray.png", 0.5, InterpolateBicubic, false, nil, "55cd664294c8144b"},
		{"pillow/gray.png", 359.99, InterpolateNearest, true, nil, "5dd8c8ae307884d0"},
		{"pillow/gray.png", 359.99, InterpolateBicubic, false, nil, "3964dca9a525a307"},
		{"pillow/gray.png", -123.4, InterpolateNearest, true, nil, "270160687b320237"},
		{"pillow/gray.png", -123.4, InterpolateBilinear, false, color.Gray{120}, "bfd392e89e13028c"},
		{"pillow/gray.png", -123.4, InterpolateBilinear, true, nil, "b4725b57a8448254"},
		{"pillow/gray.png", -123.4, InterpolateBilinear, true, color.Gray{120}, "4a42b351104df41c"},
		{"pillow/gray.png", -123.4, InterpolateBicubic, false, color.Gray{120}, "61a4a225c7e51393"},
		{"pillow/gray.png", -123.4, InterpolateBicubic, true, color.Gray{120}, "a999df3db8625b50"},
	} {
		for _, threads := range []int{1, 4} {
			o := RotateOptions{Method: MethodPillow, Interpolator: c.interp, Expand: c.expand, Background: c.bg}
			out, err := FromBytes(sourcePNG(t, c.in)).Threads(threads).Rotate(c.deg, o).PNG().ToBytes()
			if err != nil {
				t.Fatal(err)
			}
			if got := rgbaSum(t, decodePNG(t, out)); got != c.sum {
				t.Errorf("%s %v %+v %v, %d threads: %s, Pillow's is %s", c.in, c.deg, o, c.bg, threads, got, c.sum)
				break
			}
		}
	}
	// By name, and the words of the steps.
	in := sourcePNG(t, "pillow/median-rgba-source.png")
	o := RotateOptions{Method: MethodPillow, Interpolator: InterpolateBicubic, Expand: true}
	want := must(FromBytes(in).Rotate(33, o).PNG().ToBytes())
	var words []string
	got := must(FromBytes(in).Do("rotate", "33", "pillow", "bicubic", "expand").Around(func(s Step, src pipeline.Source, run func(pipeline.Source) (pipeline.Source, error)) (pipeline.Source, error) {
		words = append([]string{s.Name}, s.Args...)
		return run(src)
	}).PNG().ToBytes())
	if !bytes.Equal(got, want) {
		t.Error("rotate by name: another image")
	}
	again := must(FromBytes(in).Do(words[0], words[1:]...).PNG().ToBytes())
	if !bytes.Equal(again, want) {
		t.Errorf("the words %v give another image", words)
	}
	for _, args := range [][]string{{"33", "pillow", "pillow"}, {"33", "red", "blue"}, {"33", "nearest", "bicubic"}, {"33", "sideways"}} {
		if _, err := FromBytes(in).Do("rotate", args...).PNG().ToBytes(); err == nil {
			t.Errorf("rotate %v accepted", args)
		}
	}
	for _, o := range []RotateOptions{{Method: 3}, {Interpolator: 9}} {
		if _, err := FromBytes(in).Rotate(10, o).PNG().ToBytes(); err == nil {
			t.Errorf("Rotate(%+v) accepted", o)
		}
	}
	// The size is known without the pixels.
	if w, h, err := FromBytes(in).Rotate(30, RotateOptions{Method: MethodPillow, Expand: true}).OutputSize(); err != nil || w != 182 || h != 165 {
		t.Errorf("OutputSize %dx%d, %v", w, h, err)
	}
}
