package ddsstream

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// The fixtures of testdata were made with Pillow 12.1.1: files it wrote
// (rgb, rgba, l, la, dxt1, dxt3, dxt5, bc2, bc3, bc5), blocks of random
// bytes under each block format (rnd_*, bc7_modes: every BC7 mode with
// every pattern and rotation) and random pixels under channel masks (m_*).
// Next to each is a PNG of the pixels Pillow reads from it.

// decode reads the whole image of a DDS file, checking what every band
// says of itself on the way.
func decode(t *testing.T, data []byte, env *pipeline.Env) (image.Image, pipeline.Source, error) {
	t.Helper()
	src, err := Open(bytes.NewReader(data), int64(len(data)), env)
	if err != nil {
		return nil, nil, err
	}
	defer src.Close()
	b := src.Bounds()
	var out image.Image
	y := 0
	for {
		m, err := src.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, src, err
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
	return out, src, nil
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

// same reports the first pixel where got is not want, comparing colours
// with their alpha as stored (not multiplied).
func same(t *testing.T, name string, got, want image.Image) {
	t.Helper()
	if got.Bounds() != want.Bounds() {
		t.Errorf("%s: bounds %v, want %v", name, got.Bounds(), want.Bounds())
		return
	}
	b := got.Bounds()
	bad := 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			g := color.NRGBAModel.Convert(got.At(x, y)).(color.NRGBA)
			w := color.NRGBAModel.Convert(want.At(x, y)).(color.NRGBA)
			if g != w {
				if bad == 0 {
					t.Errorf("%s: pixel %d,%d is %v, want %v", name, x, y, g, w)
				}
				bad++
			}
		}
	}
	if bad > 1 {
		t.Errorf("%s: %d pixels differ", name, bad)
	}
}

func fixtures(t testing.TB) []string {
	names, err := filepath.Glob(filepath.Join("testdata", "*.dds"))
	if err != nil || len(names) < 50 {
		t.Fatalf("%d fixtures (%v)", len(names), err)
	}
	return names
}

// TestFixtures: every fixture gives exactly the pixels Pillow reads, as
// grey where Pillow's image is grey and as colour with alpha otherwise.
func TestFixtures(t *testing.T) {
	for _, name := range fixtures(t) {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		want := readPNG(t, strings.TrimSuffix(name, ".dds")+".png")
		got, src, err := decode(t, data, nil)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		same(t, name, got, want)
		_, grey := want.(*image.Gray)
		switch g := got.(type) {
		case *image.Gray:
			if !grey {
				t.Errorf("%s: grey, Pillow's image is %T", name, want)
			}
		case *image.NRGBA:
			if grey {
				t.Errorf("%s: colour, Pillow's image is grey", name)
			}
			if pipeline.IsOpaque(src) && !g.Opaque() {
				t.Errorf("%s: said to be opaque, has transparent pixels", name)
			}
		default:
			t.Errorf("%s: bands of %T", name, got)
		}
	}
}

func fixture(t *testing.T, name string) ([]byte, image.Image) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name+".dds"))
	if err != nil {
		t.Fatal(err)
	}
	return data, readPNG(t, filepath.Join("testdata", name+".png"))
}

// TestDX10Formats: the DXGI formats Pillow does not read (the sRGB and
// typeless names of the block formats, BGRA, BGRX) hold the same data as
// the ones it reads: the same pixels are expected.
func TestDX10Formats(t *testing.T) {
	for _, c := range []struct {
		fixture string
		formats []uint32
	}{
		{"rnd_dx10_bc1", []uint32{70, 72}},
		{"rnd_dx10_bc2", []uint32{73, 75}},
		{"rnd_dx10_bc3", []uint32{76, 78}},
		{"bc2_37x23", []uint32{74, 75}},
		{"bc3_37x23", []uint32{77, 78}},
		{"rnd_dx10_bc4", []uint32{79}},
		{"rnd_dx10_bc5", []uint32{82}},
		{"rnd_dx10_bc7", []uint32{97, 99}},
		{"dx10_rgba", []uint32{27, 29}},
	} {
		data, want := fixture(t, c.fixture)
		for _, f := range c.formats {
			d := append([]byte(nil), data...)
			binary.LittleEndian.PutUint32(d[128:], f)
			got, _, err := decode(t, d, nil)
			if err != nil {
				t.Errorf("%s as format %d: %v", c.fixture, f, err)
				continue
			}
			same(t, c.fixture, got, want)
		}
	}
	// Blue first: the pixels of dx10_rgba with red and blue changed over.
	data, want := fixture(t, "dx10_rgba")
	for _, f := range []uint32{87, 90, 91, 88, 92, 93} {
		d := append([]byte(nil), data...)
		binary.LittleEndian.PutUint32(d[128:], f)
		for i := 148; i+4 <= len(d); i += 4 {
			d[i], d[i+2] = d[i+2], d[i]
		}
		got, src, err := decode(t, d, nil)
		if err != nil {
			t.Errorf("format %d: %v", f, err)
			continue
		}
		w := want
		noAlpha := f == 88 || f == 92 || f == 93
		if noAlpha {
			o := image.NewNRGBA(want.Bounds())
			for i, v := range want.(*image.NRGBA).Pix {
				if i%4 == 3 {
					v = 255
				}
				o.Pix[i] = v
			}
			w = o
		}
		same(t, "dx10 bgra", got, w)
		if pipeline.IsOpaque(src) != noAlpha {
			t.Errorf("format %d: opaque %v", f, !noAlpha)
		}
	}
}

// ddsHeader is a DDS header for the tests.
func ddsHeader(w, h int, flags uint32, fourcc string, depth uint32, masks [4]uint32, dxgi uint32) []byte {
	b := make([]byte, 128, 148)
	copy(b, "DDS ")
	le := binary.LittleEndian
	le.PutUint32(b[4:], 124)
	le.PutUint32(b[8:], 0x1007)
	le.PutUint32(b[12:], uint32(h))
	le.PutUint32(b[16:], uint32(w))
	le.PutUint32(b[76:], 32)
	le.PutUint32(b[80:], flags)
	copy(b[84:88], fourcc)
	le.PutUint32(b[88:], depth)
	for i, m := range masks {
		le.PutUint32(b[92+4*i:], m)
	}
	le.PutUint32(b[108:], 0x1000)
	if fourcc == "DX10" {
		b = b[:148]
		le.PutUint32(b[128:], dxgi)
		le.PutUint32(b[132:], 3)
		le.PutUint32(b[140:], 1)
	}
	return b
}

// TestRefused: files that are not DDS, and kinds of DDS that are not read,
// each with the error that says which.
func TestRefused(t *testing.T) {
	body := make([]byte, 4096)
	none := [4]uint32{}
	for _, c := range []struct {
		name string
		data []byte
		want error
	}{
		{"BC6H", ddsHeader(8, 8, pfFourCC, "DX10", 0, none, 95), errors.ErrUnsupported},
		{"signed BC5", ddsHeader(8, 8, pfFourCC, "DX10", 0, none, 84), errors.ErrUnsupported},
		{"16-bit float", ddsHeader(8, 8, pfFourCC, "DX10", 0, none, 10), errors.ErrUnsupported},
		{"BC5S", ddsHeader(8, 8, pfFourCC, "BC5S", 0, none, 0), errors.ErrUnsupported},
		{"DXT2", ddsHeader(8, 8, pfFourCC, "DXT2", 0, none, 0), errors.ErrUnsupported},
		{"colour map", ddsHeader(8, 8, pfPalette8, "", 8, none, 0), errors.ErrUnsupported},
		{"alpha only", ddsHeader(8, 8, 2, "", 8, none, 0), errors.ErrUnsupported},
		{"15 bits", ddsHeader(8, 8, pfRGB, "", 15, [4]uint32{0x7c00, 0x3e0, 0x1f}, 0), errors.ErrUnsupported},
		{"luminance 16", ddsHeader(8, 8, pfLuminance, "", 16, none, 0), errors.ErrUnsupported},
		{"no width", ddsHeader(0, 8, pfFourCC, "DXT1", 0, none, 0), errFormat},
		{"no height", ddsHeader(8, 0, pfFourCC, "DXT1", 0, none, 0), errFormat},
		{"too wide", ddsHeader(maxSide+1, 8, pfFourCC, "DXT1", 0, none, 0), errFormat},
		{"other magic", append([]byte("DDS!"), ddsHeader(8, 8, pfFourCC, "DXT1", 0, none, 0)[4:]...), errFormat},
		{"header size", func() []byte {
			b := ddsHeader(8, 8, pfFourCC, "DXT1", 0, none, 0)
			b[4] = 100
			return b
		}(), errFormat},
		{"empty", nil, errFormat},
	} {
		data := append(c.data, body...)
		if c.data == nil {
			data = nil
		}
		_, err := Open(bytes.NewReader(data), int64(len(data)), nil)
		if !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", c.name, err, c.want)
		}
	}
	// A header alone, and one cut short.
	h := ddsHeader(8, 8, pfFourCC, "DXT1", 0, none, 0)
	if _, err := Open(bytes.NewReader(h), int64(len(h)), nil); err != io.ErrUnexpectedEOF {
		t.Errorf("no data: %v", err)
	}
	if _, err := Open(bytes.NewReader(h[:100]), 100, nil); err != io.ErrUnexpectedEOF {
		t.Errorf("cut header: %v", err)
	}
	if !Match(h[:32]) || Match([]byte("DDS")) || Match([]byte("DDSX and more")) {
		t.Error("Match")
	}
}

// TestBands: an image of many bands is put together from the right blocks
// and bytes: each pixel is looked up in the file on its own.
func TestBands(t *testing.T) {
	rnd := rand.New(rand.NewSource(7))
	// DXT5, 601 by 1003: neither a multiple of 4.
	w, h := 601, 1003
	across, down := (w+3)/4, (h+3)/4
	data := ddsHeader(w, h, pfFourCC, "DXT5", 0, [4]uint32{}, 0)
	blocks := make([]byte, across*down*16)
	rnd.Read(blocks)
	data = append(data, blocks...)
	got, _, err := decode(t, data, nil)
	if err != nil {
		t.Fatal(err)
	}
	m := got.(*image.NRGBA)
	var px [64]byte
	for by := 0; by < down; by++ {
		for bx := 0; bx < across; bx++ {
			blk := blocks[(by*across+bx)*16:][:16]
			colours(&px, blk[8:], true)
			interpolated(px[3:], 4, blk)
			for i := 0; i < 16; i++ {
				x, y := 4*bx+i%4, 4*by+i/4
				if x < w && y < h && !bytes.Equal(m.Pix[m.PixOffset(x, y):][:4], px[4*i:4*i+4]) {
					t.Fatalf("pixel %d,%d is %v, want %v", x, y, m.Pix[m.PixOffset(x, y):][:4], px[4*i:4*i+4])
				}
			}
		}
	}
	// 24 bits, blue first, 700 by 900.
	w, h = 700, 900
	data = ddsHeader(w, h, pfRGB, "", 24, [4]uint32{0xff0000, 0xff00, 0xff}, 0)
	pix := make([]byte, w*h*3)
	rnd.Read(pix)
	data = append(data, pix...)
	got, src, err := decode(t, data, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !pipeline.IsOpaque(src) {
		t.Error("24 bits not opaque")
	}
	m = got.(*image.NRGBA)
	for i := 0; i < w*h; i++ {
		if p, q := m.Pix[4*i:4*i+4], pix[3*i:]; p[0] != q[2] || p[1] != q[1] || p[2] != q[0] || p[3] != 255 {
			t.Fatalf("pixel %d is %v, stored %v", i, p, q[:3])
		}
	}
}

// zeros is a file of any length, all zero bytes after its header.
type zeros struct{ head []byte }

func (z zeros) ReadAt(p []byte, off int64) (int, error) {
	clear(p)
	if off < int64(len(z.head)) {
		copy(p, z.head[off:])
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
	trials := 0
	try := func(data []byte) {
		trials++
		env := pipeline.NewEnv(1<<20, dir)
		src, err := Open(bytes.NewReader(data), int64(len(data)), env)
		if err == nil {
			for {
				m, err := src.Next()
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
	for _, name := range fixtures(t) {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		// Cut short: every length of the headers, then a spread of others.
		for n := 0; n < len(data); n++ {
			if n > 160 && rnd.Intn(len(data)/24+1) != 0 {
				continue
			}
			try(data[:n])
		}
		// Bytes changed, mostly in the headers.
		d := make([]byte, len(data))
		for i := 0; i < 80; i++ {
			copy(d, data)
			for k := rnd.Intn(4) + 1; k > 0; k-- {
				at := rnd.Intn(len(d))
				if rnd.Intn(3) != 0 {
					at = rnd.Intn(min(len(d), 148))
				}
				d[at] = byte(rnd.Intn(256))
			}
			try(d)
		}
	}
	if trials < 5000 {
		t.Errorf("%d trials", trials)
	}

	// Huge sizes in a small file: the data cannot be there.
	for _, c := range []struct {
		w, h   int
		fourcc string
	}{{maxSide, maxSide, "DXT1"}, {maxSide, maxSide, "DX10"}, {maxSide, 1, "DXT5"}, {1, maxSide, "DXT5"}, {60000, 60000, ""}} {
		flags, depth := uint32(pfFourCC), uint32(0)
		if c.fourcc == "" {
			flags, depth = pfRGB|pfAlphaPixels, 32
		}
		data := append(ddsHeader(c.w, c.h, flags, c.fourcc, depth, [4]uint32{0xff, 0xff00, 0xff0000, 0xff000000}, 98), make([]byte, 1000)...)
		env := pipeline.NewEnv(1<<20, dir)
		if _, err := Open(bytes.NewReader(data), int64(len(data)), env); err != io.ErrUnexpectedEOF {
			t.Errorf("%dx%d %q in a small file: %v", c.w, c.h, c.fourcc, err)
		}
		if env.Budget.Peak() != 0 {
			t.Errorf("%dx%d: %d bytes reserved", c.w, c.h, env.Budget.Peak())
		}
	}
	// The same in a file that is as long as it says: a row does not fit the
	// memory limit, which is found out before the row is allocated.
	for _, fourcc := range []string{"DXT1", "DX10", ""} {
		flags, depth := uint32(pfFourCC), uint32(0)
		if fourcc == "" {
			flags, depth = pfRGB, 24
		}
		z := zeros{ddsHeader(maxSide, maxSide, flags, fourcc, depth, [4]uint32{0xff0000, 0xff00, 0xff}, 98)}
		env := pipeline.NewEnv(1<<20, dir)
		if _, err := Open(z, 1<<60, env); !errors.Is(err, pipeline.ErrMemoryLimit) {
			t.Errorf("a huge image %q: %v", fourcc, err)
		}
		if env.Budget.Used() != 0 || env.Budget.Peak() != 0 {
			t.Errorf("a huge image %q: %d bytes reserved", fourcc, env.Budget.Peak())
		}
	}
	// Very tall and narrow fits: it is read a band at a time.
	z := zeros{ddsHeader(64, maxSide, pfFourCC, "DXT5", 0, [4]uint32{}, 0)}
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
}

// TestMemory: large images are decoded within a few rows of memory.
func TestMemory(t *testing.T) {
	// 4096 by 4096 as DXT5: one row of random blocks, repeated.
	const side = 4096
	rnd := rand.New(rand.NewSource(3))
	row := make([]byte, side/4*16)
	rnd.Read(row)
	data := append(ddsHeader(side, side, pfFourCC, "DXT5", 0, [4]uint32{}, 0), bytes.Repeat(row, side/4)...)
	// The four rows every row of blocks decodes to.
	ref, _, err := decode(t, append(ddsHeader(side, 4, pfFourCC, "DXT5", 0, [4]uint32{}, 0), row...), nil)
	if err != nil {
		t.Fatal(err)
	}
	refPix := ref.(*image.NRGBA).Pix

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
		b := m.(*image.NRGBA)
		for y := b.Rect.Min.Y; y < b.Rect.Max.Y; y++ {
			if !bytes.Equal(b.Pix[b.PixOffset(0, y):][:4*side], refPix[(y%4)*4*side:][:4*side]) {
				t.Fatalf("row %d differs", y)
			}
			rows++
		}
		bands++
	}
	peak := env.Budget.Peak()
	src.Close()
	t.Logf("DXT5 %dx%d (%d bytes decoded): %d bands, peak %d bytes", side, side, 4*side*side, bands, peak)
	if rows != side || peak > 1<<20 || env.Budget.Used() != 0 {
		t.Errorf("%d rows, peak %d, %d still reserved", rows, peak, env.Budget.Used())
	}

	// 3000 by 2000 of 16 bits under masks.
	w, h := 3000, 2000
	data = append(ddsHeader(w, h, pfRGB, "", 16, [4]uint32{0xf800, 0x07e0, 0x001f}, 0), make([]byte, w*h*2)...)
	rnd.Read(data[128:])
	env = pipeline.NewEnv(8<<20, t.TempDir())
	src, err = Open(bytes.NewReader(data), int64(len(data)), env)
	if err != nil {
		t.Fatal(err)
	}
	rows = 0
	for {
		m, err := src.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		rows += m.Bounds().Dy()
	}
	peak = env.Budget.Peak()
	src.Close()
	t.Logf("R5G6B5 %dx%d (%d bytes decoded): peak %d bytes", w, h, 4*w*h, peak)
	if rows != h || peak > 1<<20 || env.Budget.Used() != 0 {
		t.Errorf("%d rows, peak %d, %d still reserved", rows, peak, env.Budget.Used())
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
