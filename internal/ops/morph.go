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
		pad := make([]uint8, 4*(w+2*rh))
		var pre, suf []uint32
		if rh > maxDirectMorph {
			pre, suf = make([]uint32, w+2*rh), make([]uint32, w+2*rh)
		}
		return func(win [][]uint8, dst []uint8) { morphRow(win, dst, pad, pre, suf, w, rh, dilate) }
	})
}

// maxDirectMorph is the widest r for which morphRow combines the pixels of
// the square across one by one rather than through blocks.
const maxDirectMorph = 4

func le64(b []uint8) uint64 { return binary.LittleEndian.Uint64(b) }
func le32(b []uint8) uint32 { return binary.LittleEndian.Uint32(b) }

// morphRow makes a row of the result from the rows of its window, through
// pad, the row combined down with r copies of its ends beyond them.
func morphRow(win [][]uint8, dst, pad []uint8, pre, suf []uint32, w, r int, and bool) {
	if w == 0 {
		return
	}
	// Down: the rows of the window, each once (the copies past the edges
	// are the same rows), two pixels at a time.
	c := pad[4*r : 4*(r+w)]
	copy(c, win[0][:4*w])
	last := &win[0][0]
	for _, row := range win[1:] {
		if &row[0] == last {
			continue
		}
		last = &row[0]
		row = row[:len(c)]
		i := 0
		if and {
			for ; i+8 <= len(c); i += 8 {
				binary.LittleEndian.PutUint64(c[i:], le64(c[i:])&le64(row[i:]))
			}
		} else {
			for ; i+8 <= len(c); i += 8 {
				binary.LittleEndian.PutUint64(c[i:], le64(c[i:])|le64(row[i:]))
			}
		}
		if i < len(c) {
			if and {
				binary.LittleEndian.PutUint32(c[i:], le32(c[i:])&le32(row[i:]))
			} else {
				binary.LittleEndian.PutUint32(c[i:], le32(c[i:])|le32(row[i:]))
			}
		}
	}
	for k := 0; k < r; k++ {
		copy(pad[4*k:4*k+4], c[:4])
		copy(pad[4*(r+w+k):4*(r+w+k)+4], c[4*(w-1):])
	}
	// Across: the square at x is the pixels x to x+2r of pad.
	n := 2*r + 1
	d := dst[:4*w]
	if r <= maxDirectMorph {
		x := 0
		for ; x+2 <= w; x += 2 {
			v := le64(pad[4*x:])
			if and {
				for k := 1; k < n; k++ {
					v &= le64(pad[4*(x+k):])
				}
			} else {
				for k := 1; k < n; k++ {
					v |= le64(pad[4*(x+k):])
				}
			}
			binary.LittleEndian.PutUint64(d[4*x:], v)
		}
		for ; x < w; x++ {
			v := le32(pad[4*x:])
			for k := 1; k < n; k++ {
				if and {
					v &= le32(pad[4*(x+k):])
				} else {
					v |= le32(pad[4*(x+k):])
				}
			}
			binary.LittleEndian.PutUint32(d[4*x:], v)
		}
		return
	}
	// Wider, as van Herk and Gil and Werman: in blocks of n, pre holds the
	// combination of each pixel with those before it in its block, suf with
	// those after; the square is the end of one block and the start of the
	// next, or one whole block.
	m := w + 2*r
	for b0 := 0; b0 < m; b0 += n {
		e := min(b0+n, m)
		v := le32(pad[4*b0:])
		pre[b0] = v
		for j := b0 + 1; j < e; j++ {
			if and {
				v &= le32(pad[4*j:])
			} else {
				v |= le32(pad[4*j:])
			}
			pre[j] = v
		}
		v = le32(pad[4*(e-1):])
		suf[e-1] = v
		for j := e - 2; j >= b0; j-- {
			if and {
				v &= le32(pad[4*j:])
			} else {
				v |= le32(pad[4*j:])
			}
			suf[j] = v
		}
	}
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
