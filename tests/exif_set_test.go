package tests

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	. "github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/inspect"
)

// TestSetExif: the fields are in the EXIF block of JPEG, PNG, WebP and
// TIFF output; with KeepExif what the camera recorded stays beside them,
// without it they are the block's only fields; GIF has no block, and is
// written all the same.
func TestSetExif(t *testing.T) {
	in, err := os.ReadFile(filepath.Join("testdata", "exif-pillow.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	orig, ok, err := inspect.ReadExif(FromBytes(in))
	if err != nil || !ok || orig.Make == "" {
		t.Fatalf("the test file's EXIF: %+v %v %v", orig, ok, err)
	}
	when := time.Date(2026, 11, 21, 23, 5, 9, 0, time.UTC)
	f := ExifFields{Copyright: "© 2026 某某", Artist: "Ann", ImageDescription: "A test", Software: "goimgtools", DateTime: when}
	for _, format := range []string{"jpeg", "png", "webp", "tiff"} {
		for _, keep := range []bool{true, false} {
			m := FromBytes(in).SetExif(f)
			if keep {
				m.KeepExif()
			}
			out, err := m.ToFormat(format).ToBytes()
			if err != nil {
				t.Fatal(err)
			}
			e, ok, err := inspect.ReadExif(FromBytes(out))
			if err != nil || !ok {
				t.Fatalf("%s keep %v: %v %v", format, keep, ok, err)
			}
			if e.Copyright != f.Copyright || e.Artist != f.Artist || e.Software != f.Software {
				t.Errorf("%s keep %v: %+v", format, keep, e)
			}
			if keep && (e.Make != orig.Make || e.Model != orig.Model || !e.Taken.Equal(orig.Taken) || e.ISO != orig.ISO) {
				t.Errorf("%s: the camera's fields changed: %+v, was %+v", format, e, orig)
			}
			if !keep && e.Make != "" {
				t.Errorf("%s without KeepExif: Make %q", format, e.Make)
			}
		}
	}
	// Later calls win; empty fields leave what is there.
	out, err := FromBytes(in).KeepExif().SetExif(ExifFields{Artist: "A"}).SetExif(ExifFields{Artist: "B"}).JPEG().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	if e, _, _ := inspect.ReadExif(FromBytes(out)); e.Artist != "B" || e.Software != orig.Software {
		t.Errorf("two calls: %+v", e)
	}
	if _, err := FromBytes(in).SetExif(f).GIF().ToBytes(); err != nil {
		t.Errorf("GIF: %v", err)
	}
}
