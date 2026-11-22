package fitsstream

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// The fixtures of testdata were written by a script (Pillow only reads
// FITS) and read back with Pillow 12.1.1 for the PNG next to each:
//   - u8_*: Pillow's pixels as they are;
//   - i16_*, i32_*: Pillow's numbers with their bytes put back in order
//     (Pillow has them byte-swapped; the script checks that this is all the
//     difference), BZERO added where the file has one, limited to 0..65535
//     by Pillow's conversion from "I" to "I;16";
//   - f32_*: the same, the fractions dropped by Pillow's conversion from
//     "F" to "I" first.

// decode reads the whole image of a FITS file, checking what every band
// says of itself on the way.
func decode(t *testing.T, data []byte, env *pipeline.Env) (image.Image, error) {
	t.Helper()
	src, err := Open(bytes.NewReader(data), int64(len(data)), env)
	if err != nil {
		return nil, err
	}
	defer src.Close()
	if !pipeline.IsOpaque(src) {
		t.Fatal("not opaque")
	}
	b := src.Bounds()
	var out image.Image
	y := 0
	for {
		m, err := src.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		r := m.Bounds()
		if r.Min.X != 0 || r.Max.X != b.Max.X || r.Min.Y != y || r.Empty() || r.Max.Y > b.Max.Y {
			t.Fatalf("band %v of an image %v after row %d", r, b, y)
		}
		if m.ColorModel() != src.ColorModel() {
			t.Fatalf("band of %T in a source of another colour model", m)
		}
		if out == nil {
			out = pipeline.NewBand(m, b)
		}
		pipeline.CopyRows(out, m, r.Min.Y, r.Max.Y)
		y = r.Max.Y
	}
	if y != b.Max.Y {
		t.Fatalf("%d rows of %d", y, b.Max.Y)
	}
	return out, nil
}

// same compares got with want, which must be of the same type: 8 or 16 bit
// grey.
func same(t *testing.T, name string, got, want image.Image) {
	t.Helper()
	if got.Bounds() != want.Bounds() {
		t.Errorf("%s: bounds %v, want %v", name, got.Bounds(), want.Bounds())
		return
	}
	var g, w []byte
	bpp := 1
	switch m := want.(type) {
	case *image.Gray:
		w = m.Pix
		if gm, ok := got.(*image.Gray); ok {
			g = gm.Pix
		}
	case *image.Gray16:
		w, bpp = m.Pix, 2
		if gm, ok := got.(*image.Gray16); ok {
			g = gm.Pix
		}
	}
	if g == nil {
		t.Errorf("%s: %T, want %T", name, got, want)
		return
	}
	bad := 0
	for i := 0; i < len(w); i += bpp {
		if !bytes.Equal(g[i:i+bpp], w[i:i+bpp]) {
			if bad == 0 {
				x, y := i/bpp%want.Bounds().Dx(), i/bpp/want.Bounds().Dx()
				t.Errorf("%s: pixel %d,%d is %v, want %v", name, x, y, got.At(x, y), want.At(x, y))
			}
			bad++
		}
	}
	if bad > 1 {
		t.Errorf("%s: %d pixels differ", name, bad)
	}
}

func readPNG(t *testing.T, name string) image.Image {
	t.Helper()
	f, err := os.Open(name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	m, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func fixtures(t testing.TB) []string {
	names, err := filepath.Glob(filepath.Join("testdata", "*.fits"))
	if err != nil || len(names) < 8 {
		t.Fatalf("%d fixtures (%v)", len(names), err)
	}
	return names
}

// TestFixtures: every fixture gives exactly the expected pixels, 8-bit
// grey for samples of 8 bits and 16-bit grey for the rest.
func TestFixtures(t *testing.T) {
	for _, name := range fixtures(t) {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		want := readPNG(t, strings.TrimSuffix(name, ".fits")+".png")
		got, err := decode(t, data, nil)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		same(t, name, got, want)
		if _, wide := want.(*image.Gray16); wide == strings.Contains(name, "u8_") {
			t.Errorf("%s: expected pixels are %T", name, want)
		}
	}
}

// card is a header line with a value.
func card(key, val string) string {
	return fmt.Sprintf("%-80s", fmt.Sprintf("%-8s= %20s", key, val))
}

// hdu is a header of the given cards and the data after it, each padded
// to blocks.
func hdu(data []byte, cards ...string) []byte {
	h := strings.Join(cards, "") + fmt.Sprintf("%-80s", "END")
	h += strings.Repeat(" ", (blockLen-len(h)%blockLen)%blockLen)
	return append([]byte(h), append(data, make([]byte, (blockLen-len(data)%blockLen)%blockLen)...)...)
}

// file is a FITS file of one image; data is what is stored, bottom row
// first.
func file(bitpix int, axes []int, data []byte, extra ...string) []byte {
	cards := []string{card("SIMPLE", "T"), card("BITPIX", fmt.Sprint(bitpix)), card("NAXIS", fmt.Sprint(len(axes)))}
	for i, n := range axes {
		cards = append(cards, card(fmt.Sprintf("NAXIS%d", i+1), fmt.Sprint(n)))
	}
	return hdu(data, append(cards, extra...)...)
}

// stored packs numbers as samples of the given BITPIX.
func stored[T int64 | float64](bitpix int, vals ...T) []byte {
	var b []byte
	for _, v := range vals {
		switch bitpix {
		case 8:
			b = append(b, byte(v))
		case 16:
			b = binary.BigEndian.AppendUint16(b, uint16(int64(v)))
		case 32:
			b = binary.BigEndian.AppendUint32(b, uint32(int64(v)))
		case 64:
			b = binary.BigEndian.AppendUint64(b, uint64(int64(v)))
		case -32:
			b = binary.BigEndian.AppendUint32(b, math.Float32bits(float32(v)))
		case -64:
			b = binary.BigEndian.AppendUint64(b, math.Float64bits(float64(v)))
		}
	}
	return b
}

// TestValues: the value of a pixel is BZERO + BSCALE * the stored number,
// its fraction dropped, limited to the range of the output; files Pillow
// does not read (64 bits, BZERO and BSCALE) built here from known numbers.
func TestValues(t *testing.T) {
	nan, inf := math.NaN(), math.Inf(1)
	for _, c := range []struct {
		name   string
		bitpix int
		data   []byte
		extra  []string
		want   []uint16
	}{
		{"8", 8, stored[int64](8, 0, 1, 200, 255), nil, []uint16{0, 1, 200, 255}},
		{"8 signed", 8, stored[int64](8, 0, 127, 128, 255), []string{card("BZERO", "-128")}, []uint16{0, 0, 0, 127}},
		{"8 scaled", 8, stored[int64](8, 0, 1, 100, 255), []string{card("BSCALE", "2.5"), card("BZERO", "3.")}, []uint16{3, 5, 253, 255}},
		{"16", 16, stored[int64](16, -32768, -1, 0, 1, 256, 32767), nil, []uint16{0, 0, 0, 1, 256, 32767}},
		{"16 unsigned", 16, stored[int64](16, -32768, -32767, -1, 0, 1, 32767), []string{card("BSCALE", "1"), card("BZERO", "32768")}, []uint16{0, 1, 32767, 32768, 32769, 65535}},
		{"16 whole shift", 16, stored[int64](16, -32768, -101, -100, 0, 32767), []string{card("BZERO", "100")}, []uint16{0, 0, 0, 100, 32867}},
		{"16 scaled", 16, stored[int64](16, -10, 100, 32767), []string{card("BSCALE", "2"), card("BZERO", "10")}, []uint16{0, 210, 65535}},
		{"16 fractions", 16, stored[int64](16, -1, 3, 4, 7), []string{card("BSCALE", "0.5"), card("BZERO", "0.25")}, []uint16{0, 1, 2, 3}},
		{"32", 32, stored[int64](32, math.MinInt32, -1, 0, 65535, 65536, math.MaxInt32), nil, []uint16{0, 0, 0, 65535, 65535, 65535}},
		{"32 unsigned", 32, stored[int64](32, math.MinInt32, math.MinInt32+1234, math.MinInt32+70000, 0), []string{card("BZERO", "2147483648")}, []uint16{0, 1234, 65535, 65535}},
		{"64", 64, stored[int64](64, math.MinInt64, -1, 0, 12345, 65535, 65536, math.MaxInt64), nil, []uint16{0, 0, 0, 12345, 65535, 65535, 65535}},
		{"64 unsigned", 64, stored[int64](64, math.MinInt64, math.MinInt64+5, math.MinInt64+65535, math.MinInt64+65536, 0, math.MaxInt64), []string{card("BZERO", "9223372036854775808")}, []uint16{0, 5, 65535, 65535, 65535, 65535}},
		{"64 shift", 64, stored[int64](64, math.MinInt64, -5, -4, 0, math.MaxInt64), []string{card("BZERO", "4")}, []uint16{0, 0, 0, 4, 65535}},
		{"64 shift down", 64, stored[int64](64, math.MinInt64, 0, 3, 4, 70000, math.MaxInt64), []string{card("BZERO", "-3")}, []uint16{0, 0, 0, 1, 65535, 65535}},
		{"-32", -32, stored(-32, -1e30, -0.5, 0, 0.999, 1, 2.5, 65534.99, 65535, 1e30, nan, inf, -inf), nil, []uint16{0, 0, 0, 0, 1, 2, 65534, 65535, 65535, 0, 65535, 0}},
		{"-32 scaled", -32, stored[float64](-32, 1.25, 0.5, -3, 7000), []string{card("BSCALE", "1.0D1"), card("BZERO", "-5.0E0")}, []uint16{7, 0, 0, 65535}},
		{"-64", -64, stored(-64, -1e300, -0.5, 0, 0.99999999, 1, 65534.99999999, 65535, 65536.5, 1e300, nan, inf, -inf), nil, []uint16{0, 0, 0, 0, 1, 65534, 65535, 65535, 65535, 0, 65535, 0}},
		{"-64 scaled", -64, stored[float64](-64, 0, 1, 2, 3), []string{card("BSCALE", "-1.5"), card("BZERO", "4")}, []uint16{4, 2, 1, 0}},
	} {
		w := len(c.want)
		got, err := decode(t, file(c.bitpix, []int{w, 1}, c.data, c.extra...), nil)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		for x, want := range c.want {
			var v uint16
			switch m := got.(type) {
			case *image.Gray:
				v = uint16(m.GrayAt(x, 0).Y)
			case *image.Gray16:
				v = m.Gray16At(x, 0).Y
			}
			if _, narrow := got.(*image.Gray); narrow != (c.bitpix == 8) {
				t.Errorf("%s: %T", c.name, got)
			}
			if v != want {
				t.Errorf("%s: value %d is %d, want %d", c.name, x, v, want)
			}
		}
	}

	// The first row stored is the bottom one; axes of length 1 after the
	// second change nothing.
	got, err := decode(t, file(16, []int{2, 3, 1, 1}, stored[int64](16, 1, 2, 3, 4, 5, 6)), nil)
	if err != nil {
		t.Fatal(err)
	}
	if m := got.(*image.Gray16); !bytes.Equal(m.Pix, []byte{0, 5, 0, 6, 0, 3, 0, 4, 0, 1, 0, 2}) {
		t.Errorf("rows: %v", m.Pix)
	}

	// The numbers of the 32-bit fixtures stored as 64 bits (which Pillow
	// does not read) are the same pixels.
	for _, c := range []struct {
		name         string
		w, h         int
		from, bitpix int
	}{{"i32_40x28", 40, 28, 32, 64}, {"f32_37x23", 37, 23, -32, -64}} {
		data, err := os.ReadFile(filepath.Join("testdata", c.name+".fits"))
		if err != nil {
			t.Fatal(err)
		}
		var wide []byte
		for i := 0; i < c.w*c.h; i++ {
			v := binary.BigEndian.Uint32(data[blockLen+4*i:])
			if c.from == 32 {
				wide = append(wide, stored(64, int64(int32(v)))...)
			} else {
				wide = append(wide, stored(-64, float64(math.Float32frombits(v)))...)
			}
		}
		got, err := decode(t, file(c.bitpix, []int{c.w, c.h}, wide), nil)
		if err != nil {
			t.Fatal(err)
		}
		same(t, fmt.Sprintf("%s as %d bits", c.name, c.bitpix), got, readPNG(t, filepath.Join("testdata", c.name+".png")))
	}
}

// TestRefused: files that are not FITS, and FITS without an image this
// package reads, each with the error that says which.
func TestRefused(t *testing.T) {
	empty := []string{card("SIMPLE", "T"), card("BITPIX", "8"), card("NAXIS", "0"), card("EXTEND", "T")}
	table := func(kind string, more ...string) []byte {
		cards := append([]string{card("XTENSION", kind), card("BITPIX", "8"), card("NAXIS", "2"), card("NAXIS1", "8"), card("NAXIS2", "4"), card("PCOUNT", "0"), card("GCOUNT", "1"), card("TFIELDS", "1")}, more...)
		return append(hdu(nil, empty...), hdu(make([]byte, 32), cards...)...)
	}
	pix := make([]byte, 64)
	for _, c := range []struct {
		name string
		data []byte
		want error
		says string
	}{
		{"compressed image", table("'BINTABLE'", card("ZIMAGE", "T"), card("ZCMPTYPE", "'RICE_1  '"), card("ZBITPIX", "16"), card("ZNAXIS", "2")), errors.ErrUnsupported, "tile-compressed"},
		{"binary table", table("'BINTABLE'"), errors.ErrUnsupported, "table"},
		{"text table", table("'TABLE   '"), errors.ErrUnsupported, "table"},
		{"other extension", table("'FOREIGN '"), errors.ErrUnsupported, "FOREIGN"},
		{"one dimension", file(8, []int{64}, pix), errors.ErrUnsupported, "one-dimensional"},
		{"cube", file(8, []int{4, 4, 3}, pix), errors.ErrUnsupported, "cube"},
		{"cube of four axes", file(8, []int{4, 4, 1, 2}, pix), errors.ErrUnsupported, "NAXIS4"},
		{"no image", hdu(nil, empty...), errNoImage, ""},
		{"no image, more bytes", append(hdu(nil, empty...), make([]byte, blockLen)...), errNoImage, ""},
		{"no width", file(8, []int{0, 4}, nil), errNoImage, ""},
		{"not simple", hdu(pix, card("SIMPLE", "F"), card("BITPIX", "8"), card("NAXIS", "2"), card("NAXIS1", "8"), card("NAXIS2", "8")), errFormat, ""},
		{"24 bits", file(24, []int{4, 4}, pix), errFormat, ""},
		{"no NAXIS2", hdu(pix, card("SIMPLE", "T"), card("BITPIX", "8"), card("NAXIS", "2"), card("NAXIS1", "8")), errFormat, ""},
		{"no BITPIX", hdu(pix, card("SIMPLE", "T"), card("NAXIS", "2"), card("NAXIS1", "8"), card("NAXIS2", "8")), errFormat, ""},
		{"a width that is no number", file(8, []int{4, 4}, pix, card("NAXIS1", "wide")), errFormat, ""},
		{"too wide", file(8, []int{maxSide + 1, 4}, pix), errFormat, ""},
		{"a BZERO that is no number", file(8, []int{4, 4}, pix, card("BZERO", "NaN")), errFormat, ""},
		{"text", []byte(strings.Repeat("a line of text\n", 400)), errFormat, ""},
		{"nothing", nil, errFormat, ""},
		{"no END", file(8, []int{4, 4}, pix)[:400], io.ErrUnexpectedEOF, ""},
		{"no END in whole blocks", bytes.Repeat([]byte(card("SIMPLE", "T")), 72), io.ErrUnexpectedEOF, ""},
		{"data cut short", file(16, []int{40, 40}, make([]byte, 3200))[:blockLen+3199], io.ErrUnexpectedEOF, ""},
	} {
		_, err := Open(bytes.NewReader(c.data), int64(len(c.data)), nil)
		if !errors.Is(err, c.want) || !strings.Contains(fmt.Sprint(err), c.says) {
			t.Errorf("%s: %v, want %v (%s)", c.name, err, c.want, c.says)
		}
	}
	// The data without the padding of its last block is all there.
	if _, err := decode(t, file(16, []int{40, 40}, make([]byte, 3200))[:blockLen+3200], nil); err != nil {
		t.Errorf("no padding: %v", err)
	}
	for head, want := range map[string]bool{
		"SIMPLE  =                    T / conforms": true,
		"SIMPLE  = T":                          true,
		"SIMPLE  =                    F / not": false,
		"SIMPLE":                               false,
		"XTENSION= 'IMAGE   '":                 false,
	} {
		if Match([]byte(head)) != want {
			t.Errorf("Match(%q) is %v", head, !want)
		}
	}
}

// zeros is a file of any length, all zero bytes after its head.
type zeros struct{ head []byte }

func (z zeros) ReadAt(p []byte, off int64) (int, error) {
	clear(p)
	if off < int64(len(z.head)) {
		copy(p, z.head[off:])
	}
	return len(p), nil
}

// endless is a file of the same card for ever.
type endless struct{ card string }

func (e endless) ReadAt(p []byte, off int64) (int, error) {
	for i := range p {
		p[i] = e.card[(off+int64(i))%cardLen]
	}
	return len(p), nil
}

// TestDamage: fixtures cut short at many lengths and with bytes changed
// are read or refused, without a panic, and give back what they reserved;
// a header that claims a huge image is refused before anything of that
// size is allocated.
func TestDamage(t *testing.T) {
	dir := t.TempDir()
	rnd := rand.New(rand.NewSource(20261122))
	trials, read := 0, 0
	try := func(data []byte) {
		trials++
		env := pipeline.NewEnv(1<<20, dir)
		src, err := Open(bytes.NewReader(data), int64(len(data)), env)
		if err == nil {
			for {
				m, err := src.Next()
				if err == io.EOF {
					read++
				}
				if err != nil {
					break
				}
				if m.Bounds().Empty() {
					t.Fatal("an empty band")
				}
			}
			src.Close()
		}
		if env.Budget.Used() != 0 {
			t.Fatalf("%d bytes still reserved", env.Budget.Used())
		}
	}
	const digits = "0123456789-+.E T'=/"
	for _, name := range fixtures(t) {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		// Cut short: a spread of lengths, and around the ends of blocks.
		for n := 0; n < len(data); n++ {
			if n%blockLen > 2 && n%blockLen < blockLen-2 && rnd.Intn(40) != 0 {
				continue
			}
			try(data[:n])
		}
		// Bytes changed, mostly in the cards that describe the image, half
		// of the time to characters a value can have.
		d := make([]byte, len(data))
		for i := 0; i < 500; i++ {
			copy(d, data)
			for k := rnd.Intn(4) + 1; k > 0; k-- {
				at := rnd.Intn(len(d))
				if rnd.Intn(4) != 0 {
					at = rnd.Intn(6 * cardLen)
				}
				if rnd.Intn(2) == 0 {
					d[at] = digits[rnd.Intn(len(digits))]
				} else {
					d[at] = byte(rnd.Intn(256))
				}
			}
			try(d)
		}
	}
	if trials < 5000 || read < 500 {
		t.Errorf("%d trials, %d read to the end", trials, read)
	}

	// Huge sizes in a small file: the data cannot be there.
	for _, c := range []struct{ bitpix, w, h int }{{8, maxSide, maxSide}, {-64, maxSide, maxSide}, {16, maxSide, 1}, {16, 1, maxSide}} {
		data := file(c.bitpix, []int{c.w, c.h}, make([]byte, 1000))
		env := pipeline.NewEnv(1<<20, dir)
		if _, err := Open(bytes.NewReader(data), int64(len(data)), env); err != io.ErrUnexpectedEOF {
			t.Errorf("%dx%d in a small file: %v", c.w, c.h, err)
		}
		if env.Budget.Peak() != 0 {
			t.Errorf("%dx%d: %d bytes reserved", c.w, c.h, env.Budget.Peak())
		}
	}
	// The same in a file that is as long as it says: a row does not fit the
	// memory limit, which is found out before the row is allocated.
	for _, bitpix := range []int{8, 16, -64} {
		z := zeros{file(bitpix, []int{maxSide, maxSide}, nil)}
		env := pipeline.NewEnv(1<<20, dir)
		if _, err := Open(z, 1<<60, env); !errors.Is(err, pipeline.ErrMemoryLimit) {
			t.Errorf("a huge image of %d bits: %v", bitpix, err)
		}
		if env.Budget.Used() != 0 || env.Budget.Peak() != 0 {
			t.Errorf("a huge image of %d bits: %d bytes reserved", bitpix, env.Budget.Peak())
		}
	}
	// Very tall and narrow fits: it is read a band at a time.
	z := zeros{file(-64, []int{64, maxSide}, nil)}
	env := pipeline.NewEnv(1<<20, dir)
	src, err := Open(z, 1<<60, env)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := src.Next(); err != nil {
			t.Fatal(err)
		}
	}
	src.Close()
	if env.Budget.Used() != 0 || env.Budget.Peak() > 1<<19 {
		t.Errorf("tall image: %d reserved, peak %d", env.Budget.Used(), env.Budget.Peak())
	}
	// A header that never ends is given up on.
	if _, err := Open(endless{card("SIMPLE", "T")}, 1<<60, nil); err != errFormat {
		t.Errorf("endless header: %v", err)
	}
}

// TestMemory: large images are decoded within a few rows of memory.
func TestMemory(t *testing.T) {
	for _, bitpix := range []int{16, 8, -64} {
		w, h := 3000, 2000
		bpp := max(bitpix, -bitpix) / 8
		// The stored number of column x of stored row y.
		value := func(x, y int) int { return (x*7 + y*13) % 30011 }
		data := make([]byte, 0, w*h*bpp)
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				v := value(x, y)
				if bitpix == -64 {
					data = append(data, stored(-64, float64(v)+0.5)...)
				} else {
					data = append(data, stored(bitpix, int64(v))[:bpp]...)
				}
			}
		}
		data = file(bitpix, []int{w, h}, data)
		env := pipeline.NewEnv(8<<20, t.TempDir())
		src, err := Open(bytes.NewReader(data), int64(len(data)), env)
		if err != nil {
			t.Fatal(err)
		}
		rows, bands := 0, 0
		for {
			m, err := src.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			r := m.Bounds()
			for y := r.Min.Y; y < r.Max.Y; y++ {
				for x := 0; x < w; x += 97 {
					want := value(x, h-1-y)
					var got int
					switch b := m.(type) {
					case *image.Gray:
						got, want = int(b.GrayAt(x, y).Y), want&255
					case *image.Gray16:
						got = int(b.Gray16At(x, y).Y)
					}
					if got != want {
						t.Fatalf("%d bits: pixel %d,%d is %d, want %d", bitpix, x, y, got, want)
					}
				}
				rows++
			}
			bands++
		}
		peak := env.Budget.Peak()
		src.Close()
		out := 2
		if bitpix == 8 {
			out = 1
		}
		t.Logf("BITPIX %d, %dx%d (%d bytes stored, %d decoded): %d bands, peak %d bytes", bitpix, w, h, w*h*bpp, w*h*out, bands, peak)
		if rows != h || peak > 1<<20 || env.Budget.Used() != 0 {
			t.Errorf("%d bits: %d rows, peak %d, %d still reserved", bitpix, rows, peak, env.Budget.Used())
		}
	}
}

// FuzzOpen: any file is read or refused, without a panic, within the
// memory it reserves.
func FuzzOpen(f *testing.F) {
	for _, name := range fixtures(f) {
		data, err := os.ReadFile(name)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(data)
	}
	dir := f.TempDir() // one for every input: making one each is slow
	f.Fuzz(func(t *testing.T, data []byte) {
		env := pipeline.NewEnv(4<<20, dir)
		src, err := Open(bytes.NewReader(data), int64(len(data)), env)
		if err != nil {
			return
		}
		for {
			if _, err := src.Next(); err != nil {
				break
			}
		}
		src.Close()
		if env.Budget.Used() != 0 {
			t.Fatalf("%d bytes reserved", env.Budget.Used())
		}
	})
}
