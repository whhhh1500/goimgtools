package tests

import (
	"bytes"
	"encoding/binary"
	xwebp "golang.org/x/image/webp"
	"image"
	"image/color"
	stddraw "image/draw"
	"image/png"
	"testing"

	. "github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// turnRef applies an EXIF orientation (1 to 8) to m the obvious way.
func turnRef(m image.Image, orient int) *image.NRGBA64 {
	b := m.Bounds()
	w, h := b.Dx(), b.Dy()
	dw, dh := w, h
	if orient >= 5 {
		dw, dh = h, w
	}
	out := image.NewNRGBA64(image.Rect(0, 0, dw, dh))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var dx, dy int
			switch orient {
			case 1:
				dx, dy = x, y
			case 2:
				dx, dy = w-1-x, y
			case 3:
				dx, dy = w-1-x, h-1-y
			case 4:
				dx, dy = x, h-1-y
			case 5:
				dx, dy = y, x
			case 6:
				dx, dy = h-1-y, x
			case 7:
				dx, dy = h-1-y, w-1-x
			case 8:
				dx, dy = y, w-1-x
			}
			out.Set(dx, dy, m.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return out
}

func sameImage(t *testing.T, what string, got, want image.Image) {
	t.Helper()
	if got.Bounds().Size() != want.Bounds().Size() {
		t.Fatalf("%s: size %v, want %v", what, got.Bounds().Size(), want.Bounds().Size())
	}
	gb, wb := got.Bounds(), want.Bounds()
	for y := 0; y < wb.Dy(); y++ {
		for x := 0; x < wb.Dx(); x++ {
			g := color.NRGBA64Model.Convert(got.At(gb.Min.X+x, gb.Min.Y+y))
			w := color.NRGBA64Model.Convert(want.At(wb.Min.X+x, wb.Min.Y+y))
			if g != w {
				t.Fatalf("%s: pixel (%d,%d) = %v, want %v", what, x, y, g, w)
			}
		}
	}
}

func decodePNG(t *testing.T, b []byte) image.Image {
	t.Helper()
	m, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// exifWithOrientation is a minimal EXIF block: one directory with the
// orientation and one other entry.
func exifWithOrientation(bo binary.ByteOrder, orient int) []byte {
	b := make([]byte, 8+2+2*12+4)
	if bo == binary.BigEndian {
		copy(b, "MM")
	} else {
		copy(b, "II")
	}
	bo.PutUint16(b[2:], 42)
	bo.PutUint32(b[4:], 8)
	bo.PutUint16(b[8:], 2)
	e := b[10:]
	bo.PutUint16(e[0:], 0x0100) // ImageWidth, a LONG
	bo.PutUint16(e[2:], 4)
	bo.PutUint32(e[4:], 1)
	bo.PutUint32(e[8:], 640)
	e = e[12:]
	bo.PutUint16(e[0:], 0x0112) // Orientation, a SHORT
	bo.PutUint16(e[2:], 3)
	bo.PutUint32(e[4:], 1)
	bo.PutUint16(e[8:], uint16(orient))
	return b
}

// withAPP inserts APP segments right after the SOI marker of a JPEG.
func withAPP(jpg []byte, segs ...[]byte) []byte {
	out := append([]byte(nil), jpg[:2]...)
	for _, s := range segs {
		out = append(out, s...)
	}
	return append(out, jpg[2:]...)
}

func app(marker byte, payload []byte) []byte {
	n := len(payload) + 2
	return append([]byte{0xff, marker, byte(n >> 8), byte(n)}, payload...)
}

// TestRotateFlipFlop checks every turn against a pixel-by-pixel reference,
// for several pixel types. (internal/ops tests the temporary-file path.)
func TestRotateFlipFlop(t *testing.T) {
	imgs := testutil.Images(53, 37)
	turns := []struct {
		name   string
		apply  func(*Image) *Image
		orient int
	}{
		{"rotate0", func(m *Image) *Image { return m.Rotate(0) }, 1},
		{"flop", func(m *Image) *Image { return m.Flop() }, 2},
		{"rotate180", func(m *Image) *Image { return m.Rotate(180) }, 3},
		{"flip", func(m *Image) *Image { return m.Flip() }, 4},
		{"rotate90", func(m *Image) *Image { return m.Rotate(90) }, 6},
		{"rotate270", func(m *Image) *Image { return m.Rotate(-90) }, 8},
		{"rotate90+flop", func(m *Image) *Image { return m.Rotate(90).Flop() }, 5},
		{"rotate90+flip", func(m *Image) *Image { return m.Rotate(90).Flip() }, 7},
		{"flip+flop", func(m *Image) *Image { return m.Flip().Flop() }, 3},
		{"four quarter turns", func(m *Image) *Image { return m.Rotate(90).Rotate(90).Rotate(90).Rotate(90) }, 1},
	}
	for _, name := range []string{"nrgba", "rgba-opaque", "gray", "gray16", "paletted", "nrgba64"} {
		in := pngBytes(t, imgs[name])
		for _, turn := range turns {
			got, err := turn.apply(FromBytes(in)).PNG().ToBytes()
			if err != nil {
				t.Fatalf("%s %s: %v", name, turn.name, err)
			}
			sameImage(t, name+" "+turn.name, decodePNG(t, got), turnRef(imgs[name], turn.orient))
		}
	}
	// A turn after another operation, and a JPEG (planar strips) turned.
	in := pngBytes(t, imgs["nrgba"])
	got, err := FromBytes(in).Extract(3, 5, 40, 20).Rotate(90).PNG().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	sub := imgs["nrgba"].(*image.NRGBA).SubImage(image.Rect(3, 5, 43, 25))
	sameImage(t, "crop then rotate", decodePNG(t, got), turnRef(sub, 6))

	jp := jpegBytes(imgs["rgba-opaque"], 90)
	got, err = FromBytes(jp).Rotate(270).PNG().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	dec, _ := decodeJPEG(bytes.NewReader(jp))
	rgba := image.NewRGBA(dec.Bounds())
	stddraw.Draw(rgba, rgba.Bounds(), dec, image.Point{}, stddraw.Src)
	sameImage(t, "jpeg rotated", decodePNG(t, got), turnRef(rgba, 8))

	if _, err := FromBytes(in).Rotate(45).PNG().ToBytes(); err == nil {
		t.Error("Rotate(45) accepted")
	}
}

// TestAutoOrient: Rotate() follows the EXIF orientation of a JPEG, for both
// byte orders, and a Resize after it gives the size and (nearly) the pixels
// of turning first.
func TestAutoOrient(t *testing.T) {
	src := testutil.Images(120, 80)["rgba-opaque"]
	plain := jpegBytes(src, 92)
	dec, _ := decodeJPEG(bytes.NewReader(plain))
	rgba := image.NewRGBA(dec.Bounds())
	stddraw.Draw(rgba, rgba.Bounds(), dec, image.Point{}, stddraw.Src)
	for orient := 1; orient <= 8; orient++ {
		for _, bo := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
			exif := exifWithOrientation(bo, orient)
			in := withAPP(plain, app(0xe1, append([]byte("Exif\x00\x00"), exif...)))
			info, err := FromBytes(in).Metadata()
			if err != nil {
				t.Fatal(err)
			}
			if info.Orientation != orient || !info.HasExif || info.Width != 120 {
				t.Fatalf("orientation %d: Metadata = %+v", orient, info)
			}
			// What Rotate() leads to is OutputSize's to tell: the sides
			// change places from orientation 5 on.
			ww, wh := 120, 80
			if orient >= 5 {
				ww, wh = 80, 120
			}
			if w, h, err := FromBytes(in).Rotate().OutputSize(); err != nil || w != ww || h != wh {
				t.Fatalf("orientation %d: OutputSize after Rotate() = %dx%d, %v", orient, w, h, err)
			}
			if info, err := FromBytes(in).Rotate().Metadata(); err != nil || info.Width != 120 || info.Height != 80 {
				t.Fatalf("orientation %d: Metadata after Rotate() = %+v, %v", orient, info, err)
			}
			got, err := FromBytes(in).Rotate().PNG().ToBytes()
			if err != nil {
				t.Fatal(err)
			}
			want := turnRef(rgba, orient)
			sameImage(t, "auto-orient", decodePNG(t, got), want)

			// Without Rotate() the pixels stay as stored.
			got, _ = FromBytes(in).PNG().ToBytes()
			sameImage(t, "no rotate", decodePNG(t, got), rgba)

			// Resize after Rotate(): the box applies to the upright image.
			got, err = FromBytes(in).Rotate().Resize(60, 0).PNG().ToBytes()
			if err != nil {
				t.Fatal(err)
			}
			wantW, wantH := 60, 40
			if orient >= 5 {
				wantH = 90
			}
			small := decodePNG(t, got)
			if small.Bounds().Dx() != wantW || small.Bounds().Dy() != wantH {
				t.Fatalf("orientation %d: resized to %v, want %dx%d", orient, small.Bounds().Size(), wantW, wantH)
			}
			ref := scaled(want, wantW, wantH)
			if d := meanDiff(small, ref); d > 0.6 {
				t.Errorf("orientation %d: resize after rotate differs from rotate then resize by %.2f levels", orient, d)
			}
			// With shrink-on-load in front as well.
			got, err = FromBytes(in).Rotate().Resize(30, 0, ResizeOptions{FastShrinkOnLoad: true}).PNG().ToBytes()
			if err != nil {
				t.Fatal(err)
			}
			tiny := decodePNG(t, got)
			wantH = 20
			if orient >= 5 {
				wantH = 45
			}
			if tiny.Bounds().Dx() != 30 || tiny.Bounds().Dy() != wantH {
				t.Fatalf("orientation %d: fast shrink gave %v", orient, tiny.Bounds().Size())
			}
		}
	}
}

func meanDiff(a, b image.Image) float64 {
	var sum, n int
	ab, bb := a.Bounds(), b.Bounds()
	for y := 0; y < ab.Dy(); y++ {
		for x := 0; x < ab.Dx(); x++ {
			r1, g1, b1, _ := a.At(ab.Min.X+x, ab.Min.Y+y).RGBA()
			r2, g2, b2, _ := b.At(bb.Min.X+x, bb.Min.Y+y).RGBA()
			for _, d := range []int{int(r1>>8) - int(r2>>8), int(g1>>8) - int(g2>>8), int(b1>>8) - int(b2>>8)} {
				sum += max(d, -d)
				n++
			}
		}
	}
	return float64(sum) / float64(n)
}

// TestMetadataKept: the ICC profile travels to the output by default and
// the EXIF block only on request, between JPEG, PNG and WebP in every
// direction; TIFF carries the profile.
func TestMetadataKept(t *testing.T) {
	src := testutil.Images(64, 48)["rgba-opaque"]
	exif := exifWithOrientation(binary.LittleEndian, 6)
	// A profile larger than one JPEG segment holds, to exercise the split.
	icc := make([]byte, 150000)
	for i := range icc {
		icc[i] = byte(i*7 + i/251)
	}
	var segs [][]byte
	segs = append(segs, app(0xe1, append([]byte("Exif\x00\x00"), exif...)))
	const part = 65000
	total := (len(icc) + part - 1) / part
	for i := 0; i < total; i++ {
		p := append([]byte("ICC_PROFILE\x00"), byte(i+1), byte(total))
		segs = append(segs, app(0xe2, append(p, icc[i*part:min((i+1)*part, len(icc))]...)))
	}
	jp := withAPP(jpegBytes(src, 90), segs...)

	info, err := FromBytes(jp).Metadata()
	if err != nil || !info.HasProfile || !info.HasExif || info.Orientation != 6 {
		t.Fatalf("Metadata = %+v, %v", info, err)
	}
	read := func(b []byte) (string, bool, bool, int, []byte) {
		t.Helper()
		m := FromBytes(b)
		info, err := m.Metadata()
		if err != nil {
			t.Fatal(err)
		}
		src, err := m.Stream(nil)
		if err != nil {
			t.Fatal(err)
		}
		icc := pipeline.MetaOf(src).ICC
		src.Close()
		return info.Format, info.HasProfile, info.HasExif, info.Orientation, icc
	}
	for _, out := range []string{"jpeg", "png", "webp", "webp lossless", "tiff"} {
		enc := func(m *Image) []byte {
			t.Helper()
			switch out {
			case "jpeg":
				m.JPEG(90)
			case "png":
				m.PNG()
			case "webp":
				m.WebP()
			case "webp lossless":
				m.WebP(WebPOptions{Lossless: true})
			case "tiff":
				m.TIFF()
			}
			b, err := m.ToBytes()
			if err != nil {
				t.Fatal(err)
			}
			return b
		}
		if out == "tiff" {
			// TIFF keeps its EXIF in a directory of its own, which is not
			// written: the profile only.
			a := enc(FromBytes(jp).KeepExif())
			if _, prof, ex, _, gotICC := read(a); !prof || ex || !bytes.Equal(gotICC, icc) {
				t.Errorf("tiff: profile %v (intact %v), exif %v", prof, bytes.Equal(gotICC, icc), ex)
			}
			if _, prof, _, _, _ := read(enc(FromBytes(jp).StripICC())); prof {
				t.Error("tiff StripICC: profile kept")
			}
			if _, prof, _, _, gotICC := read(enc(FromBytes(a))); !prof || !bytes.Equal(gotICC, icc) {
				t.Error("tiff second generation: profile lost")
			}
			continue
		}
		// Default: profile kept, EXIF dropped.
		a := enc(FromBytes(jp))
		if _, prof, ex, _, gotICC := read(a); !prof || ex || !bytes.Equal(gotICC, icc) {
			t.Errorf("%s default: profile %v (intact %v), exif %v", out, prof, bytes.Equal(gotICC, icc), ex)
		}
		// Both kept; the orientation is still what the camera wrote.
		b := enc(FromBytes(jp).KeepExif())
		if _, prof, ex, orient, _ := read(b); !prof || !ex || orient != 6 {
			t.Errorf("%s KeepExif: profile %v exif %v orientation %d", out, prof, ex, orient)
		}
		// Turned upright: the copied EXIF says so, so viewers do not turn it again.
		c := enc(FromBytes(jp).Rotate().KeepExif())
		if _, _, ex, orient, _ := read(c); !ex || orient != 1 {
			t.Errorf("%s Rotate+KeepExif: exif %v orientation %d", out, ex, orient)
		}
		// Neither.
		d := enc(FromBytes(jp).StripICC())
		if _, prof, ex, _, _ := read(d); prof || ex {
			t.Errorf("%s StripICC: profile %v exif %v", out, prof, ex)
		}
		// A second generation keeps what the first kept (PNG -> JPEG and back).
		e := enc(FromBytes(b).KeepExif())
		if _, prof, ex, orient, gotICC := read(e); !prof || !ex || orient != 6 || !bytes.Equal(gotICC, icc) {
			t.Errorf("%s second generation: profile %v exif %v orientation %d", out, prof, ex, orient)
		}
		// The pixels are those of the same chain without metadata.
		if out == "png" {
			sameImage(t, "pixels with metadata", decodePNG(t, b), decodePNG(t, d))
		}
	}
	// Without metadata in the input the output is byte for byte what it was.
	plain := jpegBytes(src, 90)
	x, _ := FromBytes(plain).KeepExif().JPEG(90).ToBytes()
	y, _ := FromBytes(plain).StripICC().JPEG(90).ToBytes()
	if !bytes.Equal(x, y) {
		t.Error("options changed the output of an image without metadata")
	}
	// A WebP without metadata stays in the simple format; one with it is
	// read by another decoder (x/image) all the same, and its pixels are
	// those of the file without.
	for _, lossless := range []bool{false, true} {
		o := WebPOptions{Lossless: lossless}
		bare, _ := FromBytes(plain).WebP(o).ToBytes()
		if string(bare[12:16]) == "VP8X" {
			t.Errorf("lossless %v: a WebP without metadata got the extended format", lossless)
		}
		full, _ := FromBytes(jp).KeepExif().WebP(o).ToBytes()
		if string(full[12:16]) != "VP8X" || full[20]&0x28 != 0x28 {
			t.Errorf("lossless %v: header %q flags %#x", lossless, full[12:16], full[20])
		}
		a, err1 := xwebp.Decode(bytes.NewReader(bare))
		b, err2 := xwebp.Decode(bytes.NewReader(full))
		if err1 != nil || err2 != nil {
			t.Fatalf("lossless %v: x/image cannot read the files: %v, %v", lossless, err1, err2)
		}
		sameImage(t, "WebP with and without metadata", b, a)
	}
	// Turned upright from the EXIF of a WebP.
	wp, _ := FromBytes(jp).KeepExif().WebP().ToBytes()
	up, err := FromBytes(wp).Rotate().PNG().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	if got := decodePNG(t, up).Bounds().Size(); got != image.Pt(48, 64) {
		t.Errorf("Rotate on a WebP with orientation 6: %v, want 48x64", got)
	}
}

// TestContainAndKernels: FitContain pads to the exact box; every kernel
// produces the size asked for and a plausible picture.
func TestContainAndKernels(t *testing.T) {
	src := testutil.Images(200, 100)["nrgba"]
	in := pngBytes(t, src)
	red := color.RGBA{200, 10, 20, 255}
	got, err := FromBytes(in).Resize(120, 120, ResizeOptions{Fit: FitContain, Background: red}).PNG().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	m := decodePNG(t, got)
	if m.Bounds().Size() != image.Pt(120, 120) {
		t.Fatalf("contain: size %v", m.Bounds().Size())
	}
	inner, err := FromBytes(in).Resize(120, 120, ResizeOptions{Fit: FitInside}).PNG().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	im := decodePNG(t, inner) // 120x60, centred with 30 rows above and below
	for y := 0; y < 120; y++ {
		for x := 0; x < 120; x++ {
			var want color.Color = red
			if y >= 30 && y < 90 {
				want = im.At(x, y-30)
			}
			if color.NRGBAModel.Convert(m.At(x, y)) != color.NRGBAModel.Convert(want) {
				t.Fatalf("contain: pixel (%d,%d) = %v, want %v", x, y, m.At(x, y), want)
			}
		}
	}
	// Bars left and right, default black, odd remainder.
	got, err = FromBytes(in).Resize(51, 20, ResizeOptions{Fit: FitContain}).Flatten(color.White).PNG().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	m = decodePNG(t, got)
	if m.Bounds().Size() != image.Pt(51, 20) {
		t.Fatalf("contain: size %v", m.Bounds().Size())
	}
	if r, g, b, a := m.At(0, 10).RGBA(); r != 0 || g != 0 || b != 0 || a != 0xffff {
		t.Errorf("contain: left bar is %v, want black", m.At(0, 10))
	}
	// Nothing to pad: same as FitInside.
	a, _ := FromBytes(in).Resize(100, 50, ResizeOptions{Fit: FitContain}).PNG().ToBytes()
	b, _ := FromBytes(in).Resize(100, 50, ResizeOptions{Fit: FitInside}).PNG().ToBytes()
	if !bytes.Equal(a, b) {
		t.Error("contain without bars differs from inside")
	}

	// Kernels, on a smooth image where every sensible filter agrees closely.
	smooth := image.NewRGBA(image.Rect(0, 0, 300, 200))
	for y := 0; y < 200; y++ {
		for x := 0; x < 300; x++ {
			smooth.SetRGBA(x, y, color.RGBA{uint8(x * 255 / 300), uint8(y * 255 / 200), uint8((x + y) / 2), 255})
		}
	}
	sin := pngBytes(t, smooth)
	for _, size := range [][2]int{{75, 50}, {450, 300}} {
		ref := scaled(smooth, size[0], size[1])
		for k := KernelCubic; k <= KernelLanczos3; k++ {
			got, err := FromBytes(sin).Resize(size[0], size[1], ResizeOptions{Fit: FitFill, Kernel: k}).PNG().ToBytes()
			if err != nil {
				t.Fatalf("kernel %d: %v", k, err)
			}
			m := decodePNG(t, got)
			if m.Bounds().Size() != image.Pt(size[0], size[1]) {
				t.Fatalf("kernel %d: size %v", k, m.Bounds().Size())
			}
			d := meanDiff(m, ref)
			if k == KernelCubic && d != 0 {
				t.Errorf("default kernel differs from x/image by %.3f", d)
			}
			if d > 1.5 {
				t.Errorf("kernel %d to %v: mean difference %.2f levels from Catmull-Rom", k, size, d)
			}
		}
	}
	if _, err := FromBytes(sin).Resize(10, 10, ResizeOptions{Kernel: 99}).PNG().ToBytes(); err == nil {
		t.Error("unknown kernel accepted")
	}
}

// TestMKSKernels: KernelMKS2013 and KernelMKS2021 are the kernels of
// sharp's mks2013 and mks2021: of all the kernels, each comes closest to
// sharp's shrink with it. (The rest of the resize is x/image's, not
// libvips', so the pixels are not sharp's to the byte.)
func TestMKSKernels(t *testing.T) {
	rgb := sourcePNG(t, "pillow/source.png")
	all := []Kernel{KernelCubic, KernelNearest, KernelLinear, KernelMitchell, KernelLanczos2, KernelLanczos3, KernelMKS2013, KernelMKS2021}
	for name, k := range map[string]Kernel{"mks2013": KernelMKS2013, "mks2021": KernelMKS2021} {
		want := readPNG(t, "sharp/resize-"+name+"-60.png")
		best, bestMean := Kernel(-1), 1e9
		for _, o := range all {
			got := decodePNG(t, must(FromBytes(rgb).Resize(60, 0, ResizeOptions{Kernel: o}).PNG().ToBytes()))
			if _, mean, _ := channelDiff(t, got, want); mean < bestMean {
				best, bestMean = o, mean
			}
		}
		if best != k || bestMean > 0.85 {
			t.Errorf("sharp's %s is closest to kernel %d (mean %.3f), want %d", name, best, bestMean, k)
		}
	}
	if _, err := FromBytes(rgb).Do("resize", "60", "0", "inside", "mks2021").PNG().ToBytes(); err != nil {
		t.Error(err)
	}
}
