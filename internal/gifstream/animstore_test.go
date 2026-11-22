package gifstream

import (
	"bytes"
	"image"
	"image/gif"
	"io"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// TestAnimationStoredOnce: the frames of an animation are kept once while
// its palette is chosen, whether it is then written with one palette or
// with a palette for each frame (which is written from the same rows): what
// is reserved at the peak stays well under two copies of the frames.
func TestAnimationStoredOnce(t *testing.T) {
	const w, h, n = 256, 192, 16
	stored := int64(w * h * 4 * n)
	for _, c := range []struct {
		name  string
		local bool
		pixel func(k, x, y int) (r, g, b uint8)
	}{
		// More than 256 colours in every frame: one palette for all.
		{"one palette", false, func(k, x, y int) (uint8, uint8, uint8) {
			return uint8(x + 3*k), uint8(y + (x*y)>>7), uint8((x ^ y) + 5*k)
		}},
		// 192 colours a frame, others in each: a palette for each frame.
		{"a palette each", true, func(k, x, y int) (uint8, uint8, uint8) {
			return uint8(6 * k), uint8(y >> 2 << 2), uint8(k + (x>>6)*60)
		}},
	} {
		// No directory for temporary files: a second copy of the frames,
		// which would not fit in memory beside the first, is an error.
		env := pipeline.NewEnv(64<<20, filepath.Join(t.TempDir(), "none"))
		k := 0
		next := func() (pipeline.Source, int, error) {
			if k == n {
				return nil, 0, io.EOF
			}
			m := image.NewNRGBA(image.Rect(0, 0, w, h))
			for y := 0; y < h; y++ {
				for x := 0; x < w; x++ {
					p := m.Pix[m.PixOffset(x, y):]
					p[0], p[1], p[2] = c.pixel(k, x, y)
					p[3] = 0xff
				}
			}
			k++
			return testutil.ChoppedEnv(m, 37, env), 4, nil
		}
		var out bytes.Buffer
		out.Grow(1 << 20)
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		if err := EncodeAnimation(&out, env, AnimOptions{Frames: n}, next); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		runtime.ReadMemStats(&after)
		// The frames themselves (made here) are 16 of 192KB. Measured: 11MB
		// with one palette and 18MB with a palette each, where it was 19MB
		// and 38MB while every trial of how a frame codes smallest took
		// tables of its own (64KB, some fifteen a frame) and every frame a
		// strip of its own.
		limit := int64(15 << 20)
		if c.local {
			limit = 28 << 20
		}
		if alloc := int64(after.TotalAlloc - before.TotalAlloc); alloc > limit {
			t.Errorf("%s: %dKB allocated", c.name, alloc>>10)
		} else {
			t.Logf("%s: %dKB allocated", c.name, alloc>>10)
		}
		g, err := gif.DecodeAll(bytes.NewReader(out.Bytes()))
		if err != nil || len(g.Image) != n {
			t.Fatalf("%s: %d frames read back, %v", c.name, len(g.Image), err)
		}
		// A palette for each frame shows as palettes that differ.
		if local := !reflect.DeepEqual(g.Image[0].Palette, g.Image[n-1].Palette); local != c.local {
			t.Errorf("%s: a palette for each frame: %v", c.name, local)
		}
		if c.local {
			// Without loss: the first and the last frame as they were.
			for _, i := range []int{0, n - 1} {
				if i == 0 || g.Image[i].Rect == image.Rect(0, 0, w, h) {
					r, gr, b, _ := g.Image[i].At(100, 50).RGBA()
					wr, wg, wb := c.pixel(i, 100, 50)
					if uint8(r>>8) != wr || uint8(gr>>8) != wg || uint8(b>>8) != wb {
						t.Errorf("%s: frame %d pixel (100, 50) is %d %d %d, want %d %d %d", c.name, i, r>>8, gr>>8, b>>8, wr, wg, wb)
					}
				}
			}
		}
		if peak := env.Budget.Peak(); peak > env.Budget.Limit()/pipeline.StagingShare {
			t.Errorf("%s: %d bytes reserved at the peak, the frames are %d", c.name, peak, stored)
		} else {
			t.Logf("%s: peak %dKB for %dKB of frames, a file of %dKB", c.name, peak>>10, stored>>10, out.Len()>>10)
		}
		if used := env.Budget.Used(); used != 0 {
			t.Errorf("%s: %d bytes still reserved", c.name, used)
		}
	}
}
