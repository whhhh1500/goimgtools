// Package deflate is a DEFLATE compressor that finds close to the smallest
// encoding: a port to Go of the near-optimal parsing of libdeflate
// (https://github.com/ebiggers/libdeflate, lib/deflate_compress.c, levels
// 10 to 12; Copyright 2016 Eric Biggers, Copyright 2024 Google LLC, MIT
// licence, see licenses/LICENSE-LIBDEFLATE), and of its lazy parsing
// (levels 5 to 9, see lazy.go).
//
// All the matches at each position are found with binary trees and kept;
// then, from the end of a block back to its start, the cheapest way on to
// the end is worked out for every position, with the bit costs of the
// symbols as the Huffman codes made from the previous pass's choices
// would code them, over several passes. Blocks end where the statistics
// of the data change. Each block is written with the smallest of a
// dynamic code, the static code and no compression.
//
// Unlike libdeflate, Compress takes a dictionary, the data before, and can
// end with a sync flush instead of a final block, so that a stream can be
// compressed in pieces on several cores (see pngstream's zBlocks). The
// output depends only on the input and the parameters: integer arithmetic
// throughout.
package deflate

import "unsafe"

const (
	windowOrder    = 15
	windowSize     = 1 << windowOrder
	minMatch       = 3
	maxMatch       = 258
	numLiterals    = 256
	endOfBlock     = 256
	firstLenSym    = 257
	numLitlenSyms  = 288
	numOffsetSyms  = 32
	numPrecodeSyms = 19
	maxNumSyms     = numLitlenSyms
	maxCodewordLen = 15

	maxLitlenCodewordLen = 14
	maxOffsetCodewordLen = 15
	maxPreCodewordLen    = 7

	// minBlockLength is the shortest block but the last: shorter ones are
	// not worth their header, and the block splitting does not work on
	// them.
	minBlockLength = 5000

	// bitCost scales the costs of the parse, for fractions of a bit.
	bitCost = 16
	// The bits assumed for a symbol the previous pass did not use.
	literalNostatBits = 13
	lengthNostatBits  = 13
	offsetNostatBits  = 10

	optimumLenShift   = 23
	optimumOffsetMask = 0xffff
	packedCostShift   = 9
	rebaseThreshold   = 1 << (32 - packedCostShift - 1)
	rebaseMargin      = 1 << 17

	maxMatchesPerPos = maxMatch - minMatch + 1
)

// Params are the settings of a level of libdeflate's near-optimal parsing,
// or with Lazy of its lazy parsing (which uses only SearchDepth, NiceLen
// and BlockLen).
type Params struct {
	Lazy          int // 1: lazy parsing; 2: lazy2, which looks two positions ahead
	SearchDepth   int // nodes visited in a binary tree, at most
	NiceLen       int // a match this long is taken without looking further
	Passes        int // passes of the parse, at most
	MinImprove    int // bits a pass must save for another to run
	MinNonfinal   int // bits by which the last pass must be worse to go back to the best
	MaxStaticLen  int // blocks up to this long are also parsed for the static code
	BlockLen      int // the soft maximum length of a block
	CachePerBlock int // entries of the match cache for each byte of a block

	// Not in libdeflate: NoSkip looks for matches inside a match of
	// NiceLen too, which libdeflate skips, so that the parse may start
	// the next match anywhere within it; SplitByCost splits blocks where
	// the estimated sizes say so, as zopfli does, instead of where the
	// statistics of the data change, after a parse with SplitPasses
	// passes at most (see split.go), and also tries the codes of each
	// block with their counts evened out for the header, as zopfli does
	// (see evenForRLE). EntropyCosts prices the symbols, from the second
	// pass on, at their entropy rather than at the length of their codes,
	// as zopfli does (see entropy.go).
	NoSkip       bool
	SplitByCost  bool
	SplitPasses  int
	EntropyCosts bool

	// MaxInput, when set, is the most data (beyond the dictionary) that a
	// call to Compress is given: the lazy parsing's record of a block is
	// made that long rather than BlockLen, which saves memory on small
	// inputs. The output does not depend on it.
	MaxInput int
}

// Level10, Level11 and Level12 are libdeflate's levels 10 to 12, but for
// BlockLen (libdeflate's is 300000).
var (
	Level10 = Params{SearchDepth: 35, NiceLen: 75, Passes: 2, MinImprove: 32, MinNonfinal: 32, MaxStaticLen: 0, BlockLen: 300000, CachePerBlock: 5}
	Level11 = Params{SearchDepth: 100, NiceLen: 150, Passes: 4, MinImprove: 16, MinNonfinal: 16, MaxStaticLen: 1000, BlockLen: 300000, CachePerBlock: 5}
	Level12 = Params{SearchDepth: 300, NiceLen: maxMatch, Passes: 10, MinImprove: 1, MinNonfinal: 1, MaxStaticLen: 10000, BlockLen: 300000, CachePerBlock: 5}
)

// Bytes is the memory a Compressor with the parameters takes.
func (p Params) Bytes() int64 {
	maxBlock := p.maxBlockLength()
	if p.Lazy != 0 {
		return hcBytes + 4*int64(p.lazyItems()) + int64(unsafe.Sizeof(Compressor{}))
	}
	return matchfinderBytes + 4*int64(p.cacheLength()) + 4*int64(maxBlock+maxMatch+1) + 4*int64(maxBlock) + int64(unsafe.Sizeof(Compressor{})) +
		maxSplitBlocks*int64(unsafe.Sizeof(codes{})) + 3*8*(maxSplitBlocks+1)
}

// lazyItems is the length of the lazy parsing's record of a block: one
// item for each byte at most.
func (p Params) lazyItems() int {
	if p.MaxInput > 0 {
		return min(p.maxBlockLength(), p.MaxInput)
	}
	return p.maxBlockLength()
}

func (p Params) maxBlockLength() int {
	return max(p.BlockLen+minBlockLength-1, p.BlockLen+1+maxMatch)
}

// cacheLength is the number of entries of the match cache, with room for
// the matches of the last position and the bytes it skips past.
func (p Params) cacheLength() int {
	return p.CachePerBlock*p.BlockLen + 2*(maxMatchesPerPos+maxMatch+1)
}

type costs struct {
	literal    [numLiterals]uint32
	length     [maxMatch + 1]uint32
	offsetSlot [numOffsetSyms]uint32
}

type codes struct {
	litlenCodewords [numLitlenSyms]uint32
	offsetCodewords [numOffsetSyms]uint32
	litlenLens      [numLitlenSyms]uint8
	offsetLens      [numOffsetSyms]uint8
}

type freqs struct {
	litlen [numLitlenSyms]uint32
	offset [numOffsetSyms]uint32
}

const (
	numLiteralObservationTypes = 8
	numMatchObservationTypes   = 2
	numObservationTypes        = numLiteralObservationTypes + numMatchObservationTypes
	numObservationsPerCheck    = 512
)

type splitStats struct {
	newObservations    [numObservationTypes]uint32
	observations       [numObservationTypes]uint32
	numNewObservations uint32
	numObservations    uint32
}

// A Compressor compresses with the near-optimal parsing. It can be used
// again, but by one goroutine at a time.
type Compressor struct {
	p Params

	mf        *btMatchfinder // the near-optimal parsing's
	hc        *hcMatchfinder // the lazy parsing's
	cache     []lzMatch
	costToEnd []uint32
	items     []uint32 // the choice at each position: a literal, or a match's length<<optimumLenShift | offset

	costs, costsSaved costs
	freqs             freqs
	codes             codes

	split            splitStats
	prevObservations [numObservationTypes]uint32
	prevNumObs       uint32
	newMatchLenFreqs [maxMatch + 1]uint32
	matchLenFreqs    [maxMatch + 1]uint32

	// The header of a dynamic block.
	precode      precode
	lenCodewords [maxMatch + 1]uint32
	lenLens      [maxMatch + 1]uint8

	w bitWriter

	// Splitting by cost (see split.go).
	partCodes []codes // maxSplitBlocks of them
	bounds    []int
	ends      []int
	done      []bool

	// The matchfinder after a dictionary, for CompressShared.
	shared *sharedDict
}

// sharedDict is the state of the matchfinder after a dictionary, saved by
// a call of CompressShared to be used again by the next.
type sharedDict struct {
	mf                               btMatchfinder
	base, nextSlide, maxLen, niceLen int
	nh                               [2]uint32
	save, use, valid, only           bool
}

// SharedBytes is the memory CompressShared takes beyond Bytes.
const SharedBytes = int64(unsafe.Sizeof(sharedDict{}))

// SharedAfter is how far beyond the dictionary the matchfinder looks while
// it enters it: the inputs that share a dictionary must have the same bytes
// for at least that long after it, for its state to be the same whatever
// comes after that.
const SharedAfter = maxMatch + mfRequiredBytes

const sharedAfter = SharedAfter

// New makes a compressor; it takes p.Bytes() of memory.
func New(p Params) *Compressor {
	maxBlock := p.maxBlockLength()
	if p.Lazy != 0 {
		return &Compressor{p: p, hc: new(hcMatchfinder), items: make([]uint32, p.lazyItems())}
	}
	return &Compressor{
		p:         p,
		mf:        new(btMatchfinder),
		partCodes: make([]codes, maxSplitBlocks),
		cache:     make([]lzMatch, p.cacheLength()),
		costToEnd: make([]uint32, maxBlock+maxMatch+1),
		items:     make([]uint32, maxBlock),
		bounds:    make([]int, 0, maxSplitBlocks+1),
		ends:      make([]int, 0, maxSplitBlocks+1),
		done:      make([]bool, 0, maxSplitBlocks+1),
	}
}

// staticCodes is the code of static blocks.
var staticCodes = func() *codes {
	var f freqs
	var c codes
	for i := range f.litlen {
		switch {
		case i < 144:
			f.litlen[i] = 1 << (9 - 8)
		case i < 256:
			f.litlen[i] = 1 << (9 - 9)
		case i < 280:
			f.litlen[i] = 1 << (9 - 7)
		default:
			f.litlen[i] = 1 << (9 - 8)
		}
	}
	for i := range f.offset {
		f.offset[i] = 1
	}
	makeCodes(&f, &c)
	return &c
}()

func makeCodes(f *freqs, c *codes) {
	makeHuffmanCode(maxLitlenCodewordLen, f.litlen[:], c.litlenLens[:], c.litlenCodewords[:])
	makeHuffmanCode(maxOffsetCodewordLen, f.offset[:], c.offsetLens[:], c.offsetCodewords[:])
}

// offsetSlot is the offset slot of an offset of 1 to 32768.
func offsetSlot(offset uint32) uint32 {
	return uint32(offsetSlotFull[offset])
}

// offsetSlotFull is the offset slot of every offset, as libdeflate keeps
// it for its near-optimal parsing (32KB, shared by all compressors).
var offsetSlotFull = func() *[windowSize + 1]uint8 {
	var t [windowSize + 1]uint8
	for o := uint32(1); o <= windowSize; o++ {
		n := (256 - o) >> 29 // 0 for offsets up to 256, 7 for larger
		t[o] = offsetSlotTab[(o-1)>>n] + uint8(n<<1)
	}
	return &t
}()

// Compress appends to dst in[start:] compressed as raw deflate blocks,
// and returns it; in[:start] is the dictionary, the data before (its last
// 32KB are used). With final, the last block is marked final; without,
// the blocks end with an empty stored block, so that the output ends on a
// byte and more blocks can follow (a sync flush).
func (c *Compressor) Compress(dst, in []byte, start int, final bool) []byte {
	out, _, _ := c.compressTo(dst, in, start, final, true)
	return out
}

// CompressShared is Compress for a run of inputs with the same dictionary
// and the same length (and the same bytes up to sharedAfter past the
// dictionary), such as the same data followed by each of several
// candidates for what comes next: with first, the dictionary is entered
// and the matchfinder's state after it kept; otherwise that state is used
// again instead of entering the dictionary anew (most of the time on
// short data after a long dictionary). The output is that of Compress;
// bits is how many bits of its last byte are the blocks'. The near-optimal
// parsing only.
func (c *Compressor) CompressShared(dst, in []byte, start int, final, first bool) (out []byte, bits int) {
	if c.shared == nil {
		c.shared = new(sharedDict)
	}
	d := c.shared
	if start > windowSize {
		in, start = in[start-windowSize:], windowSize
	}
	ok := c.p.Lazy == 0 && len(in)-start >= sharedAfter
	if first {
		d.valid = false
	}
	d.save, d.use = ok && first, ok && !first && d.valid
	out, bits, _ = c.compressTo(dst, in, start, final, true)
	d.save, d.use = false, false
	return out, bits
}

// EnterShared enters in[:start] as the dictionary, as CompressShared with
// first does, without compressing in[start:]: the calls of CompressShared
// that follow (with first not set), on inputs of the length of in and with
// the same bytes up to sharedAfter past the dictionary, take the
// matchfinder from there; so do those of another Compressor that takes it
// with TakeShared.
func (c *Compressor) EnterShared(in []byte, start int) {
	if c.shared == nil {
		c.shared = new(sharedDict)
	}
	d := c.shared
	d.valid = false
	if start > windowSize {
		in, start = in[start-windowSize:], windowSize
	}
	if c.p.Lazy != 0 || len(in)-start < sharedAfter {
		return
	}
	d.save, d.only = true, true
	c.compress(in, start, true)
	d.save, d.only = false, false
}

// TakeShared gives c the matchfinder's state after the dictionary src
// entered last (with CompressShared or EnterShared), for the calls of
// CompressShared that follow; c and src must have the same Params. src must
// not be in use meanwhile.
func (c *Compressor) TakeShared(src *Compressor) {
	if c.shared == nil {
		c.shared = new(sharedDict)
	}
	if src.shared == nil || !src.shared.valid {
		c.shared.valid = false
		return
	}
	*c.shared = *src.shared
	c.shared.save, c.shared.use, c.shared.only = false, false, false
}

// CompressBits is Compress without the sync flush: the blocks end where
// they end, and bits says how many bits of the last byte they take (1 to
// 8; the rest are zeros), so that blocks compressed after them can be
// joined to them bit by bit. With nothing to compress and final not set,
// nothing is appended. stored is the bit of the output (after dst) at
// which its first stored block starts, -1 if it has none: that block's
// data starts on a byte, wherever its header ends, so that joined to bits
// before it, the blocks up to its header move by those bits, its padding
// changes, and what follows it stays as it is.
func (c *Compressor) CompressBits(dst, in []byte, start int, final bool) (out []byte, bits, stored int) {
	return c.compressTo(dst, in, start, final, false)
}

func (c *Compressor) compressTo(dst, in []byte, start int, final, flush bool) ([]byte, int, int) {
	base := len(dst)
	c.w = bitWriter{out: dst}
	if start == len(in) {
		if final {
			// A static block with only the end of block.
			c.w.add(1|1<<1, 3)
			c.w.add(0, 7)
		}
	} else {
		if start > windowSize {
			in, start = in[start-windowSize:], windowSize
		}
		if c.p.Lazy != 0 {
			c.compressLazy(in, start, final)
		} else {
			c.compress(in, start, final)
		}
	}
	if !final && flush {
		c.w.add(0, 3)
		c.w.align()
		c.w.out = append(c.w.out, 0, 0, 0xff, 0xff)
	}
	bits := 8
	if r := c.w.count % 8; r != 0 {
		bits = int(r)
	}
	stored := c.w.stored - 1 - 8*base
	if c.w.stored == 0 {
		stored = -1
	}
	c.w.align()
	out := c.w.out
	c.w = bitWriter{}
	return out, bits, stored
}

// precode is the header of a dynamic block: the code of the code lengths
// and the lengths coded with it.
type precode struct {
	freqs       [numPrecodeSyms]uint32
	codewords   [numPrecodeSyms]uint32
	lens        [numPrecodeSyms]uint8
	items       [numLitlenSyms + numOffsetSyms]uint32
	numLitlen   int
	numOffset   int
	numExplicit int
	numItems    int
}
