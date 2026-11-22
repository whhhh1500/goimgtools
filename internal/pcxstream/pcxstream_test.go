package pcxstream

import (
	"bytes"
	"encoding/binary"
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

// fixtures are the files of testdata Pillow wrote or read.
func fixtures(t testing.TB) map[string][]byte {
	t.Helper()
	names, _ := filepath.Glob("testdata/*.pcx")
	out := map[string][]byte{}
	for _, name := range names {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		out[strings.TrimSuffix(filepath.Base(name), ".pcx")] = data
	}
	return out
}

// TestPillow: the files Pillow 12.1.1 wrote (two colours, grey, palette,
// RGB), those ffmpeg 9.0.2 wrote and the planar 4 and 16 colour ones
// Pillow reads come out pixel for pixel as Pillow reads them.
func TestPillow(t *testing.T) {
	files := fixtures(t)
	if len(files) != 14 {
		t.Fatalf("%d fixtures, want 14", len(files))
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
		kind := fmt.Sprintf("%T", got)
		wantKind := map[string]string{"mono": "*image.Gray", "grey": "*image.Gray", "pal": "*image.Paletted",
			"ega4": "*image.Paletted", "ega16": "*image.Paletted", "rgb": "*image.NRGBA"}[strings.Split(name, "_")[0]]
		if kind != wantKind {
			t.Errorf("%s: decoded as %s, want %s", name, kind, wantKind)
		}
	}
}

// variant is a PCX file built here from known pixels.
type variant struct {
	name         string
	bits, planes int
	w, h         int
	raw          bool // not compressed
	across       bool // runs go on from one row into the next
	flat         bool // rows of one colour, three by three: runs of the longest
	rowBytes     int  // of a plane as the header says; 0 for what the width needs, made even
	stored       int  // of a plane as stored, when not rowBytes
	noPalette    bool // the colours of the header left zero
	tail         int  // 1: a palette at the end, 2: a grey one
	version      byte
}

// index is the number of the colour of a pixel, of n colours.
func index(x, y, n int) int {
	if (x/5+y/3)%2 == 0 {
		return x / 5 % n
	}
	return (x*3 + y*5 + x*y) % n
}

// sample is the value of a pixel in a plane of eight bits.
func sample(x, y, p int) uint8 {
	if (x/6+y/4)%2 == 0 {
		return uint8(40*p + x/6*16)
	}
	return uint8(x*(p+2) + y*(7-p))
}

func headerColour(i int) color.NRGBA {
	return color.NRGBA{uint8(i*16 + 1), uint8(255 - i*13), uint8(i * 7 % 256), 255}
}

func tailColour(i int) color.NRGBA {
	return color.NRGBA{uint8(i), uint8(255 - i), uint8(i * 37 % 256), 255}
}

// packRun is the run-length coding of PCX.
func packRun(out, row []byte) []byte {
	for i := 0; i < len(row); {
		n := 1
		for i+n < len(row) && n < 63 && row[i+n] == row[i] {
			n++
		}
		if n > 1 || row[i] >= 0xc0 {
			out = append(out, 0xc0|byte(n), row[i])
		} else {
			out = append(out, row[i])
		}
		i += n
	}
	return out
}

// build writes the file of v and the image it holds.
func (v variant) build() ([]byte, image.Image) {
	stored := (v.w*v.bits + 7) / 8
	stored += stored % 2
	if v.rowBytes != 0 {
		stored = v.rowBytes
	}
	if v.stored != 0 {
		stored = v.stored
	}
	head := make([]byte, headerSize)
	head[0], head[1], head[2], head[3] = 10, 5, 1, byte(v.bits)
	if v.version != 0 {
		head[1] = v.version
	}
	if v.raw {
		head[2] = 0
	}
	// A window that does not start at 0, as some files have.
	binary.LittleEndian.PutUint16(head[4:], 3)
	binary.LittleEndian.PutUint16(head[6:], 2)
	binary.LittleEndian.PutUint16(head[8:], uint16(v.w+2))
	binary.LittleEndian.PutUint16(head[10:], uint16(v.h+1))
	head[65] = byte(v.planes)
	binary.LittleEndian.PutUint16(head[66:], uint16(v.rowBytes))
	if v.rowBytes == 0 && v.stored == 0 {
		binary.LittleEndian.PutUint16(head[66:], uint16(stored))
	}
	colours := 1 << (v.bits * v.planes)
	want := image.NewNRGBA(image.Rect(0, 0, v.w, v.h))
	if v.bits < 8 && !v.noPalette {
		for i := 0; i < 16; i++ {
			c := headerColour(i)
			head[16+3*i], head[17+3*i], head[18+3*i] = c.R, c.G, c.B
		}
	}
	var body, all []byte
	for y := 0; y < v.h; y++ {
		row := make([]byte, v.planes*stored)
		for i := range row {
			row[i] = 0xee // what lies past the width is not looked at
		}
		for p := 0; p < v.planes; p++ {
			clear(row[p*stored : p*stored+(v.w*v.bits+7)/8])
		}
		for x := 0; x < v.w; x++ {
			i := index(x, y, min(colours, 256))
			if v.flat {
				i = y / 3 * 5 % min(colours, 256)
			}
			switch {
			case v.bits == 8 && v.planes == 1:
				row[x] = uint8(i)
				switch v.tail {
				case 1:
					want.SetNRGBA(x, y, tailColour(i))
				default:
					want.SetNRGBA(x, y, color.NRGBA{uint8(i), uint8(i), uint8(i), 255})
				}
				continue
			case v.bits == 8:
				c := color.NRGBA{A: 255}
				for p := 0; p < v.planes; p++ {
					row[p*stored+x] = sample(x, y, p)
				}
				c.R, c.G, c.B = sample(x, y, 0), sample(x, y, 1), sample(x, y, 2)
				if v.planes == 4 {
					c.A = sample(x, y, 3)
				}
				want.SetNRGBA(x, y, c)
				continue
			case v.bits == 1:
				for p := 0; p < v.planes; p++ {
					if i>>p&1 != 0 {
						row[p*stored+x/8] |= 0x80 >> (x % 8)
					}
				}
			case v.bits == 2:
				row[x/4] |= byte(i) << (6 - 2*(x%4))
			case v.bits == 4:
				row[x/2] |= byte(i) << (4 - 4*(x%2))
			}
			switch {
			case colours == 2:
				want.SetNRGBA(x, y, color.NRGBA{uint8(255 * i), uint8(255 * i), uint8(255 * i), 255})
			case v.noPalette && colours == 4:
				c := standard[[]int{0, 11, 13, 15}[i]]
				want.SetNRGBA(x, y, color.NRGBA{c[0], c[1], c[2], 255})
			case v.noPalette:
				c := standard[i]
				want.SetNRGBA(x, y, color.NRGBA{c[0], c[1], c[2], 255})
			default:
				want.SetNRGBA(x, y, headerColour(i))
			}
		}
		switch {
		case v.raw:
			body = append(body, row...)
		case v.across:
			all = append(all, row...)
		default:
			body = packRun(body, row)
		}
	}
	if v.across {
		body = packRun(body, all)
	}
	file := append(head, body...)
	if v.tail != 0 {
		file = append(file, 12)
		for i := 0; i < 256; i++ {
			c := tailColour(i)
			if v.tail == 2 {
				c = color.NRGBA{uint8(i), uint8(i), uint8(i), 255}
			}
			file = append(file, c.R, c.G, c.B)
		}
	}
	return file, want
}

var variants = []variant{
	{name: "two colours", bits: 1, planes: 1, w: 37, h: 20},
	{name: "two colours, not compressed", bits: 1, planes: 1, w: 37, h: 20, raw: true},
	{name: "2 planes of 1 bit", bits: 1, planes: 2, w: 37, h: 20},
	{name: "3 planes of 1 bit", bits: 1, planes: 3, w: 37, h: 20},
	{name: "4 planes of 1 bit", bits: 1, planes: 4, w: 37, h: 20},
	{name: "4 planes of 1 bit, not compressed", bits: 1, planes: 4, w: 40, h: 20, raw: true},
	{name: "4 planes of 1 bit, no palette", bits: 1, planes: 4, w: 37, h: 20, noPalette: true, version: 3},
	{name: "2 bits", bits: 2, planes: 1, w: 37, h: 20},
	{name: "2 bits, no palette", bits: 2, planes: 1, w: 39, h: 20, noPalette: true},
	{name: "4 bits", bits: 4, planes: 1, w: 37, h: 20},
	{name: "4 bits, not compressed", bits: 4, planes: 1, w: 37, h: 20, raw: true},
	{name: "8 bits, no palette", bits: 8, planes: 1, w: 37, h: 20},
	{name: "8 bits, palette", bits: 8, planes: 1, w: 37, h: 20, tail: 1},
	{name: "8 bits, palette, not compressed", bits: 8, planes: 1, w: 37, h: 20, tail: 1, raw: true},
	{name: "8 bits, grey palette", bits: 8, planes: 1, w: 37, h: 20, tail: 2},
	{name: "3 planes of 8 bits", bits: 8, planes: 3, w: 37, h: 20},
	{name: "3 planes of 8 bits, not compressed", bits: 8, planes: 3, w: 37, h: 20, raw: true},
	{name: "4 planes of 8 bits", bits: 8, planes: 4, w: 37, h: 20},
	{name: "long runs", bits: 8, planes: 1, w: 200, h: 9, flat: true, tail: 1},
	{name: "long runs across rows", bits: 4, planes: 1, w: 300, h: 9, flat: true, across: true},
	{name: "long runs across bands", bits: 8, planes: 1, w: 8, h: 600, flat: true, across: true},
	{name: "runs across rows", bits: 8, planes: 3, w: 37, h: 20, across: true},
	{name: "runs across rows, 1 bit", bits: 1, planes: 4, w: 37, h: 20, across: true},
	{name: "rows longer than the width needs", bits: 8, planes: 3, w: 37, h: 20, rowBytes: 44},
	{name: "odd rows", bits: 8, planes: 3, w: 37, h: 20, rowBytes: 37},
	{name: "row size left zero", bits: 8, planes: 3, w: 37, h: 20, stored: 38},
	{name: "version 2", bits: 1, planes: 4, w: 37, h: 20, version: 2},
}

// TestVariants: the kinds of file Pillow does not write, built here.
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
		if got, opaque := pipeline.IsOpaque(src), !(v.bits == 8 && v.planes == 4); got != opaque {
			t.Errorf("%s: opaque %v", v.name, got)
		}
		src.Close()
		got, err := decode(file, nil)
		if err != nil {
			t.Fatalf("%s: %v", v.name, err)
		}
		same(t, v.name, got, want)
		kind := "*image.Paletted"
		switch {
		case v.bits == 8 && v.planes > 1:
			kind = "*image.NRGBA"
		case v.bits*v.planes == 1, v.bits == 8 && v.tail != 1:
			kind = "*image.Gray"
		}
		if g := fmt.Sprintf("%T", got); g != kind {
			t.Errorf("%s: decoded as %s, want %s", v.name, g, kind)
		}
	}
}

// TestRefused: what is not a PCX file, or one of a kind not read.
func TestRefused(t *testing.T) {
	file, _ := variant{bits: 8, planes: 3, w: 8, h: 8}.build()
	for _, c := range []struct {
		name   string
		change func(b []byte)
		want   error
	}{
		{"first byte", func(b []byte) { b[0] = 11 }, errFormat},
		{"version 1", func(b []byte) { b[1] = 1 }, errFormat},
		{"version 6", func(b []byte) { b[1] = 6 }, errFormat},
		{"encoding 2", func(b []byte) { b[2] = 2 }, errFormat},
		{"3 bits", func(b []byte) { b[3] = 3 }, errFormat},
		{"empty window", func(b []byte) { b[8], b[9] = 0, 0 }, errFormat},
		{"2 planes of 8 bits", func(b []byte) { b[65] = 2 }, errUnsupported},
		{"no planes", func(b []byte) { b[65] = 0 }, errUnsupported},
		{"2 planes of 4 bits", func(b []byte) { b[3], b[65] = 4, 2 }, errUnsupported},
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
	for _, head := range []string{"", "\n", "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR", "qoif\x00\x00\x00\x10\x00\x00\x00\x10\x03\x00",
		"\xff\xd8\xff\xe0\x00\x10JFIF\x00\x01", "GIF89a\x10\x00\x10\x00\x80\x00\x00", "BM\x46\x00\x00\x00\x00\x00\x00\x00\x36\x00"} {
		if Match([]byte(head)) {
			t.Errorf("%q matched", head)
		}
	}
	if _, err := decode(file[:100], nil); err != errFormat {
		t.Errorf("a short header: %v", err)
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
	// In an order that does not change from run to run.
	for i := range names {
		for j := i + 1; j < len(names); j++ {
			if names[j] < names[i] {
				names[i], names[j] = names[j], names[i]
			}
		}
	}
	trials := 0
	for _, name := range names {
		data := files[name]
		step := max(1, len(data)/400)
		for cut := 0; cut < len(data); cut += step {
			drain(t, name, data[:cut], env)
			trials++
		}
	}
	rng := rand.New(rand.NewPCG(1, 2))
	for i := 0; i < 6000; i++ {
		name := names[rng.IntN(len(names))]
		data := bytes.Clone(files[name])
		for k := 1 + rng.IntN(4); k > 0; k-- {
			at := rng.IntN(len(data))
			if rng.IntN(2) == 0 {
				at = rng.IntN(min(len(data), 70))
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

// TestTruncated: a file without its last rows is an error, not an image.
func TestTruncated(t *testing.T) {
	for _, v := range variants {
		file, _ := v.build()
		end := len(file)
		if v.tail != 0 {
			end -= tailSize
		}
		rows := end - headerSize
		short := append(bytes.Clone(file[:headerSize+rows/2]), file[end:]...)
		if _, err := decode(short, nil); !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Errorf("%s: half the rows: %v", v.name, err)
		}
	}
}

// TestHuge: a header that claims 65536 by 65536 pixels over a few bytes
// fails without the image being allocated.
func TestHuge(t *testing.T) {
	for _, raw := range []bool{false, true} {
		file, _ := variant{bits: 8, planes: 3, w: 8, h: 8, raw: raw}.build()
		binary.LittleEndian.PutUint16(file[4:], 0)
		binary.LittleEndian.PutUint16(file[6:], 0)
		binary.LittleEndian.PutUint16(file[8:], 65535)
		binary.LittleEndian.PutUint16(file[10:], 65535)
		binary.LittleEndian.PutUint16(file[66:], 65535)
		env := pipeline.NewEnv(1<<20, t.TempDir())
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		err := drain(t, "huge", file, env)
		runtime.ReadMemStats(&after)
		if !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, pipeline.ErrMemoryLimit) {
			t.Errorf("not compressed %v: %v", raw, err)
		}
		if got := after.TotalAlloc - before.TotalAlloc; got > 2<<20 {
			t.Errorf("not compressed %v: %d bytes allocated", raw, got)
		}
	}
}

// TestMemory: 3000 by 2000 pixels of true colour (24MB decoded) are read
// in bands, within a few hundred KB.
func TestMemory(t *testing.T) {
	const w, h = 3000, 2000
	for _, raw := range []bool{false, true} {
		file := make([]byte, headerSize, headerSize+3*w*h)
		file[0], file[1], file[2], file[3] = 10, 5, 1, 8
		if raw {
			file[2] = 0
		}
		binary.LittleEndian.PutUint16(file[8:], w-1)
		binary.LittleEndian.PutUint16(file[10:], h-1)
		file[65] = 3
		binary.LittleEndian.PutUint16(file[66:], w)
		row := make([]byte, 3*w)
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				row[x], row[w+x], row[2*w+x] = sample(x, y, 0), sample(x, y, 1), sample(x, y, 2)
			}
			if raw {
				file = append(file, row...)
			} else {
				file = packRun(file, row)
			}
		}
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
			b := m.(*image.NRGBA)
			if b.Rect.Min.Y != y || b.Rect.Dx() != w {
				t.Fatalf("strip %v at row %d", b.Rect, y)
			}
			for ; y < b.Rect.Max.Y; y++ {
				p := b.Pix[b.PixOffset(0, y):]
				for x := 0; x < w; x++ {
					if p[4*x] != sample(x, y, 0) || p[4*x+1] != sample(x, y, 1) || p[4*x+2] != sample(x, y, 2) || p[4*x+3] != 255 {
						t.Fatalf("pixel %d,%d is %v", x, y, p[4*x:4*x+4])
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
		t.Logf("not compressed %v: file %d bytes, %d strips, peak %d bytes", raw, len(file), strips, peak)
		if peak > 512<<10 {
			t.Errorf("peak %d bytes", peak)
		}
		if used := env.Budget.Used(); used != 0 {
			t.Errorf("%d bytes still reserved", used)
		}
	}
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
