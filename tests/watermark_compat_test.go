package tests

import (
	"os"
	"testing"

	. "github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/inspect"
)

// TestWatermarkCompat: marks made by a release (a picture of 400 x 300,
// each kind, saved as JPEG 85) are read by every later one; the mark's
// format is part of what a release promises, as its API is.
func TestWatermarkCompat(t *testing.T) {
	for _, f := range []string{"blind-0.2.0.jpg", "screen-0.2.0.jpg"} {
		b, err := os.ReadFile("testdata/watermark/" + f)
		if err != nil {
			t.Fatal(err)
		}
		got, ok, err := inspect.ReadWatermark(FromBytes(b), "release key")
		if err != nil || !ok || string(got) != "made by 0.2.0" {
			t.Errorf("%s: read %q, %v, %v", f, got, ok, err)
		}
		if got, ok, _ := inspect.ReadWatermark(FromBytes(b), "another key"); ok {
			t.Errorf("%s: read %q with another key", f, got)
		}
	}
}
