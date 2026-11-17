package tests

import (
	"os"
	"testing"

	. "github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/inspect"
)

// TestTIFFExif: what EXIF holds in a TIFF's first directory is read: the
// orientation (Metadata, Rotate turns the image as Pillow's exif_transpose
// does), and the rest for inspect.ReadExif and KeepExif, which carries it
// into a JPEG. The sums are of Pillow 12.1.1's pixels.
func TestTIFFExif(t *testing.T) {
	for _, c := range []struct {
		name        string
		orientation int
		w, h        int
		sum         string
	}{
		{"orient3.tif", 3, 16, 10, "ba5c7d82533e6537"},
		{"orient6.tif", 6, 10, 16, "8dfe5ac3bb6bd3ac"},
		{"orient8.tif", 8, 10, 16, "8481e163980f7c56"},
	} {
		in, err := os.ReadFile("testdata/tiff/" + c.name)
		if err != nil {
			t.Fatal(err)
		}
		info, err := FromBytes(in).Metadata()
		if err != nil || info.Orientation != c.orientation || !info.HasExif {
			t.Errorf("%s: %+v, %v", c.name, info, err)
		}
		out := must(FromBytes(in).Rotate().PNG().ToBytes())
		m := decodePNG(t, out)
		if m.Bounds().Dx() != c.w || m.Bounds().Dy() != c.h || rgbaSum(t, m) != c.sum {
			t.Errorf("%s: upright %v, %s; Pillow's %dx%d, %s", c.name, m.Bounds().Size(), rgbaSum(t, m), c.w, c.h, c.sum)
		}
		x, ok, err := inspect.ReadExif(FromBytes(in))
		if err != nil || !ok || x.Make != "TestMake" || x.Model != "TestModel" || x.Artist != "Someone" || x.Orientation != c.orientation {
			t.Errorf("%s: ReadExif %+v, %v, %v", c.name, x, ok, err)
		}
		jpg := must(FromBytes(in).Rotate().KeepExif().JPEG(90).ToBytes())
		x, ok, err = inspect.ReadExif(FromBytes(jpg))
		if err != nil || !ok || x.Make != "TestMake" || x.Orientation > 1 {
			t.Errorf("%s: EXIF kept in a JPEG %+v, %v, %v", c.name, x, ok, err)
		}
	}
	// And written: a JPEG's EXIF (its directories of EXIF and GPS
	// included, big-endian or not) kept in a TIFF reads back the same, the
	// orientation made 1 by Rotate.
	jpg, err := os.ReadFile("testdata/exif-pillow.jpg")
	if err != nil {
		t.Fatal(err)
	}
	want, ok, err := inspect.ReadExif(FromBytes(jpg))
	if err != nil || !ok || want.Make == "" || !want.HasTaken || !want.HasGPS {
		t.Fatalf("the JPEG's EXIF: %+v, %v, %v", want, ok, err)
	}
	for _, rotate := range []bool{false, true} {
		m := FromBytes(jpg).KeepExif()
		if rotate {
			m.Rotate()
		}
		tif := must(m.TIFF().ToBytes())
		got, ok, err := inspect.ReadExif(FromBytes(tif))
		if err != nil || !ok {
			t.Fatalf("TIFF: %v, %v", ok, err)
		}
		w := want
		if rotate {
			w.Orientation = 1
		}
		if got.String() != w.String() || got.Orientation != w.Orientation {
			t.Errorf("rotate %v: EXIF in the TIFF\n%+v\nthe JPEG's\n%+v", rotate, got, w)
		}
		if _, err := FromBytes(tif).PNG().ToBytes(); err != nil {
			t.Errorf("the TIFF: %v", err)
		}
	}
	// Without KeepExif, none.
	if _, ok, _ := inspect.ReadExif(FromBytes(must(FromBytes(jpg).TIFF().ToBytes()))); ok {
		t.Error("EXIF in a TIFF without KeepExif")
	}
}
