package quant

import (
	"math"
	"testing"
)

// TestChannelCoords checks the colour coordinates: the gamma table against
// math.Pow, the interpolation at whole values, and that every 8-bit value of
// every channel comes back from its coordinate as itself.
func TestChannelCoords(t *testing.T) {
	for v := range gammaLevel {
		want := 255 * math.Pow(float64(v)/255, 0.5499/0.45455)
		if math.Abs(gammaLevel[v]-want) > 1e-9 {
			t.Fatalf("gammaLevel[%d] = %v, want %v", v, gammaLevel[v], want)
		}
	}
	for c := 0; c < 3; c++ {
		prev := -1.0
		for v := 0; v < 256; v++ {
			x := chanLevel(c, float64(v))
			if math.Abs(x-gammaLevel[v]*chanScale[c]) > 1e-9 || x <= prev {
				t.Fatalf("channel %d: chanLevel(%d) = %v", c, v, x)
			}
			prev = x
			if mid := chanLevel(c, float64(v)+0.5); v < 255 && (mid <= x || mid >= chanLevel(c, float64(v+1))) {
				t.Fatalf("channel %d: chanLevel(%d.5) = %v", c, v, mid)
			}
		}
	}
	for v := 0; v < 256; v++ {
		p := coords(uint8(v), uint8(v), uint8(v), 255)
		got := toNRGBA([4]float64{float64(p[0]), float64(p[1]), float64(p[2]), float64(p[3])})
		if got.R != uint8(v) || got.G != uint8(v) || got.B != uint8(v) || got.A != 255 {
			t.Fatalf("%d comes back as %v", v, got)
		}
	}
	// The largest coordinates stay within the space the search covers, red
	// and blue in half of it (their cells are half as long).
	if chanCoord[1][255] != maxC || 2*chanCoord[0][255] > maxC || 2*chanCoord[2][255] > maxC {
		t.Fatalf("largest coordinates %d, %d, %d", chanCoord[0][255], chanCoord[1][255], chanCoord[2][255])
	}
}
