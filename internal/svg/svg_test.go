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
