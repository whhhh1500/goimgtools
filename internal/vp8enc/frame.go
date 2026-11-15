package vp8enc

import (
	"image"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// Tuning constants. They were set by measuring PSNR against file size on
// photographs; none of them affects the validity of the output.
const (
	// The Lagrange multiplier that trades one bit for squared error is
	// lambdaScale/256 times the square of the AC quantizer step. Chroma
	// uses a quarter of that: an error in a chroma sample is spread over
	// four pixels.
	lambdaScale   = 8
	lambdaScaleUV = 2
	// i4Tries is how many of the ten 4x4 modes get the full
	// transform-quantize-reconstruct evaluation; the rest are ruled out by
	// the size of their prediction error.
	i4Tries = 4
	// The loop filter level is filterScale/64 times the luma AC step.
	filterScale = 20
	// The chroma quantizer index is uvDeltaQ/10 below the luma one (at most
	// 15, the format's limit), for the same reason as lambdaScaleUV.
	uvDeltaQ = 3
)

// Rounding offsets of the quantizer, out of 256, for the DC and the AC
// coefficients: 128 would round to nearest, less favours smaller values.
var (
	biasY1 = [2]int32{110, 86}
	biasY2 = [2]int32{96, 90}
	biasUV = [2]int32{115, 90}
)

// qualityToIndex maps Quality to the quantizer index (0 finest .. 127),
// following libwebp: 127 * (1 - cbrt(c)), where c is quality/100 scaled to
// 2/3 of its value below 75 and 2c-1 above.
var qualityToIndex = [101]uint8{
	127, 103, 96, 92, 89, 86, 83, 81, 79, 77, 75, 73, 72, 70, 69, 68, 66, 65, 64, 63,
	62, 61, 60, 59, 58, 57, 56, 55, 54, 53, 52, 51, 51, 50, 49, 48, 48, 47, 46, 45,
	45, 44, 43, 43, 42, 41, 41, 40, 40, 39, 38, 38, 37, 37, 36, 36, 35, 35, 34, 33,
	33, 32, 32, 31, 31, 30, 30, 29, 29, 28, 28, 28, 27, 27, 26, 26, 24, 23, 22, 21,
	19, 18, 17, 16, 15, 14, 13, 12, 11, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1, 0, 0,
}

// quantizer holds the DC and AC step of one plane type.
type quantizer struct {
	q    [2]int32
	iq   [2]int32 // 1<<17 / q
	bias [2]int32
}

func (z *quantizer) set(dc, ac int, bias [2]int32) {
	z.q = [2]int32{int32(dc), int32(ac)}
	for i := range z.q {
		z.iq[i] = (1 << 17) / z.q[i]
		z.bias[i] = bias[i] << 9
	}
}

// quantize quantizes the coefficients in (raster order) from scan position
// first on. It stores the levels in lv (scan order) and what the decoder
// will get back from them in deq (raster order), and returns the scan
// position of the last non-zero level, or -1.
func (z *quantizer) quantize(in *[16]int16, first int, lv, deq *[16]int16) int {
	last := -1
	lv[0] = 0
	k := first // 0 selects the DC step, anything else the AC step
	for n := first; n < 16; n++ {
		j := zigzag[n]
		c := int32(in[j])
		neg := c < 0
		if neg {
			c = -c
		}
		l := (c*z.iq[k] + z.bias[k]) >> 17
		q := z.q[k]
		k = 1
		if l == 0 {
			lv[n], deq[j] = 0, 0
			continue
		}
		if l > maxLevel {
			l = maxLevel
		}
		d := l * q
		if neg {
			l, d = -l, -d
		}
		lv[n], deq[j] = int16(l), int16(d)
		last = n
	}
	return last
}

// mbCtx is what coding a macroblock needs to know about the one above or to
// the left of it (the same state the decoder keeps).
type mbCtx struct {
	pred   [4]uint8 // predictor modes of the 4 nearest 4x4 luma blocks
	nzMask uint8    // which of the nearest blocks have coefficients: 4 luma, 2 U, 2 V
	nzY16  uint8    // whether the last 16x16-predicted macroblock had a Y2 block
}

// cand16 is the luma of a macroblock coded with a 16x16 predictor.
type cand16 struct {
	mode   uint8
	y2     [16]int16
	y2last int
	lv     [16][16]int16
	last   [16]int8
	rec    [256]uint8
	score  int64
}

// cand4 is the luma of a macroblock coded with 4x4 predictors. Its
// reconstruction is written straight into the workspace.
type cand4 struct {
	modes [16]uint8
	lv    [16][16]int16
	last  [16]int8
}

// candUV is the chroma of a macroblock.
type candUV struct {
	mode  uint8
	lv    [8][16]int16
	last  [8]int8
	rec   [2][64]uint8
	score int64
}

type encoder struct {
	env  *pipeline.Env
	b    image.Rectangle
	w, h int
	mbw  int
	mbh  int
	dbg  *debug

	fixed int64 // budget reserved for the buffers below

	// Quantization, the same for the whole frame.
	baseQ       int
	uvDQ        int // chroma quantizer index minus baseQ
	qY1         quantizer
	qY2         quantizer
	qUV         quantizer
	lamY, lamUV int64
	filterLevel int

	// The bottom edge of the reconstruction above and the contexts the
	// macroblocks below need. The rows being coded at a time share them;
	// see rows.go for why that is safe.
	topY []uint8
	topU []uint8
	topV []uint8
	up   []mbCtx

	// The source's rows and their conversion to Y, U and V, used by one
	// row at a time, in order.
	rr   *pipeline.RowReader
	conv *converter

	// What the first pass decided: per macroblock row, its contexts, modes
	// and coefficients in the spool (see rows.go), and the totals.
	spool     *store
	rows      []rowSpan
	stats     tokenStats
	skipStats tokenStats // "end of block" bits that skipped macroblocks would need
	nSkip     int
	usedI4    int
	modeBits  int64 // bits (1/256) of the modes
	modeMax   int64 // what all modes together may use (modeBudget)
	mbShare   int64 // a macroblock's share of modeMax

	// Alpha plane, one byte per pixel, when the source may be transparent.
	alpha      *alphaSpool
	alphaStore *store

	// The second pass: the frame's token probabilities, the first
	// partition and the token partitions.
	probs    probs
	part0    *store
	parts    [8]*store // where each token partition is: its own store, or
	partOff  [8]int64  // all in one, one after the other
	ioBuf    []byte
	readBuf  []byte
	nParts   int
	partLen  [8]int64
	p0Len    int64
	useSkip  bool
	skipProb uint8
}

const (
	ioBufSize   = 4 << 10
	readBufSize = 32 << 10

	// The first partition's length field has 19 bits. modeBudget is what
	// the prediction modes may use of it, in 1/256 bit, leaving room for
	// the frame header and for the arithmetic coder being slightly less
	// efficient than the cost tables say.
	maxPartition0 = 1<<19 - 1
	modeBudget    = int64(maxPartition0-8192) * 8 * 256 * 31 / 32
)

func hasAlphaChannel(m image.Image) bool {
	switch m.(type) {
	case *image.YCbCr, *image.Gray, *image.Gray16, *image.CMYK:
		return false
	}
	return true
}

func (e *encoder) init(quality int, rr *pipeline.RowReader, src pipeline.Source) error {
	first, err := rr.Row(e.b.Min.Y)
	if err != nil {
		return err
	}
	wantAlpha := hasAlphaChannel(first) && !pipeline.IsOpaque(src)

	w16 := e.mbw * 16
	e.fixed = converterBytes(e.w, wantAlpha) +
		int64(w16+4) + int64(w16) + // top rows
		int64(e.mbw)*6 + // contexts
		int64(e.mbh)*rowBytes + // per row: its place in the spool, mode budget
		ioBufSize + readBufSize
	if err := e.env.Budget.Reserve(e.fixed, "webp encoder rows"); err != nil {
		e.fixed = 0
		return err
	}
	e.rr = rr
	e.spool = newStore(e.env)
	if wantAlpha {
		e.alphaStore = newStore(e.env)
		e.alpha = newAlphaSpool(e.alphaStore, e.w)
	}
	e.conv = newConverter(e.b, w16, e.alpha)
	e.topY = make([]uint8, w16+4)
	e.topU = make([]uint8, w16/2)
	e.topV = make([]uint8, w16/2)
	e.up = make([]mbCtx, e.mbw)
	e.rows = make([]rowSpan, e.mbh)
	e.ioBuf = make([]byte, ioBufSize)
	e.readBuf = make([]byte, readBufSize)
	if e.dbg != nil && e.dbg.rec {
		e.dbg.y = make([]uint8, w16*16*e.mbh)
		e.dbg.u = make([]uint8, w16/2*8*e.mbh)
		e.dbg.v = make([]uint8, w16/2*8*e.mbh)
	}

	q := int(qualityToIndex[quality])
	e.baseQ = q
	dc, ac := int(dequantTableDC[q]), int(dequantTableAC[q])
	e.qY1.set(dc, ac, biasY1)
	e.qY2.set(dc*2, max(ac*155/100, 8), biasY2)
	e.uvDQ = -min(15, (uvDeltaQ*q+5)/10)
	uvq := q + e.uvDQ
	e.qUV.set(int(dequantTableDC[min(uvq, 117)]), int(dequantTableAC[min(uvq, 127)]), biasUV)
	e.lamY = lambdaScale * int64(ac*ac)
	e.lamUV = lambdaScaleUV * int64(e.qUV.q[1]*e.qUV.q[1])
	e.filterLevel = min(ac*filterScale/64, 63)
	if e.dbg != nil && e.dbg.noFilter {
		e.filterLevel = 0
	}

	e.modeMax = modeBudget
	if e.dbg != nil && e.dbg.modeBudget != 0 {
		e.modeMax = e.dbg.modeBudget
	}
	e.mbShare = e.modeMax / (int64(e.mbw) * int64(e.mbh))
	return nil
}

func (e *encoder) release() {
	for _, s := range []*store{e.spool, e.alphaStore, e.part0} {
		s.Close()
	}
	for i, s := range e.parts {
		s.Close() // (closing a store twice does nothing)
		e.parts[i] = nil
	}
	e.spool, e.alphaStore, e.part0 = nil, nil, nil
	e.env.Budget.Release(e.fixed)
	e.fixed = 0
}
