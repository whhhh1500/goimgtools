package vp8lenc

// Near-lossless coding, and the cleaning of fully transparent pixels, as
// libwebp does them with its predictor (src/enc/predictor_enc.c, BSD
// licence). Where a pixel differs from one of its four neighbours by more
// than a little, the residual of each of its channels is rounded to a
// multiple of a power of two below that difference (and at most
// 1<<NearLossless); the pixel then becomes what the decoder will make of
// the rounded residual, so that the pixels after it are predicted from what
// the decoder has and the errors do not add up. In smooth areas, where a
// change would show, nothing changes. A fully transparent pixel, whose
// colour cannot be seen, gets the colour of its prediction: a residual of
// zero but for alpha.

// lossy says what a variant may change.
type lossy struct {
	q     int  // the largest rounding of a residual, a power of two; 1 for none
	sg    bool // the pixels are subtract-green ones (red and blue offsets from green)
	gray  bool // the stream carries green only: red and blue must stay offsets of 0
	clean bool // fully transparent pixels may change colour
}

func (l *lossy) on() bool { return l.q > 1 || l.clean }

// residual returns the residual of pixel v predicted as p, and the pixel
// the decoder will reconstruct from it. md is the pixel's largest channel
// difference with its neighbours (maxDiffsForRow); nl says whether the
// pixel may be rounded at all (libwebp leaves the image's outer rows and
// columns and predictor 0 exact).
func (l *lossy) residual(v, p uint32, md int, nl bool) (res, rec uint32) {
	if nl && l.q > 1 && (l.clean || v>>24 != 0) {
		res = nearLossless(v, p, l.q, md, l.sg, l.gray)
		rec = addPixels(p, res)
	} else {
		res, rec = subPixels(v, p), v
	}
	if l.clean && rec>>24 == 0 {
		res &= 0xff000000
		rec = p & 0x00ffffff
	}
	return res, rec
}

// addPixels adds b to a channel by channel, modulo 256.
func addPixels(a, b uint32) uint32 {
	ag := (a & 0xff00ff00) + (b & 0xff00ff00)
	rb := (a & 0x00ff00ff) + (b & 0x00ff00ff)
	return ag&0xff00ff00 | rb&0x00ff00ff
}

// predict is predictor mode applied to one pixel: l is its left neighbour,
// tl, t and tr those above left, above and above right. It is what
// residuals subtracts.
func predict(mode uint8, l, tl, t, tr uint32) uint32 {
	switch mode {
	case 0:
		return 0xff000000
	case 1:
		return l
	case 2:
		return t
	case 3:
		return tr
	case 4:
		return tl
	case 5:
		return avg2(avg2(l, tr), t)
	case 6:
		return avg2(l, tl)
	case 7:
		return avg2(l, t)
	case 8:
		return avg2(tl, t)
	case 9:
		return avg2(t, tr)
	case 10:
		return avg2(avg2(l, tl), avg2(t, tr))
	case 11:
		if sad(tl, t) < sad(tl, l) {
			return l
		}
		return t
	case 12:
		return clampAddSub(l, t, tl)
	}
	return clampAddSubHalf(avg2(l, t), tl)
}

// maxDiff is the largest difference of a channel of a and b.
func maxDiff(a, b uint32) int {
	m := 0
	for s := 0; s < 32; s += 8 {
		d := int(a>>s&0xff) - int(b>>s&0xff)
		m = max(m, d, -d)
	}
	return m
}

// addGreen undoes subtract green.
func addGreen(p uint32) uint32 {
	g := p >> 8 & 0xff
	return p&0xff00ff00 | (p&0x00ff00ff+(g<<16|g))&0x00ff00ff
}

// maxDiffsForRow sets out[x], for 0 < x < len(row)-1, to the largest
// channel difference of row[x] with its four neighbours (as libwebp's
// MaxDiffsForRow, in true colours when sg). up and down are the rows above
// and below.
func maxDiffsForRow(row, up, down []uint32, out []uint8, sg bool) {
	w := len(row)
	if w <= 2 {
		return
	}
	px := func(p uint32) uint32 {
		if sg {
			return addGreen(p)
		}
		return p
	}
	cur, right := px(row[0]), px(row[1])
	for x := 1; x < w-1; x++ {
		left := cur
		cur, right = right, px(row[x+1])
		m := max(maxDiff(cur, px(up[x])), maxDiff(cur, left), maxDiff(cur, right))
		if down != nil {
			m = max(m, maxDiff(cur, px(down[x])))
		}
		out[x] = uint8(m)
	}
}

// nearLosslessComponent rounds the residual of one channel, value minus
// predict modulo 256, to a multiple of q (a power of two), without making
// the value cross boundary (as libwebp's NearLosslessComponent).
func nearLosslessComponent(value, predict, boundary uint8, q int) uint8 {
	residual := int(value - predict)
	boundaryResidual := int(boundary - predict)
	lower := residual &^ (q - 1)
	upper := lower + q
	// Ties go to the value nearer the prediction.
	bias := 0
	if int(boundary-value) < boundaryResidual {
		bias = 1
	}
	if residual-lower < upper-residual+bias {
		if residual > boundaryResidual && lower <= boundaryResidual {
			// Half the step, not to cross the boundary.
			return uint8(lower + q>>1)
		}
		return uint8(lower)
	}
	if residual <= boundaryResidual && upper > boundaryResidual {
		return uint8(lower + q>>1)
	}
	return uint8(upper)
}

// nearLossless is the residual of v predicted as p with every channel
// rounded to a multiple of a power of two up to maxQ and below md (as
// libwebp's NearLossless). Alpha 0 and 255 stay exact. With subtract green,
// red and blue are offsets from green, and what the rounding moved green
// by is taken off them first so that the two errors do not add up. With
// gray, only green is rounded: red and blue stay as they are, zero.
func nearLossless(v, p uint32, maxQ, md int, sg, gray bool) uint32 {
	if md <= 2 {
		return subPixels(v, p)
	}
	q := maxQ
	for q >= md {
		q >>= 1
	}
	va, pa := uint8(v>>24), uint8(p>>24)
	a := va - pa
	if va != 0 && va != 0xff {
		a = nearLosslessComponent(va, pa, 0xff, q)
	}
	g := nearLosslessComponent(uint8(v>>8), uint8(p>>8), 0xff, q)
	if gray {
		return subPixels(v, p)&0x00ff00ff | uint32(a)<<24 | uint32(g)<<8
	}
	var newGreen, greenDiff uint8
	if sg {
		newGreen = uint8(p>>8) + g
		greenDiff = newGreen - uint8(v>>8)
	}
	r := nearLosslessComponent(uint8(v>>16)-greenDiff, uint8(p>>16), 0xff-newGreen, q)
	b := nearLosslessComponent(uint8(v)-greenDiff, uint8(p), 0xff-newGreen, q)
	return uint32(a)<<24 | uint32(r)<<16 | uint32(g)<<8 | uint32(b)
}

// computeMaxDiffs fills maxDiffs for the band's rows from their pixels as
// they came (before lossyResiduals changes them), and keeps a copy of the
// band's last row for the next band's first.
func (f *frontend) computeMaxDiffs() {
	w := f.sw
	for r := 1; r <= f.nrows; r++ {
		y := f.y0 + r - 1
		if y == 0 || y == f.h-1 {
			continue // never rounded
		}
		up := f.band[(r-1)*w : r*w]
		if r == 1 {
			up = f.origPrev
		}
		down := f.ahead
		if r < f.nrows {
			down = f.band[(r+1)*w : (r+2)*w]
		}
		maxDiffsForRow(f.band[r*w:(r+1)*w], up, down, f.maxDiffs[(r-1)*w:r*w], f.sg)
	}
	copy(f.origPrev, f.band[f.nrows*w:(f.nrows+1)*w])
}

// bandTransparent reports whether a pixel of the band's new rows is fully
// transparent.
func (f *frontend) bandTransparent() bool {
	for _, p := range f.band[f.sw : (f.nrows+1)*f.sw] {
		if p>>24 == 0 {
			return true
		}
	}
	return false
}

// tileTransparent reports whether a pixel of band rows r0 to r1-1, columns
// x0 to x1-1, is fully transparent.
func (f *frontend) tileTransparent(r0, r1, x0, x1 int) bool {
	w := f.sw
	for r := r0; r < r1; r++ {
		for _, p := range f.band[r*w+x0 : r*w+x1] {
			if p>>24 == 0 {
				return true
			}
		}
	}
	return false
}

// chooseModesLossy is chooseModes for a variant that may change pixels:
// the cost of a mode is that of the residuals it leaves once rounded, each
// block's rows taken in order with the pixels changed as the decoder will
// see them (as libwebp's GetBestPredictorForTile). The pixels around the
// block are taken as they came. tmp holds three rows of scratch and two
// pixels.
func (f *frontend) chooseModesLossy(r0, r1 int, cm *costModel, tmp []uint32) {
	w := f.sw
	res, u, c := tmp[:w], tmp[w:2*w+1], tmp[2*w+1:3*w+2]
	row := ((f.y0 + r0 - 1) >> f.bits) * f.tw
	left := uint8(255)
	for tx := 0; tx < f.tw; tx++ {
		x0, x1 := max(tx<<f.bits, 1), min(w, (tx+1)<<f.bits)
		simulate := f.ls.q > 1 || f.ls.clean && f.tileTransparent(r0, r1, x0, x1)
		best, bestCost := f.modeSet[0], uint32(1<<32-1)
		for _, m := range f.modeSet {
			var cost uint32
			if m != left {
				cost = modeBias
			}
			for r := r0; r < r1 && cost < bestCost; r++ {
				y := f.y0 + r - 1
				if y == 0 {
					continue
				}
				cur, upRow := f.band[r*w:(r+1)*w], f.band[(r-1)*w:r*w+1]
				if !simulate {
					residuals(m, cur, upRow, x0, x1, res)
					for _, d := range res[x0:x1] {
						cost += cm.pixel(d)
					}
					continue
				}
				copy(c[x0-1:x1], cur[x0-1:x1])
				if r == r0 || y == 1 {
					copy(u[x0-1:x1], upRow[x0-1:x1])
				}
				u[x1] = upRow[x1] // outside the block: as it came
				var md []uint8
				if f.maxDiffs != nil {
					md = f.maxDiffs[(r-1)*w : r*w]
				}
				nlRow := y < f.h-1 && m != 0
				for x := x0; x < x1; x++ {
					p := predict(m, c[x-1], u[x-1], u[x], u[x+1])
					d := 0
					if md != nil {
						d = int(md[x])
					}
					var rd uint32
					rd, c[x] = f.ls.residual(c[x], p, d, nlRow && x < w-1)
					cost += cm.pixel(rd)
				}
				u, c = c, u
			}
			if cost < bestCost {
				best, bestCost = m, cost
			}
		}
		f.modes[row+tx] = best
		left = best
	}
}

// lossyResiduals makes the residuals of the band's rows one after the
// other, into resBand from off on, changing each pixel to what the decoder
// will reconstruct.
func (f *frontend) lossyResiduals(off int) {
	w := f.sw
	for r := 1; r <= f.nrows; r++ {
		res := f.resBand[off+(r-1)*w : off+r*w]
		cur := f.band[r*w : (r+1)*w]
		y := f.y0 + r - 1
		if y == 0 {
			// The first row is predicted from the left, its first pixel
			// from opaque black; it is never rounded.
			res[0], cur[0] = f.ls.residual(cur[0], 0xff000000, 0, false)
			for x := 1; x < w; x++ {
				res[x], cur[x] = f.ls.residual(cur[x], cur[x-1], 0, false)
			}
			continue
		}
		up := f.band[(r-1)*w : r*w+1] // up[w] is cur[0], the top-right of the last pixel
		res[0], cur[0] = f.ls.residual(cur[0], up[0], 0, false)
		var md []uint8
		if f.maxDiffs != nil {
			md = f.maxDiffs[(r-1)*w : r*w]
		}
		nlRow := y < f.h-1
		for tx, m := range f.modes[(y>>f.bits)*f.tw:][:f.tw] {
			x0, x1 := max(tx<<f.bits, 1), min(w, (tx+1)<<f.bits)
			for x := x0; x < x1; x++ {
				p := predict(m, cur[x-1], up[x-1], up[x], up[x+1])
				d := 0
				if md != nil {
					d = int(md[x])
				}
				res[x], cur[x] = f.ls.residual(cur[x], p, d, nlRow && m != 0 && x < w-1)
			}
		}
	}
}
