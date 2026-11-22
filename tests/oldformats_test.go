package tests

import (
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/whhhh1500/goimgtools"
)

// TestReadOnlyFormats: PCX, SGI, XBM, XPM, DDS and FITS files are known by
// their content, read to the pixels of the reference beside each (see the
// packages' own tests for where those come from), and not written.
func TestReadOnlyFormats(t *testing.T) {
	for _, c := range []struct {
		format, pkg string
		files       []string
	}{
		{"pcx", "pcxstream", []string{"mono_37x29", "ega16_37x29", "pal_40x30", "grey_40x30", "rgb_37x29"}},
		{"sgi", "sgistream", []string{"grey8_rle_37x29", "rgb8_40x30", "rgba8_rle_40x30", "rgb16_ffmpeg_37x29"}},
		{"xbm", "xbmstream", []string{"mono_37x29", "hot_16x16"}},
		{"xpm", "xpmstream", []string{"c1_37x29", "c2_many_37x29", "none_ffmpeg_37x29"}},
		{"dds", "ddsstream", []string{"dxt1_37x23", "dxt5_40x28", "rgba_37x23", "l_40x28", "bc7_modes_80x64"}},
		{"fits", "fitsstream", []string{"u8_40x28", "i16_bzero_37x23", "f32_37x23"}},
	} {
		dir := filepath.Join("..", "internal", c.pkg, "testdata")
		for _, name := range c.files {
			data, err := os.ReadFile(filepath.Join(dir, name+"."+c.format))
			if err != nil {
				t.Fatal(err)
			}
			ref, err := os.ReadFile(filepath.Join(dir, name+".png"))
			if err != nil {
				t.Fatal(err)
			}
			what := c.format + " " + name
			info, err := FromBytes(data).Metadata()
			if err != nil || info.Format != c.format {
				t.Errorf("%s: Metadata = %+v, %v", what, info, err)
				continue
			}
			got, err := FromBytes(data).ToImage()
			if err != nil {
				t.Errorf("%s: %v", what, err)
				continue
			}
			want := decodeOurs(t, ref)
			if got.Bounds() != want.Bounds() || info.Width != want.Bounds().Dx() || info.Height != want.Bounds().Dy() {
				t.Errorf("%s: %v (Metadata %dx%d), the reference is %v", what, got.Bounds(), info.Width, info.Height, want.Bounds())
				continue
			}
			b := got.Bounds()
		pixels:
			for y := b.Min.Y; y < b.Max.Y; y++ {
				for x := b.Min.X; x < b.Max.X; x++ {
					p, q := color.NRGBA64Model.Convert(got.At(x, y)), color.NRGBA64Model.Convert(want.At(x, y))
					if p != q {
						t.Errorf("%s: pixel (%d, %d) is %v, the reference has %v", what, x, y, p, q)
						break pixels
					}
				}
			}
			// Through the usual chain, to a file of another format.
			if out, err := FromBytes(data).Resize(20, 0).JPEG().ToBytes(); err != nil || len(out) == 0 {
				t.Errorf("%s resized to a JPEG: %v", what, err)
			}
		}
		// Known by its name, and not written.
		if f, ok := FormatByName(c.format); !ok || f.Name != c.format || len(f.Extensions) == 0 || f.Extensions[0] != "."+c.format {
			t.Errorf("%s: FormatByName gives %+v, %v", c.format, f, ok)
		}
		if _, err := FromImage(photoLike(8, 8)).ToFormat(c.format).ToBytes(); err == nil || !strings.Contains(err.Error(), c.format) {
			t.Errorf("%s was written: %v", c.format, err)
		}
	}
}
