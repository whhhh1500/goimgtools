package tests

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"path/filepath"
	"testing"

	. "github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/internal/testutil"
)

// photoLike is a picture with smooth areas, edges and a little texture, so
// that compression options behave as they do on photographs.
func photoLike(w, h int) *image.RGBA {
	m := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r := uint8(40 + 180*x/w)
			g := uint8(60 + 150*y/h)
			b := uint8(128 + 100*((x/40+y/30)%2) - 50)
			n := uint8((x*7 + y*13) % 9)
			m.SetRGBA(x, y, color.RGBA{r + n, g + n, b, 255})
		}
	}
	return m
}

// TestJPEGOptimizeAndProgressive: the two options make the file smaller and
// leave every pixel as it was.
func TestJPEGOptimizeAndProgressive(t *testing.T) {
	decode := func(b []byte) image.Image {
		t.Helper()
		m, err := jpeg.Decode(bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	progressive := func(b []byte) bool { return bytes.Contains(b, []byte{0xff, 0xc2}) }
	for name, src := range map[string]image.Image{"colour": photoLike(613, 457), "gray": testutil.Images(301, 203)["gray"]} {
		in := pngBytes(t, src)
		for _, q := range []int{60, 90} {
			plain, err := FromBytes(in).JPEG(q).ToBytes()
			if err != nil {
				t.Fatal(err)
			}
			opt, err := FromBytes(in).JPEG(q, JPEGOptions{Optimize: true}).ToBytes()
			if err != nil {
				t.Fatalf("%s q%d optimize: %v", name, q, err)
			}
			prog, err := FromBytes(in).JPEG(q, JPEGOptions{Progressive: true}).ToBytes()
			if err != nil {
				t.Fatalf("%s q%d progressive: %v", name, q, err)
			}
			if !bytes.Equal(plain, jpegBytes(decodePNG(t, in), q)) {
				t.Errorf("%s q%d: without options the file is not the standard library's", name, q)
			}
			sameImage(t, name+" optimized", decode(opt), decode(plain))
			sameImage(t, name+" progressive", decode(prog), decode(plain))
			if len(opt) >= len(plain) {
				t.Errorf("%s q%d: optimized %d bytes, plain %d", name, q, len(opt), len(plain))
			}
			if name == "colour" && len(prog) >= len(opt) {
				t.Errorf("%s q%d: progressive %d bytes, optimized %d", name, q, len(prog), len(opt))
			}
			if progressive(plain) || progressive(opt) || !progressive(prog) {
				t.Errorf("%s q%d: progressive marker: plain %v, optimized %v, progressive %v", name, q, progressive(plain), progressive(opt), progressive(prog))
			}
			// This module reads its own files back to the same pixels.
			a, err := FromBytes(prog).PNG().ToBytes()
			if err != nil {
				t.Fatal(err)
			}
			b, _ := FromBytes(plain).PNG().ToBytes()
			if !bytes.Equal(a, b) {
				t.Errorf("%s q%d: the progressive file reads back differently", name, q)
			}
		}
	}
	// After a chain, under a small memory limit (the coefficients go to a
	// temporary file), with a size limit on top.
	in := pngBytes(t, photoLike(613, 457))
	dir := t.TempDir()
	want, _ := FromBytes(in).Resize(400, 0).JPEG(80, JPEGOptions{Progressive: true}).ToBytes()
	got, err := FromBytes(in).Resize(400, 0).JPEG(80, JPEGOptions{Progressive: true}).MemoryLimit(1 << 20).TempDir(dir).ToBytes()
	if err != nil || !bytes.Equal(got, want) {
		t.Errorf("progressive under a 1MB limit: err %v, same bytes %v", err, bytes.Equal(got, want))
	}
	if left := tempFiles(dir); left != 0 {
		t.Errorf("%d temp files left", left)
	}
	limit := int64(len(want) * 2 / 3)
	small, err := FromBytes(in).Resize(400, 0).JPEG(80, JPEGOptions{Progressive: true}).MaxFileSize(limit).ToBytes()
	if err != nil || int64(len(small)) > limit || !progressive(small) {
		t.Errorf("progressive with a size limit: err %v, %d bytes for a limit of %d", err, len(small), limit)
	}
	// The options apply to a size limit too: the same limit allows at least
	// as high a quality with them as without.
	plainSmall, err := FromBytes(in).Resize(400, 0).JPEG(80).MaxFileSize(limit).ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	qualityOf := func(file []byte, o JPEGOptions) int {
		for q := 80; q >= 1; q-- {
			if b, _ := FromBytes(in).Resize(400, 0).JPEG(q, o).ToBytes(); bytes.Equal(b, file) {
				return q
			}
		}
		t.Fatal("the file is not an encode at any quality")
		return 0
	}
	if qp, qn := qualityOf(small, JPEGOptions{Progressive: true}), qualityOf(plainSmall, JPEGOptions{}); qp < qn {
		t.Errorf("under the same size limit: quality %d with Progressive, %d without", qp, qn)
	} else {
		t.Logf("size limit %d bytes: quality %d with Progressive, %d without", limit, qp, qn)
	}
}

func tempFiles(dir string) int {
	names, _ := filepath.Glob(filepath.Join(dir, "*"))
	return len(names)
}

// TestPNGColors: PNG output reduced to a palette is a much smaller file,
// close to the original picture, with transparency kept.
func TestPNGColors(t *testing.T) {
	// Gradients with noise, as a photograph has: the kind of picture that
	// colour reduction is for. (A perfectly regular picture compresses so
	// well in full colour that dithering it can only make the file larger.)
	src := testutil.Images(613, 457)["rgba-opaque"]
	in := pngBytes(t, src)
	full, err := FromBytes(in).PNG().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	last := 0.0
	for _, colors := range []int{256, 64, 16, 2} {
		got, err := FromBytes(in).PNG(PNGOptions{Colors: colors}).ToBytes()
		if err != nil {
			t.Fatalf("%d colours: %v", colors, err)
		}
		m := decodePNG(t, got)
		pal, ok := m.(*image.Paletted)
		if !ok || len(pal.Palette) > colors || len(pal.Palette) < 2 {
			t.Fatalf("%d colours: decoded as %T with %d palette entries", colors, m, len(pal.Palette))
		}
		if m.Bounds().Size() != src.Bounds().Size() {
			t.Fatalf("%d colours: size %v", colors, m.Bounds().Size())
		}
		if len(got) >= len(full)*2/3 {
			t.Errorf("%d colours: %d bytes, the full-colour file is %d", colors, len(got), len(full))
		}
		// Fewer colours, further from the original; 256 are very close.
		d := meanDiff(m, src)
		if d < last {
			t.Errorf("%d colours are closer to the original (%.2f) than more colours (%.2f)", colors, d, last)
		}
		last = d
		t.Logf("%d colours: %d bytes (full colour %d), mean difference %.2f levels", colors, len(got), len(full), d)
		// (The noise in this picture alone is some 10 levels per channel.)
		if limit := map[int]float64{256: 12, 64: 20, 16: 32, 2: 80}[colors]; d > limit {
			t.Errorf("%d colours: mean difference %.2f levels", colors, d)
		}
		if _, _, _, a := m.At(10, 10).RGBA(); a != 0xffff {
			t.Errorf("%d colours: an opaque image got transparency", colors)
		}
	}
	// On a picture without noise 256 colours are close to the original.
	clean := photoLike(613, 457)
	for _, o := range []PNGOptions{{Colors: 256}, {Colors: 256, NoDither: true}} {
		got, err := FromBytes(pngBytes(t, clean)).PNG(o).ToBytes()
		if err != nil {
			t.Fatal(err)
		}
		if d := meanDiff(decodePNG(t, got), clean); d > 3 {
			t.Errorf("%+v on a clean picture: mean difference %.2f levels", o, d)
		}
	}
	// Dithering can be turned off, which gives another (more compressible) file.
	a, _ := FromBytes(in).PNG(PNGOptions{Colors: 16}).ToBytes()
	b, err := FromBytes(in).PNG(PNGOptions{Colors: 16, NoDither: true}).ToBytes()
	if err != nil || bytes.Equal(a, b) || len(b) >= len(a) {
		t.Errorf("NoDither: err %v, %d bytes against %d dithered", err, len(b), len(a))
	}
	// The compression level still applies.
	fast, _ := FromBytes(in).PNG(PNGOptions{Colors: 64, CompressionLevel: 1}).ToBytes()
	small, _ := FromBytes(in).PNG(PNGOptions{Colors: 64, CompressionLevel: 9}).ToBytes()
	if len(small) > len(fast) {
		t.Errorf("level 9 (%d bytes) is larger than level 1 (%d)", len(small), len(fast))
	}

	// Transparency: fully transparent stays so, opaque stays opaque, soft
	// edges keep levels in between.
	soft := image.NewNRGBA(image.Rect(0, 0, 200, 120))
	for y := 0; y < 120; y++ {
		for x := 0; x < 200; x++ {
			alpha := uint8(0)
			switch {
			case x >= 60 && x < 140:
				alpha = 255
			case x >= 40 && x < 60:
				alpha = uint8((x - 40) * 255 / 20)
			case x >= 140 && x < 160:
				alpha = uint8((160 - x) * 255 / 20)
			}
			soft.SetNRGBA(x, y, color.NRGBA{uint8(x), uint8(2 * y), 200, alpha})
		}
	}
	got, err := FromBytes(pngBytes(t, soft)).PNG(PNGOptions{Colors: 64}).ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	m := decodePNG(t, got)
	levels := map[uint32]bool{}
	for y := 0; y < 120; y++ {
		for x := 0; x < 200; x++ {
			_, _, _, a := m.At(x, y).RGBA()
			levels[a] = true
			switch {
			case (x < 40 || x >= 160) && a != 0:
				t.Fatalf("pixel (%d,%d) was transparent and now has alpha %#x", x, y, a)
			case x >= 60 && x < 140 && a != 0xffff:
				t.Fatalf("pixel (%d,%d) was opaque and now has alpha %#x", x, y, a)
			}
		}
	}
	if len(levels) < 4 {
		t.Errorf("only %d alpha levels are left of the soft edge", len(levels))
	}

	// An image with few colours keeps them exactly.
	few := image.NewRGBA(image.Rect(0, 0, 90, 60))
	for y := 0; y < 60; y++ {
		for x := 0; x < 90; x++ {
			few.SetRGBA(x, y, color.RGBA{uint8(x / 30 * 100), uint8(y / 30 * 200), 50, 255})
		}
	}
	got, err = FromBytes(pngBytes(t, few)).PNG(PNGOptions{Colors: 16}).ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	sameImage(t, "few colours", decodePNG(t, got), few)

	// After a chain, by file name, under a small memory limit: same file,
	// nothing left behind.
	dir := t.TempDir()
	want, _ := FromBytes(in).Resize(300, 0).PNG(PNGOptions{Colors: 32}).ToBytes()
	lean, err := FromBytes(in).Resize(300, 0).PNG(PNGOptions{Colors: 32}).MemoryLimit(4 << 20).TempDir(dir).ToBytes()
	if err != nil || !bytes.Equal(lean, want) {
		t.Errorf("under a 4MB limit: err %v, same bytes %v", err, bytes.Equal(lean, want))
	}
	if left := tempFiles(dir); left != 0 {
		t.Errorf("%d temp files left", left)
	}
	// With the decoder and the encoder on threads of their own: same file.
	threaded, err := FromBytes(in).Resize(300, 0).PNG(PNGOptions{Colors: 32}).Threads(3).ToBytes()
	if err != nil || !bytes.Equal(threaded, want) {
		t.Errorf("with 3 threads: err %v, same bytes %v", err, bytes.Equal(threaded, want))
	}
	for _, bad := range []int{1, 257, -4} {
		if _, err := FromBytes(in).PNG(PNGOptions{Colors: bad}).ToBytes(); err == nil {
			t.Errorf("%d colours accepted", bad)
		}
	}
}

// dqt returns the quantization tables of a JPEG file, by table number, in
// the zig-zag order they are stored in.
func dqt(t *testing.T, b []byte) map[int][]byte {
	tables := map[int][]byte{}
	for i := 2; i+4 <= len(b) && b[i] == 0xff; {
		marker, n := b[i+1], int(b[i+2])<<8|int(b[i+3])
		if marker == 0xda {
			break
		}
		if marker == 0xdb {
			for seg := b[i+4 : i+2+n]; len(seg) >= 65; seg = seg[65:] {
				if seg[0]>>4 != 0 {
					t.Fatal("16-bit quantization table")
				}
				tables[int(seg[0]&15)] = seg[1:65]
			}
		}
		i += 2 + n
	}
	return tables
}

// TestJPEGQuantTables: table 0 is the standard library's file, table 3
// stores N. Robidoux's table (mozjpeg's default) scaled by the quality,
// every table gives a readable file, and a table that does not exist is
// refused.
func TestJPEGQuantTables(t *testing.T) {
	src := photoLike(200, 120)
	std, err := FromImage(src).JPEG(75).ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	if t0, err := FromImage(src).JPEG(75, JPEGOptions{QuantTable: 0}).ToBytes(); err != nil || !bytes.Equal(t0, std) {
		t.Errorf("table 0 is not the default file: %v", err)
	}
	// Quality 50 scales the table by 100%, quality 75 by 50%; the values
	// are stored in zig-zag order and limited to 255.
	robidoux := []int{
		16, 16, 16, 16, 17, 16, 18, 20, 20, 18, 25, 27, 24, 27, 25, 37,
		34, 31, 31, 34, 37, 56, 40, 43, 40, 43, 40, 56, 85, 53, 62, 53,
		53, 62, 53, 85, 75, 91, 74, 69, 74, 91, 75, 135, 106, 94, 94, 106,
		135, 156, 131, 124, 131, 156, 189, 169, 169, 189, 238, 226, 238, 311, 311, 418,
	}
	for _, c := range []struct{ q, scale int }{{50, 100}, {75, 50}} {
		b, err := FromImage(src).JPEG(c.q, JPEGOptions{QuantTable: 3}).ToBytes()
		if err != nil {
			t.Fatal(err)
		}
		want := make([]byte, 64)
		for i, v := range robidoux {
			want[i] = byte(min((v*c.scale+50)/100, 255))
		}
		tables := dqt(t, b)
		if !bytes.Equal(tables[0], want) || !bytes.Equal(tables[1], want) {
			t.Errorf("table 3 at quality %d: luma %v, chroma %v, want %v", c.q, tables[0], tables[1], want)
		}
	}
	for n := range 9 {
		for _, o := range []JPEGOptions{{QuantTable: n}, {QuantTable: n, Progressive: true, Trellis: true}} {
			b, err := FromImage(src).JPEG(80, o).ToBytes()
			if err != nil {
				t.Fatalf("table %d: %v", n, err)
			}
			m, err := jpeg.Decode(bytes.NewReader(b))
			if err != nil {
				t.Fatalf("table %d %+v: %v", n, o, err)
			}
			if p, _, _ := psnrOf(src, m); p < 30 {
				t.Errorf("table %d %+v: PSNR %.1f", n, o, p)
			}
		}
	}
	for _, n := range []int{-1, 9} {
		if _, err := FromImage(src).JPEG(80, JPEGOptions{QuantTable: n}).ToBytes(); err == nil || errors.Is(err, ErrFormat) {
			t.Errorf("table %d: %v", n, err)
		}
	}
}
