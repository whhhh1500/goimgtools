package pngstream

import (
	"compress/flate"
	"encoding/binary"
	"hash"
	"hash/adler32"
	"io"
	"sync/atomic"

	"github.com/whhhh1500/goimgtools/internal/deflate"
	"github.com/whhhh1500/goimgtools/internal/gflate"
	"github.com/whhhh1500/goimgtools/internal/zopfli"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// zBlocks writes a zlib stream the way pigz does, so that it can be
// compressed on several cores: the data is cut into blocks of zBlockBytes,
// each compressed on its own with the 32KB before it as its dictionary,
// and the compressed blocks follow each other in one deflate stream (every
// block but the last ends with a sync flush, the last is final). The
// checksum is over all the data, in order. At level 10 the blocks are
// Zopfli's master blocks, of zopfli.MasterBlock, and each follows the bits
// of the one before without a sync flush: the stream is the one Zopfli
// writes for all the data (smaller blocks, or the flushes, would make it
// larger: 0.03% with blocks of 256KB).
//
// Up to level 6 the blocks are compressed with compress/flate; with Go
// 1.27 or later, data of more than one block with internal/gflate, Go
// 1.27's compress/flate, which writes the same bytes and whose writer takes
// a new dictionary when it is reset: compress/flate's has to be made anew
// for each block, about 0.9MB of garbage per 256KB of data (at level 6, 16%
// more time).
// Level 7 uses libdeflate's lazy parsing (internal/deflate, its level 7;
// see lazyLevel), the same with every Go version. Levels 8 and 9 use the
// near-optimal parsing of libdeflate, with the settings of its levels 10
// and 12, and the blocks of deflate split where it saves bits, as zopfli
// does. Level 10 uses Zopfli itself (internal/zopfli; see zopfliOptions).
//
// The blocks are the same however many helpers the pipeline has (see
// pipeline.Env.Go), and so is the file. Without helpers, up to level 6,
// the blocks are compressed as the data comes, straight to the output,
// with no buffer beyond the 32KB of the dictionary, and data of up to one
// block comes out exactly as compress/zlib writes it; otherwise they are
// gathered and compressed, on the helpers while the next ones fill.
type zBlocks struct {
	w     io.Writer
	env   *pipeline.Env
	level int
	sum   hash.Hash32
	tail  []byte // the last 32KB of the data so far
	n     int64  // data so far
	err   error
	held  int64 // reserved from the budget

	// Without helpers, up to level 7: the compressor of the current block,
	// and gflate's, reset for each block, when there is more than one.
	fw    flateWriter
	kw    *gflate.Writer
	multi bool // the data takes more than one block, for gflate
	// With helpers, up to level 7: the compressors not in use.
	kws chan *gflate.Writer

	// Otherwise.
	ways int
	cur  *zBlock   // being filled
	busy []*zBlock // handed out to be compressed, oldest first
	free []*zBlock

	// From level 7: how a compressor is made and the memory it takes,
	// and the compressors not in use (one for each block being compressed
	// at once, as many as the memory limit allows).
	newComp   func() blockCompressor
	compBytes int64
	comps     chan blockCompressor
	made      int

	// size is the data of a block: zBlockBytes, or zopfli.MasterBlock at
	// level 10. blk is the size of the blocks' buffers: size, or all the
	// data when there is less.
	size int
	blk  int

	// At level 10, the bits of the last byte written out so far that are
	// not yet a whole byte: pendN of them, in the low bits of pend. The
	// blocks filled and not yet handed to a helper, oldest first (all newer
	// than the busy ones): a block waits for a helper to be free rather than
	// be compressed on the goroutine that filters the rows (see dispatch).
	pend    byte
	pendN   int
	pending []*zBlock
	prepaid bool // the first compressor is reserved already
}

const (
	zBlockBytes = 256 << 10
	zWindow     = 32 << 10
)

// nearLevels are the settings of levels 8 and 9: libdeflate's levels 10
// and 12, split by cost as zopfli does; level 9 also looks for matches
// inside long matches. A block of deflate is parsed whole within at most
// 128KB at level 8, so the compressor takes 3MB (libdeflate's 300000
// bytes would take 8.6MB, for files 0.05% smaller), and within the 256KB
// of a compression block at level 9 (5.6MB, 0.2% smaller than 128KB).
//
// Measured on the filtered rows of three screenshots and three
// photographs, against libdeflate's settings: level 8 0.75% smaller in
// 25% more time; level 9 1% smaller (screenshots 3 to 4.5%, within 0.4
// to 1.7% of zopfli with 15 iterations), in 3.5 times the time. Pricing
// the symbols at their entropy, as zopfli does, makes level 8 another
// 0.07% smaller and level 9 0.18% (17 images; 9 takes 18% longer).
var nearLevels = map[int]deflate.Params{
	8: withFine(withSplit(withBlocks(deflate.Level10, 128<<10), false, 1)),
	9: withFine(withSplit(withBlocks(deflate.Level12, zBlockBytes), true, 2)),
}

// DefaultLevel is the compression level used when none is given.
const DefaultLevel = 7

// MaxLevel is the highest compression level.
const MaxLevel = zopfliLevel

// zopfliLevel is the level compressed with Zopfli, with zopfliOptions: 60
// runs of its optimal parse rather than its default 15 (17 images 0.03%
// smaller), and as ECT does, runs priced by the code lengths after them,
// no limit on the blocks, and the codes of a block chosen among more, its
// short matches replaced by literals where that is smaller (0.026%
// smaller on the filtered rows of the 17, in about the same time).
const zopfliLevel = 10

var zopfliOptions = zopfli.Options{NumIterations: 60, BlockSplitting: true, Ultra: true, MoreCodes: true, FineSplit: true}

// zopfliBlock is the data of a block at level 10 (a variable for tests).
var zopfliBlock = zopfli.MasterBlock

// blockCompressor compresses a block after its dictionary, as
// deflate.Compressor does.
type blockCompressor interface {
	Compress(dst, in []byte, start int, final bool) []byte
}

// tenCompressor compresses a block of level 10 with Zopfli and with level
// 9's compressor, and keeps the smaller (Zopfli's on a tie). Zopfli is the
// smaller on the rows of photographs and screenshots (0.12% on 17 of
// them), but not always: on a synthetic photograph of 1000×300, level 9's
// was 2% smaller.
type tenCompressor struct {
	z   *zopfli.Compressor
	d   *deflate.Compressor
	alt []byte
}

// tenBytes is the memory of a tenCompressor for blocks of n bytes.
func tenBytes(n int) int64 {
	return zopfli.Bytes(n) + nearLevels[9].Bytes() + int64(n+zOutSlack)
}

// newTenCompressor makes a tenCompressor for blocks of n bytes, which
// parses the parts of a block on up to lanes cores at a time.
func newTenCompressor(n int, env *pipeline.Env, lanes int) *tenCompressor {
	t := &tenCompressor{z: zopfli.New(zopfliOptions, n), d: deflate.New(nearLevels[9]), alt: make([]byte, 0, n+zOutSlack)}
	if lanes > 1 {
		t.z.SetWorkers(&envWorkers{env: env, lanes: lanes})
	}
	return t
}

// envWorkers lends Zopfli the pipeline's helpers and memory, to parse the
// parts of a block on up to lanes cores at a time: on the helpers free, and
// those that become free while parts are left. A block filled meanwhile
// waits for a helper (see zBlocks.dispatch).
type envWorkers struct {
	env   *pipeline.Env
	lanes int
}

func (w *envWorkers) Parallel(n int, fn func(i int)) {
	var next atomic.Int64
	w.env.Parallel(min(n, w.lanes), func(int) {
		for i := int(next.Add(1) - 1); i < n; i = int(next.Add(1) - 1) {
			fn(i)
		}
	})
}

func (w *envWorkers) Reserve(n int64) bool {
	return w.env.Budget.Reserve(n, "png compression helper") == nil
}

func (w *envWorkers) Release(n int64) { w.env.Budget.Release(n) }

// CompressBits compresses as zopfli.Compressor.CompressBits does.
func (t *tenCompressor) CompressBits(dst, in []byte, start int, final bool) ([]byte, int, int) {
	out, bits, stored := t.z.CompressBits(dst, in, start, final)
	alt, altBits, altStored := t.d.CompressBits(t.alt[:0], in, start, final)
	t.alt = alt
	if 8*len(alt)+altBits < 8*(len(out)-len(dst))+bits {
		return append(out[:len(dst)], alt...), altBits, altStored
	}
	return out, bits, stored
}

// Compress compresses as zopfli.Compressor.Compress does.
func (t *tenCompressor) Compress(dst, in []byte, start int, final bool) []byte {
	out := t.z.Compress(dst, in, start, final)
	t.alt = t.d.Compress(t.alt[:0], in, start, final)
	if len(t.alt) < len(out)-len(dst) {
		return append(out[:len(dst)], t.alt...)
	}
	return out
}

// lazyParams are the settings of level 7: libdeflate's level 7, its lazy
// parsing (see lazyLevel).
var lazyParams = deflate.LazyLevel7

// lazyLevel tells whether the level is compressed with libdeflate's lazy
// parsing rather than compress/flate: level 7. compress/flate's level 7
// does poorly on the rows of paletted images (on images reduced to 256
// colours, smooth gradients came out 1.4 to 4 times as large, a photograph
// 5% larger, in about the same time) and is 3% larger on photographs,
// screenshots and drawings in RGB and RGBA (from 1 to 5%, in two thirds of
// the time); Go 1.26's level 7 is another again.
func lazyLevel(level int) bool {
	return level == 7
}

func withSplit(p deflate.Params, noSkip bool, passes int) deflate.Params {
	p.SplitByCost, p.SplitPasses, p.NoSkip, p.EntropyCosts = true, passes, noSkip, true
	return p
}

// nearBytes is the memory levels 8 to 10 take beyond what level 7 takes
// without helpers, for images of the width: a compressor, and a block
// gathered for it (for each helper too, when the limit allows; see
// compressor), the search of the rows' filters (see bruteFilter) and the
// data chunk (see idatChunker), at most.
func nearBytes(level, width int) int64 {
	var comp int64
	block := int64(zBlockBytes)
	zopfliBlock := int64(zopfliBlock)
	if p, ok := nearLevels[level]; ok {
		comp = p.Bytes()
	} else if level == zopfliLevel {
		comp, block = tenBytes(int(zopfliBlock)), zopfliBlock
	} else {
		return 0
	}
	row := 1 + 8*width // at most
	lanes := 0
	if level >= zopfliLevel {
		lanes = nTrial
	}
	return comp + zWindow + 2*block + zOutSlack + idatBytes + bruteBytes(row, level >= zopfliLevel) +
		trialNeed(row, segmentBytes(level), true, lanes) - trialNeed(row, trialBytes, false, 0)
}

func withFine(p deflate.Params) deflate.Params {
	p.FineSplit = true
	return p
}

func withBlocks(p deflate.Params, blockLen int) deflate.Params {
	p.BlockLen, p.CachePerBlock = blockLen, 3
	return p
}

// A zBlock gathered to be compressed keeps its dictionary and data, one
// after the other, its output and the sizes of the writes the compressor
// made: written out again in those sizes, the bytes are grouped into IDAT
// chunks as they are when the compressor writes to the output itself,
// without helpers (the buffer that makes the chunks passes a large write
// on whole). From level 8 the chunks are of a fixed size (see idatChunker).
type zBlock struct {
	buf    []byte // the dictionary, then the data
	dict   int    // the length of the dictionary
	out    []byte
	writes []int
	bits   int // at level 10: the bits of out's last byte that are the block's
	stored int // and the bit of out at which its first stored block starts, -1 for none
	last   bool
	err    error
	wait   func()
}

// flateWriter is a deflate compressor: compress/flate's or gflate's.
type flateWriter interface {
	io.WriteCloser
	Flush() error
}

// zOutSlack is room enough beyond a block's data for it compressed: stored
// blocks and the sync flush add a few bytes to every 64KB at most.
const zOutSlack = 1 << 10

// recorder appends to the output of a block and notes each write's size.
type recorder struct{ b *zBlock }

func (r recorder) Write(p []byte) (int, error) {
	r.b.writes = append(r.b.writes, len(p))
	r.b.out = append(r.b.out, p...)
	return len(p), nil
}

// newZBlocks starts the zlib stream of total bytes of data.
func newZBlocks(w io.Writer, level int, env *pipeline.Env, total int64) (*zBlocks, error) {
	size := zBlockBytes
	if level == zopfliLevel {
		size = zopfliBlock
	}
	z := &zBlocks{w: w, env: env, level: level, sum: adler32.New(), ways: env.Helpers() + 1, multi: gflateSame && total > zBlockBytes,
		size: size, blk: int(max(1, min(total, int64(size))))}
	if p, ok := nearLevels[level]; ok {
		z.newComp, z.compBytes = func() blockCompressor { return deflate.New(p) }, p.Bytes()
	} else if lazyLevel(level) {
		p := lazyParams
		p.MaxInput = z.blk
		z.newComp, z.compBytes = func() blockCompressor { return deflate.New(p) }, p.Bytes()
	} else if level == zopfliLevel {
		n, ways := z.blk, z.ways
		z.newComp, z.compBytes = func() blockCompressor { return newTenCompressor(n, env, ways) }, tenBytes(n)
	} else if z.multi {
		z.kws = make(chan *gflate.Writer, z.ways+1)
	}
	if z.newComp != nil {
		z.comps = make(chan blockCompressor, z.ways)
	}
	if err := env.Budget.Reserve(zWindow, "png compression window"); err != nil {
		return nil, err
	}
	z.held = zWindow
	if level == zopfliLevel {
		// The first compressor is reserved at once, before the rest of the
		// encoder (and the blocks waiting for it) take the room it needs.
		if err := env.Budget.Reserve(z.compBytes, "png compressor"); err != nil {
			return z, err
		}
		z.held += z.compBytes
		z.prepaid = true
	}
	if _, err := z.w.Write(z.header()); err != nil {
		return z, err
	}
	if z.ways == 1 && z.newComp == nil {
		if z.multi {
			kw, err := gflate.NewWriter(z.w, level)
			if err != nil {
				return z, err
			}
			z.fw, z.kw = kw, kw
		} else {
			fw, err := flate.NewWriter(z.w, level)
			if err != nil {
				return z, err
			}
			z.fw = fw
		}
	}
	return z, nil
}

// header is that of compress/zlib for the level.
func (z *zBlocks) header() []byte {
	h := []byte{0x78, 0}
	var flags byte
	switch z.level {
	case -2, 0, 1:
		flags = 0
	case 2, 3, 4, 5:
		flags = 1
	case 6, -1:
		flags = 2
	case 7, 8, 9, 10:
		flags = 3
	}
	h[1] = flags << 6
	h[1] += uint8(31 - binary.BigEndian.Uint16(h)%31)
	return h
}

// keepTail remembers the end of the data: the dictionary of the next block.
func (z *zBlocks) keepTail(p []byte) {
	if len(p) >= zWindow {
		z.tail = append(z.tail[:0], p[len(p)-zWindow:]...)
		return
	}
	z.tail = append(z.tail, p...)
	if over := len(z.tail) - zWindow; over > 0 {
		z.tail = append(z.tail[:0], z.tail[over:]...)
	}
}

func (z *zBlocks) Write(p []byte) (int, error) {
	if z.err != nil {
		return 0, z.err
	}
	if len(z.pending) > 0 {
		if z.err = z.dispatch(false); z.err != nil {
			return 0, z.err
		}
	}
	n := len(p)
	z.sum.Write(p)
	for len(p) > 0 {
		// A full block is ended only when more data comes, so that the last
		// block is the one Close finishes.
		if z.n > 0 && z.n%int64(z.size) == 0 {
			if z.err = z.next(); z.err != nil {
				return 0, z.err
			}
		}
		k := min(len(p), z.size-int(z.n%int64(z.size)))
		if z.fw != nil {
			if _, z.err = z.fw.Write(p[:k]); z.err != nil {
				return 0, z.err
			}
		} else {
			if z.cur == nil {
				if z.cur, z.err = z.block(); z.err != nil {
					return 0, z.err
				}
			}
			z.cur.buf = append(z.cur.buf, p[:k]...)
		}
		z.keepTail(p[:k])
		z.n += int64(k)
		p = p[k:]
	}
	return n, nil
}

// next ends the full block. Without helpers, up to level 7: flushed, and a
// new compressor for the next block, with the dictionary; otherwise,
// compressed.
func (z *zBlocks) next() error {
	if z.fw != nil {
		if err := z.fw.Flush(); err != nil {
			return err
		}
		if z.kw != nil {
			z.kw.ResetDict(z.w, z.tail)
			return nil
		}
		fw, err := flate.NewWriterDict(z.w, z.level, z.tail)
		if err != nil {
			return err
		}
		z.fw = fw
		return nil
	}
	return z.send()
}

// block returns an empty block whose dictionary is the end of the data so
// far.
func (z *zBlocks) block() (*zBlock, error) {
	var b *zBlock
	if n := len(z.free); n > 0 {
		b = z.free[n-1]
		z.free = z.free[:n-1]
		b.out, b.writes, b.bits, b.last, b.err = b.out[:0], b.writes[:0], 0, false, nil
	} else {
		// The dictionary, the data and its output.
		need := int64(zWindow + 2*z.blk + zOutSlack)
		if err := z.env.Budget.Reserve(need, "png compression block"); err != nil {
			if len(z.busy)+len(z.pending) == 0 {
				return nil, err
			}
			// The memory limit allows no more blocks at once: the oldest is
			// waited for and its room taken. The file is the same.
			if err := z.drain(); err != nil {
				return nil, err
			}
			return z.block()
		}
		z.held += need
		b = &zBlock{buf: make([]byte, 0, zWindow+z.blk), out: make([]byte, 0, z.blk+zOutSlack)}
	}
	b.buf = append(b.buf[:0], z.tail...)
	b.dict = len(z.tail)
	return b, nil
}

// ahead is the memory the blocks need at the least, beyond what is reserved
// so far, to be compressed from level 7 (a compressor, and a block to fill
// while another is compressed): the search for the filters, which reserves
// after the blocks are set up, leaves it free.
func (z *zBlocks) ahead() int64 {
	if z.newComp == nil {
		return 0
	}
	n := 2 * int64(zWindow+2*z.blk+zOutSlack)
	if !z.prepaid {
		n += z.compBytes
	}
	return n
}

// tryCompressor returns a free compressor, or a new one if the memory limit
// allows it; nil if neither.
func (z *zBlocks) tryCompressor() blockCompressor {
	select {
	case c := <-z.comps:
		return c
	default:
	}
	if z.made == 0 && z.prepaid {
		z.made++
		return z.newComp()
	}
	if z.made < z.ways && z.env.Budget.Reserve(z.compBytes, "png compressor") == nil {
		z.held += z.compBytes
		z.made++
		return z.newComp()
	}
	return nil
}

// dispatch hands the blocks waiting at level 10 to helpers, oldest first,
// while helpers are free and compressors to be had; with force, all of them,
// those with no helper free compressed on this goroutine.
func (z *zBlocks) dispatch(force bool) error {
	for len(z.pending) > 0 {
		b := z.pending[0]
		c := z.tryCompressor()
		if c == nil {
			if !force {
				return nil
			}
			var err error
			if c, err = z.compressor(); err != nil {
				return err
			}
		}
		comps, tc := z.comps, c.(*tenCompressor)
		fn := func() {
			defer func() { comps <- c }()
			b.out, b.bits, b.stored = tc.CompressBits(b.out, b.buf, b.dict, b.last)
		}
		if wait, ok := z.env.TryGo(fn); ok {
			b.wait = wait
		} else if force {
			b.wait = z.env.Go(fn)
		} else {
			z.comps <- c
			return nil
		}
		z.pending = z.pending[1:]
		z.busy = append(z.busy, b)
	}
	return nil
}

// compressor returns a compressor from level 7: a free one, a new one if
// the memory limit allows it, or the next to be free.
func (z *zBlocks) compressor() (blockCompressor, error) {
	select {
	case c := <-z.comps:
		return c, nil
	default:
	}
	if z.made == 0 && z.prepaid {
		z.made++
		return z.newComp(), nil
	}
	if z.made < z.ways {
		need := z.compBytes
		err := z.env.Budget.Reserve(need, "png compressor")
		if err == nil {
			z.held += need
			z.made++
			return z.newComp(), nil
		}
		if z.made == 0 {
			return nil, err
		}
	}
	// Every compressor is in use on a helper, which gives it back.
	return <-z.comps, nil
}

// send hands the current block to a helper to be compressed (or compresses
// it, when none is free), and writes out what is done when too many blocks
// are waiting.
func (z *zBlocks) send() error {
	b := z.cur
	z.cur = nil
	level := z.level
	if level == zopfliLevel && z.ways > 1 {
		z.pending = append(z.pending, b)
		if err := z.dispatch(b.last); err != nil {
			return err
		}
		for len(z.busy)+len(z.pending) > z.ways {
			if err := z.drain(); err != nil {
				return err
			}
		}
		return nil
	}
	if z.newComp != nil {
		c, err := z.compressor()
		if err != nil {
			return err
		}
		comps := z.comps
		b.wait = z.env.Go(func() {
			defer func() { comps <- c }()
			if tc, ok := c.(*tenCompressor); ok {
				b.out, b.bits, b.stored = tc.CompressBits(b.out, b.buf, b.dict, b.last)
				return
			}
			b.out = c.Compress(b.out, b.buf, b.dict, b.last)
			b.writes = append(b.writes, len(b.out))
		})
	} else {
		b.wait = z.env.Go(func() { b.err = z.compressBlock(recorder{b}, b.buf[:b.dict], b.buf[b.dict:], level, b.last) })
	}
	z.busy = append(z.busy, b)
	waiting := z.ways
	if z.ways == 1 {
		// Without helpers the block is done: out with it, to keep one
		// block in memory.
		waiting = 0
	}
	for len(z.busy) > waiting {
		if err := z.drain(); err != nil {
			return err
		}
	}
	return nil
}

// compressBlock compresses a block up to level 7, with a compressor of
// z.kws when there is more than one.
func (z *zBlocks) compressBlock(out io.Writer, dict, data []byte, level int, last bool) error {
	var fw flateWriter
	if !z.multi {
		var w *flate.Writer
		var err error
		if len(dict) == 0 {
			w, err = flate.NewWriter(out, level)
		} else {
			w, err = flate.NewWriterDict(out, level, dict)
		}
		if err != nil {
			return err
		}
		fw = w
	} else {
		var kw *gflate.Writer
		select {
		case kw = <-z.kws:
			kw.ResetDict(out, dict)
		default:
			w, err := gflate.NewWriterDict(out, level, dict)
			if err != nil {
				return err
			}
			kw = w
		}
		defer func() {
			select {
			case z.kws <- kw:
			default:
			}
		}()
		fw = kw
	}
	if _, err := fw.Write(data); err != nil {
		return err
	}
	if last {
		return fw.Close()
	}
	return fw.Flush()
}

// drain waits for the oldest block and writes it out (at level 10 handing
// it to a helper first, or compressing it, if it is still waiting).
func (z *zBlocks) drain() error {
	if len(z.busy) == 0 {
		if err := z.dispatch(true); err != nil {
			return err
		}
	}
	b := z.busy[0]
	z.busy = z.busy[1:]
	b.wait()
	b.wait = nil
	if b.err != nil {
		return b.err
	}
	if b.bits != 0 {
		if err := z.writeBits(b); err != nil {
			return err
		}
	} else {
		out := b.out
		for _, n := range b.writes {
			if _, err := z.w.Write(out[:n]); err != nil {
				return err
			}
			out = out[n:]
		}
	}
	z.free = append(z.free, b)
	return nil
}

// writeBits writes out a block of level 10 after the bits before it: its
// bytes shifted by the pendN bits pending, the bits left over pending. A
// stored block's data starts on a byte wherever its header ends (Zopfli
// writes its master blocks into one stream): when the block has one, only
// the bytes up to the end of the first one's header are shifted, its
// padding made to fit, and what follows written as it is.
func (z *zBlocks) writeBits(b *zBlock) error {
	out := b.out
	if len(out) == 0 {
		return nil
	}
	s := z.pendN
	total := s + 8*(len(out)-1) + b.bits
	if s != 0 && b.stored >= 0 {
		head := b.stored + 3       // the bits up to the end of the stored block's header
		n := (head + 7) / 8        // its length starts at out[n]
		keep := (head + s + 7) / 8 // and at that of the result
		if keep > n {
			out = append(out, 0) // within zOutSlack
			copy(out[n+1:], out[n:])
		}
		carry := z.pend
		for i, v := range out[:n] {
			out[i] = carry | v<<s
			carry = v >> (8 - s)
		}
		if keep > n {
			out[n] = carry
		} // else what is carried is the padding's zeros
		total = 8*(len(out)-1) + b.bits
	} else if s != 0 {
		carry := z.pend
		for i, v := range out {
			out[i] = carry | v<<s
			carry = v >> (8 - s)
		}
		out = append(out, carry) // within zOutSlack
	}
	whole := total / 8
	z.pendN = total % 8
	if z.pendN != 0 {
		z.pend = out[whole]
	}
	b.out = out[:0]
	_, err := z.w.Write(out[:whole])
	return err
}

// Close ends the last block and writes everything out with the checksum.
func (z *zBlocks) Close() error {
	if z.err != nil {
		return z.err
	}
	if z.fw != nil {
		z.err = z.fw.Close()
	} else {
		if z.cur == nil {
			if z.cur, z.err = z.block(); z.err != nil {
				return z.err
			}
		}
		z.cur.last = true
		if z.err = z.send(); z.err == nil {
			for len(z.busy) > 0 && z.err == nil {
				z.err = z.drain()
			}
		}
		if z.err == nil && z.pendN != 0 {
			_, z.err = z.w.Write([]byte{z.pend})
		}
	}
	if z.err != nil {
		return z.err
	}
	var sum [4]byte
	binary.BigEndian.PutUint32(sum[:], z.sum.Sum32())
	_, z.err = z.w.Write(sum[:])
	return z.err
}

// release waits for whatever is still being compressed (after an error)
// and gives back the budget.
func (z *zBlocks) release() {
	for _, b := range z.busy {
		if b.wait != nil {
			b.wait()
		}
	}
	if z.comps != nil {
		// The memory the compressors took for their workers.
		for len(z.comps) > 0 {
			if t, ok := (<-z.comps).(*tenCompressor); ok {
				t.z.Release()
			}
		}
	}
	z.busy, z.free, z.cur, z.fw, z.kw, z.comps = nil, nil, nil, nil, nil, nil
	z.env.Budget.Release(z.held)
	z.held = 0
}
