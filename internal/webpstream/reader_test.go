package webpstream

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"testing"
	"time"

	"golang.org/x/image/webp"

	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// testFiles returns every *.webp of the golang.org/x/image testdata.
func testFiles(t testing.TB) map[string][]byte {
	dir := testutil.XImageTestdata()
	if dir == "" {
		t.Skip("golang.org/x/image testdata not found")
	}
	names, err := filepath.Glob(filepath.Join(dir, "*.webp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(names) < 17 {
		t.Fatalf("found %d webp files, want at least 17", len(names))
	}
	files := map[string][]byte{}
	for _, name := range names {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		files[filepath.Base(name)] = data
	}
	return files
}

// reference decodes with golang.org/x/image/webp. panicked reports that the
// reference decoder itself panicked.
func reference(data []byte) (m image.Image, err error, panicked bool) {
	defer func() {
		if r := recover(); r != nil {
			m, err, panicked = nil, fmt.Errorf("panic: %v", r), true
		}
	}()
	m, err = webp.Decode(bytes.NewReader(data))
	return m, err, false
}

// stream decodes with this package and checks the Source bookkeeping.
func stream(t testing.TB, data []byte, env *pipeline.Env) (m image.Image, err error) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("panic: %v\n%s", r, debug.Stack())
			m, err = nil, fmt.Errorf("panic: %v", r)
		}
	}()
	src, err := Open(bytes.NewReader(data), int64(len(data)), env)
	if err != nil {
		return nil, err
	}
	m, err = pipeline.Collect(src)
	if cerr := src.Close(); cerr != nil {
		t.Errorf("Close: %v", cerr)
	}
	if cerr := src.Close(); cerr != nil {
		t.Errorf("second Close: %v", cerr)
	}
	if used := env.Budget.Used(); used != 0 {
		t.Errorf("budget still has %d bytes reserved after Close", used)
	}
	return m, err
}

func ycbcrDiff(got, want *image.YCbCr) string {
	b := want.Rect
	if got.SubsampleRatio != want.SubsampleRatio {
		return fmt.Sprintf("subsample ratio %v, want %v", got.SubsampleRatio, want.SubsampleRatio)
	}
	w := b.Dx()
	cw := (b.Max.X+1)/2 - b.Min.X/2
	for y := b.Min.Y; y < b.Max.Y; y++ {
		if !bytes.Equal(got.Y[got.YOffset(b.Min.X, y):][:w], want.Y[want.YOffset(b.Min.X, y):][:w]) {
			return fmt.Sprintf("Y differs in row %d", y)
		}
		if !bytes.Equal(got.Cb[got.COffset(b.Min.X, y):][:cw], want.Cb[want.COffset(b.Min.X, y):][:cw]) {
			return fmt.Sprintf("Cb differs in row %d", y)
		}
		if !bytes.Equal(got.Cr[got.COffset(b.Min.X, y):][:cw], want.Cr[want.COffset(b.Min.X, y):][:cw]) {
			return fmt.Sprintf("Cr differs in row %d", y)
		}
	}
	return ""
}

// diff describes the first difference between got and want, or returns "".
func diff(got, want image.Image) string {
	if got.Bounds() != want.Bounds() {
		return fmt.Sprintf("bounds %v, want %v", got.Bounds(), want.Bounds())
	}
	b := want.Bounds()
	switch want := want.(type) {
	case *image.YCbCr:
		got, ok := got.(*image.YCbCr)
		if !ok {
			return fmt.Sprintf("type %T, want %T", got, want)
		}
		return ycbcrDiff(got, want)
	case *image.NYCbCrA:
		got, ok := got.(*image.NYCbCrA)
		if !ok {
			return fmt.Sprintf("type %T, want %T", got, want)
		}
		if d := ycbcrDiff(&got.YCbCr, &want.YCbCr); d != "" {
			return d
		}
		for y := b.Min.Y; y < b.Max.Y; y++ {
			if !bytes.Equal(got.A[got.AOffset(b.Min.X, y):][:b.Dx()], want.A[want.AOffset(b.Min.X, y):][:b.Dx()]) {
				return fmt.Sprintf("A differs in row %d", y)
			}
		}
		return ""
	case *image.NRGBA:
		got, ok := got.(*image.NRGBA)
		if !ok {
			return fmt.Sprintf("type %T, want %T", got, want)
		}
		for y := b.Min.Y; y < b.Max.Y; y++ {
			if !bytes.Equal(got.Pix[got.PixOffset(b.Min.X, y):][:4*b.Dx()], want.Pix[want.PixOffset(b.Min.X, y):][:4*b.Dx()]) {
				return fmt.Sprintf("pixels differ in row %d", y)
			}
		}
		return ""
	}
	return fmt.Sprintf("unexpected reference type %T", want)
}

// setBandRows forces the strip height for the duration of a test.
func setBandRows(t testing.TB, n int) {
	old := testBandRows
	testBandRows = n
	t.Cleanup(func() { testBandRows = old })
}

// check decodes data both ways and compares: an error where the reference
// has one, the same pixels where it has none.
func check(t *testing.T, what string, data []byte) {
	t.Helper()
	want, werr, panicked := reference(data)
	got, gerr := stream(t, data, pipeline.NewEnv(0, t.TempDir()))
	if panicked {
		return // The reference is no guide; not panicking ourselves is enough.
	}
	if werr != nil {
		if gerr == nil {
			t.Errorf("%s: decoded, but golang.org/x/image/webp fails with: %v", what, werr)
		}
		return
	}
	if want.Bounds().Empty() {
		// Documented difference: a lossy frame of zero width or height is an
		// error here and an empty image there.
		if gerr == nil {
			t.Errorf("%s: decoded an image that has no pixels", what)
		}
		return
	}
	if gerr != nil {
		t.Errorf("%s: %v, but golang.org/x/image/webp decodes it", what, gerr)
		return
	}
	if d := diff(got, want); d != "" {
		t.Errorf("%s: %s", what, d)
	}
}

func TestXImageFiles(t *testing.T) {
	for name, data := range testFiles(t) {
		for _, rows := range []int{0, 1, 7, 16, 48} {
			t.Run(fmt.Sprintf("%s/rows=%d", name, rows), func(t *testing.T) {
				setBandRows(t, rows)
				check(t, name, data)

				src, err := Open(bytes.NewReader(data), int64(len(data)), pipeline.NewEnv(0, t.TempDir()))
				if err != nil {
					t.Fatal(err)
				}
				defer src.Close()
				cfg, err := webp.DecodeConfig(bytes.NewReader(data))
				if err != nil {
					t.Fatal(err)
				}
				// The color model is that of the decoded image. (For a VP8X
				// file with the alpha flag and a VP8L chunk, DecodeConfig says
				// NYCbCrA, going by the VP8X header alone, but Decode returns
				// an *image.NRGBA.)
				ref, _, _ := reference(data)
				if src.Bounds() != image.Rect(0, 0, cfg.Width, cfg.Height) || src.ColorModel() != ref.ColorModel() {
					t.Errorf("header says %v %v, want %dx%d %v", src.Bounds(), src.ColorModel(), cfg.Width, cfg.Height, ref.ColorModel())
				}
				_, lossy := ref.(*image.YCbCr)
				if pipeline.IsOpaque(src) != lossy {
					t.Errorf("Opaque() = %v, want %v", pipeline.IsOpaque(src), lossy)
				}
				// Strips must be contiguous, full width, and no taller than asked.
				y := 0
				for {
					m, err := src.Next()
					if err != nil {
						break
					}
					b := m.Bounds()
					if b.Min.Y != y || b.Min.X != 0 || b.Max.X != cfg.Width || b.Empty() {
						t.Fatalf("strip %v after row %d", b, y)
					}
					y = b.Max.Y
				}
				if y != cfg.Height {
					t.Errorf("strips end at row %d, want %d", y, cfg.Height)
				}
			})
		}
	}
}

// TestTruncated cuts every file short at many offsets.
func TestTruncated(t *testing.T) {
	for name, data := range testFiles(t) {
		t.Run(name, func(t *testing.T) {
			setBandRows(t, 16)
			offsets := map[int]bool{}
			for i := 0; i < 64 && i < len(data); i++ {
				offsets[i] = true
			}
			for i := 0; i < 60; i++ {
				offsets[len(data)*i/60] = true
			}
			for i := 1; i <= 12 && i < len(data); i++ {
				offsets[len(data)-i] = true
			}
			for off := range offsets {
				check(t, fmt.Sprintf("%s cut at %d of %d", name, off, len(data)), data[:off])
			}
		})
	}
}

// TestCorrupt changes single bytes.
func TestCorrupt(t *testing.T) {
	n := 150
	if testing.Short() {
		n = 30
	}
	for name, data := range testFiles(t) {
		t.Run(name, func(t *testing.T) {
			setBandRows(t, 16)
			rnd := rand.New(rand.NewSource(int64(len(data))))
			buf := make([]byte, len(data))
			for i := 0; i < n; i++ {
				// A third of the changes go to the headers at the front.
				off := rnd.Intn(len(data))
				if i%3 == 0 {
					off = rnd.Intn(min(len(data), 160))
				}
				copy(buf, data)
				buf[off] ^= byte(1 + rnd.Intn(255))
				check(t, fmt.Sprintf("%s with byte %d changed to %#02x", name, off, buf[off]), buf)
			}
		})
	}
}

// chunk builds a RIFF chunk.
func chunk(id string, data []byte) []byte {
	b := append([]byte(id), 0, 0, 0, 0)
	binary.LittleEndian.PutUint32(b[4:], uint32(len(data)))
	b = append(b, data...)
	if len(data)%2 == 1 {
		b = append(b, 0)
	}
	return b
}

// TestAnimated checks that an animated file fails as it does in
// golang.org/x/image/webp.
func TestAnimated(t *testing.T) {
	files := testFiles(t)
	frame := files["blue-purple-pink.lossy.webp"][12:] // The "VP8 " chunk.
	vp8x := make([]byte, 10)
	vp8x[0] = 1 << 1 // The animation flag.
	vp8x[4], vp8x[7] = 151, 199
	anmf := append(make([]byte, 16), frame...)
	body := append([]byte("WEBP"), chunk("VP8X", vp8x)...)
	body = append(body, chunk("ANIM", make([]byte, 6))...)
	body = append(body, chunk("ANMF", anmf)...)
	data := chunk("RIFF", body)

	_, werr := webp.Decode(bytes.NewReader(data))
	if werr == nil {
		t.Fatal("golang.org/x/image/webp decodes the animated file")
	}
	_, gerr := Open(bytes.NewReader(data), int64(len(data)), pipeline.NewEnv(0, t.TempDir()))
	if gerr == nil || gerr.Error() != werr.Error() {
		t.Errorf("Open: %v, want %v", gerr, werr)
	}
}

// chunks splits a WebP file into its chunks: ids and data.
func chunks(t testing.TB, data []byte) (ids []string, bodies [][]byte) {
	for pos := 12; pos+8 <= len(data); {
		n := int(binary.LittleEndian.Uint32(data[pos+4:]))
		if pos+8+n > len(data) {
			t.Fatal("bad chunk")
		}
		ids = append(ids, string(data[pos:pos+4]))
		bodies = append(bodies, data[pos+8:pos+8+n])
		pos += 8 + n + n&1
	}
	return ids, bodies
}

// TestAlphaVariants rewrites the alpha chunk of the lossy-with-alpha file:
// every filter with the original VP8L-compressed data, and every filter with
// uncompressed data. The alpha values are arbitrary; only agreement with
// golang.org/x/image/webp matters.
func TestAlphaVariants(t *testing.T) {
	data := testFiles(t)["yellow_rose.lossy-with-alpha.webp"]
	ids, bodies := chunks(t, data)
	cfg, err := webp.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	build := func(alph []byte) []byte {
		body := []byte("WEBP")
		for i, id := range ids {
			if id == "ALPH" {
				body = append(body, chunk(id, alph)...)
			} else {
				body = append(body, chunk(id, bodies[i])...)
			}
		}
		return chunk("RIFF", body)
	}
	var orig []byte
	for i, id := range ids {
		if id == "ALPH" {
			orig = bodies[i]
		}
	}
	if orig == nil {
		t.Fatal("no ALPH chunk")
	}
	t.Logf("original ALPH flags %#02x, chunks %v", orig[0], ids)
	rnd := rand.New(rand.NewSource(1))
	raw := make([]byte, 1+cfg.Width*cfg.Height)
	for i := range raw {
		raw[i] = byte(rnd.Intn(7)) // Small residuals, so filters do not just wrap around.
	}
	for _, rows := range []int{0, 16, 48} {
		setBandRows(t, rows)
		for filter := byte(0); filter < 4; filter++ {
			alph := append([]byte(nil), orig...)
			alph[0] = alph[0]&^0x0c | filter<<2
			check(t, fmt.Sprintf("compressed alpha, filter %d, rows %d", filter, rows), build(alph))

			raw[0] = filter << 2
			file := build(raw)
			check(t, fmt.Sprintf("raw alpha, filter %d, rows %d", filter, rows), file)
			// One byte short, and extra bytes after the plane.
			check(t, fmt.Sprintf("short raw alpha, filter %d", filter), build(raw[:len(raw)-1]))
			check(t, fmt.Sprintf("long raw alpha, filter %d", filter), build(append(raw[:len(raw):len(raw)], 1, 2, 3)))
			// Make sure the valid variants really decode.
			if _, err := webp.Decode(bytes.NewReader(file)); err != nil {
				t.Fatalf("reference fails on raw alpha: %v", err)
			}
		}
	}
}

func tempFiles(t testing.TB, dir string) []string {
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

// TestMemory decodes the largest test images under a small budget.
func TestMemory(t *testing.T) {
	files := testFiles(t)
	for _, name := range []string{
		"blue-purple-pink-large.normal-filter.lossy.webp",
		"blue-purple-pink-large.lossless.webp",
		"yellow_rose.lossy-with-alpha.webp",
		"yellow_rose.lossless.webp",
	} {
		t.Run(name, func(t *testing.T) {
			data := files[name]
			want, err := webp.Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			const limit = 4 << 20
			env := pipeline.NewEnv(limit, dir)
			got, err := stream(t, data, env)
			if err != nil {
				t.Fatal(err)
			}
			if d := diff(got, want); d != "" {
				t.Error(d)
			}
			peak := env.Budget.Peak()
			t.Logf("%v: budget peak %d bytes", want.Bounds().Size(), peak)
			if peak <= 0 || peak > limit {
				t.Errorf("budget peak %d, want within (0, %d]", peak, limit)
			}
			if names := tempFiles(t, dir); len(names) != 0 {
				t.Errorf("temporary files left behind: %v", names)
			}
		})
	}
}

// TestLossyMemoryIsBounded checks that the lossy decoder's memory does not
// grow with the image height: it is far below the size of the frame.
func TestLossyMemoryIsBounded(t *testing.T) {
	data := testFiles(t)["blue-purple-pink-large.normal-filter.lossy.webp"]
	setBandRows(t, 16)
	env := pipeline.NewEnv(0, t.TempDir())
	m, err := stream(t, data, env)
	if err != nil {
		t.Fatal(err)
	}
	b := m.Bounds()
	// Two macroblock rows of 4:2:0 samples, plus line buffers and the
	// partition read windows, whatever the height.
	mbw := int64(b.Dx()+15) / 16
	if peak, most := env.Budget.Peak(), 2*384*mbw+64*mbw+(80<<10); peak > most {
		t.Errorf("budget peak %d bytes, want at most %d", peak, most)
	}
}

// TestSpill decodes the lossless files with a budget that has no room for
// optional in-memory stores, so the sub-images go to temporary files.
func TestSpill(t *testing.T) {
	for name, data := range testFiles(t) {
		t.Run(name, func(t *testing.T) {
			want, err := webp.Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			env := pipeline.NewEnv(64<<20, dir)
			// TryReserve keeps 1/8 of the limit free; with more than 7/8 in
			// use, only required buffers are granted.
			const held = 57 << 20
			if err := env.Budget.Reserve(held, "test"); err != nil {
				t.Fatal(err)
			}
			src, err := Open(bytes.NewReader(data), int64(len(data)), env)
			if err != nil {
				t.Fatal(err)
			}
			got, err := pipeline.Collect(src)
			if err != nil {
				t.Fatal(err)
			}
			if d := diff(got, want); d != "" {
				t.Error(d)
			}
			if err := src.Close(); err != nil {
				t.Error(err)
			}
			if used := env.Budget.Used(); used != held {
				t.Errorf("budget has %d bytes reserved after Close, want %d", used, held)
			}
			if names := tempFiles(t, dir); len(names) != 0 {
				t.Errorf("temporary files left behind: %v", names)
			}
		})
	}
}

// TestCloseEarly closes a source that is partly read, and one never read.
func TestCloseEarly(t *testing.T) {
	for name, data := range testFiles(t) {
		setBandRows(t, 16)
		dir := t.TempDir()
		env := pipeline.NewEnv(64<<20, dir)
		if err := env.Budget.Reserve(57<<20, "test"); err != nil {
			t.Fatal(err)
		}
		for _, strips := range []int{0, 1, 2} {
			src, err := Open(bytes.NewReader(data), int64(len(data)), env)
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < strips; i++ {
				src.Next()
			}
			if err := src.Close(); err != nil {
				t.Errorf("%s: %v", name, err)
			}
			if _, err := src.Next(); err == nil {
				t.Errorf("%s: Next after Close returned a strip", name)
			}
			if used := env.Budget.Used(); used != 57<<20 {
				t.Errorf("%s: %d bytes reserved after Close", name, used-57<<20)
			}
			if names := tempFiles(t, dir); len(names) != 0 {
				t.Errorf("%s: temporary files left behind: %v", name, names)
			}
		}
	}
}

// TestBigFiles measures real large files. It is skipped unless
// WEBPSTREAM_BIG_DIR names a directory with lossy.webp and lossless.webp.
func TestBigFiles(t *testing.T) {
	dir := os.Getenv("WEBPSTREAM_BIG_DIR")
	if dir == "" {
		t.Skip("WEBPSTREAM_BIG_DIR is not set")
	}
	for _, name := range []string{"lossy.webp", "lossless.webp"} {
		t.Run(name, func(t *testing.T) {
			f, err := os.Open(filepath.Join(dir, name))
			if err != nil {
				t.Skip(err)
			}
			defer f.Close()
			st, err := f.Stat()
			if err != nil {
				t.Fatal(err)
			}

			// Pass 1: time and memory, discarding the strips.
			var before, after runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&before)
			env := pipeline.NewEnv(16<<20, t.TempDir())
			start := time.Now()
			src, err := Open(f, st.Size(), env)
			if err != nil {
				t.Fatal(err)
			}
			strips := 0
			for {
				if _, err := src.Next(); err != nil {
					break
				}
				strips++
			}
			elapsed := time.Since(start)
			runtime.ReadMemStats(&after)
			src.Close()
			t.Logf("Go heap: %.2f MB allocated in total, %.2f MB in use at the end",
				float64(after.TotalAlloc-before.TotalAlloc)/(1<<20), float64(after.HeapInuse)/(1<<20))
			t.Logf("webpstream: %v, %d strips, %v, budget peak %.2f MB",
				src.Bounds().Size(), strips, elapsed.Round(time.Millisecond), float64(env.Budget.Peak())/(1<<20))

			// Pass 2: the reference, and a pixel comparison.
			data, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			start = time.Now()
			want, err := webp.Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("x/image/webp: %v", time.Since(start).Round(time.Millisecond))
			env = pipeline.NewEnv(16<<20, t.TempDir())
			src, err = Open(f, st.Size(), env)
			if err != nil {
				t.Fatal(err)
			}
			defer src.Close()
			for {
				m, err := src.Next()
				if err != nil {
					break
				}
				b := m.Bounds()
				sub := want.(interface {
					SubImage(image.Rectangle) image.Image
				}).SubImage(b)
				if d := diff(m, sub); d != "" {
					t.Fatalf("strip %v: %s", b, d)
				}
			}
		})
	}
}
