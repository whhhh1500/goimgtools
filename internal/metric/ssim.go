// Package metric measures how alike two pictures are.
package metric

import (
	"fmt"
	"image"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// ssimWindow is the 11-tap Gaussian (sigma 1.5) of SSIM, normalized: the
// window of Wang, Bovik, Sheikh and Simoncelli's reference, as constants
// so that the score is the same on every processor.
var ssimWindow = [11]float64{
	0.00102838008447911, 0.007598758135239185, 0.03600077212843083, 0.10936068950970002, 0.2130055377112537,
	0.26601172486179436,
	0.2130055377112537, 0.10936068950970002, 0.03600077212843083, 0.007598758135239185, 0.00102838008447911,
}

// SSIM is the structural similarity of sb to sa (see inspect.SSIM), which
// must be the same size. It reads both to their ends; it closes neither.
func SSIM(sa, sb pipeline.Source) (float64, error) {
	ba, bb := sa.Bounds(), sb.Bounds()
	if ba.Size() != bb.Size() {
		return 0, fmt.Errorf("goimgtools: SSIM of images of different sizes, %v and %v", ba.Size(), bb.Size())
	}
	w, h := ba.Dx(), ba.Dy()
	// The window, or the whole picture when it is smaller.
	kw, kh := min(11, w), min(11, h)
	wx, wy := window(kw), window(kh)
	env := pipeline.EnvOf(sa)
	// Rows of the five sums (x, y, x², y², xy), filtered across, for the
	// kh rows of a window.
	need := int64(kh) * int64(w) * 5 * 8
	if err := env.Budget.Reserve(need, "ssim rows"); err != nil {
		return 0, err
	}
	defer env.Budget.Release(need)
	rows := make([][5][]float64, kh)
	for i := range rows {
		for k := range rows[i] {
			rows[i][k] = make([]float64, w-kw+1)
		}
	}
	keep := func(*image.NRGBA) error { return nil }
	ra, rb := pipeline.NewRowReader(pipeline.Map(sa, keep)), pipeline.NewRowReader(pipeline.Map(sb, keep))
	la, lb := make([]float64, w), make([]float64, w)
	luma := func(r *pipeline.RowReader, y int, b image.Rectangle, out []float64) error {
		p, err := r.Row(b.Min.Y + y)
		if err != nil {
			return err
		}
		m := p.(*image.NRGBA)
		row := m.Pix[m.PixOffset(b.Min.X, b.Min.Y+y):][:4*w]
		for x := range out {
			q := row[4*x : 4*x+4 : 4*x+4]
			// Laid on white, then Rec. 601.
			a := float64(q[3]) / 255
			c := func(v uint8) float64 { return float64(float64(v)*a) + float64(255*(1-a)) }
			out[x] = float64(0.299*c(q[0])) + float64(0.587*c(q[1])) + float64(0.114*c(q[2]))
		}
		return nil
	}
	const c1, c2 = (0.01 * 255) * (0.01 * 255), (0.03 * 255) * (0.03 * 255)
	var total float64
	var count int
	for y := 0; y < h; y++ {
		if err := luma(ra, y, ba, la); err != nil {
			return 0, err
		}
		if err := luma(rb, y, bb, lb); err != nil {
			return 0, fmt.Errorf("second image: %w", err)
		}
		// Across: the window's weighted sums of the five quantities.
		r := &rows[y%kh]
		for x := 0; x+kw <= w; x++ {
			var s [5]float64
			for k, wt := range wx {
				p, q := la[x+k], lb[x+k]
				s[0] += float64(wt * p)
				s[1] += float64(wt * q)
				s[2] += float64(wt * float64(p*p))
				s[3] += float64(wt * float64(q*q))
				s[4] += float64(wt * float64(p*q))
			}
			for k := range s {
				r[k][x] = s[k]
			}
		}
		if y+1 < kh {
			continue
		}
		// Down: the kh rows ending here, the oldest first.
		for x := 0; x+kw <= w; x++ {
			var s [5]float64
			for k, wt := range wy {
				src := &rows[(y+1-kh+k)%kh]
				for i := range s {
					s[i] += float64(wt * src[i][x])
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
	if err := ra.Drain(); err != nil {
		return 0, err
	}
	if err := rb.Drain(); err != nil {
		return 0, fmt.Errorf("second image: %w", err)
	}
	return total / float64(count), nil
}

// window is the Gaussian window of n taps (11, or fewer for a smaller
// picture: its middle, normalized again).
func window(n int) []float64 {
	if n == 11 {
		return ssimWindow[:]
	}
	off := (11 - n) / 2
	w := append([]float64(nil), ssimWindow[off:off+n]...)
	var s float64
	for _, v := range w {
		s += v
	}
	for i := range w {
		w[i] /= s
	}
	return w
}
