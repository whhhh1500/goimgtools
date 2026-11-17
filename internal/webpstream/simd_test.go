package webpstream

import (
	"math/rand/v2"
	"testing"
)

// TestYuvRowAVX2: rows of every width from 1 to 300, random samples (runs
// of 0 and 255 among them), convert with AVX2 as the Go code converts them.
func TestYuvRowAVX2(t *testing.T) {
	if !useAVX2 {
		t.Skip("no AVX2")
	}
	rng := rand.New(rand.NewPCG(5, 6))
	fill := func(b []byte) {
		for i := range b {
			switch rng.IntN(4) {
			case 0:
				b[i] = 0
			case 1:
				b[i] = 255
			default:
				b[i] = byte(rng.IntN(256))
			}
		}
	}
	for w := 1; w <= 300; w++ {
		cw := (w + 1) / 2
		for range 20 {
			lum, nu, fu, nv, fv := make([]byte, w), make([]byte, cw), make([]byte, cw), make([]byte, cw), make([]byte, cw)
			for _, b := range [][]byte{lum, nu, fu, nv, fv} {
				fill(b)
			}
			want, got := make([]byte, 4*w), make([]byte, 4*w)
			upU, upV := make([]byte, w), make([]byte, w)
			upsample(nu, fu, upU)
			upsample(nv, fv, upV)
			for x, v := range lum {
				d := want[4*x : 4*x+4]
				u, vv := int(upU[x]), int(upV[x])
				yy := multHi(int(v), 19077)
				d[0] = clip8(yy + multHi(vv, 26149) - 14234)
				d[1] = clip8(yy - multHi(u, 6419) - multHi(vv, 13320) + 8708)
				d[2] = clip8(yy + multHi(u, 33050) - 17685)
				d[3] = 0xff
			}
			if !yuvRowSIMD(got, lum, nu, fu, nv, fv) {
				if w >= 33 {
					t.Fatalf("width %d not converted", w)
				}
				continue
			}
			for i := range got {
				if got[i] != want[i] {
					t.Fatalf("width %d: byte %d (pixel %d) is %d, want %d", w, i, i/4, got[i], want[i])
				}
			}
		}
	}
}
