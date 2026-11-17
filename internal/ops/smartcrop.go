package ops

import (
	"errors"
	"image"
	"image/color"
	"image/draw"
	"io"
	"math"

	"github.com/whhhh1500/goimgtools/internal/resize"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// What a smart crop keeps (libvips' VIPS_INTERESTING_*).
const (
	InterestingEntropy   = 1 + iota // the part with the most detail
	InterestingAttention            // the part that draws the eye
)

// SmartCrop crops src to w x h (each at most src's side) where what it
// shows is most interesting, by libvips' smartcrop method (sharp's
// strategy.entropy and strategy.attention):
//
//   - InterestingEntropy takes slices off the sides in up to eight steps
//     (along the side that has more to lose), each time the one of the two
//     opposite slices whose levels say less: the lower entropy of their
//     histograms, all channels counted together.
//   - InterestingAttention shrinks the image to 32 x 32 and scores each
//     place for edges (a Laplacian of the luminance Y, times 5), skin
//     tones (how near the direction of its XYZ colour is to that of skin,
//     from smartcrop.js) and redness (L*a*b* a*), the last two where Y is
//     above 5; it blurs the scores over about a tenth of the crop and
//     centres the crop on the best.
//
// The whole of src is held in memory (charged to the budget) while the
// place is chosen; colours are scored multiplied by alpha, as libvips
// does, so that what is hidden does not count.
func SmartCrop(src pipeline.Source, w, h, mode int) (pipeline.Source, error) {
	b := src.Bounds()
	if w < 1 || h < 1 || w > b.Dx() || h > b.Dy() {
		return nil, errors.New("goimgtools: smart crop area outside the image")
	}
	if mode != InterestingEntropy && mode != InterestingAttention {
		return nil, errors.New("goimgtools: unknown smart crop")
	}
	env := pipeline.EnvOf(src)
	var img image.Image
	var reserved int64
	for {
		s, err := src.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			env.Budget.Release(reserved)
			return nil, err
		}
		if img == nil {
			reserved = pipeline.BandBytes(s, b)
			if err := env.Budget.Reserve(reserved, "smart crop image"); err != nil {
				return nil, err
			}
			img = pipeline.NewBand(s, b)
		}
		pipeline.CopyRows(img, s, s.Bounds().Min.Y, s.Bounds().Max.Y)
	}
	if img == nil {
		return nil, io.ErrUnexpectedEOF
	}
	sc := scorer{img: img, b: b, row: image.NewRGBA(image.Rect(0, 0, b.Dx(), 1)), channels: 4}
	switch {
	case img.ColorModel() == color.GrayModel:
		sc.channels = 1
	case pipeline.IsOpaque(src):
		sc.channels = 3
	}
	var left, top int
	if mode == InterestingEntropy {
		left, top = sc.entropyCrop(w, h)
	} else {
		left, top = sc.attentionCrop(w, h)
	}
	r := image.Rect(b.Min.X+left, b.Min.Y+top, b.Min.X+left+w, b.Min.Y+top+h)
	sub := img.(interface {
		SubImage(image.Rectangle) image.Image
	}).SubImage(r)
	return &cropped{Source: pipeline.FromImage(sub, env), src: src, env: env, reserved: reserved, opaque: pipeline.IsOpaque(src)}, nil
}

// cropped is the part of the held image a smart crop keeps.
type cropped struct {
	pipeline.Source
	src      pipeline.Source
	env      *pipeline.Env
	reserved int64
	opaque   bool
}

func (c *cropped) Opaque() bool { return c.opaque }

func (c *cropped) Close() error {
	c.env.Budget.Release(c.reserved)
	c.reserved = 0
	c.Source.Close()
	return c.src.Close()
}

// scorer reads the held image for a smart crop, a row at a time, as
// 8-bit colours multiplied by alpha.
type scorer struct {
	img      image.Image
	b        image.Rectangle
	row      *image.RGBA
	channels int // counted: 1 grey, 3 colour, 4 colour and alpha
}

// rgba returns the pixels x0..x1 of row y (from the image's corner).
func (s *scorer) rgba(y, x0, x1 int) []uint8 {
	r := image.Rect(x0, 0, x1, 1)
	draw.Draw(s.row, r, s.img, s.b.Min.Add(image.Pt(x0, y)), draw.Src)
	return s.row.Pix[4*x0 : 4*x1]
}

// entropy is libvips' vips_hist_entropy of the histogram of the area
// (x, y, w, h): -sum p*log2(p) over the levels of all channels, p each
// count over the count of all.
func (s *scorer) entropy(x, y, w, h int) float64 {
	var hist [4][256]int
	for j := y; j < y+h; j++ {
		p := s.rgba(j, x, x+w)
		for i := 0; i+3 < len(p); i += 4 {
			if s.channels == 1 {
				hist[0][p[i]]++
				continue
			}
			hist[0][p[i]]++
			hist[1][p[i+1]]++
			hist[2][p[i+2]]++
			if s.channels == 4 {
				hist[3][p[i+3]]++
			}
		}
	}
	total := float64(w * h * s.channels)
	e := 0.0
	for c := 0; c < s.channels; c++ {
		for _, n := range hist[c] {
			if n > 0 {
				p := float64(n) / total
				e -= p * math.Log2(p)
			}
		}
	}
	return e
}

// entropyCrop is libvips' vips_smartcrop_entropy.
func (s *scorer) entropyCrop(tw, th int) (left, top int) {
	W, H := s.b.Dx(), s.b.Dy()
	maxSlice := max(int(math.Ceil(float64(W-tw)/8)), int(math.Ceil(float64(H-th)/8)))
	for W > tw || H > th {
		if sw := min(W-tw, maxSlice); sw > 0 {
			l := s.entropy(left, top, sw, H)
			r := s.entropy(left+W-sw, top, sw, H)
			W -= sw
			if l < r {
				left += sw
			}
		}
		if sh := min(H-th, maxSlice); sh > 0 {
			t := s.entropy(left, top, W, sh)
			bo := s.entropy(left, top+H-sh, W, sh)
			H -= sh
			if t < bo {
				top += sh
			}
		}
	}
	return left, top
}

// attentionCrop is libvips' vips_smartcrop_attention, the image shrunk to
// 32 x 32 by averaging.
func (s *scorer) attentionCrop(tw, th int) (left, top int) {
	const n = 32
	W, H := s.b.Dx(), s.b.Dy()
	// The image shrunk to n x n as libvips' resize does it: averaged over
	// blocks of a whole number of pixels (half the scale or less), then
	// reduced the rest of the way with Lanczos3, 8 bits throughout.
	sx, sy := max(1, W/(2*n)), max(1, H/(2*n))
	bw, bh := max(1, int(math.Round(float64(W)/float64(sx)))), max(1, int(math.Round(float64(H)/float64(sy))))
	blocks := image.NewNRGBA(image.Rect(0, 0, bw, bh))
	var acc []float64
	for by := 0; by < bh; by++ {
		acc = append(acc[:0], make([]float64, 3*bw)...)
		for y := by * sy; y < (by+1)*sy; y++ {
			p := s.rgba(min(y, H-1), 0, W)
			for bx := 0; bx < bw; bx++ {
				for x := bx * sx; x < (bx+1)*sx; x++ {
					q := p[4*min(x, W-1):]
					acc[3*bx] += float64(q[0])
					acc[3*bx+1] += float64(q[1])
					acc[3*bx+2] += float64(q[2])
				}
			}
		}
		for bx := 0; bx < bw; bx++ {
			d := blocks.Pix[blocks.PixOffset(bx, by):]
			for c := 0; c < 3; c++ {
				d[c] = uint8(math.Round(acc[3*bx+c] / float64(sx*sy)))
			}
			d[3] = 255
		}
	}
	var grid image.Image = blocks
	if bw != n || bh != n {
		small, err := resize.Scale(pipeline.FromImage(blocks, nil), n, n, resize.Lanczos3)
		if err == nil {
			grid, err = pipeline.Collect(small)
			small.Close()
		}
		if err != nil {
			grid = blocks
		}
	}
	var sum [n * n][3]float64
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			c := color.NRGBAModel.Convert(grid.At(grid.Bounds().Min.X+min(x, grid.Bounds().Dx()-1), grid.Bounds().Min.Y+min(y, grid.Bounds().Dy()-1))).(color.NRGBA)
			sum[y*n+x] = [3]float64{float64(c.R), float64(c.G), float64(c.B)}
		}
	}
	// XYZ (D65, Y of white 100), and the scores.
	var Y, score [n * n]float64
	var X, Z [n * n]float64
	for k := range sum {
		r, g, b := srgbLinear(sum[k][0]/255), srgbLinear(sum[k][1]/255), srgbLinear(sum[k][2]/255)
		X[k] = 100 * (0.4124*r + 0.3576*g + 0.1805*b)
		Y[k] = 100 * (0.2126*r + 0.7152*g + 0.0722*b)
		Z[k] = 100 * (0.0193*r + 0.1192*g + 0.9505*b)
	}
	at := func(x, y int) float64 { return math.Trunc(Y[max(0, min(y, n-1))*n+max(0, min(x, n-1))]) }
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			k := y*n + x
			// Edges: a Laplacian of Y in whole numbers, times 5.
			edge := math.Abs(5 * (4*at(x, y) - at(x-1, y) - at(x+1, y) - at(x, y-1) - at(x, y+1)))
			score[k] = edge
			if Y[k] <= 5 {
				continue
			}
			// Skin: the direction of the XYZ colour against skin's.
			m := math.Sqrt(X[k]*X[k] + Y[k]*Y[k] + Z[k]*Z[k])
			dx, dy, dz := X[k]/m-0.78, Y[k]/m-0.57, Z[k]/m-0.44
			score[k] += 100 - 100*math.Sqrt(dx*dx+dy*dy+dz*dz)
			// Saturation as libvips counts it: the a* of L*a*b*.
			score[k] += labA(X[k], Y[k], Z[k])
		}
	}
	// Blurred over about a tenth of the crop, at the scale of the grid.
	sigma := math.Max(math.Hypot(float64(tw)*n/float64(W), float64(th)*n/float64(H))/10, 1)
	blurred := gaussGrid(score[:], n, sigma)
	best, bx, by := math.Inf(-1), 0, 0
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			if v := blurred[y*n+x]; v > best {
				best, bx, by = v, x, y
			}
		}
	}
	ax, ay := bx*W/n, by*H/n
	left = max(0, min(ax-tw/2, W-tw))
	top = max(0, min(ay-th/2, H-th))
	return left, top
}

// labA is the a* of an XYZ colour (D65, Y of white 100).
func labA(x, y, z float64) float64 {
	f := func(t float64) float64 {
		if t > 216.0/24389 {
			return math.Cbrt(t)
		}
		return (24389.0/27*t + 16) / 116
	}
	return 500 * (f(x/95.047) - f(y/100))
}

// gaussGrid blurs an n x n grid with a Gaussian of sigma, the edges
// repeated; the mask reaches as far as its weight is 0.2 or more of the
// centre's, as libvips' gaussmat does by default.
func gaussGrid(v []float64, n int, sigma float64) []float64 {
	r := 0
	for float64(r+1)*float64(r+1) <= 2*sigma*sigma*math.Log(5) {
		r++
	}
	k := make([]float64, 2*r+1)
	total := 0.0
	for i := range k {
		d := float64(i - r)
		k[i] = math.Exp(-d * d / (2 * sigma * sigma))
		total += k[i]
	}
	for i := range k {
		k[i] /= total
	}
	tmp := make([]float64, len(v))
	out := make([]float64, len(v))
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			s := 0.0
			for i, w := range k {
				s += w * v[y*n+max(0, min(x+i-r, n-1))]
			}
			tmp[y*n+x] = s
		}
	}
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			s := 0.0
			for i, w := range k {
				s += w * tmp[max(0, min(y+i-r, n-1))*n+x]
			}
			out[y*n+x] = s
		}
	}
	return out
}

// srgbLinear is the linear light of an sRGB value from 0 to 1.
func srgbLinear(v float64) float64 {
	if v <= 0.04045 {
		return v / 12.92
	}
	return math.Pow((v+0.055)/1.055, 2.4)
}
