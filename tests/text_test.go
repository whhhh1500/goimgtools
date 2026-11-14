package tests

import (
	"errors"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"

	. "github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/internal/textsrc"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// ink counts the pixels of m that are not the background colour bg.
func ink(m image.Image, bg color.Color) int {
	n := 0
	b := m.Bounds()
	want := color.NRGBAModel.Convert(bg)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if color.NRGBAModel.Convert(m.At(x, y)) != want {
				n++
			}
		}
	}
	return n
}

func textPNG(t *testing.T, text string, o TextOptions) image.Image {
	t.Helper()
	b, err := Text(text, o).ToBytes()
	if err != nil {
		t.Fatalf("Text(%q): %v", text, err)
	}
	return decodePNG(t, b)
}

// TestText uses the fonts built into the module, so it runs anywhere.
func TestText(t *testing.T) {
	builtin := TextOptions{FontData: [][]byte{goregular.TTF}, Size: 40}
	m := textPNG(t, "Hello, world", builtin)
	w, h := m.Bounds().Dx(), m.Bounds().Dy()
	if w < 150 || w > 300 || h < 40 || h > 60 {
		t.Fatalf("size %dx%d for 12 characters at 40 pixels", w, h)
	}
	if n := ink(m, color.Transparent); n < 500 || n > w*h/2 {
		t.Errorf("%d of %d pixels drawn", n, w*h)
	}
	if _, _, _, a := m.At(0, 0).RGBA(); a != 0 {
		t.Error("the default background is not transparent")
	}
	info, err := Text("Hello, world", builtin).Metadata()
	if err != nil || info.Width != w || info.Height != h || !info.HasAlpha {
		t.Errorf("Metadata = %+v, %v; the image is %dx%d", info, err, w, h)
	}

	// Size scales the picture; colours are used; padding adds a margin.
	big := textPNG(t, "Hello, world", TextOptions{FontData: [][]byte{goregular.TTF}, Size: 80})
	if r := float64(big.Bounds().Dx()) / float64(w); r < 1.9 || r > 2.1 {
		t.Errorf("twice the size gave %.2f times the width", r)
	}
	red, white := color.RGBA{200, 0, 0, 255}, color.RGBA{255, 255, 255, 255}
	c := textPNG(t, "X", TextOptions{FontData: [][]byte{goregular.TTF}, Size: 60, Color: red, Background: white, Padding: 10})
	seenRed := false
	cb := c.Bounds()
	for y := 0; y < cb.Dy(); y++ {
		for x := 0; x < cb.Dx(); x++ {
			px := color.NRGBAModel.Convert(c.At(x, y)).(color.NRGBA)
			if px == (color.NRGBA{200, 0, 0, 255}) {
				seenRed = true
			}
			if (x < 10 || y < 10 || x >= cb.Dx()-10 || y >= cb.Dy()-10) && px != (color.NRGBA{255, 255, 255, 255}) {
				t.Fatalf("pixel (%d,%d) in the padding is %v", x, y, px)
			}
		}
	}
	if !seenRed {
		t.Error("no pixel has the text colour")
	}
	if i, _ := Text("X", TextOptions{FontData: [][]byte{goregular.TTF}, Background: white}).Metadata(); i.HasAlpha {
		t.Error("an opaque background still reports transparency")
	}

	// Line breaks, wrapping and alignment.
	one := textPNG(t, "one", builtin)
	three := textPNG(t, "one\ntwo\n\nfour", builtin)
	if three.Bounds().Dy() != 4*one.Bounds().Dy() {
		t.Errorf("4 lines are %d pixels high, one line %d", three.Bounds().Dy(), one.Bounds().Dy())
	}
	long := "The quick brown fox jumps over the lazy dog and keeps running"
	wrapped := textPNG(t, long, TextOptions{FontData: [][]byte{goregular.TTF}, Size: 40, Width: 300})
	if wrapped.Bounds().Dx() != 300 || wrapped.Bounds().Dy() < 4*one.Bounds().Dy() {
		t.Errorf("wrapped at 300: %v", wrapped.Bounds().Size())
	}
	spaced := textPNG(t, "one\ntwo", TextOptions{FontData: [][]byte{goregular.TTF}, Size: 40, LineSpacing: 2})
	if spaced.Bounds().Dy() != 4*one.Bounds().Dy() {
		t.Errorf("double spacing: %d pixels for 2 lines, a line is %d", spaced.Bounds().Dy(), one.Bounds().Dy())
	}
	side := func(m image.Image) (left, right int) { // empty columns on each side of the first line
		b := m.Bounds()
		col := func(x int) bool {
			for y := 0; y < one.Bounds().Dy(); y++ {
				if _, _, _, a := m.At(x, y).RGBA(); a != 0 {
					return true
				}
			}
			return false
		}
		for left < b.Dx() && !col(left) {
			left++
		}
		for right < b.Dx() && !col(b.Dx()-1-right) {
			right++
		}
		return
	}
	for _, c := range []struct {
		a     TextAlign
		check func(l, r int) bool
	}{
		{TextLeft, func(l, r int) bool { return l < 10 && r > 100 }},
		{TextCenter, func(l, r int) bool { return l > 50 && r > 50 && l-r < 12 && r-l < 12 }},
		{TextRight, func(l, r int) bool { return r < 10 && l > 100 }},
	} {
		m := textPNG(t, "hi\na much longer second line", TextOptions{FontData: [][]byte{goregular.TTF}, Size: 40, Align: c.a})
		if l, r := side(m); !c.check(l, r) {
			t.Errorf("align %d: first line has %d empty columns left, %d right", c.a, l, r)
		}
	}

	// A second font: the same text in bold is drawn differently.
	a, _ := Text("Bold?", TextOptions{FontData: [][]byte{goregular.TTF}, Size: 40}).ToBytes()
	b, _ := Text("Bold?", TextOptions{FontData: [][]byte{gobold.TTF}, Size: 40}).ToBytes()
	if string(a) == string(b) {
		t.Error("another font gave the same picture")
	}
	// A font given as a file, and as one font of a file.
	dir := t.TempDir()
	path := filepath.Join(dir, "bold.ttf")
	if err := os.WriteFile(path, gobold.TTF, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, spec := range []string{path, path + "#0"} {
		f, err := Text("Bold?", TextOptions{Fonts: []string{spec}, Size: 40}).ToBytes()
		if err != nil {
			t.Fatalf("font %s: %v", spec, err)
		}
		if string(f) != string(b) {
			t.Errorf("font %s: differs from the same font given as data", spec)
		}
	}

	// It is an input like any other: a chain applies, and it can be an overlay.
	turned, err := Text("Hello", builtin).Rotate(90).Resize(0, 100).JPEG(90).ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	if i, err := FromBytes(turned).Metadata(); err != nil || i.Format != "jpeg" || i.Height != 100 || i.Width >= 100 {
		t.Errorf("text, turned and resized: %+v, %v", i, err)
	}
	base := image.NewRGBA(image.Rect(0, 0, 400, 200))
	for i := range base.Pix {
		base.Pix[i] = 255
	}
	marked, err := FromBytes(pngBytes(t, base)).
		Composite(Text("watermark", TextOptions{FontData: [][]byte{goregular.TTF}, Size: 30, Color: color.RGBA{0, 0, 0, 128}}),
			CompositeOptions{LeftRatio: 0.5, TopRatio: 0.5, Anchor: Center, Angle: -30}).PNG().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	mm := decodePNG(t, marked)
	if n := ink(mm, color.White); n < 300 {
		t.Errorf("text watermark changed only %d pixels", n)
	}
	if mm.At(5, 5) != (color.RGBA{255, 255, 255, 255}) && color.NRGBAModel.Convert(mm.At(5, 5)) != (color.NRGBA{255, 255, 255, 255}) {
		t.Errorf("a corner far from the watermark changed: %v", mm.At(5, 5))
	}

	// What cannot be done is an error.
	for what, m := range map[string]*Image{
		"empty text":        Text("  \n ", builtin),
		"size 0.2":          Text("x", TextOptions{FontData: [][]byte{goregular.TTF}, Size: 0.2}),
		"size 9000":         Text("x", TextOptions{FontData: [][]byte{goregular.TTF}, Size: 9000}),
		"missing font file": Text("x", TextOptions{Fonts: []string{filepath.Join(dir, "none.ttf")}}),
		"not a font":        Text("x", TextOptions{FontData: [][]byte{[]byte("not a font at all")}}),
		"font index":        Text("x", TextOptions{Fonts: []string{path + "#3"}}),
		"too wide":          Text(strings.Repeat("wide ", 2000), builtin),
		"bad alignment":     Text("x", TextOptions{FontData: [][]byte{goregular.TTF}, Align: 7}),
	} {
		if _, err := m.ToBytes(); err == nil {
			t.Errorf("%s: no error", what)
		}
	}
}

// TestTextMemory: fonts and glyph buffers are counted, a font is not read
// whole, and everything is given back.
func TestTextMemory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "regular.ttf")
	if err := os.WriteFile(path, goregular.TTF, 0o644); err != nil {
		t.Fatal(err)
	}
	env := pipeline.NewEnv(8<<20, dir)
	src, err := textsrc.New(strings.Repeat("many lines of text\n", 200)+"end", textsrc.Options{Fonts: []string{path}, Size: 24}, env)
	if err != nil {
		t.Fatal(err)
	}
	m, err := pipeline.Collect(src)
	if err != nil {
		t.Fatal(err)
	}
	// 201 lines, but only one font (the text needs no fallback) and one
	// line of pixels were ever reserved.
	line := int64(m.Bounds().Dx()) * 4 * int64(m.Bounds().Dy()/201)
	if peak, most := env.Budget.Peak(), textsrc.FontBytes+int64(6*24*24)+line+line/2; peak > most {
		t.Errorf("peak %d bytes for %v, expected at most %d", peak, m.Bounds().Size(), most)
	}
	src.Close()
	src.Close()
	if env.Budget.Used() != 0 {
		t.Errorf("%d bytes still reserved", env.Budget.Used())
	}
	// A limit too small for the glyph buffers of huge text is reported.
	_, err = Text("big", TextOptions{Fonts: []string{path}, Size: 3000}).MemoryLimit(4 << 20).ToBytes()
	if !errors.Is(err, ErrMemoryLimit) {
		t.Errorf("3000 pixel text under a 4MB limit: %v", err)
	}
}

// TestFontsOfThisSystem checks the font lists and, where a Chinese font is
// installed, that Chinese text is drawn with it by default.
func TestFontsOfThisSystem(t *testing.T) {
	def := DefaultFonts()
	if len(def) == 0 || def[len(def)-1].Name != "Go Regular" || def[len(def)-1].File != "" {
		t.Fatalf("DefaultFonts = %+v; the built-in font must be last", def)
	}
	t.Logf("default fonts: %+v", def)
	sys := SystemFonts()
	t.Logf("%d fonts installed", len(sys))
	chinese := ""
	for _, f := range def {
		if f.Chinese {
			chinese = f.Name
			break
		}
	}
	if chinese == "" {
		t.Skip("no Chinese font on this system")
	}
	// Each default with a file is also in the system list, and can be asked for by name.
	for _, f := range def[:len(def)-1] {
		found := false
		for _, s := range sys {
			found = found || s.File == f.File
		}
		if !found {
			t.Errorf("default font %s (%s) is not in SystemFonts", f.Name, f.File)
		}
	}
	zh := textPNG(t, "中文水印 Mixed 文字", TextOptions{Size: 40})
	if n := ink(zh, color.Transparent); n < 2000 {
		t.Errorf("Chinese text drew only %d pixels", n)
	}
	byName, err := Text("中文", TextOptions{Fonts: []string{chinese}, Size: 40}).ToBytes()
	if err != nil {
		t.Fatalf("font by name %q: %v", chinese, err)
	}
	byDefault, _ := Text("中文", TextOptions{Size: 40}).ToBytes()
	if string(byName) != string(byDefault) {
		t.Errorf("asking for the first default font %q by name gave another picture", chinese)
	}
	// The built-in font has no Chinese: forcing it first still falls back.
	fallback := textPNG(t, "中文", TextOptions{FontData: [][]byte{goregular.TTF}, Size: 40})
	sameImage(t, "fallback", fallback, decodePNG(t, byDefault))

	if out := os.Getenv("GOIMG_TEXT_OUT"); out != "" {
		err := Text("goimgtools 文字水印\n第二行：中英文 Mixed 123", TextOptions{Size: 48, Color: color.RGBA{20, 40, 160, 255}, Background: color.White, Padding: 16}).ToFile(out)
		if err != nil {
			t.Fatal(err)
		}
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		t.Logf("wrote %s; heap in use %.1fMB", out, float64(ms.HeapAlloc)/(1<<20))
	}
}
