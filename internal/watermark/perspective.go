package watermark

import "math"

// perspective estimates a homography from the tile's size measured in
// four windows of half the picture (left, right, top, bottom), each
// refined from B, the tiling in the middle: under a homography the area a
// tile covers changes as the cube of the projective denominator, so the
// sizes give its two perspective terms; the linear terms are B's. The
// tiles' start is left to the sync pattern.
func (d *detector) perspective(lum []float64, B mat2) (mapping, bool) {
	w, h := d.w, d.h
	cw, ch := w/2, h/2
	if cw < 128 || ch < 128 {
		return mapping{}, false
	}
	det0 := float64(B[0][0]*B[1][1]) - float64(B[0][1]*B[1][0])
	if det0 == 0 {
		return mapping{}, false
	}
	cy, cx := float64(float64(h)/2), float64(float64(w)/2)
	// Each window's centre relative to the picture's, and the ratio of
	// the projective denominator there to the middle's.
	var rows [][3]float64
	for _, o := range [4][2]int{{h / 4, 0}, {h / 4, w / 2}, {0, w / 4}, {h / 2, w / 4}} {
		win := make([]float64, cw*ch)
		for y := range ch {
			copy(win[y*cw:], lum[(y+o[0])*w+o[1]:][:cw])
		}
		dw := &detector{k: d.k, p: d.p, w: cw, h: ch}
		dw.prepare(win)
		Bw := dw.polishFrom(B)
		Bl, ok := dw.lsq(Bw)
		if !ok {
			continue
		}
		det := float64(Bl[0][0]*Bl[1][1]) - float64(Bl[0][1]*Bl[1][0])
		if det == 0 || det/det0 < 0.5 || det/det0 > 2 {
			continue
		}
		// The tile's area in pixels is 1/det: the denominator there over
		// the middle's is (det0/det)^(1/3).
		ratio := math.Cbrt(det0 / det)
		wy, wx := float64(o[0]+ch/2)-cy, float64(o[1]+cw/2)-cx
		rows = append(rows, [3]float64{wy, wx, ratio - 1})
	}
	if len(rows) < 3 {
		return mapping{}, false
	}
	// ratio - 1 = gy (y - cy) + gx (x - cx), by least squares.
	var a00, a01, a11, b0, b1 float64
	for _, r := range rows {
		a00 += float64(r[0] * r[0])
		a01 += float64(r[0] * r[1])
		a11 += float64(r[1] * r[1])
		b0 += float64(r[0] * r[2])
		b1 += float64(r[1] * r[2])
	}
	det := float64(a00*a11) - float64(a01*a01)
	if det == 0 {
		return mapping{}, false
	}
	gy := (float64(a11*b0) - float64(a01*b1)) / det
	gx := (float64(a00*b1) - float64(a01*b0)) / det
	// t = M (p - c) / (1 + g . (p - c)), M = B transposed (the middle's
	// tile coordinates per pixel).
	M := mat2{{B[0][0], B[1][0]}, {B[0][1], B[1][1]}}
	var m mapping
	for r := range 2 {
		m[r][0], m[r][1] = M[r][0], M[r][1]
		m[r][2] = -float64(M[r][0]*cy) - float64(M[r][1]*cx)
	}
	m[2][0], m[2][1] = gy, gx
	m[2][2] = 1 - float64(gy*cy) - float64(gx*cx)
	return m, true
}

// orient turns a mapping half round when the sync pattern says the tile's
// axes run the other way.
func (d *detector) orient(m mapping) mapping {
	f := d.fold(&m, d.all())
	z0, _, _ := peak(d.corr(f, 0), 0, 0, 0)
	z1, _, _ := peak(d.corr(f, 1), 0, 0, 0)
	if z1 > z0 {
		for j := range 3 {
			m[0][j], m[1][j] = -m[0][j], -m[1][j]
		}
	}
	return m
}

// polishFrom is polish started from a given B rather than a candidate.
func (d *detector) polishFrom(B mat2) mat2 {
	cur := d.score(d.magBox, B)
	scale := max(math.Abs(B[0][0]), math.Abs(B[0][1]), math.Abs(B[1][0]), math.Abs(B[1][1]))
	for _, step := range [...]float64{0.008, 0.004, 0.0015, 0.0005, 0.00015} {
		for improved := true; improved; {
			improved = false
			for i := range 2 {
				for j := range 2 {
					for _, sgn := range [2]float64{1, -1} {
						T := B
						T[i][j] += float64(sgn * step * scale)
						if v := d.score(d.magBox, T); v > cur {
							B, cur, improved = T, v, true
						}
					}
				}
			}
		}
	}
	return B
}
