package tests

import (
	"bytes"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/whhhh1500/goimgtools"
)

// onWhite is an image laid on white, as 8-bit RGB values.
func onWhite(m image.Image) []int {
	b := m.Bounds()
	out := make([]int, 0, 3*b.Dx()*b.Dy())
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := color.NRGBAModel.Convert(m.At(x, y)).(color.NRGBA)
			for _, v := range []uint8{c.R, c.G, c.B} {
				out = append(out, (int(v)*int(c.A)+255*(255-int(c.A))+127)/255)
			}
		}
	}
	return out
}

// TestSVGAsLibrsvg: shapes, paths (arcs, curves, dashes, joins, caps),
// gradients (focal, reflected, inherited), groups with opacity, CSS,
// symbols, clip paths, masks, embedded pictures, patterns, markers, filters and switch look
// as librsvg draws
// them (sharp 0.35.5's PNGs): differences only along edges, where the
// anti-aliasing differs.
func TestSVGAsLibrsvg(t *testing.T) {
	// The text is set in Arial, as librsvg drew it: where that is not
	// installed (Linux, mostly), another font draws it.
	hasArial := false
	for _, f := range SystemFonts() {
		if strings.EqualFold(f.Family, "Arial") {
			hasArial = true
			break
		}
	}
	for _, name := range []string{"shapes", "paths", "gradients", "groups", "mask", "pattern", "marker", "filter", "filter2", "text"} {
		if name == "text" && !hasArial {
			continue
		}
		doc, err := os.ReadFile(filepath.Join("testdata", "sharp", "svg-"+name+".svg"))
		if err != nil {
			t.Fatal(err)
		}
		got, err := FromBytes(doc).ToImage()
		if err != nil {
			t.Fatal(name, err)
		}
		want, err := Open(filepath.Join("testdata", "sharp", "svg-"+name+".png")).ToImage()
		if err != nil {
			t.Fatal(err)
		}
		if got.Bounds().Size() != want.Bounds().Size() {
			t.Fatalf("%s: %v, librsvg %v", name, got.Bounds().Size(), want.Bounds().Size())
		}
		a, b := onWhite(got), onWhite(want)
		sum, far := 0, 0
		for i := range a {
			d := a[i] - b[i]
			if d < 0 {
				d = -d
			}
			sum += d
			if d > 32 {
				far++
			}
		}
		if mean := float64(sum) / float64(len(a)); mean > 0.5 || far > 30 {
			t.Errorf("%s: mean difference %.3f, %d values more than 32 apart", name, mean, far)
		}
	}
}

// TestSVG: the size from width, height and viewBox; a vector resized as
// it is drawn, its edges as sharp as at its own size; read only; a broken
// document is an error.
func TestSVG(t *testing.T) {
	for _, c := range []struct {
		doc  string
		w, h int
	}{
		{`<svg width="40" height="30"/>`, 40, 30},
		{`<svg viewBox="0 0 80 20"/>`, 80, 20},
		{`<svg width="160" viewBox="0 0 80 20"/>`, 160, 40},
		{`<svg height="1in" viewBox="0 0 2 1"/>`, 192, 96},
		{`<?xml version="1.0"?><!-- a comment --><svg xmlns="http://www.w3.org/2000/svg"/>`, 100, 100},
	} {
		info, err := FromBytes([]byte(c.doc)).Metadata()
		if err != nil || info.Format != "svg" || info.Width != c.w || info.Height != c.h {
			t.Errorf("%s: %+v %v", c.doc, info, err)
		}
	}
	// Half red, half transparent: drawn at four times its size, the edge
	// falls between two pixels.
	doc := []byte(`<svg width="10" height="4"><rect width="5" height="4" fill="red"/></svg>`)
	m, err := FromBytes(doc).Resize(40, 16).ToImage()
	if err != nil {
		t.Fatal(err)
	}
	if b := m.Bounds(); b.Dx() != 40 || b.Dy() != 16 {
		t.Fatalf("resized to %v", b)
	}
	for _, x := range []int{0, 19, 20, 39} {
		c := color.NRGBAModel.Convert(m.At(x, 7)).(color.NRGBA)
		if want := (color.NRGBA{255, 0, 0, 255}); x < 20 && c != want || x >= 20 && c.A != 0 {
			t.Errorf("pixel %d: %v", x, c)
		}
	}
	if err := FromBytes(doc).ToFile(t.TempDir() + "/x.svg"); err == nil {
		t.Error("an SVG was written")
	}
	for _, bad := range []string{"<svg", "<html><body/></html>", "<?xml version=\"1.0\"?><nope/>"} {
		if _, err := FromBytes([]byte(bad)).PNG().ToBytes(); err == nil {
			t.Errorf("%q drawn", bad)
		}
	}
	// The same picture within a small memory limit.
	full, err := FromBytes(doc).Resize(400, 0).PNG().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	small, err := FromBytes(doc).Resize(400, 0).MemoryLimit(4 << 20).PNG().ToBytes()
	if err != nil || !bytes.Equal(full, small) {
		t.Errorf("within 4MB: %v", err)
	}
}
