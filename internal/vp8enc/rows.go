package vp8enc

import (
	"errors"
	"sync"
	"sync/atomic"
)

// The first pass decides how every macroblock is coded. It codes several
// macroblock rows at once, each on a goroutine of its own, in a wavefront:
// a macroblock needs the one to its left, which the same goroutine has just
// coded, and of the row above the macroblocks up to the one above and to
// the right of it. So each row runs at least two macroblocks behind the row
// above, and the edges the row below needs (topY, topU, topV, up) can live
// in one line that each row overwrites behind itself: when row r writes
// the entry of macroblock x, row r-1 is past x+1 and needs it no more, and
// row r+1 reads it only once row r is past x+1.
//
// The file must not depend on how many goroutines there are, so nothing a
// row decides may depend on how far the rows above it have got:
//
//   - The bit costs a row decides with follow the statistics of the rows at
//     least rowLag above it, rebuilt every 64 macroblocks' worth of rows.
//     (Measured on photographs against the costs of the rows just above:
//     the same size and quality, within 0.02%.)
//   - A row's room for its modes is set when it starts, from what the rows
//     at least rowLag above used and what the rows in between were given.
//   - Each row writes its decisions to streams of its own, which go into
//     the spool in row order.
//
// So up to rowLag+1 rows are coded at a time. They read their samples from
// the converted image (see analysis.go), in order, and what fails anywhere
// stops all of them.
const rowLag = 7

// rowSpan is where a macroblock row's decisions are in the spool: the
// contexts found above its macroblocks (2 bytes each), its modes stream and
// its coefficients stream, which ends at end.
type rowSpan struct {
	ctxs, modes, coeffs, end int64
}

// rowBytes is what is kept per macroblock row: its rowSpan, its progress
// and its mode budget figures.
const rowBytes = 32 + 4 + 3*8

// costTablesBytes is about the size of a costTables.
const costTablesBytes = 14 << 10

type pass1 struct {
	e *encoder

	mu      sync.Mutex
	cond    sync.Cond
	waiting atomic.Int32 // goroutines in a wait
	err     error        // the first failure (under mu)
	failed  atomic.Bool

	next      atomic.Int64   // the next row to take
	read      int            // rows read from the source (under mu)
	committed int            // rows in the spool (under mu)
	progress  []atomic.Int32 // macroblocks done, per row

	// The room for modes: what each row was given and used, and
	// usedSum[k], what rows 0..k-1 used.
	grant, used, usedSum []int64

	// costs[v] is built from the statistics of the rows above v (and v
	// is a multiple of every, or 0 for the defaults); spare holds tables
	// no longer in use, for the next versions.
	every int
	costs map[int]*costTables
	spare []*costTables
}

// firstPass codes every macroblock row and leaves the decisions in the
// spool.
func (e *encoder) firstPass() error {
	p := &pass1{
		e:        e,
		progress: make([]atomic.Int32, e.mbh),
		grant:    make([]int64, e.mbh),
		used:     make([]int64, e.mbh),
		usedSum:  make([]int64, e.mbh+1),
		every:    max(1, (64+e.mbw-1)/e.mbw),
	}
	p.cond.L = &p.mu
	// The versions of the costs in use: those of the rows being coded.
	held := int64(rowLag/p.every+3) * costTablesBytes
	if err := e.env.Budget.Reserve(held, "webp bit costs"); err != nil {
		return err
	}
	defer e.env.Budget.Release(held)
	def := new(costTables)
	pr := probs(defaultTokenProb)
	def.build(&pr)
	p.costs = map[int]*costTables{0: def}

	workers := min(rowLag+1, 1+e.env.Helpers(), e.mbh)
	need := mbCoderBytes(e.mbw * 16)
	noRoom := make([]error, workers)
	e.env.Parallel(workers, func(i int) {
		// Each goroutine has its own buffers. One that cannot have them
		// leaves the rows to the others.
		if err := e.env.Budget.Reserve(need, "webp encoder row"); err != nil {
			noRoom[i] = err
			return
		}
		defer e.env.Budget.Release(need)
		c := newMBCoder(e)
		defer c.release()
		defer func() {
			if v := recover(); v != nil {
				// Let the others stop waiting for this row; the panic
				// goes on to the caller of Parallel.
				p.fail(errors.New("goimgtools: webp: a row panicked"))
				panic(v)
			}
		}()
		if err := p.work(c); err != nil {
			p.fail(err)
		}
	})
	if p.err != nil {
		return p.err
	}
	if p.committed < e.mbh {
		for _, err := range noRoom {
			if err != nil {
				return err
			}
		}
		return errors.New("goimgtools: webp: rows were left uncoded")
	}
	if e.dbg != nil {
		e.dbg.usedI4 = e.usedI4
		e.dbg.segments = e.nSegs
		for k := range e.dbg.segQ {
			e.dbg.segQ[k] = e.segs[min(k, e.nSegs-1)].q
		}
	}
	return nil
}

// work codes rows, taking the next one free, until there are none left or
// something failed.
func (p *pass1) work(c *mbCoder) error {
	e := p.e
	for {
		r := int(p.next.Add(1) - 1)
		if r >= e.mbh || p.failed.Load() {
			return nil
		}
		// The rows rowLag above must be in, for the costs and the room for
		// modes, and the source is read in order.
		if !p.wait(func() bool { return p.committed >= r-rowLag && p.read == r }) {
			return nil
		}
		if err := p.load(c, r); err != nil {
			return err
		}
		p.mu.Lock()
		costs := p.costs[p.version(r)]
		p.mu.Unlock()
		c.startRow(costs, p.grant[r])
		for x := 0; x < e.mbw; x++ {
			if r > 0 && !p.waitRow(r-1, min(x+2, e.mbw)) {
				return nil
			}
			c.encodeMB(x, r)
			p.done(r, x+1)
		}
		if err := c.finishRow(); err != nil {
			return err
		}
		if !p.wait(func() bool { return p.committed == r }) {
			return nil
		}
		if err := p.commit(c, r); err != nil {
			return err
		}
	}
}

// version is the version of the costs row r decides with.
func (p *pass1) version(r int) int {
	done := r - rowLag
	if done < p.every {
		return 0
	}
	return done / p.every * p.every
}

// load reads macroblock row r into c and sets its room for modes: its
// share of all rows up to it, less what the rows rowLag and more above used
// and what the rows in between were given. Rows load in order.
func (p *pass1) load(c *mbCoder, r int) error {
	e := p.e
	if err := e.readRow(r, c.buf); err != nil {
		return err
	}
	d := max(0, r-rowLag)
	g := e.mbShare*int64(e.mbw)*int64(r+1) - p.usedSum[d]
	for j := d; j < r; j++ {
		g -= p.grant[j]
	}
	p.grant[r] = g
	p.mu.Lock()
	p.read = r + 1
	p.cond.Broadcast()
	p.mu.Unlock()
	return nil
}

// commit appends row r's decisions to the spool and adds its counts to the
// frame's. Rows commit in order.
func (p *pass1) commit(c *mbCoder, r int) error {
	e := p.e
	sp := &e.rows[r]
	sp.ctxs = e.spool.Size()
	if _, err := e.spool.Write(c.ctxs); err != nil {
		return err
	}
	sp.modes = e.spool.Size()
	if _, err := e.spool.Write(c.modes.b); err != nil {
		return err
	}
	sp.coeffs = e.spool.Size()
	if _, err := e.spool.Write(c.coeffs.b); err != nil {
		return err
	}
	sp.end = e.spool.Size()

	p.mu.Lock()
	defer p.mu.Unlock()
	e.stats.add(&c.stats)
	e.skipStats.add(&c.skipStats)
	e.nSkip += c.nSkip
	e.usedI4 += c.usedI4
	e.modeBits += c.rowBits
	p.used[r] = c.rowBits
	p.usedSum[r+1] = p.usedSum[r] + c.rowBits
	if v := r + 1; v%p.every == 0 {
		var pr probs
		e.stats.finalize(&pr)
		var t *costTables
		if n := len(p.spare); n > 0 {
			t, p.spare = p.spare[n-1], p.spare[:n-1]
		} else {
			t = new(costTables)
		}
		t.build(&pr)
		p.costs[v] = t
	}
	// The rows still to come need no older version.
	for v := range p.costs {
		if v < p.version(r+1) {
			if v != 0 {
				p.spare = append(p.spare, p.costs[v])
			}
			delete(p.costs, v)
		}
	}
	p.committed = r + 1
	p.cond.Broadcast()
	return nil
}

// wait blocks until ready (called with mu held) is true, and reports false
// if instead something failed.
func (p *pass1) wait(ready func() bool) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.waiting.Add(1)
	defer p.waiting.Add(-1)
	for !ready() {
		if p.failed.Load() {
			return false
		}
		p.cond.Wait()
	}
	return !p.failed.Load()
}

// waitRow blocks until row r has n macroblocks done.
func (p *pass1) waitRow(r int, n int) bool {
	if p.progress[r].Load() >= int32(n) {
		return true
	}
	return p.wait(func() bool { return p.progress[r].Load() >= int32(n) })
}

// done records that row r has n macroblocks done.
func (p *pass1) done(r, n int) {
	p.progress[r].Store(int32(n))
	// A goroutine that is about to wait has counted itself first, so it
	// either sees the progress or is woken here.
	if p.waiting.Load() > 0 {
		p.mu.Lock()
		p.cond.Broadcast()
		p.mu.Unlock()
	}
}

// fail stops every row.
func (p *pass1) fail(err error) {
	p.mu.Lock()
	if p.err == nil {
		p.err = err
	}
	p.failed.Store(true)
	p.cond.Broadcast()
	p.mu.Unlock()
}

// add adds the counts of o.
func (s *tokenStats) add(o *tokenStats) {
	for pl := range s {
		for b := range s[pl] {
			for c := range s[pl][b] {
				for i := range s[pl][b][c] {
					s[pl][b][c][i].ones += o[pl][b][c][i].ones
					s[pl][b][c][i].total += o[pl][b][c][i].total
				}
			}
		}
	}
}
