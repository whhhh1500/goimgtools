package zopfli

import "math"

// zlog is math.Log's algorithm (FreeBSD's e_log.c, as Go's math package
// writes it) with each product rounded on its own. math.Log is assembly
// on amd64 and s390x, and on the other processors with a fused multiply
// and add the compiler may fuse its products and sums, so its results
// could differ from one processor to another; with zlog the costs, and
// so the parse, are the same on every one. On amd64 and 386 it gives
// math.Log's results (see TestLog).
func zlog(x float64) float64 {
	const (
		ln2Hi = 6.93147180369123816490e-01 /* 3fe62e42 fee00000 */
		ln2Lo = 1.90821492927058770002e-10 /* 3dea39ef 35793c76 */
		l1    = 6.666666666666735130e-01   /* 3FE55555 55555593 */
		l2    = 3.999999999940941908e-01   /* 3FD99999 9997FA04 */
		l3    = 2.857142874366239149e-01   /* 3FD24924 94229359 */
		l4    = 2.222219843214978396e-01   /* 3FCC71C5 1D8E78AF */
		l5    = 1.818357216161805012e-01   /* 3FC74664 96CB03DE */
		l6    = 1.531383769920937332e-01   /* 3FC39A09 D078C69F */
		l7    = 1.479819860511658591e-01   /* 3FC2F112 DF3E5244 */
	)
	switch {
	case math.IsNaN(x) || math.IsInf(x, 1):
		return x
	case x < 0:
		return math.NaN()
	case x == 0:
		return math.Inf(-1)
	}
	f1, ki := math.Frexp(x)
	if f1 < math.Sqrt2/2 {
		f1 *= 2
		ki--
	}
	f := f1 - 1
	k := float64(ki)
	s := f / (2 + f)
	s2 := float64(s * s)
	s4 := float64(s2 * s2)
	t1 := float64(s2 * (l1 + float64(s4*(l3+float64(s4*(l5+float64(s4*l7)))))))
	t2 := float64(s4 * (l2 + float64(s4*(l4+float64(s4*l6)))))
	r := t1 + t2
	hfsq := float64(float64(0.5*f) * f)
	return float64(k*ln2Hi) - ((hfsq - (float64(s*(hfsq+r)) + float64(k*ln2Lo))) - f)
}

// zlog2 is math.Log2 as zlog is math.Log.
func zlog2(x float64) float64 {
	frac, exp := math.Frexp(x)
	if frac == 0.5 {
		return float64(exp - 1)
	}
	return float64(zlog(frac)*(1/math.Ln2)) + float64(exp)
}
