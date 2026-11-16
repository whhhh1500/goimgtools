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
// which makes that cheap, and in a run of equal pixels (the flat areas of
// a drawing or an alpha plane) the search passes over the run's own
// pixels (find). The copies of up to 32 pixels are priced together, from
// the least cost to the end after each length.
//
// Besides the match the hash chain finds (the one that saves the most going
// by the statistics), the longest match at the nearest distances above and
// to the left, whose codes are the cheapest, is offered at every pixel (as
// libwebp's LZ77 "box" searches those distances, in
// src/enc/backward_references_enc.c): the hash chain, which goes through
// the latest pixels first, rarely gets as far as the rows above in a
// smooth area, where the residuals repeat those a few rows up. A gradient
// mask 16% smaller, a drawing 2%, photographs and game artwork 0.1 to 1%.

// optimalStretch is the most pixels parsed at once.
const optimalStretch = 8 << 10

// optimalBytes is the memory of the optimal parse. The variants are fed
// one after the other and each parses a stretch at a time, from start to
// end: they share it.
const optimalBytes = (optimalStretch+2)*(4+4+4+4+4+4+4+2+4) + 4<<maxCacheBits + 4*(4*256+1<<maxCacheBits)

// boxCodes: the distances whose codes are 3 to boxCodes (the pixels a few
// rows above, a few columns to either side) are searched at every pixel
// for a second match, besides the one the hash chain finds.
const boxCodes = 16

// maxCacheBits is the largest colour cache tried.
const maxCacheBits = 10

type optimal struct {
	ml, md    []int32        // the match at each pixel of the stretch: length (0: none), distance
	bl, bd    []int32        // the longest match at a near distance (box), if any
	lit       []uint32       // the cost of each pixel as a literal
	cte       []uint32       // the least cost from each pixel to the end of the stretch
	choice    []uint16       // the length taken at each pixel (0: a literal)
	cd        []int32        // the distance of the copy taken at each pixel
	nextBreak []int32        // the next pixel whose match does not just continue the one before
	sim       []uint32       // the colour cache, simulated
	litc      [4][256]uint32 // literal costs, of the literals the cache misses
	hitc      []uint32       // the cost of a hit, by cache index
}

func newOptimal() *optimal {
	n := optimalStretch + 1
	return &optimal{
		ml: make([]int32, n), md: make([]int32, n), bl: make([]int32, n), bd: make([]int32, n),
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
		bl, bd := o.bl[:n+1], o.bd[:n+1]
		for i := 0; i < n; i++ {
			t.insertTo(t.cur + i)
			l, dist, _ := t.find(t.cur + i)
			if l < minMatch {
				l = 0
			}
			ml[i], md[i] = int32(min(l, n-i)), int32(dist)
			bl[i] = 0
			if len(t.box) > 0 {
				l, dist := t.boxMatch(t.cur + i)
				if dist != 0 && !(int32(dist) == md[i] && ml[i] > 0) {
					bl[i], bd[i] = int32(min(l, n-i)), int32(dist)
				}
			}
		}
		t.insertTo(t.cur + n)
		// Where a match stops just continuing the one before: the places
		// where ending a long match can pay. (A match that goes on at the
		// same distance continues the one before even when it is no
		// shorter: the one before was cut at the longest length, as in a
		// flat area every match is. Taken for breaks, those made the
		// search through a long match go over every pixel of it, at every
		// pixel.)
		nb[n], nb[n+1] = int32(n), int32(n)
		for j := n - 1; j >= 0; j-- {
			if j == 0 || ml[j] == 0 || md[j] != md[j-1] || ml[j] < ml[j-1]-1 {
				nb[j] = int32(j)
			} else {
				nb[j] = nb[j+1]
			}
		}
		o.literalCosts(t, n)
		cte[n] = 0
		buf := t.buf
		// What a length of up to 32 and the distances 1 and width cost,
		// for this stretch.
		var lenc [33]uint32
		for k := 1; k <= 32; k++ {
			ls, ln, _ := prefixCode(uint32(k))
			lenc[k] = uint32(t.cm.length[ls]) + 16*uint32(ln)
		}
		distc := func(d int) uint32 {
			ds, dn, _ := prefixCode(distCode(d, t.width))
			return uint32(t.cm.dist[ds]) + 16*uint32(dn)
		}
		short := [2]int{1, t.width}
		shortc := [2]uint32{distc(1), distc(t.width)}
		// How far the pixels from i on repeat those 1 and width before them,
		// up to 32: worked out from the next pixel's, as i goes back.
		var reach [2]int
		// The cheapest way to the end with a copy of up to k pixels (of a
		// distance that costs nothing), and its length: the copies of up
		// to 32 pixels are priced from these.
		var pmc [33]uint32
		var pmk [33]uint16
		for i := n - 1; i >= 0; i-- {
			best := lit[i] + cte[i+1]
			ch, chd := uint16(0), int32(0)
			try := func(k int, dc uint32, d int32) {
				ls, ln, _ := prefixCode(uint32(k))
				if c := dc + uint32(t.cm.length[ls]) + 16*uint32(ln) + cte[i+k]; c < best {
					best, ch, chd = c, uint16(k), d
				}
			}
			p := t.cur + i - t.base
			K := 0
			L := int(ml[i])
			if L >= minMatch {
				K = min(L, 32)
			}
			B := int(bl[i])
			if B >= minMatch {
				K = max(K, min(B, 32))
			}
			for si, d := range short {
				if d <= t.maxDist && p >= d && buf[p] == buf[p-d] {
					reach[si] = min(reach[si]+1, 32)
				} else {
					reach[si] = 0
				}
				K = max(K, reach[si])
			}
			// (The earliest length wins a tie, as when the lengths are
			// tried one after the other.)
			pmc[0] = math.MaxUint32
			for k := 1; k <= K; k++ {
				if c := lenc[k] + cte[i+k]; c < pmc[k-1] {
					pmc[k], pmk[k] = c, uint16(k)
				} else {
					pmc[k], pmk[k] = pmc[k-1], pmk[k-1]
				}
			}
			if L >= minMatch {
				d := md[i]
				var dc uint32
				switch int(d) {
				case 1:
					dc = shortc[0]
				case t.width:
					dc = shortc[1]
				default:
					dc = distc(int(d))
				}
				if c := dc + pmc[min(L, 32)]; c < best {
					best, ch, chd = c, pmk[min(L, 32)], d
				}
				if L > 32 {
					for j := int(nb[i+33]); j-i < L; j = int(nb[j+1]) {
						try(j-i, dc, md[i])
					}
					try(L, dc, md[i])
				}
			}
			// The match at a near distance.
			if B >= minMatch {
				dc := distc(int(bd[i]))
				if c := dc + pmc[min(B, 32)]; c < best {
					best, ch, chd = c, pmk[min(B, 32)], bd[i]
				}
				if B > 32 {
					try(B, dc, bd[i])
				}
			}
			// Short copies from the left and from above.
			for si, d := range short {
				if r := reach[si]; r > 0 && !(int32(d) == md[i] && ml[i] > 0) {
					if c := shortc[si] + pmc[r]; c < best {
						best, ch, chd = c, pmk[r], int32(d)
					}
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
		if t.weigh {
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
