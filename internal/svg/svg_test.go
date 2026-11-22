package svg

import (
	"bytes"
	"math"
	"testing"

	"github.com/whhhh1500/goimgtools/pipeline"
)

const sample = `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" width="157" height="113" viewBox="0 0 100 72">
  <style>.c { stroke: #036; stroke-width: 1.5 }</style>
  <defs>
    <radialGradient id="r" fx="0.3" fy="0.3"><stop offset="0" stop-color="#fff"/><stop offset="1" stop-color="#a30" stop-opacity="0.8"/></radialGradient>
    <clipPath id="k"><circle cx="70" cy="30" r="22"/></clipPath>
    <mask id="m"><rect width="100" height="72" fill="#888"/></mask>
    <symbol id="s" viewBox="0 0 4 4"><path d="M0 0h4L2 4z"/></symbol>
  </defs>
  <rect x="3.3" y="2.7" width="40" height="30" rx="5" fill="url(#r)" class="c"/>
  <g transform="rotate(17.5 70 30)" clip-path="url(#k)" opacity="0.8">
    <path d="M50 10 c10-5 30 5 40 0 s0 30-10 40 a20 15 30 0 1-30-10z" fill="teal" stroke="black" stroke-width="3" stroke-linejoin="miter"/>
  </g>
  <polyline points="5,60 20,40 35,62 50,41" fill="none" stroke="purple" stroke-width="4" stroke-dasharray="7 3" stroke-linecap="round"/>
  <use xlink:href="#s" x="60" y="50" width="16" height="16" fill="green" mask="url(#m)"/>
</svg>`

// TestBands: the picture is the same whatever the height of the bands.
func TestBands(t *testing.T) {
	draw := func(rows int) []byte {
		s, err := Open([]byte(sample), pipeline.NewEnv(0, ""))
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		if err := s.start(); err != nil {
			t.Fatal(err)
		}
		if rows > 0 {
			s.r.n = min(rows, s.r.n)
		}
		var out []byte
		for {
			m, err := s.Next()
			if err != nil {
				break
			}
			r := m.Bounds()
			pix := s.r.pool[0][:4*r.Dx()*r.Dy()]
			out = append(out, pix...)
		}
		return out
	}
	ref := draw(0)
	if len(ref) != 4*157*113 {
		t.Fatalf("%d bytes", len(ref))
	}
	for _, rows := range []int{1, 3, 17} {
		if !bytes.Equal(draw(rows), ref) {
			t.Errorf("bands of %d rows: another picture", rows)
		}
	}
}

func TestParse(t *testing.T) {
	vp := viewport{200, 100}
	for _, c := range []struct {
		s    string
		axis int
		want float64
	}{{"10", axisX, 10}, {"1in", axisX, 96}, {"25.4mm", axisX, 96}, {"12pt", axisX, 16}, {"50%", axisX, 100}, {"50%", axisY, 50}, {"2em", axisX, 32}} {
		if v, ok := parseLength(c.s, 16, vp, c.axis); !ok || math.Abs(v-c.want) > 1e-9 {
			t.Errorf("%q: %v %v", c.s, v, ok)
		}
	}
	m, ok := parseTransform("translate(10,20) scale(2) rotate(90)")
	if x, y := m.apply(1, 0); !ok || math.Abs(x-10) > 1e-12 || math.Abs(y-22) > 1e-12 {
		t.Errorf("transform: %v %v %v", x, y, ok)
	}
	if v, ok := numbers("1-2.5.5e1,3"); !ok || len(v) != 4 || v[1] != -2.5 || v[2] != 5 {
		t.Errorf("numbers: %v", v)
	}
	// Arc flags written together, and a quarter circle's end.
	segs := parsePath("M10 0A10 10 0 0110 20")
	if len(segs) < 2 || segs[len(segs)-1].pts[2] != [2]float64{10, 20} {
		t.Errorf("arc: %+v", segs)
	}
}

// FuzzOpen: any document is drawn or refused, without a panic, within the
// memory it reserves.
func FuzzOpen(f *testing.F) {
	f.Add([]byte(sample))
	f.Add([]byte(`<svg width="10" height="10"><text x="1" y="8" font-size="8">ab</text></svg>`))
	f.Add([]byte(`<svg viewBox="0 0 1 1"><use href="#a"/><g id="a"><use href="#a"/></g></svg>`))
	f.Add([]byte(`<svg width="24" height="16"><filter id="f"><feTurbulence baseFrequency=".2" numOctaves="2" stitchTiles="stitch" result="t"/><feMorphology radius="1" operator="dilate" in="SourceGraphic"/><feConvolveMatrix order="3" kernelMatrix="0 1 0 1 -4 1 0 1 0" edgeMode="wrap"/><feDisplacementMap in2="t" scale="4" xChannelSelector="R"/><feTile x="2" y="2" width="5" height="5"/><feDiffuseLighting in="SourceAlpha" surfaceScale="3"><fePointLight x="4" y="4" z="9"/></feDiffuseLighting><feSpecularLighting specularExponent="8"><feSpotLight x="1" y="1" z="5" pointsAtX="9" limitingConeAngle="40"/></feSpecularLighting><feImage href="#c" x="1" width="9"/></filter><circle id="c" cx="8" cy="8" r="5" filter="url(#f)"/></svg>`))
	f.Add([]byte(`<svg width="40" height="20" font-size="8" letter-spacing="1"><path id="p" d="M0 15 Q20 0 40 15"/><text text-decoration="underline line-through overline"><textPath href="#p" startOffset="10%">ab c</textPath></text><text x="1" y="9" rotate="10 -20" textLength="30" lengthAdjust="spacingAndGlyphs" dominant-baseline="central">x<tspan baseline-shift="super" word-spacing="2">y z</tspan></text></svg>`))
	// Text shaped by its font's tables: clusters moved, turned, coloured
	// apart, stretched; right to left.
	f.Add([]byte(`<svg width="60" height="30" font-size="12" font-family="'` + testFont + `'"><text x="1" y="12" dx="0 2 2" rotate="0 5" textLength="50">नमस्ते <tspan fill="red">क्ष</tspan>ि ◌ा</text><text x="58" y="26" direction="rtl" letter-spacing="1">abc אב (क) ‏١٢</text></svg>`))
	// Encodings, entities, pictures that are SVG, CSS transforms and
	// origins, context paint, paint order, a root's background.
	f.Add(append([]byte(`<?xml version="1.0" encoding="windows-1251"?><!DOCTYPE svg [<!ENTITY c "green">]><svg width="9" height="9" style="background: #ffd; transform: rotate(5deg)"><rect width="9" height="9" fill="&c;" transform-origin="center" transform="scale(.5)"/><text>`), 0xCF, 0xF0, '<', '/', 't', 'e', 'x', 't', '>', '<', '/', 's', 'v', 'g', '>'))
	f.Add([]byte{0xFF, 0xFE, '<', 0, 's', 0, 'v', 0, 'g', 0, ' ', 0, 'w', 0, 'i', 0, 'd', 0, 't', 0, 'h', 0, '=', 0, '"', 0, '4', 0, '"', 0, '/', 0, '>', 0})
	f.Add([]byte(`<svg width="20" height="20"><style>[x] { transform: translate(1px, 10%) } rect[fill$="n"] { paint-order: markers stroke }</style><marker id="m" orient="0.1turn"><path d="M0 0h3v3z" fill="context-stroke"/></marker><defs><path id="p" d="M1 1h9v9" fill="context-fill"/></defs><use href="#p" fill="url(#g) red" stroke="blue" marker-mid="url(#m)"/><path d="M2 2h9v9" stroke="red" marker-mid="url(#m)" paint-order="markers"/><image width="9" height="9" href="data:image/svg+xml;utf8,%3Csvg width='4' height='4'%3E%3Cimage width='2' height='2' href='data:image/svg+xml;utf8,%3Csvg/%3E'/%3E%3C/svg%3E"/></svg>`))
	// HTML in a foreignObject: blocks, breaks, backgrounds, an SVG in it.
	f.Add([]byte(`<svg width="40" height="30" style="text-align:center"><style>@import url(a);</style><style>span{background:#ff0}</style><foreignObject width="40" height="30"><div xmlns="http://www.w3.org/1999/xhtml" style="line-height:1.5;white-space:pre"><p>a<br/>b <strong>c</strong></p><h1>d</h1><svg width="9" height="50%" viewBox="0 0 2 1"><foreignObject width="5" height="5"><b>e</b></foreignObject></svg><script>x</script></div></foreignObject></svg>`))

	// Filter functions of CSS, a url among them; media queries.
	f.Add([]byte(`<svg width="20" height="20"><style>@media (prefers-color-scheme: light) and screen { rect { fill: red } }</style><filter id="a" filterUnits="userSpaceOnUse" x="2" width="9"><feFlood/></filter><rect width="9" height="9" filter="blur(1px) url(#a) drop-shadow(red 1 2 3) hue-rotate(.5turn) sepia(20%) opacity(2) contrast() url(#none)"/><g filter="url(#a)"><g filter="url(#a)"><circle r="5"/></g></g></svg>`))

	f.Fuzz(func(t *testing.T, data []byte) {
		env := pipeline.NewEnv(16<<20, "")
		s, err := Open(data, env)
		if err != nil {
			if env.Budget.Used() != 0 {
				t.Fatalf("%d bytes reserved after an error", env.Budget.Used())
			}
			return
		}
		if b := s.Bounds(); int64(b.Dx())*int64(b.Dy()) > 1<<20 {
			s.ScaleOnLoad(min(b.Dx(), 512), min(b.Dy(), 512))
		}
		for {
			if _, err := s.Next(); err != nil {
				break
			}
		}
		s.Close()
		if env.Budget.Used() != 0 {
			t.Fatalf("%d bytes reserved", env.Budget.Used())
		}
	})
}
