package textsrc

import (
	"image"
	"math"
)

// dilate writes into dst the coverage of src grown by radius pixels in
// every direction: each pixel becomes the largest value within that
// distance. It is how an outline is made from the shape of the text.
// dst and src have the same size; tmp is scratch space of one row's worth
// of bytes times three.
func dilate(dst, src *image.Alpha, radius float64, tmp []uint8) {
	w, h := src.Rect.Dx(), src.Rect.Dy()
	clear(dst.Pix)
	R := int(math.Ceil(radius))
	row, pre, suf := tmp[:w], tmp[w:2*w], tmp[2*w:3*w]
	for dy := -R; dy <= R; dy++ {
		// Half the width of the disk at this distance from its centre row.
		half := int(math.Floor(math.Sqrt(math.Max(0, float64(radius*radius)-float64(dy*dy))) + 1e-9))
		if float64(abs(dy)) > radius {
			continue
		}
		for y := 0; y < h; y++ {
			sy := y + dy
			if sy < 0 || sy >= h {
				continue
			}
			line := src.Pix[sy*src.Stride : sy*src.Stride+w]
			rowMax(row, line, half, pre, suf)
			out := dst.Pix[y*dst.Stride : y*dst.Stride+w]
			for x, v := range row {
				if v > out[x] {
					out[x] = v
				}
			}
		}
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// rowMax sets dst[x] to the largest of src[x-half .. x+half], in time that
// does not grow with half (the van Herk / Gil-Werman method: running maxima
// forward and backward inside blocks of the window's width).
func rowMax(dst, src []uint8, half int, pre, suf []uint8) {
	n := len(src)
	if half <= 0 {
		copy(dst, src)
		return
	}
	k := 2*half + 1
	for i := 0; i < n; i++ {
		if i%k == 0 {
			pre[i] = src[i]
		} else {
			pre[i] = max(pre[i-1], src[i])
		}
	}
	for i := n - 1; i >= 0; i-- {
		if i == n-1 || (i+1)%k == 0 {
			suf[i] = src[i]
		} else {
			suf[i] = max(suf[i+1], src[i])
		}
	}
	for x := 0; x < n; x++ {
		lo, hi := x-half, x+half
		var v uint8
		switch {
		case lo < 0 && hi >= n:
			v = pre[n-1]
			if n > k { // several blocks: take them all
				for b := k - 1; b < n; b += k {
					v = max(v, pre[b])
				}
			}
		case lo < 0:
			// The window starts before the row: blocks 0 .. hi.
			v = pre[hi]
			for b := k - 1; b < hi; b += k {
				v = max(v, pre[b])
			}
		case hi >= n:
			v = suf[lo]
			for b := (lo/k + 1) * k; b < n; b += k {
				v = max(v, suf[b])
			}
		default:
			v = max(suf[lo], pre[hi])
		}
		dst[x] = v
	}
}

// blur softens the coverage in m with a box of the given radius run twice
// in each direction, which is close to a Gaussian. tmp is scratch space of
// max(width, height) bytes.
func blur(m *image.Alpha, radius int, tmp []uint8) {
	if radius <= 0 {
		return
	}
	w, h := m.Rect.Dx(), m.Rect.Dy()
	for pass := 0; pass < 2; pass++ {
		for y := 0; y < h; y++ {
			boxLine(m.Pix[y*m.Stride:], 1, w, radius, tmp)
		}
		for x := 0; x < w; x++ {
			boxLine(m.Pix[x:], m.Stride, h, radius, tmp)
		}
	}
}

// boxLine replaces the n values p[0], p[step], ... by the mean of the
// 2*radius+1 values around each (values outside count as zero).
func boxLine(p []uint8, step, n, radius int, tmp []uint8) {
	for i := 0; i < n; i++ {
		tmp[i] = p[i*step]
	}
	k := 2*radius + 1
	sum := 0
	for i := 0; i < min(radius, n); i++ {
		sum += int(tmp[i])
	}
	for i := 0; i < n; i++ {
		if j := i + radius; j < n {
			sum += int(tmp[j])
		}
		if j := i - radius - 1; j >= 0 {
			sum -= int(tmp[j])
		}
		p[i*step] = uint8((sum + k/2) / k)
	}
}
