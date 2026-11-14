package ops_test

import (
	"image"
	"image/color"
	"image/draw"
	"path/filepath"
	"testing"

	"github.com/whhhh1500/goimgtools/internal/ops"
	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// turnRef applies o to m pixel by pixel.
func turnRef(m image.Image, o ops.Orientation) *image.NRGBA64 {
	// Planar strips (YCbCr) are turned as the 8-bit RGBA draw.Draw gives.
	// (No turn at all hands the source through untouched.)
	if _, _, _, _, ok := pipeline.Pix(m); !ok && o != ops.Upright {
		rgba := image.NewRGBA(m.Bounds())
		draw.Draw(rgba, rgba.Bounds(), m, m.Bounds().Min, draw.Src)
		m = rgba
	}
	b := m.Bounds()
	w, h := b.Dx(), b.Dy()
	dw, dh := o.Size(w, h)
	out := image.NewNRGBA64(image.Rect(0, 0, dw, dh))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dx, dy := x, y
			if o.Swap {
				dx, dy = y, x
			}
			if o.MirrorX {
				dx = dw - 1 - dx
			}
			if o.MirrorY {
				dy = dh - 1 - dy
			}
			out.Set(dx, dy, m.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return out
}

func sameAs(t *testing.T, what string, got image.Image, want *image.NRGBA64) {
	t.Helper()
	if got.Bounds() != want.Bounds() {
		t.Fatalf("%s: bounds %v, want %v", what, got.Bounds(), want.Bounds())
	}
	b := want.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if g := color.NRGBA64Model.Convert(got.At(x, y)); g != want.At(x, y) {
				t.Fatalf("%s: pixel (%d,%d) = %v, want %v", what, x, y, g, want.At(x, y))
			}
		}
	}
}

// TestOrient runs all eight orientations over every pixel type, with odd
// strip heights, in memory and with the pixels in a temporary file, and
// checks that what was reserved and stored is given back.
func TestOrient(t *testing.T) {
	all := []ops.Orientation{}
	for i := 0; i < 8; i++ {
		all = append(all, ops.Orientation{Swap: i&1 != 0, MirrorX: i&2 != 0, MirrorY: i&4 != 0})
	}
	imgs := testutil.Images(61, 43)
	run := func(name string, m image.Image, o ops.Orientation, limit int64, rows int) {
		t.Helper()
		dir := t.TempDir()
		env := pipeline.NewEnv(limit, dir)
		src := ops.Orient(testutil.ChoppedEnv(m, rows, env), o)
		got, err := pipeline.Collect(src)
		if err != nil {
			t.Fatalf("%s %+v limit %d: %v", name, o, limit, err)
		}
		sameAs(t, name, got, turnRef(m, o))
		if limit != 0 && env.Budget.Peak() > limit {
			t.Fatalf("%s %+v: peak %d over the limit %d", name, o, env.Budget.Peak(), limit)
		}
		src.Close()
		left, _ := filepath.Glob(filepath.Join(dir, "*"))
		if env.Budget.Used() != 0 || len(left) != 0 {
			t.Fatalf("%s %+v: %d bytes reserved, %d temp files left", name, o, env.Budget.Used(), len(left))
		}
	}
	for name, m := range imgs {
		for _, o := range all {
			for _, rows := range []int{1, 7, 500} {
				run(name, m, o, 0, rows)
			}
		}
	}
	// Larger images under a limit they do not fit in: the pixels go to a
	// temporary file (960KB of NRGBA64 against 600KB).
	big := testutil.Images(400, 300)
	for _, name := range []string{"nrgba64", "nrgba", "ycbcr420", "paletted"} {
		for _, o := range all {
			run(name, big[name], o, 600<<10, 7)
		}
	}
	// A sub-image source (bounds not at the origin).
	sub := imgs["nrgba"].(*image.NRGBA).SubImage(image.Rect(5, 9, 50, 40))
	for _, o := range all[1:] { // all[0] is no turn: the source itself
		got, err := pipeline.Collect(ops.Orient(testutil.Chopped(sub, 4), o))
		if err != nil {
			t.Fatal(err)
		}
		sameAs(t, "sub-image", got, turnRef(sub, o))
	}
	// Then composes like doing one after the other; two opposite turns
	// cancel into the source itself.
	m := imgs["nrgba"]
	for _, a := range all {
		for _, b := range all {
			got, err := pipeline.Collect(ops.Orient(ops.Orient(testutil.Chopped(m, 5), a), b))
			if err != nil {
				t.Fatal(err)
			}
			want := turnRef(turnRef(m, a), b)
			sameAs(t, "composed", got, want)
			if w, h := a.Then(b).Size(61, 43); image.Pt(w, h) != want.Bounds().Size() {
				t.Fatalf("%+v then %+v: Size disagrees", a, b)
			}
		}
	}
	base := testutil.Chopped(m, 5)
	turned := ops.Orient(base, ops.Rotate90)
	if ops.Orient(turned, ops.Rotate270) != base {
		t.Fatal("a quarter turn and its opposite did not cancel")
	}
	for v, want := range map[int]ops.Orientation{1: ops.Upright, 2: ops.Flop, 3: ops.Rotate180, 4: ops.Flip, 6: ops.Rotate90, 8: ops.Rotate270, 0: ops.Upright, 9: ops.Upright} {
		if ops.FromExif(v) != want {
			t.Errorf("FromExif(%d) = %+v", v, ops.FromExif(v))
		}
	}
}

// TestExtend adds borders and compares with drawing the image on a filled
// canvas.
func TestExtend(t *testing.T) {
	bg := color.NRGBA{10, 200, 30, 255}
	for name, m := range testutil.Images(37, 29) {
		for _, pad := range [][4]int{{0, 0, 0, 0}, {3, 0, 0, 0}, {0, 5, 0, 0}, {0, 0, 7, 0}, {0, 0, 0, 2}, {4, 60, 9, 70}} {
			for _, rows := range []int{1, 7, 500} {
				env := pipeline.NewEnv(0, "")
				src, err := ops.Extend(testutil.ChoppedEnv(m, rows, env), pad[0], pad[1], pad[2], pad[3], bg)
				if err != nil {
					t.Fatal(err)
				}
				got, err := pipeline.Collect(src)
				if err != nil {
					t.Fatalf("%s %v: %v", name, pad, err)
				}
				src.Close()
				w, h := 37+pad[0]+pad[2], 29+pad[1]+pad[3]
				if got.Bounds() != image.Rect(0, 0, w, h) {
					t.Fatalf("%s %v: bounds %v", name, pad, got.Bounds())
				}
				for y := 0; y < h; y++ {
					for x := 0; x < w; x++ {
						var want color.Color = bg
						if x >= pad[0] && x < pad[0]+37 && y >= pad[1] && y < pad[1]+29 {
							want = m.At(x-pad[0], y-pad[1])
						}
						if g, w := color.RGBAModel.Convert(got.At(x, y)), color.RGBAModel.Convert(want); g != w {
							t.Fatalf("%s %v: pixel (%d,%d) = %v, want %v", name, pad, x, y, g, w)
						}
					}
				}
				if env.Budget.Used() != 0 {
					t.Fatalf("%d bytes still reserved", env.Budget.Used())
				}
			}
		}
	}
	if _, err := ops.Extend(testutil.Chopped(testutil.Images(4, 4)["gray"], 1), -1, 0, 0, 0, bg); err == nil {
		t.Error("negative border accepted")
	}
}
