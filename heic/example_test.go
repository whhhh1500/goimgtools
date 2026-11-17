package heic_test

import (
	"fmt"

	"github.com/whhhh1500/goimgtools"
	_ "github.com/whhhh1500/goimgtools/heic" // registers HEIC
)

// A phone's HEIC photograph as a JPEG for the web: upright, at most 1600
// pixels a side, its EXIF left out.
func Example() {
	out, err := goimgtools.Open("testdata/test_exif.heic").
		Resize(1600, 1600, goimgtools.ResizeOptions{Fit: goimgtools.FitInside, WithoutEnlargement: true}).
		JPEG(85).
		ToBytes()
	if err != nil {
		fmt.Println(err)
		return
	}
	info, _ := goimgtools.FromBytes(out).Metadata()
	fmt.Println(info.Format, info.Width, info.Height)
	// Output: jpeg 480 640
}
