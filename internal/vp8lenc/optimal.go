package vp8lenc

import "math"

// The optimal parse: instead of deciding match by match, the LZ77 stage
// takes a stretch of pixels, finds the best match at each, and chooses the
// cheapest way through the stretch (from its end back to its start, the
// least cost from each pixel on: a literal, the match at any length that
// can pay, or a copy of one or a few pixels from the left or from above).
// This is the idea of libwebp's TraceBackwards
// (src/enc/backward_references_cost_enc.c; Copyright 2012 Google Inc., BSD
// licence, see licenses/LICENSE-LIBWEBP), and so is pricing a colour cache
// hit by its index, and, for the predictor and colour table variants, the
// weights given to literals (82%) and hits (68%) against what the
// statistics make them cost: the prefix codes made for each region of the
// image later code them for less than the statistics of the whole stream
// say. The rest is written for this package: a backward pass over a
// stretch at a time, as the pixels stream through the window.
//
// From the default effort it is used for every variant: those with the
// predictor and with the colour table (photographs, palette images) and
// the others (screenshots: 1 to 3.5% smaller). Searching at every pixel
// would go through every long repeat of a screenshot again and again; the
// tokenizer remembers where the matches at each distance end (matchAt),
// which makes that cheap.

// optimalStretch is the most pixels parsed at once.
const optimalStretch = 8 << 10

// optimalBytes is the memory of the optimal parse, per tokenizer.
const optimalBytes = (optimalStretch+2)*(4+4+4+4+4+2+4) + 4<<maxCacheBits + 4*(4*256+1<<maxCacheBits)

// maxCacheBits is the largest colour cache tried.
const maxCacheBits = 10

type optimal struct {
	weigh     bool           // weigh literals and hits as libwebp does
	ml, md    []int32        // the match at each pixel of the stretch: length (0: none), distance
	lit       []uint32       // the cost of each pixel as a literal
	cte       []uint32       // the least cost from each pixel to the end of the stretch
	choice    []uint16       // the length taken at each pixel (0: a literal)
	cd        []int32        // the distance of the copy taken at each pixel
	nextBreak []int32        // the next pixel whose match does not just continue the one before
	sim       []uint32       // the colour cache, simulated
	litc      [4][256]uint32 // literal costs, of the literals the cache misses
	hitc      []uint32       // the cost of a hit, by cache index
}

func newOptimal(weigh bool) *optimal {
	n := optimalStretch + 1
	return &optimal{
		weigh: weigh,
		ml:    make([]int32, n), md: make([]int32, n),
		lit: make([]uint32, n), cte: make([]uint32, n),
		choice: make([]uint16, n), cd: make([]int32, n), nextBreak: make([]int32, n+1),
		sim: make([]uint32, 0, 1<<maxCacheBits), hitc: make([]uint32, 0, 1<<maxCacheBits),
	}
}

// processOptimal is process with the optimal parse.
func (t *tokenizer) processOptimal(final bool) {
	end := t.base + t.n
	stop := end
	if !final {
		stop -= maxMatch + 2
	}
	o := t.opt
	if !final && t.n < len(t.buf) && stop-t.cur < optimalStretch {
		// Wait for a whole stretch, unless the buffer is full: matches end
		// at the end of a stretch, and rows come one at a time.
		return
	}
	for t.cur < stop {
		if n := t.st.nLit + t.st.nCopy; n >= t.nextCM {
			t.cm.update(t.st)
			if t.nextCM < 1<<18 {
				t.nextCM *= 4
			} else {
				t.nextCM += 1 << 18
			}
		}
		n := min(stop, t.cur+optimalStretch) - t.cur
		ml, md, lit, cte, choice, cd, nb := o.ml[:n+1], o.md[:n+1], o.lit[:n+1], o.cte[:n+1], o.choice[:n+1], o.cd[:n+1], o.nextBreak[:n+2]
		for i := 0; i < n; i++ {
			t.insertTo(t.cur + i)
			l, dist, _ := t.find(t.cur + i)
			if l < minMatch {
				l = 0
			}
			ml[i], md[i] = int32(min(l, n-i)), int32(dist)
		}
		t.insertTo(t.cur + n)
		// Where a match stops just continuing the one before: the places
		// where ending a long match can pay.
		nb[n], nb[n+1] = int32(n), int32(n)
		for j := n - 1; j >= 0; j-- {
			if j == 0 || ml[j] == 0 || md[j] != md[j-1] || ml[j] != ml[j-1]-1 {
				nb[j] = int32(j)
			} else {
				nb[j] = nb[j+1]
			}
		}
		o.literalCosts(t, n)
		cte[n] = 0
		buf := t.buf
		for i := n - 1; i >= 0; i-- {
			best := lit[i] + cte[i+1]
			ch, chd := uint16(0), int32(0)
			try := func(k int, dc uint32, d int32) {
				ls, ln, _ := prefixCode(uint32(k))
				if c := dc + uint32(t.cm.length[ls]) + 16*uint32(ln) + cte[i+k]; c < best {
					best, ch, chd = c, uint16(k), d
				}
			}
			if L := int(ml[i]); L >= minMatch {
				ds, dn, _ := prefixCode(distCode(int(md[i]), t.width))
				dc := uint32(t.cm.dist[ds]) + 16*uint32(dn)
				for k := 1; k <= min(L, 32); k++ {
					try(k, dc, md[i])
				}
				if L > 32 {
					for j := int(nb[i+33]); j-i < L; j = int(nb[j+1]) {
						try(j-i, dc, md[i])
					}
					try(L, dc, md[i])
				}
			}
			// Short copies from the left and from above.
			p := t.cur + i - t.base
			for _, d := range [2]int{1, t.width} {
				if d > t.maxDist || p < d || int32(d) == md[i] && ml[i] > 0 {
					continue
				}
				ds, dn, _ := prefixCode(distCode(d, t.width))
				dc := uint32(t.cm.dist[ds]) + 16*uint32(dn)
				for k := 1; k <= min(n-i, 32) && buf[p+k-1] == buf[p+k-1-d]; k++ {
					try(k, dc, int32(d))
				}
			}
			cte[i], choice[i], cd[i] = best, ch, chd
		}
		for i := 0; i < n; {
			if k := int(choice[i]); k > 0 {
				t.copyRef(t.cur+i, k, int(cd[i]))
				i += k
			} else {
				t.literal(t.cur + i)
				i++
			}
		}
		t.cur += n
	}
}

// literalCosts sets the cost of each pixel of the stretch of n as a
// literal: a colour cache hit (the largest cache simulated over the
// stretch, which every pixel goes through, literal or copied) or the
// literal itself.
func (o *optimal) literalCosts(t *tokenizer, n int) {
	lit := o.lit[:n]
	weigh := func(c, pct uint32) uint32 {
		if o.weigh {
			return (c*pct + 50) / 100
		}
		return c
	}
	if t.cm.hit == 0 {
		for i := range lit {
			lit[i] = max(weigh(t.cm.pixel(t.buf[t.cur+i-t.base]), 82), 16)
		}
		return
	}
	cc := &t.st.cands[len(t.st.cands)-1]
	o.model(t.st, cc)
	o.sim = append(o.sim[:0], cc.tab...)
	for i := range lit {
		px := t.buf[t.cur+i-t.base]
		c := weigh(o.litc[0][px>>8&0xff]+o.litc[1][px>>16&0xff]+o.litc[2][px&0xff]+o.litc[3][px>>24], 82)
		k := px * cacheMult >> cc.shift
		if o.sim[k] == px {
			c = min(c, weigh(o.hitc[k], 68))
		} else {
			o.sim[k] = px
		}
		// A prefix code takes a bit at least, however common the symbol
		// (the statistics alone say almost nothing for the one colour of
		// a flat area).
		lit[i] = max(c, 16)
	}
}

// model sets the literal and hit costs from the statistics, with the
// colour cache cc: a hit costs its index's share of the green symbols, a
// literal what its channels cost among the literals the cache missed.
func (o *optimal) model(st *stats, cc *cacheCand) {
	g := float64(st.nLit + st.nCopy)
	var hits float64
	for _, h := range cc.hit {
		hits += float64(h)
	}
	miss := float64(st.nLit) - hits
	conv := func(h uint32, total float64) uint32 {
		if total <= 0 {
			return 16 * 8
		}
		v := 16 * (math.Log2(total+1) - math.Log2(float64(h)+0.5))
		return uint32(max(0, min(v, costCap)))
	}
	for ch := range o.litc {
		total := miss
		if ch == 0 {
			total = g
		}
		for v := range o.litc[ch] {
			o.litc[ch][v] = conv(st.lit[ch][v]-cc.hitCh[ch][v], total)
		}
	}
	o.hitc = o.hitc[:len(cc.hit)]
	for k, h := range cc.hit {
		o.hitc[k] = conv(h, g)
	}
}
