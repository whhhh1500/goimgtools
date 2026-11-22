package svg

import (
	"image"
	"testing"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// render draws a document of 100 by 100 pixels.
func render(t *testing.T, doc string) image.Image {
	t.Helper()
	s, err := Open([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="100" height="100">`+doc+`</svg>`), pipeline.NewEnv(0, ""))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	img, err := pipeline.Collect(s)
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func rgbaAt(img image.Image, x, y int) [4]uint32 {
	r, g, b, a := img.At(x, y).RGBA()
	return [4]uint32{r >> 8, g >> 8, b >> 8, a >> 8}
}

// TestBlendModes: mix-blend-mode blends an element with what is under it
// (multiply: the product, screen: its complement); as an attribute, it is
// not one (only CSS has it).
func TestBlendModes(t *testing.T) {
	under := `<rect width="100" height="100" fill="#8080ff"/>`
	for _, c := range []struct {
		top  string
		want [4]uint32
	}{
		{`<rect width="100" height="100" fill="#ff8000" style="mix-blend-mode:multiply"/>`, [4]uint32{128, 64, 0, 255}},
		{`<rect width="100" height="100" fill="#ff8000" style="mix-blend-mode:screen"/>`, [4]uint32{255, 191, 255, 255}},
		{`<rect width="100" height="100" fill="#ff8000" mix-blend-mode="multiply"/>`, [4]uint32{255, 128, 0, 255}},
		{`<g style="isolation:isolate"><rect width="100" height="100" fill="#ff8000" style="mix-blend-mode:multiply"/></g>`, [4]uint32{255, 128, 0, 255}},
	} {
		got := rgbaAt(render(t, under+c.top), 50, 50)
		for q := range got {
			if d := int(got[q]) - int(c.want[q]); d < -1 || d > 1 {
				t.Errorf("%s: %v, want %v", c.top, got, c.want)
				break
			}
		}
	}
}

// TestClipPaths: a child of a clip path clipped by a clip path of its own;
// a clip path that refers to itself through its children (the second time,
// no clip); clip-rule inherited where the clip path is; a clip path on the
// root.
func TestClipPaths(t *testing.T) {
	inside := func(doc string, x, y int) bool { return rgbaAt(render(t, doc), x, y)[3] > 128 }
	nested := `<clipPath id="a"><rect x="0" y="0" width="50" height="100"/></clipPath>
<clipPath id="b"><rect x="0" y="0" width="100" height="50" clip-path="url(#a)"/></clipPath>
<rect width="100" height="100" fill="green" clip-path="url(#b)"/>`
	if !inside(nested, 25, 25) || inside(nested, 75, 25) || inside(nested, 25, 75) {
		t.Error("a clipped child of a clip path")
	}
	recursive := `<clipPath id="a"><rect x="0" y="0" width="60" height="100" clip-path="url(#b)"/></clipPath>
<clipPath id="b"><rect x="40" y="0" width="60" height="100" clip-path="url(#a)"/></clipPath>
<rect width="100" height="100" fill="green" clip-path="url(#a)"/>`
	if !inside(recursive, 50, 50) || inside(recursive, 20, 50) || inside(recursive, 80, 50) {
		t.Error("a clip path that refers to itself")
	}
	rule := `<g clip-rule="evenodd"><clipPath id="a"><path d="M0 0h100v100h-100z M25 25h50v50h-50z"/></clipPath></g>
<rect width="100" height="100" fill="green" clip-path="url(#a)"/>`
	if inside(rule, 50, 50) || !inside(rule, 10, 10) {
		t.Error("clip-rule from where the clip path is")
	}
	s, err := Open([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="100" height="100" clip-path="url(#a)"><clipPath id="a"><rect width="50" height="50"/></clipPath><rect width="100" height="100" fill="green"/></svg>`), pipeline.NewEnv(0, ""))
	if err != nil {
		t.Fatal(err)
	}
	img, err := pipeline.Collect(s)
	s.Close()
	if err != nil {
		t.Fatal(err)
	}
	if rgbaAt(img, 25, 25)[3] != 255 || rgbaAt(img, 75, 75)[3] != 0 {
		t.Error("a clip path on the root")
	}
}
