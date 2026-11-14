package tests

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"golang.org/x/image/bmp"
	"golang.org/x/image/tiff"
	"golang.org/x/image/webp"

	. "github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/internal/testutil"
)

// realFiles returns one real file of every input format, from the test
// data of Go and golang.org/x/image.
func realFiles(t *testing.T) map[string][]byte {
	t.Helper()
	goDir := filepath.Join(runtime.GOROOT(), "src", "image", "testdata")
	xDir := testutil.XImageTestdata()
	if xDir == "" {
		t.Skip("golang.org/x/image testdata not found")
	}
	out := map[string][]byte{}
	for name, path := range map[string]string{
		"png":              filepath.Join(goDir, "video-001.png"),
		"jpeg":             filepath.Join(goDir, "video-001.jpeg"),
		"jpeg-progressive": filepath.Join(goDir, "video-001.progressive.jpeg"),
		"gif":              filepath.Join(goDir, "video-001.gif"),
		"bmp":              filepath.Join(xDir, "video-001.bmp"),
		"tiff":             filepath.Join(xDir, "video-001.tiff"),
		"webp-lossy":       filepath.Join(xDir, "blue-purple-pink.lossy.webp"),
		"webp-lossless":    filepath.Join(xDir, "blue-purple-pink.lossless.webp"),
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		out[name] = data
	}
	return out
}

// brokenAfter reads n bytes of data and then fails, like a connection that
// drops.
type brokenAfter struct {
	data []byte
	n    int
	err  error
}

func (b *brokenAfter) Read(p []byte) (int, error) {
	if b.n <= 0 {
		return 0, b.err
	}
	k := copy(p, b.data[:min(b.n, len(b.data))])
	b.data, b.n = b.data[k:], b.n-k
	if k == 0 {
		return 0, b.err
	}
	return k, nil
}

type failingWriter struct{ err error }

func (f failingWriter) Write(p []byte) (int, error) { return 0, f.err }

// TestErrorKinds: a bad image is an ErrFormat; a failure of anything else
// is not, and can be told by its own error.
func TestErrorKinds(t *testing.T) {
	files := realFiles(t)
	refs := map[string]func(io.Reader) (image.Image, error){
		"png": png.Decode, "jpeg": jpeg.Decode, "jpeg-progressive": jpeg.Decode, "gif": gif.Decode,
		"bmp": bmp.Decode, "tiff": tiff.Decode, "webp-lossy": webp.Decode, "webp-lossless": webp.Decode,
	}
	for name, data := range files {
		// The file as it is: fine, by every way in.
		if _, err := FromBytes(data).PNG().ToBytes(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		// Cut short, at several places: a bad image, in a run and when
		// only the size is asked for (where the header is still whole,
		// Metadata may rightly succeed).
		for _, cut := range []int{0, 3, 20, len(data) / 3, len(data) / 2, len(data) - 9} {
			short := data[:cut]
			for how, in := range map[string]func() *Image{
				"bytes":  func() *Image { return FromBytes(short) },
				"reader": func() *Image { return FromReader(struct{ io.Reader }{bytes.NewReader(short)}) },
			} {
				// It is an error exactly when the standard decoder of the
				// format says so (a TIFF, for one, can lose its last bytes
				// without losing a pixel), and then it is an ErrFormat.
				_, refErr := refs[name](bytes.NewReader(short))
				_, err := in().PNG().ToBytes()
				switch {
				case (err == nil) != (refErr == nil):
					t.Errorf("%s cut at %d (%s): err = %v, the standard decoder says %v", name, cut, how, err, refErr)
				case err != nil && !errors.Is(err, ErrFormat):
					t.Errorf("%s cut at %d (%s): not an ErrFormat: %v", name, cut, how, err)
				}
				if _, err := in().Metadata(); err != nil && !errors.Is(err, ErrFormat) {
					t.Errorf("%s cut at %d (%s): Metadata: not an ErrFormat: %v", name, cut, how, err)
				}
			}
		}
		// Damaged in the middle: an error where the standard decoder gives
		// one, and then an ErrFormat.
		for _, at := range []int{len(data) / 4, len(data) / 2, len(data) * 3 / 4} {
			bad := append([]byte(nil), data...)
			bad[at] ^= 0x5a
			bad[at+1] ^= 0xff
			_, refErr := refs[name](bytes.NewReader(bad))
			_, err := FromBytes(bad).PNG().ToBytes()
			switch {
			case (err == nil) != (refErr == nil):
				t.Errorf("%s damaged at %d: err = %v, the standard decoder says %v", name, at, err, refErr)
			case err != nil && !errors.Is(err, ErrFormat):
				t.Errorf("%s damaged at %d: not an ErrFormat: %v", name, at, err)
			}
		}
		// The connection drops: the error is the connection's, not the image's.
		dropped := errors.New("connection reset")
		for _, n := range []int{0, 10, len(data) / 2} {
			_, err := FromReader(&brokenAfter{data: data, n: n, err: dropped}).PNG().ToBytes()
			if !errors.Is(err, dropped) || errors.Is(err, ErrFormat) {
				t.Errorf("%s, reader failing after %d bytes: %v", name, n, err)
			}
		}
		// Limits and a failing destination are not the image's fault either.
		if _, err := FromBytes(data).LimitInputPixels(100).PNG().ToBytes(); !errors.Is(err, ErrPixelLimit) || errors.Is(err, ErrFormat) {
			t.Errorf("%s over the pixel limit: %v", name, err)
		}
		if _, err := FromBytes(data).LimitInputPixels(100).Metadata(); !errors.Is(err, ErrPixelLimit) {
			t.Errorf("%s over the pixel limit, Metadata: %v", name, err)
		}
		if _, err := FromBytes(data).MemoryLimit(2000).PNG().ToBytes(); !errors.Is(err, ErrMemoryLimit) || errors.Is(err, ErrFormat) {
			t.Errorf("%s under a 2000 byte memory limit: %v", name, err)
		}
		full := errors.New("disk full")
		if err := FromBytes(data).PNG().ToWriter(failingWriter{full}); !errors.Is(err, full) || errors.Is(err, ErrFormat) {
			t.Errorf("%s to a failing writer: %v", name, err)
		}
	}
	// Not images at all.
	for _, data := range [][]byte{nil, []byte("x"), []byte("hello, this is a text file, not a picture\n"), bytes.Repeat([]byte{0}, 500)} {
		if _, err := FromBytes(data).PNG().ToBytes(); !errors.Is(err, ErrFormat) {
			t.Errorf("%q: %v", data, err)
		}
		if _, err := FromBytes(data).Metadata(); !errors.Is(err, ErrFormat) {
			t.Errorf("%q: Metadata: %v", data, err)
		}
	}
	// A file that is not there is the file system's error.
	_, err := Open(filepath.Join(t.TempDir(), "missing.png")).PNG().ToBytes()
	if !errors.Is(err, fs.ErrNotExist) || errors.Is(err, ErrFormat) {
		t.Errorf("missing file: %v", err)
	}
	// Working storage that cannot be created (a progressive JPEG keeps its
	// coefficients there) is not the image's fault.
	_, err = FromBytes(files["jpeg-progressive"]).MemoryLimit(200 << 10).TempDir(filepath.Join(t.TempDir(), "gone")).PNG().ToBytes()
	if err == nil || errors.Is(err, ErrFormat) {
		t.Errorf("temporary directory missing: %v", err)
	}
	// An animation cut short, processed frame by frame.
	var anim bytes.Buffer
	if err := gif.EncodeAll(&anim, testAnimation(64, 48)); err != nil {
		t.Fatal(err)
	}
	if _, err := FromBytes(anim.Bytes()[:anim.Len()*2/3]).Animated().ToBytes(); !errors.Is(err, ErrFormat) {
		t.Errorf("animation cut short: %v", err)
	}
	// A bad watermark is reported as such.
	if _, err := FromBytes(files["png"]).Composite(FromBytes([]byte("not an image"))).PNG().ToBytes(); !errors.Is(err, ErrFormat) {
		t.Errorf("bad overlay: %v", err)
	}
	// Bad arguments are neither.
	if _, err := FromBytes(files["png"]).Extract(0, 0, 9000, 9000).PNG().ToBytes(); err == nil || errors.Is(err, ErrFormat) {
		t.Errorf("crop outside the image: %v", err)
	}
}

// TestClone: recipes branched with Clone are independent, and each is what
// the same chain written out in full gives.
func TestClone(t *testing.T) {
	in := pngBytes(t, testutil.Images(613, 457)["rgba-opaque"])
	base := FromBytes(in).Rotate(90)
	thumb := base.Clone().Resize(100, 0).JPEG(70)
	large := base.Clone().Resize(400, 0).WebP()
	plain := base.Clone()
	for _, c := range []struct {
		name      string
		got, want *Image
	}{
		{"thumb", thumb, FromBytes(in).Rotate(90).Resize(100, 0).JPEG(70)},
		{"large", large, FromBytes(in).Rotate(90).Resize(400, 0).WebP()},
		{"plain", plain.PNG(), FromBytes(in).Rotate(90).PNG()},
		{"base", base.PNG(), FromBytes(in).Rotate(90).PNG()},
	} {
		a, err := c.got.ToBytes()
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		b, _ := c.want.ToBytes()
		if !bytes.Equal(a, b) {
			t.Errorf("%s: the clone differs from the chain written out", c.name)
		}
	}
	// Without Clone the two are one recipe: this is what Clone is for.
	shared := FromBytes(in)
	x, y := shared.Resize(100, 0), shared.Resize(400, 0)
	if x != y {
		t.Error("methods are documented to return the Image they are called on")
	}
	// An error recorded in a clone stays there.
	bad := base.Clone().Rotate(45)
	if _, err := bad.PNG().ToBytes(); err == nil {
		t.Error("Rotate(45) in a clone: no error")
	}
	if _, err := base.Clone().PNG().ToBytes(); err != nil {
		t.Errorf("the base after a failed clone: %v", err)
	}
}

// cancelAtEnd cancels when the whole input has been handed out.
type cancelAtEnd struct {
	r      *bytes.Reader
	cancel context.CancelFunc
}

func (c *cancelAtEnd) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if c.r.Len() == 0 {
		c.cancel()
	}
	return n, err
}

// cancelOnWrite cancels at the first write and counts what arrives.
type cancelOnWrite struct {
	cancel context.CancelFunc
	n      int
}

func (c *cancelOnWrite) Write(p []byte) (int, error) {
	c.cancel()
	c.n += len(p)
	return len(p), nil
}

// TestContext: a cancelled context ends the run with its error, while the
// image is being read and while it is being written, and leaves no file.
func TestContext(t *testing.T) {
	in := pngBytes(t, testutil.Images(1200, 900)["rgba-opaque"])
	dir := t.TempDir()
	chains := map[string]func(*Image) *Image{
		"plain":       func(m *Image) *Image { return m.JPEG(80) },
		"resize":      func(m *Image) *Image { return m.Resize(300, 0).PNG() },
		"rotate":      func(m *Image) *Image { return m.Rotate(90).JPEG(80) },
		"size limit":  func(m *Image) *Image { return m.JPEG(80).MaxFileSize(40 << 10) },
		"png colours": func(m *Image) *Image { return m.PNG(PNGOptions{Colors: 64}) },
		"webp":        func(m *Image) *Image { return m.WebP() },
		"threads":     func(m *Image) *Image { return m.Threads(3).Resize(300, 0).JPEG(80) },
	}
	for name, chain := range chains {
		// Without a cancellation the context changes nothing.
		want, err := chain(FromBytes(in)).ToBytes()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got, err := chain(FromBytes(in).WithContext(context.Background())).ToBytes()
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s with a live context: err %v, same bytes %v", name, err, bytes.Equal(got, want))
		}
		// Cancelled before the start.
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		out := filepath.Join(dir, "out.jpg")
		if err := chain(FromBytes(in).WithContext(ctx)).ToFile(out); !errors.Is(err, context.Canceled) {
			t.Errorf("%s, cancelled before the start: %v", name, err)
		}
		// Cancelled when the last of the input has been read: the work that
		// remains is not done.
		ctx, cancel = context.WithCancel(context.Background())
		err = chain(FromReader(&cancelAtEnd{bytes.NewReader(in), cancel}).WithContext(ctx)).ToFile(out)
		if !errors.Is(err, context.Canceled) || errors.Is(err, ErrFormat) {
			t.Errorf("%s, cancelled at the end of the input: %v", name, err)
		}
		cancel()
		if names, _ := filepath.Glob(filepath.Join(dir, "*")); len(names) != 0 {
			t.Fatalf("%s: files left after a cancelled run: %v", name, names)
		}
	}
	// Cancelled while writing, after everything has been read: a half turn
	// has the whole image in store before the first row goes out, so only
	// the encoder is still at work.
	full, _ := FromBytes(in).Rotate(180).JPEG(90).ToBytes()
	ctx, cancel := context.WithCancel(context.Background())
	w := &cancelOnWrite{cancel: cancel}
	err := FromBytes(in).Rotate(180).JPEG(90).WithContext(ctx).ToWriter(w)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled while writing: %v", err)
	}
	if w.n >= len(full)/2 {
		t.Errorf("cancelled at the first write, yet %d of %d bytes were written", w.n, len(full))
	}
	// A deadline that has passed.
	ctx, cancel = context.WithTimeout(context.Background(), -1)
	defer cancel()
	if _, err := FromBytes(in).WithContext(ctx).JPEG(80).ToBytes(); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("deadline passed: %v", err)
	}
	// An animation, frame by frame.
	var anim bytes.Buffer
	gif.EncodeAll(&anim, testAnimation(64, 48))
	ctx, cancel = context.WithCancel(context.Background())
	_, err = FromReader(&cancelAtEnd{bytes.NewReader(anim.Bytes()), cancel}).WithContext(ctx).Animated().GIF().ToBytes()
	if !errors.Is(err, context.Canceled) {
		t.Errorf("animation, cancelled at the end of the input: %v", err)
	}
	cancel()
}

// TestMisuse: wrong arguments are errors when the recipe is run, never a
// crash.
func TestMisuse(t *testing.T) {
	in := pngBytes(t, testutil.Images(60, 40)["nrgba"])
	logo := FromBytes(in)
	self := FromBytes(in)
	var zero Image
	for name, m := range map[string]*Image{
		"Composite(nil)":     FromBytes(in).Composite(nil),
		"Composite(itself)":  self.Composite(self),
		"Flatten(nil)":       FromBytes(in).Flatten(nil),
		"zero Image":         &zero,
		"two Resize options": FromBytes(in).Resize(10, 10, ResizeOptions{}, ResizeOptions{}),
		"two Composite opts": FromBytes(in).Composite(logo, CompositeOptions{}, CompositeOptions{}),
		"two JPEG options":   FromBytes(in).JPEG(80, JPEGOptions{}, JPEGOptions{}),
		"two PNG options":    FromBytes(in).PNG(PNGOptions{}, PNGOptions{}),
		"two WebP options":   FromBytes(in).WebP(WebPOptions{}, WebPOptions{}),
		"two GIF options":    FromBytes(in).GIF(GIFOptions{}, GIFOptions{}),
		"two TIFF options":   FromBytes(in).TIFF(TIFFOptions{}, TIFFOptions{}),
		"two Text options":   Text("x", TextOptions{}, TextOptions{}),
		"two angles":         FromBytes(in).Rotate(90, 180),
	} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%s: panic: %v", name, r)
				}
			}()
			if _, err := m.ToBytes(); err == nil {
				t.Errorf("%s: no error", name)
			} else if errors.Is(err, ErrFormat) {
				t.Errorf("%s: reported as a bad image: %v", name, err)
			}
			if _, err := m.Metadata(); err == nil {
				t.Errorf("%s: Metadata: no error", name)
			}
			if _, _, err := m.OutputSize(); err == nil {
				t.Errorf("%s: OutputSize: no error", name)
			}
		}()
	}
	// One options value, or none, is fine.
	if _, err := FromBytes(in).Resize(10, 10, ResizeOptions{}).Composite(logo).Flatten(color.White).JPEG(80, JPEGOptions{}).ToBytes(); err != nil {
		t.Error(err)
	}
}

// TestSizeLeavesTheOverlayAlone: asking what the input is, or how large the
// result will be, does not open the watermark, so a watermark that can be
// read only once is still there for the run, and its faults do not matter
// to either question.
func TestSizeLeavesTheOverlayAlone(t *testing.T) {
	in := pngBytes(t, testutil.Images(200, 150)["rgba-opaque"])
	logo := pngBytes(t, testutil.Images(30, 20)["nrgba"])
	m := FromBytes(in).Resize(100, 0).Composite(FromReader(bytes.NewReader(logo)), CompositeOptions{Gravity: SouthEast})
	for i := 0; i < 3; i++ {
		w, h, err := m.OutputSize()
		if err != nil || w != 100 || h != 75 {
			t.Fatalf("OutputSize: %dx%d, %v", w, h, err)
		}
		info, err := m.Metadata()
		if err != nil || info.Width != 200 || info.Height != 150 {
			t.Fatalf("Metadata: %+v, %v", info, err)
		}
	}
	got, err := m.PNG().ToBytes()
	if err != nil {
		t.Fatalf("the run after Metadata: %v", err)
	}
	want, _ := FromBytes(in).Resize(100, 0).Composite(FromBytes(logo), CompositeOptions{Gravity: SouthEast}).PNG().ToBytes()
	if !bytes.Equal(got, want) {
		t.Error("the result differs from the same chain with the overlay given as bytes")
	}
	// The run itself still reports a missing watermark.
	missing := FromBytes(in).Composite(Open(filepath.Join(t.TempDir(), "no-logo.png")))
	if _, err := missing.Metadata(); err != nil {
		t.Errorf("Metadata with a missing overlay: %v", err)
	}
	if _, _, err := missing.OutputSize(); err != nil {
		t.Errorf("OutputSize with a missing overlay: %v", err)
	}
	if _, err := missing.PNG().ToBytes(); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("run with a missing overlay: %v", err)
	}
}

// TestConcurrentRuns runs one recipe from several goroutines at once, as a
// server does; under the race detector this is what finds shared state.
func TestConcurrentRuns(t *testing.T) {
	in := pngBytes(t, testutil.Images(300, 220)["rgba-opaque"])
	logo := pngBytes(t, testutil.Images(30, 20)["nrgba"])
	recipes := map[string]*Image{
		"size limit":  FromBytes(in).Rotate().Resize(200, 0).Composite(FromBytes(logo), CompositeOptions{Gravity: SouthEast}).JPEG(80).MaxFileSize(5000),
		"png colours": FromBytes(in).Resize(200, 0).PNG(PNGOptions{Colors: 32}),
		"webp":        FromBytes(in).Rotate(90).WebP(WebPOptions{Quality: 60}),
		"text mark": FromBytes(in).Composite(Text("mark", TextOptions{Size: 20, Color: color.White, StrokeWidth: 1}),
			CompositeOptions{Tile: true, Angle: -30, Opacity: 0.4}).JPEG(80, JPEGOptions{Progressive: true}),
		"threads": FromBytes(in).Threads(3).Resize(150, 0).GIF(),
	}
	for name, m := range recipes {
		want, err := m.ToBytes()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var wg sync.WaitGroup
		for i := 0; i < 6; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				got, err := m.ToBytes()
				if err != nil || !bytes.Equal(got, want) {
					t.Errorf("%s: concurrent run: err %v, same bytes %v", name, err, bytes.Equal(got, want))
				}
				if _, err := m.Metadata(); err != nil {
					t.Errorf("%s: concurrent Metadata: %v", name, err)
				}
				if _, _, err := m.OutputSize(); err != nil {
					t.Errorf("%s: concurrent OutputSize: %v", name, err)
				}
				if _, err := m.Clone().Resize(50, 0).ToBytes(); err != nil {
					t.Errorf("%s: concurrent clone: %v", name, err)
				}
			}()
		}
		wg.Wait()
	}
}
