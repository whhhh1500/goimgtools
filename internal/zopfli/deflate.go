package zopfli

import "unsafe"

// Options are Zopfli's settings.
type Options struct {
	NumIterations     int  // runs of the optimal parse of each block (Zopfli's default 15)
	BlockSplitting    bool // split the data into blocks where that saves bits
	BlockSplittingMax int  // blocks at most (0: no limit; Zopfli's default 15)
}

// DefaultOptions are Zopfli's defaults.
func DefaultOptions() Options {
	return Options{NumIterations: 15, BlockSplitting: true, BlockSplittingMax: 15}
}

// A Compressor compresses with Zopfli. It keeps its memory, made for data of
// up to a length, from one call to the next; one goroutine at a time.
type Compressor struct {
	opts Options
	max  int // the longest data it has memory for

	h   *hash
	lmc matchCache
	q   squeezer
	// The parse of all the data (or first the greedy parse that splits it).
	whole lz77Store
}

// hashBytes is the memory of the hash chains.
const hashBytes = 2*65536*4 + 2*windowSize*2 + 2*windowSize*4 + windowSize*2

// Bytes is the memory a Compressor takes for data of up to maxInput bytes
// (beyond the dictionary): about 44 bytes for each, and 1MB. It is made
// whole at once, for the worst case (Zopfli makes it as it goes, for the
// parts the data is split into and the items of their parse: 27MB at the
// peak on 1MB of a photograph).
func Bytes(maxInput int) int64 {
	n := max(maxInput, 1)
	return hashBytes + cacheBytes(n) + squeezerBytes(n) + storeBytes(n) + int64(unsafe.Sizeof(Compressor{}))
}

// New makes a Compressor for data of up to maxInput bytes; it takes
// Bytes(maxInput) of memory. Longer data is compressed too, with memory
// made for it then.
func New(opts Options, maxInput int) *Compressor {
	c := &Compressor{opts: opts, h: newHash(windowSize)}
	c.grow(max(maxInput, 1))
	return c
}

func (c *Compressor) grow(n int) {
	c.max = n
	c.lmc = makeMatchCache(n)
	c.q = makeSqueezer(n, c.h)
	c.whole = makeStore(n)
}

// Compress appends to dst in[start:] compressed as raw deflate blocks, and
// returns it; in[:start] is the dictionary, the data before (its last 32KB
// are used). With final, the last block is marked final; without, the
// blocks end with an empty stored block, so that the output ends on a byte
// and more blocks can follow (a sync flush). The blocks are those Zopfli's
// ZopfliDeflatePart writes for the data.
func (c *Compressor) Compress(dst, in []byte, start int, final bool) []byte {
	out, _ := c.compress(dst, in, start, final, true)
	return out
}

// CompressBits is Compress without the sync flush: the blocks end where
// they end, and bits says how many bits of the last byte they take (1 to
// 8; the rest are zeros). Blocks compressed this way one after the other,
// each joined to the bits before, are the bytes ZopfliDeflate writes when
// each is a master block of it.
func (c *Compressor) CompressBits(dst, in []byte, start int, final bool) (out []byte, bits int) {
	return c.compress(dst, in, start, final, false)
}

func (c *Compressor) compress(dst, in []byte, start int, final, flush bool) ([]byte, int) {
	if start > windowSize {
		in, start = in[start-windowSize:], windowSize
	}
	if n := len(in) - start; n > c.max {
		c.grow(n)
	}
	w := &bitWriter{out: dst}
	c.deflatePart(2, final, in, start, len(in), w)
	if !final && flush {
		addNonCompressedBlock(false, nil, 0, 0, w)
	}
	if w.bp == 0 {
		return w.out, 8
	}
	return w.out, int(w.bp)
}

// MasterBlock is the length of Zopfli's master blocks: ZopfliDeflate
// compresses the data a master block at a time, each after the one before.
const MasterBlock = masterBlock

// Deflate appends data compressed as raw deflate to dst, in master blocks
// of 1MB as Zopfli makes them: the bytes of zopfli --deflate.
func Deflate(dst []byte, opts Options, data []byte) []byte {
	return DeflateBlocks(dst, opts, data, masterBlock)
}

// DeflateBlocks is Deflate with master blocks of the length given.
func DeflateBlocks(dst []byte, opts Options, data []byte, master int) []byte {
	c := New(opts, min(len(data), master))
	w := &bitWriter{out: dst}
	i := 0
	for {
		masterFinal := i+master >= len(data)
		size := master
		if masterFinal {
			size = len(data) - i
		}
		c.deflatePart(2, masterFinal, data, i, i+size, w)
		i += size
		if i >= len(data) {
			break
		}
	}
	return w.out
}

func addNonCompressedBlock(final bool, data []byte, inStart, inEnd int, w *bitWriter) {
	pos := inStart
	for {
		blockSize := 65535
		if pos+blockSize > inEnd {
			blockSize = inEnd - pos
		}
		currentFinal := pos+blockSize >= inEnd
		nlen := ^uint16(blockSize)
		b := uint32(0)
		if final && currentFinal {
			b = 1
		}
		w.addBit(b)
		w.addBit(0)
		w.addBit(0)
		w.bp = 0
		w.out = append(w.out, byte(blockSize), byte(blockSize>>8), byte(nlen), byte(nlen>>8))
		w.out = append(w.out, data[pos:pos+blockSize]...)
		if currentFinal {
			break
		}
		pos += blockSize
	}
}

func addLZ77Block(btype int, final bool, s *lz77Store, lstart, lend int, w *bitWriter) {
	var llLengths, llSymbols [numLL]uint32
	var dLengths, dSymbols [numD]uint32
	if btype == 0 {
		length := s.byteRange(lstart, lend)
		pos := 0
		if lstart != lend {
			pos = int(s.pos[lstart])
		}
		addNonCompressedBlock(final, s.data, pos, pos+length, w)
		return
	}
	f := uint32(0)
	if final {
		f = 1
	}
	w.addBit(f)
	w.addBit(uint32(btype & 1))
	w.addBit(uint32((btype & 2) >> 1))
	if btype == 1 {
		fixedTree(llLengths[:], dLengths[:])
	} else {
		dynamicLengths(s, lstart, lend, llLengths[:], dLengths[:])
		addDynamicTree(llLengths[:], dLengths[:], w)
	}
	lengthsToSymbols(llLengths[:], 15, llSymbols[:])
	lengthsToSymbols(dLengths[:], 15, dSymbols[:])
	addLZ77Data(s, lstart, lend, llSymbols[:], llLengths[:], dSymbols[:], dLengths[:], w)
	w.addHuffmanBits(llSymbols[256], llLengths[256])
}

func (c *Compressor) addLZ77BlockAutoType(final bool, s *lz77Store, lstart, lend int, w *bitWriter) {
	unc := calculateBlockSize(s, lstart, lend, 0)
	fixed := calculateBlockSize(s, lstart, lend, 1)
	dyn := calculateBlockSize(s, lstart, lend, 2)
	expensiveFixed := s.size() < 1000 || fixed <= float64(dyn*1.1)
	if lstart == lend {
		f := uint32(0)
		if final {
			f = 1
		}
		w.addBits(f, 1)
		w.addBits(1, 2)
		w.addBits(0, 7)
		return
	}
	// The parse for the fixed code is counted, and made again if the block
	// is written with it (Zopfli keeps it in a store).
	var inStart, inEnd int
	var bs *blockState
	if expensiveFixed {
		inStart = int(s.pos[lstart])
		inEnd = inStart + s.byteRange(lstart, lend)
		bs = c.blockState(inStart, inEnd, true)
		var counts symbolCounts
		c.lz77OptimalFixed(bs, s.data, inStart, inEnd, &counts)
		fixed = fixedBlockSize(&counts)
	}
	switch {
	case unc < fixed && unc < dyn:
		addLZ77Block(0, final, s, lstart, lend, w)
	case fixed < dyn:
		if expensiveFixed {
			f := uint32(0)
			if final {
				f = 1
			}
			w.addBit(f)
			w.addBit(1)
			w.addBit(0)
			fw := newFixedWriter(w)
			followPath(bs, c.h, s.data, inStart, inEnd, c.q.path, fw)
			w.addHuffmanBits(fw.llSymbols[256], fw.llLengths[256])
		} else {
			addLZ77Block(1, final, s, lstart, lend, w)
		}
	default:
		addLZ77Block(2, final, s, lstart, lend, w)
	}
}

// deflatePart compresses data[inStart:inEnd] as dynamic, fixed or stored
// blocks (btype 2: the best of the three for each block), the data
// before inStart serving as the dictionary.
func (c *Compressor) deflatePart(btype int, final bool, data []byte, inStart, inEnd int, w *bitWriter) {
	opts := &c.opts
	switch btype {
	case 0:
		addNonCompressedBlock(final, data, inStart, inEnd, w)
		return
	case 1:
		store := &c.whole
		store.reset(data)
		bs := c.blockState(inStart, inEnd, true)
		c.lz77OptimalFixed(bs, data, inStart, inEnd, store)
		addLZ77Block(1, final, store, 0, store.size(), w)
		return
	}
	var splitUnc []int
	if opts.BlockSplitting {
		splitUnc = c.blockSplit(data, inStart, inEnd, opts.BlockSplittingMax)
	}
	npoints := len(splitUnc)
	split := make([]int, npoints)
	lz77 := &c.whole
	lz77.reset(data)
	totalCost := 0.0
	for i := 0; i <= npoints; i++ {
		start, end := inStart, inEnd
		if i > 0 {
			start = splitUnc[i-1]
		}
		if i < npoints {
			end = splitUnc[i]
		}
		base := lz77.size()
		bs := c.blockState(start, end, true)
		c.lz77Optimal(bs, data, start, end, opts.NumIterations, lz77)
		totalCost += blockSizeAutoType(lz77, base, lz77.size(), lz77.size()-base)
		if i < npoints {
			split[i] = lz77.size()
		}
	}
	if opts.BlockSplitting && npoints > 1 {
		split2 := c.blockSplitLZ77(lz77, opts.BlockSplittingMax)
		totalCost2 := 0.0
		for i := 0; i <= len(split2); i++ {
			start, end := 0, lz77.size()
			if i > 0 {
				start = split2[i-1]
			}
			if i < len(split2) {
				end = split2[i]
			}
			totalCost2 += calculateBlockSizeAutoType(lz77, start, end)
		}
		if totalCost2 < totalCost {
			split = split2
		}
	}
	npoints = len(split)
	for i := 0; i <= npoints; i++ {
		start, end := 0, lz77.size()
		if i > 0 {
			start = split[i-1]
		}
		if i < npoints {
			end = split[i]
		}
		c.addLZ77BlockAutoType(i == npoints && final, lz77, start, end, w)
	}
}
