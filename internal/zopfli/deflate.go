package zopfli

import "unsafe"

// Options are Zopfli's settings.
type Options struct {
	NumIterations     int  // runs of the optimal parse of each block (Zopfli's default 15)
	BlockSplitting    bool // split the data into blocks where that saves bits
	BlockSplittingMax int  // blocks at most (0: no limit; Zopfli's default 15)

	// Ultra (not in Zopfli; ECT's "ultra") runs the optimal parse of a
	// part again after the last run, priced by the lengths of the Huffman
	// codes of the best parse so far, while that makes it smaller.
	Ultra bool
	// MoreCodes (not in Zopfli; as ECT does) writes a dynamic block with
	// the smallest of more codes: made from its counts evened out for the
	// header as Zopfli evens them, as Brotli does, or not at all, and then
	// limited to 14 bits down to 9 while that is smaller; and its header
	// in the smallest of 32 ways rather than 8.
	MoreCodes bool
	// FineSplit (not in Zopfli) splits each block further after the optimal
	// parse, where its symbols' statistics change (see fineSplit).
	FineSplit bool
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

	own *worker
	// The parse of all the data (or first the greedy parse that splits it).
	whole lz77Store
	// With MoreCodes, room for two bits an item of a block (see writeMore).
	marks []uint64

	// More cores, and the workers made with the memory they lend (see
	// SetWorkers).
	workers Workers
	compressorSync
}

// hashBytes is the memory of the hash chains.
const hashBytes = 2*65536*4 + 2*windowSize*2 + 2*windowSize*4 + windowSize*2

// Bytes is the memory a Compressor takes for data of up to maxInput bytes
// (beyond the dictionary): about 43 bytes for each, and 1MB. It is made
// whole at once, for the worst case (Zopfli makes it as it goes, for the
// parts the data is split into and the items of their parse: 27MB at the
// peak on 1MB of a photograph).
func Bytes(maxInput int) int64 {
	n := max(maxInput, 1)
	return workerBytes(n) + storeBytes(n) + marksBytes(n) + int64(unsafe.Sizeof(Compressor{}))
}

// marksBytes is the memory MoreCodes takes for blocks of up to n items: two
// bits an item (counted whether the options ask for it or not).
func marksBytes(n int) int64 { return 2 * 8 * int64(n/64+1) }

// New makes a Compressor for data of up to maxInput bytes; it takes
// Bytes(maxInput) of memory. Longer data is compressed too, with memory
// made for it then.
func New(opts Options, maxInput int) *Compressor {
	c := &Compressor{opts: opts}
	c.grow(max(maxInput, 1))
	return c
}

func (c *Compressor) grow(n int) {
	c.max = n
	c.own = newWorker(n)
	c.own.ultra = c.opts.Ultra
	c.whole = makeStore(n)
	if c.opts.MoreCodes {
		c.marks = make([]uint64, 2*(n/64+1))
	}
}

// Compress appends to dst in[start:] compressed as raw deflate blocks, and
// returns it; in[:start] is the dictionary, the data before (its last 32KB
// are used). With final, the last block is marked final; without, the
// blocks end with an empty stored block, so that the output ends on a byte
// and more blocks can follow (a sync flush). The blocks are those Zopfli's
// ZopfliDeflatePart writes for the data.
func (c *Compressor) Compress(dst, in []byte, start int, final bool) []byte {
	out, _, _ := c.compress(dst, in, start, final, true)
	return out
}

// CompressBits is Compress without the sync flush: the blocks end where
// they end, and bits says how many bits of the last byte they take (1 to
// 8; the rest are zeros). Blocks compressed this way one after the other,
// each joined to the bits before, are the bytes ZopfliDeflate writes when
// each is a master block of it. stored is the bit of the output (after dst)
// at which its first stored block starts, -1 if it has none: that block's
// data starts on a byte, wherever its header ends (Zopfli writes all its
// master blocks into one stream), so that joined to bits before it, the
// blocks up to its header move by those bits, its padding changes, and
// what follows it stays as it is.
func (c *Compressor) CompressBits(dst, in []byte, start int, final bool) (out []byte, bits, stored int) {
	return c.compress(dst, in, start, final, false)
}

func (c *Compressor) compress(dst, in []byte, start int, final, flush bool) ([]byte, int, int) {
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
	stored := w.stored - 1 - 8*len(dst)
	if w.stored == 0 {
		stored = -1
	}
	if w.bp == 0 {
		return w.out, 8, stored
	}
	return w.out, int(w.bp), stored
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
		if w.stored == 0 {
			w.stored = 1 + w.bit()
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

// addLZ77Block writes items lstart to lend of s as a block of type btype;
// with more (room for two bits an item, see writeMore), a dynamic block's
// codes and header are chosen among more (see Options.MoreCodes).
func addLZ77Block(btype int, final bool, s *lz77Store, lstart, lend int, more []uint64, w *bitWriter) {
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
		if more != nil {
			s.writeMore(lstart, lend, more, w)
			return
		}
		dynamicLengths(s, lstart, lend, llLengths[:], dLengths[:])
		addDynamicTree(llLengths[:], dLengths[:], zopfliWays, w)
	}
	lengthsToSymbols(llLengths[:], 15, llSymbols[:])
	lengthsToSymbols(dLengths[:], 15, dSymbols[:])
	addLZ77Data(s, lstart, lend, llSymbols[:], llLengths[:], dSymbols[:], dLengths[:], w)
	w.addHuffmanBits(llSymbols[256], llLengths[256])
}

func (c *Compressor) addLZ77BlockAutoType(final bool, s *lz77Store, lstart, lend int, w *bitWriter) {
	more := c.marks
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
		bs = c.own.blockState(&c.opts, inStart, inEnd, true)
		var counts symbolCounts
		c.own.lz77OptimalFixed(bs, s.data, inStart, inEnd, &counts)
		fixed = fixedBlockSize(&counts)
	}
	switch {
	case unc < fixed && unc < dyn:
		addLZ77Block(0, final, s, lstart, lend, more, w)
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
			followPath(bs, c.own.h, s.data, inStart, inEnd, c.own.q.path, fw)
			w.addHuffmanBits(fw.llSymbols[256], fw.llLengths[256])
		} else {
			addLZ77Block(1, final, s, lstart, lend, more, w)
		}
	default:
		addLZ77Block(2, final, s, lstart, lend, more, w)
	}
}

// deflatePart compresses data[inStart:inEnd] as dynamic, fixed or stored
// blocks (btype 2: the best of the three for each block), the data
// before inStart serving as the dictionary.
func (c *Compressor) deflatePart(btype int, final bool, data []byte, inStart, inEnd int, w *bitWriter) {
	opts := &c.opts
	more := c.marks
	switch btype {
	case 0:
		addNonCompressedBlock(final, data, inStart, inEnd, w)
		return
	case 1:
		store := &c.whole
		store.reset(data)
		bs := c.own.blockState(&c.opts, inStart, inEnd, true)
		c.own.lz77OptimalFixed(bs, data, inStart, inEnd, store)
		addLZ77Block(1, final, store, 0, store.size(), more, w)
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
	// The optimal parse of each part, into a region of the parse's arrays
	// as far in as the part starts (at the same time with Workers), then
	// the regions packed together.
	bounds := append(append([]int{inStart}, splitUnc...), inEnd)
	lens, at, counts := make([]int, npoints+1), make([]int, npoints+1), make([]int, npoints+1)
	for i := range lens {
		lens[i], at[i] = bounds[i+1]-bounds[i], bounds[i]-inStart
	}
	lz77.open(inEnd - inStart)
	c.eachPart(lens, func(i int, w *worker) {
		bs := w.blockState(opts, bounds[i], bounds[i+1], true)
		sink := regionSink{s: lz77, at: at[i]}
		w.lz77Optimal(bs, data, bounds[i], bounds[i+1], opts.NumIterations, &sink)
		counts[i] = sink.n
	})
	lz77.pack(at, counts)
	totalCost := 0.0
	item := 0
	for i := 0; i <= npoints; i++ {
		totalCost += blockSizeAutoType(lz77, item, item+counts[i], counts[i])
		item += counts[i]
		if i < npoints {
			split[i] = item
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
	if opts.FineSplit && opts.BlockSplitting {
		split = fineSplit(lz77, split)
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
