package jxl_test

import (
	"fmt"

	"github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/jxl" // registers JPEG XL
)

// A photograph as a JPEG XL, with settings; ToFile("x.jxl") picks the
// format by the name too.
func Example() {
	out, err := goimgtools.Open("../tests/testdata/pillow/source.png").
		ToFormat("jxl", jxl.Options{Quality: 85, Effort: 5}).
		ToBytes()
	if err != nil {
		fmt.Println(err)
		return
	}
	info, _ := goimgtools.FromBytes(out).Metadata()
	fmt.Println(info.Format, info.Width, info.Height)
	// Output: jxl 150 103
}
