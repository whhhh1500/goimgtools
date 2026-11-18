package watermark

// boxClamp is the mean of v (rows of w) over the (2r+1)^2 window around
// each place, the edges repeated.
func boxClamp(v []float64, w, h, r int) []float64 {
	tmp := make([]float64, len(v))
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
	out := make([]float64, len(v))
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

// boxWrap is the mean of v (rows of w) over the (2r+1)^2 window around
// each place, wrapping round (sums in double precision).
func boxWrap(v []float32, w, h, r int) []float32 {
	tmp := make([]float32, len(v))
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
	out := make([]float32, len(v))
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

// maxWrap3 is the largest of v over the 3 x 3 window around each place,
// wrapping round.
func maxWrap3(v []float32, w, h int) []float32 {
	tmp := make([]float32, len(v))
	for y := range h {
		for x := range w {
			tmp[y*w+x] = max(v[y*w+mod(x-1, w)], v[y*w+x], v[y*w+mod(x+1, w)])
		}
	}
	out := make([]float32, len(v))
	for y := range h {
		for x := range w {
			out[y*w+x] = max(tmp[mod(y-1, h)*w+x], tmp[y*w+x], tmp[mod(y+1, h)*w+x])
		}
	}
	return out
}
