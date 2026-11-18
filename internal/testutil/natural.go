package testutil

import "image"

// Natural is a picture with the statistics of a photograph: noise of every
// scale, its power falling with frequency, and some hard-edged shapes;
// seed picks one. The same on every platform.
func Natural(w, h int, seed uint64) *image.NRGBA {
	s := seed
	next := func() uint64 {
		s += 0x9e3779b97f4a7c15
		z := s
		z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
		z = (z ^ (z >> 27)) * 0x94d049bb133111eb
		return z ^ (z >> 31)
	}
	intn := func(n int) int { return int(next() % uint64(n)) }
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	acc := make([][3]float64, w*h)
	amp := 60.0
	for cell := 256; cell >= 2; cell /= 2 {
		gw, gh := w/cell+2, h/cell+2
		grid := make([][3]float64, gw*gh)
		for i := range grid {
			for c := range 3 {
				grid[i][c] = float64(float64(intn(2001)-1000)/1000) * amp
			}
		}
		for y := range h {
			fy := float64(y) / float64(cell)
			y0 := int(fy)
			dy := fy - float64(y0)
			for x := range w {
				fx := float64(x) / float64(cell)
				x0 := int(fx)
				dx := fx - float64(x0)
				for c := range 3 {
					a := float64(grid[y0*gw+x0][c]*(1-dx)) + float64(grid[y0*gw+x0+1][c]*dx)
					b := float64(grid[(y0+1)*gw+x0][c]*(1-dx)) + float64(grid[(y0+1)*gw+x0+1][c]*dx)
					acc[y*w+x][c] += float64(a*(1-dy)) + float64(b*dy)
				}
			}
		}
		amp *= 0.6
	}
	for i := range 12 {
		x0, y0 := intn(w), intn(h)
		x1, y1 := min(w, x0+intn(w/3)+10), min(h, y0+intn(h/3)+10)
		shade := float64(float64(intn(120)-60) * float64(1+i%3) / 2)
		for y := y0; y < y1; y++ {
			for x := x0; x < x1; x++ {
				for c := range 3 {
					acc[y*w+x][c] += shade
				}
			}
		}
	}
	for i, v := range acc {
		for c := range 3 {
			img.Pix[4*i+c] = uint8(max(0, min(255, 128+v[c])))
		}
		img.Pix[4*i+3] = 255
	}
	return img
}
