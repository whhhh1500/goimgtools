package inspect

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
	"math"
	"sort"
	"strings"

	"github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/internal/ops"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// Statistics is what Stats found by looking at every pixel.
type Statistics struct {
	// Width and Height are the size of the image.
	Width, Height int
	// Average is the colour of the image as a whole: the mean of its
	// pixels, each counted by how opaque it is. Its alpha is the mean
	// opacity.
	Average color.NRGBA
	// Brightness is the mean brightness of the visible pixels, from 0
	// (black) to 1 (white): whether dark or light text will show on it.
	Brightness float64
	// Dominant lists the colours that cover most of the image, the most
	// frequent first, at most five. Similar shades count as one colour;
	// Share is the part of the visible pixels it covers, 0 to 1.
	Dominant []ColorShare
	// Gray reports whether the picture is in fact greyscale: no visible
	// pixel has a colour, whatever the file stores (a black and white
	// photo saved as a colour JPEG is greyscale here).
	Gray bool
	// Opaque reports whether no pixel is transparent to any degree,
	// whatever the file can store (Info.HasAlpha tells what it can).
	Opaque bool
	// Transparent is the part of the pixels that are fully transparent,
	// 0 to 1.
	Transparent float64
	// Colors is the number of different colours when there are at most
	// 256, and 0 when there are more: an image with a count here can be
	// stored with a palette without losing anything.
	Colors int
	// Channels are the statistics of the red, green, blue and alpha
	// levels of all the pixels (the transparent ones with the colour they
	// store), as sharp's stats().channels, to the last digit: a greyscale
	// image has the same in red, green and blue, an image without
	// transparency 255 throughout in alpha.
	Channels [4]ChannelStats
	// Entropy is how varied the grey levels are, in bits, from 0 (a
	// single level) to 8: the entropy of their histogram, as sharp's
	// stats().entropy (the grey is libvips' B_W, to the byte).
	Entropy float64
	// Sharpness is how much edges stand out: the standard deviation of
	// the Laplacian of the grey levels, divided by 9, the edge pixels
	// repeated past the edges, as sharp's stats().sharpness; 0 for a
	// single pixel.
	Sharpness float64
}

// ChannelStats are the statistics of the levels of one channel over an
// image, as libvips' stats (sharp's stats().channels).
type ChannelStats struct {
	// Min and Max are the lowest and highest level; MinAt and MaxAt where
	// each first occurs, row by row from the top left, which is (0, 0).
	Min, Max     uint8
	MinAt, MaxAt image.Point
	// Sum and SquaresSum add up the levels and their squares.
	Sum, SquaresSum uint64
	// Mean is the mean level and Stdev the standard deviation of the
	// levels, as of a sample (divided by the number of pixels less one,
	// as libvips does; 0 for a single pixel).
	Mean, Stdev float64
}

// ColorShare is a colour and the part of an image it covers.
type ColorShare struct {
	Color color.NRGBA
	Share float64
}

// Hex writes a colour the way web pages do: #rrggbb, or #rrggbbaa when it
// is not opaque.
func Hex(c color.NRGBA) string {
	if c.A == 255 {
		return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
	}
	return fmt.Sprintf("#%02x%02x%02x%02x", c.R, c.G, c.B, c.A)
}

// String puts the statistics in a line of words.
func (s Statistics) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "average %s, brightness %.0f%%", Hex(s.Average), 100*s.Brightness)
	if s.Gray {
		b.WriteString(", greyscale")
	}
	switch {
	case s.Opaque:
	case s.Transparent > 0:
		fmt.Fprintf(&b, ", %.1f%% fully transparent", 100*s.Transparent)
	default:
		b.WriteString(", partly transparent")
	}
	if s.Colors > 0 {
		fmt.Fprintf(&b, ", %d colours", s.Colors)
	} else {
		b.WriteString(", more than 256 colours")
	}
	if len(s.Dominant) > 0 {
		b.WriteString("; mostly")
		for i, d := range s.Dominant {
			if i > 0 {
				b.WriteString(",")
			}
			fmt.Fprintf(&b, " %s %.0f%%", Hex(d.Color), 100*d.Share)
		}
	}
	fmt.Fprintf(&b, "; entropy %.2f bits, sharpness %.2f", s.Entropy, s.Sharpness)
	return b.String()
}

// The bins of the histogram behind Dominant: the top bits of each channel.
const (
	binBits  = 4
	binCount = 1 << (3 * binBits)
)

type bin struct {
	n       uint64
	r, g, b uint64
}

// Stats reads every pixel of the image and reports its colours: the
// average, the dominant ones, whether it is really greyscale, really
// opaque, and how many colours it has. It is what a placeholder colour, a
// choice between light and dark text, or the decision to store an image
// as greyscale or with a palette is made from. It also reports what
// sharp's stats() does: the statistics of each channel, the entropy and
// the sharpness.
//
// The image is read a few rows at a time; what is kept is a small table of
// colours (about 100KB) and three rows of grey levels. The operations of
// img, if it has any, are run first.
func Stats(img *goimgtools.Image) (st Statistics, err error) {
	if img == nil {
		return Statistics{}, errors.New("goimgtools/inspect: Stats needs an image")
	}
	src, err := img.Clone().Stream(nil)
	if err != nil {
		return Statistics{}, err
	}
	defer func() { src.Close() }()
	b := src.Bounds()
	st.Width, st.Height = b.Dx(), b.Dy()
	st.Gray, st.Opaque = true, true
	w := b.Dx()
	env := pipeline.EnvOf(src)
	if err := env.Budget.Reserve(int64(3*w), "stats rows"); err != nil {
		return Statistics{}, err
	}
	defer env.Budget.Release(int64(3 * w))

	// The channels' lowest and highest levels, where they first occur,
	// and their sums.
	var lo, hi [4]int
	var loAt, hiAt [4]image.Point
	var sum, sum2 [4]uint64
	for c := range lo {
		lo[c], hi[c] = 256, -1
	}
	// The grey levels: their histogram, and the last three rows for the
	// Laplacian, whose sums are kept.
	var greyHist [256]uint64
	greys := [3][]uint8{make([]uint8, w), make([]uint8, w), make([]uint8, w)}
	rows := 0
	// How many pixels have each value of the Laplacian, -1020 to 1020.
	lapHist := make([]uint64, 2*1020+1)
	laplace := func(up, mid, down []uint8) {
		for x := range mid {
			l := int(up[x]) + int(down[x]) + int(mid[max(x-1, 0)]) + int(mid[min(x+1, w-1)]) - 4*int(mid[x])
			lapHist[l+1020]++
		}
	}

	bins := make([]bin, binCount)
	seen := map[[4]uint8]struct{}{}
	many := false
	var last [4]uint8 // the colour of the pixel before, once there is one
	haveLast := false
	var sr, sg, sb, sa, luma, visible, clear uint64
	src = pipeline.Map(src, func(strip *image.NRGBA) error {
		r := strip.Rect
		for y := r.Min.Y; y < r.Max.Y; y++ {
			row := strip.Pix[strip.PixOffset(r.Min.X, y):][:r.Dx()*4]
			gr := greys[rows%3][r.Min.X-b.Min.X:][:r.Dx()]
			ops.GreyRow(gr, row)
			for _, v := range gr {
				greyHist[v]++
			}
			// The channels: the lowest and highest levels of the row,
			// looked for only when one is new.
			mn, mx, s, s2 := channelRow(row)
			for c := range mn {
				if int(mn[c]) < lo[c] {
					i := firstLevel(row, c, mn[c])
					lo[c], loAt[c] = int(mn[c]), image.Pt(r.Min.X-b.Min.X+i, y-b.Min.Y)
				}
				if int(mx[c]) > hi[c] {
					i := firstLevel(row, c, mx[c])
					hi[c], hiAt[c] = int(mx[c]), image.Pt(r.Min.X-b.Min.X+i, y-b.Min.Y)
				}
				sum[c] += s[c]
				sum2[c] += s2[c]
			}
			for x := 0; x < len(row); x += 4 {
				p := row[x : x+4 : x+4]
				a := uint64(p[3])
				// (Most pixels are the colour of the one before them.)
				if c := [4]uint8{p[0], p[1], p[2], p[3]}; !many && (!haveLast || c != last) {
					seen[c], last, haveLast = struct{}{}, c, true
					many = len(seen) > 256
				}
				if a != 255 {
					st.Opaque = false
					if a == 0 {
						clear++
						continue
					}
				}
				if p[0] != p[1] || p[1] != p[2] {
					st.Gray = false
				}
				// Weighted by how opaque the pixel is.
				sr += uint64(p[0]) * a
				sg += uint64(p[1]) * a
				sb += uint64(p[2]) * a
				sa += a
				luma += (299*uint64(p[0]) + 587*uint64(p[1]) + 114*uint64(p[2])) * a
				visible++
				k := &bins[int(p[0])>>(8-binBits)<<(2*binBits)|int(p[1])>>(8-binBits)<<binBits|int(p[2])>>(8-binBits)]
				k.n++
				k.r += uint64(p[0])
				k.g += uint64(p[1])
				k.b += uint64(p[2])
			}
			// The row before this one has both its neighbours now.
			if rows++; rows >= 2 {
				k := rows - 2
				laplace(greys[max(k-1, 0)%3], greys[k%3], greys[(k+1)%3])
			}
		}
		return nil
	})
	for {
		if _, err := src.Next(); err == io.EOF {
			break
		} else if err != nil {
			return Statistics{}, err
		}
	}

	total := uint64(st.Width) * uint64(st.Height)
	if total == 0 {
		return st, nil
	}
	// The last row, its own neighbour below.
	if k := rows - 1; k >= 0 {
		laplace(greys[max(k-1, 0)%3], greys[k%3], greys[k%3])
	}
	n := float64(total)
	for c := range st.Channels {
		st.Channels[c] = ChannelStats{Min: uint8(lo[c]), Max: uint8(hi[c]), MinAt: loAt[c], MaxAt: hiAt[c],
			Sum: sum[c], SquaresSum: sum2[c], Mean: float64(sum[c]) / n, Stdev: deviation(float64(sum[c]), float64(sum2[c]), n)}
	}
	st.Entropy = entropy(&greyHist)
	if total > 1 {
		// Each value divided by 9 in single precision, as libvips'
		// convolution gives it.
		var lsum, lsum2 float64
		for i, k := range lapHist {
			if k > 0 {
				v := float64(float32(float64(i-1020) / 9))
				lsum += float64(float64(k) * v)
				lsum2 += float64(float64(k) * float64(v*v))
			}
		}
		st.Sharpness = deviation(lsum, lsum2, n)
	}
	st.Transparent = float64(clear) / float64(total)
	if !many {
		st.Colors = len(seen)
	}
	if sa == 0 {
		// Nothing to see: no colour, and "greyscale" would say nothing.
		st.Gray = false
		return st, nil
	}
	st.Average = color.NRGBA{uint8((sr + sa/2) / sa), uint8((sg + sa/2) / sa), uint8((sb + sa/2) / sa), uint8((sa + total/2) / total)}
	st.Brightness = float64(luma) / float64(sa) / 1000 / 255

	// The fullest bins, each as the mean of what fell into it; a bin whose
	// colour is close to one already listed is a shade of that one.
	order := make([]int, 0, 64)
	for i := range bins {
		if bins[i].n > 0 {
			order = append(order, i)
		}
	}
	sort.Slice(order, func(i, j int) bool {
		a, b := bins[order[i]].n, bins[order[j]].n
		return a > b || a == b && order[i] < order[j]
	})
	type shade struct {
		c color.NRGBA
		n uint64
	}
	var shades []shade
	for _, i := range order {
		k := bins[i]
		c := color.NRGBA{uint8(k.r / k.n), uint8(k.g / k.n), uint8(k.b / k.n), 255}
		merged := false
		for j := range shades {
			if near(shades[j].c, c) {
				shades[j].n += k.n
				merged = true
				break
			}
		}
		if !merged && len(shades) < 5 {
			shades = append(shades, shade{c, k.n})
		}
	}
	sort.SliceStable(shades, func(i, j int) bool { return shades[i].n > shades[j].n })
	for _, s := range shades {
		if share := float64(s.n) / float64(visible); share >= 0.01 || len(st.Dominant) == 0 {
			st.Dominant = append(st.Dominant, ColorShare{s.c, share})
		}
	}
	return st, nil
}

// channelRow returns the lowest and highest level of each channel of a
// row of NRGBA pixels, the sums of its levels and of their squares.
func channelRow(row []uint8) (mn, mx [4]uint8, s, s2 [4]uint64) {
	// (Comparing uint32 values compiles without branches.)
	mn0, mn1, mn2, mn3 := uint32(255), uint32(255), uint32(255), uint32(255)
	var mx0, mx1, mx2, mx3, s0, s1, s2a, s3 uint32
	var q0, q1, q2, q3 uint64
	for len(row) > 0 {
		// At most 65793 pixels a run, whose sums fit 32 bits.
		n := min(len(row), 4*65793)
		var p0, p1, p2, p3 uint64
		for i := 0; i+3 < n; i += 4 {
			p := row[i : i+4 : i+4]
			r, g, b, a := uint32(p[0]), uint32(p[1]), uint32(p[2]), uint32(p[3])
			mn0, mn1, mn2, mn3 = min(mn0, r), min(mn1, g), min(mn2, b), min(mn3, a)
			mx0, mx1, mx2, mx3 = max(mx0, r), max(mx1, g), max(mx2, b), max(mx3, a)
			s0, s1, s2a, s3 = s0+r, s1+g, s2a+b, s3+a
			p0, p1, p2, p3 = p0+uint64(r*r), p1+uint64(g*g), p2+uint64(b*b), p3+uint64(a*a)
		}
		s[0], s[1], s[2], s[3] = s[0]+uint64(s0), s[1]+uint64(s1), s[2]+uint64(s2a), s[3]+uint64(s3)
		s0, s1, s2a, s3 = 0, 0, 0, 0
		q0, q1, q2, q3 = q0+p0, q1+p1, q2+p2, q3+p3
		row = row[n:]
	}
	mn = [4]uint8{uint8(mn0), uint8(mn1), uint8(mn2), uint8(mn3)}
	mx = [4]uint8{uint8(mx0), uint8(mx1), uint8(mx2), uint8(mx3)}
	return mn, mx, s, [4]uint64{q0, q1, q2, q3}
}

// firstLevel returns the first pixel of a row of NRGBA pixels whose
// channel c is v.
func firstLevel(row []uint8, c int, v uint8) int {
	for i := c; i < len(row); i += 4 {
		if row[i] == v {
			return i / 4
		}
	}
	return 0
}

// deviation is libvips' standard deviation of n values that sum to s and
// whose squares sum to s2: that of a sample, 0 for fewer than two.
func deviation(s, s2, n float64) float64 {
	if n < 2 {
		return 0
	}
	return math.Sqrt(math.Abs(s2-float64(s*s/n)) / (n - 1))
}

// entropy is libvips' vips_hist_entropy of a histogram, -sum p log2(p),
// in its single precision (to the last digit of sharp's).
func entropy(h *[256]uint64) float64 {
	var total uint64
	for _, n := range h {
		total += n
	}
	if total == 0 {
		return 0
	}
	a, inv := float32(1/float64(total)), float32(1/math.Ln2)
	var acc float64
	for _, n := range h {
		if p := float32(a * float32(n)); p > 0 {
			acc += float64(float32(p * float32(inv*float32(math.Log(float64(p))))))
		}
	}
	return math.Abs(acc / 256 * 256)
}

// near reports whether two colours are shades of one: within a bin's width
// of each other in every channel.
func near(a, b color.NRGBA) bool {
	const d = 1<<(8-binBits) + 8
	diff := func(x, y uint8) int {
		if x > y {
			return int(x - y)
		}
		return int(y - x)
	}
	return diff(a.R, b.R) < d && diff(a.G, b.G) < d && diff(a.B, b.B) < d
}
