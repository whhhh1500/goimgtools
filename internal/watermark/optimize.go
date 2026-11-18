package watermark

import "math"

// optimize improves a mapping where the picture is too damaged for its
// regions to place it: coordinate ascent of the sync pattern's z-score in
// the whole picture over the mapping's four linear and two perspective
// terms, each step moving the tiling at the picture's edge by a fraction
// of a chip, smaller and smaller. It returns the mapping, aligned to the
// sync pattern's peak, and the z-score.
func (d *detector) optimize(m mapping) (mapping, float64) {
	h, w := float64(d.h), float64(d.w)
	edge := max(h, w) / 2
	score := func(m *mapping) float64 {
		z, _, _ := peak(d.corr(d.fold(m, d.all()), 0), 0, 0, 0)
		return z
	}
	// The perspective terms are tried about the picture's centre: the
	// mapping is m composed with a homography that is the identity there.
	cy, cx := float64(h/2), float64(w/2)
	persp := func(m mapping, py, px float64) mapping {
		// (y, x) -> (y, x, 1 + py (y - cy) + px (x - cx)): the
		// denominator of every row gains the term.
		var out mapping
		for r := range 3 {
			out[r][0] = m[r][0] + float64(m[r][2]*py)
			out[r][1] = m[r][1] + float64(m[r][2]*px)
			out[r][2] = m[r][2] - float64(float64(m[r][2]*py)*cy) - float64(float64(m[r][2]*px)*cx)
		}
		return out
	}
	cur := score(&m)
	for _, chips := range [...]float64{0.5, 0.25, 0.1, 0.04} {
		// A step moves the tiling at the picture's edge by that many
		// chips: as tile coordinates per pixel, and as a relative change
		// of the denominator per pixel (about the same, the tiling being
		// a few tiles across).
		step := chips / N / edge
		pstep := step
		for improved, rounds := true, 0; improved && rounds < 3; rounds++ {
			improved = false
			for i := range 6 {
				for _, sgn := range [2]float64{1, -1} {
					t := m
					switch {
					case i < 4:
						t[i/2][i%2] += float64(sgn * step)
					case i == 4:
						t = persp(m, float64(sgn*pstep), 0)
					default:
						t = persp(m, 0, float64(sgn*pstep))
					}
					if v := score(&t); v > cur {
						m, cur, improved = t, v, true
					}
				}
			}
		}
	}
	c := d.corr(d.fold(&m, d.all()), 0)
	z, py, px := peak(c, 0, 0, 0)
	dy, dx := subpeak(c, py, px)
	dy, dx = float64(wrapHalf(dy)/ns), float64(wrapHalf(dx)/ns)
	for j := range 3 {
		m[0][j] -= float64(dy * m[2][j])
		m[1][j] -= float64(dx * m[2][j])
	}
	if math.IsInf(z, 0) {
		z = 0
	}
	return m, z
}
