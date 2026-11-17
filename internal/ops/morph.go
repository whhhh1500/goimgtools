package ops

import (
	"encoding/binary"
	"errors"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// sharp's dilate and erode: libvips' morph with a square mask of zeros,
// inverted after. For each channel that comes to the bitwise AND (dilate)
// or OR (erode) of the square of 2·width+1 pixels around a pixel, the
// square reaching past the edges onto copies of the edge pixels. Both are
// worked out here as one pass down and one across: down, the rows of the
// square combined a pixel (four channels) at a time; across, as van Herk
// and Gil and Werman did for maxima, from the running combinations within
// blocks as wide as the square, two of which make any square, so that the
// time it takes does not grow with the width across. (libvips looks at
// every pixel of the square.)

// MaxMorphWidth is the largest width Morph takes, as sharp.
const MaxMorphWidth = 65536

// Morph gives each channel of each pixel the bitwise AND (dilate) or OR
// (erode) of the square of 2·width+1 pixels around it, as sharp's dilate
// and erode, to the byte.
func Morph(src pipeline.Source, width int, dilate bool) (pipeline.Source, error) {
	if width < 1 || width > MaxMorphWidth {
		return nil, errors.New("goimgtools: dilate and erode width must be 1 to 65536")
	}
	b := src.Bounds()
	w := b.Dx()
	// A square wider than the image reaches the same pixels as one as wide
	// (those past the edges being copies).
	rv, rh := min(width, max(b.Dy()-1, 0)), min(width, max(w-1, 0))
	return newWindow(src, rv, func() func(win [][]uint8, dst []uint8) {
		col := make([]uint32, w)
		pre, suf := make([]uint32, w+2*rh), make([]uint32, w+2*rh)
		return func(win [][]uint8, dst []uint8) { morphRow(win, dst, col, pre, suf, rh, dilate) }
	})
}

// morphRow makes a row of the result from the rows of its window.
func morphRow(win [][]uint8, dst []uint8, col, pre, suf []uint32, r int, and bool) {
	w := len(col)
	// Down: the rows of the window, each once (the copies past the edges
	// are the same rows).
	first := win[0][:4*w]
	for x := range col {
		col[x] = binary.LittleEndian.Uint32(first[4*x:])
	}
	last := &win[0][0]
	for _, row := range win[1:] {
		if &row[0] == last {
			continue
		}
		last = &row[0]
		row = row[:4*w]
		if and {
			for x := range col {
				col[x] &= binary.LittleEndian.Uint32(row[4*x:])
			}
		} else {
			for x := range col {
				col[x] |= binary.LittleEndian.Uint32(row[4*x:])
			}
		}
	}
	// Across: the row with r copies of its ends beyond them, in blocks of
	// n; pre holds the combination of each pixel with those before it in
	// its block, suf with those after. The square at x is the pixels x to
	// x+2r of that row: the end of one block and the start of the next,
	// or one whole block.
	n := 2*r + 1
	at := func(j int) uint32 { return col[min(max(j-r, 0), w-1)] }
	m := len(pre)
	for j := 0; j < m; j++ {
		v := at(j)
		if j%n != 0 {
			if and {
				v &= pre[j-1]
			} else {
				v |= pre[j-1]
			}
		}
		pre[j] = v
	}
	for j := m - 1; j >= 0; j-- {
		v := at(j)
		if j%n != n-1 && j != m-1 {
			if and {
				v &= suf[j+1]
			} else {
				v |= suf[j+1]
			}
		}
		suf[j] = v
	}
	d := dst[:4*w]
	for x := 0; x < w; x++ {
		v := suf[x]
		if and {
			v &= pre[x+2*r]
		} else {
			v |= pre[x+2*r]
		}
		binary.LittleEndian.PutUint32(d[4*x:], v)
	}
}
