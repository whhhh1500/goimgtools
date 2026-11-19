package watermark

// A mark changes red, green and blue alike, so it is all in the brightness
// and none in the colour (blue and red less the brightness); much of a
// photograph's texture is in both. CleanChroma takes from the brightness
// what its colour explains around each place, and leaves the mark with
// less of the picture over it: on the 24 Kodak photographs as PNG, fitted
// over 17 x 17, 1 to 2.7 dB of the brightness's high-pass power, after
// JPEG 80 (its colour at half the size) 0.3 to 1.2. A camera's white
// balance, though, puts some of the mark in the colour, where the fit takes
// it out, so the reader tries the brightness as it is first. (Measured with
// 3 keys at Strength 0.75 after the 18 attacks made with Pillow: read both
// ways, 1215 readings of 1296 against 1190, turned by 3° 72 against 68,
// cropped to 25% 29 against 18; read only cleaned, 1209, and with gains of
// 0.75 to 1.4 on the channels of the photograph of a screen 106 of 144
// against 113. Fits over 5 x 5, 7 x 7 and 17 x 17 to 65 x 65 read less, as
// high passes less the mean of 3 x 3 or 7 x 7.)
const (
	cleanHP   = 2  // the high pass: each place less the mean of 5 x 5
	cleanFit  = 4  // the fit: over 9 x 9
	cleanRows = 32 // rows done at a time (at least cleanHP + cleanFit)
)

// CleanChroma takes from l (w x h brightness, written over) what a local
// least-squares fit of its high-pass part on the high-pass cb and cr
// explains. It reports false, and leaves l as it is, when the colour has
// next to no high-pass power (a grey picture).
func CleanChroma(l []float64, cb, cr []float32, w, h int) bool {
	if w <= 0 || h <= 0 {
		return false
	}
	const m = cleanHP + cleanFit
	load := func(v []float32, a, b int) []float64 {
		o := make([]float64, (b-a)*w)
		for i := range o {
			o[i] = float64(v[a*w+i])
		}
		return o
	}
	highPass := func(v []float64, rows int) {
		mean := boxClamp(v, w, rows, cleanHP)
		for i := range v {
			v[i] -= mean[i]
		}
	}
	// The ridge: a two-hundredth of the colour's high-pass power.
	var e float64
	for y0 := 0; y0 < h; y0 += cleanRows {
		y1 := min(h, y0+cleanRows)
		a, b := max(0, y0-cleanHP), min(h, y1+cleanHP)
		B, C := load(cb, a, b), load(cr, a, b)
		highPass(B, b-a)
		highPass(C, b-a)
		for i := (y0 - a) * w; i < (y1-a)*w; i++ {
			e += float64(B[i]*B[i]) + float64(C[i]*C[i])
		}
	}
	e /= float64(w * h)
	if !(e > 0.25) {
		return false
	}
	lam := float64(0.005 * e)
	// Strip by strip; a strip is written back once the next is done, which
	// reads at most m rows of it.
	var pend []float64
	pendY := 0
	for y0 := 0; y0 < h; y0 += cleanRows {
		y1 := min(h, y0+cleanRows)
		a, b := max(0, y0-m), min(h, y1+m)
		rows := b - a
		Y := make([]float64, rows*w)
		copy(Y, l[a*w:b*w])
		B, C := load(cb, a, b), load(cr, a, b)
		highPass(Y, rows)
		highPass(B, rows)
		highPass(C, rows)
		fit := func(p, q []float64) []float64 {
			o := make([]float64, len(p))
			for i := range o {
				o[i] = float64(p[i] * q[i])
			}
			return boxClamp(o, w, rows, cleanFit)
		}
		bb, cc, bc, yb, yc := fit(B, B), fit(C, C), fit(B, C), fit(Y, B), fit(Y, C)
		out := make([]float64, (y1-y0)*w)
		for y := y0; y < y1; y++ {
			o := (y - a) * w
			for x := range w {
				i := o + x
				b11, b22 := bb[i]+lam, cc[i]+lam
				det := float64(b11*b22) - float64(bc[i]*bc[i])
				ka := (float64(yb[i]*b22) - float64(yc[i]*bc[i])) / det
				kb := (float64(yc[i]*b11) - float64(yb[i]*bc[i])) / det
				out[(y-y0)*w+x] = l[y*w+x] - float64(ka*B[i]) - float64(kb*C[i])
			}
		}
		if pend != nil {
			copy(l[pendY*w:], pend)
		}
		pend, pendY = out, y0
	}
	copy(l[pendY*w:], pend)
	return true
}
