package tests

import (
	"image"
	"image/color"
	"math"
	"testing"

	. "github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/inspect"
)

// TestSSIM: 1 for the same picture, less the more it is changed, the value
// of the reference (Wang et al.'s ssim_index, worked out apart for this
// picture), the same with the second image read in other strips; pictures
// smaller than the window; sizes must match.
func TestSSIM(t *testing.T) {
	photo := pngBytes(t, noisyPhoto(96, 72, false))
	same, err := inspect.SSIM(FromBytes(photo), FromBytes(photo))
	if err != nil || same != 1 {
		t.Fatalf("same picture: %v, %v", same, err)
	}
	q90, _ := inspect.SSIM(FromBytes(photo), FromBytes(must(FromBytes(photo).JPEG(JPEGOptions{Quality: 90}).ToBytes())))
	q30, _ := inspect.SSIM(FromBytes(photo), FromBytes(must(FromBytes(photo).JPEG(JPEGOptions{Quality: 30}).ToBytes())))
	blur, _ := inspect.SSIM(FromBytes(photo), FromBytes(photo).Blur(1.5))
	if !(1 > q90 && q90 > q30 && q30 > 0) || blur >= q90 {
		t.Errorf("SSIM: quality 90 %.4f, 30 %.4f, blurred %.4f", q90, q30, blur)
	}
	// A picture made for it: grey levels in steps, against them shifted.
	a, b := image.NewGray(image.Rect(0, 0, 40, 30)), image.NewGray(image.Rect(0, 0, 40, 30))
	for y := 0; y < 30; y++ {
		for x := 0; x < 40; x++ {
			a.Pix[y*40+x] = uint8((x/4)*20 + y)
			b.Pix[y*40+x] = uint8((x/4)*20 + y + (x*y)%7)
		}
	}
	got, err := inspect.SSIM(FromImage(a), FromImage(b))
	if want := 0.9807748651; err != nil || math.Abs(got-want) > 1e-9 {
		t.Errorf("steps: %.10f, %v; the reference's %.10f", got, err, want)
	}
	// Smaller than the window; with transparency.
	if s, err := inspect.SSIM(Blank(5, 4, color.White), Blank(5, 4, color.NRGBA{250, 250, 250, 255})); err != nil || s <= 0.99 || s >= 1 {
		t.Errorf("5x4: %v, %v", s, err)
	}
	if s, err := inspect.SSIM(Blank(20, 20, color.Transparent), Blank(20, 20, color.White)); err != nil || s != 1 {
		t.Errorf("transparent against white: %v, %v", s, err)
	}
	if _, err := inspect.SSIM(Blank(20, 20, color.White), Blank(21, 20, color.White)); err == nil {
		t.Error("other sizes: no error")
	}
}
