package tests

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	. "github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/inspect"
	"github.com/whhhh1500/goimgtools/internal/testutil"
)

// TestStats: the colours of an image, held against what a plain count
// over its pixels gives.
func TestStats(t *testing.T) {
	// One colour.
	solid := image.NewNRGBA(image.Rect(0, 0, 40, 30))
	for i := 0; i < len(solid.Pix); i += 4 {
		solid.Pix[i], solid.Pix[i+1], solid.Pix[i+2], solid.Pix[i+3] = 200, 100, 50, 255
	}
	st, err := inspect.Stats(FromImage(solid))
	want := color.NRGBA{200, 100, 50, 255}
	if err != nil || st.Average != want || st.Colors != 1 || st.Gray || !st.Opaque || st.Transparent != 0 ||
		len(st.Dominant) != 1 || st.Dominant[0].Color != want || st.Dominant[0].Share != 1 || st.Width != 40 || st.Height != 30 {
		t.Errorf("one colour: %+v, %v", st, err)
	}
	if b := (299*200.0 + 587*100 + 114*50) / 1000 / 255; math.Abs(st.Brightness-b) > 1e-9 {
		t.Errorf("brightness %v, want %v", st.Brightness, b)
	}
	if s := st.String(); s != "average #c86432, brightness 49%, 1 colours; mostly #c86432 100%; entropy 0.00 bits, sharpness 0.00" {
		t.Errorf("in words: %q", s)
	}

	// Three quarters dark blue, one quarter yellow, one stray pixel.
	two := image.NewNRGBA(image.Rect(0, 0, 80, 40))
	for y := 0; y < 40; y++ {
		for x := 0; x < 80; x++ {
			c := color.NRGBA{10, 20, 120, 255}
			if x >= 60 {
				c = color.NRGBA{240, 220, 30, 255}
			}
			two.SetNRGBA(x, y, c)
		}
	}
	two.SetNRGBA(3, 3, color.NRGBA{0, 255, 0, 255})
	st, err = inspect.Stats(FromBytes(pngBytes(t, two)))
	if err != nil || len(st.Dominant) < 2 || st.Dominant[0].Color != (color.NRGBA{10, 20, 120, 255}) || st.Dominant[1].Color != (color.NRGBA{240, 220, 30, 255}) ||
		math.Abs(st.Dominant[0].Share-0.75) > 0.001 || math.Abs(st.Dominant[1].Share-0.25) > 0.001 || st.Colors != 3 {
		t.Errorf("two colours: %+v, %v", st, err)
	}

	// A black and white picture stored in colour is greyscale; a colour
	// picture is not.
	grey := testutil.Images(120, 90)["gray"]
	asColour := image.NewRGBA(grey.Bounds())
	for y := 0; y < 90; y++ {
		for x := 0; x < 120; x++ {
			asColour.Set(x, y, grey.At(x, y))
		}
	}
	for name, file := range map[string][]byte{"png": pngBytes(t, asColour), "jpeg": jpegBytes(asColour, 85)} {
		info, _ := FromBytes(file).Metadata()
		st, err := inspect.Stats(FromBytes(file))
		if err != nil || !st.Gray || info.Color == "gray" {
			t.Errorf("grey picture stored as %s %s: Gray=%v, %v", info.Color, name, st.Gray, err)
		}
	}
	if st, err := inspect.Stats(FromBytes(jpegBytes(photoLike(120, 90), 85))); err != nil || st.Gray || st.Colors != 0 {
		t.Errorf("colour photo: %+v, %v", st, err)
	}

	// A photograph: the average is the mean of its pixels.
	photo := photoLike(311, 207)
	var sr, sg, sb float64
	for i := 0; i < len(photo.Pix); i += 4 {
		sr, sg, sb = sr+float64(photo.Pix[i]), sg+float64(photo.Pix[i+1]), sb+float64(photo.Pix[i+2])
	}
	n := float64(311 * 207)
	st, err = inspect.Stats(FromImage(photo).MemoryLimit(1 << 20))
	if err != nil || math.Abs(float64(st.Average.R)-sr/n) > 0.5001 || math.Abs(float64(st.Average.G)-sg/n) > 0.5001 || math.Abs(float64(st.Average.B)-sb/n) > 0.5001 || st.Average.A != 255 {
		t.Errorf("photo: average %v, the mean is %.2f %.2f %.2f (%v)", st.Average, sr/n, sg/n, sb/n, err)
	}
	total := 0.0
	for _, d := range st.Dominant {
		total += d.Share
	}
	if len(st.Dominant) == 0 || len(st.Dominant) > 5 || total > 1.0001 || st.Dominant[0].Share < st.Dominant[len(st.Dominant)-1].Share {
		t.Errorf("photo: dominant colours %+v", st.Dominant)
	}

	// Transparency: half of the picture clear, the rest half see-through.
	half := image.NewNRGBA(image.Rect(0, 0, 20, 10))
	for y := 0; y < 10; y++ {
		for x := 10; x < 20; x++ {
			half.SetNRGBA(x, y, color.NRGBA{255, 0, 0, 128})
		}
	}
	st, err = inspect.Stats(FromImage(half))
	if err != nil || st.Opaque || st.Transparent != 0.5 || st.Average != (color.NRGBA{255, 0, 0, 64}) || st.Colors != 2 || st.Gray {
		t.Errorf("half transparent: %+v, %v", st, err)
	}
	st, err = inspect.Stats(FromImage(image.NewNRGBA(image.Rect(0, 0, 5, 5))))
	if err != nil || st.Transparent != 1 || st.Gray || len(st.Dominant) != 0 || st.Average != (color.NRGBA{}) {
		t.Errorf("fully transparent: %+v, %v", st, err)
	}

	// The number of colours, up to what a palette holds.
	many := image.NewNRGBA(image.Rect(0, 0, 257, 1))
	for x := 0; x < 257; x++ {
		many.SetNRGBA(x, 0, color.NRGBA{uint8(x), uint8(x >> 8), 7, 255})
	}
	if st, _ := inspect.Stats(FromImage(many.SubImage(image.Rect(0, 0, 256, 1)))); st.Colors != 256 {
		t.Errorf("256 colours counted as %d", st.Colors)
	}
	if st, _ := inspect.Stats(FromImage(many)); st.Colors != 0 {
		t.Errorf("257 colours counted as %d", st.Colors)
	}

	if _, err := inspect.Stats(FromBytes([]byte("nothing"))); !errors.Is(err, ErrFormat) {
		t.Errorf("no image: %v", err)
	}
	if _, err := inspect.Stats(nil); err == nil {
		t.Error("Stats(nil) accepted")
	}
}

// TestHash: the same picture in other sizes, formats and qualities keeps
// its fingerprint, give or take a few bits; another picture does not. The
// pictures are real ones: the test images of Go and golang.org/x/image.
func TestHash(t *testing.T) {
	files := realFiles(t)
	orig := files["png"] // a frame of a video, 150x103
	base, err := inspect.Hash(FromBytes(orig))
	if err != nil {
		t.Fatal(err)
	}
	// The same frame as Go ships it in five other formats.
	for _, name := range []string{"jpeg", "jpeg-progressive", "gif", "bmp", "tiff"} {
		h, err := inspect.Hash(FromBytes(files[name]))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if d := base.Distance(h); d > 6 {
			t.Errorf("the same picture as %s: %d bits away (%v, %v)", name, d, base, h)
		}
	}
	// And as this library makes it smaller, larger, worse, and marked.
	for name, m := range map[string]*Image{
		"jpeg 40":       FromBytes(orig).JPEG(JPEGOptions{Quality: 40}),
		"half size":     FromBytes(orig).Resize(75, 0).PNG(),
		"a fifth":       FromBytes(orig).Resize(30, 0).JPEG(JPEGOptions{Quality: 80}),
		"four times":    FromBytes(orig).Resize(600, 0).JPEG(JPEGOptions{Quality: 85}),
		"squeezed":      FromBytes(orig).Resize(150, 60, ResizeOptions{Fit: FitFill}).PNG(),
		"webp":          FromBytes(orig).WebP(WebPOptions{Quality: 60}),
		"256 colours":   FromBytes(orig).PNG(PNGOptions{Colors: 256}),
		"a small label": FromBytes(orig).Composite(Text("(c)", TextOptions{Size: 9, Color: color.White}), CompositeOptions{Gravity: SouthEast, Left: -2, Top: -2}).JPEG(JPEGOptions{Quality: 85}),
	} {
		file, err := m.ToBytes()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		h, err := inspect.Hash(FromBytes(file))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if d := base.Distance(h); d > 6 {
			t.Errorf("%s: %d bits from the original (%v, %v)", name, d, base, h)
		}
	}
	// Other pictures, and this one mirrored and upside down.
	other, err := os.ReadFile(filepath.Join(testutil.XImageDir(), "testdata", "go-turns-two-280x360.jpeg"))
	if err != nil {
		t.Fatal(err)
	}
	for name, m := range map[string]*Image{
		"another photo":  FromBytes(other),
		"a colour chart": FromBytes(files["webp-lossy"]),
		"mirrored":       FromBytes(orig).Flop(),
		"upside down":    FromBytes(orig).Flip(),
	} {
		h, err := inspect.Hash(m)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if d := base.Distance(h); d < 14 {
			t.Errorf("%s: only %d bits from the picture (%v, %v)", name, d, base, h)
		}
	}
	// A picture stored on its side with an EXIF orientation is the picture
	// as it is shown.
	turned, err := FromBytes(orig).Rotate(270).JPEG(JPEGOptions{Quality: 90}).ToBytes() // what a camera held upright stores
	if err != nil {
		t.Fatal(err)
	}
	withExif := withAPP(turned, app(0xe1, append([]byte("Exif\x00\x00"), exifWithOrientation(binary.BigEndian, 6)...)))
	if h, err := inspect.Hash(FromBytes(withExif)); err != nil || base.Distance(h) > 6 {
		t.Errorf("stored on its side: %d bits away (%v)", base.Distance(h), err)
	}
	if h, _ := inspect.Hash(FromBytes(turned)); base.Distance(h) < 14 {
		t.Errorf("on its side without the orientation: only %d bits away", base.Distance(h))
	}

	// The same every time, and through its text form.
	again, _ := inspect.Hash(FromBytes(orig))
	back, err := inspect.ParseFingerprint(base.String())
	if again != base || err != nil || back != base || len(base.String()) != 16 {
		t.Errorf("fingerprint %v, again %v, parsed %v (%v)", base, again, back, err)
	}
	if base.Distance(base) != 0 || base.Distance(^base) != 64 || inspect.Fingerprint(0b1011).Distance(0b0001) != 2 {
		t.Error("Distance miscounts")
	}
	for _, bad := range []string{"", "123", "zzzzzzzzzzzzzzzz", "00000000000000000"} {
		if _, err := inspect.ParseFingerprint(bad); err == nil {
			t.Errorf("ParseFingerprint(%q) accepted", bad)
		}
	}
	// Small images and transparent ones have a fingerprint too.
	for name, m := range map[string]image.Image{"1x1": image.NewGray(image.Rect(0, 0, 1, 1)), "clear": image.NewNRGBA(image.Rect(0, 0, 30, 30))} {
		if _, err := inspect.Hash(FromImage(m)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := inspect.Hash(FromBytes([]byte("nothing"))); !errors.Is(err, ErrFormat) {
		t.Errorf("no image: %v", err)
	}
	if _, err := inspect.Hash(nil); err == nil {
		t.Error("Hash(nil) accepted")
	}
}

func TestMirroredHelper(t *testing.T) {
	m := image.NewRGBA(image.Rect(0, 0, 2, 1))
	m.SetRGBA(0, 0, color.RGBA{1, 2, 3, 255})
	if got := mirrored(m).RGBAAt(1, 0); got != (color.RGBA{1, 2, 3, 255}) {
		t.Errorf("mirrored: %v", got)
	}
}

func mirrored(m *image.RGBA) *image.RGBA {
	b := m.Bounds()
	out := image.NewRGBA(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			out.SetRGBA(x, y, m.RGBAAt(b.Max.X-1-(x-b.Min.X), y))
		}
	}
	return out
}

// TestExif: the fields of EXIF blocks written by another program (Pillow)
// are read as that program reads them back.
func TestExif(t *testing.T) {
	jp, err := os.ReadFile(filepath.Join("testdata", "exif-pillow.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	e, ok, err := inspect.ReadExif(FromBytes(jp))
	if err != nil || !ok {
		t.Fatalf("jpeg: %v, %v", ok, err)
	}
	near := func(a, b float64) bool { return math.Abs(a-b) < 1e-9 }
	if e.Make != "TestCam Corp" || e.Model != "TestCam X100" || e.Lens != "Test 35mm F2.8" || e.Software != "Pillow test writer" ||
		e.Artist != "A. Photographer" || e.Copyright != "(c) 2026 Example" || e.Orientation != 6 || e.ISO != 400 ||
		!near(e.Exposure, 1.0/250) || !near(e.Aperture, 2.8) || !near(e.FocalLength, 35) {
		t.Errorf("jpeg: %+v", e)
	}
	if !e.HasTaken || !e.Taken.Equal(time.Date(2026, 5, 17, 1, 30, 15, 0, time.UTC)) || e.Taken.Format("15:04 -07:00") != "09:30 +08:00" {
		t.Errorf("jpeg: taken %v", e.Taken)
	}
	if !e.HasGPS || !near(e.Latitude, 31+13.0/60+48.12/3600) || !near(e.Longitude, 121+28.0/60+29.88/3600) || !e.HasAltitude || !near(e.Altitude, 12.5) {
		t.Errorf("jpeg: position %v %v, altitude %v", e.Latitude, e.Longitude, e.Altitude)
	}
	if s := e.String(); s != "TestCam Corp TestCam X100, Test 35mm F2.8, taken 2026-05-17 09:30:15, 1/250 s, f/2.8, ISO 400, 35 mm, at 31.230033, 121.474967, 12 m, orientation 6, written by Pillow test writer, by A. Photographer, (c) 2026 Example" {
		t.Errorf("in words: %q", s)
	}

	pn, err := os.ReadFile(filepath.Join("testdata", "exif-pillow.png"))
	if err != nil {
		t.Fatal(err)
	}
	e, ok, err = inspect.ReadExif(FromBytes(pn))
	if err != nil || !ok {
		t.Fatalf("png: %v, %v", ok, err)
	}
	if e.Make != "Canon" || e.Model != "Canon EOS Test" || e.Orientation != 0 || !near(e.Exposure, 2) || e.ISO != 0 || e.Lens != "" ||
		!e.HasTaken || !e.Taken.Equal(time.Date(2025, 12, 31, 23, 59, 58, 0, time.UTC)) ||
		!e.HasGPS || !near(e.Latitude, -(33+51.0/60+25.0/3600)) || !near(e.Longitude, -(70+40.0/60)) || !e.HasAltitude || !near(e.Altitude, -4) {
		t.Errorf("png: %+v", e)
	}
	if s := e.String(); s != "Canon EOS Test, taken 2025-12-31 23:59:58, 2 s, at -33.856944, -70.666667, -4 m" {
		t.Errorf("in words: %q", s)
	}

	// Carried into a new file, it reads the same; after Rotate() it says
	// the picture is upright.
	kept, err := FromBytes(jp).KeepExif().Resize(12, 0).JPEG(JPEGOptions{Quality: 80}).ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	first, _, _ := inspect.ReadExif(FromBytes(jp))
	again, ok, err := inspect.ReadExif(FromBytes(kept))
	sameTime := again.Taken.Equal(first.Taken)
	again.Taken = first.Taken // equal times may differ in how their zone is held
	if err != nil || !ok || !sameTime || again != first {
		t.Errorf("after KeepExif: %+v, %v, %v", again, ok, err)
	}
	upright, _ := FromBytes(jp).AutoOrient().KeepExif().JPEG(JPEGOptions{Quality: 80}).ToBytes()
	if again, ok, _ := inspect.ReadExif(FromBytes(upright)); !ok || again.Orientation != 1 || again.Model != first.Model {
		t.Errorf("after Rotate: %+v", again)
	}
	// By default the block is dropped, position and all.
	plain, _ := FromBytes(jp).JPEG(JPEGOptions{Quality: 80}).ToBytes()
	if _, ok, err := inspect.ReadExif(FromBytes(plain)); ok || err != nil {
		t.Errorf("a file written without KeepExif has EXIF: %v, %v", ok, err)
	}
	if _, ok, err := inspect.ReadExif(FromBytes(pngBytes(t, testutil.Images(8, 8)["gray"]))); ok || err != nil {
		t.Errorf("an image without EXIF: %v, %v", ok, err)
	}

	// The plain block, in either byte order, with or without the prefix.
	for _, bo := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
		block := exifWithOrientation(bo, 8)
		for _, data := range [][]byte{block, append([]byte("Exif\x00\x00"), block...)} {
			if e, err := inspect.ParseExif(data); err != nil || e.Orientation != 8 || e.HasGPS || e.HasTaken {
				t.Errorf("%v: %+v, %v", bo, e, err)
			}
		}
	}
	// What is not EXIF is refused; what is cut short keeps what it still has.
	for _, bad := range [][]byte{nil, []byte("II"), []byte("XX*\x00\x08\x00\x00\x00\x00\x00"), []byte("II+\x00\x08\x00\x00\x00\x00\x00"),
		[]byte("II*\x00\xff\xff\xff\x7f"), []byte("MM\x00*\x00\x00\x00\x08\xff\xff")} {
		if _, err := inspect.ParseExif(bad); err == nil {
			t.Errorf("ParseExif(%q) accepted", bad)
		}
	}
	block := jp[bytes.Index(jp, []byte("Exif\x00\x00")):]
	for cut := 0; cut < 600 && cut < len(block); cut += 7 {
		e, err := inspect.ParseExif(block[:cut]) // must not panic; fields come and go
		if err == nil && cut < 14 {
			t.Errorf("a block of %d bytes was read: %+v", cut, e)
		}
	}
	if _, _, err := inspect.ReadExif(nil); err == nil {
		t.Error("ReadExif(nil) accepted")
	}
}

// TestStatsAsSharp: the channels, entropy and sharpness Stats reports are
// sharp 0.35's stats() of the same files: the levels, their positions and
// sums exactly, the rest to the last digits.
func TestStatsAsSharp(t *testing.T) {
	type ch = inspect.ChannelStats
	cs := func(lo, hi uint8, loAt, hiAt image.Point, sum, sq uint64, mean, sd float64) ch {
		return ch{Min: lo, Max: hi, MinAt: loAt, MaxAt: hiAt, Sum: sum, SquaresSum: sq, Mean: mean, Stdev: sd}
	}
	rgb := []ch{
		cs(0, 255, image.Pt(118, 44), image.Pt(127, 13), 2236595, 404011459, 144.76343042071198, 72.06590288127651),
		cs(0, 255, image.Pt(94, 0), image.Pt(22, 18), 1460540, 241015762, 94.53333333333333, 81.6309125640135),
		cs(0, 255, image.Pt(5, 0), image.Pt(118, 55), 1085069, 207134889, 70.23100323624595, 92.05945718127897),
	}
	opaque := cs(255, 255, image.Pt(0, 0), image.Pt(0, 0), 255*150*103, 255*255*150*103, 255, 0)
	grey := cs(0, 255, image.Pt(117, 43), image.Pt(123, 54), 1649882, 265929532, 106.78847896440129, 76.21590504677833)
	for _, c := range []struct {
		file               string
		channels           []ch
		entropy, sharpness float64
	}{
		{"pillow/source.png", append(rgb[:3:3], opaque), 7.273467719438486, 6.879013640588321},
		{"pillow/median-rgba-source.png", append(rgb[:3:3], cs(0, 255, image.Pt(0, 0), image.Pt(37, 0), 2192708, 421257648, 141.92284789644012, 84.40517487020414)),
			7.273467719438486, 6.879013640588321},
		{"pillow/gray.png", []ch{grey, grey, grey, opaque}, 7.284216635860503, 7.045214864725048},
		{"png/anim-ops.png", []ch{
			cs(0, 255, image.Pt(0, 0), image.Pt(6, 101), 997490, 184816470, 51.95260416666667, 83.2294750772279),
			cs(0, 255, image.Pt(0, 31), image.Pt(6, 101), 1114715, 166546125, 58.05807291666667, 72.8272871474193),
			cs(0, 255, image.Pt(0, 31), image.Pt(0, 0), 868920, 133638320, 45.25625, 70.08892109122723),
			cs(0, 255, image.Pt(0, 31), image.Pt(0, 0), 1863962, 456893590, 97.08135416666667, 119.88536352169119),
		}, 2.739074549637735, 6.78826717313007},
	} {
		st, err := inspect.Stats(FromBytes(sourcePNG(t, c.file)))
		if err != nil {
			t.Fatalf("%s: %v", c.file, err)
		}
		near := func(a, b float64) bool { return math.Abs(a-b) <= 1e-12*math.Max(1, math.Abs(b)) }
		for i, w := range c.channels {
			g := st.Channels[i]
			if g.Min != w.Min || g.Max != w.Max || g.MinAt != w.MinAt || g.MaxAt != w.MaxAt || g.Sum != w.Sum || g.SquaresSum != w.SquaresSum ||
				!near(g.Mean, w.Mean) || !near(g.Stdev, w.Stdev) {
				t.Errorf("%s channel %d: %+v, sharp %+v", c.file, i, g, w)
			}
		}
		if !near(st.Entropy, c.entropy) || !near(st.Sharpness, c.sharpness) {
			t.Errorf("%s: entropy %v sharpness %v, sharp %v and %v", c.file, st.Entropy, st.Sharpness, c.entropy, c.sharpness)
		}
	}
	// One pixel: no spread, no sharpness.
	one := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	one.Pix[0], one.Pix[3] = 7, 255
	st, err := inspect.Stats(FromImage(one))
	if err != nil || st.Channels[0].Stdev != 0 || st.Sharpness != 0 || st.Entropy != 0 || st.Channels[0].Mean != 7 {
		t.Errorf("one pixel: %+v, %v", st, err)
	}
}
