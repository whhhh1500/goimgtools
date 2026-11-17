package avif_test

import (
	"fmt"

	"github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/avif" // registers AVIF
)

// A photograph as an AVIF, with settings; ToFile("x.avif") picks the
// format by the name too.
func Example() {
	out, err := goimgtools.Open("../tests/testdata/pillow/source.png").
		ToFormat("avif", avif.Options{Quality: 55, Effort: 6}).
		ToBytes()
	if err != nil {
		fmt.Println(err)
		return
	}
	info, _ := goimgtools.FromBytes(out).Metadata()
	fmt.Println(info.Format, info.Width, info.Height)
	// Output: avif 150 103
}
