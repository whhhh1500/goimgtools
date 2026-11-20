package zopfli

import "sync"

// A worker holds the memory of the optimal parse of a part of the data:
// the hash chains, the longest match cache and the squeezer's arrays, for
// parts of up to cap bytes.
type worker struct {
	h   *hash
	lmc matchCache
	q   squeezer
	cap int
}

// workerBytes is the memory of a worker for parts of up to n bytes.
func workerBytes(n int) int64 { return hashBytes + cacheBytes(n) + squeezerBytes(n) }

func newWorker(n int) *worker {
	h := newHash(windowSize)
	return &worker{h: h, lmc: makeMatchCache(n), q: makeSqueezer(n, h), cap: n}
}

// Workers lends a Compressor more cores, and memory for them, to make the
// optimal parses of the parts of a block at the same time (Zopfli makes
// them one after the other). The output is the same.
type Workers interface {
	// Parallel calls fn(0) to fn(n-1), each once, at the same time on the
	// calling goroutine and on whatever helpers are free, as
	// pipeline.Env.Parallel does, and returns when they have all returned.
	Parallel(n int, fn func(i int))
	// Reserve takes n bytes from the memory limit, and tells whether it
	// could; Release gives them back.
	Reserve(n int64) bool
	Release(n int64)
}

// SetWorkers lends the Compressor w (nil: none, the parts one after the
// other). Release gives back the memory it took from w.
func (c *Compressor) SetWorkers(w Workers) { c.workers = w }

// minWorker is the least a worker is made for: most parts of a block of
// 1MB are shorter (a block is split into 15 parts at most), so that the
// workers can be used again for the next.
const minWorker = 256 << 10

// takeWorker returns a worker for a part of n bytes: the Compressor's own
// if it is free, one kept from before, or a new one if the memory limit has
// room for it; nil if none.
func (c *Compressor) takeWorker(n int) *worker {
	c.mu.Lock()
	if !c.ownBusy {
		c.ownBusy = true
		c.mu.Unlock()
		return c.own
	}
	for i, w := range c.pool {
		if w.cap >= n {
			c.pool = append(c.pool[:i], c.pool[i+1:]...)
			c.mu.Unlock()
			return w
		}
	}
	var drop int64
	if len(c.pool) > 0 {
		// None is large enough: one gives way to a larger.
		drop = workerBytes(c.pool[0].cap)
		c.pool = c.pool[1:]
		c.reserved -= drop
	}
	c.mu.Unlock()
	if drop > 0 {
		c.workers.Release(drop)
	}
	m := max(n, minWorker)
	need := workerBytes(m)
	if !c.workers.Reserve(need) {
		return nil
	}
	c.mu.Lock()
	c.reserved += need
	c.mu.Unlock()
	return newWorker(m)
}

func (c *Compressor) giveWorker(w *worker) {
	c.mu.Lock()
	if w == c.own {
		c.ownBusy = false
	} else {
		c.pool = append(c.pool, w)
	}
	c.mu.Unlock()
}

// Release gives back the memory the Compressor took from its Workers.
func (c *Compressor) Release() {
	c.mu.Lock()
	n := c.reserved
	c.pool, c.reserved = nil, 0
	c.mu.Unlock()
	if n > 0 {
		c.workers.Release(n)
	}
}

// eachPart calls fn for each part, of the lengths given, with a worker: on
// the Workers, at the same time, each with a worker of its own; the parts
// left without one (when the memory limit has no room), and all of them
// without Workers, one after the other with the Compressor's own.
func (c *Compressor) eachPart(lens []int, fn func(i int, w *worker)) {
	if c.workers == nil || len(lens) == 1 {
		for i := range lens {
			fn(i, c.own)
		}
		return
	}
	left := make([]bool, len(lens))
	c.workers.Parallel(len(lens), func(i int) {
		w := c.takeWorker(lens[i])
		if w == nil {
			left[i] = true
			return
		}
		defer c.giveWorker(w)
		fn(i, w)
	})
	for i, l := range left {
		if l {
			fn(i, c.own)
		}
	}
}

// regionSink writes the items of a parse into a store's arrays from item
// at on: the parts of a block are parsed into regions of their own, as far
// apart as their first bytes (a part has no more items than bytes), and
// packed together after (see lz77Store.pack).
type regionSink struct {
	s     *lz77Store
	at, n int
}

func (r *regionSink) add(length, dist uint16, pos int) {
	i := r.at + r.n
	r.s.litlens[i], r.s.dists[i], r.s.pos[i] = length, dist, int32(pos)
	r.n++
}

// open lengthens the store's arrays to n items, for regions.
func (s *lz77Store) open(n int) {
	s.litlens, s.dists, s.pos = s.litlens[:n], s.dists[:n], s.pos[:n]
}

// pack moves the regions, of counts[i] items from item at[i] on, together
// in order, and makes the histograms.
func (s *lz77Store) pack(at, counts []int) {
	n := 0
	for i, k := range counts {
		copy(s.litlens[n:], s.litlens[at[i]:at[i]+k])
		copy(s.dists[n:], s.dists[at[i]:at[i]+k])
		copy(s.pos[n:], s.pos[at[i]:at[i]+k])
		n += k
	}
	s.litlens, s.dists, s.pos = s.litlens[:n], s.dists[:n], s.pos[:n]
	s.llCounts, s.dCounts = s.llCounts[:0], s.dCounts[:0]
	for i := 0; i < n; i++ {
		c := i / countsEvery
		if i%countsEvery == 0 {
			s.llCounts = nextCounts(s.llCounts, numLL)
			s.dCounts = nextCounts(s.dCounts, numD)
		}
		s.llCounts[numLL*c+s.llSymbol(i)]++
		if s.dists[i] != 0 {
			s.dCounts[numD*c+s.dSymbol(i)]++
		}
	}
}

// compressorSync is what a Compressor shares with the workers it lends.
type compressorSync struct {
	mu       sync.Mutex
	ownBusy  bool
	pool     []*worker
	reserved int64
}
