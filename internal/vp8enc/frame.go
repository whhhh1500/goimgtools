package vp8enc

import (
	"image"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// Tuning constants. They were set by measuring file size at equal
// SSIMULACRA2 and equal butteraugli (decoded by libwebp) on photographs,
// against libwebp's best settings (cwebp -m 6 -af -sharp_yuv); none of
// them affects the validity of the output.
const (
	// The Lagrange multiplier that trades one bit for squared error is
	// lambdaScale/2560 times the square of the AC quantizer step for luma,
	// and lambdaScaleUV/256 times that of the chroma step for chroma.
	lambdaScale   = 32
	lambdaScaleUV = 4
	// i4Tries is how many of the ten 4x4 modes get the full
	// transform-quantize-reconstruct evaluation; the rest are ruled out by
	// the size of their prediction error.
	i4Tries = 4
	// The loop filter level is filterScale/64 times the luma AC step (the
	// level the measuring starts from, see autofilter.go).
	filterScale = 16
	// The chroma quantizer index is uvDeltaQ/10 below the luma one (at most
	// 15, the format's limit).
	uvDeltaQ = 1
)

// Rounding offsets of the quantizer, out of 256, for the DC and the AC
// coefficients: 128 would round to nearest, less favours smaller values.
var (
	biasY1 = [2]int32{110, 70}
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
	derr  [2][3]int8 // DC errors to diffuse (dcdiffuse.go)
}

type encoder struct {
	env  *pipeline.Env
	b    image.Rectangle
	w, h int
	mbw  int
	mbh  int
	dbg  *debug

	effort  int
	sharp   bool // convert colours with sharp YUV (sharp.go)
	trellis bool // quantize 4x4 blocks by trellis (trellis.go)
	// trellisFinal: the trellis only for the 4x4 mode chosen (effort 3).
	trellisFinal bool
	autoFilter   bool // choose the filter levels by measuring (autofilter.go)

	fixed int64 // budget reserved for the buffers below

	// Quantization: the segments (see analysis.go) and which one each
	// macroblock belongs to.
	baseQ     int
	uvDQ      int // chroma quantizer index minus the luma one
	segs      [numSegments]segment
	nSegs     int
	segMap    []uint8
	segProb   [3]uint8 // the probabilities the map is coded with
	updateMap bool     // whether the map is coded (more than one segment)

	// The bottom edge of the reconstruction above and the contexts the
	// macroblocks below need. The rows being coded at a time share them;
	// see rows.go for why that is safe.
	topY []uint8
	topU []uint8
	topV []uint8
	// The chroma DC errors each macroblock leaves to the one below, with
	// diffusion (dcdiffuse.go), or nil.
	topDerr [][2][2]int8
	up      []mbCtx

	// The source's rows, their conversion to Y, U and V, and the converted
	// macroblock rows (see readAll).
	rr   *pipeline.RowReader
	conv *converter
	yuv  *store

	// What the first pass decided: per macroblock row, its contexts, modes
	// and coefficients in the spool (see rows.go), and the totals.
	spool     *store
	rows      []rowSpan
	stats     tokenStats
	skipStats tokenStats // "end of block" bits that skipped macroblocks would need
	nSkip     int
	usedI4    int
	lf        filterStats // with autoFilter: SSIM per segment and filter level
	modeBits  int64       // bits (1/256) of the modes
	modeMax   int64       // what all modes together may use (modeBudget)
	mbShare   int64       // a macroblock's share of modeMax

	// Alpha plane, one byte per pixel, when the source may be transparent,
	// and the plane coded by the lossless encoder (alphall.go).
	alpha      *alphaSpool
	alphaStore *store
	alphaLL    *alphaLL

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
	yuvInMemory = 256 << 10 // the largest converted image kept in memory

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
	e.sharp = e.effort >= 3 && useSharp(first) && (e.dbg == nil || !e.dbg.noSharp)
	e.trellis = e.effort >= 3
	// Effort 3 chooses the 4x4 modes by plain quantization and codes the
	// one chosen with the trellis (libwebp's method 5); 4 and up weigh
	// every mode tried with the trellis: 15% faster at 3, files about 1%
	// larger at the same look.
	e.trellisFinal = e.effort == 3
	e.autoFilter = e.effort >= 5 && (e.dbg == nil || !e.dbg.noFilter && !e.dbg.noAutoFilter)

	w16 := e.mbw * 16
	e.fixed = converterBytes(e.w, wantAlpha) +
		int64(w16+4) + int64(w16) + // top rows
		int64(e.mbw)*6 + // contexts
		int64(e.mbw)*4 + // chroma DC errors (dcdiffuse.go)
		int64(e.mbh)*rowBytes + // per row: its place in the spool, mode budget
		ioBufSize + readBufSize
	if err := e.env.Budget.Reserve(e.fixed, "webp encoder rows"); err != nil {
		e.fixed = 0
		return err
	}
	e.rr = rr
	e.yuv = newStore(e.env)
	// The converted image is only read back, row by row: beyond a small
	// image it costs nothing measurable on disk, and memory comes first.
	e.yuv.expect(int64(e.mbh)*e.yuvRowBytes(), yuvInMemory)
	e.spool = newStore(e.env)
	if wantAlpha {
		e.alphaStore = newStore(e.env)
		e.alpha = newAlphaSpool(e.alphaStore, e.w)
	}
	e.conv = newConverter(e.b, w16, e.alpha)
	e.conv.noClean = e.dbg != nil && e.dbg.noClean
	e.topY = make([]uint8, w16+4)
	e.topU = make([]uint8, w16/2)
	e.topV = make([]uint8, w16/2)
	if quality <= dcDiffuseQuality && (e.dbg == nil || !e.dbg.noDiffuse) {
		e.topDerr = make([][2][2]int8, e.mbw)
	}
	e.up = make([]mbCtx, e.mbw)
	e.rows = make([]rowSpan, e.mbh)
	e.ioBuf = make([]byte, ioBufSize)
	e.readBuf = make([]byte, readBufSize)
	if e.dbg != nil && e.dbg.rec {
		e.dbg.y = make([]uint8, w16*16*e.mbh)
		e.dbg.u = make([]uint8, w16/2*8*e.mbh)
		e.dbg.v = make([]uint8, w16/2*8*e.mbh)
	}

	// The quantizer of the quality; the segments' are set around it
	// once the macroblocks are measured.
	q := int(qualityToIndex[quality])
	e.baseQ = q
	e.uvDQ = -min(15, (uvDeltaQ*q+5)/10)
	e.segs[0].q, e.nSegs = q, 1
	e.setQuant(&e.segs[0])

	e.modeMax = modeBudget
	if e.dbg != nil && e.dbg.modeBudget != 0 {
		e.modeMax = e.dbg.modeBudget
	}
	e.mbShare = e.modeMax / (int64(e.mbw) * int64(e.mbh))
	return nil
}

// hold reserves n bytes of the budget until the encoder is released.
func (e *encoder) hold(n int64, what string) error {
	if err := e.env.Budget.Reserve(n, what); err != nil {
		return err
	}
	e.fixed += n
	return nil
}

func (e *encoder) release() {
	for _, s := range []*store{e.yuv, e.spool, e.alphaStore, e.part0} {
		s.Close()
	}
	e.yuv = nil
	for i, s := range e.parts {
		s.Close() // (closing a store twice does nothing)
		e.parts[i] = nil
	}
	e.spool, e.alphaStore, e.part0 = nil, nil, nil
	if e.alphaLL != nil {
		e.alphaLL.st.Close()
		e.alphaLL = nil
	}
	e.env.Budget.Release(e.fixed)
	e.fixed = 0
}
