package gifstream

import (
	"bytes"
	"compress/lzw"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"io"
	"io/fs"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// decodeStream decodes data with the streaming decoder, checking the strip
// contract on the way, and assembles the strips into one image.
func decodeStream(data []byte, env *pipeline.Env) (image.Image, int, error) {
	src, err := Open(bytes.NewReader(data), env)
	if err != nil {
		return nil, 0, err
	}
	defer src.Close()
	return collect(src)
}

func collect(src pipeline.Source) (image.Image, int, error) {
	b := src.Bounds()
	pal, ok := src.ColorModel().(color.Palette)
	if !ok {
		return nil, 0, fmt.Errorf("ColorModel is %T, want color.Palette", src.ColorModel())
	}
	out := &image.Paletted{Pix: make([]uint8, b.Dx()*b.Dy()), Stride: b.Dx(), Rect: b, Palette: pal}
	y, strips := b.Min.Y, 0
	for {
		s, err := src.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, strips, err
		}
		strips++
		p, ok := s.(*image.Paletted)
		if !ok {
			return nil, strips, fmt.Errorf("strip is %T", s)
		}
		sb := p.Bounds()
		if sb.Min.X != b.Min.X || sb.Max.X != b.Max.X || sb.Min.Y != y || sb.Max.Y > b.Max.Y || (sb.Dy() == 0 && !b.Empty()) {
			return nil, strips, fmt.Errorf("strip %v does not continue at row %d of %v", sb, y, b)
		}
		if !reflect.DeepEqual(p.Palette, pal) {
			return nil, strips, fmt.Errorf("strip palette differs from ColorModel")
		}
		for ; y < sb.Max.Y; y++ {
			copy(out.Pix[out.PixOffset(b.Min.X, y):][:b.Dx()], p.Pix[p.PixOffset(b.Min.X, y):][:b.Dx()])
		}
	}
	if y != b.Max.Y {
		return nil, strips, fmt.Errorf("strips end at row %d of %v", y, b)
	}
	if strips == 0 {
		return nil, 0, fmt.Errorf("no strip before io.EOF")
	}
	if _, err := src.Next(); err != io.EOF {
		return nil, strips, fmt.Errorf("Next after io.EOF: %v", err)
	}
	return out, strips, nil
}

func same(want, got image.Image) error {
	if reflect.TypeOf(want) != reflect.TypeOf(got) {
		return fmt.Errorf("type %T, want %T", got, want)
	}
	if want.Bounds() != got.Bounds() {
		return fmt.Errorf("bounds %v, want %v", got.Bounds(), want.Bounds())
	}
	w, g := want.(*image.Paletted), got.(*image.Paletted)
	if len(w.Palette) != len(g.Palette) {
		return fmt.Errorf("palette has %d entries, want %d", len(g.Palette), len(w.Palette))
	}
	for i := range w.Palette {
		if w.Palette[i] != g.Palette[i] {
			return fmt.Errorf("palette[%d] = %v, want %v", i, g.Palette[i], w.Palette[i])
		}
	}
	if !bytes.Equal(w.Pix, g.Pix) {
		return fmt.Errorf("pixels differ")
	}
	return nil
}

// compare decodes data with gif.Decode and with the streaming decoder and
// requires the same image, or an error from both. It reports whether the
// reference decoded the data.
func compare(t *testing.T, name string, data []byte) bool {
	t.Helper()
	want, werr := gif.Decode(bytes.NewReader(data))
	got, _, gerr := decodeStream(data, nil)
	if (werr == nil) != (gerr == nil) {
		t.Errorf("%s: gif.Decode error %v, stream error %v", name, werr, gerr)
		return werr == nil
	}
	if werr != nil {
		return false
	}
	if err := same(want, got); err != nil {
		t.Errorf("%s: %v", name, err)
	}
	return true
}

// gifFiles returns every .gif under the directories of test images that
// come with Go and golang.org/x/image.
func gifFiles(t *testing.T) []string {
	roots := []string{filepath.Join(runtime.GOROOT(), "src"), filepath.Join(runtime.GOROOT(), "misc")}
	if dir := xImageDir(); dir != "" {
		roots = append(roots, dir)
	}
	var files []string
	for _, root := range roots {
		filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.EqualFold(filepath.Ext(path), ".gif") {
				files = append(files, path)
			}
			return nil
		})
	}
	if len(files) < 5 {
		t.Fatalf("only %d gif files found under %v", len(files), roots)
	}
	return files
}

// xImageDir locates the golang.org/x/image module that provides testdata.
func xImageDir() string { return testutil.XImageDir() }

// TestTestdata decodes every GIF in Go's and x/image's test corpora.
func TestTestdata(t *testing.T) {
	n := 0
	for _, f := range gifFiles(t) {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if compare(t, f, data) {
			n++
		}
	}
	t.Logf("%d files matched gif.Decode", n)
}

// variant describes one generated GIF.
type variant struct {
	interlaced  bool
	transparent bool
	local       bool // frame has its own colour table
	offset      bool // frame is smaller than, and offset inside, the screen
	colors      int
}

func (v variant) String() string {
	return fmt.Sprintf("interlaced=%t,transparent=%t,local=%t,offset=%t,colors=%d",
		v.interlaced, v.transparent, v.local, v.offset, v.colors)
}

func variants() []variant {
	var vs []variant
	for i := 0; i < 16; i++ {
		v := variant{i&1 != 0, i&2 != 0, i&4 != 0, i&8 != 0, 256}
		if i%3 == 1 {
			v.colors = 37 // not a power of two: the colour table is padded
		}
		vs = append(vs, v)
	}
	return vs
}

func testPalette(n int, transparent bool, seed int) color.Palette {
	pal := make(color.Palette, n)
	for i := range pal {
		pal[i] = color.RGBA{uint8(i*7 + seed), uint8(255 - i), uint8(i*i + 3*seed), 0xFF}
	}
	if transparent {
		pal[n/3] = color.RGBA{}
	}
	return pal
}

// testImage is w×h of noise over a gradient, so the LZW stream has both
// long and short codes.
func testImage(r image.Rectangle, pal color.Palette, seed int64) *image.Paletted {
	rnd := rand.New(rand.NewSource(seed))
	m := image.NewPaletted(r, pal)
	w := r.Dx()
	for i := range m.Pix {
		x, y := i%w, i/w
		v := (x/9 + y/5) % len(pal)
		if rnd.Intn(4) == 0 {
			v = rnd.Intn(len(pal))
		}
		m.Pix[i] = uint8(v)
	}
	return m
}

// interlaceRows returns a copy of m whose rows are in the order an
// interlaced GIF stores them, so that encoding it and setting the interlace
// flag yields an interlaced GIF of m.
func interlaceRows(m *image.Paletted) *image.Paletted {
	dx, dy := m.Rect.Dx(), m.Rect.Dy()
	out := image.NewPaletted(m.Rect, m.Palette)
	offset := 0
	for _, pass := range interlacing {
		for y := pass.start; y < dy; y += pass.skip {
			copy(out.Pix[offset:offset+dx], m.Pix[y*m.Stride:y*m.Stride+dx])
			offset += dx
		}
	}
	return out
}

// imageDescriptor returns the offset of the first image descriptor (the
// 0x2C byte) and of the first graphic control extension, or -1.
func imageDescriptor(t testing.TB, data []byte) (desc, gce int) {
	gce = -1
	p := 13
	if data[10]&fColorTable != 0 {
		p += 3 << (1 + data[10]&fColorTableBitsMask)
	}
	for {
		switch data[p] {
		case sImageDescriptor:
			return p, gce
		case sExtension:
			if data[p+1] == eGraphicControl && gce < 0 {
				gce = p
			}
			p += 2
			for data[p] != 0 {
				p += 1 + int(data[p])
			}
			p++
		default:
			t.Fatalf("no image descriptor (block 0x%02x at %d)", data[p], p)
		}
	}
}

// makeGIF encodes m as described by v with the standard encoder and patches
// in what the encoder cannot write (the interlace flag). It returns the
// file and the image gif.Decode must give back.
func makeGIF(t testing.TB, w, h int, v variant, seed int64) ([]byte, *image.Paletted) {
	pal := testPalette(v.colors, v.transparent, 1)
	rect := image.Rect(0, 0, w, h)
	cfg := image.Config{Width: w, Height: h}
	if v.offset {
		rect = image.Rect(w/7, h/9, w-w/5, h-h/11)
	}
	cfg.ColorModel = pal
	if v.local {
		cfg.ColorModel = testPalette(64, false, 2)
	}
	m := testImage(rect, pal, seed)
	frame := m
	if v.interlaced {
		frame = interlaceRows(m)
	}
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, &gif.GIF{Image: []*image.Paletted{frame}, Delay: []int{0}, Config: cfg}); err != nil {
		t.Fatal(err)
	}
	data := buf.Bytes()
	desc, gce := imageDescriptor(t, data)
	if v.interlaced {
		data[desc+9] |= fInterlace
	}
	if (data[desc+9]&fColorTable != 0) != v.local {
		t.Fatalf("%v: local colour table flag is %t", v, !v.local)
	}
	if (gce >= 0 && data[gce+3]&gcTransparentColorSet != 0) != v.transparent {
		t.Fatalf("%v: transparent flag is %t", v, !v.transparent)
	}
	return data, m
}

// TestGenerated decodes every variant at sizes that span many strips, and
// checks the result against gif.Decode and against the source image.
func TestGenerated(t *testing.T) {
	sizes := []image.Point{{397, 611}, {2000, 1500}, {1, 1}, {3, 2}, {5, 9}, {700, 7}, {65, 300}}
	if testing.Short() {
		sizes = sizes[:1]
	}
	for _, size := range sizes {
		for i, v := range variants() {
			name := fmt.Sprintf("%dx%d/%v", size.X, size.Y, v)
			data, m := makeGIF(t, size.X, size.Y, v, int64(i))
			want, err := gif.Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatalf("%s: gif.Decode: %v", name, err)
			}
			env := pipeline.NewEnv(0, t.TempDir())
			src, err := Open(bytes.NewReader(data), env)
			if err != nil {
				t.Fatalf("%s: Open: %v", name, err)
			}
			if got := pipeline.IsOpaque(src); got != !v.transparent {
				t.Errorf("%s: Opaque() = %t", name, got)
			}
			if pipeline.EnvOf(src) != env {
				t.Errorf("%s: Env() is not the env given to Open", name)
			}
			got, strips, err := collect(src)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if err := src.Close(); err != nil {
				t.Errorf("%s: Close: %v", name, err)
			}
			if err := same(want, got); err != nil {
				t.Errorf("%s: %v", name, err)
			}
			if !bytes.Equal(got.(*image.Paletted).Pix, m.Pix) || got.Bounds() != m.Bounds() {
				t.Errorf("%s: decoded image differs from the source image", name)
			}
			if size.Y > 600 && strips < 2 {
				t.Errorf("%s: only %d strips", name, strips)
			}
			if used := env.Budget.Used(); used != 0 {
				t.Errorf("%s: %d bytes still reserved after Close", name, used)
			}
			if size.X == 2000 && !v.local && !v.offset && v.colors == 256 {
				t.Logf("%s: %d strips, budget peak %d bytes", name, strips, env.Budget.Peak())
			}
		}
	}
}

// TestReaders feeds the decoder through readers without ReadByte and with
// short reads.
func TestReaders(t *testing.T) {
	for _, v := range []variant{{colors: 256}, {interlaced: true, transparent: true, colors: 37}} {
		data, m := makeGIF(t, 397, 611, v, 3)
		for name, r := range map[string]io.Reader{
			"plain":   struct{ io.Reader }{bytes.NewReader(data)},
			"onebyte": iotest.OneByteReader(bytes.NewReader(data)),
			"dataerr": iotest.DataErrReader(bytes.NewReader(data)),
		} {
			src, err := Open(r, nil)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			got, _, err := collect(src)
			src.Close()
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if !bytes.Equal(got.(*image.Paletted).Pix, m.Pix) {
				t.Errorf("%s %v: pixels differ", name, v)
			}
		}
	}
}

// TestTransparentIndexOutOfRange patches the transparent index to one past
// the colour table, which gif.Decode handles by growing the palette.
func TestTransparentIndexOutOfRange(t *testing.T) {
	for _, interlaced := range []bool{false, true} {
		data, _ := makeGIF(t, 120, 90, variant{interlaced: interlaced, transparent: true, colors: 16}, 1)
		_, gce := imageDescriptor(t, data)
		for _, ti := range []byte{15, 16, 200, 255} {
			data[gce+6] = ti
			if !compare(t, fmt.Sprintf("ti=%d", ti), data) {
				t.Errorf("ti=%d: gif.Decode failed", ti)
			}
			got, _, err := decodeStream(data, nil)
			if err != nil {
				t.Fatal(err)
			}
			if n := len(got.(*image.Paletted).Palette); n != max(16, int(ti)+1) {
				t.Errorf("ti=%d: palette has %d entries", ti, n)
			}
		}
	}
}

// lzwBlocks compresses pix and cuts it into data sub-blocks of at most
// blockSize bytes, without the terminating empty block.
func lzwBlocks(litWidth int, pix []byte, blockSize int) []byte {
	var z bytes.Buffer
	w := lzw.NewWriter(&z, lzw.LSB, litWidth)
	w.Write(pix)
	w.Close()
	var out []byte
	for b := z.Bytes(); len(b) > 0; {
		n := min(len(b), blockSize)
		out = append(out, byte(n))
		out = append(out, b[:n]...)
		b = b[n:]
	}
	return out
}

func le16(v int) []byte { return []byte{byte(v), byte(v >> 8)} }

// handGIF writes a GIF by hand: a sw×sh screen with a global colour table
// of 1<<(gctBits+1) entries (none if gctBits < 0), then extra, then one
// frame with the given descriptor fields, litWidth and image data, then tail.
func handGIF(sw, sh, gctBits int, extra []byte, left, top, w, h int, fields byte, lct []byte, litWidth byte, imageData, tail []byte) []byte {
	var b bytes.Buffer
	b.WriteString("GIF89a")
	b.Write(le16(sw))
	b.Write(le16(sh))
	if gctBits >= 0 {
		b.Write([]byte{0x80 | byte(gctBits), 0, 0})
		for i := 0; i < 1<<(gctBits+1); i++ {
			b.Write([]byte{byte(i * 16), byte(255 - i), byte(i)})
		}
	} else {
		b.Write([]byte{0, 0, 0})
	}
	b.Write(extra)
	b.WriteByte(sImageDescriptor)
	b.Write(le16(left))
	b.Write(le16(top))
	b.Write(le16(w))
	b.Write(le16(h))
	b.WriteByte(fields)
	b.Write(lct)
	b.WriteByte(litWidth)
	b.Write(imageData)
	b.Write(tail)
	return b.Bytes()
}

// TestHandMade covers the inputs the encoder cannot produce: empty frames,
// bad pixel values, wrong amounts of data, sloppy block terminators,
// extensions and so on. Every case must agree with gif.Decode.
func TestHandMade(t *testing.T) {
	pix := func(n, mod int) []byte {
		p := make([]byte, n)
		for i := range p {
			p[i] = byte(i * 7 % mod)
		}
		return p
	}
	term := []byte{0}
	trailer := []byte{sTrailer}
	data := func(litWidth, n, mod int) []byte { return append(lzwBlocks(litWidth, pix(n, mod), 255), 0) }
	// An LZW stream without the end-of-information code (issue 9856): drop
	// the last byte of a stream whose final byte holds only the end code.
	type tc struct {
		name string
		data []byte
		ok   bool // gif.Decode is expected to accept it
	}
	netscape := append([]byte{sExtension, eApplication, 11}, "NETSCAPE2.0\x03\x01\x05\x00\x00"...)
	comment := append([]byte{sExtension, eComment, 5}, "hello\x00"...)
	text := append(append([]byte{sExtension, eText, 12}, make([]byte, 12)...), 3, 'a', 'b', 'c', 0)
	gceTrans := []byte{sExtension, eGraphicControl, 4, 1, 0, 0, 2, 0}
	lct4 := []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
	second := append([]byte{sImageDescriptor, 0, 0, 0, 0, 1, 0, 1, 0, 0, 2}, append(lzwBlocks(2, []byte{1}, 255), 0, sTrailer)...)
	cases := []tc{
		{"basic", handGIF(20, 10, 1, nil, 0, 0, 20, 10, 0, nil, 2, data(2, 200, 4), trailer), true},
		{"basic interlaced", handGIF(20, 10, 1, nil, 0, 0, 20, 10, fInterlace, nil, 2, data(2, 200, 4), trailer), true},
		{"small blocks", handGIF(20, 10, 1, nil, 0, 0, 20, 10, 0, nil, 2, append(lzwBlocks(2, pix(200, 4), 3), 0), trailer), true},
		{"no trailer", handGIF(20, 10, 1, nil, 0, 0, 20, 10, 0, nil, 2, data(2, 200, 4), nil), true},
		{"garbage after frame", handGIF(20, 10, 1, nil, 0, 0, 20, 10, 0, nil, 2, data(2, 200, 4), []byte("garbage")), true},
		{"second frame", handGIF(20, 10, 1, nil, 0, 0, 20, 10, 0, nil, 2, data(2, 200, 4), second), true},
		{"extensions", handGIF(20, 10, 1, bytes.Join([][]byte{netscape, comment, text, gceTrans}, nil), 0, 0, 20, 10, 0, nil, 2, data(2, 200, 4), trailer), true},
		{"transparent local", handGIF(20, 10, 1, gceTrans, 0, 0, 20, 10, fColorTable|1, lct4, 2, data(2, 200, 4), trailer), true},
		{"transparent local interlaced", handGIF(20, 10, 1, gceTrans, 3, 2, 8, 5, fColorTable|fInterlace|1, lct4, 2, data(2, 40, 4), trailer), true},
		{"local only", handGIF(20, 10, -1, nil, 0, 0, 20, 10, fColorTable|1, lct4, 2, data(2, 200, 4), trailer), true},
		{"no colour table", handGIF(20, 10, -1, nil, 0, 0, 20, 10, 0, nil, 2, data(2, 200, 4), trailer), false},
		{"offset frame", handGIF(20, 10, 1, nil, 5, 3, 10, 4, 0, nil, 2, data(2, 40, 4), trailer), true},
		{"frame too wide", handGIF(20, 10, 1, nil, 11, 0, 10, 10, 0, nil, 2, data(2, 100, 4), trailer), false},
		{"frame too tall", handGIF(20, 10, 1, nil, 0, 1, 20, 10, 0, nil, 2, data(2, 200, 4), trailer), false},
		{"empty 0x0", handGIF(20, 10, 1, nil, 0, 0, 0, 0, 0, nil, 2, data(2, 0, 4), trailer), true},
		{"empty 0x5", handGIF(20, 10, 1, nil, 4, 2, 0, 5, 0, nil, 2, data(2, 0, 4), trailer), true},
		{"empty 5x0", handGIF(20, 10, 1, nil, 4, 2, 5, 0, 0, nil, 2, data(2, 0, 4), trailer), true},
		{"empty 5x0 interlaced", handGIF(20, 10, 1, nil, 4, 2, 5, 0, fInterlace, nil, 2, data(2, 0, 4), trailer), true},
		{"empty with pixels", handGIF(20, 10, 1, nil, 0, 0, 0, 0, 0, nil, 2, data(2, 3, 4), trailer), false},
		{"empty beyond screen", handGIF(20, 10, 1, nil, 30, 0, 0, 0, 0, nil, 2, data(2, 0, 4), trailer), false},
		{"empty screen", handGIF(0, 0, 1, nil, 0, 0, 0, 0, 0, nil, 2, data(2, 0, 4), trailer), true},
		{"pixel out of range", handGIF(20, 10, 0, nil, 0, 0, 20, 10, 0, nil, 2, data(2, 200, 4), trailer), false},
		{"pixel out of range interlaced", handGIF(20, 10, 0, nil, 0, 0, 20, 10, fInterlace, nil, 2, data(2, 200, 4), trailer), false},
		{"pixel in range", handGIF(20, 10, 0, nil, 0, 0, 20, 10, 0, nil, 2, data(2, 200, 2), trailer), true},
		{"last pixel out of range", handGIF(20, 10, 0, nil, 0, 0, 20, 10, 0, nil, 2, append(lzwBlocks(2, append(pix(199, 2), 3), 255), 0), trailer), false},
		{"not enough data", handGIF(20, 10, 1, nil, 0, 0, 20, 10, 0, nil, 2, data(2, 199, 4), trailer), false},
		{"not enough data interlaced", handGIF(20, 10, 1, nil, 0, 0, 20, 10, fInterlace, nil, 2, data(2, 199, 4), trailer), false},
		{"too much data", handGIF(20, 10, 1, nil, 0, 0, 20, 10, 0, nil, 2, data(2, 201, 4), trailer), false},
		{"too much data interlaced", handGIF(20, 10, 1, nil, 0, 0, 20, 10, fInterlace, nil, 2, data(2, 201, 4), trailer), false},
		{"no data", handGIF(20, 10, 1, nil, 0, 0, 20, 10, 0, nil, 2, term, trailer), false},
		{"lit width 1", handGIF(20, 10, 1, nil, 0, 0, 20, 10, 0, nil, 1, data(2, 200, 4), trailer), false},
		{"lit width 9", handGIF(20, 10, 1, nil, 0, 0, 20, 10, 0, nil, 9, data(2, 200, 4), trailer), false},
		{"lit width 8", handGIF(20, 10, 1, nil, 0, 0, 20, 10, 0, nil, 8, data(8, 200, 4), trailer), true},
		{"extra block of 1", handGIF(20, 10, 1, nil, 0, 0, 20, 10, 0, nil, 2, append(lzwBlocks(2, pix(200, 4), 255), 1, 0x55, 0), trailer), true},
		{"extra block of 2", handGIF(20, 10, 1, nil, 0, 0, 20, 10, 0, nil, 2, append(lzwBlocks(2, pix(200, 4), 255), 2, 0x55, 0x55, 0), trailer), false},
		{"two extra blocks", handGIF(20, 10, 1, nil, 0, 0, 20, 10, 0, nil, 2, append(lzwBlocks(2, pix(200, 4), 255), 1, 0x55, 1, 0x55, 0), trailer), false},
		{"extra block interlaced", handGIF(20, 10, 1, nil, 0, 0, 20, 10, fInterlace, nil, 2, append(lzwBlocks(2, pix(200, 4), 255), 2, 0x55, 0x55, 0), trailer), false},
		{"no terminator", handGIF(20, 10, 1, nil, 0, 0, 20, 10, 0, nil, 2, lzwBlocks(2, pix(200, 4), 255), nil), false},
		{"no image", append(handGIF(20, 10, 1, nil, 0, 0, 20, 10, 0, nil, 2, nil, nil)[:13+12], sTrailer), false},
		{"unknown block", append(handGIF(20, 10, 1, nil, 0, 0, 20, 10, 0, nil, 2, nil, nil)[:13+12], 0x99), false},
		{"unknown extension", handGIF(20, 10, 1, []byte{sExtension, 0x77, 0}, 0, 0, 20, 10, 0, nil, 2, data(2, 200, 4), trailer), false},
		{"bad graphic control", handGIF(20, 10, 1, []byte{sExtension, eGraphicControl, 4, 1, 0, 0, 2, 1}, 0, 0, 20, 10, 0, nil, 2, data(2, 200, 4), trailer), false},
		{"bad signature", append([]byte("GIF88a"), handGIF(20, 10, 1, nil, 0, 0, 20, 10, 0, nil, 2, data(2, 200, 4), trailer)[6:]...), false},
	}
	// Streams that end without the LZW end code, with and without a
	// terminator (gif.Decode accepts the first, see issue 9856).
	blocks := lzwBlocks(2, pix(200, 4), 255)
	for cut := 1; cut <= 3; cut++ {
		b := append([]byte(nil), blocks...)
		b[0] -= byte(cut)
		b = b[:len(b)-cut]
		cases = append(cases, tc{fmt.Sprintf("lzw cut %d", cut), handGIF(20, 10, 1, nil, 0, 0, 20, 10, 0, nil, 2, append(b, 0), trailer), cut == 1})
	}
	accepted := 0
	for _, c := range cases {
		ok := compare(t, c.name, c.data)
		if ok {
			accepted++
		}
		if ok != c.ok && !strings.HasPrefix(c.name, "lzw cut") {
			t.Errorf("%s: gif.Decode accepted = %t, the test expects %t", c.name, ok, c.ok)
		}
	}
	t.Logf("%d cases, %d accepted by gif.Decode", len(cases), accepted)
}

// samples are the files used by the truncation and corruption tests.
func samples(t *testing.T) map[string][]byte {
	out := map[string][]byte{}
	for _, f := range gifFiles(t) {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		out[filepath.Base(f)] = data
	}
	for i, v := range []variant{
		{colors: 256},
		{interlaced: true, colors: 256},
		{transparent: true, local: true, offset: true, colors: 37},
		{interlaced: true, transparent: true, local: true, offset: true, colors: 37},
		{interlaced: true, transparent: true, colors: 16},
	} {
		data, _ := makeGIF(t, 397, 611, v, int64(40+i))
		out[v.String()] = data
	}
	return out
}

// cutPoints returns many positions in [0, n): every position near the start
// and the end, and a spread of positions in between.
func cutPoints(n, dense, spread int, rnd *rand.Rand) []int {
	seen := map[int]bool{}
	var out []int
	add := func(p int) {
		if p >= 0 && p < n && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for i := 0; i < dense; i++ {
		add(i)
		add(n - 1 - i)
	}
	for i := 0; i < spread; i++ {
		add(rnd.Intn(n))
	}
	return out
}

// TestTruncated cuts the input at many points: the streaming decoder must
// fail exactly when gif.Decode fails.
func TestTruncated(t *testing.T) {
	rnd := rand.New(rand.NewSource(5))
	for name, data := range samples(t) {
		cuts, failed := cutPoints(len(data), 300, 100, rnd), 0
		if testing.Short() {
			cuts = cuts[:200]
		}
		for _, cut := range cuts {
			if !compare(t, fmt.Sprintf("%s cut at %d", name, cut), data[:cut]) {
				failed++
			}
		}
		t.Logf("%s: %d cut points, gif.Decode failed at %d", name, len(cuts), failed)
	}
}

// TestCorrupted changes single bytes at many positions: the streaming
// decoder must fail exactly when gif.Decode fails, and give the same image
// when it does not.
func TestCorrupted(t *testing.T) {
	rnd := rand.New(rand.NewSource(6))
	for name, data := range samples(t) {
		points, failed := cutPoints(len(data), 400, 200, rnd), 0
		if testing.Short() {
			points = points[:200]
		}
		for _, p := range points {
			bad := append([]byte(nil), data...)
			x := byte(1 << rnd.Intn(8))
			if rnd.Intn(2) == 0 {
				x = byte(1 + rnd.Intn(255))
			}
			bad[p] ^= x
			if !compare(t, fmt.Sprintf("%s byte %d ^ 0x%02x", name, p, x), bad) {
				failed++
			}
		}
		t.Logf("%s: %d corrupted positions, gif.Decode failed at %d", name, len(points), failed)
	}
}

func dirEntries(t *testing.T, dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// TestInterlacedSpillsToDisk decodes a large interlaced GIF under a 1MB
// budget, which forces the pass data into a temporary file.
func TestInterlacedSpillsToDisk(t *testing.T) {
	for _, v := range []variant{
		{interlaced: true, colors: 256},
		{interlaced: true, transparent: true, local: true, offset: true, colors: 37},
	} {
		data, m := makeGIF(t, 2000, 1500, v, 9)
		want, err := gif.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		env := pipeline.NewEnv(1<<20, dir)
		src, err := Open(bytes.NewReader(data), env)
		if err != nil {
			t.Fatal(err)
		}
		// Pull the first strip by hand to look at the spill.
		if _, err := src.Next(); err != nil {
			t.Fatal(err)
		}
		s := src.(*stream)
		if !s.spill.OnDisk() {
			t.Errorf("%v: spill of %d bytes stayed in memory under a 1MB budget", v, s.spill.Size())
		}
		if n := len(dirEntries(t, dir)); n != 1 {
			t.Errorf("%v: %d files in the temp dir while decoding, want 1", v, n)
		}
		src.Close()

		src, err = Open(bytes.NewReader(data), env)
		if err != nil {
			t.Fatal(err)
		}
		got, strips, err := collect(src)
		if err != nil {
			t.Fatal(err)
		}
		// Closing in the middle of the frame must clean up as well.
		if err := src.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
		if err := same(want, got); err != nil {
			t.Errorf("%v: %v", v, err)
		}
		if !bytes.Equal(got.(*image.Paletted).Pix, m.Pix) {
			t.Errorf("%v: decoded image differs from the source image", v)
		}
		if left := dirEntries(t, dir); len(left) != 0 {
			t.Errorf("%v: temp dir not empty after Close: %v", v, left)
		}
		if used := env.Budget.Used(); used != 0 {
			t.Errorf("%v: %d bytes still reserved after Close", v, used)
		}
		if peak := env.Budget.Peak(); peak > env.Budget.Limit() {
			t.Errorf("%v: peak %d over the limit", v, peak)
		}
		t.Logf("%v: %d strips, budget peak %d bytes of %d", v, strips, env.Budget.Peak(), env.Budget.Limit())
	}
}

// TestMemoryLimit checks that a frame whose row buffers alone exceed the
// budget is refused instead of being allocated.
func TestMemoryLimit(t *testing.T) {
	data, _ := makeGIF(t, 2000, 1500, variant{colors: 256}, 9)
	env := pipeline.NewEnv(100<<10, t.TempDir())
	if _, err := Open(bytes.NewReader(data), env); err == nil {
		t.Fatal("Open succeeded with a 100KB budget")
	}
	if used := env.Budget.Used(); used != 0 {
		t.Errorf("%d bytes still reserved after a failed Open", used)
	}
}

// TestCloseEarly closes sources before the frame is finished.
func TestCloseEarly(t *testing.T) {
	for _, v := range []variant{{colors: 256}, {interlaced: true, colors: 256}} {
		data, _ := makeGIF(t, 397, 611, v, 2)
		dir := t.TempDir()
		env := pipeline.NewEnv(1<<20, dir)
		for _, n := range []int{0, 1, 2} {
			src, err := Open(bytes.NewReader(data), env)
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < n; i++ {
				if _, err := src.Next(); err != nil {
					t.Fatal(err)
				}
			}
			if err := src.Close(); err != nil {
				t.Errorf("Close: %v", err)
			}
			if _, err := src.Next(); err == nil || err == io.EOF {
				t.Errorf("Next after Close: %v", err)
			}
			if used := env.Budget.Used(); used != 0 {
				t.Errorf("%d bytes still reserved after Close", used)
			}
			if left := dirEntries(t, dir); len(left) != 0 {
				t.Errorf("temp dir not empty after Close: %v", left)
			}
		}
	}
}
