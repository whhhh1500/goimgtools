package pngstream

import (
	"bytes"
	"math/rand"
	"testing"
)

// TestFilterPaeth: filterPaeth undoes rows as the standard library's does,
// for every number of bytes per pixel a PNG has, on noise and on smooth
// rows (where each of the three predictions gets chosen).
func TestFilterPaeth(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for _, bpp := range []int{1, 2, 3, 4, 6, 8} {
		for k := 0; k < 400; k++ {
			n := bpp * (1 + r.Intn(300))
			cur, prev := make([]byte, n), make([]byte, n)
			for i := range cur {
				if k%2 == 0 {
					cur[i], prev[i] = byte(r.Intn(256)), byte(r.Intn(256))
				} else {
					cur[i], prev[i] = byte(r.Intn(5)), byte(i/bpp*3+r.Intn(4))
				}
			}
			want := append([]byte(nil), cur...)
			refFilterPaeth(want, prev, bpp)
			filterPaeth(cur, prev, bpp)
			if !bytes.Equal(cur, want) {
				t.Fatalf("bpp %d, row %d: differs from the standard library", bpp, k)
			}
		}
	}
}

// refFilterPaeth is the standard library's filterPaeth, kept for the
// tests to check filterPaeth against.
func refFilterPaeth(cdat, pdat []byte, bytesPerPixel int) {
	var a, b, c, pa, pb, pc int
	for i := 0; i < bytesPerPixel; i++ {
		a, c = 0, 0
		for j := i; j < len(cdat); j += bytesPerPixel {
			b = int(pdat[j])
			pa = b - c
			pb = a - c
			pc = abs(pa + pb)
			pa = abs(pa)
			pb = abs(pb)
			if pa <= pb && pa <= pc {
				// No-op.
			} else if pb <= pc {
				a = b
			} else {
				a = c
			}
			a += int(cdat[j])
			a &= 0xff
			cdat[j] = uint8(a)
			c = b
		}
	}
}
