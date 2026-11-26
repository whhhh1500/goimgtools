package webpanim

import (
	"bytes"
	"image"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// What a lossy frame leaves out, after libwebp's animation encoder
// (src/mux/anim_encode.c, BSD licence): a pixel counts as unchanged when
// no channel is further from the picture than the quality allows
// (QualityToMaxDiff, PixelsAreSimilar), the frame is the rectangle around
// the pixels that are not, and in a frame without transparent pixels each
// block of that rectangle that is unchanged all over is made transparent,
// with one colour, and the frame blended onto the picture
// (FlattenSimilarBlocks): such a block takes a few bits where coding it
// again would take as many as the first time.
//
// Four things are not libwebp's.
//
// Its blocks lie on the canvas' grid of eight pixels, and the encoder's on
// the frame's; here they are the frame's, so that a block left out is a
// block of the encoder.
//
// What a frame is compared with is not the frame before but the picture as
// it was last coded, block by block (keep): a block that changes a little
// in every frame, each time by less than the allowance, is coded again
// once it has moved that far from what is shown, where compared with the
// frame before it would never be (libwebp bounds that by writing a whole
// frame every few, which costs far more).
//
// Leaving blocks out does not always pay: the mask that tells them apart
// takes bits, and what is left of the picture codes worse in pieces. Where
// little stands still, and that costs little (a slow pan over a
// photograph), the frame is smaller whole; where most does, or what does
// is dear (a dithered picture, a caption), blocks of 4x4 pixels leave out
// more than blocks of 8x8 and their mask is worth its bits. Nothing told
// the cases apart as well as trying, so a frame is encoded each way and
// the smallest kept: whole, with blocks of 8 and with blocks of 4. Not
// every frame is encoded three times: the way that won is used alone for
// the next frames, twice as many each time it wins again, eight at most
// (see add).
//
// And the loop filter stays on. libwebp turns it off for blended frames;
// here its levels are chosen by measuring (vp8enc), and with it the frames
// came out as small and nearer the source at every quality tried.

// cells are the sides of the blocks a lossy frame may leave out, each
// twice the one before.
var cells = [2]int{4, 8}

// maxDiff is how far a channel may be from the picture's for a pixel to
// count as unchanged at quality q (1 to 100): 28 levels at 1, 5 at 75, 1 at
// 100. It is libwebp's QualityToMaxDiff, 31 - 30*sqrt(q/100) rounded, in
// integers: the largest m with (63-2m)^2 >= 36q.
func maxDiff(q int) int {
	for m := 31; m > 1; m-- {
		if (63-2*m)*(63-2*m) >= 36*q {
			return m
		}
	}
	return 1
}

// similar reports whether the pixel b (four bytes of NRGBA) may stand for
// a: the same alpha, and every channel within d levels of a's, more the
// less the pixel shows (libwebp's PixelsAreSimilar).
func similar(a, b []byte, d int) bool {
	if a[3] != b[3] {
		return false
	}
	al, lim := int(b[3]), d*255
	for i := 0; i < 3; i++ {
		v := int(a[i]) - int(b[i])
		if v < 0 {
			v = -v
		}
		if v*al > lim {
			return false
		}
	}
	return true
}

// blocks tells which blocks of cell x cell pixels of a frame's rectangle
// stay as they are: a bit each, w to a row, h rows, n of them set, which
// hold sub blocks of the next smaller size.
type blocks struct {
	cell     int
	w, h     int
	n, sub   int
	bits     []uint64
	reserved int64
}

// reset makes b the blocks of a rectangle of dx x dy pixels, none still.
func (b *blocks) reset(env *pipeline.Env, cell, dx, dy int) error {
	b.cell, b.w, b.h, b.n, b.sub = cell, (dx+cell-1)/cell, (dy+cell-1)/cell, 0, 0
	words := (b.w*b.h + 63) / 64
	if len(b.bits) < words {
		b.release(env)
		if err := env.Budget.Reserve(8*int64(words), "animation blocks"); err != nil {
			return err
		}
		b.bits, b.reserved = make([]uint64, words), 8*int64(words)
	}
	clear(b.bits[:words])
	return nil
}

func (b *blocks) release(env *pipeline.Env) {
	env.Budget.Release(b.reserved)
	b.bits, b.reserved = nil, 0
}

func (b *blocks) set(bx, by int) {
	k := by*b.w + bx
	b.bits[k>>6] |= 1 << (k & 63)
	b.n++
}

func (b *blocks) still(bx, by int) bool {
	k := by*b.w + bx
	return b.bits[k>>6]>>(k&63)&1 != 0
}

// findStill notes, for the frame just read, which blocks of rect are still:
// every pixel near enough to the picture's. The small blocks are compared,
// and a block of the next size is still when all its quarters are.
func (e *encoder) findStill(rect image.Rectangle) error {
	for i, cell := range cells {
		if err := e.still[i].reset(e.env, cell, rect.Dx(), rect.Dy()); err != nil {
			return err
		}
	}
	b := &e.still[0]
	cell, stride := b.cell, 4*rect.Dx()
	buf, err := e.rowBuf(2*stride + b.w)
	if err != nil {
		return err
	}
	now, before, moved := buf[:stride], buf[stride:2*stride], buf[2*stride:2*stride+b.w]
	for by := 0; by < b.h; by++ {
		clear(moved)
		for y := rect.Min.Y + by*cell; y < min(rect.Min.Y+(by+1)*cell, rect.Max.Y); y++ {
			if _, err := e.cur.ReadAt(now, e.off(rect.Min.X, y)); err != nil {
				return err
			}
			if _, err := e.prev.ReadAt(before, e.off(rect.Min.X, y)); err != nil {
				return err
			}
			if bytes.Equal(now, before) {
				continue
			}
			for bx := range moved {
				if moved[bx] != 0 {
					continue
				}
				lo, hi := 4*cell*bx, min(4*cell*(bx+1), stride)
				if bytes.Equal(now[lo:hi], before[lo:hi]) {
					continue
				}
				for i := lo; i < hi; i += 4 {
					if !similar(before[i:i+4], now[i:i+4], e.maxDiff) {
						moved[bx] = 1
						break
					}
				}
			}
		}
		for bx, m := range moved {
			if m == 0 {
				b.set(bx, by)
			}
		}
	}
	for i := 1; i < len(cells); i++ {
		small, large := &e.still[i-1], &e.still[i]
		for by := 0; by < large.h; by++ {
			for bx := 0; bx < large.w; bx++ {
				x, y := 2*bx, 2*by
				right, below := x+1 < small.w, y+1 < small.h
				if small.still(x, y) && (!right || small.still(x+1, y)) &&
					(!below || small.still(x, y+1) && (!right || small.still(x+1, y+1))) {
					large.set(bx, by)
					large.sub++
					if right {
						large.sub++
					}
					if below {
						large.sub++
					}
					if right && below {
						large.sub++
					}
				}
			}
		}
	}
	return nil
}

// flatten makes the still blocks of a band of n rows of the frame's
// rectangle, from row y0 (a multiple of the blocks' size), transparent,
// each with the mean of its colours: none of it is seen, and one colour
// takes the fewest bits.
func (b *blocks) flatten(band []byte, stride, y0, n int) {
	cell := b.cell
	for y := 0; y < n; y += cell {
		rows := min(cell, n-y)
		for bx := 0; bx < b.w; bx++ {
			if !b.still(bx, (y0+y)/cell) {
				continue
			}
			lo, hi := 4*cell*bx, min(4*cell*(bx+1), stride)
			var r, g, bl int
			for i := 0; i < rows; i++ {
				row := band[(y+i)*stride:]
				for x := lo; x < hi; x += 4 {
					r += int(row[x])
					g += int(row[x+1])
					bl += int(row[x+2])
				}
			}
			count := rows * (hi - lo) / 4
			px := [4]byte{byte(r / count), byte(g / count), byte(bl / count), 0}
			for i := 0; i < rows; i++ {
				row := band[(y+i)*stride:]
				for x := lo; x < hi; x += 4 {
					copy(row[x:x+4], px[:])
				}
			}
		}
	}
}

// keep makes the picture the frames are compared with show the lossy frame
// just coded: the pixels of rect, but for the blocks left out (still, or
// nil for none), which stay what they were when they were last coded.
func (e *encoder) keep(rect image.Rectangle, still *blocks) error {
	stride := 4 * rect.Dx()
	buf, err := e.rowBuf(2 * stride)
	if err != nil {
		return err
	}
	now, before := buf[:stride], buf[stride:2*stride]
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		off := e.off(rect.Min.X, y)
		if _, err := e.cur.ReadAt(now, off); err != nil {
			return err
		}
		if still != nil {
			if _, err := e.prev.ReadAt(before, off); err != nil {
				return err
			}
			cell, by := still.cell, (y-rect.Min.Y)/still.cell
			for bx := 0; bx < still.w; bx++ {
				if still.still(bx, by) {
					lo, hi := 4*cell*bx, min(4*cell*(bx+1), stride)
					copy(now[lo:hi], before[lo:hi])
				}
			}
		}
		if _, err := e.prev.WriteAt(now, off); err != nil {
			return err
		}
	}
	return nil
}
