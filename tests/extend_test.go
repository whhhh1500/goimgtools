package tests

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	. "github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// ---- an operation written against the public packages only ----

// border puts a frame of n pixels in one colour around the image: an
// operation that changes the size and makes strips of its own.
type border struct {
	n int
	c color.NRGBA
}

func (b border) Apply(src pipeline.Source) (pipeline.Source, error) {
	if b.n < 0 {
		return nil, errors.New("border: negative width")
	}
	// Map gives one known pixel type to work on.
	in := pipeline.Map(src, func(*image.NRGBA) error { return nil })
	r := in.Bounds()
	return &bordered{Source: in, n: b.n, c: b.c,
		rect: image.Rect(r.Min.X-b.n, r.Min.Y-b.n, r.Max.X+b.n, r.Max.Y+b.n)}, nil
}

type bordered struct {
	pipeline.Source // Env comes from here
	n               int
	c               color.NRGBA
	rect            image.Rectangle
	buf             []uint8
	reserved        int64
	y               int // rows handed out
	inner           bool
	closed          int
}

func (b *bordered) Bounds() image.Rectangle { return b.rect }
func (b *bordered) ColorModel() color.Model { return color.NRGBAModel }

func (b *bordered) Close() error {
	b.closed++
	b.Env().Budget.Release(b.reserved)
	b.reserved = 0
	return b.Source.Close()
}

// strip returns a strip of rows [y0, y1) filled with the border colour.
func (b *bordered) strip(y0, y1 int) (*image.NRGBA, error) {
	w := b.rect.Dx()
	need := int64(w) * 4 * int64(y1-y0)
	if int64(len(b.buf)) < need {
		b.Env().Budget.Release(b.reserved)
		b.reserved = 0
		if err := b.Env().Budget.Reserve(need, "border strip"); err != nil {
			return nil, err
		}
		b.reserved, b.buf = need, make([]uint8, need)
	}
	out := &image.NRGBA{Pix: b.buf[:need], Stride: w * 4, Rect: image.Rect(b.rect.Min.X, y0, b.rect.Max.X, y1)}
	for i := 0; i < len(out.Pix); i += 4 {
		out.Pix[i], out.Pix[i+1], out.Pix[i+2], out.Pix[i+3] = b.c.R, b.c.G, b.c.B, b.c.A
	}
	return out, nil
}

func (b *bordered) Next() (image.Image, error) {
	top := b.rect.Min.Y
	switch {
	case b.n > 0 && b.y == 0:
		b.y = b.n
		return b.strip(top, top+b.n)
	case b.y >= b.rect.Dy():
		return nil, io.EOF
	case b.y >= b.rect.Dy()-b.n:
		// The source must have reached its end before the last strip goes out.
		if _, err := b.Source.Next(); err != io.EOF {
			if err == nil {
				err = errors.New("border: source has rows left")
			}
			return nil, err
		}
		y0 := top + b.y
		b.y = b.rect.Dy()
		return b.strip(y0, b.rect.Max.Y)
	}
	s, err := b.Source.Next()
	if err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return nil, err
	}
	in := s.(*image.NRGBA)
	out, err := b.strip(in.Rect.Min.Y, in.Rect.Max.Y)
	if err != nil {
		return nil, err
	}
	for y := in.Rect.Min.Y; y < in.Rect.Max.Y; y++ {
		copy(out.Pix[out.PixOffset(in.Rect.Min.X, y):], in.Pix[in.PixOffset(in.Rect.Min.X, y):][:in.Rect.Dx()*4])
	}
	b.y += in.Rect.Dy()
	return out, nil
}

// TestPipe: an operation of the caller's own sits in the chain like one of
// the package's: it gets what came before, what comes after gets its
// result, the size it makes is the size reported, and its memory counts.
func TestPipe(t *testing.T) {
	src := testutil.Images(413, 290)["rgba-opaque"]
	in := pngBytes(t, src)
	red := color.NRGBA{200, 10, 20, 255}

	m := FromBytes(in).Resize(200, 0).Pipe(border{7, red}).Crop(0, 0, 214, 100)
	w, h, err := FromBytes(in).Resize(200, 0).Pipe(border{7, red}).OutputSize()
	if err != nil || w != 214 || h != 140+14 {
		t.Fatalf("size with a border: %dx%d, %v; want 214x154", w, h, err)
	}
	got, err := m.PNG().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	plain, err := FromBytes(in).Resize(200, 0).PNG().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	want := image.NewNRGBA(image.Rect(0, 0, 214, 100))
	inner := decodePNG(t, plain)
	for y := 0; y < 100; y++ {
		for x := 0; x < 214; x++ {
			if x < 7 || x >= 207 || y < 7 {
				want.SetNRGBA(x, y, red)
			} else {
				want.Set(x, y, inner.At(x-7, y-7))
			}
		}
	}
	sameImage(t, "border then extract", decodePNG(t, got), want)

	// The same through every thread setting, and repeatedly: an operation
	// holds nothing between runs.
	for threads := 1; threads <= 3; threads++ {
		again, err := m.Clone().Threads(threads).PNG().ToBytes()
		if err != nil || !bytes.Equal(again, got) {
			t.Errorf("threads %d: differs from the first run (%v)", threads, err)
		}
	}

	// What an operation does wrong comes back as the error of the run.
	if _, err := FromBytes(in).Pipe(border{n: -1}).ToBytes(); err == nil || !strings.Contains(err.Error(), "negative width") {
		t.Errorf("an operation's own error: %v", err)
	}
	if _, err := FromBytes(in).Pipe(OperationFunc(func(pipeline.Source) (pipeline.Source, error) { return nil, nil })).ToBytes(); err == nil {
		t.Error("an operation that returns nothing was accepted")
	}
	if _, err := FromBytes(in).Pipe(nil).ToBytes(); err == nil {
		t.Error("Pipe(nil) accepted")
	}
	// Its strips count against the limit: a border strip of the full width
	// times 3000 rows does not fit in 1MB.
	_, err = FromBytes(in).MemoryLimit(1 << 20).Pipe(border{3000, red}).ToBytes()
	if !errors.Is(err, ErrMemoryLimit) {
		t.Errorf("a stage that needs more than the limit: %v", err)
	}
}

// closeCounter is an operation that passes everything through and counts
// how its stage is used.
type closeCounter struct{ made, closed, rows *atomic.Int64 }

type counted struct {
	pipeline.Source
	c closeCounter
}

func (c closeCounter) Apply(src pipeline.Source) (pipeline.Source, error) {
	c.made.Add(1)
	return counted{src, c}, nil
}

func (c counted) Next() (image.Image, error) {
	s, err := c.Source.Next()
	if err == nil {
		c.c.rows.Add(int64(s.Bounds().Dy()))
	}
	return s, err
}

func (c counted) Close() error { c.c.closed.Add(1); return c.Source.Close() }

// TestPipeLifecycle: a stage is made once per run and closed exactly once,
// whether the run succeeds, fails early or fails in the middle; asking for
// the size makes it but reads no rows.
func TestPipeLifecycle(t *testing.T) {
	in := jpegBytes(testutil.Images(640, 480)["rgba-opaque"], 90)
	var made, closed, rows atomic.Int64
	c := closeCounter{&made, &closed, &rows}
	check := func(what string, wantMade, wantRows int64) {
		t.Helper()
		if made.Load() != wantMade || closed.Load() != wantMade {
			t.Errorf("%s: stage made %d times, closed %d times, want %d each", what, made.Load(), closed.Load(), wantMade)
		}
		if wantRows >= 0 && rows.Load() != wantRows {
			t.Errorf("%s: %d rows went through, want %d", what, rows.Load(), wantRows)
		}
		made.Store(0)
		closed.Store(0)
		rows.Store(0)
	}
	if _, err := FromBytes(in).Pipe(c).JPEG(JPEGOptions{Quality: 80}).ToBytes(); err != nil {
		t.Fatal(err)
	}
	check("a run", 1, 480)
	if _, _, err := FromBytes(in).Pipe(c).Resize(100, 0).OutputSize(); err != nil {
		t.Fatal(err)
	}
	check("OutputSize", 1, 0)
	// A later operation fails when the chain is built.
	if _, err := FromBytes(in).Pipe(c).Crop(0, 0, 5000, 5000).ToBytes(); err == nil {
		t.Fatal("extract outside the image accepted")
	}
	check("a chain that cannot be built", 1, 0)
	// The input breaks off half way.
	if _, err := FromBytes(in[:len(in)/2]).Pipe(c).PNG().ToBytes(); !errors.Is(err, ErrFormat) {
		t.Fatalf("truncated input: %v", err)
	}
	check("a truncated input", 1, -1)
	// The writer fails.
	if err := FromBytes(in).Pipe(c).PNG().ToWriter(failingWriter{errors.New("disk full")}); err == nil {
		t.Fatal("failing writer accepted")
	}
	check("a failing writer", 1, -1)
}

// TestPixels: the function sees every pixel exactly once as 8-bit NRGBA,
// whatever the image is stored as, and what it writes is what comes out.
func TestPixels(t *testing.T) {
	invert := func(strip *image.NRGBA) error {
		r := strip.Rect
		for y := r.Min.Y; y < r.Max.Y; y++ {
			row := strip.Pix[strip.PixOffset(r.Min.X, y):][:r.Dx()*4]
			for i := 0; i < len(row); i += 4 {
				row[i], row[i+1], row[i+2] = 255-row[i], 255-row[i+1], 255-row[i+2]
			}
		}
		return nil
	}
	want := func(m image.Image) image.Image {
		b := m.Bounds()
		out := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
		for y := 0; y < b.Dy(); y++ {
			for x := 0; x < b.Dx(); x++ {
				n := color.NRGBAModel.Convert(m.At(b.Min.X+x, b.Min.Y+y)).(color.NRGBA)
				if c, ok := m.At(b.Min.X+x, b.Min.Y+y).(color.NRGBA64); ok {
					// 16 bits that are not multiplied by alpha: the high bytes.
					n = color.NRGBA{uint8(c.R >> 8), uint8(c.G >> 8), uint8(c.B >> 8), uint8(c.A >> 8)}
				}
				out.SetNRGBA(x, y, color.NRGBA{255 - n.R, 255 - n.G, 255 - n.B, n.A})
			}
		}
		return out
	}
	// 2731 wide: strips of 11 rows, so vertically subsampled images are
	// cut on odd rows as well.
	for _, size := range []image.Point{{97, 61}, {2731, 37}} {
		imgs := testutil.Images(size.X, size.Y)
		cmyk := image.NewCMYK(image.Rect(0, 0, size.X, size.Y))
		for i := range cmyk.Pix {
			cmyk.Pix[i] = uint8(i * 7)
		}
		imgs["cmyk"] = cmyk
		names := make([]string, 0, len(imgs))
		for name := range imgs {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			m := imgs[name]
			got, err := FromImage(m).Pixels(invert).PNG().ToBytes()
			if err != nil {
				t.Fatalf("%s %v: %v", name, size, err)
			}
			sameImage(t, fmt.Sprintf("%s %v inverted", name, size), decodePNG(t, got), want(m))
		}
	}
	// From files too: a JPEG, and a PNG with a palette.
	photo := testutil.Images(333, 222)["rgba-opaque"]
	jp := jpegBytes(photo, 85)
	dec, _ := decodeJPEG(bytes.NewReader(jp))
	got, err := FromBytes(jp).Pixels(invert).PNG().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	sameImage(t, "jpeg inverted", decodePNG(t, got), want(dec))

	// The function's error ends the run.
	boom := errors.New("boom")
	_, err = FromBytes(jp).Pixels(func(*image.NRGBA) error { return boom }).ToBytes()
	if !errors.Is(err, boom) {
		t.Errorf("error of the function: %v", err)
	}
	if _, err := FromBytes(jp).Pixels(nil).ToBytes(); err == nil {
		t.Error("Pixels(nil) accepted")
	}
	// Twice inverted is the picture again, and an opaque picture stays one
	// for what follows (a JPEG can be written without Flatten either way).
	twice, err := FromBytes(jp).Pixels(invert).Pixels(invert).PNG().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	same8(t, "inverted twice", decodePNG(t, twice), dec)
	if _, ok := decodePNG(t, twice).(*image.RGBA); !ok {
		t.Errorf("an opaque image came out of Pixels as %T, not as a PNG without alpha", decodePNG(t, twice))
	}
}

// TestApply: a named group of steps is the steps.
func TestApply(t *testing.T) {
	in := pngBytes(t, testutil.Images(300, 200)["nrgba"])
	thumb := func(size int) func(*Image) *Image {
		return func(m *Image) *Image { return m.Resize(size, size, ResizeOptions{Fit: FitCover}).Flatten(color.White) }
	}
	a, err := FromBytes(in).Apply(thumb(64), func(m *Image) *Image { return m.Flip() }).PNG().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	b, err := FromBytes(in).Resize(64, 64, ResizeOptions{Fit: FitCover}).Flatten(color.White).Flip().PNG().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Error("Apply gives another file than the steps written out")
	}
	if _, err := FromBytes(in).Apply(nil).ToBytes(); err == nil {
		t.Error("Apply(nil) accepted")
	}
	// A step that returns nothing leaves the recipe as it changed it.
	c, err := FromBytes(in).Apply(func(m *Image) *Image {
		m.Resize(64, 64, ResizeOptions{Fit: FitCover}).Flatten(color.White).Flip()
		return nil
	}).PNG().ToBytes()
	if err != nil || !bytes.Equal(c, b) {
		t.Errorf("a step without a result: %v", err)
	}
}

// photo is a type of the caller's own with the methods of Image and more.
type photo struct {
	*Image
	credit string
}

func openPhoto(data []byte, credit string) photo { return photo{FromBytes(data).AutoOrient(), credit} }

func (p photo) Web() photo {
	p.Resize(160, 0).Composite(Text(p.credit, TextOptions{Size: 12, Color: color.White}),
		CompositeOptions{Gravity: SouthEast, Left: -4, Top: -4})
	return p
}

// TestEmbedding: a type that embeds *Image has every method of Image, and
// what its own methods add to the recipe stays in it.
func TestEmbedding(t *testing.T) {
	in := jpegBytes(testutil.Images(320, 240)["rgba-opaque"], 90)
	p := openPhoto(in, "© test")
	p.Flatten(color.White) // a method of Image, called on the new type
	got, err := p.Web().JPEG(JPEGOptions{Quality: 80}).ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	want, err := FromBytes(in).AutoOrient().Flatten(color.White).Resize(160, 0).
		Composite(Text("© test", TextOptions{Size: 12, Color: color.White}), CompositeOptions{Gravity: SouthEast, Left: -4, Top: -4}).
		JPEG(JPEGOptions{Quality: 80}).ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Error("the embedding type gives another file than the same steps on an Image")
	}
}

// gradient is an input that is computed, not read.
type gradient struct {
	w, h, y int
	env     *pipeline.Env
	buf     *image.Gray
}

func (g *gradient) Bounds() image.Rectangle { return image.Rect(0, 0, g.w, g.h) }
func (g *gradient) ColorModel() color.Model { return color.GrayModel }
func (g *gradient) Env() *pipeline.Env      { return g.env }
func (g *gradient) Opaque() bool            { return true }
func (g *gradient) Close() error            { return nil }

func (g *gradient) Next() (image.Image, error) {
	if g.y >= g.h {
		return nil, io.EOF
	}
	y1 := min(g.y+16, g.h)
	if g.buf == nil {
		g.buf = image.NewGray(image.Rect(0, 0, g.w, 16))
	}
	out := g.buf.SubImage(image.Rect(0, 0, g.w, y1-g.y)).(*image.Gray)
	out = &image.Gray{Pix: out.Pix, Stride: out.Stride, Rect: image.Rect(0, g.y, g.w, y1)}
	for y := g.y; y < y1; y++ {
		for x := 0; x < g.w; x++ {
			out.Pix[(y-g.y)*out.Stride+x] = uint8((x + y) & 255)
		}
	}
	g.y = y1
	return out, nil
}

// TestFromSource: an input of the caller's own is an input like a file:
// read strip by strip, as often as the recipe is run.
func TestFromSource(t *testing.T) {
	opens := 0
	m := FromSource(func(env *pipeline.Env) (pipeline.Source, error) {
		opens++
		return &gradient{w: 300, h: 200, env: env}, nil
	})
	info, err := m.Metadata()
	if err != nil || info.Format != "source" || info.Width != 300 || info.Height != 200 || info.HasAlpha {
		t.Fatalf("Metadata: %+v, %v", info, err)
	}
	for i := 0; i < 2; i++ {
		got, err := m.Clone().Resize(150, 0).ToBytes() // PNG, as for every input that is not a file
		if err != nil {
			t.Fatal(err)
		}
		img := decodePNG(t, got)
		if img.Bounds().Size() != image.Pt(150, 100) {
			t.Fatalf("size %v", img.Bounds().Size())
		}
		if g := color.GrayModel.Convert(img.At(75, 50)).(color.Gray).Y; g < 245 && g > 10 {
			if d := int(g) - 250; d < -6 || d > 6 { // (150+100)&255 = 250
				t.Errorf("middle pixel %d, want about 250", g)
			}
		}
	}
	if opens != 3 {
		t.Errorf("the source was opened %d times for one Metadata and two runs", opens)
	}
	// Its errors are passed on as they are: not "a bad image".
	boom := errors.New("no data today")
	_, err = FromSource(func(*pipeline.Env) (pipeline.Source, error) { return nil, boom }).ToBytes()
	if !errors.Is(err, boom) || errors.Is(err, ErrFormat) {
		t.Errorf("error of the open function: %v", err)
	}
	if _, err := FromSource(func(*pipeline.Env) (pipeline.Source, error) { return nil, nil }).ToBytes(); err == nil {
		t.Error("a source function that returns nothing was accepted")
	}
	if _, err := FromSource(nil).ToBytes(); err == nil {
		t.Error("FromSource(nil) accepted")
	}
	if _, err := FromImage(nil).ToBytes(); err == nil {
		t.Error("FromImage(nil) accepted")
	}
}

// TestFromImage: an image in memory of any of the standard types goes
// through unchanged, also as a watermark.
func TestFromImage(t *testing.T) {
	for _, size := range []image.Point{{1, 1}, {61, 47}, {2731, 23}} {
		for name, m := range testutil.Images(size.X, size.Y) {
			got, err := FromImage(m).PNG().ToBytes()
			if err != nil {
				t.Fatalf("%s %v: %v", name, size, err)
			}
			if strings.HasPrefix(name, "ycbcr") || name == "rgba" {
				// Colours computed to 16 bits, or multiplied by alpha:
				// the PNG holds them rounded to 8 bits.
				same8(t, fmt.Sprintf("%s %v", name, size), decodePNG(t, got), m)
			} else {
				sameImage(t, fmt.Sprintf("%s %v", name, size), decodePNG(t, got), m)
			}
			info, err := FromImage(m).Metadata()
			if err != nil || info.Format != "image" || info.Width != size.X || info.Height != size.Y {
				t.Errorf("%s: Metadata %+v, %v", name, info, err)
			}
		}
	}
	// A sub-image keeps its place: the result starts at its corner.
	big := testutil.Images(200, 150)["nrgba"].(*image.NRGBA)
	sub := big.SubImage(image.Rect(40, 30, 140, 90))
	got, err := FromImage(sub).PNG().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	sameImage(t, "sub-image", decodePNG(t, got), sub)
	// As an overlay.
	base := pngBytes(t, testutil.Images(120, 80)["rgba-opaque"])
	logo := testutil.Images(30, 20)["nrgba"]
	a, err := FromBytes(base).Composite(FromImage(logo), CompositeOptions{Left: 5, Top: 6}).PNG().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	b, err := FromBytes(base).Composite(FromBytes(pngBytes(t, logo)), CompositeOptions{Left: 5, Top: 6}).PNG().ToBytes()
	if err != nil || !bytes.Equal(a, b) {
		t.Errorf("an overlay from memory differs from the same overlay from a file (%v)", err)
	}
}

// same8 is sameImage at 8 bits a channel, for references that compute
// their colours to 16 bits (YCbCr).
func same8(t *testing.T, what string, got, want image.Image) {
	t.Helper()
	if got.Bounds().Size() != want.Bounds().Size() {
		t.Fatalf("%s: size %v, want %v", what, got.Bounds().Size(), want.Bounds().Size())
	}
	gb, wb := got.Bounds(), want.Bounds()
	for y := 0; y < wb.Dy(); y++ {
		for x := 0; x < wb.Dx(); x++ {
			g := color.NRGBAModel.Convert(got.At(gb.Min.X+x, gb.Min.Y+y))
			w := color.NRGBAModel.Convert(want.At(wb.Min.X+x, wb.Min.Y+y))
			if g != w {
				t.Fatalf("%s: pixel (%d,%d) = %v, want %v", what, x, y, g, w)
			}
		}
	}
}

// TestStream: the result of a recipe as a stream is the pixels the file
// would have held; closing it closes the input.
func TestStream(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "in.jpg")
	exif := exifWithOrientation(binary.LittleEndian, 6)
	jp := withAPP(jpegBytes(testutil.Images(400, 300)["rgba-opaque"], 90), app(0xe1, append([]byte("Exif\x00\x00"), exif...)))
	if err := os.WriteFile(path, jp, 0o644); err != nil {
		t.Fatal(err)
	}
	m := Open(path).AutoOrient().Resize(120, 0).KeepExif()
	want, err := m.Clone().PNG().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	src, err := m.Stream(nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := pipeline.ExifOrientation(pipeline.MetaOf(src).Exif); len(pipeline.MetaOf(src).Exif) == 0 || got != 1 {
		t.Errorf("EXIF of the stream: %d bytes, orientation %d; want the block, upright", len(pipeline.MetaOf(src).Exif), got)
	}
	img, err := pipeline.Collect(src)
	if err != nil {
		t.Fatal(err)
	}
	same8(t, "stream", img, decodePNG(t, want))
	if err := src.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Errorf("the input is still open after Close: %v", err)
	}

	// ToImage is the same pixels in one piece, within the memory limit.
	in := pngBytes(t, testutil.Images(500, 400)["rgba-opaque"])
	whole, err := FromBytes(in).Resize(250, 0).ToImage()
	if err != nil {
		t.Fatal(err)
	}
	ref, _ := FromBytes(in).Resize(250, 0).PNG().ToBytes()
	sameImage(t, "ToImage", whole, decodePNG(t, ref))
	if _, err := FromBytes(in).MemoryLimit(400 << 10).ToImage(); !errors.Is(err, ErrMemoryLimit) {
		t.Errorf("an 800KB image within 400KB: %v", err)
	}

	// The operations of the package on a stream the caller has: a recipe
	// whose input is that stream, inside an operation.
	half := OperationFunc(func(src pipeline.Source) (pipeline.Source, error) {
		return FromSource(func(*pipeline.Env) (pipeline.Source, error) { return src, nil }).
			Resize(src.Bounds().Dx()/2, 0).Stream(src.Env())
	})
	a, err := FromBytes(in).Pipe(half).Flip().PNG().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	b, err := FromBytes(in).Resize(250, 0).Flip().PNG().ToBytes()
	if err != nil || !bytes.Equal(a, b) {
		t.Errorf("Resize inside an operation differs from Resize in the chain (%v)", err)
	}
}

// ---- a format written against the public packages only ----

// The "tpix" test format: "TPIX", width and height (32 bits each, big
// endian), a flag byte, then the rows. Flag bit 0: one byte per pixel
// (grey) instead of four (RGBA). Flag bit 1: rows stored bottom row first,
// which a decoder can only read with random access.
const (
	tpixGray     = 1
	tpixBottomUp = 2
)

type tpixOptions struct{ BottomUp bool }

type tpixReader struct {
	in       Input
	ra       io.ReaderAt
	env      *pipeline.Env
	w, h, y  int
	flags    byte
	row      []byte
	out      *image.NRGBA
	reserved int64
}

func tpixDecode(in Input, env *pipeline.Env) (pipeline.Source, error) {
	var head [13]byte
	if _, err := io.ReadFull(in, head[:]); err != nil {
		return nil, err
	}
	r := &tpixReader{in: in, env: env, w: int(binary.BigEndian.Uint32(head[4:])), h: int(binary.BigEndian.Uint32(head[8:])), flags: head[12]}
	if r.w <= 0 || r.h <= 0 || r.w > 1<<20 || r.h > 1<<20 {
		return nil, errors.New("tpix: bad size")
	}
	if r.flags&tpixBottomUp != 0 {
		ra, size, err := in.ReaderAt()
		if err != nil {
			return nil, err
		}
		if size < 13+int64(r.w)*int64(r.h)*int64(r.bpp()) {
			return nil, errors.New("tpix: file too short")
		}
		r.ra = ra
	}
	return r, nil
}

func (r *tpixReader) bpp() int {
	if r.flags&tpixGray != 0 {
		return 1
	}
	return 4
}

func (r *tpixReader) Bounds() image.Rectangle { return image.Rect(0, 0, r.w, r.h) }
func (r *tpixReader) ColorModel() color.Model { return color.NRGBAModel }
func (r *tpixReader) Env() *pipeline.Env      { return r.env }
func (r *tpixReader) Opaque() bool            { return r.flags&tpixGray != 0 }
func (r *tpixReader) Meta() pipeline.Meta     { return pipeline.Meta{ICC: []byte("tpix profile")} }

func (r *tpixReader) Close() error {
	r.env.Budget.Release(r.reserved)
	r.reserved = 0
	return nil
}

func (r *tpixReader) Next() (image.Image, error) {
	if r.y >= r.h {
		return nil, io.EOF
	}
	const rows = 8
	if r.out == nil {
		need := int64(r.w) * (4*rows + int64(r.bpp()))
		if err := r.env.Budget.Reserve(need, "tpix strip"); err != nil {
			return nil, err
		}
		r.reserved = need
		r.out, r.row = image.NewNRGBA(image.Rect(0, 0, r.w, rows)), make([]byte, r.w*r.bpp())
	}
	y1 := min(r.y+rows, r.h)
	strip := &image.NRGBA{Pix: r.out.Pix[:(y1-r.y)*r.out.Stride], Stride: r.out.Stride, Rect: image.Rect(0, r.y, r.w, y1)}
	for y := r.y; y < y1; y++ {
		var err error
		if r.ra != nil {
			_, err = r.ra.ReadAt(r.row, 13+int64(r.h-1-y)*int64(len(r.row)))
		} else {
			_, err = io.ReadFull(r.in, r.row)
		}
		if err != nil {
			return nil, fmt.Errorf("tpix: row %d: %w", y, err)
		}
		d := strip.Pix[(y-r.y)*strip.Stride:][:r.w*4]
		if r.bpp() == 4 {
			copy(d, r.row)
			continue
		}
		for x, v := range r.row {
			d[4*x], d[4*x+1], d[4*x+2], d[4*x+3] = v, v, v, 255
		}
	}
	r.y = y1
	return strip, nil
}

// tpixEncode writes grey below quality 50 and RGBA from there on, so the
// file size follows the quality (in two steps).
func tpixEncode(w io.Writer, src pipeline.Source, o EncodeOptions) error {
	opts, _ := o.Options.(tpixOptions)
	b := src.Bounds()
	var flags byte
	if o.Quality < 50 {
		flags |= tpixGray
	}
	if opts.BottomUp {
		flags |= tpixBottomUp
	}
	head := append([]byte("TPIX"), 0, 0, 0, 0, 0, 0, 0, 0, flags)
	binary.BigEndian.PutUint32(head[4:], uint32(b.Dx()))
	binary.BigEndian.PutUint32(head[8:], uint32(b.Dy()))
	if _, err := w.Write(head); err != nil {
		return err
	}
	var rowsOut [][]byte
	in := pipeline.Map(noClose{src}, func(strip *image.NRGBA) error {
		for y := strip.Rect.Min.Y; y < strip.Rect.Max.Y; y++ {
			row := strip.Pix[strip.PixOffset(strip.Rect.Min.X, y):][:strip.Rect.Dx()*4]
			out := append([]byte(nil), row...)
			if flags&tpixGray != 0 {
				out = out[:0]
				for i := 0; i < len(row); i += 4 {
					out = append(out, uint8((int(row[i])+int(row[i+1])+int(row[i+2]))/3))
				}
			}
			if opts.BottomUp {
				rowsOut = append(rowsOut, out) // a test format may keep the image
				continue
			}
			if _, err := w.Write(out); err != nil {
				return err
			}
		}
		return nil
	})
	defer in.Close()
	for {
		if _, err := in.Next(); err == io.EOF {
			break
		} else if err != nil {
			return err
		}
	}
	for i := len(rowsOut) - 1; i >= 0; i-- {
		if _, err := w.Write(rowsOut[i]); err != nil {
			return err
		}
	}
	return nil
}

// noClose keeps a stage from closing a source it was only lent.
type noClose struct{ pipeline.Source }

func (noClose) Close() error { return nil }

func (n noClose) Opaque() bool { return pipeline.IsOpaque(n.Source) }

var tpixCodec = Codec{
	Format:  Format{Name: "TPix", Extensions: []string{"tpix", ".TPX"}},
	Match:   func(h []byte) bool { return bytes.HasPrefix(h, []byte("TPIX")) },
	Decode:  tpixDecode,
	Encode:  tpixEncode,
	Quality: func(any) (int, bool) { return 90, true },
}

// withFormats registers codecs for the duration of a test.
func withFormats(t *testing.T, codecs ...Codec) {
	t.Helper()
	t.Cleanup(func() {
		for _, c := range codecs {
			UnregisterFormat(c.Name)
		}
	})
	for _, c := range codecs {
		RegisterFormat(c)
	}
}

// TestRegisterFormat: a format added from outside is recognised, read,
// written, named and limited like the ones of the package.
func TestRegisterFormat(t *testing.T) {
	withFormats(t, tpixCodec)

	f, ok := FormatByName(".TPX")
	if !ok || f.Name != "tpix" || len(f.Extensions) != 2 || f.Extensions[0] != ".tpix" || f.Extensions[1] != ".tpx" {
		t.Fatalf("FormatByName: %+v, %v", f, ok)
	}
	if all := Formats(); len(all) != 15 || all[0].Name != "tpix" || all[1].Name != "jpeg" {
		t.Fatalf("Formats: %+v", all)
	}

	src := testutil.Images(310, 205)["nrgba"]
	in := pngBytes(t, src)
	dir := t.TempDir()

	// Written by its extension and by its name; read back by its content.
	path := filepath.Join(dir, "a.tpx")
	if err := FromBytes(in).ToFile(path); err != nil {
		t.Fatal(err)
	}
	file, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := 13 + 310*205*4; len(file) != want {
		t.Fatalf("file of %d bytes, want %d", len(file), want)
	}
	byName, err := FromBytes(in).ToFormat("tpix").ToBytes()
	if err != nil || !bytes.Equal(byName, file) {
		t.Fatalf("ToFormat gives another file than the extension (%v)", err)
	}
	info, err := Open(path).Metadata()
	if err != nil || info.Format != "tpix" || info.Width != 310 || info.Height != 205 || !info.HasAlpha || !info.HasICC {
		t.Fatalf("Metadata: %+v, %v", info, err)
	}
	back, err := Open(path).PNG().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	sameImage(t, "through tpix", decodePNG(t, back), src)

	// Its options reach the encoder; a decoder that needs random access
	// gets it from every kind of input.
	up, err := FromBytes(in).ToFormat("tpix", tpixOptions{BottomUp: true}).ToBytes()
	if err != nil || bytes.Equal(up, file) || len(up) != len(file) {
		t.Fatalf("options did not reach the encoder (%v)", err)
	}
	for name, m := range map[string]*Image{
		"bytes":                FromBytes(up),
		"reader":               FromReader(bytes.NewReader(up)),
		"reader, small memory": FromReader(struct{ io.Reader }{bytes.NewReader(up)}).MemoryLimit(1 << 20),
	} {
		// Level 6: the default level's compressor needs more than the
		// small limit allows.
		got, err := m.TempDir(dir).PNG(PNGOptions{CompressionLevel: 6}).ToBytes()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		sameImage(t, "bottom-up tpix from "+name, decodePNG(t, got), src)
	}

	// The chain works on it, and the result goes on to any format.
	small, err := FromBytes(file).Resize(100, 0).Rotate(90).JPEG(JPEGOptions{Quality: 80}).ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	if c, err := jpeg.DecodeConfig(bytes.NewReader(small)); err != nil || c.Width != 66 || c.Height != 100 {
		t.Errorf("tpix to jpeg: %+v, %v", c, err)
	}
	// The colour profile its decoder reports is carried over.
	withICC, err := FromBytes(file).PNG().ToBytes()
	if err != nil || !bytes.Contains(withICC, []byte("iCCP")) {
		t.Errorf("profile of a registered format not carried over (%v)", err)
	}

	// MaxFileSize steers its quality.
	grey := 13 + 310*205
	fit, err := FromBytes(in).ToFormat("tpix").MaxFileSize(int64(grey + 100)).ToBytes()
	if err != nil || len(fit) != grey {
		t.Errorf("MaxFileSize: %d bytes, %v; want the %d of the lower quality", len(fit), err, grey)
	}
	if _, err := FromBytes(in).ToFormat("tpix").MaxFileSize(1000).ToBytes(); !errors.Is(err, ErrFileSize) {
		t.Errorf("a size no quality reaches: %v", err)
	}

	// What its decoder rejects is a bad image; what it cannot do says so.
	if _, err := FromBytes(file[:len(file)/2]).PNG().ToBytes(); !errors.Is(err, ErrFormat) {
		t.Errorf("truncated tpix: %v", err)
	}
	if _, err := FromBytes([]byte("TPIX\x00\x00\x00\x00\x00\x00\x00\x00\x00")).Metadata(); !errors.Is(err, ErrFormat) {
		t.Errorf("tpix of no size: %v", err)
	}
	_, err = FromBytes(in).MemoryLimit(1<<20).Resize(40000, 0, ResizeOptions{Kernel: KernelNearest}).
		LimitOutputPixels(1 << 40).ToFormat("tpix").ToBytes()
	if !errors.Is(err, ErrMemoryLimit) {
		t.Errorf("a strip larger than the limit: %v", err)
	}
	if _, err := FromBytes(in).ToFormat("nope").ToBytes(); err == nil || !strings.Contains(err.Error(), "tpix, jpeg") {
		t.Errorf("unknown format: %v", err)
	}
}

// TestFormatsReadOnlyWriteOnly: a format may be only read or only written.
func TestFormatsReadOnlyWriteOnly(t *testing.T) {
	ro, wo := tpixCodec, tpixCodec
	ro.Name, ro.Extensions, ro.Encode = "tpread", []string{".tpr"}, nil
	wo.Name, wo.Extensions, wo.Decode, wo.Match = "tpwrite", []string{".tpw"}, nil, nil
	withFormats(t, wo, ro)
	in := pngBytes(t, testutil.Images(40, 30)["nrgba"])
	file, err := FromBytes(in).ToFormat("tpwrite").ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	// The file is read as "tpread", which cannot be written back.
	info, err := FromBytes(file).Metadata()
	if err != nil || info.Format != "tpread" {
		t.Fatalf("Metadata: %+v, %v", info, err)
	}
	if _, err := FromBytes(file).ToBytes(); err == nil || !strings.Contains(err.Error(), "can be read but not written") {
		t.Errorf("writing a read-only format: %v", err)
	}
	if _, err := FromBytes(file).ToFormat("tpread").ToBytes(); err == nil {
		t.Error("ToFormat with a read-only format accepted")
	}
	if _, err := FromBytes(file).PNG().ToBytes(); err != nil {
		t.Errorf("read-only format to PNG: %v", err)
	}
	for _, bad := range []Codec{{}, {Format: Format{Name: "a b"}, Encode: tpixEncode}, {Format: Format{Name: "x"}}, {Format: Format{Name: "x"}, Decode: tpixDecode}} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("RegisterFormat accepted %+v", bad.Format)
				}
			}()
			RegisterFormat(bad)
		}()
	}
}

// TestWrapBuiltinFormat: a format of the package can be given a decoder or
// encoder that adds to the package's own.
func TestWrapBuiltinFormat(t *testing.T) {
	jp, ok := CodecByName("jpg")
	if !ok || jp.Name != "jpeg" || jp.Decode == nil || jp.Encode == nil || jp.Quality == nil {
		t.Fatalf("CodecByName: %+v, %v", jp.Format, ok)
	}
	var encodes, decodes atomic.Int64
	var seen EncodeOptions
	var mu sync.Mutex
	wrapped := jp
	wrapped.Encode = func(w io.Writer, src pipeline.Source, o EncodeOptions) error {
		encodes.Add(1)
		mu.Lock()
		seen = o
		mu.Unlock()
		return jp.Encode(w, src, o)
	}
	wrapped.Decode = func(in Input, env *pipeline.Env) (pipeline.Source, error) {
		decodes.Add(1)
		return jp.Decode(in, env)
	}
	in := jpegBytes(testutil.Images(320, 240)["rgba-opaque"], 90)
	run := func() []byte {
		t.Helper()
		out, err := FromBytes(in).Resize(160, 0, ResizeOptions{FastShrinkOnLoad: true}).JPEG(JPEGOptions{Quality: 70, Progressive: true}).ToBytes()
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	before := run()
	withFormats(t, wrapped)
	after := run()
	if !bytes.Equal(before, after) {
		t.Error("a wrapper that only counts changed the file")
	}
	if encodes.Load() != 1 || decodes.Load() != 1 {
		t.Errorf("wrapper called %d times to encode, %d to decode", encodes.Load(), decodes.Load())
	}
	if o, ok := seen.Options.(JPEGOptions); !ok || !o.Progressive || seen.Quality != 70 {
		t.Errorf("the wrapper saw %+v", seen)
	}
	if all := Formats(); len(all) != 14 || all[0].Name != "jpeg" {
		t.Errorf("replacing a format changed the list: %+v", all)
	}
	// Taking the replacement back brings the package's own format back.
	if !UnregisterFormat("JPEG") || UnregisterFormat("jpeg") || UnregisterFormat("no-such-format") {
		t.Error("UnregisterFormat: wrong report")
	}
	if again := run(); !bytes.Equal(again, before) || encodes.Load() != 1 {
		t.Errorf("after UnregisterFormat the wrapper was still used (%d encodes)", encodes.Load())
	}
	if all := Formats(); len(all) != 14 || all[0].Name != "jpeg" {
		t.Errorf("unregistering changed the list: %+v", all)
	}

	// The package's decoder on an input of the caller's own, the package's
	// encoder on a stream of the caller's own.
	png, _ := CodecByName("png")
	src, err := jp.Decode(memInput{bytes.NewReader(in)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := png.Encode(&out, src, EncodeOptions{}); err != nil {
		t.Fatal(err)
	}
	src.Close()
	ref, _ := decodeJPEG(bytes.NewReader(in))
	same8(t, "decoder and encoder called directly", decodePNG(t, out.Bytes()), ref)
	bmp, _ := CodecByName("bmp")
	src, err = bmp.Decode(memInput{bytes.NewReader(must(FromBytes(in).BMP().ToBytes()))}, nil)
	if err != nil {
		t.Fatal(err)
	}
	img, err := pipeline.Collect(src)
	src.Close()
	if err != nil {
		t.Fatal(err)
	}
	same8(t, "a decoder that reads at offsets, called directly", img, ref)
}

// memInput is an Input made outside the package.
type memInput struct{ *bytes.Reader }

func (m memInput) ReaderAt() (io.ReaderAt, int64, error) { return m.Reader, m.Size(), nil }

// TestToFormat: the formats of the package by name, with their options.
func TestToFormat(t *testing.T) {
	in := pngBytes(t, testutil.Images(200, 150)["rgba-opaque"])
	same := func(what string, a, b *Image) {
		t.Helper()
		x, err := a.ToBytes()
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		y, err := b.ToBytes()
		if err != nil || !bytes.Equal(x, y) {
			t.Errorf("%s: ToFormat gives another file than the method (%v)", what, err)
		}
	}
	same("jpeg", FromBytes(in).ToFormat("jpeg"), FromBytes(in).JPEG())
	same("jpeg with options", FromBytes(in).ToFormat("JPG", JPEGOptions{Quality: 60, Optimize: true}), FromBytes(in).JPEG(JPEGOptions{Quality: 60, Optimize: true}))
	same("jpeg, options by pointer", FromBytes(in).ToFormat("jpeg", &JPEGOptions{Quality: 60}), FromBytes(in).JPEG(JPEGOptions{Quality: 60}))
	same("png", FromBytes(in).ToFormat(".png", PNGOptions{Colors: 16}), FromBytes(in).PNG(PNGOptions{Colors: 16}))
	same("webp", FromBytes(in).ToFormat("webp", WebPOptions{Lossless: true}), FromBytes(in).WebP(WebPOptions{Lossless: true}))
	same("gif", FromBytes(in).ToFormat("gif", GIFOptions{Colors: 8}), FromBytes(in).GIF(GIFOptions{Colors: 8}))
	same("tiff", FromBytes(in).ToFormat("tif", TIFFOptions{Compression: TIFFDeflate}), FromBytes(in).TIFF(TIFFOptions{Compression: TIFFDeflate}))
	same("bmp", FromBytes(in).ToFormat("bmp"), FromBytes(in).BMP())
	// The default quality is 80.
	same("default quality", FromBytes(in).JPEG(), FromBytes(in).JPEG(JPEGOptions{Quality: 80}))
	same("quality", FromBytes(in).ToFormat("jpg", JPEGOptions{Quality: 60}), FromBytes(in).JPEG(JPEGOptions{Quality: 60}))
	// Options of one format do not leak into another chosen afterwards by
	// the file name.
	dir := t.TempDir()
	if err := FromBytes(in).PNG(PNGOptions{Colors: 4}).ToFile(filepath.Join(dir, "a.png")); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []*Image{
		FromBytes(in).ToFormat("jpeg", PNGOptions{}),
		FromBytes(in).ToFormat("bmp", 3),
		FromBytes(in).ToFormat("jpeg", JPEGOptions{}, JPEGOptions{}),
		FromBytes(in).ToFormat(""),
		FromBytes(in).ToFormat("heic"),
	} {
		if _, err := bad.ToBytes(); err == nil {
			t.Error("a wrong ToFormat was accepted")
		}
	}
}

// TestCustomFilter: a filter of the caller's own is used as given.
func TestCustomFilter(t *testing.T) {
	in := pngBytes(t, testutil.Images(400, 300)["rgba-opaque"])
	tent := &CustomKernel{Support: 1, At: func(t float64) float64 { return 1 - t }}
	for _, size := range []int{150, 640} {
		a, err := FromBytes(in).Resize(size, 0, ResizeOptions{CustomKernel: tent}).PNG().ToBytes()
		if err != nil {
			t.Fatal(err)
		}
		b, err := FromBytes(in).Resize(size, 0, ResizeOptions{Kernel: KernelLinear}).PNG().ToBytes()
		if err != nil || !bytes.Equal(a, b) {
			t.Errorf("to %d: the tent filter differs from KernelLinear (%v)", size, err)
		}
	}
	for _, bad := range []*CustomKernel{{}, {Support: 1}, {Support: -1, At: tent.At}, {Support: math.Inf(1), At: tent.At}, {Support: math.NaN(), At: tent.At}} {
		if _, err := FromBytes(in).Resize(100, 0, ResizeOptions{CustomKernel: bad}).ToBytes(); err == nil {
			t.Errorf("filter %+v accepted", *bad)
		}
	}
}

// TestPanicBecomesError: a bug that an input brings out, in the package or
// in code given to it, ends the run with an error; it does not end the
// program, and nothing is left behind.
func TestPanicBecomesError(t *testing.T) {
	in := jpegBytes(testutil.Images(1600, 1200)["rgba-opaque"], 90)
	dir := t.TempDir()
	tmp := filepath.Join(dir, "tmp")
	os.Mkdir(tmp, 0o755)
	rows := 0
	// The bug: an index past the end. (Not a nil pointer: that one reaches
	// the runtime as a memory fault, and an emulated processor, as the
	// automatic tests use, does not always report the faulting address the
	// way the runtime needs to recognise it. An index is checked in code,
	// the same everywhere.)
	bad := func(strip *image.NRGBA) error {
		if rows += strip.Rect.Dy(); rows > 200 {
			_ = strip.Pix[len(strip.Pix)+rows]
		}
		return nil
	}
	for threads := 1; threads <= 3; threads++ {
		rows = 0
		out := filepath.Join(dir, "out.png")
		// A rotation first, so that working storage is in use when it happens.
		err := FromBytes(in).MemoryLimit(8 << 20).TempDir(tmp).Threads(threads).Rotate(90).Pixels(bad).ToFile(out)
		var pe *pipeline.PanicError
		if !errors.As(err, &pe) {
			t.Fatalf("threads %d: %v, want a PanicError", threads, err)
		}
		if !strings.Contains(string(pe.Stack), "extend_test.go") || !strings.Contains(err.Error(), "index out of range") {
			t.Errorf("threads %d: the error does not say where: %v", threads, err)
		}
		if errors.Is(err, ErrFormat) {
			t.Errorf("threads %d: a bug reported as a bad image", threads)
		}
		if n := tempFiles(tmp); n != 0 {
			t.Errorf("threads %d: %d temporary files left", threads, n)
		}
		if names, _ := filepath.Glob(filepath.Join(dir, "*")); len(names) != 1 {
			t.Errorf("threads %d: left in the output directory: %v", threads, names)
		}
	}
	// In the other entry points too.
	explode := OperationFunc(func(pipeline.Source) (pipeline.Source, error) { panic("made a mess") })
	var pe *pipeline.PanicError
	if _, _, err := FromBytes(in).Pipe(explode).OutputSize(); !errors.As(err, &pe) {
		t.Errorf("OutputSize: %v", err)
	}
	if _, err := FromBytes(in).Pipe(explode).Stream(nil); !errors.As(err, &pe) {
		t.Errorf("Stream: %v", err)
	}
	if _, err := FromBytes(in).Pipe(explode).ToImage(); !errors.As(err, &pe) {
		t.Errorf("ToImage: %v", err)
	}
	withFormats(t, Codec{Format: Format{Name: "boom"}, Match: func(h []byte) bool { return h[100] == 0 }, Decode: tpixDecode})
	if _, err := FromBytes(in).Metadata(); !errors.As(err, &pe) {
		t.Errorf("Metadata: %v", err)
	}
}

// TestLimitOutputPixels: a chain that makes an image larger than the limit
// is refused before any pixel is made.
func TestLimitOutputPixels(t *testing.T) {
	in := pngBytes(t, testutil.Images(40, 30)["rgba-opaque"])
	var w countingWriter
	err := FromBytes(in).Resize(40000, 0).JPEG(JPEGOptions{Quality: 80}).ToWriter(&w)
	if !errors.Is(err, ErrPixelLimit) || w.n != 0 {
		t.Errorf("40000x30000 from a 40x30 file: %v, %d bytes written", err, w.n)
	}
	if _, _, err := FromBytes(in).Resize(40000, 0).OutputSize(); !errors.Is(err, ErrPixelLimit) {
		t.Errorf("OutputSize of a result over the limit: %v", err)
	}
	if _, err := FromBytes(in).Resize(400, 0).LimitOutputPixels(400 * 299).ToBytes(); !errors.Is(err, ErrPixelLimit) {
		t.Errorf("400x300 with a limit of 400x299: %v", err)
	}
	if _, err := FromBytes(in).Resize(400, 0).LimitOutputPixels(400 * 300).ToBytes(); err != nil {
		t.Errorf("400x300 with a limit of 400x300: %v", err)
	}
	// Also when it is only large in between, or tall and narrow, or made
	// by an operation that is not a resize.
	if _, err := FromBytes(in).Resize(40000, 0).Resize(40, 0).ToBytes(); !errors.Is(err, ErrPixelLimit) {
		t.Errorf("large in between: %v", err)
	}
	if _, err := FromBytes(in).Resize(4, 100000000, ResizeOptions{Fit: FitFill}).ToBytes(); !errors.Is(err, ErrPixelLimit) {
		t.Errorf("tall and narrow: %v", err)
	}
	if _, err := FromBytes(in).Resize(30000, 30000, ResizeOptions{Fit: FitContain}).ToBytes(); !errors.Is(err, ErrPixelLimit) {
		t.Errorf("bars around the image: %v", err)
	}
	if _, err := FromBytes(in).Pipe(border{9000, color.NRGBA{}}).ToBytes(); !errors.Is(err, ErrPixelLimit) {
		t.Errorf("an operation of the caller's: %v", err)
	}
	// An overlay has its own limit.
	if _, err := FromBytes(in).Composite(FromBytes(in).Resize(40000, 0)).ToBytes(); !errors.Is(err, ErrPixelLimit) {
		t.Errorf("an overlay over the limit: %v", err)
	}
}

type countingWriter struct{ n int }

func (c *countingWriter) Write(p []byte) (int, error) { c.n += len(p); return len(p), nil }

// TestAddTransparency: an operation that makes pixels of an opaque image
// transparent says so with pipeline.WithAlpha, and the transparency
// reaches the file.
func TestAddTransparency(t *testing.T) {
	in := jpegBytes(testutil.Images(120, 80)["rgba-opaque"], 90)
	fade := OperationFunc(func(src pipeline.Source) (pipeline.Source, error) {
		return pipeline.Map(pipeline.WithAlpha(src), func(strip *image.NRGBA) error {
			for y := strip.Rect.Min.Y; y < strip.Rect.Max.Y; y++ {
				row := strip.Pix[strip.PixOffset(strip.Rect.Min.X, y):][:strip.Rect.Dx()*4]
				for x := 0; x < len(row); x += 4 {
					row[x+3] = uint8(x / 4 * 255 / 119) // from clear on the left to solid on the right
				}
			}
			return nil
		}), nil
	})
	got, err := FromBytes(in).Pipe(fade).PNG().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	img := decodePNG(t, got)
	if _, _, _, a := img.At(0, 40).RGBA(); a != 0 {
		t.Errorf("left edge has alpha %d, want 0", a)
	}
	if _, _, _, a := img.At(119, 40).RGBA(); a != 0xffff {
		t.Errorf("right edge has alpha %d, want full", a)
	}
	info, err := FromBytes(got).Metadata()
	if err != nil || !info.HasAlpha {
		t.Errorf("the file has no alpha channel: %+v, %v", info, err)
	}
	// Without the operation the same input gives a file without one.
	plain, _ := FromBytes(in).PNG().ToBytes()
	if info, _ := FromBytes(plain).Metadata(); info.HasAlpha {
		t.Error("an opaque image was written with an alpha channel")
	}
}

// TestRegisterWhileRunning: formats may be registered while recipes run.
func TestRegisterWhileRunning(t *testing.T) {
	t.Cleanup(func() {
		for i := 0; i < 7; i++ {
			UnregisterFormat(fmt.Sprintf("tp%d", i))
		}
	})
	in := pngBytes(t, testutil.Images(64, 48)["nrgba"])
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if _, err := FromBytes(in).Resize(32, 0).JPEG(JPEGOptions{Quality: 80}).ToBytes(); err != nil {
					t.Error(err)
					return
				}
				Formats()
				FormatByName("tpix")
			}
		}()
	}
	for i := 0; i < 200; i++ {
		c := tpixCodec
		c.Name = fmt.Sprintf("tp%d", i%7)
		c.Extensions = nil
		RegisterFormat(c)
	}
	close(stop)
	wg.Wait()
	if n := len(Formats()); n != 14+7 {
		t.Errorf("%d formats after registering seven names many times", n)
	}
}

// TestAffine: Affine is sharp's affine to the byte: a turn (crossing
// libvips' tiles of 128 columns), an enlargement (whole rows), and offsets
// with a background, with and without transparency; the identity is the
// image itself, and by name it takes the same words.
func TestAffine(t *testing.T) {
	rgb, rgba := sourcePNG(t, "pillow/source.png"), sourcePNG(t, "pillow/median-rgba-source.png")
	// The sine and cosine of 30 degrees as JavaScript gives them, with
	// which the references were made (math.Sincos gives a sine of 0.5,
	// which moves the box around the turned image by a pixel).
	sin, cos := 0.49999999999999994, 0.8660254037844387
	offs := AffineOptions{Background: color.NRGBA{0x20, 0x50, 0xc0, 255}, InputX: 3, InputY: -2, OutputX: 10.5, OutputY: -5.25}
	with := func(o AffineOptions, i Interpolator) AffineOptions {
		o.Interpolator = i
		return o
	}
	nearest, bicubic := AffineOptions{Interpolator: InterpolateNearest}, AffineOptions{Interpolator: InterpolateBicubic}
	for _, c := range []struct {
		name string
		m    *Image
	}{
		{"affine-rot30", FromBytes(rgb).Affine(cos, -sin, sin, cos)},
		{"affine-scale", FromBytes(rgb).Affine(1.5, 0, 0, 1.37)},
		{"affine-offs", FromBytes(rgb).Affine(0.9, 0.2, -0.2, 1.1, offs)},
		{"affine-rgba-rot30", FromBytes(rgba).Affine(cos, -sin, sin, cos)},
		{"affine-rgba-offs", FromBytes(rgba).Affine(0.9, 0.2, -0.2, 1.1, offs)},
		{"affine-nearest-rot30", FromBytes(rgb).Affine(cos, -sin, sin, cos, nearest)},
		{"affine-bicubic-rot30", FromBytes(rgb).Affine(cos, -sin, sin, cos, bicubic)},
		{"affine-bicubic-offs", FromBytes(rgb).Affine(0.9, 0.2, -0.2, 1.1, with(offs, InterpolateBicubic))},
		{"affine-rgba-bicubic-rot30", FromBytes(rgba).Affine(cos, -sin, sin, cos, bicubic)},
		{"affine-rgba-nearest-offs", FromBytes(rgba).Affine(0.9, 0.2, -0.2, 1.1, with(offs, InterpolateNearest))},
	} {
		for _, threads := range []int{1, 4} {
			got := decodePNG(t, must(c.m.Clone().Threads(threads).PNG().ToBytes()))
			if maxd, mean, _ := channelDiff(t, got, readPNG(t, "sharp/"+c.name+".png")); maxd != 0 {
				t.Errorf("%s, %d threads: differs from sharp by up to %d (mean %.3f)", c.name, threads, maxd, mean)
			}
		}
	}
	if a, b := must(FromBytes(rgba).Affine(1, 0, 0, 1).PNG().ToBytes()), must(FromBytes(rgba).PNG().ToBytes()); !bytes.Equal(a, b) {
		t.Error("the identity changed the image")
	}
	if w, h, err := FromBytes(rgb).Affine(1, 0.3, 0.1, 1).OutputSize(); err != nil || w != 181 || h != 118 {
		t.Errorf("size %dx%d (%v), want 181x118 as sharp", w, h, err)
	}
	a := must(FromBytes(rgb).Affine(0.9, 0.2, -0.2, 1.1, offs).PNG().ToBytes())
	b := must(FromBytes(rgb).Do("affine", "0.9", "0.2", "-0.2", "1.1", "#2050c0", "3", "-2", "10.5", "-5.25").PNG().ToBytes())
	if !bytes.Equal(a, b) {
		t.Error("by name: another image")
	}
	a = must(FromBytes(rgb).Affine(0.9, 0.2, -0.2, 1.1, with(offs, InterpolateBicubic)).PNG().ToBytes())
	b = must(FromBytes(rgb).Do("affine", "0.9", "0.2", "-0.2", "1.1", "#2050c0", "3", "-2", "10.5", "-5.25", "bicubic").PNG().ToBytes())
	if !bytes.Equal(a, b) {
		t.Error("by name, bicubic: another image")
	}
	for _, m := range []*Image{FromBytes(rgb).Affine(1, 2, 2, 4), FromBytes(rgb).Affine(math.NaN(), 0, 0, 1), FromBytes(rgb).Affine(0.001, 0, 0, 1),
		FromBytes(rgb).Do("affine", "1", "0", "0"), FromBytes(rgb).Do("affine", "1", "0", "0", "1", "pinkish"),
		FromBytes(rgb).Affine(1, 0.1, 0, 1, AffineOptions{Interpolator: 3})} {
		if _, err := m.PNG().ToBytes(); err == nil {
			t.Error("no error")
		}
	}
}
