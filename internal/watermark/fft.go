package watermark

import (
	"math"
	"math/bits"
)

// fftPlan holds the twiddle factors and bit reversal of a power-of-two
// length. The transforms are in single precision: the spectra are large
// (a picture of 2048 x 1024), and what they are used for (where peaks are,
// correlations) needs no more.
type fftPlan struct {
	n   int
	tw  []complex64 // exp(-2 pi i k / n), k < n/2
	rev []int32
}

func newFFTPlan(n int) *fftPlan {
	p := &fftPlan{n: n, tw: make([]complex64, n/2), rev: make([]int32, n)}
	for k := range p.tw {
		s, c := math.Sincos(-2 * math.Pi * float64(k) / float64(n))
		p.tw[k] = complex(float32(c), float32(s))
	}
	shift := 64 - bits.Len(uint(n-1))
	for i := range p.rev {
		if n > 1 {
			p.rev[i] = int32(bits.Reverse64(uint64(i)) >> shift)
		}
	}
	return p
}

// transform is the DFT of a (contiguous, of the plan's length), in place;
// inverse without the 1/n.
func (p *fftPlan) transform(a []complex64, inverse bool) {
	n := p.n
	a = a[:n:n]
	for i := range n {
		if j := int(p.rev[i]); i < j {
			a[i], a[j] = a[j], a[i]
		}
	}
	for size := 2; size <= n; size <<= 1 {
		half, step := size/2, n/size
		for start := 0; start < n; start += size {
			lo, hi := a[start:start+half:start+half], a[start+half:start+size:start+size]
			for k := range half {
				t := p.tw[k*step]
				if inverse {
					t = complex(real(t), -imag(t))
				}
				u, v := lo[k], cmul(hi[k], t)
				lo[k], hi[k] = u+v, u-v
			}
		}
	}
}

// cmul multiplies without fusing (see the fused job of the tests).
func cmul(a, b complex64) complex64 {
	ar, ai, br, bi := real(a), imag(a), real(b), imag(b)
	return complex(float32(ar*br)-float32(ai*bi), float32(ar*bi)+float32(ai*br))
}

// fft2 is the 2-D DFT of an h x w array (both powers of two), in place.
// The columns go through a buffer, so that each transform reads memory in
// order.
func fft2(a []complex64, h, w int, inverse bool) { fft2Par(a, h, w, inverse, nil) }

// fft2Par is fft2 with its rows, then its columns, shared out by par (nil:
// one at a time); the result is the same.
func fft2Par(a []complex64, h, w int, inverse bool, par Parallel) {
	if par == nil {
		par = func(n int, fn func(i int)) {
			for i := range n {
				fn(i)
			}
		}
	}
	pw, ph := newFFTPlan(w), newFFTPlan(h)
	const rows = 32 // rows a share at a time
	par((h+rows-1)/rows, func(i int) {
		for y := i * rows; y < min(h, (i+1)*rows); y++ {
			pw.transform(a[y*w:], inverse)
		}
	})
	const cols = 8   // columns copied out at a time
	const blocks = 8 // of them a share at a time
	par((w+cols*blocks-1)/(cols*blocks), func(i int) {
		buf := make([]complex64, cols*h)
		for x0 := i * cols * blocks; x0 < min(w, (i+1)*cols*blocks); x0 += cols {
			n := min(cols, w-x0)
			for y := range h {
				row := a[y*w+x0 : y*w+x0+n]
				for c, v := range row {
					buf[c*h+y] = v
				}
			}
			for c := range n {
				ph.transform(buf[c*h:], inverse)
			}
			for y := range h {
				row := a[y*w+x0 : y*w+x0+n]
				for c := range row {
					row[c] = buf[c*h+y]
				}
			}
		}
	})
	if inverse {
		s := 1 / float32(h*w)
		for i, v := range a {
			a[i] = complex(real(v)*s, imag(v)*s)
		}
	}
}

func pow2(n int) int { return 1 << bits.Len(uint(n-1)) }
