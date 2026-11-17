package tests

import (
	"bytes"
	"testing"

	. "github.com/whhhh1500/goimgtools"
)

// TestDensity: the resolution a file records is read (as sharp 0.35
// reads it: EXIF first, a whole number of pixels per inch from pixels per
// metre or centimetre, 25.4 or less as none), kept in the output, also
// after a resize, and replaced by Density, in the EXIF block too.
func TestDensity(t *testing.T) {
	density := func(name string, data []byte) float64 {
		t.Helper()
		info, err := FromBytes(data).Metadata()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return info.Density
	}
	// Files sharp wrote: 300 in JFIF and EXIF, 96.5 in pHYs (96.49) and
	// EXIF (96.5), 300 in the EXIF chunk of a WebP.
	for name, want := range map[string]float64{"density-300.jpg": 300, "density-96.5.png": 96.5, "density-300.webp": 300} {
		if got := density(name, sourcePNG(t, "sharp/"+name)); got != want {
			t.Errorf("%s: density %v, want %v", name, got, want)
		}
	}

	photo := photoLike(40, 30)
	jpeg300 := sourcePNG(t, "sharp/density-300.jpg")
	for _, format := range []string{"jpeg", "png", "tiff", "bmp"} {
		// Kept, also through a resize.
		out, err := FromBytes(jpeg300).Resize(5, 0).ToFormat(format).ToBytes()
		if err != nil {
			t.Fatal(err)
		}
		if got := density(format, out); got != 300 {
			t.Errorf("%s: kept density %v, want 300", format, got)
		}
		// Set, and read back.
		for _, dpi := range []float64{72, 96, 150, 600, 2400} {
			out := must(FromImage(photo).Density(dpi).ToFormat(format).ToBytes())
			if got := density(format, out); got != dpi {
				t.Errorf("%s: density %v written, %v read", format, dpi, got)
			}
		}
		// None to keep: none recorded (the TIFF says 72, as it always did).
		want := 0.0
		if format == "tiff" {
			want = 72
		}
		if got := density(format, must(FromImage(photo).ToFormat(format).ToBytes())); got != want {
			t.Errorf("%s: density %v without one, want %v", format, got, want)
		}
	}
	// A JPEG without a resolution is as before: no JFIF segment.
	if out := must(FromImage(photo).JPEG(85).ToBytes()); bytes.Contains(out[:40], []byte("JFIF")) {
		t.Error("a JFIF segment without a resolution")
	}
	// Density replaces the resolution of a kept EXIF block (which the
	// reading prefers), and is written where EXIF is the only place.
	for _, format := range []string{"jpeg", "png", "webp"} {
		out := must(FromBytes(jpeg300).KeepExif().Density(144).ToFormat(format).ToBytes())
		if got := density(format, out); got != 144 {
			t.Errorf("%s with EXIF: density %v, want 144", format, got)
		}
	}
	// JFIF in dots per centimetre: 118 is 300 per inch.
	out := must(FromImage(photo).Density(300).JPEG(85).ToBytes())
	i := bytes.Index(out, []byte("JFIF\x00"))
	cm := append([]byte(nil), out...)
	cm[i+7], cm[i+8], cm[i+9] = 2, 0, 118
	if got := density("cm", cm); got != 300 {
		t.Errorf("118 per cm: %v, want 300", got)
	}
	// 1 pixel per millimetre or less is none, as sharp reads it.
	if got := density("low", must(FromImage(photo).Density(20).PNG().ToBytes())); got != 0 {
		t.Errorf("20 dpi read as %v, want none", got)
	}
	// Density(0) records none: what the standard library writes.
	for _, format := range []string{"jpeg", "png", "bmp"} {
		if got := density(format, must(FromBytes(jpeg300).Density(0).ToFormat(format).ToBytes())); got != 0 {
			t.Errorf("%s: Density(0) recorded %v", format, got)
		}
	}
	for _, dpi := range []float64{0.5, -1, 70000} {
		if _, err := FromImage(photo).Density(dpi).PNG().ToBytes(); err == nil {
			t.Errorf("Density(%v): no error", dpi)
		}
	}
}
