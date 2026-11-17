package webpstream

import (
	"bytes"
	"testing"

	"github.com/whhhh1500/goimgtools/internal/testutil"
)

// FuzzDecode compares the decoder with golang.org/x/image/webp (see check)
// on mutations of its test files and of this module's.
func FuzzDecode(f *testing.F) {
	if testutil.XImageTestdata() != "" {
		for _, data := range testFiles(f) {
			if len(data) < 64<<10 {
				f.Add(data)
			}
		}
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		// Large images only make the two decoders slow.
		if src, err := Open(bytes.NewReader(data), int64(len(data)), nil); err == nil {
			b := src.Bounds()
			src.Close()
			if int64(b.Dx())*int64(b.Dy()) > 1<<18 {
				return
			}
		}
		setBandRows(t, 16)
		check(t, "fuzz input", data)
	})
}
