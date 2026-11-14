package ops

import (
	"image"
	"image/color"
	"image/draw"
	"reflect"
	"testing"

	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

func same(t *testing.T, what string, want, got image.Image) {
	t.Helper()
	if want.Bounds() != got.Bounds() {
		t.Fatalf("%s: bounds %v, want %v", what, got.Bounds(), want.Bounds())
	}
	b := want.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if want.At(x, y) != got.At(x, y) {
				t.Fatalf("%s: pixel (%d,%d) = %v, want %v", what, x, y, got.At(x, y), want.At(x, y))
			}
		}
	}
}

func collect(t *testing.T, src pipeline.Source, env *pipeline.Env) image.Image {
	t.Helper()
	got, err := pipeline.Collect(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := src.Close(); err != nil {
		t.Fatal(err)
	}
	if env.Budget.Used() != 0 {
		t.Fatalf("%d bytes still reserved after Close", env.Budget.Used())
	}
	return got
}

type subImager interface {
	SubImage(image.Rectangle) image.Image
}

// TestCrop compares with SubImage for every pixel type, for regions that
// end inside the image (the last strip must survive draining the source)
// and at its bottom edge, including odd offsets into subsampled chroma.
func TestCrop(t *testing.T) {
	regions := []image.Rectangle{
		image.Rect(0, 0, 97, 131), image.Rect(10, 20, 60, 90), image.Rect(11, 21, 12, 22),
		image.Rect(3, 100, 97, 131), image.Rect(31, 33, 64, 48),
	}
	for name, m := range testutil.Images(97, 131) {
		for i, r := range regions {
			env := pipeline.NewEnv(0, "")
			src, err := Crop(testutil.ChoppedEnv(m, []int{1, 7, 16, 500, 13}[i], env), r.Min.X, r.Min.Y, r.Dx(), r.Dy())
			if err != nil {
				t.Fatal(err)
			}
			got := collect(t, src, env)
			want := m.(subImager).SubImage(r)
			if reflect.TypeOf(got) != reflect.TypeOf(want) {
				t.Fatalf("%s: type %T, want %T", name, got, want)
			}
			same(t, name, want, got)
		}
	}
	m := testutil.Images(20, 20)["rgba"]
	for _, bad := range [][4]int{{0, 0, 21, 20}, {-1, 0, 5, 5}, {5, 5, 0, 5}, {19, 19, 2, 1}} {
		if _, err := Crop(testutil.Chopped(m, 4), bad[0], bad[1], bad[2], bad[3]); err == nil {
			t.Errorf("Crop%v: no error", bad)
		}
	}
	// A cropped source with a non-zero origin can be cropped again,
	// relative to its own corner.
	full := testutil.Images(97, 131)["nrgba"]
	a, _ := Crop(testutil.Chopped(full, 9), 10, 20, 60, 90)
	b, err := Crop(a, 5, 6, 30, 40)
	if err != nil {
		t.Fatal(err)
	}
	got, err := pipeline.Collect(b)
	if err != nil {
		t.Fatal(err)
	}
	same(t, "crop of crop", full.(subImager).SubImage(image.Rect(15, 26, 45, 66)), got)
}

func TestFlatten(t *testing.T) {
	bg := color.NRGBA{250, 240, 30, 255}
	for name, m := range testutil.Images(97, 131) {
		env := pipeline.NewEnv(0, "")
		got := collect(t, Flatten(testutil.ChoppedEnv(m, 7, env), bg), env)
		want := image.NewRGBA(m.Bounds())
		draw.Draw(want, want.Bounds(), image.NewUniform(bg), image.Point{}, draw.Src)
		draw.Draw(want, want.Bounds(), m, m.Bounds().Min, draw.Over)
		same(t, name, want, got)
		pix := got.(*image.RGBA).Pix
		for i := 3; i < len(pix); i += 4 {
			if pix[i] != 0xff {
				t.Fatalf("%s: flattened image is not opaque", name)
			}
		}
	}
	// A source that says it is opaque is passed through untouched.
	src := opaque{testutil.Chopped(testutil.Images(8, 8)["gray"], 4)}
	if Flatten(src, color.White) != pipeline.Source(src) {
		t.Error("opaque source was wrapped")
	}
}

type opaque struct{ pipeline.Source }

func (opaque) Opaque() bool { return true }

// TestOverlay places a translucent image at several positions, including
// partly and wholly outside the base image.
func TestOverlay(t *testing.T) {
	top := testutil.Images(30, 20)["nrgba"]
	for name, m := range testutil.Images(97, 131) {
		for i, at := range []image.Point{{0, 0}, {60, 100}, {-10, -5}, {90, 125}, {200, 200}, {40, 55}} {
			env := pipeline.NewEnv(0, "")
			src, err := Overlay(testutil.ChoppedEnv(m, []int{1, 7, 16, 500, 13, 3}[i], env), top, at.X, at.Y)
			if err != nil {
				t.Fatal(err)
			}
			got := collect(t, src, env)
			want := image.NewRGBA(m.Bounds())
			draw.Draw(want, want.Bounds(), m, m.Bounds().Min, draw.Src)
			draw.Draw(want, top.Bounds().Add(at), top, image.Point{}, draw.Over)
			same(t, name, want, got)
		}
	}
	// The overlay is charged to the budget.
	env := pipeline.NewEnv(1<<10, "")
	if _, err := Overlay(testutil.ChoppedEnv(top, 4, env), top, 0, 0); err == nil {
		t.Error("overlay larger than the budget: no error")
	}
}
