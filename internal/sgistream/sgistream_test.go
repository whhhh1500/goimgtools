package sgistream

import (
	"bytes"
	"encoding/binary"
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

// value is the pixel of m at x, y: red, green, blue and alpha, not
// multiplied, of 16 bits.
func value(m image.Image, x, y int) [4]uint16 {
	switch p := m.(type) {
	case *image.Gray:
		v := uint16(p.GrayAt(x, y).Y) * 257
		return [4]uint16{v, v, v, 0xffff}
	case *image.Gray16:
		v := p.Gray16At(x, y).Y
		return [4]uint16{v, v, v, 0xffff}
	case *image.NRGBA:
		c := p.NRGBAAt(x, y)
		return [4]uint16{uint16(c.R) * 257, uint16(c.G) * 257, uint16(c.B) * 257, uint16(c.A) * 257}
	case *image.NRGBA64:
		c := p.NRGBA64At(x, y)
		return [4]uint16{c.R, c.G, c.B, c.A}
	case *image.RGBA64: // an opaque PNG of 16 bits
		c := p.RGBA64At(x, y)
		return [4]uint16{c.R, c.G, c.B, c.A}
	case *image.RGBA: // an opaque PNG
		c := p.RGBAAt(x, y)
		return [4]uint16{uint16(c.R) * 257, uint16(c.G) * 257, uint16(c.B) * 257, uint16(c.A) * 257}
	}
	panic(fmt.Sprintf("%T", m))
}

func fixtures(t testing.TB) map[string][]byte {
	t.Helper()
	names, _ := filepath.Glob("testdata/*.sgi")
	out := map[string][]byte{}
	for _, name := range names {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		out[strings.TrimSuffix(filepath.Base(name), ".sgi")] = data
	}
	return out
}

// The pixels of the fixtures, as the script that made them computes them.
func flat(x, y int) bool { return (x/9+y/7)%3 == 0 }

func fixtureValue(x, y, w, h, c, z int) int {
	if z == 1 {
		if flat(x, y) {
			return 50 * (x/9 + y/7) % 256
		}
		return (x*5 + y*3) % 256
	}
	switch {
	case c == 3 && flat(x, y):
		return 255
	case c == 3:
		return (x*11 + y*5) % 256
	case flat(x, y):
		return [3]int{40 * (x / 9) % 256, 200, 30 * (y / 7) % 256}[c]
	}
	return [3]int{x * 255 / (w - 1), y * 255 / (h - 1), (x*7 + y*13) % 256}[c]
}

// TestPillow: the files Pillow 12.1.1 wrote (verbatim) and the compressed
// ones it reads come out pixel for pixel as Pillow reads them. Of two
// bytes a channel Pillow keeps the high one; all 16 bits are compared with
// the values the files were made from. The compressed files ffmpeg 9.0.2
// wrote end their rows without a count of zero, and Pillow refuses them:
// the PNGs beside them are the pictures ffmpeg was given, of 16 bits the
// files as ffmpeg reads them back.
func TestPillow(t *testing.T) {
	files := fixtures(t)
	if len(files) != 27 {
		t.Fatalf("%d fixtures, want 27", len(files))
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
		if got.Bounds() != want.Bounds() {
			t.Fatalf("%s: bounds %v, want %v", name, got.Bounds(), want.Bounds())
		}
		kind := strings.Split(name, "_")[0]
		wide := strings.HasSuffix(kind, "16")
		z := map[string]int{"grey": 1, "rgb": 3, "rgba": 4}[strings.TrimRight(kind, "0123456789")]
		b := got.Bounds()
		for y := 0; y < b.Dy(); y++ {
			for x := 0; x < b.Dx(); x++ {
				g, w := value(got, x, y), value(want, x, y)
				exact := w
				if wide {
					for c := range g {
						// Pillow's value is the high byte.
						if g[c]>>8 != w[c]>>8 {
							t.Fatalf("%s: pixel %d,%d is %v, Pillow has %v", name, x, y, g, w)
						}
					}
					for c := 0; c < z; c++ {
						v := fixtureValue(x, y, b.Dx(), b.Dy(), c, z)
						switch {
						case strings.Contains(name, "_ffmpeg_"):
							// The PNG has all 16 bits, as ffmpeg reads the file.
						case strings.Contains(name, "_rle_"):
							exact[c] = uint16((v*257 + x*3 + y) % 65536)
						default:
							exact[c] = uint16(v << 8) // as Pillow writes 8 bits
						}
					}
					if z == 1 {
						exact[1], exact[2] = exact[0], exact[0]
					}
				}
				if g != exact {
					t.Fatalf("%s: pixel %d,%d is %v, want %v", name, x, y, g, exact)
				}
			}
		}
		wantKind := map[string]string{"grey8": "*image.Gray", "grey16": "*image.Gray16", "rgb8": "*image.NRGBA",
			"rgba8": "*image.NRGBA", "rgb16": "*image.NRGBA64", "rgba16": "*image.NRGBA64"}[kind]
		if k := fmt.Sprintf("%T", got); k != wantKind {
			t.Errorf("%s: decoded as %s, want %s", name, k, wantKind)
		}
	}
}

// variant is an SGI file built here from known pixels.
type variant struct {
	name         string
	w, h, z, bpc int
	packed       bool
	dim          int // the number of dimensions in the header, when not what z asks for
	order        int // compressed rows: 0 as usual, 1 the top row first, 2 the channels in turn row by row, 3 equal rows stored once
}

// sample is the value of a pixel in a channel.
func sample(x, y, c, bpc int) uint16 {
	v := x*(c+2) + y*(7-c)
	if (x/6+y/4)%2 == 0 {
		v = 40*c + x/6*16
	}
	if bpc == 2 {
		return uint16(v*259 + c)
	}
	return uint16(v % 256)
}

// packRow is the run-length coding of a row of SGI.
func packRow(out []byte, vals []uint16, bpc int) []byte {
	count := func(c int) {
		if bpc == 2 {
			out = append(out, 0)
		}
		out = append(out, byte(c))
	}
	word := func(v uint16) {
		if bpc == 2 {
			out = append(out, byte(v>>8))
		}
		out = append(out, byte(v))
	}
	for i := 0; i < len(vals); {
		r := 1
		for i+r < len(vals) && r < 127 && vals[i+r] == vals[i] {
			r++
		}
		if r >= 3 {
			count(r)
			word(vals[i])
			i += r
			continue
		}
		j := i
		for j < len(vals) && j-i < 127 && !(j+2 < len(vals) && vals[j] == vals[j+1] && vals[j] == vals[j+2]) {
			j++
		}
		count(0x80 | (j - i))
		for _, v := range vals[i:j] {
			word(v)
		}
		i = j
	}
	count(0)
	return out
}

func sgiHeader(packed bool, bpc, dim, w, h, z int) []byte {
	head := make([]byte, headerSize)
	head[0], head[1], head[3] = 0x01, 0xda, byte(bpc)
	if packed {
		head[2] = 1
	}
	binary.BigEndian.PutUint16(head[4:], uint16(dim))
	binary.BigEndian.PutUint16(head[6:], uint16(w))
	binary.BigEndian.PutUint16(head[8:], uint16(h))
	binary.BigEndian.PutUint16(head[10:], uint16(z))
	binary.BigEndian.PutUint32(head[16:], 255)
	return head
}

// build writes the file of v and the image it holds.
func (v variant) build() ([]byte, image.Image) {
	dim := v.dim
	if dim == 0 {
		dim = 3
		if v.z == 1 {
			dim = 2
		}
	}
	z, h := v.z, v.h
	switch dim { // the sizes that do not count hold something else
	case 1:
		z, h = 7, 9
	case 2:
		z = 3
	}
	file := sgiHeader(v.packed, v.bpc, dim, v.w, h, z)
	val := func(x, y, c int) uint16 {
		if v.order == 3 {
			y /= 3 // rows equal three by three
		}
		return sample(x, y, c, v.bpc)
	}
	want := image.NewNRGBA64(image.Rect(0, 0, v.w, v.h))
	for y := 0; y < v.h; y++ {
		for x := 0; x < v.w; x++ {
			p := want.Pix[want.PixOffset(x, y):]
			put := func(i int, s uint16) {
				if v.bpc == 1 {
					s *= 257
				}
				binary.BigEndian.PutUint16(p[2*i:], s)
			}
			put(3, 0xffff/map[int]uint16{1: 257, 2: 1}[v.bpc])
			switch v.z {
			case 1, 2:
				put(0, val(x, y, 0))
				put(1, val(x, y, 0))
				put(2, val(x, y, 0))
				if v.z == 2 {
					put(3, val(x, y, 1))
				}
			default:
				for c := 0; c < v.z; c++ {
					put(c, val(x, y, c))
				}
			}
		}
	}
	row := func(c, r int) []uint16 { // row r from the bottom
		vals := make([]uint16, v.w)
		for x := range vals {
			vals[x] = val(x, v.h-1-r, c)
		}
		return vals
	}
	if !v.packed {
		for c := 0; c < v.z; c++ {
			for r := 0; r < v.h; r++ {
				for _, s := range row(c, r) {
					if v.bpc == 2 {
						file = append(file, byte(s>>8))
					}
					file = append(file, byte(s))
				}
			}
		}
		return file, want
	}
	entries := v.h * v.z
	tables := make([]byte, 8*entries)
	var body []byte
	seen := map[string]int{}
	store := func(c, r int) {
		data := packRow(nil, row(c, r), v.bpc)
		at, ok := seen[string(data)]
		if !ok || v.order != 3 {
			at = headerSize + len(tables) + len(body)
			seen[string(data)] = at
			body = append(body, data...)
		}
		binary.BigEndian.PutUint32(tables[4*(c*v.h+r):], uint32(at))
		binary.BigEndian.PutUint32(tables[4*(entries+c*v.h+r):], uint32(len(data)))
	}
	switch v.order {
	case 1:
		for c := v.z - 1; c >= 0; c-- {
			for r := v.h - 1; r >= 0; r-- {
				store(c, r)
			}
		}
	case 2:
		for r := 0; r < v.h; r++ {
			for c := 0; c < v.z; c++ {
				store(c, r)
			}
		}
	default:
		for c := 0; c < v.z; c++ {
			for r := 0; r < v.h; r++ {
				store(c, r)
			}
		}
	}
	return append(append(file, tables...), body...), want
}

var variants = []variant{
	{name: "grey and alpha", w: 37, h: 20, z: 2, bpc: 1},
	{name: "grey and alpha, compressed", w: 37, h: 20, z: 2, bpc: 1, packed: true},
	{name: "grey and alpha, 16 bits", w: 37, h: 20, z: 2, bpc: 2},
	{name: "grey and alpha, 16 bits, compressed", w: 37, h: 20, z: 2, bpc: 2, packed: true},
	{name: "grey, 16 bits", w: 37, h: 20, z: 1, bpc: 2},
	{name: "grey, 16 bits, compressed", w: 37, h: 20, z: 1, bpc: 2, packed: true},
	{name: "RGB, 16 bits", w: 37, h: 20, z: 3, bpc: 2},
	{name: "RGBA, 16 bits, compressed", w: 37, h: 20, z: 4, bpc: 2, packed: true},
	{name: "RGBA, compressed, wide", w: 301, h: 9, z: 4, bpc: 1, packed: true},
	{name: "a single row", w: 37, h: 1, z: 1, bpc: 1, dim: 1},
	{name: "a single row, compressed", w: 37, h: 1, z: 1, bpc: 1, dim: 1, packed: true},
	{name: "a single channel of three dimensions", w: 37, h: 20, z: 1, bpc: 1, dim: 3},
	{name: "two dimensions", w: 37, h: 20, z: 1, bpc: 1, dim: 2, packed: true},
	{name: "the top row first", w: 37, h: 20, z: 3, bpc: 1, packed: true, order: 1},
	{name: "the channels in turn", w: 37, h: 20, z: 4, bpc: 1, packed: true, order: 2},
	{name: "equal rows stored once", w: 37, h: 20, z: 3, bpc: 1, packed: true, order: 3},
	{name: "one pixel", w: 1, h: 1, z: 3, bpc: 1, packed: true},
}

// TestVariants: the kinds of file Pillow does not read or write, built
// here.
func TestVariants(t *testing.T) {
	for _, v := range variants {
		file, want := v.build()
		if !Match(file[:32]) {
			t.Errorf("%s: not matched", v.name)
		}
		src, err := Open(bytes.NewReader(file), int64(len(file)), nil)
		if err != nil {
			t.Fatalf("%s: %v", v.name, err)
		}
		if got, opaque := pipeline.IsOpaque(src), v.z == 1 || v.z == 3; got != opaque {
			t.Errorf("%s: opaque %v", v.name, got)
		}
		src.Close()
		got, err := decode(file, nil)
		if err != nil {
			t.Fatalf("%s: %v", v.name, err)
		}
		if got.Bounds() != want.Bounds() {
			t.Fatalf("%s: bounds %v, want %v", v.name, got.Bounds(), want.Bounds())
		}
		for y := 0; y < v.h; y++ {
			for x := 0; x < v.w; x++ {
				if g, w := value(got, x, y), value(want, x, y); g != w {
					t.Fatalf("%s: pixel %d,%d is %v, want %v", v.name, x, y, g, w)
				}
			}
		}
		kind := [2][2]string{{"*image.Gray", "*image.Gray16"}, {"*image.NRGBA", "*image.NRGBA64"}}[min(v.z-1, 1)][v.bpc-1]
		if g := fmt.Sprintf("%T", got); g != kind {
			t.Errorf("%s: decoded as %s, want %s", v.name, g, kind)
		}
	}
}

// TestUnpack: the packets of a row, and the rows that are refused.
func TestUnpack(t *testing.T) {
	for _, c := range []struct {
		src, want string
		bpc       int
		err       error
	}{
		{"\x03A\x82BC\x00", "AAABC", 1, nil},
		{"\x85ABCDE\x00rest", "ABCDE", 1, nil},
		{"\x00\x02AB\x00\x81CD\x00\x00", "ABABCD", 2, nil},
		{"\x03A\x00", "AAAAA", 1, errFormat},                 // short
		{"\x06A\x00", "AAAAA", 1, errFormat},                 // long
		{"\x03A\x82B", "AAABC", 1, io.ErrUnexpectedEOF},      // cut in the values
		{"\x05A", "AAAAA", 1, nil},                           // no end, as ffmpeg writes a row
		{"\x03", "AAA", 1, io.ErrUnexpectedEOF},              // cut after the count
		{"\x00\x03A", "AAAAAA", 2, io.ErrUnexpectedEOF},      // half a value
		{"\x00\x03AB", "ABABAB", 2, nil},                     // no end
		{"\x00\x02AB\x00", "ABABAB", 2, io.ErrUnexpectedEOF}, // half a count
	} {
		dst := make([]byte, len(c.want))
		err := unpack(dst, []byte(c.src), c.bpc)
		if err != c.err || err == nil && string(dst) != c.want {
			t.Errorf("%q: %q, %v; want %q, %v", c.src, dst, err, c.want, c.err)
		}
	}
}

// TestRefused: what is not an SGI file, or one of a kind not read.
func TestRefused(t *testing.T) {
	file, _ := variant{w: 8, h: 8, z: 3, bpc: 1}.build()
	for _, c := range []struct {
		name   string
		change func(b []byte)
		want   error
	}{
		{"the number", func(b []byte) { b[1] = 0xdb }, errFormat},
		{"storage 2", func(b []byte) { b[2] = 2 }, errFormat},
		{"3 bytes a channel", func(b []byte) { b[3] = 3 }, errFormat},
		{"no bytes a channel", func(b []byte) { b[3] = 0 }, errFormat},
		{"no dimensions", func(b []byte) { b[5] = 0 }, errFormat},
		{"4 dimensions", func(b []byte) { b[5] = 4 }, errFormat},
		{"no width", func(b []byte) { b[7] = 0 }, errFormat},
		{"no height", func(b []byte) { b[9] = 0 }, errFormat},
		{"no channels", func(b []byte) { b[11] = 0 }, errFormat},
		{"5 channels", func(b []byte) { b[11] = 5 }, errUnsupported},
		{"more rows than stored", func(b []byte) { b[9] = 9 }, io.ErrUnexpectedEOF},
	} {
		b := bytes.Clone(file)
		c.change(b)
		if _, err := decode(b, nil); err != c.want {
			t.Errorf("%s: %v, want %v", c.name, err, c.want)
		}
		if c.want == errFormat && Match(b[:32]) {
			t.Errorf("%s: matched", c.name)
		}
	}
	for _, head := range []string{"", "\x01", "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR", "qoif\x00\x00\x00\x10\x00\x00\x00\x10\x03\x00",
		"\xff\xd8\xff\xe0\x00\x10JFIF\x00\x01", "GIF89a\x10\x00\x10\x00\x80\x00\x00", "\xda\x01\x00\x01\x00\x02\x00\x08\x00\x08\x00\x01"} {
		if Match([]byte(head)) {
			t.Errorf("%q matched", head)
		}
	}
	// A compressed row that starts in the tables, or has no length.
	packed, _ := variant{w: 8, h: 8, z: 1, bpc: 1, packed: true}.build()
	b := bytes.Clone(packed)
	binary.BigEndian.PutUint32(b[headerSize:], headerSize)
	if _, err := decode(b, nil); err != errFormat {
		t.Errorf("a row in the tables: %v", err)
	}
	b = bytes.Clone(packed)
	binary.BigEndian.PutUint32(b[headerSize+4*8:], 0)
	if _, err := decode(b, nil); err != errFormat {
		t.Errorf("a row of no length: %v", err)
	}
	b = bytes.Clone(packed)
	binary.BigEndian.PutUint32(b[headerSize+4*3:], uint32(len(b)-2))
	if _, err := decode(b, nil); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("a row past the end: %v", err)
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
	for _, v := range variants {
		files[v.name], _ = v.build()
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names) // an order that does not change from run to run
	trials := 0
	for _, name := range names {
		data := files[name]
		for cut := 0; cut < len(data); cut += max(1, len(data)/300) {
			if err := drain(t, name, data[:cut], env); err == nil {
				t.Errorf("%s: read from its first %d of %d bytes", name, cut, len(data))
			}
			trials++
		}
	}
	rng := rand.New(rand.NewPCG(3, 4))
	for i := 0; i < 8000; i++ {
		name := names[rng.IntN(len(names))]
		data := bytes.Clone(files[name])
		for k := 1 + rng.IntN(4); k > 0; k-- {
			at := rng.IntN(len(data))
			switch rng.IntN(3) {
			case 0: // the header
				at = rng.IntN(12)
			case 1: // the tables of a compressed file, the first rows of another
				at = headerSize + rng.IntN(min(len(data)-headerSize, 8*40*4))
			}
			data[at] = byte(rng.Uint32())
		}
		if rng.IntN(8) == 0 {
			data = data[:rng.IntN(len(data)+1)]
		}
		drain(t, name, data, env)
		trials++
	}
	t.Logf("%d damaged files", trials)
}

// TestHuge: a header that claims far more pixels than the file holds
// fails without the image being allocated.
func TestHuge(t *testing.T) {
	for _, c := range []struct {
		name    string
		packed  bool
		w, h, z int
		size    int
	}{
		{"verbatim", false, 65535, 65535, 4, 4096},
		{"compressed", true, 65535, 65535, 4, 4096},
		{"compressed, the tables there", true, 65535, 100, 4, headerSize + 8*100*4 + 100},
	} {
		file := append(sgiHeader(c.packed, 2, 3, c.w, c.h, c.z), make([]byte, c.size-headerSize)...)
		env := pipeline.NewEnv(1<<20, t.TempDir())
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		err := drain(t, c.name, file, env)
		runtime.ReadMemStats(&after)
		if !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, pipeline.ErrMemoryLimit) && err != errFormat {
			t.Errorf("%s: %v", c.name, err)
		}
		if got := after.TotalAlloc - before.TotalAlloc; got > 2<<20 {
			t.Errorf("%s: %d bytes allocated", c.name, got)
		}
	}
}

// TestMemory: 3000 by 2000 pixels of RGBA (24MB decoded, 48MB of 16
// bits) are read in bands, within a few hundred KB, the rows of a
// compressed file through a window and not one by one.
func TestMemory(t *testing.T) {
	const w, h = 3000, 2000
	for _, v := range []variant{
		{name: "verbatim", w: w, h: h, z: 4, bpc: 1},
		{name: "compressed", w: w, h: h, z: 4, bpc: 1, packed: true},
		{name: "compressed, the top row first", w: w, h: h, z: 3, bpc: 1, packed: true, order: 1},
		{name: "compressed, 16 bits", w: w, h: h, z: 4, bpc: 2, packed: true},
	} {
		file, _ := v.buildLarge()
		env := pipeline.NewEnv(8<<20, t.TempDir())
		counter := &countingReader{r: bytes.NewReader(file)}
		src, err := Open(counter, int64(len(file)), env)
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
					want := [4]uint16{0, 0, 0, 0xffff}
					for c := 0; c < v.z; c++ {
						want[c] = sample(x, y, c, v.bpc)
						if v.bpc == 1 {
							want[c] *= 257
						}
					}
					if got := value(m, x, y); got != want {
						t.Fatalf("%s: pixel %d,%d is %v, want %v", v.name, x, y, got, want)
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
		t.Logf("%s: file %d bytes, %d strips, %d reads, peak %d bytes", v.name, len(file), strips, counter.n, peak)
		if peak > 512<<10 {
			t.Errorf("%s: peak %d bytes", v.name, peak)
		}
		if used := env.Budget.Used(); used != 0 {
			t.Errorf("%s: %d bytes still reserved", v.name, used)
		}
		// Three or four reads a channel of a band (the two tables and
		// the window), not one for every row.
		if v.packed && counter.n > 4*strips*v.z {
			t.Errorf("%s: %d reads", v.name, counter.n)
		}
	}
}

// buildLarge is build without the image.
func (v variant) buildLarge() ([]byte, image.Image) {
	file := sgiHeader(v.packed, v.bpc, 3, v.w, v.h, v.z)
	entries := v.h * v.z
	if v.packed {
		file = append(file, make([]byte, 8*entries)...)
	}
	vals := make([]uint16, v.w)
	store := func(c, r int) {
		for x := range vals {
			vals[x] = sample(x, v.h-1-r, c, v.bpc)
		}
		if !v.packed {
			for _, s := range vals {
				if v.bpc == 2 {
					file = append(file, byte(s>>8))
				}
				file = append(file, byte(s))
			}
			return
		}
		at := len(file)
		file = packRow(file, vals, v.bpc)
		binary.BigEndian.PutUint32(file[headerSize+4*(c*v.h+r):], uint32(at))
		binary.BigEndian.PutUint32(file[headerSize+4*(entries+c*v.h+r):], uint32(len(file)-at))
	}
	for c := 0; c < v.z; c++ {
		for r := 0; r < v.h; r++ {
			if v.order == 1 {
				store(v.z-1-c, v.h-1-r)
			} else {
				store(c, r)
			}
		}
	}
	return file, nil
}

// countingReader counts the reads made of r.
type countingReader struct {
	r io.ReaderAt
	n int
}

func (c *countingReader) ReadAt(p []byte, off int64) (int, error) {
	c.n++
	return c.r.ReadAt(p, off)
}

// FuzzOpen: any file is read or refused, without a panic, within the
// memory it reserves.
func FuzzOpen(f *testing.F) {
	for _, data := range fixtures(f) {
		f.Add(data)
	}
	for _, v := range variants {
		data, _ := v.build()
		f.Add(data)
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
