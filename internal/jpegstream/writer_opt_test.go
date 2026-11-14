package jpegstream

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

var modes = []struct {
	name string
	o    Options
}{
	{"optimize", Options{Optimize: true}},
	{"progressive", Options{Progressive: true}},
	{"progressive+optimize", Options{Optimize: true, Progressive: true}},
}

// encodeMode encodes m in strips of the given height and checks that the
// budget is given back.
func encodeMode(t *testing.T, m image.Image, strip int, o Options) []byte {
	t.Helper()
	env := pipeline.NewEnv(0, t.TempDir())
	var buf bytes.Buffer
	if err := Encode(&buf, testutil.ChoppedEnv(m, strip, env), &o); err != nil {
		t.Fatalf("encode %+v: %v", o, err)
	}
	if env.Budget.Used() != 0 {
		t.Fatalf("%d bytes still reserved after Encode", env.Budget.Used())
	}
	return buf.Bytes()
}

// planes decodes with the standard library and returns the samples of the
// image's planes (Y, or Y, Cb and Cr at their own resolution), without the
// padding to whole MCUs that the decoder's buffers also hold: a scan of one
// component does not code the blocks that are only padding.
func planes(t *testing.T, what string, data []byte) (image.Rectangle, [][]byte) {
	t.Helper()
	m, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("%s: image/jpeg: %v", what, err)
	}
	crop := func(pix []byte, stride, w, h int) []byte {
		var out []byte
		for y := 0; y < h; y++ {
			out = append(out, pix[y*stride:y*stride+w]...)
		}
		return out
	}
	switch m := m.(type) {
	case *image.Gray:
		return m.Rect, [][]byte{crop(m.Pix, m.Stride, m.Rect.Dx(), m.Rect.Dy())}
	case *image.YCbCr:
		if m.SubsampleRatio != image.YCbCrSubsampleRatio420 || m.Rect.Min != (image.Point{}) {
			t.Fatalf("%s: decoded to %v at %v", what, m.SubsampleRatio, m.Rect)
		}
		cw, ch := (m.Rect.Dx()+1)/2, (m.Rect.Dy()+1)/2
		return m.Rect, [][]byte{
			crop(m.Y, m.YStride, m.Rect.Dx(), m.Rect.Dy()),
			crop(m.Cb, m.CStride, cw, ch),
			crop(m.Cr, m.CStride, cw, ch),
		}
	}
	t.Fatalf("%s: decoded to %T", what, m)
	return image.Rectangle{}, nil
}

// checkSamePixels requires data to decode, with the standard library and
// with this package, to exactly what the plain file decodes to.
func checkSamePixels(t *testing.T, what string, plain, data []byte) {
	t.Helper()
	wr, wp := planes(t, what+" plain", plain)
	gr, gp := planes(t, what, data)
	if wr != gr || len(wp) != len(gp) {
		t.Fatalf("%s: decoded to %v with %d planes, plain to %v with %d", what, gr, len(gp), wr, len(wp))
	}
	for i := range wp {
		if !bytes.Equal(wp[i], gp[i]) {
			t.Fatalf("%s: plane %d differs from the plain file's", what, i)
		}
	}
	want, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeStream(data, nil)
	if err != nil {
		t.Fatalf("%s: jpegstream.Open: %v", what, err)
	}
	same(t, what, want, got)
}

func hasMarker(data []byte, marker byte) bool {
	// Only the segments before the first scan are walked.
	for i := 2; i+4 <= len(data) && data[i] == 0xff; {
		if data[i+1] == marker {
			return true
		}
		if data[i+1] == sosMarker {
			break
		}
		i += 2 + int(data[i+2])<<8 + int(data[i+3])
	}
	return false
}

// TestOptimizedPixels: the optimized and the progressive file hold the same
// coefficients as the plain one, for every pixel type, awkward sizes, strip
// heights and qualities.
func TestOptimizedPixels(t *testing.T) {
	sizes := [][2]int{{1, 1}, {8, 8}, {16, 16}, {17, 9}, {9, 17}, {33, 8}, {191, 143}, {64, 257}, {250, 40}}
	if testing.Short() {
		sizes = sizes[:6]
	}
	for _, sz := range sizes {
		for name, m := range testutil.Images(sz[0], sz[1]) {
			for _, q := range []int{1, 30, 75, 95, 100} {
				for _, strip := range []int{1, 7, 16, 500} {
					plain := encodeMode(t, m, strip, Options{Quality: q})
					// Progressive with Optimize set as well is the same
					// code path; the other tests cover it.
					for _, mode := range modes[:2] {
						o := mode.o
						o.Quality = q
						what := fmt.Sprintf("%s %s %dx%d q%d strip %d", mode.name, name, sz[0], sz[1], q, strip)
						data := encodeMode(t, m, strip, o)
						checkSamePixels(t, what, plain, data)
						if o.Progressive != hasMarker(data, sof2Marker) || o.Progressive == hasMarker(data, sof0Marker) {
							t.Fatalf("%s: wrong frame marker", what)
						}
					}
				}
			}
		}
	}
}

// special returns images that reach the corners of the progressive coder:
// long runs of empty blocks (EOB runs past 32767), blocks with many large
// coefficients and no small ones (correction bits filling the buffer), and
// plain noise.
func special() map[string]image.Image {
	out := map[string]image.Image{}
	flat := image.NewGray(image.Rect(0, 0, 2400, 1200))
	for i := range flat.Pix {
		flat.Pix[i] = 90
	}
	out["flat-gray"] = flat
	flatc := image.NewRGBA(image.Rect(0, 0, 2400, 1900))
	for i := range flatc.Pix {
		flatc.Pix[i] = 200
	}
	out["flat-colour"] = flatc

	r := rand.New(rand.NewSource(3))
	stripes := image.NewGray(image.Rect(0, 0, 1024, 512))
	for y := 0; y < 512; y++ {
		for x := 0; x < 1024; x++ {
			// Strong pattern inside the block, the same in every block.
			v := uint8(0)
			if (x/2+y)%2 == 0 {
				v = 255
			}
			stripes.Pix[y*stripes.Stride+x] = v
		}
	}
	out["pattern-gray"] = stripes
	pc := image.NewRGBA(image.Rect(0, 0, 1024, 512))
	for y := 0; y < 512; y++ {
		for x := 0; x < 1024; x++ {
			g := stripes.Pix[y*stripes.Stride+x]
			pc.SetRGBA(x, y, color.RGBA{g, 255 - g, uint8((x / 4 % 2) * 255), 255})
		}
	}
	out["pattern-colour"] = pc

	noise := image.NewRGBA(image.Rect(0, 0, 301, 203))
	for i := range noise.Pix {
		noise.Pix[i] = uint8(r.Intn(256))
	}
	out["noise"] = noise
	// Noise in a few places of an otherwise flat picture.
	spots := image.NewGray(image.Rect(0, 0, 900, 700))
	for i := 0; i < 400; i++ {
		x, y := r.Intn(900), r.Intn(700)
		spots.Pix[y*spots.Stride+x] = uint8(r.Intn(256))
	}
	out["spots"] = spots
	return out
}

func TestOptimizedSpecialImages(t *testing.T) {
	for name, m := range special() {
		for _, q := range []int{20, 90, 100} {
			plain := encodeMode(t, m, 37, Options{Quality: q})
			for _, mode := range modes {
				o := mode.o
				o.Quality = q
				data := encodeMode(t, m, 37, o)
				checkSamePixels(t, fmt.Sprintf("%s %s q%d", mode.name, name, q), plain, data)
				if len(data) >= len(plain) {
					t.Errorf("%s %s q%d: %d bytes, plain %d", mode.name, name, q, len(data), len(plain))
				}
			}
		}
	}
}

// TestOptimizedSmaller: on an ordinary picture each mode is smaller than
// the one before.
func TestOptimizedSmaller(t *testing.T) {
	for _, name := range []string{"rgba-opaque", "gray"} {
		m := testutil.Images(640, 480)[name]
		plain := encodeMode(t, m, 16, Options{Quality: 80})
		opt := encodeMode(t, m, 16, Options{Quality: 80, Optimize: true})
		prog := encodeMode(t, m, 16, Options{Quality: 80, Progressive: true})
		if !(len(prog) < len(opt) && len(opt) < len(plain)) {
			t.Errorf("%s: plain %d, optimize %d, progressive %d bytes", name, len(plain), len(opt), len(prog))
		}
	}
}

// TestOptimizedMeta: Exif and ICC are written where the plain file has them.
func TestOptimizedMeta(t *testing.T) {
	m := testutil.Images(70, 50)["rgba-opaque"]
	exif := []byte("II*\x00\x08\x00\x00\x00\x00\x00\x00\x00\x00\x00")
	icc := bytes.Repeat([]byte("profile!"), 9000) // two APP2 segments
	plain := encodeMode(t, m, 16, Options{Quality: 80, Exif: exif, ICC: icc})
	head := bytes.Index(plain, []byte{0xff, dqtMarker})
	if head < len(icc) {
		t.Fatal("metadata not before DQT in the plain file")
	}
	for _, mode := range modes {
		o := mode.o
		o.Quality, o.Exif, o.ICC = 80, exif, icc
		data := encodeMode(t, m, 16, o)
		if !bytes.HasPrefix(data, plain[:head+2]) {
			t.Errorf("%s: file does not start with the plain file's SOI and metadata", mode.name)
		}
		src, err := Open(bytes.NewReader(data), nil)
		if err != nil {
			t.Fatal(err)
		}
		meta := src.(interface{ Meta() pipeline.Meta }).Meta()
		if !bytes.Equal(meta.Exif, exif) || !bytes.Equal(meta.ICC, icc) {
			t.Errorf("%s: metadata does not read back", mode.name)
		}
		src.Close()
		checkSamePixels(t, mode.name, plain, data)
	}
}

// TestOptimalSpec checks the table rules for many frequency distributions:
// every used symbol has a code, none longer than 16 bits, the code of all
// ones stays free, and rarer symbols never get shorter codes.
func TestOptimalSpec(t *testing.T) {
	r := rand.New(rand.NewSource(11))
	check := func(what string, freq [257]int64) {
		orig := freq
		s := optimalSpec(&freq)
		n, kraft := 0, 0
		for i, c := range s.count {
			n += int(c)
			kraft += int(c) << (15 - i)
		}
		if n != len(s.value) {
			t.Fatalf("%s: %d codes for %d symbols", what, n, len(s.value))
		}
		if kraft >= 1<<16 {
			t.Fatalf("%s: codes use the whole code space (%d/65536), all-ones is not free", what, kraft)
		}
		size := map[byte]int{}
		k := 0
		for i, c := range s.count {
			for j := 0; j < int(c); j++ {
				if _, dup := size[s.value[k]]; dup {
					t.Fatalf("%s: symbol %d twice", what, s.value[k])
				}
				size[s.value[k]] = i + 1
				k++
			}
		}
		used := 0
		for sym, f := range orig[:256] {
			if f == 0 {
				continue
			}
			used++
			if size[byte(sym)] == 0 {
				t.Fatalf("%s: symbol %d has no code", what, sym)
			}
			for sym2, f2 := range orig[:256] {
				if f2 > f && size[byte(sym2)] > size[byte(sym)] {
					t.Fatalf("%s: symbol %d (%d times) has a longer code than %d (%d times)", what, sym2, f2, sym, f)
				}
			}
		}
		if used > 0 && len(size) != used {
			t.Fatalf("%s: %d codes for %d used symbols", what, len(size), used)
		}
		// The table must be one the decoder accepts and the encoder can use.
		var lut codeLUT
		lut.init(&s)
		var l huffmanLUT
		l.init(s)
	}
	check("empty", [257]int64{})
	var one [257]int64
	one[7] = 5
	check("one symbol", one)
	// Fibonacci counts give the deepest tree: lengths far above 16 (and
	// above libjpeg's limit of 32) before limiting.
	for _, n := range []int{20, 40, 60, 80} {
		var f [257]int64
		a, b := int64(1), int64(1)
		for i := 0; i < n; i++ {
			f[i*3] = a
			a, b = b, a+b
		}
		check(fmt.Sprintf("fibonacci %d", n), f)
	}
	for i := 0; i < 300; i++ {
		var f [257]int64
		for j := r.Intn(256) + 1; j > 0; j-- {
			f[r.Intn(256)] = int64(1) << uint(r.Intn(40)) >> uint(r.Intn(8))
		}
		check(fmt.Sprintf("random %d", i), f)
	}
	var all [257]int64
	for i := range all[:256] {
		all[i] = 1
	}
	check("all equal", all)
}

// dirWatch is a writer that notes whether a temporary file exists while the
// image data is being written.
type dirWatch struct {
	dir      string
	sawFiles bool
	n        int
}

func (d *dirWatch) Write(p []byte) (int, error) {
	if ents, _ := os.ReadDir(d.dir); len(ents) > 0 {
		d.sawFiles = true
	}
	d.n += len(p)
	return len(p), nil
}

// TestOptimizedSmallBudget: a 3 megapixel image under 2MB goes through
// temporary files, stays inside the budget and leaves nothing behind.
func TestOptimizedSmallBudget(t *testing.T) {
	const limit = 2 << 20
	r := rand.New(rand.NewSource(5))
	m := image.NewRGBA(image.Rect(0, 0, 2000, 1500))
	for y := 0; y < 1500; y++ {
		for x := 0; x < 2000; x++ {
			n := uint8(r.Intn(90))
			m.SetRGBA(x, y, color.RGBA{uint8(x/8) + n, uint8(y/6) + n, uint8((x+y)/14) ^ n, 255})
		}
	}
	g := image.NewGray(m.Rect)
	for i := range g.Pix {
		g.Pix[i] = m.Pix[4*i]
	}
	for name, m := range map[string]image.Image{"colour": m, "gray": g} {
		var plain bytes.Buffer
		if err := Encode(&plain, testutil.Chopped(m, 64), &Options{Quality: 90}); err != nil {
			t.Fatal(err)
		}
		for _, mode := range modes {
			dir := t.TempDir()
			env := pipeline.NewEnv(limit, dir)
			o := mode.o
			o.Quality = 90
			w := &dirWatch{dir: dir}
			var buf bytes.Buffer
			if err := Encode(io.MultiWriter(w, &buf), testutil.ChoppedEnv(m, 64, env), &o); err != nil {
				t.Fatalf("%s %s: %v", name, mode.name, err)
			}
			if !w.sawFiles {
				t.Errorf("%s %s: no temporary file was used", name, mode.name)
			}
			if p := env.Budget.Peak(); p > limit {
				t.Errorf("%s %s: peak %d over the limit %d", name, mode.name, p, limit)
			} else {
				t.Logf("%s %s: peak %d bytes, file %d (plain %d)", name, mode.name, p, buf.Len(), plain.Len())
			}
			if env.Budget.Used() != 0 {
				t.Errorf("%s %s: %d bytes still reserved", name, mode.name, env.Budget.Used())
			}
			if ents, _ := os.ReadDir(dir); len(ents) != 0 {
				t.Errorf("%s %s: %d temporary files left", name, mode.name, len(ents))
			}
			checkSamePixels(t, name+" "+mode.name, plain.Bytes(), buf.Bytes())
		}
	}
}

// failing is a source that fails, or just ends, after some strips.
type failing struct {
	pipeline.Source
	left int
	err  error
}

func (f *failing) Env() *pipeline.Env { return pipeline.EnvOf(f.Source) }
func (f *failing) Next() (image.Image, error) {
	if f.left == 0 {
		return nil, f.err
	}
	f.left--
	return f.Source.Next()
}

type failWriter struct {
	left int
}

var errWrite = errors.New("disk full")

func (f *failWriter) Write(p []byte) (int, error) {
	if f.left < len(p) {
		n := f.left
		f.left = 0
		return n, errWrite
	}
	f.left -= len(p)
	return len(p), nil
}

// TestOptimizedErrors: failures come back as errors, with the budget given
// back and no temporary file left, whether the data was staged in memory or
// on disk.
func TestOptimizedErrors(t *testing.T) {
	errSrc := errors.New("source broke")
	imgs := testutil.Images(400, 300)
	for _, name := range []string{"rgba-opaque", "gray"} {
		m := imgs[name]
		for _, limit := range []int64{0, 600 << 10} {
			for _, mode := range modes {
				o := mode.o
				o.Quality = 90
				run := func(what string, w io.Writer, wrap func(pipeline.Source) pipeline.Source, want error) {
					dir := t.TempDir()
					env := pipeline.NewEnv(limit, dir)
					err := Encode(w, wrap(testutil.ChoppedEnv(m, 10, env)), &o)
					what = fmt.Sprintf("%s %s limit %d %s", name, mode.name, limit, what)
					if !errors.Is(err, want) {
						t.Errorf("%s: error %v, want %v", what, err, want)
					}
					if env.Budget.Used() != 0 {
						t.Errorf("%s: %d bytes still reserved", what, env.Budget.Used())
					}
					if ents, _ := os.ReadDir(dir); len(ents) != 0 {
						t.Errorf("%s: %d temporary files left", what, len(ents))
					}
				}
				asIs := func(s pipeline.Source) pipeline.Source { return s }
				for _, after := range []int{0, 1, 13, 29} {
					run(fmt.Sprintf("source error after %d", after), io.Discard,
						func(s pipeline.Source) pipeline.Source { return &failing{s, after, errSrc} }, errSrc)
					run(fmt.Sprintf("source end after %d", after), io.Discard,
						func(s pipeline.Source) pipeline.Source { return &failing{s, after, io.EOF} }, io.ErrUnexpectedEOF)
				}
				var whole bytes.Buffer
				run("no failure", &whole, asIs, nil)
				for _, n := range []int{0, 1, 300, 700, 5000, whole.Len() / 2, whole.Len() - 1} {
					run(fmt.Sprintf("writer failing after %d", n), &failWriter{left: n}, asIs, errWrite)
				}
			}
		}
	}
	// A budget too small for even one row of coefficients.
	env := pipeline.NewEnv(64<<10, t.TempDir())
	big := image.NewRGBA(image.Rect(0, 0, 3000, 16))
	err := Encode(io.Discard, testutil.ChoppedEnv(big, 16, env), &Options{Progressive: true})
	if !errors.Is(err, pipeline.ErrMemoryLimit) || env.Budget.Used() != 0 {
		t.Errorf("tiny budget: error %v, %d bytes reserved", err, env.Budget.Used())
	}
}

// TestOptimizedLargestSides: 65535 pixels a side, the format's limit.
func TestOptimizedLargestSides(t *testing.T) {
	for _, sz := range [][2]int{{65535, 3}, {3, 65535}} {
		rect := image.Rect(0, 0, sz[0], sz[1])
		g := image.NewGray(rect)
		for i := range g.Pix {
			g.Pix[i] = uint8(i * 7 / 5)
		}
		c := image.NewYCbCr(rect, image.YCbCrSubsampleRatio420)
		copy(c.Y, g.Pix)
		for i := range c.Cb {
			c.Cb[i], c.Cr[i] = uint8(i), uint8(i/3)
		}
		for name, m := range map[string]image.Image{"gray": g, "colour": c} {
			plain := encodeMode(t, m, 5, Options{Quality: 60})
			for _, mode := range modes {
				o := mode.o
				o.Quality = 60
				checkSamePixels(t, fmt.Sprintf("%s %s %v", mode.name, name, sz), plain, encodeMode(t, m, 5, o))
			}
		}
	}
	if err := Encode(io.Discard, pipeline.FromImage(image.NewGray(image.Rect(0, 0, 65536, 1)), nil), &Options{Progressive: true}); err == nil {
		t.Error("65536 wide: no error")
	}
}

// TestLibjpegReadsThem decodes the files with libjpeg (through Python's
// PIL) and requires the same pixels as libjpeg gets from the plain file.
func TestLibjpegReadsThem(t *testing.T) {
	python, err := exec.LookPath("python")
	if err != nil {
		t.Skip("python not found")
	}
	if err := exec.Command(python, "-c", "import PIL.Image").Run(); err != nil {
		t.Skip("PIL not available")
	}
	dir := t.TempDir()
	var names []string
	add := func(name string, m image.Image, q int) {
		for _, mode := range append(modes[:2:2], struct {
			name string
			o    Options
		}{"plain", Options{}}) {
			o := mode.o
			o.Quality = q
			file := fmt.Sprintf("%s-q%d.%s.jpg", name, q, mode.name)
			if err := os.WriteFile(filepath.Join(dir, file), encodeMode(t, m, 16, o), 0o644); err != nil {
				t.Fatal(err)
			}
			if mode.name != "plain" {
				names = append(names, file)
			}
		}
	}
	for _, sz := range [][2]int{{1, 1}, {17, 9}, {191, 143}, {64, 257}} {
		imgs := testutil.Images(sz[0], sz[1])
		for _, name := range []string{"rgba-opaque", "gray", "ycbcr444", "nrgba64"} {
			for _, q := range []int{1, 30, 75, 95, 100} {
				add(fmt.Sprintf("%s-%dx%d", name, sz[0], sz[1]), imgs[name], q)
			}
		}
	}
	for name, m := range special() {
		add(name, m, 90)
	}
	const script = `
import sys, os, hashlib
from PIL import Image
d = sys.argv[1]
def load(p):
    im = Image.open(os.path.join(d, p))
    im.load()
    return im
for line in sys.stdin.read().split():
    try:
        im = load(line)
        kind = line.split('.')[-2]
        ref = load(line.replace('.' + kind + '.', '.plain.'))
        prog = im.info.get('progressive', 0)
        ok = im.mode == ref.mode and im.size == ref.size and im.tobytes() == ref.tobytes()
        print(line, 'same' if ok else 'DIFFERENT', 'progressive' if prog else 'sequential')
    except Exception as e:
        print(line, 'ERROR', repr(e).replace(' ', '_'))
`
	cmd := exec.Command(python, "-c", script, dir)
	cmd.Stdin = strings.NewReader(strings.Join(names, "\n"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("python: %v\n%s", err, out)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != len(names) {
		t.Fatalf("python reported %d files, want %d:\n%s", len(lines), len(names), out)
	}
	for _, line := range lines {
		f := strings.Fields(line)
		if len(f) != 3 || f[1] != "same" {
			t.Errorf("libjpeg: %s", line)
			continue
		}
		if want := map[bool]string{true: "progressive", false: "sequential"}[strings.Contains(f[0], ".progressive.")]; f[2] != want {
			t.Errorf("libjpeg: %s, want %s", line, want)
		}
	}
}
