package tests

import (
	"image"
	"image/draw"
	"os"
	"testing"

	. "github.com/whhhh1500/goimgtools"
)

// TestICCLookupTables: a CMYK JPEG with a profile of lookup tables (lut16
// to Lab, lutAToB to XYZ) is turned into sRGB through it as soon as it is
// decoded, and an RGB image with one by ToSRGB: what LittleCMS gives
// (Pillow's ImageCms), to within one level for CMYK and three for RGB; the
// output carries no profile. A CMYK JPEG without a profile is converted as
// color.CMYK converts it.
func TestICCLookupTables(t *testing.T) {
	for _, c := range []struct {
		in, ref string
		srgb    bool
		limit   int
	}{
		{"cmyk-lut16.jpg", "cmyk-lut16-lcms.png", false, 1},
		{"cmyk-mab.jpg", "cmyk-mab-lcms.png", false, 1},
		{"rgb-lut16.png", "rgb-lut16-lcms.png", true, 3},
	} {
		in, err := os.ReadFile("testdata/icc/" + c.in)
		if err != nil {
			t.Fatal(err)
		}
		for _, threads := range []int{1, 3} {
			im := FromBytes(in).Threads(threads)
			if c.srgb {
				im.ToSRGB()
			}
			out, err := im.PNG().ToBytes()
			if err != nil {
				t.Fatal(err)
			}
			got := nrgbaOf(decodePNG(t, out))
			want := nrgbaOf(decodePNG(t, must(os.ReadFile("testdata/icc/"+c.ref))))
			worst := 0
			for i := range got.Pix {
				worst = max(worst, int(got.Pix[i])-int(want.Pix[i]), int(want.Pix[i])-int(got.Pix[i]))
			}
			if worst > c.limit {
				t.Errorf("%s, %d threads: up to %d levels from LittleCMS", c.in, threads, worst)
			}
			if info, err := FromBytes(out).Metadata(); err != nil || info.HasProfile {
				t.Errorf("%s: the output has a profile (%v)", c.in, err)
			}
		}
	}
	// Without its profile the CMYK JPEG is converted as before.
	in := must(os.ReadFile("testdata/icc/cmyk-lut16.jpg"))
	plain := must(FromBytes(in).StripICC().PNG().ToBytes())
	if info, err := FromBytes(in).Metadata(); err != nil || info.Color != "cmyk" || !info.HasProfile {
		t.Errorf("input: %+v, %v", info, err)
	}
	if len(plain) == 0 {
		t.Error("no output without the profile")
	}
}

func nrgbaOf(m image.Image) *image.NRGBA {
	n := image.NewNRGBA(m.Bounds())
	draw.Draw(n, n.Rect, m, m.Bounds().Min, draw.Src)
	return n
}
