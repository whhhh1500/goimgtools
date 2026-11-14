package bmpstream

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"io"
	"io/fs"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/image/bmp"

	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// decodeStream decodes data with the streaming decoder, checking the strip
// contract on the way, and assembles the strips into one image.
func decodeStream(data []byte, env *pipeline.Env) (image.Image, int, error) {
	src, err := Open(bytes.NewReader(data), int64(len(data)), env)
	if err != nil {
		return nil, 0, err
	}
	defer src.Close()
	return collect(src)
}

func collect(src pipeline.Source) (image.Image, int, error) {
	b := src.Bounds()
	var out image.Image
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
		sb := s.Bounds()
		if sb.Min.X != b.Min.X || sb.Max.X != b.Max.X || sb.Min.Y != y || sb.Max.Y > b.Max.Y || (sb.Dy() == 0 && !b.Empty()) {
			return nil, strips, fmt.Errorf("strip %v does not continue at row %d of %v", sb, y, b)
		}
		if out == nil {
			out = pipeline.NewBand(s, b)
		}
		if reflect.TypeOf(out) != reflect.TypeOf(s) {
			return nil, strips, fmt.Errorf("strip type changed from %T to %T", out, s)
		}
		if p, ok := s.(*image.Paletted); ok && !reflect.DeepEqual(color.Model(p.Palette), src.ColorModel()) {
			return nil, strips, fmt.Errorf("strip palette differs from ColorModel")
		}
		pipeline.CopyRows(out, s, sb.Min.Y, sb.Max.Y)
		y = sb.Max.Y
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
	var wp, gp []uint8
	switch w := want.(type) {
	case *image.Paletted:
		g := got.(*image.Paletted)
		if len(w.Palette) != len(g.Palette) {
			return fmt.Errorf("palette has %d entries, want %d", len(g.Palette), len(w.Palette))
		}
		for i := range w.Palette {
			if w.Palette[i] != g.Palette[i] {
				return fmt.Errorf("palette[%d] = %v, want %v", i, g.Palette[i], w.Palette[i])
			}
		}
		wp, gp = w.Pix, g.Pix
	case *image.RGBA:
		wp, gp = w.Pix, got.(*image.RGBA).Pix
	case *image.NRGBA:
		wp, gp = w.Pix, got.(*image.NRGBA).Pix
	default:
		return fmt.Errorf("unexpected reference type %T", want)
	}
	if !bytes.Equal(wp, gp) {
		return fmt.Errorf("pixels differ")
	}
	return nil
}

// refDecode is bmp.Decode, except that it does not run the reference on
// files whose header asks for more than maxRefPixels pixels: bmp.Decode
// allocates the whole image before reading, which can exhaust memory after
// a corrupted width or height. Such a file is far too short to hold its
// rows, so the reference is known to fail with a read error.
const maxRefPixels = 1 << 25

func refDecode(data []byte) (image.Image, error) {
	c, err := bmp.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if n := int64(c.Width) * int64(c.Height); n > maxRefPixels {
		if n/8 <= int64(len(data)) {
			panic("test file large enough to hold a huge image")
		}
		return nil, io.ErrUnexpectedEOF
	}
	return bmp.Decode(bytes.NewReader(data))
}

// compare decodes data with bmp.Decode and with the streaming decoder and
// requires the same image, or an error from both. It reports whether the
// reference decoded the data.
func compare(t *testing.T, name string, data []byte) bool {
	t.Helper()
	want, werr := refDecode(data)
	got, _, gerr := decodeStream(data, nil)
	if (werr == nil) != (gerr == nil) {
		t.Errorf("%s: bmp.Decode error %v, stream error %v", name, werr, gerr)
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

// bmpFiles returns every .bmp under the directories of test images that
// come with Go and golang.org/x/image.
func bmpFiles(t *testing.T) []string {
	roots := []string{filepath.Join(runtime.GOROOT(), "src"), filepath.Join(runtime.GOROOT(), "misc")}
	dir := xImageDir()
	if dir == "" {
		t.Fatal("golang.org/x/image module directory not found")
	}
	roots = append(roots, dir)
	var files []string
	for _, root := range roots {
		filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.EqualFold(filepath.Ext(path), ".bmp") {
				files = append(files, path)
			}
			return nil
		})
	}
	if len(files) < 9 {
		t.Fatalf("only %d bmp files found under %v", len(files), roots)
	}
	return files
}

// xImageDir locates the golang.org/x/image module that provides testdata.
func xImageDir() string { return testutil.XImageDir() }

// TestTestdata decodes every BMP in x/image's (and Go's) test corpora.
func TestTestdata(t *testing.T) {
	n := 0
	for _, f := range bmpFiles(t) {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if compare(t, f, data) {
			n++
		}
		// The same through a real file, the way the library opens it.
		fh, err := os.Open(f)
		if err != nil {
			t.Fatal(err)
		}
		if src, err := Open(fh, int64(len(data)), nil); err == nil {
			got, _, err := collect(src)
			src.Close()
			if want, werr := bmp.Decode(bytes.NewReader(data)); werr == nil {
				if err != nil {
					t.Errorf("%s: from file: %v", f, err)
				} else if err := same(want, got); err != nil {
					t.Errorf("%s: from file: %v", f, err)
				}
			}
		}
		fh.Close()
	}
	t.Logf("%d of %d files decoded by bmp.Decode and matched", n, len(bmpFiles(t)))
}

// variant describes one hand-written BMP.
type variant struct {
	bpp       int
	topDown   bool
	infoLen   int  // 40, 108 or 124
	colorUsed int  // paletted: 0 means the full 1<<bpp
	bitfields bool // BI_BITFIELDS with the default masks (V4/V5 only)
}

func (v variant) String() string {
	return fmt.Sprintf("bpp=%d,topDown=%t,infoLen=%d,colorUsed=%d,bitfields=%t",
		v.bpp, v.topDown, v.infoLen, v.colorUsed, v.bitfields)
}

func variants() []variant {
	var vs []variant
	for _, bpp := range []int{1, 2, 4, 8, 24, 32} {
		for _, topDown := range []bool{false, true} {
			for _, infoLen := range []int{40, 108, 124} {
				vs = append(vs, variant{bpp: bpp, topDown: topDown, infoLen: infoLen})
			}
		}
	}
	vs = append(vs,
		variant{bpp: 8, infoLen: 40, colorUsed: 200},
		variant{bpp: 8, topDown: true, infoLen: 124, colorUsed: 1},
		variant{bpp: 4, infoLen: 40, colorUsed: 10},
		variant{bpp: 2, topDown: true, infoLen: 108, colorUsed: 3},
		variant{bpp: 1, infoLen: 40, colorUsed: 1},
		variant{bpp: 32, infoLen: 108, bitfields: true},
		variant{bpp: 32, topDown: true, infoLen: 124, bitfields: true},
	)
	return vs
}

// handBMP writes a w×h BMP described by v by hand, with pseudo-random
// pixels (and random padding bytes). Paletted pixels stay inside the colour
// table.
func handBMP(w, h int, v variant, seed int64) []byte {
	rnd := rand.New(rand.NewSource(seed))
	colors := 0
	if v.bpp <= 8 {
		colors = 1 << v.bpp
		if v.colorUsed != 0 {
			colors = v.colorUsed
		}
	}
	var rowBytes int
	switch v.bpp {
	case 24:
		rowBytes = (3*w + 3) &^ 3
	case 32:
		rowBytes = 4 * w
	default:
		ppb := 8 / v.bpp
		rowBytes = ((w+ppb-1)/ppb + 3) &^ 3
	}
	offset := 14 + v.infoLen + 4*colors
	hdr := make([]byte, 14+v.infoLen)
	copy(hdr, "BM")
	le := binary.LittleEndian
	le.PutUint32(hdr[2:], uint32(offset+rowBytes*h))
	le.PutUint32(hdr[10:], uint32(offset))
	le.PutUint32(hdr[14:], uint32(v.infoLen))
	le.PutUint32(hdr[18:], uint32(int32(w)))
	if v.topDown {
		le.PutUint32(hdr[22:], uint32(int32(-h)))
	} else {
		le.PutUint32(hdr[22:], uint32(int32(h)))
	}
	le.PutUint16(hdr[26:], 1)
	le.PutUint16(hdr[28:], uint16(v.bpp))
	if v.bitfields {
		le.PutUint32(hdr[30:], 3)
		le.PutUint32(hdr[54:], 0xff0000)
		le.PutUint32(hdr[58:], 0xff00)
		le.PutUint32(hdr[62:], 0xff)
		le.PutUint32(hdr[66:], 0xff000000)
	}
	le.PutUint32(hdr[34:], uint32(rowBytes*h))
	le.PutUint32(hdr[46:], uint32(v.colorUsed))
	out := append([]byte(nil), hdr...)
	for i := 0; i < colors; i++ {
		out = append(out, byte(i*3+int(seed)), byte(255-i), byte(i*i), byte(rnd.Intn(256)))
	}
	row := make([]byte, rowBytes)
	for y := 0; y < h; y++ {
		rnd.Read(row)
		if v.bpp <= 8 && colors < 1<<v.bpp {
			// Keep every index of the row, padding included, in range.
			for i := range row {
				var b byte
				for s := 0; s < 8; s += v.bpp {
					b |= byte(rnd.Intn(colors)) << s
				}
				row[i] = b
			}
		}
		// A smooth run in each row, so rows are easy to tell apart.
		for i := 0; i < len(row)/3 && v.bpp >= 24; i++ {
			row[i] = byte(y + i/5)
		}
		out = append(out, row...)
	}
	return out
}

func wantType(v variant) string {
	switch v.bpp {
	case 24:
		return "*image.RGBA"
	case 32:
		return "*image.NRGBA"
	}
	return "*image.Paletted"
}

// TestHandWritten decodes every header variant at sizes that span many
// strips and requires the result of bmp.Decode.
func TestHandWritten(t *testing.T) {
	sizes := []image.Point{{397, 611}, {2000, 1500}, {1, 1}, {3, 2}, {5, 9}, {33, 7}, {7, 300}, {0, 0}}
	if testing.Short() {
		sizes = sizes[:1]
	}
	for _, size := range sizes {
		for i, v := range variants() {
			name := fmt.Sprintf("%dx%d/%v", size.X, size.Y, v)
			data := handBMP(size.X, size.Y, v, int64(i))
			want, err := bmp.Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatalf("%s: bmp.Decode: %v", name, err)
			}
			env := pipeline.NewEnv(0, t.TempDir())
			src, err := Open(bytes.NewReader(data), int64(len(data)), env)
			if err != nil {
				t.Fatalf("%s: Open: %v", name, err)
			}
			wantOpaque := !(v.bpp == 32 && v.infoLen > 40)
			if got := pipeline.IsOpaque(src); got != wantOpaque {
				t.Errorf("%s: Opaque() = %t", name, got)
			}
			if pipeline.EnvOf(src) != env {
				t.Errorf("%s: Env() is not the env given to Open", name)
			}
			if !reflect.DeepEqual(src.ColorModel(), want.ColorModel()) {
				t.Errorf("%s: ColorModel differs from the reference image's", name)
			}
			got, strips, err := collect(src)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			src.Close()
			if err := same(want, got); err != nil {
				t.Errorf("%s: %v", name, err)
			}
			if typ := fmt.Sprintf("%T", got); typ != wantType(v) {
				t.Errorf("%s: type %s, want %s", name, typ, wantType(v))
			}
			if size.Y > 600 && strips < 2 {
				t.Errorf("%s: only %d strips", name, strips)
			}
			if used := env.Budget.Used(); used != 0 {
				t.Errorf("%s: %d bytes still reserved after Close", name, used)
			}
			if size.X == 2000 && v.infoLen == 124 && v.colorUsed == 0 && !v.bitfields {
				t.Logf("%s: %d bytes, %d strips, budget peak %d bytes", name, len(data), strips, env.Budget.Peak())
			}
		}
	}
}

// encoderImages returns one image for each branch of x/image's encoder.
func encoderImages(w, h int) map[string]image.Image {
	rnd := rand.New(rand.NewSource(11))
	rect := image.Rect(0, 0, w, h)
	gray := image.NewGray(rect)
	rnd.Read(gray.Pix)
	pal := make(color.Palette, 256)
	for i := range pal {
		pal[i] = color.RGBA{uint8(i), uint8(i * 5), uint8(255 - i), 0xFF}
	}
	paletted := image.NewPaletted(rect, pal)
	rnd.Read(paletted.Pix)
	small := image.NewPaletted(rect, pal[:20])
	for i := range small.Pix {
		small.Pix[i] = uint8(rnd.Intn(20))
	}
	opaque := image.NewRGBA(rect)
	rnd.Read(opaque.Pix)
	for i := 3; i < len(opaque.Pix); i += 4 {
		opaque.Pix[i] = 0xFF
	}
	nrgba := image.NewNRGBA(rect)
	rnd.Read(nrgba.Pix)
	rgba := image.NewRGBA(rect)
	for i := 0; i < len(rgba.Pix); i += 4 {
		a := rnd.Intn(256)
		rgba.Pix[i], rgba.Pix[i+1], rgba.Pix[i+2], rgba.Pix[i+3] = uint8(rnd.Intn(a+1)), uint8(rnd.Intn(a+1)), uint8(rnd.Intn(a+1)), uint8(a)
	}
	ycbcr := image.NewYCbCr(rect, image.YCbCrSubsampleRatio420)
	rnd.Read(ycbcr.Y)
	rnd.Read(ycbcr.Cb)
	rnd.Read(ycbcr.Cr)
	return map[string]image.Image{
		"gray": gray, "paletted": paletted, "paletted20": small, "rgba-opaque": opaque,
		"nrgba": nrgba, "rgba": rgba, "ycbcr": ycbcr,
	}
}

// TestEncoded decodes what x/image's own encoder writes.
func TestEncoded(t *testing.T) {
	sizes := []image.Point{{397, 611}, {2000, 1500}, {1, 1}, {2, 3}, {0, 0}}
	if testing.Short() {
		sizes = sizes[:1]
	}
	for _, size := range sizes {
		for name, m := range encoderImages(size.X, size.Y) {
			var buf bytes.Buffer
			if err := bmp.Encode(&buf, m); err != nil {
				t.Fatal(err)
			}
			name = fmt.Sprintf("%dx%d/%s", size.X, size.Y, name)
			if !compare(t, name, buf.Bytes()) {
				t.Errorf("%s: bmp.Decode failed", name)
			}
			// Data after the last row is ignored by both decoders.
			if !compare(t, name+"+tail", append(buf.Bytes(), "trailing data"...)) {
				t.Errorf("%s+tail: bmp.Decode failed", name)
			}
		}
	}
}

// TestRejected checks the headers bmp.Decode refuses and bad palette
// indexes in the first, a middle and the last stored row.
func TestRejected(t *testing.T) {
	base := func(v variant) []byte { return handBMP(40, 30, v, 1) }
	le := binary.LittleEndian
	patch := func(data []byte, f func(b []byte)) []byte { f(data); return data }
	cases := map[string][]byte{
		"empty":                 nil,
		"short":                 []byte("BM"),
		"bad magic":             patch(base(variant{bpp: 24, infoLen: 40}), func(b []byte) { b[0] = 'X' }),
		"core header":           patch(base(variant{bpp: 24, infoLen: 40}), func(b []byte) { le.PutUint32(b[14:], 12) }),
		"negative width":        patch(base(variant{bpp: 24, infoLen: 40}), func(b []byte) { le.PutUint32(b[18:], 0xffffffd8) }),
		"zero width":            patch(base(variant{bpp: 24, infoLen: 40}), func(b []byte) { le.PutUint32(b[18:], 0) }),
		"zero height":           patch(base(variant{bpp: 24, infoLen: 40}), func(b []byte) { le.PutUint32(b[22:], 0) }),
		"two planes":            patch(base(variant{bpp: 24, infoLen: 40}), func(b []byte) { le.PutUint16(b[26:], 2) }),
		"16 bpp":                patch(base(variant{bpp: 24, infoLen: 40}), func(b []byte) { le.PutUint16(b[28:], 16) }),
		"rle8":                  patch(base(variant{bpp: 8, infoLen: 40}), func(b []byte) { le.PutUint32(b[30:], 1) }),
		"bitfields v3":          patch(base(variant{bpp: 32, infoLen: 40}), func(b []byte) { le.PutUint32(b[30:], 3) }),
		"bitfields other masks": patch(base(variant{bpp: 32, infoLen: 108, bitfields: true}), func(b []byte) { le.PutUint32(b[54:], 0xff) }),
		"offset past header":    patch(base(variant{bpp: 24, infoLen: 40}), func(b []byte) { le.PutUint32(b[10:], 58) }),
		"offset past palette":   patch(base(variant{bpp: 8, infoLen: 40}), func(b []byte) { le.PutUint32(b[10:], 14+40+1024+4) }),
		"too many colours":      patch(base(variant{bpp: 4, infoLen: 40}), func(b []byte) { le.PutUint32(b[46:], 17) }),
		"huge":                  patch(base(variant{bpp: 24, infoLen: 40}), func(b []byte) { le.PutUint32(b[18:], 0x7fffffff); le.PutUint32(b[22:], 0x7fffffff) }),
		"huge top-down":         patch(base(variant{bpp: 24, infoLen: 40}), func(b []byte) { le.PutUint32(b[22:], 0x80000000) }),
		"one row short":         base(variant{bpp: 24, infoLen: 40})[:14+40+29*120],
		"one byte short":        base(variant{bpp: 8, topDown: true, infoLen: 40})[:14+40+1024+30*40-1],
		"palette cut":           base(variant{bpp: 8, infoLen: 40})[:14+40+500],
		"header cut":            base(variant{bpp: 8, infoLen: 124})[:100],
	}
	for name, data := range cases {
		if compare(t, name, data) {
			t.Errorf("%s: bmp.Decode accepted it", name)
		}
	}
	// Palette indexes beyond the colour table.
	for _, v := range []variant{
		{bpp: 8, infoLen: 40, colorUsed: 100},
		{bpp: 8, topDown: true, infoLen: 40, colorUsed: 100},
		{bpp: 4, infoLen: 108, colorUsed: 9},
		{bpp: 1, topDown: true, infoLen: 40, colorUsed: 1},
	} {
		good := handBMP(397, 611, v, 2)
		if !compare(t, v.String(), good) {
			t.Fatalf("%v: bmp.Decode failed on the unmodified file", v)
		}
		rowBytes := (len(good) - 14 - v.infoLen - 4*v.colorUsed) / 611
		for _, row := range []int{0, 1, 300, 609, 610} {
			for _, col := range []int{0, 396 * v.bpp / 8} {
				bad := append([]byte(nil), good...)
				bad[len(bad)-611*rowBytes+row*rowBytes+col] = 0xff
				if compare(t, fmt.Sprintf("%v bad index in stored row %d", v, row), bad) {
					t.Errorf("%v: bmp.Decode accepted a bad index in stored row %d", v, row)
				}
			}
		}
		// Out-of-range bits in the padding are not looked at.
		bad := append([]byte(nil), good...)
		bad[len(bad)-1] = 0xff
		if v.bpp == 8 && !compare(t, v.String()+" bad padding", bad) {
			t.Errorf("%v: bmp.Decode rejected a bad index in the padding", v)
		}
	}
}

// samples are the files used by the truncation and corruption tests.
func samples(t *testing.T) map[string][]byte {
	out := map[string][]byte{}
	for _, f := range bmpFiles(t) {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		out[filepath.Base(f)] = data
	}
	for i, v := range []variant{
		{bpp: 1, infoLen: 40},
		{bpp: 4, topDown: true, infoLen: 108, colorUsed: 10},
		{bpp: 8, infoLen: 40, colorUsed: 200},
		{bpp: 8, topDown: true, infoLen: 124},
		{bpp: 24, infoLen: 40},
		{bpp: 24, topDown: true, infoLen: 124},
		{bpp: 32, infoLen: 40},
		{bpp: 32, topDown: true, infoLen: 108, bitfields: true},
	} {
		out[v.String()] = handBMP(397, 611, v, int64(20+i))
	}
	return out
}

// points returns many positions in [0, n): every position near the start
// and the end, and a spread of positions in between.
func points(n, dense, spread int, rnd *rand.Rand) []int {
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
// fail exactly when bmp.Decode fails.
func TestTruncated(t *testing.T) {
	rnd := rand.New(rand.NewSource(5))
	for name, data := range samples(t) {
		cuts, failed := points(len(data), 1200, 200, rnd), 0
		if testing.Short() {
			cuts = cuts[:200]
		}
		for _, cut := range cuts {
			if !compare(t, fmt.Sprintf("%s cut at %d", name, cut), data[:cut]) {
				failed++
			}
		}
		t.Logf("%s: %d cut points, bmp.Decode failed at %d", name, len(cuts), failed)
	}
}

// TestCorrupted changes single bytes at many positions: the streaming
// decoder must fail exactly when bmp.Decode fails, and give the same image
// when it does not.
func TestCorrupted(t *testing.T) {
	rnd := rand.New(rand.NewSource(6))
	for name, data := range samples(t) {
		ps, failed := points(len(data), 700, 300, rnd), 0
		if testing.Short() {
			ps = ps[:200]
		}
		for _, p := range ps {
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
		t.Logf("%s: %d corrupted positions, bmp.Decode failed at %d", name, len(ps), failed)
	}
}

// TestSizeArgument checks that the size given to Open bounds the file.
func TestSizeArgument(t *testing.T) {
	data := handBMP(397, 611, variant{bpp: 24, infoLen: 40}, 1)
	if _, err := Open(bytes.NewReader(data), int64(len(data))-1, nil); err == nil {
		t.Error("Open succeeded with a size one byte short")
	}
	if _, err := Open(bytes.NewReader(data), 20, nil); err == nil {
		t.Error("Open succeeded with a size inside the header")
	}
	// A reader that fails although size promised the data.
	src, err := Open(bytes.NewReader(data[:len(data)-5]), int64(len(data)), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := collect(src); err == nil {
		t.Error("decoding succeeded although the reader is shorter than size")
	}
	src.Close()
}

// TestSmallBudget decodes large files under a 1MB budget.
func TestSmallBudget(t *testing.T) {
	for _, v := range []variant{
		{bpp: 1, infoLen: 40},
		{bpp: 8, infoLen: 40},
		{bpp: 24, infoLen: 40},
		{bpp: 32, infoLen: 124},
		{bpp: 32, topDown: true, infoLen: 124},
	} {
		data := handBMP(2000, 1500, v, 4)
		want, err := bmp.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		env := pipeline.NewEnv(1<<20, dir)
		got, strips, err := decodeStream(data, env)
		if err != nil {
			t.Fatalf("%v: %v", v, err)
		}
		if err := same(want, got); err != nil {
			t.Errorf("%v: %v", v, err)
		}
		if used := env.Budget.Used(); used != 0 {
			t.Errorf("%v: %d bytes still reserved after Close", v, used)
		}
		if entries, _ := os.ReadDir(dir); len(entries) != 0 {
			t.Errorf("%v: %d files left in the temp dir", v, len(entries))
		}
		t.Logf("%v: %d bytes, %d strips, budget peak %d bytes of %d", v, len(data), strips, env.Budget.Peak(), env.Budget.Limit())
	}
}

// TestMemoryLimit checks that an image whose row buffers alone exceed the
// budget is refused instead of being allocated, and that Close is safe at
// any point.
func TestMemoryLimit(t *testing.T) {
	data := handBMP(2000, 1500, variant{bpp: 24, infoLen: 40}, 4)
	env := pipeline.NewEnv(10<<10, t.TempDir())
	if _, err := Open(bytes.NewReader(data), int64(len(data)), env); err == nil {
		t.Fatal("Open succeeded with a 10KB budget")
	}
	if used := env.Budget.Used(); used != 0 {
		t.Errorf("%d bytes still reserved after a failed Open", used)
	}
	env = pipeline.NewEnv(0, t.TempDir())
	for _, n := range []int{0, 1, 3} {
		src, err := Open(bytes.NewReader(data), int64(len(data)), env)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < n; i++ {
			if _, err := src.Next(); err != nil {
				t.Fatal(err)
			}
		}
		src.Close()
		if _, err := src.Next(); err == nil || err == io.EOF {
			t.Errorf("Next after Close: %v", err)
		}
		if used := env.Budget.Used(); used != 0 {
			t.Errorf("%d bytes still reserved after Close", used)
		}
	}
}
