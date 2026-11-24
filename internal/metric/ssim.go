// Package metric measures how alike two pictures are.
package metric

import (
	"fmt"
	"image"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// ssimTaps is the side of SSIM's window.
const ssimTaps = 11

// ssimWindow is the 11-tap Gaussian (sigma 1.5) of SSIM, normalized: the
// window of Wang, Bovik, Sheikh and Simoncelli's reference, as constants
// so that the score is the same on every processor.
var ssimWindow = [ssimTaps]float64{
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
	kw, kh := min(ssimTaps, w), min(ssimTaps, h)
	wx, wy := window(kw), window(kh)
	n := w - kw + 1 // the places a window fits across
	env := pipeline.EnvOf(sa)
	// The five quantities (x, y, x², y², xy): a row of them as they are,
	// the kh rows of a window filtered across, and a row filtered down.
	need := (5*int64(w) + int64(kh)*5*int64(n) + 5*int64(n)) * 8
	if err := env.Budget.Reserve(need, "ssim rows"); err != nil {
		return 0, err
	}
	defer env.Budget.Release(need)
	flat := make([]float64, 5*w)
	la, lb, aa, bb2, ab := flat[:w], flat[w:2*w], flat[2*w:3*w], flat[3*w:4*w], flat[4*w:]
	rows := make([]float64, kh*5*n) // row y's quantity i at ((y%kh)*5+i)*n
	down := make([]float64, 5*n)
	col := make([][]float64, kh)
	// The strips as NRGBA: the stages that make them are closed (their
	// buffers given back to the budget), the sources under them are not.
	keep := func(*image.NRGBA) error { return nil }
	ma, mb := pipeline.Map(noClose{sa}, keep), pipeline.Map(noClose{sb}, keep)
	defer ma.Close()
	defer mb.Close()
	ra, rb := pipeline.NewRowReader(ma), pipeline.NewRowReader(mb)
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
		for x, p := range la {
			q := lb[x]
			aa[x], bb2[x], ab[x] = float64(p*p), float64(q*q), float64(p*q)
		}
		// Across: the window's weighted sums of the five quantities.
		r := rows[(y%kh)*5*n:][:5*n]
		for i := 0; i < 5; i++ {
			across(r[i*n:][:n], flat[i*w:][:w], wx)
		}
		if y+1 < kh {
			continue
		}
		// Down: the kh rows ending here, the oldest first.
		for i := 0; i < 5; i++ {
			for k := range col {
				col[k] = rows[(((y+1-kh+k)%kh)*5+i)*n:][:n]
			}
			downward(down[i*n:][:n], col, wy)
		}
		ma, mb, sa2, sb2, sab := down[:n], down[n:2*n], down[2*n:3*n], down[3*n:4*n], down[4*n:]
		for x, mx := range ma {
			my := mb[x]
			vx := sa2[x] - float64(mx*mx)
			vy := sb2[x] - float64(my*my)
			cxy := sab[x] - float64(mx*my)
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

// noClose keeps the caller's source open when a stage over it is closed.
type noClose struct{ pipeline.Source }

func (noClose) Close() error { return nil }

// across puts in dst the weighted sums of src under the window wt at each
// place it fits (len(src)-len(wt)+1 of them), each summed from the first
// tap on. The values are never negative, so that a sum may start from its
// first term.
func across(dst, src, wt []float64) {
	if len(wt) != ssimTaps {
		for x := range dst {
			var a float64
			for k, t := range wt {
				a += float64(t * src[x+k])
			}
			dst[x] = a
		}
		return
	}
	t := (*[ssimTaps]float64)(wt)
	for x := range dst {
		s := src[x : x+ssimTaps : x+ssimTaps]
		a := float64(t[0] * s[0])
		a += float64(t[1] * s[1])
		a += float64(t[2] * s[2])
		a += float64(t[3] * s[3])
		a += float64(t[4] * s[4])
		a += float64(t[5] * s[5])
		a += float64(t[6] * s[6])
		a += float64(t[7] * s[7])
		a += float64(t[8] * s[8])
		a += float64(t[9] * s[9])
		a += float64(t[10] * s[10])
		dst[x] = a
	}
}

// downward puts in dst the weighted sums of the rows (each as long as dst)
// under the window wt, a tap to a row, summed from the first row on.
func downward(dst []float64, rows [][]float64, wt []float64) {
	n := len(dst)
	if len(wt) != ssimTaps {
		clear(dst)
		for k, t := range wt {
			for x, v := range rows[k][:n] {
				dst[x] += float64(t * v)
			}
		}
		return
	}
	t := (*[ssimTaps]float64)(wt)
	r0, r1, r2, r3, r4, r5 := rows[0][:n], rows[1][:n], rows[2][:n], rows[3][:n], rows[4][:n], rows[5][:n]
	r6, r7, r8, r9, r10 := rows[6][:n], rows[7][:n], rows[8][:n], rows[9][:n], rows[10][:n]
	for x := range dst {
		a := float64(t[0] * r0[x])
		a += float64(t[1] * r1[x])
		a += float64(t[2] * r2[x])
		a += float64(t[3] * r3[x])
		a += float64(t[4] * r4[x])
		a += float64(t[5] * r5[x])
		a += float64(t[6] * r6[x])
		a += float64(t[7] * r7[x])
		a += float64(t[8] * r8[x])
		a += float64(t[9] * r9[x])
		a += float64(t[10] * r10[x])
		dst[x] = a
	}
}

// window is the Gaussian window of n taps (11, or fewer for a smaller
// picture: its middle, normalized again).
func window(n int) []float64 {
	if n == ssimTaps {
		return ssimWindow[:]
	}
	off := (ssimTaps - n) / 2
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
