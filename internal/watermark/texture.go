package watermark

import "math"

// fineTexture is the texture (TextureMeter) from which a small picture's
// blind mark has chips of a pixel (see smallSide). A busy picture hides
// the many fine chips and keeps them through compression; on a smooth one
// WebP and JPEG wipe them out of the flat areas, and the fewer larger
// chips read better. (Measured on 37 pictures, the Kodak ones, smooth
// synthetic ones and the five of the docs, made 448 to 192 pixels long,
// each read as it is and after JPEG 80, WebP 80 and JPEG 50: of 740
// readings, chips of 2 pixels always 354, of a pixel always 520, of a pixel
// from a texture of 4 572; the photographs' textures were 4.4 and up, the
// smooth pictures' 1.4 to 3.6.)
const fineTexture = 4.0

// NeedsTexture reports whether the mark of a picture of w x h pixels with
// these options depends on its texture: the blind mark of a small picture
// whose chips would be the kind's smallest.
func NeedsTexture(o Options, w, h int) bool {
	if o.Kind != Blind || min(w, h) >= smallSide {
		return false
	}
	density := o.Density
	if density == 0 {
		density = kinds[Blind].density
	}
	return float64(min(w, h))/float64(density*N) < kinds[Blind].minChip
}

// TextureMeter measures a picture's texture from its rows of NRGBA pixels
// in order: the mean, over the places not on its edge, of the standard
// deviation of the luma (0 to 255) over the 3 x 3 pixels around each.
type TextureMeter struct {
	w          int
	rows       [3][]float64 // the luma of the last three rows
	n          int          // rows seen
	sum        float64
	count      int
	colS, colQ []float64 // per column: sum and sum of squares down the three rows
}

// NewTextureMeter makes a TextureMeter for rows of w pixels.
func NewTextureMeter(w int) *TextureMeter {
	t := &TextureMeter{w: w, colS: make([]float64, w), colQ: make([]float64, w)}
	for i := range t.rows {
		t.rows[i] = make([]float64, w)
	}
	return t
}

// Row takes the next row (4 bytes a pixel, alpha left aside).
func (t *TextureMeter) Row(pix []uint8) {
	cur := t.rows[t.n%3]
	for x := range t.w {
		p := pix[4*x : 4*x+3 : 4*x+3]
		cur[x] = float64(0.299*float64(p[0])) + float64(0.587*float64(p[1])) + float64(0.114*float64(p[2]))
	}
	t.n++
	if t.n < 3 || t.w < 3 {
		return
	}
	a, b, c := t.rows[(t.n-3)%3], t.rows[(t.n-2)%3], t.rows[(t.n-1)%3]
	for x := range t.w {
		t.colS[x] = a[x] + b[x] + c[x]
		t.colQ[x] = float64(a[x]*a[x]) + float64(b[x]*b[x]) + float64(c[x]*c[x])
	}
	for x := 1; x < t.w-1; x++ {
		s := t.colS[x-1] + t.colS[x] + t.colS[x+1]
		q := t.colQ[x-1] + t.colQ[x] + t.colQ[x+1]
		mean := s / 9
		t.sum += math.Sqrt(max(0, q/9-float64(mean*mean)))
		t.count++
	}
}

// Mean is the texture of the rows so far (0 for none).
func (t *TextureMeter) Mean() float64 {
	if t.count == 0 {
		return 0
	}
	return t.sum / float64(t.count)
}
