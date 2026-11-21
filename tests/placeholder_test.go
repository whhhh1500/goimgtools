package tests

import (
	"strings"
	"testing"

	. "github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/inspect"
)

// TestPlaceholderHashes: a BlurHash has the length its components give and
// does not change with the size of the picture; a ThumbHash says whether
// the picture has transparency; bad arguments are errors.
func TestPlaceholderHashes(t *testing.T) {
	photo := pngBytes(t, noisyPhoto(640, 480, false))
	b, err := inspect.BlurHash(FromBytes(photo), 4, 3)
	if err != nil || len(b) != 4+2*12 {
		t.Fatalf("BlurHash %q, %v", b, err)
	}
	// The same picture smaller: the mean colour within a level or two.
	small, _ := inspect.BlurHash(FromBytes(photo).Resize(320, 0), 4, 3)
	dc := func(s string) (v int) {
		const digits = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz#$%*+,-.:;=?@[]^_{|}~"
		for _, c := range s[2:6] {
			v = v*83 + strings.IndexRune(digits, c)
		}
		return v
	}
	for _, sh := range []int{16, 8, 0} {
		if d := dc(small)>>sh&255 - dc(b)>>sh&255; d > 2 || d < -2 || small[0] != b[0] {
			t.Errorf("BlurHash %s at half the size, %s", small, b)
		}
	}
	th, err := inspect.ThumbHash(FromBytes(photo))
	if err != nil || len(th) < 5 || th[2]&0x80 != 0 {
		t.Fatalf("ThumbHash %x, %v", th, err)
	}
	cut, err := inspect.ThumbHash(FromBytes(pngBytes(t, noisyPhoto(200, 150, true))))
	if err != nil || cut[2]&0x80 == 0 {
		t.Errorf("ThumbHash of a cut-out %x (no alpha bit), %v", cut, err)
	}
	for _, c := range [][2]int{{0, 3}, {4, 10}} {
		if _, err := inspect.BlurHash(FromBytes(photo), c[0], c[1]); err == nil {
			t.Errorf("%v components: no error", c)
		}
	}
	if _, err := inspect.BlurHash(nil, 4, 3); err == nil {
		t.Error("nil: no error")
	}
	if _, err := inspect.ThumbHash(FromBytes([]byte("not an image"))); err == nil {
		t.Error("not an image: no error")
	}
}
