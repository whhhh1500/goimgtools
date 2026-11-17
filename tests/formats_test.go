package tests

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	stddraw "image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/image/bmp"
	"golang.org/x/image/tiff"
	"golang.org/x/image/webp"

	. "github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/internal/webpstream"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// toPNGRef is what PNG output is expected to hold for a decoded image: the
// image itself for the types PNG stores directly, otherwise 8-bit RGBA (or
// NRGBA when the type can be transparent).
func toPNGRef(m image.Image) image.Image {
	switch m.(type) {
	case *image.Gray, *image.Gray16, *image.RGBA, *image.NRGBA, *image.RGBA64, *image.NRGBA64, *image.Paletted:
		return m
	case *image.NYCbCrA:
		out := image.NewNRGBA(m.Bounds())
		stddraw.Draw(out, out.Bounds(), m, m.Bounds().Min, stddraw.Src)
		return out
	}
	out := image.NewRGBA(m.Bounds())
	stddraw.Draw(out, out.Bounds(), m, m.Bounds().Min, stddraw.Src)
	return out
}

func encodePNG(t *testing.T, m image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, m); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// checkFile converts one file to PNG through the library, from a path, from
// bytes and from a plain reader, and requires the PNG the standard decoders
// plus png.Encode produce.
func checkFile(t *testing.T, path string, ref func(io.Reader) (image.Image, error), format string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Base(path)
	want, werr := ref(bytes.NewReader(data))
	inputs := map[string]func() *Image{
		"path":   func() *Image { return Open(path) },
		"bytes":  func() *Image { return FromBytes(data) },
		"reader": func() *Image { return FromReader(struct{ io.Reader }{bytes.NewReader(data)}) },
	}
	for how, in := range inputs {
		// Level 6 is that of png.Encode; the reference has no profile.
		got, gerr := in().StripICC().Density(0).PNG(PNGOptions{CompressionLevel: 6}).ToBytes()
		if werr != nil || want.Bounds().Empty() {
			if gerr == nil {
				t.Errorf("%s (%s): reference failed (%v) but the library produced output", name, how, werr)
			}
			continue
		}
		if gerr != nil {
			t.Errorf("%s (%s): %v", name, how, gerr)
			continue
		}
		pngLikeStdlib(t, name+" ("+how+")", got, encodePNG(t, toPNGRef(want)))
		info, err := in().Metadata()
		if err != nil {
			t.Errorf("%s (%s): Metadata: %v", name, how, err)
			continue
		}
		if info.Format != format || info.Width != want.Bounds().Dx() || info.Height != want.Bounds().Dy() {
			t.Errorf("%s (%s): Metadata = %+v, want %s %v", name, how, info, format, want.Bounds().Size())
		}
	}
}

// TestEveryFormat runs the Go and x/image test corpora of every supported
// input format through the public interface.
func TestEveryFormat(t *testing.T) {
	goDir := filepath.Join(runtime.GOROOT(), "src", "image", "testdata")
	xDir := testutil.XImageTestdata()
	if xDir == "" {
		t.Fatal("golang.org/x/image testdata not found")
	}
	refs := map[string]struct {
		format string
		dec    func(io.Reader) (image.Image, error)
	}{
		".png":  {"png", png.Decode},
		".jpeg": {"jpeg", decodeJPEG},
		".gif":  {"gif", gif.Decode},
		".bmp":  {"bmp", bmp.Decode},
		".tiff": {"tiff", tiff.Decode},
		".webp": {"webp", webpRGB},
	}
	seen := map[string]int{}
	for _, dir := range []string{goDir, xDir, filepath.Join(goDir, "..", "png", "testdata", "pngsuite")} {
		files, _ := filepath.Glob(filepath.Join(dir, "*"))
		for _, f := range files {
			r, ok := refs[strings.ToLower(filepath.Ext(f))]
			if !ok {
				continue
			}
			seen[r.format]++
			checkFile(t, f, r.dec, r.format)
		}
	}
	for _, r := range refs {
		if seen[r.format] < 3 {
			t.Errorf("only %d %s files found in the corpora", seen[r.format], r.format)
		}
	}
	t.Logf("files checked: %v", seen)
}

// TestFormatsThroughChain runs resize + flatten + JPEG on one image of every
// input format and compares with the same steps on the whole decoded image.
func TestFormatsThroughChain(t *testing.T) {
	src := testutil.Images(613, 457)
	encoders := map[string]func(io.Writer, image.Image) error{
		"png":  png.Encode,
		"jpeg": func(w io.Writer, m image.Image) error { return jpeg.Encode(w, m, &jpeg.Options{Quality: 90}) },
		"gif":  func(w io.Writer, m image.Image) error { return gif.Encode(w, m, nil) },
		"bmp":  bmp.Encode,
		"tiff": func(w io.Writer, m image.Image) error {
			return tiff.Encode(w, m, &tiff.Options{Compression: tiff.Deflate})
		},
	}
	decoders := map[string]func(io.Reader) (image.Image, error){
		"png": png.Decode, "jpeg": decodeJPEG, "gif": gif.Decode, "bmp": bmp.Decode, "tiff": tiff.Decode,
	}
	for format, enc := range encoders {
		for _, name := range []string{"nrgba", "rgba-opaque", "gray", "paletted"} {
			var in bytes.Buffer
			if err := enc(&in, src[name]); err != nil {
				t.Fatalf("%s/%s: encode: %v", format, name, err)
			}
			got, err := FromBytes(in.Bytes()).Density(0).
				Resize(200, 200, ResizeOptions{Fit: FitInside}).
				Flatten(color.White).
				JPEG(85).ToBytes()
			if err != nil {
				t.Fatalf("%s/%s: %v", format, name, err)
			}
			dec, err := decoders[format](bytes.NewReader(in.Bytes()))
			if err != nil {
				t.Fatal(err)
			}
			small := scaled(dec, 200, 149)
			ref := image.NewRGBA(small.Bounds())
			stddraw.Draw(ref, ref.Bounds(), image.NewUniform(color.White), image.Point{}, stddraw.Src)
			stddraw.Draw(ref, ref.Bounds(), small, image.Point{}, stddraw.Over)
			want := jpegBytes(ref, 85)
			if _, isGray := dec.(*image.Gray); isGray {
				// A greyscale image stays greyscale through the resize: the
				// same values in one channel, and a JPEG of one component.
				g := image.NewGray(small.Bounds())
				for i := range g.Pix {
					g.Pix[i] = small.Pix[4*i]
				}
				want = jpegBytes(g, 85)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("%s/%s: output differs from the reference", format, name)
			}
		}
	}
}

// TestPNGOutput covers the output side: format chosen by extension, by
// PNG(), kept from the input, and the compression level.
func TestPNGOutput(t *testing.T) {
	dir := t.TempDir()
	m := testutil.Images(120, 90)["nrgba"]
	in := filepath.Join(dir, "in.png")
	os.WriteFile(in, encodePNG(t, m), 0o644)

	// Same format as the input when nothing says otherwise; at level 6 the
	// bytes of png.Encode, at the default level the same pixels in a file
	// no larger.
	six, err := Open(in).PNG(PNGOptions{CompressionLevel: 6}).ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(six, encodePNG(t, m)) {
		t.Error("PNG to PNG at level 6 differs from png.Encode")
	}
	got, err := Open(in).ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	if d, _, _ := channelDiff(t, decodePNG(t, got), m); d != 0 || len(got) > len(six) {
		t.Errorf("PNG to PNG at the default level: pixels differ by %d, %d bytes against %d at level 6", d, len(got), len(six))
	}
	// The extension decides.
	out := filepath.Join(dir, "out.PNG")
	if err := Open(in).Resize(60, 0).ToFile(out); err != nil {
		t.Fatal(err)
	}
	if _, err := png.Decode(bytes.NewReader(must(os.ReadFile(out)))); err != nil {
		t.Fatalf("out.PNG is not a PNG: %v", err)
	}
	// Levels: 9 is no larger than 1, and both decode to the same pixels.
	fast, err := Open(in).PNG(PNGOptions{CompressionLevel: 1}).ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	small, err := Open(in).PNG(PNGOptions{CompressionLevel: 9}).ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	if len(small) > len(fast) {
		t.Errorf("level 9 (%d bytes) is larger than level 1 (%d bytes)", len(small), len(fast))
	}
	if _, err := Open(in).PNG(PNGOptions{CompressionLevel: 12}).ToBytes(); err == nil {
		t.Error("level 12: no error")
	}
	// An unknown output extension is an error, and leaves nothing behind.
	if err := Open(in).ToFile(filepath.Join(dir, "out.avif")); err == nil {
		t.Error("writing .avif: no error")
	}
	if names, _ := filepath.Glob(filepath.Join(dir, "*")); len(names) != 2 {
		t.Errorf("unexpected files: %v", names)
	}
}

// TestMemoryLimitAndDisk checks that a small limit sends an interlaced PNG
// and a progressive JPEG through temporary files with the same output, that
// the files are removed, and that a whole-decode format reports the limit.
func TestMemoryLimitAndDisk(t *testing.T) {
	goDir := filepath.Join(runtime.GOROOT(), "src", "image", "testdata")
	for _, name := range []string{"video-001.progressive.jpeg", "video-001.jpeg"} {
		in := filepath.Join(goDir, name)
		want, err := Open(in).PNG().ToBytes()
		if err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		// Each extra thread holds three strips of its own.
		limit := int64(300+150*max(0, defaultThreads-1)) << 10
		got, err := Open(in).MemoryLimit(limit).TempDir(dir).PNG().ToBytes()
		if err != nil {
			t.Fatalf("%s with a %dKB limit: %v", name, limit>>10, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s: output changed under a small limit", name)
		}
		if names, _ := filepath.Glob(filepath.Join(dir, "*")); len(names) != 0 {
			t.Errorf("%s: temp files left: %v", name, names)
		}
	}
	xDir := testutil.XImageTestdata()
	big := filepath.Join(xDir, "blue-purple-pink-large.lossless.webp")
	if _, err := Open(big).MemoryLimit(1 << 20).PNG().ToBytes(); !errors.Is(err, ErrMemoryLimit) {
		t.Errorf("large WebP under a 1MB limit: err = %v, want ErrMemoryLimit", err)
	}
	if _, err := Open(big).PNG().ToBytes(); err != nil {
		t.Errorf("large WebP under the default limit: %v", err)
	}
}

// TestNotImages feeds garbage and truncated files of every format.
func TestNotImages(t *testing.T) {
	for _, data := range [][]byte{nil, []byte("x"), []byte("GIF89a"), []byte("\x89PNG\r\n\x1a\n"), []byte("\xff\xd8\xff"),
		[]byte("BM"), []byte("II*\x00"), []byte("RIFF\x00\x00\x00\x00WEBP"), bytes.Repeat([]byte{0}, 100)} {
		if _, err := FromBytes(data).PNG().ToBytes(); err == nil {
			t.Errorf("%q: no error", data)
		}
		if _, err := FromBytes(data).Metadata(); err == nil {
			t.Errorf("%q: Metadata: no error", data)
		}
	}
	if err := Open(filepath.Join(t.TempDir(), "missing.png")).ToFile(filepath.Join(t.TempDir(), "o.jpg")); err == nil {
		t.Error("missing input: no error")
	}
}

// webpRGB is the x/image decoder followed by the conversion to RGB that
// this module applies to lossy images, libwebp's (x/image leaves the
// samples in video range and each chroma sample over its 2x2 block, which
// makes its colours differ from libwebp's).
func webpRGB(r io.Reader) (image.Image, error) {
	m, err := webp.Decode(r)
	if err != nil {
		return nil, err
	}
	if m.Bounds().Empty() {
		return m, nil
	}
	return pipeline.Collect(webpstream.ToRGB(pipeline.FromImage(m, nil)))
}

// pngLikeStdlib requires got to be want, png.Encode's file (at the same
// level), byte for byte when the image data fits one compression block of
// 256KB; beyond that the library compresses block by block (so that it can
// use several cores): then the same pixels, in a file at most 1% and 64
// bytes a block larger.
func pngLikeStdlib(t *testing.T, what string, got, want []byte) {
	t.Helper()
	if bytes.Equal(got, want) {
		return
	}
	w, err := png.Decode(bytes.NewReader(want))
	if err != nil {
		t.Fatal(err)
	}
	g, err := png.Decode(bytes.NewReader(got))
	if err != nil {
		t.Errorf("%s: %v", what, err)
		return
	}
	b := w.Bounds()
	bpp := map[string]int{"*image.Gray": 1, "*image.Gray16": 2, "*image.RGBA": 4, "*image.NRGBA": 4,
		"*image.RGBA64": 8, "*image.NRGBA64": 8, "*image.Paletted": 1}[fmt.Sprintf("%T", w)]
	data := (1 + b.Dx()*bpp) * b.Dy()
	if bpp == 4 && fmt.Sprintf("%T", g) == "*image.RGBA" {
		data = (1 + b.Dx()*3) * b.Dy() // written as RGB when opaque
	}
	if bpp == 0 || data <= 256<<10 {
		t.Errorf("%s: PNG output differs from the reference", what)
		return
	}
	sameImage(t, what, g, w)
	if blocks := (data + 256<<10 - 1) / (256 << 10); len(got) > len(want)+len(want)/100+64*blocks {
		t.Errorf("%s: %d bytes, png.Encode %d", what, len(got), len(want))
	}
}

// failingAt is a Codec Input of the caller's that cannot be read at any
// offset.
type failingAt struct{ io.Reader }

func (failingAt) ReaderAt() (io.ReaderAt, int64, error) {
	return nil, 0, errors.New("no random access here")
}

// TestCodecInputProgressive decodes a progressive JPEG through the Codec
// from an Input that has no random access to give: the decoder does not
// ask for it, and stores the coefficients as for a stream.
func TestCodecInputProgressive(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(runtime.GOROOT(), "src", "image", "testdata", "video-001.progressive.jpeg"))
	if err != nil {
		t.Fatal(err)
	}
	c, ok := CodecByName("jpeg")
	if !ok {
		t.Fatal("no jpeg codec")
	}
	src, err := c.Decode(failingAt{bytes.NewReader(data)}, pipeline.NewEnv(0, t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	got, err := pipeline.Collect(src)
	src.Close()
	if err != nil {
		t.Fatal(err)
	}
	want, err := decodeJPEG(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	sameImage(t, "progressive through the codec", got, want)
}
