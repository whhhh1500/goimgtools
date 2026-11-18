package jpegstream

import (
	"encoding/binary"
	"math/rand/v2"
	"testing"
)

// TestACMask: acMaskAsm marks the coefficients whose absolute value is not
// zero once shifted right by al, as acFirst's Go loop does, for random
// blocks (values small, large and at the ends of int16) and every al the
// scan script can use.
func TestACMask(t *testing.T) {
	if !acMaskSIMD {
		t.Skip("no SIMD")
	}
	rng := rand.New(rand.NewPCG(19, 20))
	w := make([]byte, 2*blockSize)
	for i := range 200000 {
		for k := 0; k < blockSize; k++ {
			var v int16
			switch rng.IntN(5) {
			case 0:
				v = int16(rng.IntN(65536) - 32768)
			case 1:
				v = []int16{-32768, 32767, -1, 1, 0}[rng.IntN(5)]
			case 2:
				v = int16(rng.IntN(9) - 4)
			}
			binary.LittleEndian.PutUint16(w[2*k:], uint16(v))
		}
		al := uint(rng.IntN(14))
		var want uint64
		for k := 0; k < blockSize; k++ {
			v := coefAt(w, k)
			if uint32((v^v>>31)-v>>31)>>al != 0 {
				want |= 1 << uint(k)
			}
		}
		if got := acMaskAsm(&w[0], al); got != want {
			t.Fatalf("case %d, al %d: %064b, want %064b", i, al, got, want)
		}
	}
}
