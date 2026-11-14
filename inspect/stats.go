package inspect

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
	"sort"
	"strings"

	"github.com/whhhh1500/goimgtools"
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
// as greyscale or with a palette is made from.
//
// The image is read a few rows at a time; what is kept is a small table of
// colours (about 100KB), whatever the size of the image. The operations of
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
