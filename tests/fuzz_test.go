package tests

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	. "github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// The fuzz targets hold the library to what a program that processes files
// it does not control needs from it, whatever the bytes are: no panic, no
// hang, strips that cover the image exactly, every reserved byte given
// back and no temporary file left, on success and on every error.
//
//	go test -run '^$' -fuzz FuzzDecode -fuzztime 5m .

// seedFiles are small real files of every format and variety: the test
// images of Go and of golang.org/x/image, and files this package wrote.
func seedFiles(tb testing.TB) [][]byte {
	var out [][]byte
	dirs := []string{filepath.Join(runtime.GOROOT(), "src", "image", "testdata")}
	if x := testutil.XImageTestdata(); x != "" {
		dirs = append(dirs, x)
	}
	for _, dir := range dirs {
		names, _ := filepath.Glob(filepath.Join(dir, "*"))
		for _, name := range names {
			switch strings.ToLower(filepath.Ext(name)) {
			case ".png", ".jpeg", ".jpg", ".gif", ".bmp", ".tiff", ".tif", ".webp":
			default:
				continue
			}
			if st, err := os.Stat(name); err != nil || st.Size() > 40<<10 {
				continue
			}
			if data, err := os.ReadFile(name); err == nil {
				out = append(out, data)
			}
		}
	}
	for _, size := range []image.Point{{1, 1}, {17, 13}} {
		for name, m := range testutil.Images(size.X, size.Y) {
			if name != "nrgba" && name != "gray" && name != "paletted" && name != "ycbcr420" && name != "nrgba64" {
				continue
			}
			for _, choose := range outputs {
				if data, err := choose(FromImage(m)).KeepExif().ToBytes(); err == nil {
					out = append(out, data)
				}
			}
		}
	}
	// With an EXIF orientation, and an animation.
	jp := withAPP(jpegBytes(testutil.Images(24, 16)["rgba-opaque"], 80), app(0xe1, append([]byte("Exif\x00\x00"), exifWithOrientation(binary.LittleEndian, 6)...)))
	out = append(out, jp)
	var anim bytes.Buffer
	if err := gif.EncodeAll(&anim, testAnimation(20, 14)); err == nil {
		out = append(out, anim.Bytes())
	}
	return out
}

// fuzzFail reports an error that is a bug rather than a refusal.
func fuzzFail(t *testing.T, what string, err error) {
	t.Helper()
	var pe *pipeline.PanicError
	if errors.As(err, &pe) {
		t.Fatalf("%s: panic: %v\n%s", what, pe.Value, pe.Stack)
	}
}

// clean checks that a run gave everything back.
func clean(t *testing.T, env *pipeline.Env, dir string) {
	t.Helper()
	if used := env.Budget.Used(); used != 0 {
		t.Fatalf("%d bytes still reserved after the run", used)
	}
	if peak := env.Budget.Peak(); peak > env.Budget.Limit() {
		t.Fatalf("%d bytes reserved at the peak, the limit is %d", peak, env.Budget.Limit())
	}
	if names, _ := os.ReadDir(dir); len(names) != 0 {
		t.Fatalf("%d temporary files left", len(names))
	}
}

// FuzzDecode reads arbitrary bytes as an image, as every decoder does.
func FuzzDecode(f *testing.F) {
	for _, s := range seedFiles(f) {
		f.Add(s)
	}
	dir := f.TempDir()
	f.Fuzz(func(t *testing.T, data []byte) {
		m := FromBytes(data).LimitInputPixels(1 << 20)
		info, err := m.Metadata()
		fuzzFail(t, "Metadata", err)
		env := pipeline.NewEnv(8<<20, dir)
		src, serr := m.Stream(env)
		fuzzFail(t, "Stream", serr)
		if (err == nil) != (serr == nil) && !errors.Is(serr, ErrMemoryLimit) {
			t.Fatalf("Metadata says %v, Stream %v", err, serr)
		}
		if serr != nil {
			clean(t, env, dir)
			return
		}
		b := src.Bounds()
		if b.Dx() != info.Width || b.Dy() != info.Height || b.Empty() {
			t.Fatalf("bounds %v, Metadata %dx%d", b, info.Width, info.Height)
		}
		y, typ := b.Min.Y, ""
		for {
			s, err := src.Next()
			if err == io.EOF {
				if y != b.Max.Y {
					t.Fatalf("the image ended at row %d of %v", y, b)
				}
				break
			}
			if err != nil {
				fuzzFail(t, "Next", err)
				break
			}
			sb := s.Bounds()
			if sb.Min.Y != y || sb.Min.X != b.Min.X || sb.Max.X != b.Max.X || sb.Dy() < 1 || sb.Max.Y > b.Max.Y {
				t.Fatalf("strip %v after row %d of %v", sb, y, b)
			}
			y = sb.Max.Y
			if now := typeName(s); typ != "" && now != typ {
				t.Fatalf("strip type changed from %s to %s", typ, now)
			} else {
				typ = now
			}
			// Every corner of the strip is backed by its buffer.
			s.At(sb.Min.X, sb.Min.Y)
			s.At(sb.Max.X-1, sb.Max.Y-1)
		}
		if err := src.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		clean(t, env, dir)
	})
}

func typeName(m image.Image) string {
	switch m.(type) {
	case *image.Gray:
		return "Gray"
	case *image.Gray16:
		return "Gray16"
	case *image.RGBA:
		return "RGBA"
	case *image.RGBA64:
		return "RGBA64"
	case *image.NRGBA:
		return "NRGBA"
	case *image.NRGBA64:
		return "NRGBA64"
	case *image.Paletted:
		return "Paletted"
	case *image.YCbCr:
		return "YCbCr"
	case *image.NYCbCrA:
		return "NYCbCrA"
	case *image.CMYK:
		return "CMYK"
	}
	return "other"
}

// FuzzChain puts arbitrary bytes through an arbitrary chain into an
// arbitrary format.
func FuzzChain(f *testing.F) {
	seeds := seedFiles(f)
	for i, s := range seeds {
		f.Add(s, uint16(i*37), uint16(i*91), uint32(i)*2654435761)
	}
	dir := f.TempDir()
	logo := FromImage(testutil.Images(9, 5)["nrgba"])
	f.Fuzz(func(t *testing.T, data []byte, w, h uint16, flags uint32) {
		bit := func(n uint) bool { return flags>>n&1 != 0 }
		pick := func(n, of uint) int { return int(flags >> n % uint32(of)) }
		// Small images: the paths taken do not depend on the size, and
		// reducing a quarter of a million pixels of noise to a palette
		// takes a second, which is time not spent on other inputs.
		m := FromBytes(data).LimitInputPixels(1 << 15).LimitOutputPixels(1 << 15).MemoryLimit(8 << 20).TempDir(dir).
			Threads(1 + pick(28, 3))
		if bit(0) {
			m.Rotate()
		}
		if bit(1) {
			m.Rotate(90 * pick(2, 4))
		}
		if bit(4) {
			m.Flip()
		}
		if bit(5) {
			m.Resize(int(w%400), int(h%400), ResizeOptions{Fit: Fit(pick(6, 5)), Kernel: Kernel(pick(9, 6)),
				FastShrinkOnLoad: bit(12), WithoutEnlargement: bit(13)})
		}
		if bit(14) {
			m.Extract(int(w%7), int(h%5), 1+int(w%50), 1+int(h%50))
		}
		if bit(15) {
			m.Flatten(color.NRGBA{uint8(w), uint8(h), uint8(flags), 255})
		}
		if bit(16) {
			m.Composite(logo, CompositeOptions{Gravity: Position(pick(17, 10)), Left: int(int16(w)), Top: int(int16(h)),
				Angle: float64(int16(w)), Tile: bit(21), Opacity: float64(h%101) / 100})
		}
		if bit(22) {
			m.KeepExif()
		}
		switch pick(23, 12) {
		case 0:
			m.JPEG(1 + int(w%100))
		case 1:
			m.JPEG(1+int(w%100), JPEGOptions{Progressive: true})
		case 2:
			m.JPEG(1+int(w%100), JPEGOptions{Optimize: true})
		case 3:
			m.PNG()
		case 4:
			m.PNG(PNGOptions{Colors: 2 + int(h%255), NoDither: bit(27)})
		case 5:
			m.GIF(GIFOptions{Colors: 1 + int(h%256), NoDither: bit(27)})
		case 6:
			m.Animated().GIF()
		case 7:
			m.BMP()
		case 8:
			m.TIFF(TIFFOptions{Compression: TIFFCompression(pick(27, 2))})
		case 9:
			m.WebP(WebPOptions{Quality: 1 + int(w%100)})
		case 10:
			m.WebP(WebPOptions{Lossless: true, Effort: 1 + int(h%9)})
		case 11:
			m.JPEG(80).MaxFileSize(200 + int64(w)*4)
		}
		// Within the pixel limits above no input has more than a moment's
		// work in it; one that takes this long has found a way around them.
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		// And when even the cancellation is not heard: end the process,
		// which makes the fuzzer keep the input.
		watchdog := time.AfterFunc(60*time.Second, func() { panic("no answer to the cancellation after 40 seconds") })
		defer watchdog.Stop()
		var out bytes.Buffer
		err := m.WithContext(ctx).ToWriter(&out)
		fuzzFail(t, "run", err)
		if errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("still running after 20 seconds")
		}
		if names, _ := os.ReadDir(dir); len(names) != 0 {
			t.Fatalf("%d temporary files left (%v)", len(names), err)
		}
		if err != nil {
			return
		}
		// What was written is an image this package reads again.
		if _, err := FromBytes(out.Bytes()).Metadata(); err != nil {
			t.Fatalf("the output cannot be read back: %v", err)
		}
	})
}

// FuzzText draws arbitrary text.
func FuzzText(f *testing.F) {
	for _, s := range []string{"", "Hello", "你好，世界", "a\nb\n\nc", "\t\x00\xff", strings.Repeat("long ", 50), "🙂 ﷽ ░ ‍́é"} {
		f.Add(s, uint16(24), uint16(0), uint8(0))
	}
	dir := f.TempDir()
	f.Fuzz(func(t *testing.T, text string, size, width uint16, flags uint8) {
		o := TextOptions{Size: float64(size%300) / 2, Width: int(width % 600), Align: TextAlign(flags % 3),
			Padding: int(flags>>2) % 9, LineSpacing: float64(flags>>4) / 4}
		if flags&4 != 0 {
			o.StrokeWidth = float64(size%7) / 2
		}
		if flags&8 != 0 {
			o.ShadowColor, o.ShadowX, o.ShadowBlur = color.Black, int(int8(width)), int(size%9)
		}
		env := pipeline.NewEnv(32<<20, dir)
		// (A long text in large letters is a large image, and large
		// images are slow; that is not what is looked for here.)
		src, err := Text(text, o).LimitInputPixels(1 << 20).Stream(env)
		fuzzFail(t, "Stream", err)
		if err == nil {
			_, err = pipeline.Collect(src)
			fuzzFail(t, "draw", err)
			src.Close()
		}
		clean(t, env, dir)
	})
}
