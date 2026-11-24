package metric

import (
	"errors"
	"image"
	"testing"

	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// plain is SSIM the long way: the luma of both pictures whole, then at
// every place the window fits its five sums, across each row and down the
// rows, a tap at a time.
func plain(a, b *image.NRGBA) float64 {
	w, h := a.Rect.Dx(), a.Rect.Dy()
	luma := func(m *image.NRGBA) []float64 {
		l := make([]float64, w*h)
		for i := range l {
			q := m.Pix[4*i : 4*i+4]
			al := float64(q[3]) / 255
			c := func(v uint8) float64 { return float64(float64(v)*al) + float64(255*(1-al)) }
			l[i] = float64(0.299*c(q[0])) + float64(0.587*c(q[1])) + float64(0.114*c(q[2]))
		}
		return l
	}
	la, lb := luma(a), luma(b)
	kw, kh := min(11, w), min(11, h)
	wx, wy := window(kw), window(kh)
	const c1, c2 = (0.01 * 255) * (0.01 * 255), (0.03 * 255) * (0.03 * 255)
	var total float64
	var count int
	for y := 0; y+kh <= h; y++ {
		for x := 0; x+kw <= w; x++ {
			var s [5]float64
			for j, wj := range wy {
				var r [5]float64
				for k, wk := range wx {
					p, q := la[(y+j)*w+x+k], lb[(y+j)*w+x+k]
					r[0] += float64(wk * p)
					r[1] += float64(wk * q)
					r[2] += float64(wk * float64(p*p))
					r[3] += float64(wk * float64(q*q))
					r[4] += float64(wk * float64(p*q))
				}
				for i := range s {
					s[i] += float64(wj * r[i])
				}
			}
			mx, my := s[0], s[1]
			vx := s[2] - float64(mx*mx)
			vy := s[3] - float64(my*my)
			cxy := s[4] - float64(mx*my)
			num := float64(float64(2*mx*my)+c1) * float64(float64(2*cxy)+c2)
			den := float64(float64(mx*mx)+float64(my*my)+c1) * float64(vx+vy+c2)
			total += num / den
			count++
		}
	}
	return total / float64(count)
}

// TestSSIMSame: the score is, to the bit, that of the sums made a window
// at a time, on pictures like photographs (with the errors a codec leaves
// and translucent parts), at the sizes where the window is cut short and
// where its rows come round, whatever strips the pictures arrive in.
func TestSSIMSame(t *testing.T) {
	for i, sz := range [][2]int{{1, 1}, {5, 4}, {11, 11}, {12, 29}, {37, 11}, {10, 40}, {40, 7}, {12, 10}, {203, 3}, {3, 203}, {64, 48}, {203, 31}, {31, 203}} {
		w, h := sz[0], sz[1]
		// A piece of a photograph-like picture.
		whole := testutil.Natural(256, 256, uint64(i)+1)
		a := image.NewNRGBA(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			copy(a.Pix[y*a.Stride:][:4*w], whole.Pix[whole.PixOffset(17+i, 9+y):])
		}
		b := image.NewNRGBA(a.Rect)
		seed := uint32(i)*2654435761 + 1
		for k := range b.Pix {
			seed = seed*1664525 + 1013904223
			v := int(a.Pix[k]) + int(seed>>29) - 3
			b.Pix[k] = uint8(max(0, min(255, v)))
		}
		// Translucent in a corner of each, not the same one.
		for y := 0; y < h/2; y++ {
			for x := 0; x < w/3; x++ {
				a.Pix[a.PixOffset(x, y)+3] = uint8(40 + (x*7+y*13)%200)
				b.Pix[b.PixOffset(w-1-x, y)+3] = uint8((x*11 + y*5) % 256)
			}
		}
		want := plain(a, b)
		if !(want > 0 && want < 1) {
			t.Fatalf("%dx%d: the long way gives %v", w, h, want)
		}
		for _, strips := range [][2]int{{1, 1}, {7, 3}, {64, 5}, {3, 256}} {
			got, err := SSIM(testutil.Chopped(a, strips[0]), testutil.Chopped(b, strips[1]))
			if err != nil || got != want {
				t.Errorf("%dx%d in strips of %v: %.17g, %v; the long way %.17g", w, h, strips, got, err, want)
			}
		}
		if same, err := SSIM(testutil.Chopped(a, 5), testutil.Chopped(a, 9)); err != nil || same != 1 {
			t.Errorf("%dx%d against itself: %v, %v", w, h, same, err)
		}
	}
}

// TestSSIMBudget: what the measure allocates is counted in the limit while
// it works (its rows of sums, and the strips of both pictures as NRGBA) and
// given back when it returns, also when it fails for want of room: at once,
// or at the second picture's strip.
func TestSSIMBudget(t *testing.T) {
	const w, h, rows = 300, 40, 7
	a := testutil.Natural(w, h, 3)
	n := int64(w - 11 + 1)
	sums := (5*int64(w) + 11*5*n + 5*n) * 8
	strip := int64(4 * w * rows)
	env := pipeline.NewEnv(0, "")
	if s, err := SSIM(testutil.ChoppedEnv(a, rows, env), testutil.ChoppedEnv(a, rows, env)); err != nil || s != 1 {
		t.Fatalf("%v, %v", s, err)
	}
	if used, peak := env.Budget.Used(), env.Budget.Peak(); used != 0 || peak != sums+2*strip {
		t.Errorf("%d bytes left reserved; %d at the most, want %d for the sums and twice %d for the strips", used, peak, sums, strip)
	}
	for _, limit := range []int64{sums - 1, sums + strip, sums + 2*strip - 1} {
		small := pipeline.NewEnv(limit, "")
		if _, err := SSIM(testutil.ChoppedEnv(a, rows, small), testutil.ChoppedEnv(a, rows, small)); !errors.Is(err, pipeline.ErrMemoryLimit) {
			t.Errorf("a limit of %d (the sums take %d, a strip %d): %v", limit, sums, strip, err)
		}
		if used := small.Budget.Used(); used != 0 {
			t.Errorf("a limit of %d: %d bytes left reserved after the error", limit, used)
		}
	}
	// With just room enough it works.
	exact := pipeline.NewEnv(sums+2*strip, "")
	if _, err := SSIM(testutil.ChoppedEnv(a, rows, exact), testutil.ChoppedEnv(a, rows, exact)); err != nil {
		t.Errorf("a limit of what it needs: %v", err)
	}
}
