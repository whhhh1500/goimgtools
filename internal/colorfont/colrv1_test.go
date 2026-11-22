package colorfont

import (
	"bytes"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"testing"

	"golang.org/x/image/font/sfnt"
)

// The test font of version 1 (made for these tests): A a box filled with a
// gradient from red to blue; B the box in green, scaled by half about its
// middle; C the box with a sweep about its middle.
const v1Font = "../../tests/testdata/fonts/ColrV1Test.ttf"

// recorder notes what a paint graph draws.
type recorder struct{ ops []string }

func (r *recorder) Transform(m [6]float64) { r.ops = append(r.ops, fmt.Sprintf("transform %g", m)) }
func (r *recorder) Clip(g uint16)          { r.ops = append(r.ops, fmt.Sprintf("clip %d", g)) }
func (r *recorder) ClipBox(x0, y0, x1, y1 float64) {
	r.ops = append(r.ops, fmt.Sprintf("box %g %g %g %g", x0, y0, x1, y1))
}
func (r *recorder) Pop() { r.ops = append(r.ops, "pop") }
func (r *recorder) Composite(mode int, backdrop, source func()) {
	r.ops = append(r.ops, fmt.Sprintf("composite %d", mode))
	backdrop()
	source()
}
func (r *recorder) Fill(b *Brush) {
	s := fmt.Sprintf("fill %d", b.Kind)
	for _, st := range b.Stops {
		s += fmt.Sprintf(" %g:%v:%g", st.Offset, st.Color, st.Alpha)
	}
	switch b.Kind {
	case BrushLinear:
		s += fmt.Sprintf(" %g", b.P)
	case BrushSweep:
		s += fmt.Sprintf(" %g %g..%g", b.P[0], b.Start, b.End)
	}
	r.ops = append(r.ops, s)
}

func TestPaint(t *testing.T) {
	data, err := os.ReadFile(v1Font)
	if err != nil {
		t.Fatal(err)
	}
	f, err := Open(bytes.NewReader(data), 0)
	if err != nil || f == nil {
		t.Fatalf("%v %v", f, err)
	}
	sf, err := sfnt.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	var b sfnt.Buffer
	id := func(r rune) uint16 {
		g, err := sf.GlyphIndex(&b, r)
		if err != nil {
			t.Fatal(err)
		}
		return uint16(g)
	}
	box := 5 // the glyph the paints clip to
	red, blue, green := "{255 0 0 255}", "{0 0 255 255}", "{0 128 0 255}"
	for _, c := range []struct {
		r    rune
		want string
	}{
		{'A', fmt.Sprintf("clip %d|fill 1 0:%s:1 1:%s:1 [[0 0] [1000 0] [0 1000]]|pop", box, red, blue)},
		{'B', fmt.Sprintf("transform [0.5 0 0 0.5 250 200]|clip %d|fill 0 0:%s:1|pop|pop", box, green)},
		{'C', fmt.Sprintf("clip %d|fill 3 0:%s:1 1:%s:1 [500 400] 0..360|pop", box, red, blue)},
	} {
		var rec recorder
		if !f.Paint(id(c.r), &rec) {
			t.Errorf("%q: no paint", c.r)
			continue
		}
		if got := strings.Join(rec.ops, "|"); got != c.want {
			t.Errorf("%q:\n got %s\nwant %s", c.r, got, c.want)
		}
	}
	if f.HasPaint(id(' ')) {
		t.Error("a space has a paint")
	}
}

// TestPaintDamaged: a COLR table changed at random draws what it can and
// never fails.
func TestPaintDamaged(t *testing.T) {
	data, err := os.ReadFile(v1Font)
	if err != nil {
		t.Fatal(err)
	}
	rnd := rand.New(rand.NewSource(1))
	for i := 0; i < 300; i++ {
		d := append([]byte(nil), data...)
		f0, _ := Open(bytes.NewReader(d), 0)
		if f0 == nil {
			t.Fatal("no colour glyphs")
		}
		// Bytes of the table itself, as it was read.
		for k := 0; k < 1+rnd.Intn(8); k++ {
			f0.colr[rnd.Intn(len(f0.colr))] = byte(rnd.Intn(256))
		}
		f0.baseList, f0.numPaints, f0.layerList, f0.clipList = 0, 0, 0, 0
		f0.v1(f0.colr) // read again, as Open reads it
		for g := 0; g < 8; g++ {
			var rec recorder
			f0.Paint(uint16(g), &rec)
		}
	}
}
