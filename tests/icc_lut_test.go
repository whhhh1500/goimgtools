package tests

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"os"
	"testing"

	. "github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/internal/iccprof"
)

// TestICCLookupTables: a CMYK JPEG with a profile of lookup tables (lut16
// to Lab, lutAToB to XYZ) is turned into sRGB through it as soon as it is
// decoded, and an RGB image with one by ToSRGB: what LittleCMS gives
// (Pillow's ImageCms, perceptual, its black point compensated), to within
// one level for CMYK and three for RGB; the output carries no profile. A
// CMYK JPEG without a profile is converted through libvips's CMYK profile,
// as sharp converts it.
func TestICCLookupTables(t *testing.T) {
	for _, c := range []struct {
		in, ref string
		srgb    bool
		limit   int
	}{
		{"cmyk-lut16.jpg", "cmyk-lut16-lcms.png", false, 1},
		{"cmyk-mab.jpg", "cmyk-mab-lcms.png", false, 1},
		{"cmyk-plain.jpg", "cmyk-plain-lcms.png", false, 1},
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
			if info, err := FromBytes(out).Metadata(); err != nil || info.HasICC {
				t.Errorf("%s: the output has a profile (%v)", c.in, err)
			}
		}
	}
	// A grey JPEG with a dot gain profile: its greys mapped, still grey,
	// with ToSRGB and for GIF output.
	gray := must(os.ReadFile("testdata/icc/gray-dotgain.jpg"))
	want := nrgbaOf(decodePNG(t, must(os.ReadFile("testdata/icc/gray-dotgain-lcms.png"))))
	for _, out := range [][]byte{must(FromBytes(gray).ToSRGB().PNG().ToBytes()), must(FromBytes(gray).GIF().ToBytes())} {
		info, err := FromBytes(out).Metadata()
		if err != nil || info.HasICC || info.Color != "gray" && info.Color != "palette" {
			t.Errorf("grey output: %+v, %v", info, err)
		}
		m, err := FromBytes(out).ToImage()
		if err != nil {
			t.Fatal(err)
		}
		got := nrgbaOf(m)
		worst := 0
		for i := range got.Pix {
			worst = max(worst, int(got.Pix[i])-int(want.Pix[i]), int(want.Pix[i])-int(got.Pix[i]))
		}
		if worst > 1 {
			t.Errorf("grey: up to %d levels from LittleCMS", worst)
		}
	}
	// A grey image made coloured (Tint) cannot keep its grey profile: each
	// channel goes through the profile's curve to sRGB, the output without
	// a profile; one that stays grey keeps it.
	icc := must(os.ReadFile("testdata/icc/gray-dotgain.icc"))
	p, err := iccprof.Parse(icc)
	if err != nil {
		t.Fatal(err)
	}
	tr := p.ToSRGB()
	raw := nrgbaOf(decodePNG(t, must(FromBytes(gray).StripICC().Tint(color.RGBA{112, 66, 20, 255}).PNG().ToBytes())))
	tinted := must(FromBytes(gray).Tint(color.RGBA{112, 66, 20, 255}).PNG().ToBytes())
	if info, err := FromBytes(tinted).Metadata(); err != nil || info.HasICC {
		t.Errorf("tinted: %+v, %v", info, err)
	}
	got := nrgbaOf(decodePNG(t, tinted))
	for i := 0; i < len(raw.Pix); i += 4 {
		r, g, b := tr.Convert(raw.Pix[i], raw.Pix[i+1], raw.Pix[i+2])
		if [3]uint8{r, g, b} != [3]uint8(got.Pix[i:i+3]) {
			t.Fatalf("tinted pixel %d: %v, want %v", i/4, got.Pix[i:i+3], [3]uint8{r, g, b})
		}
	}
	if info, err := FromBytes(must(FromBytes(gray).Resize(20, 0).JPEG(JPEGOptions{Quality: 90}).ToBytes())).Metadata(); err != nil || !info.HasICC {
		t.Errorf("still grey: %+v, %v", info, err)
	}
	// StripICC drops the profile from the output, not from the conversion.
	in := must(os.ReadFile("testdata/icc/cmyk-lut16.jpg"))
	if info, err := FromBytes(in).Metadata(); err != nil || info.Color != "cmyk" || !info.HasICC {
		t.Errorf("input: %+v, %v", info, err)
	}
	if !bytes.Equal(must(FromBytes(in).StripICC().PNG().ToBytes()), must(FromBytes(in).PNG().ToBytes())) {
		t.Error("StripICC changes the conversion")
	}
}

func nrgbaOf(m image.Image) *image.NRGBA {
	n := image.NewNRGBA(m.Bounds())
	draw.Draw(n, n.Rect, m, m.Bounds().Min, draw.Src)
	return n
}
