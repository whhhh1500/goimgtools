// Package deflate is a DEFLATE compressor that finds close to the smallest
// encoding: a port to Go of the near-optimal parsing of libdeflate
// (https://github.com/ebiggers/libdeflate, lib/deflate_compress.c, levels
// 10 to 12; Copyright 2016 Eric Biggers, Copyright 2024 Google LLC, MIT
// licence, see licenses/LICENSE-LIBDEFLATE).
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

// Params are the settings of a level of libdeflate's near-optimal parsing.
type Params struct {
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
	// passes at most (see split.go).
	NoSkip      bool
	SplitByCost bool
	SplitPasses int
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
	return matchfinderBytes + 4*int64(p.cacheLength()) + 4*int64(maxBlock+maxMatch+1) + 4*int64(maxBlock) + 4<<10 +
		maxSplitBlocks*int64(unsafe.Sizeof(codes{})) + 3*8*(maxSplitBlocks+1)
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

	mf        btMatchfinder
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
	precode struct {
		freqs       [numPrecodeSyms]uint32
		codewords   [numPrecodeSyms]uint32
		lens        [numPrecodeSyms]uint8
		items       [numLitlenSyms + numOffsetSyms]uint32
		numLitlen   int
		numOffset   int
		numExplicit int
		numItems    int
	}
	lenCodewords [maxMatch + 1]uint32
	lenLens      [maxMatch + 1]uint8

	w bitWriter

	// Splitting by cost (see split.go).
	partCodes [maxSplitBlocks]codes
	bounds    []int
	ends      []int
	done      []bool
}

// New makes a compressor; it takes p.Bytes() of memory.
func New(p Params) *Compressor {
	maxBlock := p.maxBlockLength()
	return &Compressor{
		p:         p,
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
		c.compress(in, start, final)
	}
	if !final {
		c.w.add(0, 3)
		c.w.align()
		c.w.out = append(c.w.out, 0, 0, 0xff, 0xff)
	}
	c.w.align()
	out := c.w.out
	c.w = bitWriter{}
	return out
}
