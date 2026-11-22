package svg

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"fmt"
	"image"
	"net/url"
	"slices"
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

// renderDoc draws a whole document.
func renderDoc(t *testing.T, doc []byte) image.Image {
	t.Helper()
	s, err := Open(doc, pipeline.NewEnv(0, ""))
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

// TestDocumentForms: entities a doctype declares; encodings other than
// UTF-8 (by the XML declaration, or UTF-16 by its byte order mark); a root
// not displayed; the root's background colour; the root's transform, about
// its centre.
func TestDocumentForms(t *testing.T) {
	ent := `<?xml version="1.0"?><!DOCTYPE svg [<!ENTITY fill_value "green">]>
<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><rect width="10" height="10" fill="&fill_value;"/></svg>`
	if got := rgbaAt(renderDoc(t, []byte(ent)), 5, 5); got != [4]uint32{0, 128, 0, 255} {
		t.Errorf("an entity: %v", got)
	}
	// Windows-1251: "Привет" as a gradient's id, its bytes not UTF-8.
	name := string([]byte{0xCF, 0xF0, 0xE8, 0xE2, 0xE5, 0xF2})
	cyr := []byte(`<?xml version="1.0" encoding="Windows-1251"?><svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><linearGradient id="` +
		name + `"><stop stop-color="blue"/></linearGradient><rect width="10" height="10" fill="url(#` + name + `) red"/><text>` + name + `</text></svg>`)
	if root, err := parse(cyr, func(int64) error { return nil }); err != nil || root.children[0].attrs["id"] != "Привет" {
		t.Errorf("Windows-1251 read as %v", err)
	}
	if got := rgbaAt(renderDoc(t, cyr), 5, 5); got != [4]uint32{0, 0, 255, 255} {
		t.Errorf("Windows-1251: %v", got)
	}
	plain := `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><rect width="10" height="10" fill="green"/></svg>`
	for _, enc := range []string{"ISO-8859-1", "GB2312", "no-such-encoding"} {
		doc := `<?xml version="1.0" encoding="` + enc + `"?>` + plain
		if got := rgbaAt(renderDoc(t, []byte(doc)), 5, 5); got != [4]uint32{0, 128, 0, 255} {
			t.Errorf("%s: %v", enc, got)
		}
	}
	u16 := []byte{0xFF, 0xFE}
	for _, r := range `<?xml version="1.0" encoding="UTF-16"?>` + plain {
		u16 = append(u16, byte(r), byte(r>>8))
	}
	if !Match(u16[:64]) {
		t.Error("UTF-16 not taken for SVG")
	}
	if got := rgbaAt(renderDoc(t, u16), 5, 5); got != [4]uint32{0, 128, 0, 255} {
		t.Errorf("UTF-16: %v", got)
	}
	hidden := `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10" display="none"><rect width="10" height="10" fill="red"/></svg>`
	if got := rgbaAt(renderDoc(t, []byte(hidden)), 5, 5); got[3] != 0 {
		t.Errorf("a root not displayed: %v", got)
	}
	for _, c := range []struct {
		style string
		want  [4]uint32
	}{
		{`background-color: white`, [4]uint32{255, 255, 255, 255}},
		{`max-width: 100%; background-color: rgb(0, 0, 255);`, [4]uint32{0, 0, 255, 255}},
		{`background: #00ff00 none`, [4]uint32{0, 255, 0, 255}},
		{`background: transparent; background-color: transparent;`, [4]uint32{}},
		{`background-color: blue; background: none`, [4]uint32{}},
	} {
		doc := `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10" style="` + c.style + `"><rect width="5" height="5" fill="red"/></svg>`
		if got := rgbaAt(renderDoc(t, []byte(doc)), 7, 7); got != c.want {
			t.Errorf("%s: %v, want %v", c.style, got, c.want)
		}
	}
	// scale(0.5) about the centre: the square of 100 now from 25 to 75.
	tr := `<svg xmlns="http://www.w3.org/2000/svg" width="100" height="100" viewBox="0 0 10 10" transform="scale(0.5)"><rect width="10" height="10" fill="green"/></svg>`
	img := renderDoc(t, []byte(tr))
	if rgbaAt(img, 20, 20)[3] != 0 || rgbaAt(img, 30, 30)[3] != 255 || rgbaAt(img, 70, 70)[3] != 255 || rgbaAt(img, 80, 80)[3] != 0 {
		t.Error("the root's transform")
	}
}

// TestTransformOrigin: transform-origin as keywords, lengths and
// percentages of the viewport; on a gradient in a bounding box's units, of
// the box.
func TestTransformOrigin(t *testing.T) {
	for _, c := range []struct {
		origin string
		x, y   int // a pixel the square of 20 at (40, 40), scale(2), covers; and one it does not
		nx, ny int
	}{
		{`center`, 35, 35, 25, 25},       // about (50, 50): 30 to 70
		{`top left`, 85, 85, 75, 75},     // about (0, 0): 80 to 120
		{`50px 50px`, 35, 35, 25, 25},    // as center
		{`50% 50%`, 35, 35, 25, 25},      // as center
		{`right bottom`, 15, 15, 25, 25}, // about (100, 100): -20 to 20
		{`bottom`, 35, 15, 35, 25},       // about (50, 100): x 30 to 70, y -20 to 20
	} {
		img := render(t, `<rect x="40" y="40" width="20" height="20" fill="green" transform="scale(2)" transform-origin="`+c.origin+`"/>`)
		if rgbaAt(img, c.x, c.y)[3] != 255 || rgbaAt(img, c.nx, c.ny)[3] != 0 {
			t.Errorf("%s", c.origin)
		}
	}
	css := render(t, `<rect x="40" y="40" width="20" height="20" fill="green" transform="scale(2)" style="transform-origin: center"/>`)
	if rgbaAt(css, 35, 35)[3] != 255 {
		t.Error("transform-origin in CSS")
	}
	// A gradient of the bounding box, scale(2) about its centre: its
	// stops stretched from the centre, blue to the left edge.
	g := render(t, `<linearGradient id="g" gradientTransform="scale(2)" transform-origin="0.5 0.5"><stop offset="0.25" stop-color="blue"/><stop offset="0.75" stop-color="red"/></linearGradient>
<rect width="100" height="100" fill="url(#g)"/>`)
	if c := rgbaAt(g, 1, 50); c[2] < 250 || c[0] > 5 {
		t.Errorf("a gradient about its centre: %v", c)
	}
}

// TestHiddenTakesNoRoom: what display none hides is left out of the
// bounding box (a clip path in its units).
func TestHiddenTakesNoRoom(t *testing.T) {
	img := render(t, `<clipPath id="c" clipPathUnits="objectBoundingBox"><rect width="0.5" height="1"/></clipPath>
<g clip-path="url(#c)"><rect width="100" height="100" fill="red" display="none"/><rect x="50" width="50" height="100" fill="green"/></g>`)
	if rgbaAt(img, 60, 50)[3] != 255 || rgbaAt(img, 90, 50)[3] != 0 {
		t.Error("a hidden element in a bounding box")
	}
}

// TestGradientWithoutStops: a gradient with no stops, after which a paint
// follows, takes that paint.
func TestGradientWithoutStops(t *testing.T) {
	img := render(t, `<linearGradient id="g"/><rect width="100" height="100" fill="url(#g) green"/>`)
	if got := rgbaAt(img, 50, 50); got != [4]uint32{0, 128, 0, 255} {
		t.Errorf("%v", got)
	}
}

// TestCSSForms: the CSS transform property (over the attribute; "none"
// none), with units; a style element of another type left out; comments
// in a style attribute; attribute selectors.
func TestCSSForms(t *testing.T) {
	covered := func(doc string, x, y int) bool { return rgbaAt(render(t, doc), x, y)[3] > 128 }
	sq := `<rect x="10" y="10" width="20" height="20" fill="green" `
	if !covered(sq+`style="transform: scale(2)"/>`, 50, 50) || covered(sq+`style="transform: scale(2)"/>`, 15, 15) {
		t.Error("transform in a style attribute")
	}
	if !covered(`<style>#r { transform: translate(50px, 50%) }</style>`+sq+`id="r"/>`, 65, 65) {
		t.Error("transform in a style sheet")
	}
	if !covered(sq+`transform="scale(2)" style="transform:none"/>`, 15, 15) {
		t.Error("transform: none over the attribute")
	}
	if !covered(sq+`transform="translate(50 0)" style="transform: rotate(1foo)"/>`, 65, 15) {
		t.Error("a CSS transform that does not parse: the attribute")
	}
	// rotate(0.25turn) = rotate(90deg): (20, 20) to (-20, 20).
	if !covered(`<g transform="translate(50 0)">`+sq+`style="transform: rotate(0.25turn)"/></g>`, 25, 15) {
		t.Error("rotate in turns")
	}
	if !covered(`<style type="text/plain">rect { fill: none }</style><style type="text/css; charset=utf-8">#a { fill: green }</style><rect id="a" width="100" height="100" fill="red"/>`, 50, 50) {
		t.Error("style types")
	}
	if got := rgbaAt(render(t, `<rect width="100" height="100" style="/* a */fill:green/* b */"/>`), 50, 50); got != [4]uint32{0, 128, 0, 255} {
		t.Errorf("comments in a style attribute: %v", got)
	}
	for _, sel := range []string{`[x]`, `rect[x="20"]`, `[class~=b]`, `[id|=one]`, `[id^=on]`, `[id$="two"]`, `[id*=e-t]`, `[id="ONE-TWO" i]`, `[data-k='a b']`} {
		doc := `<style>` + sel + ` { fill: green }</style><rect id="one-two" class="a b" data-k="a b" x="20" width="80" height="100" fill="red"/>`
		if got := rgbaAt(render(t, doc), 50, 50); got != [4]uint32{0, 128, 0, 255} {
			t.Errorf("%s: %v", sel, got)
		}
	}
	for _, sel := range []string{`[y]`, `[x="2"]`, `[class~=c]`, `[id^=two]`, `[id=ONE-TWO]`} {
		doc := `<style>` + sel + ` { fill: red }</style><rect id="one-two" class="a b" x="20" width="80" height="100" fill="green"/>`
		if got := rgbaAt(render(t, doc), 50, 50); got != [4]uint32{0, 128, 0, 255} {
			t.Errorf("%s matched: %v", sel, got)
		}
	}
}

// TestContextPaint: context-fill and context-stroke (SVG 2) in a marker
// take the fill and stroke of its shape; in what a use element draws,
// the use element's; with no context, none.
func TestContextPaint(t *testing.T) {
	marker := `<marker id="m" markerWidth="20" markerHeight="20" refX="10" refY="10" markerUnits="userSpaceOnUse"><rect width="20" height="20" fill="context-stroke"/></marker>
<path d="M50 50 h40" stroke="blue" stroke-width="2" fill="none" marker-start="url(#m)"/>`
	if got := rgbaAt(render(t, marker), 45, 45); got != [4]uint32{0, 0, 255, 255} {
		t.Errorf("context-stroke in a marker: %v", got)
	}
	use := `<defs><rect id="r" width="40" height="40" fill="context-fill"/></defs>
<use href="#r" x="30" y="30" fill="green"/>`
	if got := rgbaAt(render(t, use), 50, 50); got != [4]uint32{0, 128, 0, 255} {
		t.Errorf("context-fill in a use: %v", got)
	}
	grad := `<linearGradient id="g"><stop offset="0" stop-color="blue"/><stop offset="1" stop-color="red"/></linearGradient>
<defs><rect id="r" width="40" height="40" fill="context-fill"/></defs>
<use href="#r" x="30" y="30" fill="url(#g)"/>`
	img := render(t, grad)
	if l, r := rgbaAt(img, 31, 50), rgbaAt(img, 68, 50); l[2] < 200 || r[0] < 200 {
		t.Errorf("a gradient over the use element's box: %v %v", l, r)
	}
	if got := rgbaAt(render(t, `<rect width="100" height="100" fill="context-fill"/>`), 50, 50); got[3] != 0 {
		t.Errorf("context-fill with no context: %v", got)
	}
}

// TestPaintOrder: markers placed by paint-order (first, between the fill
// and the stroke); a value with a part twice or one unknown is none.
func TestPaintOrder(t *testing.T) {
	doc := func(order string) string {
		return `<marker id="m" markerWidth="40" markerHeight="40" refX="10" refY="10" markerUnits="userSpaceOnUse"><rect width="20" height="20" fill="orange"/></marker>
<path d="M20 20 H80 V80 H20 Z" fill="green" stroke="blue" stroke-width="10" marker-start="url(#m)" paint-order="` + order + `"/>`
	}
	orange, blue, green := [4]uint32{255, 165, 0, 255}, [4]uint32{0, 0, 255, 255}, [4]uint32{0, 128, 0, 255}
	for _, c := range []struct {
		order       string
		edge, inner [4]uint32 // at (17, 17), under the stroke; at (27, 27), inside the fill
	}{
		{`normal`, orange, orange},
		{`markers`, blue, green},
		{`fill markers`, blue, orange},
		{`stroke markers`, orange, green},
		{`stroke stroke`, orange, orange},
		{`stroke markers fill qwe`, orange, orange},
	} {
		img := render(t, doc(c.order))
		if e, i := rgbaAt(img, 17, 17), rgbaAt(img, 27, 27); e != c.edge || i != c.inner {
			t.Errorf("%s: %v %v, want %v %v", c.order, e, i, c.edge, c.inner)
		}
	}
}

// TestSVGPicture: an image element whose picture is an SVG document (as
// text, base64, gzip-compressed) is drawn at the size it takes, as
// preserveAspectRatio fits it; pictures within pictures stop at a depth.
func TestSVGPicture(t *testing.T) {
	inner := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 20 10"><rect width="10" height="10" fill="green"/><rect x="10" width="10" height="10" fill="blue"/></svg>`
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	zw.Write([]byte(inner))
	zw.Close()
	for _, href := range []string{
		"data:image/svg+xml;utf8," + url.PathEscape(inner),
		"data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(inner)),
		"data:;base64," + base64.StdEncoding.EncodeToString(gz.Bytes()),
	} {
		// 100 by 100, meet: 100 by 50 from y = 25.
		img := render(t, `<image width="100" height="100" href="`+href+`"/>`)
		if l, r, out := rgbaAt(img, 25, 50), rgbaAt(img, 75, 50), rgbaAt(img, 50, 10); l != [4]uint32{0, 128, 0, 255} || r != [4]uint32{0, 0, 255, 255} || out[3] != 0 {
			t.Errorf("%.30s: %v %v %v", href, l, r, out)
		}
	}
	// slice: 200 by 100 from x = -50, cut to the box.
	img := render(t, `<image width="100" height="100" preserveAspectRatio="xMidYMid slice" href="data:image/svg+xml;utf8,`+url.PathEscape(inner)+`"/>`)
	if l, r := rgbaAt(img, 25, 10), rgbaAt(img, 75, 90); l != [4]uint32{0, 128, 0, 255} || r != [4]uint32{0, 0, 255, 255} {
		t.Errorf("slice: %v %v", l, r)
	}
	// Six deep: drawn to the fourth.
	doc := `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><rect width="10" height="10" fill="green"/></svg>`
	for i := 0; i < 6; i++ {
		doc = `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><image width="10" height="10" href="data:image/svg+xml;base64,` + base64.StdEncoding.EncodeToString([]byte(doc)) + `"/></svg>`
		if i == 2 {
			if got := rgbaAt(renderDoc(t, []byte(doc)), 5, 5); got != [4]uint32{0, 128, 0, 255} {
				t.Errorf("pictures nested three deep: %v", got)
			}
		}
	}
	if got := rgbaAt(renderDoc(t, []byte(doc)), 5, 5); got[3] != 0 {
		t.Errorf("pictures nested six deep: %v", got)
	}
}

// TestGradientForms: stops styled where the gradient is (currentColor of
// its ancestors; inherit of stop-color); geometry not taken from the other
// kind of gradient; a gradientTransform that does not parse passed over
// for the one it refers to; linearRGB interpolation; an attribute of
// another namespace not taken for the attribute.
func TestGradientForms(t *testing.T) {
	green := [4]uint32{0, 128, 0, 255}
	cur := `<g color="green"><linearGradient id="g"><stop offset="0" stop-color="currentColor"/><stop offset="1" stop-color="currentColor"/></linearGradient></g>
<rect width="100" height="100" fill="url(#g)" color="red"/>`
	if got := rgbaAt(render(t, cur), 50, 50); got != green {
		t.Errorf("currentColor where the gradient is: %v", got)
	}
	inh := `<linearGradient id="g" stop-color="green"><stop offset="0" stop-color="inherit"/><stop offset="1" stop-color="inherit"/></linearGradient>
<rect width="100" height="100" fill="url(#g)"/>`
	if got := rgbaAt(render(t, inh), 50, 50); got != green {
		t.Errorf("stop-color inherit: %v", got)
	}
	// A radial gradient referring to a linear one with cx: its own centre.
	cross := `<linearGradient id="l" cx="0"/><radialGradient id="r" href="#l"><stop offset="0" stop-color="white"/><stop offset="1" stop-color="black"/></radialGradient>
<rect width="100" height="100" fill="url(#r)"/>`
	if got := rgbaAt(render(t, cross), 50, 50); got[0] < 250 {
		t.Errorf("geometry from a linear gradient: %v", got)
	}
	// Vertical through the referred gradient: green at the top.
	bad := `<linearGradient id="a" gradientTransform="rotate(90 0.5 0.5)"><stop offset="0" stop-color="green"/><stop offset="1" stop-color="blue"/></linearGradient>
<linearGradient id="b" href="#a" gradientTransform="invalid"/><rect width="100" height="100" fill="url(#b)"/>`
	if top, bottom := rgbaAt(render(t, bad), 50, 2), rgbaAt(render(t, bad), 50, 97); top[1] < 120 || bottom[2] < 240 {
		t.Errorf("an invalid gradientTransform: %v %v", top, bottom)
	}
	lin := `<linearGradient id="g" color-interpolation="linearRGB"><stop offset="0" stop-color="black"/><stop offset="1" stop-color="white"/></linearGradient>
<rect width="101" height="10" fill="url(#g)"/>`
	if got := rgbaAt(render(t, lin), 50, 5); got[0] < 185 || got[0] > 191 {
		t.Errorf("linearRGB half way: %v, want about 188", got)
	}
	ns := `<rect width="100" height="100" fill="green" xmlns:x="http://example.org/x" x:fill="red" x:width="10"/>`
	if got := rgbaAt(render(t, ns), 80, 80); got != green {
		t.Errorf("an attribute of another namespace: %v", got)
	}
}

// TestUseForms: a use element's opacity over the symbol it draws; an svg
// element it draws keeps its x and y.
func TestUseForms(t *testing.T) {
	sym := `<symbol id="s"><rect width="100" height="100" fill="#008000"/></symbol><use href="#s" opacity="0.5"/>`
	if got := rgbaAt(render(t, sym), 50, 50); got[3] < 120 || got[3] > 135 {
		t.Errorf("opacity on a use of a symbol: %v", got)
	}
	svg := `<defs><svg id="s" x="40" y="40" width="20" height="20"><rect width="100" height="100" fill="green"/></svg></defs><use href="#s" width="30" height="30"/>`
	img := render(t, svg)
	if rgbaAt(img, 35, 35)[3] != 0 || rgbaAt(img, 65, 65)[3] != 255 || rgbaAt(img, 75, 75)[3] != 0 {
		t.Error("x and y of an svg element a use draws")
	}
}

// TestFilterForms: as Chrome draws them, a filter on an empty group draws
// nothing (in a bounding box's units); an input naming no result is the
// last one; a filter without primitives draws nothing.
func TestFilterForms(t *testing.T) {
	if got := rgbaAt(render(t, `<filter id="f"><feFlood flood-color="red"/></filter><g filter="url(#f)"/>`), 50, 50); got[3] != 0 {
		t.Errorf("a filter on an empty group: %v", got)
	}
	if got := rgbaAt(render(t, `<filter id="f" filterUnits="userSpaceOnUse" x="0" y="0" width="100" height="100"><feFlood flood-color="green"/></filter><g filter="url(#f)"/>`), 50, 50); got != [4]uint32{0, 128, 0, 255} {
		t.Errorf("a filter of the user space on an empty group: %v", got)
	}
	if got := rgbaAt(render(t, `<filter id="f"><feOffset dx="0" in="nothing"/></filter><rect width="100" height="100" fill="green" filter="url(#f)"/>`), 50, 50); got != [4]uint32{0, 128, 0, 255} {
		t.Errorf("an input naming no result: %v", got)
	}
	if got := rgbaAt(render(t, `<filter id="f"/><rect width="100" height="100" fill="green" filter="url(#f)"/>`), 50, 50); got[3] != 0 {
		t.Errorf("a filter without primitives: %v", got)
	}
	if got := rgbaAt(render(t, `<rect width="100" height="100" fill="green" filter="url(#missing)"/>`), 50, 50); got != [4]uint32{0, 128, 0, 255} {
		t.Errorf("a filter that is not there: %v", got)
	}
}

// TestShapeForms: as Chrome draws them, a transform attribute that does
// not parse (units in it) is none; a negative radius of an ellipse is
// auto; path data that does not start with a move is none; a gradient of
// a bounding box's units on a straight line takes the fallback.
func TestShapeForms(t *testing.T) {
	green := [4]uint32{0, 128, 0, 255}
	if got := rgbaAt(render(t, `<rect width="20" height="20" fill="green" transform="translate(50px, 50px)"/>`), 10, 10); got != green {
		t.Errorf("a transform with units: %v", got)
	}
	if got := rgbaAt(render(t, `<ellipse cx="50" cy="50" rx="-40" ry="40" fill="green"/>`), 50, 50); got != green {
		t.Errorf("a negative rx: %v", got)
	}
	if got := rgbaAt(render(t, `<path d="L 0 0 L 100 0 L 100 100 L 0 100 z" fill="green"/>`), 50, 50); got[3] != 0 {
		t.Errorf("path data without a move: %v", got)
	}
	line := `<linearGradient id="g"><stop offset="0" stop-color="red"/><stop offset="1" stop-color="red"/></linearGradient>
<line x1="0" y1="50" x2="100" y2="50" stroke="url(#g) green" stroke-width="10"/>`
	if got := rgbaAt(render(t, line), 50, 50); got != green {
		t.Errorf("a gradient on a straight line: %v", got)
	}
}

// TestTextBox: the bounding box of text is its characters' cells (a
// filter's region in its units covers them, not the picture).
func TestTextBox(t *testing.T) {
	doc := `<filter id="f" x="0" y="0" width="1" height="1"><feFlood flood-color="green"/></filter>
<text x="10" y="50" font-size="20" font-family="'` + testFont + `'" filter="url(#f)">नमस्ते</text>`
	img := render(t, doc)
	if got := rgbaAt(img, 15, 45); got != [4]uint32{0, 128, 0, 255} {
		t.Errorf("in the text's box: %v", got)
	}
	if got := rgbaAt(img, 90, 90); got[3] != 0 {
		t.Errorf("outside the text's box: %v", got)
	}
	// A mask on text, its region in the text's box (as by default): the
	// text is drawn.
	masked := render(t, `<mask id="m"><rect width="100" height="100" fill="white"/></mask>
<text x="10" y="50" font-size="20" font-family="'`+testFont+`'" mask="url(#m)">नमस्ते</text>`)
	inked := false
	for y := 30; y < 60 && !inked; y++ {
		for x := 10; x < 90; x++ {
			if rgbaAt(masked, x, y)[3] > 0 {
				inked = true
				break
			}
		}
	}
	if !inked {
		t.Error("a mask on text hid it")
	}
}

// TestPathAfterClose: a command after a close (Z) goes on from the start
// of the path closed; a filter takes its primitives and region from the
// filter it refers to.
func TestPathAfterClose(t *testing.T) {
	if got := rgbaAt(render(t, `<path d="M 0 0 L 50 0 L 50 50 Z L 0 100 L 50 100 Z" fill="green"/>`), 10, 80); got != [4]uint32{0, 128, 0, 255} {
		t.Errorf("a line after a close: %v", got)
	}
	doc := `<filter id="a" x="0" y="0" width="1" height="1"><feFlood flood-color="green"/></filter><filter id="b" href="#a"/>
<rect x="10" y="10" width="50" height="50" fill="red" filter="url(#b)"/>`
	if img := render(t, doc); rgbaAt(img, 30, 30) != [4]uint32{0, 128, 0, 255} || rgbaAt(img, 70, 70)[3] != 0 {
		t.Errorf("a filter by href: %v %v", rgbaAt(img, 30, 30), rgbaAt(img, 70, 70))
	}
}

// TestFontWeights: the weights of a family tried for one asked for, in
// the order CSS matches fonts (as Chrome picks Segoe UI Semibold for 600,
// Light for 300, Bold for 650); bolder and lighter by the parent's.
func TestFontWeights(t *testing.T) {
	for _, c := range []struct {
		w    int
		head []int
	}{
		{400, []int{400, 500, 350, 300}},
		{500, []int{500, 400, 350}},
		{300, []int{300, 200, 100, 350, 400}},
		{600, []int{600, 700, 800, 900, 500}},
		{650, []int{700, 800, 900, 600}},
		{900, []int{900, 800, 700}},
	} {
		if got := weightOrder(c.w); !slices.Equal(got[:len(c.head)], c.head) {
			t.Errorf("weightOrder(%d) = %v, want %v first", c.w, got, c.head)
		}
	}
	for _, c := range []struct {
		v           string
		parent, out int
	}{
		{"bold", 400, 700}, {"normal", 900, 400}, {"650", 400, 650}, {"1500", 400, 0},
		{"bolder", 500, 700}, {"bolder", 300, 400}, {"bolder", 600, 900}, {"bolder", 900, 900},
		{"lighter", 800, 700}, {"lighter", 600, 400}, {"lighter", 400, 100}, {"lighter", 50, 50},
	} {
		if got := weightValue(c.v, c.parent); got != c.out {
			t.Errorf("weightValue(%q, %d) = %d, want %d", c.v, c.parent, got, c.out)
		}
	}
}

// TestForeignObject: the HTML of a foreignObject (as mermaid writes its
// labels) is drawn as text: centred by text-align, lines broken by br and
// blocks, the backgrounds of inline elements under their text, an SVG
// document in it drawn as SVG; what browsers do not show (style elements)
// is not drawn.
func TestForeignObject(t *testing.T) {
	font := `font-family="'` + testFont + `'"`
	ink := func(img image.Image, x0, y0, x1, y1 int) (n int, minX, maxX int) {
		minX, maxX = 1<<30, -1
		for y := y0; y < y1; y++ {
			for x := x0; x < x1; x++ {
				if c := rgbaAt(img, x, y); c[3] > 0 && c[1] < 100 {
					n++
					minX, maxX = min(minX, x), max(maxX, x)
				}
			}
		}
		return n, minX, maxX
	}
	label := `<g ` + font + ` font-size="16" style="text-align:center"><foreignObject width="100" height="60"><div xmlns="http://www.w3.org/1999/xhtml" style="display: table-cell; white-space: nowrap;"><span>नमस्ते<br/>नमस्ते</span></div></foreignObject></g>`
	img := render(t, label)
	top, l1, r1 := ink(img, 0, 0, 100, 22)
	bottom, _, _ := ink(img, 0, 22, 100, 50)
	if top == 0 || bottom == 0 {
		t.Fatalf("two lines: %d and %d pixels of ink", top, bottom)
	}
	if mid := (l1 + r1) / 2; mid < 45 || mid > 55 {
		t.Errorf("a line centred: its ink from %d to %d", l1, r1)
	}
	if n, _, _ := ink(render(t, `<foreignObject width="100" height="100"><div xmlns="http://www.w3.org/1999/xhtml"><style>p { color: red }</style></div></foreignObject>`), 0, 0, 100, 100); n != 0 {
		t.Errorf("a style element drawn: %d pixels", n)
	}
	bg := `<foreignObject width="100" height="40" ` + font + `><div xmlns="http://www.w3.org/1999/xhtml"><span style="background-color: #0000ff">नमस्ते</span></div></foreignObject>`
	blue := 0
	bimg := render(t, bg)
	for y := 0; y < 40; y++ {
		for x := 0; x < 100; x++ {
			if c := rgbaAt(bimg, x, y); c[2] > 200 && c[0] < 50 {
				blue++
			}
		}
	}
	if blue < 50 {
		t.Errorf("the background of a span: %d pixels", blue)
	}
	// Words that do not fit the box's width wrap (white-space lets them);
	// with nowrap they stay on one line.
	words := func(ws string) image.Image {
		return render(t, `<foreignObject width="60" height="80" `+font+` font-size="16"><div xmlns="http://www.w3.org/1999/xhtml" style="white-space: `+ws+`">नमस्ते नमस्ते नमस्ते</div></foreignObject>`)
	}
	if n, _, _ := ink(words("normal"), 0, 22, 60, 60); n == 0 {
		t.Error("words not wrapped to the box")
	}
	if n, _, _ := ink(words("nowrap"), 0, 22, 60, 60); n != 0 {
		t.Errorf("words wrapped with nowrap: %d pixels below the first line", n)
	}
	if got := splitWords("a b-c  d 1-2"); !slices.Equal(got, []string{"a ", "b-", "c  ", "d ", "1-2"}) {
		t.Errorf("break opportunities: %q", got)
	}
	nested := `<foreignObject width="100" height="100"><div xmlns="http://www.w3.org/1999/xhtml"><svg xmlns="http://www.w3.org/2000/svg" width="50" height="50"><rect width="50" height="50" fill="green"/></svg></div></foreignObject>`
	if got := rgbaAt(render(t, nested), 25, 25); got != [4]uint32{0, 128, 0, 255} {
		t.Errorf("an SVG document in the HTML: %v", got)
	}
	// A sheet's broken end does not spill into the next; @import ends at
	// its semicolon.
	sheets := `<style>@import url("x.css");'</style><style>#r { fill: green }</style><rect id="r" width="100" height="100" fill="red"/>`
	// Media queries: those of a screen in the light scheme hold.
	for _, c := range []struct {
		query string
		holds bool
	}{
		{"(prefers-color-scheme: light)", true}, {"(prefers-color-scheme: dark)", false}, {"screen", true},
		{"print", false}, {"not print", true}, {"screen and (prefers-reduced-motion: reduce)", false},
		{"print, (prefers-color-scheme: light)", true}, {"only screen and (color)", true},
	} {
		doc := `<style>rect { fill: red } @media ` + c.query + ` { rect { fill: green } }</style><rect width="100" height="100"/>`
		if got := rgbaAt(render(t, doc), 50, 50) == [4]uint32{0, 128, 0, 255}; got != c.holds {
			t.Errorf("@media %s: applied %v", c.query, got)
		}
	}
	if got := rgbaAt(render(t, sheets), 50, 50); got != [4]uint32{0, 128, 0, 255} {
		t.Errorf("a rule after @import in another sheet: %v", got)
	}
}

// TestFilterFunctions: CSS's filter functions, as Chrome draws them: the
// colour functions, drop-shadow (its third length the blur's standard
// deviation), a list of them; values that are none (a percentage blur,
// an angle without a unit, commas) leave the element as it is; a filter
// used again within an element it is on.
func TestFilterFunctions(t *testing.T) {
	at := func(doc string, x, y int) [4]uint32 { return rgbaAt(render(t, doc), x, y) }
	rect := `<rect x="10" y="10" width="40" height="40" fill="#ff0000" filter="`
	near := func(got, want [4]uint32) bool {
		for q := range got {
			if d := int(got[q]) - int(want[q]); d < -2 || d > 2 {
				return false
			}
		}
		return true
	}
	for _, c := range []struct {
		filter string
		x, y   int
		want   [4]uint32
	}{
		{"grayscale(1)", 30, 30, [4]uint32{54, 54, 54, 255}},
		{"invert(100%)", 30, 30, [4]uint32{0, 255, 255, 255}},
		{"opacity(0.5)", 30, 30, [4]uint32{128, 0, 0, 128}}, // premultiplied
		{"brightness(0.5)", 30, 30, [4]uint32{128, 0, 0, 255}},
		{"drop-shadow(20px 20px #0000ff)", 65, 65, [4]uint32{0, 0, 255, 255}},
		{"drop-shadow(#0000ff 20 20)", 65, 65, [4]uint32{0, 0, 255, 255}},
		{"grayscale(1) invert(1)", 30, 30, [4]uint32{201, 201, 201, 255}},
		{"blur(50%)", 30, 30, [4]uint32{255, 0, 0, 255}},
		{"hue-rotate(90)", 30, 30, [4]uint32{255, 0, 0, 255}},
		{"drop-shadow(#0000ff, 20px, 20px)", 65, 65, [4]uint32{}},
	} {
		if got := at(rect+c.filter+`"/>`, c.x, c.y); !near(got, c.want) {
			t.Errorf("%s: %v, want %v", c.filter, got, c.want)
		}
	}
	nested := `<filter id="f"><feOffset dx="0"/></filter><g filter="url(#f)"><g filter="url(#f)"><rect width="100" height="100" fill="green"/></g></g>`
	if got := at(nested, 50, 50); got != [4]uint32{0, 128, 0, 255} {
		t.Errorf("a filter within an element it is on: %v", got)
	}
}

// TestForeignNamespace: an element of another namespace than SVG's is not
// drawn, whatever its name; one bound to SVG's by a prefix is.
func TestForeignNamespace(t *testing.T) {
	doc := `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><s:g xmlns="http://example.org/notsvg" xmlns:s="http://www.w3.org/2000/svg"><s:rect width="10" height="10" fill="green"/><rect width="10" height="10" fill="red"/></s:g></svg>`
	if got := rgbaAt(renderDoc(t, []byte(doc)), 5, 5); got != [4]uint32{0, 128, 0, 255} {
		t.Errorf("%v", got)
	}
}

// TestReviewFixes: lines break between the characters of Chinese and the
// like (not before closing punctuation) and not between inline elements
// within a word; a percentage line-height inherited from SVG; SVG's
// fill-opacity does not reach HTML; a filter function's region holds the
// strokes; currentColor of a filter function where it is used; an XHTML
// style sheet applies; a foreignObject has a box.
func TestReviewFixes(t *testing.T) {
	if got := splitWords("用户提交订单。然后ab cd"); !slices.Equal(got, []string{"用", "户", "提", "交", "订", "单。", "然", "后", "ab ", "cd"}) {
		t.Errorf("break opportunities: %q", got)
	}
	if breakBetween('b', 'c') || !breakBetween(' ', 'c') || !breakBetween('a', '中') || breakBetween('中', '，') || breakBetween('（', '中') {
		t.Error("breakBetween")
	}
	font := `font-family="'` + testFont + `'" font-size="16"`
	ink := func(img image.Image, y0, y1 int) (n, maxX int) {
		for y := y0; y < y1; y++ {
			for x := 0; x < 100; x++ {
				if c := rgbaAt(img, x, y); c[3] > 0 && c[1] < 100 {
					n++
					maxX = max(maxX, x)
				}
			}
		}
		return n, maxX
	}
	one := render(t, `<foreignObject width="100" height="30" `+font+`><div xmlns="http://www.w3.org/1999/xhtml">नमस्ते</div></foreignObject>`)
	_, w1 := ink(one, 0, 22)
	// One word fits; the second and third are one word (no space between
	// the bold and what follows), wrapped together.
	box := w1 + w1*7/10
	joined := render(t, fmt.Sprintf(`<foreignObject width="%d" height="60" %s><div xmlns="http://www.w3.org/1999/xhtml">नमस्ते <b>नम</b>स्ते</div></foreignObject>`, box, font))
	spaced := render(t, fmt.Sprintf(`<foreignObject width="%d" height="60" %s><div xmlns="http://www.w3.org/1999/xhtml">नमस्ते <b>नम</b> स्ते</div></foreignObject>`, box, font))
	_, xj := ink(joined, 0, 20)
	_, xsp := ink(spaced, 0, 20)
	if xj > w1+2 || xsp < w1+5 {
		t.Errorf("a line broken within a word: ink to %d (with a space between, %d), a word %d", xj, xsp, w1)
	}
	pct := render(t, `<g `+font+` style="line-height:150%"><foreignObject width="100" height="80"><div xmlns="http://www.w3.org/1999/xhtml">नमस्ते<br/>नमस्ते</div></foreignObject></g>`)
	if n, _ := ink(pct, 26, 50); n == 0 {
		t.Error("an inherited percentage line-height: the second line not below the first")
	}
	faded := render(t, `<g fill-opacity="0.3" `+font+`><foreignObject width="100" height="30"><div xmlns="http://www.w3.org/1999/xhtml">नमस्ते</div></foreignObject></g>`)
	full := false
	for y := 0; y < 30 && !full; y++ {
		for x := 0; x < 100; x++ {
			if rgbaAt(faded, x, y)[3] == 255 {
				full = true
				break
			}
		}
	}
	if !full {
		t.Error("SVG's fill-opacity on HTML text")
	}
	if got := rgbaAt(render(t, `<rect x="20" y="20" width="40" height="40" fill="red" stroke="#00f" stroke-width="10" style="filter:grayscale(1)"/>`), 17, 40); got[3] != 255 {
		t.Errorf("a stroke cut by a filter function's region: %v", got)
	}
	use := `<symbol id="s" overflow="visible"><rect width="10" height="10" fill="#000" style="filter: drop-shadow(30px 0px currentColor)"/></symbol><use href="#s" color="#f00"/><use href="#s" y="50" color="#00f"/>`
	img := render(t, use)
	if a, b := rgbaAt(img, 35, 5), rgbaAt(img, 35, 55); a != [4]uint32{255, 0, 0, 255} || b != [4]uint32{0, 0, 255, 255} {
		t.Errorf("currentColor of drop-shadow where it is used: %v %v", a, b)
	}
	xs := `<style xmlns="http://www.w3.org/1999/xhtml">rect { fill: green }</style><rect width="100" height="100" fill="red"/>`
	if got := rgbaAt(render(t, xs), 50, 50); got != [4]uint32{0, 128, 0, 255} {
		t.Errorf("an XHTML style sheet: %v", got)
	}
	fo := `<filter id="f"><feFlood flood-color="green"/></filter><foreignObject width="50" height="50" filter="url(#f)"><div xmlns="http://www.w3.org/1999/xhtml">x</div></foreignObject>`
	if got := rgbaAt(render(t, fo), 25, 25); got != [4]uint32{0, 128, 0, 255} {
		t.Errorf("a filter on a foreignObject: %v", got)
	}
}
