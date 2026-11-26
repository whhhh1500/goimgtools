// Package vp8enc writes lossy WebP files: a VP8 key frame (RFC 6386) in a
// RIFF container, with the alpha channel in an ALPH chunk when the image has
// transparent pixels.
//
// The source is read and converted to Y, U and V once, into a store of
// macroblock rows (in memory while small, else a temporary file), and its
// macroblocks are measured; then they are sorted into segments, each coded
// with a quantizer of its own (analysis.go). The coding works on a
// macroblock row at a time: besides that row it keeps only the bottom row
// of its own reconstruction of the row above, which intra prediction
// needs. With helpers (pipeline.Env.SetHelpers) the measuring, several
// rows and the partitions of the file are done at once; the file is the
// same either way (rows.go, replay.go).
//
// A VP8 frame cannot be written straight through: its header, which comes
// first, holds the token probabilities and the size of the mode partition,
// and those are only known once every macroblock has been coded. So what is
// decided for each macroblock (prediction modes and quantized coefficients)
// goes to a spool, itself arithmetic coded and therefore about the size of
// the final file, and a second pass reads the spool back and codes it again
// with the probabilities that fit the image. The alpha plane is spooled
// too, run-length packed. Spools live in memory while small and move to a
// pipeline.Spill, and with it to a temporary file when the budget says so.
//
// What the encoder does for compression: 16x16 and 4x4 luma prediction and
// chroma prediction chosen by rate-distortion cost, segments that spend
// the bits where errors show (flat areas finer, texture coarser, as
// libwebp's spatial noise shaping), token probabilities adapted to the
// image, the macroblock skip flag, and chroma DC errors diffused to the
// blocks after (dcdiffuse.go). From effort 3 on, colours are converted
// with sharp YUV (sharp.go) and 4x4 blocks quantized by trellis
// (trellis.go); from effort 5, the default, the loop filter levels are
// chosen by measuring (autofilter.go), as libwebp does at its best
// settings. The tuning constants (frame.go) were set on SSIMULACRA2 and
// butteraugli.
package vp8enc

import (
	"errors"
	"fmt"
	"io"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// Options are the encoding parameters.
type Options struct {
	// Quality ranges from 1 to 100; higher is better and larger. 0 means 75.
	Quality int
	// Effort trades speed for size at the same quality, 1 to 9; 0 means
	// the default (5). From 3 on, colours are converted with sharp YUV and
	// 4x4 blocks quantized by trellis; from 5 on, the loop filter levels
	// are chosen by measuring. Above 5 only the alpha plane gets more
	// (see alphall.go).
	Effort int
	// Exact keeps the samples of fully transparent pixels as they are. By
	// default, as libwebp does without its exact setting, those of the
	// blocks of 8x8 pixels they share with visible ones are made smooth
	// and those of wholly transparent blocks flat (convert.go,
	// cleanTransparent): they cannot be seen, and code much smaller.
	Exact bool
	// AlphaQuality, 1 to 99, reduces the alpha plane to fewer levels
	// before it is coded (alphaquant.go); 0 or 100 keeps it exactly.
	AlphaQuality int
	// AlphaBlocks says that the alpha plane is a mask of blocks: every
	// pixel fully transparent or opaque, and those of each block of
	// AlphaBlocks x AlphaBlocks pixels, on the grid from the top left
	// corner, alike. Such a plane is coded by a coder of its own (mask.go),
	// which takes a fraction of the lossless encoder's time and memory. A
	// plane that turns out not to be one is coded as usual.
	AlphaBlocks int
}

// DefaultQuality is used when Options is nil or Quality is 0.
const DefaultQuality = 75

// DefaultEffort is used when Options is nil or Effort is 0.
const DefaultEffort = 5

const maxDimension = 16383 // 14 bits in the frame header

// debug carries test hooks.
type debug struct {
	noFilter     bool // signal loop filter level 0
	noSharp      bool // convert colours without sharp YUV
	noAutoFilter bool // keep the filter levels that follow the quantizers
	noDiffuse    bool // no chroma DC error diffusion
	noClean      bool // keep the samples of transparent pixels
	alphaMethod  int  // 1 + the only alpha filter to try, or 0
	// When rec is set the encoder's reconstruction is stored here: planes
	// of whole macroblocks, luma stride 16*mbw, chroma stride 8*mbw.
	rec     bool
	y, u, v []uint8
	// Overrides, to exercise paths that only huge images reach.
	forceParts int   // number of token partitions (1, 2, 4 or 8)
	modeBudget int64 // room for the prediction modes, in 1/256 bit
	// What happened.
	usedI4     int   // macroblocks with 4x4 prediction
	usedSkip   int   // macroblocks coded with the skip flag
	partitions int   // token partitions
	modeBits   int64 // what the modes were estimated to take, in 1/256 bit
	p0Len      int64 // size of the first partition
	spool      int64 // size of the spool
	segments   int   // segments used
	maskBits   int64 // size of the alpha plane coded as a mask of blocks, 0 if it was not
	segQ       [4]int
}

// Encode writes src as a lossy WebP file: a RIFF container with a VP8 chunk,
// or VP8X + ALPH + VP8 when the image has transparent pixels. It does not
// close src. If it returns an error, what was written to w is not a valid
// file and must be discarded.
func Encode(w io.Writer, src pipeline.Source, o *Options) error {
	return encode(w, src, o, nil)
}

func encode(w io.Writer, src pipeline.Source, o *Options, dbg *debug) error {
	quality := DefaultQuality
	if o != nil && o.Quality != 0 {
		quality = o.Quality
	}
	if quality < 1 || quality > 100 {
		return fmt.Errorf("goimgtools: webp quality %d out of range 1..100", quality)
	}
	effort := DefaultEffort
	if o != nil && o.Effort != 0 {
		effort = o.Effort
	}
	if effort < 1 || effort > 9 {
		return fmt.Errorf("goimgtools: webp effort %d out of range 1..9", effort)
	}
	b := src.Bounds()
	if b.Dx() <= 0 || b.Dy() <= 0 {
		return errors.New("goimgtools: webp: image has no pixels")
	}
	if b.Dx() > maxDimension || b.Dy() > maxDimension {
		return fmt.Errorf("goimgtools: webp: image %dx%d is larger than %d pixels a side", b.Dx(), b.Dy(), maxDimension)
	}
	e := &encoder{env: pipeline.EnvOf(src), b: b, w: b.Dx(), h: b.Dy(), dbg: dbg, effort: effort, exact: o != nil && o.Exact}
	if o != nil {
		e.maskCell = o.AlphaBlocks
	}
	if o != nil && o.AlphaQuality != 0 {
		if o.AlphaQuality < 0 || o.AlphaQuality > 100 {
			return fmt.Errorf("goimgtools: webp alpha quality %d out of range 1..100", o.AlphaQuality)
		}
		e.alphaQ = o.AlphaQuality
	}
	e.mbw, e.mbh = (e.w+15)>>4, (e.h+15)>>4
	defer e.release()
	// The rows are pulled straight from the source's strips; an early end
	// of the source comes back as io.ErrUnexpectedEOF.
	rr := pipeline.NewRowReader(src)
	if err := e.init(quality, rr, src); err != nil {
		return err
	}
	if err := e.readAll(); err != nil {
		return err
	}
	if err := e.codeAlpha(); err != nil {
		return err
	}
	if err := e.analyse(quality); err != nil {
		return err
	}
	if err := e.firstPass(); err != nil {
		return err
	}
	return e.write(w)
}
