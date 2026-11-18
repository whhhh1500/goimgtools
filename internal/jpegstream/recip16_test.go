package jpegstream

import "testing"

// TestRecip16: libjpeg-turbo's division with recip16's values (two 16-bit
// high multiplies) is the rounded division, (|x| + d/2) / d, for every
// divisor d = 8*q of a quantization table and every |x| up to 32767 (the
// coefficients of 8-bit samples are well inside), every value in 16 bits.
func TestRecip16(t *testing.T) {
	for q := uint32(1); q <= 255; q++ {
		d := 8 * q
		fq, c, sc := recip16(d)
		for x := uint32(0); x <= 32767; x++ {
			if x+uint32(c) > 0xffff {
				t.Fatalf("q %d, x %d: the sum does not fit 16 bits", q, x)
			}
			got := (x + uint32(c)) * uint32(fq) >> 16 * uint32(sc) >> 16
			if want := (x + d/2) / d; got != want {
				t.Fatalf("q %d, x %d: %d, want %d", q, x, got, want)
			}
		}
	}
}
