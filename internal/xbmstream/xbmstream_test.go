package xbmstream

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// decode reads a whole file within env.
func decode(data []byte, env *pipeline.Env) (image.Image, error) {
	src, err := Open(bytes.NewReader(data), int64(len(data)), env)
	if err != nil {
		return nil, err
	}
	defer src.Close()
	if b := src.Bounds(); int64(b.Dx())*int64(b.Dy()) > 1<<22 {
		// Too large to put together (a damaged header): only read through.
		for {
			if _, err := src.Next(); err == io.EOF {
				return nil, nil
			} else if err != nil {
				return nil, err
			}
		}
	}
	m, err := pipeline.Collect(src)
	if err != nil {
		return nil, err
	}
	if m.Bounds() != src.Bounds() {
		return nil, fmt.Errorf("bounds %v, the source says %v", m.Bounds(), src.Bounds())
	}
	return m, nil
}

func fixtures(t testing.TB) map[string][]byte {
	t.Helper()
	names, _ := filepath.Glob("testdata/*.xbm")
	out := map[string][]byte{}
	for _, name := range names {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		out[strings.TrimSuffix(filepath.Base(name), ".xbm")] = data
	}
	return out
}

// TestPillow: the files Pillow 12.1.1 wrote, and one ffmpeg 9.0.2 wrote,
// come out pixel for pixel as Pillow reads them, but for the two colours:
// Pillow has a set bit white, the PNGs beside the files are its pixels the
// other way round. (ffmpeg, given a picture, sets the bits of its black
// pixels: read here it is the picture again.)
func TestPillow(t *testing.T) {
	files := fixtures(t)
	if len(files) != 5 {
		t.Fatalf("%d fixtures, want 5", len(files))
	}
	for name, data := range files {
		if !Match(data[:min(32, len(data))]) {
			t.Errorf("%s: not matched", name)
		}
		f, err := os.Open("testdata/" + name + ".png")
		if err != nil {
			t.Fatal(err)
		}
		want, err := png.Decode(f)
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
		m, err := decode(data, nil)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got, ok := m.(*image.Gray)
		if !ok {
			t.Fatalf("%s: decoded as %T", name, m)
		}
		if got.Bounds() != want.Bounds() {
			t.Fatalf("%s: bounds %v, want %v", name, got.Bounds(), want.Bounds())
		}
		b := got.Bounds()
		black := 0
		for y := 0; y < b.Dy(); y++ {
			for x := 0; x < b.Dx(); x++ {
				g, w := got.GrayAt(x, y).Y, want.(*image.Gray).GrayAt(x, y).Y
				if g != w {
					t.Fatalf("%s: pixel %d,%d is %d, want %d", name, x, y, g, w)
				}
				if g == 0 {
					black++
				}
			}
		}
		if black == 0 && b.Dx() > 1 {
			t.Errorf("%s: no black pixel", name)
		}
	}
}

// ink reports whether a pixel of the images built here is set.
func ink(x, y int) bool { return (x/3^y/5)&1 != 0 || x == y }

// style is a way of writing the file.
type style struct {
	name     string
	decl     string // the declaration of the array
	prefix   string // of the names
	eol      string
	number   func(v byte) string
	sep      string // between the numbers
	perLine  int
	before   string // the lines before the sizes
	hot      bool
	comments bool
	end      string
}

// write is the text of a bitmap of w by h in the style.
func (s style) write(w, h int) []byte {
	var b strings.Builder
	eol := s.eol
	if eol == "" {
		eol = "\n"
	}
	b.WriteString(s.before)
	fmt.Fprintf(&b, "#define %swidth %d%s#define %sheight %d%s", s.prefix, w, eol, s.prefix, h, eol)
	if s.hot {
		fmt.Fprintf(&b, "#define %sx_hot 3%s#define %sy_hot -1%s", s.prefix, eol, s.prefix, eol)
	}
	decl := s.decl
	if decl == "" {
		decl = "static char %sbits[] = {"
	}
	fmt.Fprintf(&b, decl, s.prefix)
	b.WriteString(eol)
	number := s.number
	if number == nil {
		number = func(v byte) string { return fmt.Sprintf("0x%02x", v) }
	}
	sep := s.sep
	if sep == "" {
		sep = ", "
	}
	perLine := s.perLine
	if perLine == 0 {
		perLine = 12
	}
	i, total := 0, (w+7)/8*h
	for y := 0; y < h; y++ {
		for x := 0; x < w; x += 8 {
			var v byte
			for k := 0; k < 8; k++ {
				// The bits past the width are set: they are not pixels.
				if x+k >= w || ink(x+k, y) {
					v |= 1 << k
				}
			}
			b.WriteString(number(v))
			i++
			if i < total {
				b.WriteString(sep)
				if i%perLine == 0 {
					if s.comments {
						fmt.Fprintf(&b, " /* 0x%02x, %d */ // 0x11,", i, i)
					}
					b.WriteString(eol)
				}
			}
		}
	}
	end := s.end
	if end == "" {
		end = "};" + eol
	}
	b.WriteString(end)
	return []byte(b.String())
}

// picture is the image write stores.
func picture(w, h int) *image.Gray {
	m := image.NewGray(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if !ink(x, y) {
				m.Pix[y*m.Stride+x] = 255
			}
		}
	}
	return m
}

var styles = []style{
	{name: "as bitmap(1) writes it", prefix: "im_"},
	{name: "unsigned", prefix: "a_b_", decl: "static unsigned char %sbits[] = {"},
	{name: "const", prefix: "x_", decl: "static const unsigned char %sbits[] =\n{"},
	{name: "no static", prefix: "x_", decl: "unsigned char\t%sbits [ ] = {"},
	{name: "names without a prefix", prefix: ""},
	{name: "a hot spot", prefix: "im_", hot: true},
	{name: "CR LF", prefix: "im_", eol: "\r\n", hot: true},
	{name: "CR", prefix: "im_", eol: "\r"},
	{name: "capitals", prefix: "im_", number: func(v byte) string { return fmt.Sprintf("0X%02X", v) }},
	{name: "one digit", prefix: "im_", number: func(v byte) string { return fmt.Sprintf("0x%x", v) }},
	{name: "decimal", prefix: "im_", number: func(v byte) string { return fmt.Sprint(v) }},
	{name: "octal", prefix: "im_", number: func(v byte) string { return fmt.Sprintf("0%o", v) }},
	{name: "more than a byte", prefix: "im_", number: func(v byte) string { return fmt.Sprintf("0xffffff%02x", v) }},
	{name: "no spaces", prefix: "im_", sep: ",", perLine: 1 << 30},
	{name: "a number a line", prefix: "im_", sep: ",", perLine: 1},
	{name: "comments", prefix: "im_", comments: true, before: "/* made by hand: #define x_width 1 */\n\n  // and so on\n"},
	{name: "space first", prefix: "im_", before: "\n \t\n"},
	{name: "a comma at the end", prefix: "im_", end: ",\n};\n"},
	{name: "nothing after the numbers", prefix: "im_", end: " "},
	{name: "no end at all", prefix: "im_", end: "\x00"},
}

// TestStyles: the ways the text is written, built here.
func TestStyles(t *testing.T) {
	for _, s := range styles {
		for _, size := range [][2]int{{37, 20}, {40, 3}, {1, 1}, {8, 9}, {9, 2}} {
			w, h := size[0], size[1]
			file := s.write(w, h)
			if s.end == "\x00" {
				file = file[:len(file)-1]
			}
			if s.before == "" && !Match(file[:min(32, len(file))]) {
				t.Errorf("%s: not matched", s.name)
			}
			m, err := decode(file, nil)
			if err != nil {
				t.Fatalf("%s, %dx%d: %v\n%s", s.name, w, h, err, file)
			}
			got, want := m.(*image.Gray), picture(w, h)
			if got.Bounds() != want.Bounds() {
				t.Fatalf("%s: bounds %v, want %v", s.name, got.Bounds(), want.Bounds())
			}
			for y := 0; y < h; y++ {
				for x := 0; x < w; x++ {
					if g, w := got.GrayAt(x, y).Y, want.GrayAt(x, y).Y; g != w {
						t.Fatalf("%s: pixel %d,%d is %d, want %d\n%s", s.name, x, y, g, w, file)
					}
				}
			}
		}
	}
	src, err := Open(strings.NewReader(string(styles[0].write(9, 2))), int64(len(styles[0].write(9, 2))), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !pipeline.IsOpaque(src) {
		t.Error("not opaque")
	}
	src.Close()
}

// TestRefused: what is not an XBM file, or one of a kind not read.
func TestRefused(t *testing.T) {
	for _, c := range []struct {
		name, text string
		want       error
	}{
		{"empty", "", errFormat},
		{"no height", "#define a_width 8\nstatic char a_bits[] = { 0x00 };", errFormat},
		{"no width", "#define a_height 8\nstatic char a_bits[] = { 0x00 };", errFormat},
		{"no array", "#define a_width 8\n#define a_height 1\n", errFormat},
		{"no brace", "#define a_width 8\n#define a_height 1\nstatic char a_bits[] = 0x00;", errFormat},
		{"width 0", "#define a_width 0\n#define a_height 1\nstatic char a_bits[] = { 0x00 };", errFormat},
		{"width below 0", "#define a_width -8\n#define a_height 1\nstatic char a_bits[] = { 0x00 };", errFormat},
		{"width not a number", "#define a_width eight\n#define a_height 1\nstatic char a_bits[] = { 0x00 };", errFormat},
		{"width too large", "#define a_width 16777217\n#define a_height 1\nstatic char a_bits[] = { 0x00 };", errFormat},
		{"height too large", "#define a_width 1\n#define a_height 99999999999999999999\nstatic char a_bits[] = { 0x00 };", errFormat},
		{"not define", "#include <a_width 8>\n#define a_height 1\nstatic char a_bits[] = { 0x00 };", errFormat},
		{"an array of int", "#define a_width 8\n#define a_height 1\nstatic int a_bits[] = { 0x00 };", errFormat},
		{"X10", "#define a_width 16\n#define a_height 1\nstatic short a_bits[] = { 0x0000 };", errUnsupported},
		{"X10, unsigned", "#define a_width 16\n#define a_height 1\nstatic unsigned short a_bits[] = { 0x0000 };", errUnsupported},
		{"a number missing", "#define a_width 16\n#define a_height 2\nstatic char a_bits[] = { 0x00, 0x01, 0x02 };", io.ErrUnexpectedEOF},
		{"a number missing, no brace", "#define a_width 16\n#define a_height 2\nstatic char a_bits[] = { 0x00, 0x01, 0x02,    ", io.ErrUnexpectedEOF},
		{"not a number", "#define a_width 16\n#define a_height 1\nstatic char a_bits[] = { 0x00, zero };", errFormat},
		{"no digits", "#define a_width 16\n#define a_height 1\nstatic char a_bits[] = { 0x00, 0x };", errFormat},
		{"not a digit", "#define a_width 16\n#define a_height 1\nstatic char a_bits[] = { 0x00, 0x0g };", errFormat},
		{"not an octal digit", "#define a_width 16\n#define a_height 1\nstatic char a_bits[] = { 0x00, 08 };", errFormat},
		{"a sign", "#define a_width 16\n#define a_height 1\nstatic char a_bits[] = { 0x00, -1 };", errFormat},
		{"a comment left open", "#define a_width 16\n#define a_height 1\nstatic char a_bits[] = { 0x00, /* 0x01 };", io.ErrUnexpectedEOF},
		{"a stroke", "#define a_width 16\n#define a_height 1\nstatic char a_bits[] = { 0x00 / 0x01 };", errFormat},
	} {
		if _, err := decode([]byte(c.text), nil); err != c.want {
			t.Errorf("%s: %v, want %v", c.name, err, c.want)
		}
	}
	for _, head := range []string{"", "\n", "/* XPM */\nstatic char *a[] = {", "#include <stdio.h>", "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR", "P4\n8 8\n", "# define a 1"} {
		if Match([]byte(head)) {
			t.Errorf("%q matched", head)
		}
	}
	if !Match([]byte(" \r\n\t#define im_width 40\n#define im_")) {
		t.Error("space before #define: not matched")
	}
}

// drain reads data to the end within env; whatever the file, the budget
// is given back.
func drain(t *testing.T, name string, data []byte, env *pipeline.Env) error {
	t.Helper()
	_, err := decode(data, env)
	if used := env.Budget.Used(); used != 0 {
		t.Fatalf("%s: %d bytes still reserved", name, used)
	}
	return err
}

// TestDamage: a file cut short or with bytes changed is read or refused,
// without a panic, within a budget of 1MB.
func TestDamage(t *testing.T) {
	env := pipeline.NewEnv(1<<20, t.TempDir())
	files := fixtures(t)
	for _, s := range styles {
		files[s.name] = s.write(37, 20)
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names) // an order that does not change from run to run
	trials := 0
	for _, name := range names {
		data := files[name]
		// Cut before the first digit of its last number a file is short.
		text := strings.TrimRight(string(data), "};, \r\n\x00")
		last := strings.LastIndexAny(text, ", \r\n") + 1
		for cut := 0; cut < len(data); cut++ {
			err := drain(t, name, data[:cut], env)
			if err == nil && cut <= last {
				t.Errorf("%s: read from its first %d of %d bytes", name, cut, len(data))
			}
			trials++
		}
	}
	rng := rand.New(rand.NewPCG(5, 6))
	for i := 0; i < 8000; i++ {
		name := names[rng.IntN(len(names))]
		data := bytes.Clone(files[name])
		for k := 1 + rng.IntN(4); k > 0; k-- {
			at := rng.IntN(len(data))
			if rng.IntN(2) == 0 {
				at = rng.IntN(min(len(data), 80)) // the header
			}
			switch rng.IntN(3) {
			case 0:
				data[at] = byte(rng.Uint32())
			case 1: // a character such a file is written with
				data[at] = "0123456789abcdefxX,{}#/* \n_-"[rng.IntN(28)]
			default: // one taken out
				data = append(data[:at], data[at+1:]...)
			}
		}
		drain(t, name, data, env)
		trials++
	}
	t.Logf("%d damaged files", trials)
}

// TestHuge: a header that claims far more pixels than the file has
// numbers for fails without the image being allocated.
func TestHuge(t *testing.T) {
	for _, size := range [][2]int{{maxSide, maxSide}, {maxSide, 1}, {8, maxSide}, {4000, 4000}} {
		file := []byte(fmt.Sprintf("#define a_width %d\n#define a_height %d\nstatic char a_bits[] = {\n0x00, 0x01, 0x02 };\n", size[0], size[1]))
		env := pipeline.NewEnv(1<<20, t.TempDir())
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		err := drain(t, "huge", file, env)
		runtime.ReadMemStats(&after)
		if !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Errorf("%v: %v", size, err)
		}
		if got := after.TotalAlloc - before.TotalAlloc; got > 1<<20 {
			t.Errorf("%v: %d bytes allocated", size, got)
		}
	}
	// A row wider than the budget, the numbers all there.
	file := styles[0].write(3<<20, 1)
	env := pipeline.NewEnv(1<<20, t.TempDir())
	if err := drain(t, "wide", file, env); !errors.Is(err, pipeline.ErrMemoryLimit) {
		t.Errorf("a row of 3MB within 1MB: %v", err)
	}
}

// TestMemory: 3000 by 2000 pixels (6MB decoded, 3.7MB of text) are read
// in bands, within a few hundred KB.
func TestMemory(t *testing.T) {
	const w, h = 3000, 2000
	file := styles[0].write(w, h)
	env := pipeline.NewEnv(8<<20, t.TempDir())
	src, err := Open(bytes.NewReader(file), int64(len(file)), env)
	if err != nil {
		t.Fatal(err)
	}
	y, strips := 0, 0
	for {
		m, err := src.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b := m.(*image.Gray)
		if b.Rect.Min.Y != y || b.Rect.Dx() != w {
			t.Fatalf("strip %v at row %d", b.Rect, y)
		}
		for ; y < b.Rect.Max.Y; y++ {
			p := b.Pix[b.PixOffset(0, y):]
			for x := 0; x < w; x++ {
				if (p[x] == 0) != ink(x, y) || p[x] != 0 && p[x] != 255 {
					t.Fatalf("pixel %d,%d is %d", x, y, p[x])
				}
			}
		}
		strips++
	}
	if y != h || strips < 2 {
		t.Fatalf("%d rows in %d strips", y, strips)
	}
	peak := env.Budget.Peak()
	src.Close()
	t.Logf("file %d bytes, %d strips, peak %d bytes", len(file), strips, peak)
	if peak > 512<<10 {
		t.Errorf("peak %d bytes", peak)
	}
	if used := env.Budget.Used(); used != 0 {
		t.Errorf("%d bytes still reserved", used)
	}
}

// FuzzOpen: any file is read or refused, without a panic, within the
// memory it reserves.
func FuzzOpen(f *testing.F) {
	for _, data := range fixtures(f) {
		f.Add(data)
	}
	for _, s := range styles {
		f.Add(s.write(19, 5))
	}
	dir := f.TempDir() // one for every input: making one each is slow
	f.Fuzz(func(t *testing.T, data []byte) {
		env := pipeline.NewEnv(4<<20, dir)
		src, err := Open(bytes.NewReader(data), int64(len(data)), env)
		if err == nil {
			for {
				if _, err := src.Next(); err != nil {
					break
				}
			}
			src.Close()
		}
		if env.Budget.Used() != 0 {
			t.Fatalf("%d bytes reserved", env.Budget.Used())
		}
	})
}
