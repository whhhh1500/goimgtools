package ops

import (
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"io"
	"math"
	"math/bits"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// The filters that make each pixel from a square window around it in the
// colours as they are (not multiplied by alpha), channel by channel, as
// Pillow's rank filters and convolution kernels do: Median and Convolve.
// The window reaches past the edges onto copies of the edge pixels (as
// Pillow's expand before filtering). The rows of the window are held in a
// ring, 8-bit NRGBA.

// MaxWindow is the largest window side Median and Convolve take.
const MaxWindow = 99

// windowStage hands out NRGBA strips whose rows are made by row from the
// rows around them. The ring holds the rows of a strip and r more above
// and below; with helpers the rows of a strip are shared out, each part
// with its own maker (and scratch), which gives the same bytes.
type windowStage struct {
	src      pipeline.Source // the source as NRGBA strips
	env      *pipeline.Env
	w, h     int
	r        int       // rows taken above and below
	ring     [][]uint8 // source rows, row i at i%len
	have     int       // source rows in the ring so far
	strip    *image.NRGBA
	sy       int // the source row strip starts at
	out      image.NRGBA
	n, y     int // rows per strip handed out, rows handed out
	reserved int64
	opaque   bool
	mirror   bool // rows past the edges are mirrored, not repeated
	// newRow makes a function that makes one row of the result from the
	// rows of its window, one for each part of the work.
	newRow func() func(win [][]uint8, dst []uint8)
	rows   []func(win [][]uint8, dst []uint8)
	wins   [][][]uint8
}

func newWindow(src pipeline.Source, r int, newRow func() func(win [][]uint8, dst []uint8)) (*windowStage, error) {
	env := pipeline.EnvOf(src)
	b := src.Bounds()
	w := &windowStage{src: pipeline.Map(src, func(*image.NRGBA) error { return nil }), env: env,
		w: b.Dx(), h: b.Dy(), r: r, newRow: newRow, opaque: pipeline.IsOpaque(src)}
	w.n = pipeline.StripRows(int64(w.w)*4, w.h)
	ringRows := min(2*r+w.n, w.h)
	need := int64(ringRows+w.n) * int64(w.w) * 4
	if err := env.Budget.Reserve(need, "window rows"); err != nil {
		return nil, err
	}
	w.reserved = need
	w.ring = make([][]uint8, ringRows)
	for i := range w.ring {
		w.ring[i] = make([]uint8, 4*w.w)
	}
	w.out = image.NRGBA{Pix: make([]uint8, w.n*4*w.w), Stride: 4 * w.w}
	return w, nil
}

func (w *windowStage) Env() *pipeline.Env      { return w.env }
func (w *windowStage) Bounds() image.Rectangle { return w.src.Bounds() }
func (w *windowStage) ColorModel() color.Model { return color.NRGBAModel }
func (w *windowStage) Opaque() bool            { return w.opaque }

func (w *windowStage) Close() error {
	w.env.Budget.Release(w.reserved)
	w.reserved, w.ring, w.out.Pix = 0, nil, nil
	return w.src.Close()
}

// load reads source rows into the ring up to row y (from 0).
func (w *windowStage) load(y int) error {
	for w.have <= y {
		if w.strip == nil || w.have >= w.sy+w.strip.Rect.Dy() {
			s, err := w.src.Next()
			if err != nil {
				if err == io.EOF {
					err = io.ErrUnexpectedEOF
				}
				return err
			}
			w.strip, w.sy = s.(*image.NRGBA), w.have
		}
		copy(w.ring[w.have%len(w.ring)], w.strip.Pix[(w.have-w.sy)*w.strip.Stride:][:4*w.w])
		w.have++
	}
	return nil
}

func (w *windowStage) Next() (image.Image, error) {
	if w.y >= w.h {
		return nil, io.EOF
	}
	b := w.src.Bounds()
	y0, y1 := w.y, min(w.y+w.n, w.h)
	if err := w.load(min(y1-1+w.r, w.h-1)); err != nil {
		return nil, err
	}
	// Parts of at least 8K pixels each.
	parts := max(1, min(w.env.Helpers()+1, y1-y0, (y1-y0)*w.w/(8<<10)))
	for len(w.rows) < parts {
		w.rows = append(w.rows, w.newRow())
		w.wins = append(w.wins, make([][]uint8, 2*w.r+1))
	}
	w.env.Parallel(parts, func(k int) {
		row, win := w.rows[k], w.wins[k]
		for y := y0 + k*(y1-y0)/parts; y < y0+(k+1)*(y1-y0)/parts; y++ {
			for i := range win {
				sy := y - w.r + i
				if w.mirror {
					sy = mirrorIndex(sy, w.h)
				} else {
					sy = max(0, min(sy, w.h-1))
				}
				win[i] = w.ring[sy%len(w.ring)]
			}
			row(win, w.out.Pix[(y-y0)*w.out.Stride:][:4*w.w])
		}
	})
	out := w.out
	out.Pix = out.Pix[:(y1-y0)*out.Stride]
	out.Rect = image.Rect(b.Min.X, b.Min.Y+y0, b.Max.X, b.Min.Y+y1)
	w.y = y1
	if w.y == w.h {
		// Let the source finish (a decoder verifies its input at the end).
		for {
			_, err := w.src.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}
		}
	}
	return &out, nil
}

// mirrorIndex is i reflected into 0..n-1 as libvips' mirror extension
// does (the edge pixel repeated: ... b a | a b c ... z | z y ...), as often
// as it takes.
func mirrorIndex(i, n int) int {
	i %= 2 * n
	if i < 0 {
		i += 2 * n
	}
	if i >= n {
		i = 2*n - 1 - i
	}
	return i
}

// ---- CLAHE ----

// CLAHE equalizes src's histogram locally, contrast limited, as libvips'
// hist_local (sharp's clahe): each red, green and blue level becomes 255
// times the share of the width x height window around it (centred, its
// left and top halves the larger for an even side; the image mirrored past
// its edges) that is at that level or below, with every level's count cut
// at maxSlope (1 or more) and what is cut spread evenly over the 256
// levels. Transparency is kept.
func CLAHE(src pipeline.Source, width, height, maxSlope int) (pipeline.Source, error) {
	if width < 1 || height < 1 || width > 1<<12 || height > 1<<12 || maxSlope < 1 {
		return nil, errors.New("goimgtools: CLAHE wants a window of 1 to 4096 a side and a slope of 1 or more")
	}
	b := src.Bounds()
	w := b.Dx()
	ws, err := newWindow(src, height/2, func() func(win [][]uint8, dst []uint8) {
		cols := make([]int, w+width)
		for x := range cols {
			cols[x] = 4 * mirrorIndex(x-width/2, w)
		}
		return func(win [][]uint8, dst []uint8) {
			claheRow(win[:height], dst, w, width, maxSlope, cols)
		}
	})
	if err != nil {
		return nil, err
	}
	ws.mirror = true
	return ws, nil
}

// claheHist is the histogram of a channel of a CLAHE window: the counts,
// and the counts cut at the slope summed in blocks of 16 levels (what is
// cut off is the size of the window less their total). It is kept up to
// date without branches, the levels coming as the picture has them.
type claheHist struct {
	h   [256]int32
	blk [16]int32
}

func (c *claheHist) add(v uint8, lim int32) {
	h := c.h[v]
	c.blk[v>>4] += int32(uint32(h-lim) >> 31) // h < lim: one more kept
	c.h[v] = h + 1
}

func (c *claheHist) remove(v uint8, lim int32) {
	h := c.h[v]
	c.blk[v>>4] -= int32(uint32(h-lim-1) >> 31) // h <= lim: one fewer kept
	c.h[v] = h - 1
}

// level is the result for level t in a window of n pixels: libvips' sum
// (the cut counts of the levels to t, and the share of what is cut that
// falls to them) times 255 over n.
func (c *claheHist) level(t int, lim int32, n int64) uint8 {
	var kept, sum int32
	for k, b := range c.blk {
		kept += b
		if k < t>>4 {
			sum += b
		}
	}
	for _, v := range c.h[t&^15 : t+1] {
		sum += min(v, lim)
	}
	over := int32(n) - kept
	return uint8(255 * int64(sum+int32(t+1)*over/256) / n)
}

// claheRow makes a row of CLAHE from the height rows of its window; cols
// are the byte offsets of the columns from x = -width/2 on, mirrored. A
// pixel's channels are read with one load.
func claheRow(win [][]uint8, dst []uint8, w, width, slope int, cols []int) {
	var r, g, b claheHist
	lim := int32(slope)
	for x := 0; x < width; x++ {
		o := cols[x]
		for _, row := range win {
			v := binary.LittleEndian.Uint32(row[o:])
			r.add(uint8(v), lim)
			g.add(uint8(v>>8), lim)
			b.add(uint8(v>>16), lim)
		}
	}
	n := int64(width * len(win))
	centre := win[len(win)/2]
	for x := 0; x < w; x++ {
		if x > 0 {
			o, p := cols[x-1], cols[x+width-1]
			for _, row := range win {
				in := binary.LittleEndian.Uint32(row[p:])
				out := binary.LittleEndian.Uint32(row[o:])
				r.add(uint8(in), lim)
				r.remove(uint8(out), lim)
				g.add(uint8(in>>8), lim)
				g.remove(uint8(out>>8), lim)
				b.add(uint8(in>>16), lim)
				b.remove(uint8(out>>16), lim)
			}
		}
		mid := centre[4*x : 4*x+4 : 4*x+4]
		dst[4*x] = r.level(int(mid[0]), lim, n)
		dst[4*x+1] = g.level(int(mid[1]), lim, n)
		dst[4*x+2] = b.level(int(mid[2]), lim, n)
		dst[4*x+3] = mid[3]
	}
}

// ---- median ----

// Median gives each pixel, channel by channel, the middle value of the
// size x size window around it (size odd, 3 to MaxWindow), as Pillow's
// MedianFilter, to the byte. Transparency is filtered as a channel too
// (an opaque image stays opaque, its alpha untouched).
func Median(src pipeline.Source, size int) (pipeline.Source, error) {
	if size < 3 || size > MaxWindow || size%2 == 0 {
		return nil, errors.New("goimgtools: Median size must be odd, 3 to 99")
	}
	r := size / 2
	width := src.Bounds().Dx()
	channels := 4
	if pipeline.IsOpaque(src) {
		channels = 3
	}
	if size == 3 {
		return newWindow(src, 1, func() func(win [][]uint8, dst []uint8) {
			return func(win [][]uint8, dst []uint8) { median3Row(win, dst, width) }
		})
	}
	return newWindow(src, r, func() func(win [][]uint8, dst []uint8) {
		return func(win [][]uint8, dst []uint8) { medianRow(win, dst, width, r, size*size/2, channels) }
	})
}

// median3Row is medianRow for 3x3 windows: the median of nine is the
// middle of the largest of the smallest, the middle of the middles and the
// smallest of the largest of the window's three columns, each sorted, and
// a column, once sorted, serves the three windows it is in. The four
// channels of a pixel are read together; an opaque image's alpha comes out
// 255 as it went in.
func median3Row(win [][]uint8, dst []uint8, w int) {
	r0, r1, r2 := win[0][:4*w], win[1][:4*w], win[2][:4*w]
	// The sorted column at x: smallest, middle and largest of each channel,
	// a byte each.
	col := func(x int) (lo, mid, hi uint32) {
		o := 4 * x
		a := binary.LittleEndian.Uint32(r0[o:])
		b := binary.LittleEndian.Uint32(r1[o:])
		c := binary.LittleEndian.Uint32(r2[o:])
		// (In 32 bits: comparisons of bytes become branches, which the
		// data would mispredict, where those of words are conditional
		// moves.)
		for k := 0; k < 32; k += 8 {
			x, y, z := a>>k&0xff, b>>k&0xff, c>>k&0xff
			l, h := min(x, y), max(x, y)
			lo |= min(l, z) << k
			mid |= max(l, min(h, z)) << k
			hi |= max(h, z) << k
		}
		return
	}
	pl, pm, ph := col(0)
	cl, cm, ch := pl, pm, ph
	for x := 0; x < w; x++ {
		nl, nm, nh := cl, cm, ch
		if x+1 < w {
			nl, nm, nh = col(x + 1)
		}
		var v uint32
		for k := 0; k < 32; k += 8 {
			lo := max(pl>>k&0xff, cl>>k&0xff, nl>>k&0xff)
			mid := med3(pm>>k&0xff, cm>>k&0xff, nm>>k&0xff)
			hi := min(ph>>k&0xff, ch>>k&0xff, nh>>k&0xff)
			v |= med3(lo, mid, hi) << k
		}
		binary.LittleEndian.PutUint32(dst[4*x:], v)
		pl, pm, ph, cl, cm, ch = cl, cm, ch, nl, nm, nh
	}
}

func med3(a, b, c uint32) uint32 {
	return max(min(a, b), min(max(a, b), c))
}

// medianRow keeps a histogram per channel of the window, moved one column
// at a time (the column coming in counted in, the one left behind counted
// out), and the median of each with the count below it, moved up or down
// to the rank after each step (Huang's method).
func medianRow(win [][]uint8, dst []uint8, width, r, rank, channels int) {
	var hist [4][256]int32
	var med, below [4]int
	add := func(x int, d int32) {
		o := 4 * max(0, min(x, width-1))
		for _, row := range win {
			p := row[o : o+4 : o+4]
			for c := 0; c < channels; c++ {
				v := int(p[c])
				hist[c][v] += d
				// d more below the median when v is (no branch: the
				// comparison follows the picture).
				below[c] -= (v - med[c]) >> (bits.UintSize - 1) * int(d)
			}
		}
	}
	for x := -r; x <= r; x++ {
		add(x, 1)
	}
	for x := 0; x < width; x++ {
		if x > 0 {
			add(x-r-1, -1)
			add(x+r, 1)
		}
		for c := 0; c < channels; c++ {
			h, m, n := &hist[c], med[c], below[c]
			for n > rank {
				m--
				n -= int(h[m])
			}
			for n+int(h[m]) <= rank {
				n += int(h[m])
				m++
			}
			med[c], below[c] = m, n
			dst[4*x+c] = uint8(m)
		}
		if channels == 3 {
			dst[4*x+3] = win[r][4*x+3]
		}
	}
}

// ---- convolve ----

// Convolve gives each pixel, channel by channel, the sum of the window of
// width x height around it (each side odd, 1 to MaxWindow) weighted by
// kernel (row by row, the top-left weight on the top-left pixel), divided
// by scale, plus offset, kept within 0 to 255 with its fraction dropped,
// as libvips (sharp's convolve) does. The colours are taken as they are;
// transparency is kept.
func Convolve(src pipeline.Source, width, height int, kernel []float64, scale, offset float64) (pipeline.Source, error) {
	if width < 1 || height < 1 || width > MaxWindow || height > MaxWindow || width%2 == 0 || height%2 == 0 {
		return nil, errors.New("goimgtools: Convolve sides must be odd, 1 to 99")
	}
	if len(kernel) != width*height {
		return nil, errors.New("goimgtools: Convolve wants width x height weights")
	}
	for _, v := range append(kernel[:len(kernel):len(kernel)], scale, offset) {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, errors.New("goimgtools: Convolve takes finite numbers")
		}
	}
	if scale == 0 {
		return nil, errors.New("goimgtools: Convolve scale must not be 0")
	}
	rx, ry := width/2, height/2
	w := src.Bounds().Dx()
	// Whole weights (the usual kernels) are summed in integers, as libvips
	// does; others in floating point. The sum is divided by scale after:
	// dividing each weight first would leave a box blur's nine ninths a
	// hair below a whole level, which truncates down.
	whole, abs := true, 0.0
	for _, v := range kernel {
		whole = whole && v == math.Trunc(v)
		abs += math.Abs(v)
	}
	whole = whole && abs*255 < 1<<62
	ik := make([]int64, len(kernel))
	for i, v := range kernel {
		ik[i] = int64(v)
	}
	fk := append([]float64(nil), kernel...)
	return newWindow(src, ry, func() func(win [][]uint8, dst []uint8) {
		cols := make([]int, width)
		return func(win [][]uint8, dst []uint8) {
			convolveRow(win, dst, w, rx, ry, width, cols, whole, ik, fk, scale, offset)
		}
	})
}

// convolveRow makes a row of Convolve; cols is its scratch, a column offset
// for each column of the window.
func convolveRow(win [][]uint8, dst []uint8, w, rx, ry, width int, cols []int, whole bool, ik []int64, fk []float64, scale, offset float64) {
	// win has 2*ry+1 rows: those of the window.
	for x := 0; x < w; x++ {
		// The byte offsets of the window's columns, the edges repeated.
		if x < rx || x+rx >= w || x == rx {
			for i := range cols {
				cols[i] = 4 * max(0, min(x-rx+i, w-1))
			}
		} else {
			for i := range cols {
				cols[i] += 4
			}
		}
		var v [3]float64
		if whole {
			var s0, s1, s2 int64
			for j, row := range win {
				kr := ik[j*width : j*width+width]
				for i, o := range cols {
					p := row[o : o+3 : o+3]
					k := kr[i]
					s0 += k * int64(p[0])
					s1 += k * int64(p[1])
					s2 += k * int64(p[2])
				}
			}
			v = [3]float64{float64(s0), float64(s1), float64(s2)}
		} else {
			for j, row := range win {
				kr := fk[j*width : j*width+width]
				for i, o := range cols {
					p := row[o : o+3 : o+3]
					v[0] += kr[i] * float64(p[0])
					v[1] += kr[i] * float64(p[1])
					v[2] += kr[i] * float64(p[2])
				}
			}
		}
		d := dst[4*x : 4*x+4 : 4*x+4]
		d[0], d[1], d[2] = truncLevel(v[0]/scale+offset), truncLevel(v[1]/scale+offset), truncLevel(v[2]/scale+offset)
		d[3] = win[ry][4*x+3]
	}
}

// truncLevel keeps v within 0 to 255 and drops its fraction, as libvips
// casts to 8 bits.
func truncLevel(v float64) uint8 {
	return uint8(math.Max(0, math.Min(255, v)))
}
