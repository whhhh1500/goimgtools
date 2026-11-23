package watermark

// boxClamp is the mean of v (rows of w) over the (2r+1)^2 window around
// each place, the edges repeated.
func boxClamp(v []float64, w, h, r int) []float64 {
	return boxClampInto(make([]float64, len(v)), make([]float64, len(v)), v, w, h, r)
}

// boxClampInto is boxClamp written to out, by way of tmp (both at least as
// long as v and apart from it and from each other).
func boxClampInto(out, tmp, v []float64, w, h, r int) []float64 {
	out, tmp = out[:len(v)], tmp[:len(v)]
	n := float64(2*r + 1)
	for y := range h {
		row := v[y*w : y*w+w]
		var s float64
		for i := -r; i <= r; i++ {
			s += row[max(0, min(i, w-1))]
		}
		for x := range w {
			tmp[y*w+x] = s / n
			s += row[min(w-1, x+r+1)] - row[max(0, x-r)]
		}
	}
	for x := range w {
		var s float64
		for i := -r; i <= r; i++ {
			s += tmp[max(0, min(i, h-1))*w+x]
		}
		for y := range h {
			out[y*w+x] = s / n
			s += tmp[min(h-1, y+r+1)*w+x] - tmp[max(0, y-r)*w+x]
		}
	}
	return out
}

// boxClampInPlace is boxClamp of float32 values, written over them (sums
// in double precision), with storage for one row and one column besides.
func boxClampInPlace(v []float32, w, h, r int) {
	n := float64(2*r + 1)
	line := make([]float32, max(w, h))
	for y := range h {
		row := v[y*w : y*w+w]
		copy(line, row)
		var s float64
		for i := -r; i <= r; i++ {
			s += float64(line[max(0, min(i, w-1))])
		}
		for x := range w {
			row[x] = float32(s / n)
			s += float64(line[min(w-1, x+r+1)]) - float64(line[max(0, x-r)])
		}
	}
	for x := range w {
		for y := range h {
			line[y] = v[y*w+x]
		}
		var s float64
		for i := -r; i <= r; i++ {
			s += float64(line[max(0, min(i, h-1))])
		}
		for y := range h {
			v[y*w+x] = float32(s / n)
			s += float64(line[min(h-1, y+r+1)]) - float64(line[max(0, y-r)])
		}
	}
}

// boxWrap is the mean of v (rows of w) over the (2r+1)^2 window around
// each place, wrapping round (sums in double precision).
func boxWrap(v []float32, w, h, r int) []float32 {
	return boxWrapInto(make([]float32, len(v)), make([]float32, len(v)), v, w, h, r)
}

// boxWrapInto is boxWrap written to out, by way of tmp (both as long as v
// and apart from it and from each other).
func boxWrapInto(out, tmp, v []float32, w, h, r int) []float32 {
	out, tmp = out[:len(v)], tmp[:len(v)]
	n := float64(2*r + 1)
	for y := range h {
		row := v[y*w : y*w+w]
		var s float64
		for i := -r; i <= r; i++ {
			s += float64(row[mod(i, w)])
		}
		for x := range w {
			tmp[y*w+x] = float32(s / n)
			s += float64(row[mod(x+r+1, w)]) - float64(row[mod(x-r, w)])
		}
	}
	for x := range w {
		var s float64
		for i := -r; i <= r; i++ {
			s += float64(tmp[mod(i, h)*w+x])
		}
		for y := range h {
			out[y*w+x] = float32(s / n)
			s += float64(tmp[mod(y+r+1, h)*w+x]) - float64(tmp[mod(y-r, h)*w+x])
		}
	}
	return out
}

func boxWrap3(v []float32, w, h int) []float32 { return boxWrap(v, w, h, 1) }

// boxWrapRows is boxWrapInto with no more than 2r+2 rows of its first
// pass at a time, instead of all of them (the same sums in the same
// order, so the same values).
func boxWrapRows(out, v []float32, w, h, r int) []float32 {
	out = out[:len(v)]
	n := float64(2*r + 1)
	ring := 2*r + 2
	rows := make([]float32, ring*w)
	// row is the first pass of row k (any integer, wrapping round), kept in
	// the ring by k.
	row := func(k int) []float32 { return rows[(k+r)%ring*w:][:w] }
	pass := func(k int) {
		src, dst := v[mod(k, h)*w:][:w], row(k)
		var s float64
		for i := -r; i <= r; i++ {
			s += float64(src[mod(i, w)])
		}
		for x := range w {
			dst[x] = float32(s / n)
			s += float64(src[mod(x+r+1, w)]) - float64(src[mod(x-r, w)])
		}
	}
	sums := make([]float64, w)
	for k := -r; k <= r; k++ {
		pass(k)
		for x, t := range row(k) {
			sums[x] += float64(t)
		}
	}
	for y := range h {
		o := out[y*w:][:w]
		for x := range w {
			o[x] = float32(sums[x] / n)
		}
		// Row y-r leaves, y+r+1 comes in (into the slot of y-r-1).
		pass(y + r + 1)
		in, gone := row(y+r+1), row(y-r)
		for x := range w {
			sums[x] += float64(in[x]) - float64(gone[x])
		}
	}
	return out
}

// maxWrap3Rows is maxWrap3Into with three rows of its first pass at a
// time.
func maxWrap3Rows(out, v []float32, w, h int) []float32 {
	out = out[:len(v)]
	rows := make([]float32, 3*w)
	row := func(k int) []float32 { return rows[(k+1)%3*w:][:w] }
	pass := func(k int) {
		y, dst := mod(k, h), row(k)
		for x := range w {
			dst[x] = max(v[y*w+mod(x-1, w)], v[y*w+x], v[y*w+mod(x+1, w)])
		}
	}
	pass(-1)
	pass(0)
	for y := range h {
		pass(y + 1)
		a, b, c := row(y-1), row(y), row(y+1)
		o := out[y*w:][:w]
		for x := range w {
			o[x] = max(a[x], b[x], c[x])
		}
	}
	return out
}

// maxWrap3 is the largest of v over the 3 x 3 window around each place,
// wrapping round.
func maxWrap3(v []float32, w, h int) []float32 {
	return maxWrap3Into(make([]float32, len(v)), make([]float32, len(v)), v, w, h)
}

// maxWrap3Into is maxWrap3 written to out, by way of tmp (both as long as
// v and apart from it and from each other).
func maxWrap3Into(out, tmp, v []float32, w, h int) []float32 {
	out, tmp = out[:len(v)], tmp[:len(v)]
	for y := range h {
		for x := range w {
			tmp[y*w+x] = max(v[y*w+mod(x-1, w)], v[y*w+x], v[y*w+mod(x+1, w)])
		}
	}
	for y := range h {
		for x := range w {
			out[y*w+x] = max(tmp[mod(y-1, h)*w+x], tmp[y*w+x], tmp[mod(y+1, h)*w+x])
		}
	}
	return out
}
