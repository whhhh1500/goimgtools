package vp8enc

import (
	"errors"
	"math"
	"sync/atomic"
)

// Segments: the frame's macroblocks are sorted into up to four segments,
// each with a quantizer and a loop filter level of its own (RFC 6386
// section 9.3). Macroblocks whose coefficients spread over many
// frequencies (texture, which hides coding errors) get a coarser quantizer,
// flat ones (where errors show as blocks and bands) a finer one: what
// libwebp calls spatial noise shaping, with its measure of how a
// macroblock's coefficients spread, the same clustering into segments and
// the same mapping to quantizers. Choosing the segments needs every
// macroblock first, so the image is converted and kept (in the yuv store)
// before anything is coded, and its macroblocks are measured (in parallel,
// from the source samples around them) between the two.

const (
	numSegments = 4

	// snsStrength is how far the quantizers of the segments spread, 0 to
	// 100; libwebp's default.
	snsStrength = 50
	snsToDQ     = 0.9

	maxAlpha     = 255 // susceptibilities are 0..maxAlpha
	coeffBins    = 32  // histogram of coefficient magnitudes (>>3)
	kMeansRounds = 6
)

// segment is what the macroblocks of one segment are coded with.
type segment struct {
	q           int // quantizer index
	alpha, beta int // susceptibility of the segment (centred, and from 0)
	qY1, qY2    quantizer
	qUV         quantizer
	lamY, lamUV int64
	filter      int
}

// yuvRowBytes is the size of a macroblock row of samples: 16 rows of luma
// and 8 of each chroma plane, padded to whole macroblocks.
func (e *encoder) yuvRowBytes() int64 { return int64(e.mbw*16) * 24 }

// readAll converts the whole source into macroblock rows of samples in the
// yuv store (and its alpha into the alpha spool).
func (e *encoder) readAll() error {
	w16 := e.mbw * 16
	n := e.yuvRowBytes()
	if err := e.env.Budget.Reserve(n, "webp input row"); err != nil {
		return err
	}
	defer e.env.Budget.Release(n)
	buf := make([]uint8, n)
	y, u, v := buf[:w16*16], buf[w16*16:w16*20], buf[w16*20:]
	e.conv.into(y, u, v)
	for r := 0; r < e.mbh; r++ {
		rows := min(16, e.h-r*16)
		for k := 0; k < rows; k++ {
			yy := e.b.Min.Y + r*16 + k
			m, err := e.rr.Row(yy)
			if err != nil {
				return err
			}
			if mb := m.Bounds(); mb.Min.X != e.b.Min.X || mb.Max.X != e.b.Max.X {
				return errors.New("goimgtools: webp: strip does not span the image width")
			}
			if err := e.conv.row(m, yy, k, yy == e.b.Max.Y-1); err != nil {
				return err
			}
		}
		e.conv.pad(rows)
		if _, err := e.yuv.Write(buf); err != nil {
			return err
		}
	}
	// Let the source finish verifying its input.
	if err := e.rr.Drain(); err != nil {
		return err
	}
	// From here on the rows are read from several goroutines.
	return e.yuv.settle()
}

// readRow reads macroblock row r of samples into buf.
func (e *encoder) readRow(r int, buf []uint8) error {
	_, err := e.yuv.ReadAt(buf, int64(r)*e.yuvRowBytes())
	return err
}

// rowAnalysis is what measuring macroblock rows found: how many
// macroblocks have each susceptibility.
type rowAnalysis struct {
	alphas [maxAlpha + 1]int32
}

// analyse measures every macroblock, sorts them into segments and sets up
// the segments' quantizers.
func (e *encoder) analyse(quality int) error {
	nmb := int64(e.mbw) * int64(e.mbh)
	if err := e.hold(nmb, "webp segment map"); err != nil {
		return err
	}
	e.segMap = make([]uint8, nmb)

	// Each goroutine measures rows of its own: the samples of the row and
	// the last ones of the row above.
	w16 := e.mbw * 16
	need := e.yuvRowBytes() + int64(w16)*2 + 1024
	jobs := min(e.mbh, 1+e.env.Helpers())
	at := jobs
	if e.env.Budget.Reserve(need*int64(at), "webp analysis rows") != nil {
		at = 1
		if err := e.env.Budget.Reserve(need, "webp analysis rows"); err != nil {
			return err
		}
	}
	defer e.env.Budget.Release(need * int64(at))
	// Each goroutine counts the susceptibilities of its rows; the counts
	// are added up afterwards.
	errs := make([]error, at)
	counts := make([]rowAnalysis, at)
	var next atomic.Int64
	worker := func(i int) {
		a := newAnalyser(e)
		for {
			r := int(next.Add(1) - 1)
			if r >= e.mbh {
				return
			}
			if err := a.row(r, &counts[i]); err != nil {
				errs[i] = err
				return
			}
		}
	}
	if at == 1 {
		worker(0)
	} else {
		e.env.Parallel(at, worker)
	}
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	var alphas [maxAlpha + 1]int64
	for i := range counts {
		for a, n := range counts[i].alphas {
			alphas[a] += int64(n)
		}
	}
	e.assignSegments(&alphas)
	e.setSegments(quality)
	return nil
}

// analyser measures macroblocks.
type analyser struct {
	e         *encoder
	buf       []uint8 // the row's samples
	topY      []uint8 // the last luma row of the row above
	topU      []uint8
	topV      []uint8
	ws        workspace
	ysrc      [256]uint8
	usrc      [64]uint8
	vsrc      [64]uint8
	pred      [256]uint8
	predC     [2][64]uint8
	lastAbove int // the row whose last samples topY holds, -1 for none
}

func newAnalyser(e *encoder) *analyser {
	w16 := e.mbw * 16
	return &analyser{e: e, buf: make([]uint8, e.yuvRowBytes()),
		topY: make([]uint8, w16), topU: make([]uint8, w16/2), topV: make([]uint8, w16/2), lastAbove: -1}
}

// row measures macroblock row r into ra and records each macroblock's
// susceptibility in the segment map (to be replaced by its segment).
func (a *analyser) row(r int, ra *rowAnalysis) error {
	e := a.e
	w16 := e.mbw * 16
	if r > 0 {
		// The source samples just above the row: the last luma row and the
		// last chroma rows of the row above.
		off := int64(r-1) * e.yuvRowBytes()
		if _, err := e.yuv.ReadAt(a.topY, off+int64(w16)*15); err != nil {
			return err
		}
		if _, err := e.yuv.ReadAt(a.topU, off+int64(w16)*16+int64(w16/2)*7); err != nil {
			return err
		}
		if _, err := e.yuv.ReadAt(a.topV, off+int64(w16)*20+int64(w16/2)*7); err != nil {
			return err
		}
	}
	if err := e.readRow(r, a.buf); err != nil {
		return err
	}
	y, u, v := a.buf[:w16*16], a.buf[w16*16:w16*20], a.buf[w16*20:]
	for x := 0; x < e.mbw; x++ {
		for j := 0; j < 16; j++ {
			copy(a.ysrc[j*16:j*16+16], y[j*w16+x*16:])
		}
		for j := 0; j < 8; j++ {
			copy(a.usrc[j*8:j*8+8], u[j*(w16/2)+x*8:])
			copy(a.vsrc[j*8:j*8+8], v[j*(w16/2)+x*8:])
		}
		a.borders(x, r, y, u, v)
		// The susceptibility: how far the coefficients of the better of
		// the DC and TrueMotion predictions spread, luma and chroma mixed
		// 3 to 1, turned so that higher means easier to code.
		best, bestUV := -1, -1
		for _, mode := range [2]uint8{predDC, predTM} {
			predLarge(&a.ws, wsY, 16, mode, x, r, a.pred[:])
			var h coeffHist
			for n := 0; n < 16; n++ {
				off := (n>>2)*64 + (n&3)*4
				h.add(a.ysrc[off:], 16, a.pred[off:], 16)
			}
			best = max(best, h.alpha())
			predLarge(&a.ws, wsB, 8, mode, x, r, a.predC[0][:])
			predLarge(&a.ws, wsR, 8, mode, x, r, a.predC[1][:])
			var hc coeffHist
			for n := 0; n < 4; n++ {
				off := (n>>1)*32 + (n&1)*4
				hc.add(a.usrc[off:], 8, a.predC[0][off:], 8)
				hc.add(a.vsrc[off:], 8, a.predC[1][off:], 8)
			}
			bestUV = max(bestUV, hc.alpha())
		}
		mixed := (3*best + bestUV + 2) >> 2
		alpha := min(max(maxAlpha-mixed, 0), maxAlpha)
		ra.alphas[alpha]++
		e.segMap[r*e.mbw+x] = uint8(alpha)
	}
	return nil
}

// borders loads the workspace with the source samples above and to the
// left of macroblock (mbx, mby), or the values the format puts at the
// frame's edges, as the coder's prepare does with its reconstruction.
func (a *analyser) borders(mbx, mby int, y, u, v []uint8) {
	w16 := a.e.mbw * 16
	ws := &a.ws
	if mbx == 0 {
		for j := 0; j < 17; j++ {
			ws[j*bps+7] = 0x81
		}
		for j := 17; j < 26; j++ {
			ws[j*bps+7] = 0x81
			ws[j*bps+23] = 0x81
		}
	} else {
		for j := 0; j < 16; j++ {
			ws[(j+1)*bps+7] = y[j*w16+mbx*16-1]
		}
		for j := 0; j < 8; j++ {
			ws[(j+18)*bps+7] = u[j*(w16/2)+mbx*8-1]
			ws[(j+18)*bps+23] = v[j*(w16/2)+mbx*8-1]
		}
		if mby > 0 {
			ws[7] = a.topY[mbx*16-1]
			ws[17*bps+7] = a.topU[mbx*8-1]
			ws[17*bps+23] = a.topV[mbx*8-1]
		}
	}
	if mby == 0 {
		for x := 7; x < 28; x++ {
			ws[x] = 0x7f
		}
		for x := 7; x < 16; x++ {
			ws[17*bps+x] = 0x7f
		}
		for x := 23; x < 32; x++ {
			ws[17*bps+x] = 0x7f
		}
	} else {
		copy(ws[8:24], a.topY[16*mbx:16*mbx+16])
		copy(ws[17*bps+8:17*bps+16], a.topU[8*mbx:8*mbx+8])
		copy(ws[17*bps+24:17*bps+32], a.topV[8*mbx:8*mbx+8])
	}
}

// coeffHist counts the magnitudes of the coefficients of residual blocks.
type coeffHist [coeffBins]int32

// add counts the coefficients of the 4x4 difference src-pred.
func (h *coeffHist) add(src []uint8, sStride int, pred []uint8, pStride int) {
	var out [16]int16
	fdct(src, sStride, pred, pStride, &out)
	for _, c := range out {
		v := int(c)
		if v < 0 {
			v = -v
		}
		h[min(v>>3, coeffBins-1)]++
	}
}

// alpha is how far the coefficients spread: the largest magnitude that
// occurs against how often the commonest one does.
func (h *coeffHist) alpha() int {
	most, last := int32(0), 1
	for k, n := range h {
		if n > 0 {
			most = max(most, n)
			last = k
		}
	}
	if most <= 1 {
		return 0
	}
	return 2 * maxAlpha * last / int(most)
}

// assignSegments clusters the susceptibilities into segments (k-means over
// their histogram), maps every macroblock to the nearest and sets the
// segments' alpha and beta.
func (e *encoder) assignSegments(alphas *[maxAlpha + 1]int64) {
	lo, hi := 0, maxAlpha
	for lo < maxAlpha && alphas[lo] == 0 {
		lo++
	}
	for hi > lo && alphas[hi] == 0 {
		hi--
	}
	var centers [numSegments]int
	for k := range centers {
		centers[k] = lo + (2*k+1)*(hi-lo)/(2*numSegments)
	}
	var near [maxAlpha + 1]uint8
	avg := 0
	for round := 0; round < kMeansRounds; round++ {
		var sum, cnt [numSegments]int64
		n := 0
		for a := lo; a <= hi; a++ {
			if alphas[a] == 0 {
				continue
			}
			for n+1 < numSegments && abs(a-centers[n+1]) < abs(a-centers[n]) {
				n++
			}
			near[a] = uint8(n)
			sum[n] += int64(a) * alphas[a]
			cnt[n] += alphas[a]
		}
		moved, wsum, wtotal := 0, int64(0), int64(0)
		for k := range centers {
			if cnt[k] == 0 {
				continue
			}
			c := int((sum[k] + cnt[k]/2) / cnt[k])
			moved += abs(centers[k] - c)
			centers[k] = c
			wsum += int64(c) * cnt[k]
			wtotal += cnt[k]
		}
		avg = int((wsum + wtotal/2) / wtotal)
		if moved < 5 {
			break
		}
	}
	for i, a := range e.segMap {
		e.segMap[i] = near[a]
	}
	// Each segment's susceptibility against the average (alpha) and the
	// least (beta), scaled to the spread of the centres.
	mn, mx := centers[0], centers[0]
	for _, c := range centers {
		mn, mx = min(mn, c), max(mx, c)
	}
	if mx == mn {
		mx = mn + 1
	}
	for k, c := range centers {
		s := &e.segs[k]
		s.alpha = min(max(255*(c-avg)/(mx-mn), -127), 127)
		s.beta = min(max(255*(c-mn)/(mx-mn), 0), 255)
	}
	e.nSegs = numSegments
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// setSegments gives each segment its quantizer (finer for the easier ones),
// lambdas and filter level, merges the segments that came out the same and
// sets the probabilities the segment map is coded with.
func (e *encoder) setSegments(quality int) {
	// libwebp's mapping of quality to compression, raised to a power that
	// follows the segment's susceptibility.
	c := float64(quality) / 100
	if c < 0.75 {
		c *= 2.0 / 3
	} else {
		c = 2*c - 1
	}
	base := math.Cbrt(c)
	amp := snsToDQ * snsStrength / 100.0 / 128
	for k := 0; k < e.nSegs; k++ {
		s := &e.segs[k]
		expn := 1 - amp*float64(s.alpha)
		s.q = min(max(int(127*(1-math.Pow(base, expn))), 0), 127)
		e.setQuant(s)
	}
	// Segments that came out alike are one.
	var remap [numSegments]uint8
	n := 1
	for k := 1; k < e.nSegs; k++ {
		j := 0
		for j < n && (e.segs[j].q != e.segs[k].q || e.segs[j].filter != e.segs[k].filter) {
			j++
		}
		remap[k] = uint8(j)
		if j == n {
			e.segs[n] = e.segs[k]
			n++
		}
	}
	if n < e.nSegs {
		for i, s := range e.segMap {
			e.segMap[i] = remap[s]
		}
		e.nSegs = n
	}
	var count [numSegments]int64
	for _, s := range e.segMap {
		count[s]++
	}
	prob := func(a, b int64) uint8 {
		if a+b == 0 {
			return 255
		}
		return uint8((255*a + (a+b)/2) / (a + b))
	}
	e.segProb = [3]uint8{prob(count[0]+count[1], count[2]+count[3]), prob(count[0], count[1]), prob(count[2], count[3])}
	e.updateMap = e.nSegs > 1 && e.segProb != [3]uint8{255, 255, 255}
	if !e.updateMap {
		for i := range e.segMap {
			e.segMap[i] = 0
		}
		e.nSegs = 1
		return
	}
	// What the map takes in the first partition comes off the room for
	// the modes.
	for k, n := range count {
		e.modeMax -= n * int64(segmentCost(k, &e.segProb))
	}
	e.mbShare = e.modeMax / (int64(e.mbw) * int64(e.mbh))
}

// segmentCost is the cost of coding segment k in the map, in 1/256 bit.
func segmentCost(k int, p *[3]uint8) int {
	if k < 2 {
		return bitCost(false, p[0]) + bitCost(k == 1, p[1])
	}
	return bitCost(true, p[0]) + bitCost(k == 3, p[2])
}

// setQuant sets a segment's quantizers, lambdas and loop filter level from
// its quantizer index.
func (e *encoder) setQuant(s *segment) {
	q := s.q
	dc, ac := int(dequantTableDC[q]), int(dequantTableAC[q])
	s.qY1.set(dc, ac, biasY1)
	s.qY2.set(dc*2, max(ac*155/100, 8), biasY2)
	uvq := min(max(q+e.uvDQ, 0), 127)
	s.qUV.set(int(dequantTableDC[min(uvq, 117)]), int(dequantTableAC[uvq]), biasUV)
	s.lamY = lambdaScale * int64(ac*ac)
	s.lamUV = lambdaScaleUV * int64(s.qUV.q[1]*s.qUV.q[1])
	// Segments of flatter macroblocks are filtered less (as libwebp does).
	s.filter = min(ac*filterScale*384/(64*(256+s.beta)), 63)
	if e.dbg != nil && e.dbg.noFilter {
		s.filter = 0
	}
}
