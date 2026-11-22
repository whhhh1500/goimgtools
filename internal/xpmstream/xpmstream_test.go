package xpmstream

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
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

// same fails unless every pixel of got is that of want.
func same(t *testing.T, name string, got, want image.Image) {
	t.Helper()
	if got.Bounds() != want.Bounds() {
		t.Fatalf("%s: bounds %v, want %v", name, got.Bounds(), want.Bounds())
	}
	b := got.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			g, w := color.NRGBAModel.Convert(got.At(x, y)), color.NRGBAModel.Convert(want.At(x, y))
			if g != w {
				t.Fatalf("%s: pixel %d,%d is %v, want %v", name, x, y, g, w)
			}
		}
	}
}

func fixtures(t testing.TB) map[string][]byte {
	t.Helper()
	names, _ := filepath.Glob("testdata/*.xpm")
	out := map[string][]byte{}
	for _, name := range names {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		out[strings.TrimSuffix(filepath.Base(name), ".xpm")] = data
	}
	return out
}

// TestPillow: files of 1, 2 and 3 characters a pixel, of 16 to 1000
// colours written #rrggbb, come out pixel for pixel as Pillow 12.1.1
// reads them (Pillow does not write XPM: a script wrote them). One has
// the colour None and names, which Pillow does not read: the PNG beside
// it is the file as ffmpeg 9.0.2 reads it.
func TestPillow(t *testing.T) {
	files := fixtures(t)
	if len(files) != 6 {
		t.Fatalf("%d fixtures, want 6", len(files))
	}
	for name, data := range files {
		if !Match(data[:32]) {
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
		got, err := decode(data, nil)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		same(t, name, got, want)
		wantKind := map[string]string{"c1_40x30": "*image.Paletted", "c1_37x29": "*image.Paletted", "c2_40x30": "*image.Paletted",
			"c2_many_37x29": "*image.NRGBA", "c3_40x30": "*image.NRGBA", "none_ffmpeg_37x29": "*image.Paletted"}[name]
		if k := fmt.Sprintf("%T", got); k != wantKind {
			t.Errorf("%s: decoded as %s, want %s", name, k, wantKind)
		}
	}
}

// TestColours: the ways a colour is written.
func TestColours(t *testing.T) {
	for _, c := range []struct {
		text string
		want color.NRGBA
	}{
		{"#fff", color.NRGBA{240, 240, 240, 255}}, // as X11 and ffmpeg read it
		{"#1aF", color.NRGBA{0x10, 0xa0, 0xf0, 255}},
		{"#12abEF", color.NRGBA{0x12, 0xab, 0xef, 255}},
		{"#123456789", color.NRGBA{0x12, 0x45, 0x78, 255}},
		{"#123456789abc", color.NRGBA{0x12, 0x56, 0x9a, 255}},
		{"#FFFF00008000", color.NRGBA{255, 0, 128, 255}},
		{"None", color.NRGBA{}},
		{"none", color.NRGBA{}},
		{"NONE", color.NRGBA{}},
		{"black", color.NRGBA{0, 0, 0, 255}},
		{"White", color.NRGBA{255, 255, 255, 255}},
		{"red", color.NRGBA{255, 0, 0, 255}},
		{"navy", color.NRGBA{0, 0, 128, 255}},
		{"LightBlue", color.NRGBA{0xad, 0xd8, 0xe6, 255}},
		{"light blue", color.NRGBA{0xad, 0xd8, 0xe6, 255}},
		{"Dark Slate Gray", color.NRGBA{0x2f, 0x4f, 0x4f, 255}},
		// Where X11 and CSS differ, X11's.
		{"gray", color.NRGBA{190, 190, 190, 255}},
		{"Grey", color.NRGBA{190, 190, 190, 255}},
		{"green", color.NRGBA{0, 255, 0, 255}},
		{"maroon", color.NRGBA{176, 48, 96, 255}},
		{"purple", color.NRGBA{160, 32, 240, 255}},
		{"gray0", color.NRGBA{0, 0, 0, 255}},
		{"gray10", color.NRGBA{26, 26, 26, 255}},
		{"grey30", color.NRGBA{77, 77, 77, 255}},
		{"gray50", color.NRGBA{127, 127, 127, 255}},
		{"gray51", color.NRGBA{130, 130, 130, 255}},
		{"gray70", color.NRGBA{179, 179, 179, 255}},
		{"Gray75", color.NRGBA{191, 191, 191, 255}},
		{"gray90", color.NRGBA{229, 229, 229, 255}},
		{"grey100", color.NRGBA{255, 255, 255, 255}},
	} {
		got, err := colourOf(c.text)
		if err != nil || got != c.want {
			t.Errorf("%s: %v, %v; want %v", c.text, got, err, c.want)
		}
	}
	for _, text := range []string{"#", "#ff", "#ffff", "#gggggg", "#1234567", "#+1+2+3", "#1234567890abcdef", "%10 20 30", "nosuchcolour",
		"rgb(1,2,3)", "gray101", "gray-1", "gray5x", "red3", "#ff0000 extra"} {
		if got, err := colourOf(text); err == nil {
			t.Errorf("%s: read as %v", text, got)
		}
	}
	for _, c := range []struct {
		text string
		want color.NRGBA
		bad  bool
	}{
		{"\tc #ff0000", color.NRGBA{255, 0, 0, 255}, false},
		{" c  #ff0000  ", color.NRGBA{255, 0, 0, 255}, false},
		{" s border m black c red", color.NRGBA{255, 0, 0, 255}, false},
		{" c red m black s border", color.NRGBA{255, 0, 0, 255}, false},
		{" m white g4 gray75 g gray80", color.NRGBA{204, 204, 204, 255}, false},
		{" m white g4 gray75", color.NRGBA{191, 191, 191, 255}, false},
		{" m white s a name", color.NRGBA{255, 255, 255, 255}, false},
		{" c light sea green m white", color.NRGBA{0x20, 0xb2, 0xaa, 255}, false},
		{" s mask c None", color.NRGBA{}, false},
		{" s name", color.NRGBA{}, true},
		{" c", color.NRGBA{}, true},
		{" #ff0000", color.NRGBA{}, true},
		{"", color.NRGBA{}, true},
		{" c no such colour", color.NRGBA{}, true},
	} {
		got, err := colourLine(c.text)
		if (err != nil) != c.bad || got != c.want {
			t.Errorf("%q: %v, %v; want %v", c.text, got, err, c.want)
		}
	}
}

const keyChars = " .XoO+@#$%&*=-;:>,<1234567890qwertyuipasdfghjklzxcvbnmMNBVCZASDFGHJKLPIUYTREWQ!~^/()_`'][{}|"

// sample is an XPM file built here.
type sample struct {
	name     string
	w, h     int
	chars    int
	colours  int      // made up, when values is empty
	none     bool     // the first of them None
	values   []string // the colours as written
	want     []color.NRGBA
	keys     []string // the characters of the colours, when not made up
	eol      string
	decl     string
	comments bool
	hot      bool
	ext      bool
	oneLine  bool
	extra    string // after the pixels of a row
	lines    func(i int, key string, value string) string
}

// index is the number of the colour of a pixel, of n colours.
func index(x, y, n int) int {
	if (x/5+y/3)%2 == 0 {
		return x / 5 % n
	}
	return (x*3 + y*5 + x*y) % n
}

// build writes the file of s and the image it holds.
func (s sample) build() ([]byte, *image.NRGBA) {
	values, want := s.values, s.want
	if len(values) == 0 {
		for i := 0; i < s.colours; i++ {
			c := color.NRGBA{uint8(i * 255 / max(1, s.colours-1)), uint8(i * 97), uint8(255 - i%256), 255}
			values = append(values, fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B))
			want = append(want, c)
		}
	}
	if s.none {
		values[0], want[0] = "None", color.NRGBA{}
	}
	keys := s.keys
	for i := 0; len(keys) < len(values); i++ {
		k, v := "", i
		for j := 0; j < s.chars; j++ {
			k += string(keyChars[v%len(keyChars)])
			v /= len(keyChars)
		}
		keys = append(keys, k)
	}
	eol := s.eol
	if eol == "" {
		eol = "\n"
	}
	sep := "," + eol
	if s.oneLine {
		eol, sep = "", ","
	}
	var b strings.Builder
	b.WriteString("/* XPM */" + eol)
	decl := s.decl
	if decl == "" {
		decl = "static char * image_xpm[] = {"
	}
	b.WriteString(decl + eol)
	if s.comments {
		b.WriteString("/* columns rows colors chars-per-pixel \"9 9 9 9\", */" + eol)
	}
	fmt.Fprintf(&b, "\"%d %d %d %d", s.w, s.h, len(values), s.chars)
	if s.hot {
		b.WriteString(" 3 4")
	}
	if s.ext {
		b.WriteString(" XPMEXT")
	}
	b.WriteString("\"" + sep)
	if s.comments {
		b.WriteString("/* colors */" + eol)
	}
	for i, v := range values {
		if s.lines != nil {
			b.WriteString("\"" + s.lines(i, keys[i], v) + "\"" + sep)
			continue
		}
		b.WriteString("\"" + keys[i] + " c " + v + "\"" + sep)
	}
	if s.comments {
		b.WriteString("/* pixels */" + eol + "/* \"" + eol + " */" + eol)
	}
	m := image.NewNRGBA(image.Rect(0, 0, s.w, s.h))
	for y := 0; y < s.h; y++ {
		b.WriteString("\"")
		for x := 0; x < s.w; x++ {
			i := index(x, y, len(values))
			b.WriteString(keys[i])
			m.SetNRGBA(x, y, want[i])
		}
		b.WriteString(s.extra + "\"")
		if y < s.h-1 || s.ext {
			b.WriteString(sep)
		} else {
			b.WriteString(eol)
		}
	}
	if s.ext {
		b.WriteString("\"XPMEXT author me\"" + sep + "\"line of the extension\"" + sep + "\"XPMENDEXT\"" + eol)
	}
	b.WriteString("};" + eol)
	return []byte(b.String()), m
}

var samples = []sample{
	{name: "1 character", w: 37, h: 20, chars: 1, colours: 7},
	{name: "1 character, every character", w: 37, h: 20, chars: 1, colours: len(keyChars)},
	{name: "2 characters", w: 37, h: 20, chars: 2, colours: 256},
	{name: "2 characters, colour", w: 37, h: 20, chars: 2, colours: 257},
	{name: "3 characters", w: 37, h: 20, chars: 3, colours: 600},
	{name: "4 characters", w: 37, h: 20, chars: 4, colours: 40},
	{name: "4 characters, colour", w: 40, h: 60, chars: 4, colours: 2000},
	{name: "one pixel", w: 1, h: 1, chars: 1, colours: 1},
	{name: "transparent", w: 37, h: 20, chars: 1,
		values: []string{"None", "#ff0000", "#00f", "black", "Light Blue", "#ffff00008000", "gray50"},
		want:   []color.NRGBA{{}, {255, 0, 0, 255}, {0, 0, 240, 255}, {0, 0, 0, 255}, {0xad, 0xd8, 0xe6, 255}, {255, 0, 128, 255}, {127, 127, 127, 255}}},
	{name: "transparent, colour", w: 37, h: 20, chars: 2, colours: 300, none: true},
	{name: "the other keys", w: 37, h: 20, chars: 1, colours: 5, lines: func(i int, key, value string) string {
		return key + [5]string{"\tc " + value, " s name m black c " + value, " m white g4 gray50 g gray30 c " + value,
			"  c  " + value + "  s  two words ", " c " + value + " m black"}[i]
	}},
	{name: "characters of C", w: 37, h: 20, chars: 2, colours: 9, keys: []string{"/*", "*/", "//", ", ", "};", "c ", " c", "  ", "{ "}},
	{name: "CR LF", w: 37, h: 20, chars: 1, colours: 7, eol: "\r\n"},
	{name: "comments", w: 37, h: 20, chars: 2, colours: 30, comments: true},
	{name: "a hot spot", w: 37, h: 20, chars: 1, colours: 7, hot: true},
	{name: "an extension", w: 37, h: 20, chars: 1, colours: 7, hot: true, ext: true},
	{name: "one line", w: 37, h: 20, chars: 1, colours: 7, oneLine: true},
	{name: "const", w: 37, h: 20, chars: 1, colours: 7, decl: "static const char *const image_xpm[] =\n{"},
	{name: "more in a row", w: 37, h: 20, chars: 2, colours: 7, extra: "!!!"},
}

// TestSamples: the kinds of file built here: 2 to 4 characters a pixel,
// the colour None, names, the other keys, comments and what else a file
// may hold.
func TestSamples(t *testing.T) {
	for _, s := range samples {
		file, want := s.build()
		if !Match(file[:min(32, len(file))]) {
			t.Errorf("%s: not matched", s.name)
		}
		got, err := decode(file, nil)
		if err != nil {
			t.Fatalf("%s: %v\n%.300s", s.name, err, file)
		}
		same(t, s.name, got, want)
		n := max(s.colours, len(s.values))
		kind := "*image.NRGBA"
		if n <= 256 {
			kind = "*image.Paletted"
		}
		if g := fmt.Sprintf("%T", got); g != kind {
			t.Errorf("%s: decoded as %s, want %s", s.name, g, kind)
		}
		if p, ok := got.(*image.Paletted); ok && len(p.Palette) != n {
			t.Errorf("%s: a palette of %d, want %d", s.name, len(p.Palette), n)
		}
		src, err := Open(bytes.NewReader(file), int64(len(file)), nil)
		if err != nil {
			t.Fatal(err)
		}
		if got, opaque := pipeline.IsOpaque(src), !strings.HasPrefix(s.name, "transparent"); got != opaque {
			t.Errorf("%s: opaque %v", s.name, got)
		}
		src.Close()
	}
	// Of two colours with the same characters the first counts.
	file := []byte("/* XPM */\nstatic char *a[] = {\n\"2 1 3 1\",\n\"a c #010203\",\n\"b c #040506\",\n\"a c #070809\",\n\"ab\"};\n")
	got, err := decode(file, nil)
	if err != nil {
		t.Fatal(err)
	}
	if c := color.NRGBAModel.Convert(got.At(0, 0)); c != (color.NRGBA{1, 2, 3, 255}) {
		t.Errorf("a colour given twice: %v", c)
	}
}

// TestRefused: what is not an XPM file of version 3, or is damaged.
func TestRefused(t *testing.T) {
	const head = "/* XPM */\nstatic char *a[] = {\n"
	long := strings.Repeat("x", maxLine)
	for _, c := range []struct {
		name, text string
		want       error
	}{
		{"empty", "", errFormat},
		{"XPM 2", "! XPM2\n2 1 2 1\na c #000000\nb c #ffffff\nab\n", errFormat},
		{"no strings", head + "};\n", io.ErrUnexpectedEOF},
		{"nothing after the comment", "/* XPM */", io.ErrUnexpectedEOF},
		{"three numbers", head + "\"2 1 2\",\n\"a c #000000\",\n\"b c #ffffff\",\n\"ab\"};\n", errFormat},
		{"not a number", head + "\"2 1 two 1\",\n\"a c #000000\",\n\"b c #ffffff\",\n\"ab\"};\n", errFormat},
		{"no width", head + "\"0 1 2 1\",\n\"a c #000000\",\n\"b c #ffffff\",\n\"ab\"};\n", errFormat},
		{"a height below 0", head + "\"2 -1 2 1\",\n\"a c #000000\",\n\"b c #ffffff\",\n\"ab\"};\n", errFormat},
		{"too wide", head + "\"16777217 1 2 1\",\n\"a c #000000\",\n\"b c #ffffff\",\n\"ab\"};\n", errFormat},
		{"no colours", head + "\"2 1 0 1\",\n\"ab\"};\n", errFormat},
		{"too many colours", head + "\"2 1 65537 3\",\n\"a c #000000\",\n\"b c #ffffff\",\n\"ab\"};\n", errFormat},
		{"5 characters", head + "\"2 1 2 5\",\n\"aaaaa c #000000\",\n\"bbbbb c #ffffff\",\n\"aaaaabbbbb\"};\n", errFormat},
		{"no characters", head + "\"2 1 2 0\",\n\"a c #000000\",\n\"b c #ffffff\",\n\"ab\"};\n", errFormat},
		{"a colour missing", head + "\"2 1 3 1\",\n\"a c #000000\",\n\"b c #ffffff\",\n\"ab\"};\n", errFormat},
		{"a colour too short", head + "\"2 1 2 2\",\n\"a\",\n\"bb c #ffffff\",\n\"aabb\"                  };\n", errFormat},
		{"a colour without a value", head + "\"2 1 2 1\",\n\"a c\",\n\"b c #ffffff\",\n\"ab\"};\n", errFormat},
		{"a colour without a key", head + "\"2 1 2 1\",\n\"a #000000\",\n\"b c #ffffff\",\n\"ab\"};\n", errFormat},
		{"a colour of a name only", head + "\"2 1 2 1\",\n\"a s name\",\n\"b c #ffffff\",\n\"ab\"};\n", errFormat},
		{"a string over lines", head + "\"2 1 2 1\",\n\"a c\n#000000\",\n\"b c #ffffff\",\n\"ab\"};\n", errFormat},
		{"a string too long", head + "\"2 1 2 1\",\n\"a c #000000 s " + long + "\",\n\"b c #ffffff\",\n\"ab\"};\n" + long, errFormat},
		{"characters of no colour", head + "\"2 1 2 1\",\n\"a c #000000\",\n\"b c #ffffff\",\n\"ac\"};\n", errFormat},
		{"a row too short", head + "\"3 2 2 1\",\n\"a c #000000\",\n\"b c #ffffff\",\n\"ab\",\n\"aba\"};\n", errFormat},
		{"a row missing", head + "\"2 3 2 1\",\n\"a c #000000\",\n\"b c #ffffff\",\n\"ab\",\n\"ab\"};\n  ", io.ErrUnexpectedEOF},
		{"a row left open", head + "\"2 2 2 1\",\n\"a c #000000\",\n\"b c #ffffff\",\n\"ab\",\n\"ab", io.ErrUnexpectedEOF},
		{"a comment left open", head + "\"2 2 2 1\",\n\"a c #000000\",\n\"b c #ffffff\",\n\"ab\",\n/* \"ab\"};\n", io.ErrUnexpectedEOF},
	} {
		if _, err := decode([]byte(c.text), nil); err != c.want {
			t.Errorf("%s: %v, want %v", c.name, err, c.want)
		}
	}
	_, err := decode([]byte(head+"\"2 1 2 1\",\n\"a c #000000\",\n\"b c NoSuchColour\",\n\"ab\"};\n"), nil)
	if err == nil || !strings.Contains(err.Error(), "NoSuchColour") {
		t.Errorf("a name X11 has not: %v", err)
	}
	for _, head := range []string{"", "/* XPM", " /* XPM */", "/* xpm */\nstatic char", "! XPM2\n2 1 2 1\n", "#define a_width 8\n", "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"} {
		if Match([]byte(head)) {
			t.Errorf("%q matched", head)
		}
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
	for _, s := range samples {
		files[s.name], _ = s.build()
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names) // an order that does not change from run to run
	trials := 0
	for _, name := range names {
		data := files[name]
		// Cut before the quote that closes its last row a file is short.
		last := bytes.LastIndexByte(data, '"')
		if i := bytes.Index(data, []byte("\"XPMEXT ")); i >= 0 {
			last = bytes.LastIndexByte(data[:i], '"')
		}
		for cut := 0; cut < len(data); cut += max(1, len(data)/500) {
			err := drain(t, name, data[:cut], env)
			if err == nil && cut <= last {
				t.Errorf("%s: read from its first %d of %d bytes", name, cut, len(data))
			}
			trials++
		}
	}
	rng := rand.New(rand.NewPCG(7, 8))
	for i := 0; i < 8000; i++ {
		name := names[rng.IntN(len(names))]
		data := bytes.Clone(files[name])
		for k := 1 + rng.IntN(4); k > 0; k-- {
			at := rng.IntN(len(data))
			if rng.IntN(2) == 0 {
				at = rng.IntN(min(len(data), 120)) // the sizes and the first colours
			}
			switch rng.IntN(3) {
			case 0:
				data[at] = byte(rng.Uint32())
			case 1: // a character such a file is written with
				data[at] = "0123456789abcdef \"\"\",#/*}\ncgms"[rng.IntN(30)]
			default: // one taken out
				data = append(data[:at], data[at+1:]...)
			}
		}
		drain(t, name, data, env)
		trials++
	}
	t.Logf("%d damaged files", trials)
}

// TestHuge: sizes that claim far more than the file holds fail without
// the image or the table of colours being allocated.
func TestHuge(t *testing.T) {
	for _, sizes := range []string{"16777216 16777216 2 1", "16777216 1 2 4", "1 16777216 2 1", "2 1 65536 4", "4000 4000 65536 4"} {
		file := []byte("/* XPM */\nstatic char *a[] = {\n\"" + sizes + "\",\n\"a c #000000\",\n\"b c #ffffff\",\n\"ab\"};\n")
		env := pipeline.NewEnv(1<<20, t.TempDir())
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		err := drain(t, sizes, file, env)
		runtime.ReadMemStats(&after)
		if !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Errorf("%s: %v", sizes, err)
		}
		if got := after.TotalAlloc - before.TotalAlloc; got > 1<<20 {
			t.Errorf("%s: %d bytes allocated", sizes, got)
		}
	}
	// 65536 colours, all there: their table is more than a budget of
	// 1MB has left, and is asked of the budget before it is made.
	file, _ := sample{w: 8, h: 8, chars: 3, colours: 65536}.build()
	env := pipeline.NewEnv(1<<20, t.TempDir())
	if err := drain(t, "65536 colours", file, env); !errors.Is(err, pipeline.ErrMemoryLimit) {
		t.Errorf("65536 colours within 1MB: %v", err)
	}
	env = pipeline.NewEnv(8<<20, t.TempDir())
	if err := drain(t, "65536 colours", file, env); err != nil {
		t.Errorf("65536 colours within 8MB: %v", err)
	}
	if peak := env.Budget.Peak(); peak < 1<<20 || peak > 3<<19 {
		t.Errorf("65536 colours: peak %d bytes", peak)
	}
}

// TestMemory: 3000 by 2000 pixels are read in bands, within a few hundred
// KB: of 90 colours (6MB decoded, 6MB of text) and of 3000 (24MB
// decoded, 12MB of text).
func TestMemory(t *testing.T) {
	const w, h = 3000, 2000
	for _, s := range []sample{
		{name: "90 colours", w: w, h: h, chars: 1, colours: 90},
		{name: "3000 colours", w: w, h: h, chars: 2, colours: 3000},
	} {
		file, _ := s.buildLarge()
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
			b := m.Bounds()
			if b.Min.Y != y || b.Dx() != w {
				t.Fatalf("strip %v at row %d", b, y)
			}
			for ; y < b.Max.Y; y++ {
				for x := 0; x < w; x++ {
					i := index(x, y, s.colours)
					want := color.NRGBA{uint8(i * 255 / (s.colours - 1)), uint8(i * 97), uint8(255 - i%256), 255}
					var got color.NRGBA
					switch p := m.(type) {
					case *image.Paletted:
						got = p.Palette[p.ColorIndexAt(x, y)].(color.NRGBA)
					case *image.NRGBA:
						got = p.NRGBAAt(x, y)
					}
					if got != want {
						t.Fatalf("%s: pixel %d,%d is %v, want %v", s.name, x, y, got, want)
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
		t.Logf("%s: file %d bytes, %d strips, peak %d bytes", s.name, len(file), strips, peak)
		if peak > 512<<10 {
			t.Errorf("%s: peak %d bytes", s.name, peak)
		}
		if used := env.Budget.Used(); used != 0 {
			t.Errorf("%s: %d bytes still reserved", s.name, used)
		}
	}
}

// buildLarge is build without the image.
func (s sample) buildLarge() ([]byte, image.Image) {
	head, _ := sample{w: 1, h: 1, chars: s.chars, colours: s.colours}.build()
	// The sizes, and the rows, written anew.
	text := string(head)
	text = strings.Replace(text, "\"1 1 ", fmt.Sprintf("\"%d %d ", s.w, s.h), 1)
	text = text[:strings.LastIndex(text[:strings.LastIndex(text, "\"")], "\"")]
	keys := make([][]byte, s.colours)
	for i := range keys {
		v := i
		for j := 0; j < s.chars; j++ {
			keys[i] = append(keys[i], keyChars[v%len(keyChars)])
			v /= len(keyChars)
		}
	}
	file := make([]byte, 0, len(text)+s.h*(s.w*s.chars+4)+8)
	file = append(file, text...)
	for y := 0; y < s.h; y++ {
		file = append(file, '"')
		for x := 0; x < s.w; x++ {
			file = append(file, keys[index(x, y, s.colours)]...)
		}
		file = append(file, '"', ',', '\n')
	}
	return append(file, "};\n"...), nil
}

// FuzzOpen: any file is read or refused, without a panic, within the
// memory it reserves.
func FuzzOpen(f *testing.F) {
	for _, data := range fixtures(f) {
		f.Add(data)
	}
	for _, s := range samples {
		if s.colours <= 600 {
			data, _ := s.build()
			f.Add(data)
		}
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
