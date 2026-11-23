package gifstream

import (
	"bytes"
	"image"
	"io"
	"testing"

	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// TestBoundTrialsSameFile: stopping a trial of how a frame codes once it is
// no smaller than the best so far gives the file coding every trial to its
// end gives: one palette, a palette each, frames of small changes, lossy.
func TestBoundTrialsSameFile(t *testing.T) {
	defer func() { boundTrials = true }()
	const w, h, n = 160, 120, 8
	pixels := map[string]func(k, x, y int) (uint8, uint8, uint8){
		"one palette": func(k, x, y int) (uint8, uint8, uint8) {
			return uint8(x + 3*k), uint8(y + (x*y)>>7), uint8((x ^ y) + 5*k)
		},
		"a palette each": func(k, x, y int) (uint8, uint8, uint8) {
			return uint8(6 * k), uint8(y >> 2 << 2), uint8(k + (x>>6)*60)
		},
		"small changes": func(k, x, y int) (uint8, uint8, uint8) {
			if x > 20*k && x < 20*k+30 && y > 40 && y < 70 {
				return 255, uint8(k * 30), 0
			}
			return uint8(x >> 3 << 3), uint8(y >> 3 << 3), uint8((x + y) >> 4 << 4)
		},
	}
	for name, pixel := range pixels {
		for _, lossy := range []int{0, 24} {
			encode := func(bound bool) []byte {
				boundTrials = bound
				env := pipeline.NewEnv(0, "")
				k := 0
				next := func() (pipeline.Source, int, error) {
					if k == n {
						return nil, 0, io.EOF
					}
					m := image.NewNRGBA(image.Rect(0, 0, w, h))
					for y := 0; y < h; y++ {
						for x := 0; x < w; x++ {
							p := m.Pix[m.PixOffset(x, y):]
							p[0], p[1], p[2] = pixel(k, x, y)
							p[3] = 0xff
						}
					}
					k++
					return testutil.ChoppedEnv(m, 17, env), 4, nil
				}
				var out bytes.Buffer
				if err := EncodeAnimation(&out, env, AnimOptions{Frames: n, Lossy: lossy}, next); err != nil {
					t.Fatalf("%s: %v", name, err)
				}
				return out.Bytes()
			}
			if a, b := encode(true), encode(false); !bytes.Equal(a, b) {
				t.Errorf("%s, lossy %d: %d bytes with the trials stopped, %d without", name, lossy, len(a), len(b))
			}
		}
	}
}
