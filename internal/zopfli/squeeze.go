package zopfli

import "math"

// The optimal parse (squeeze.c): the cheapest way to every byte under a
// cost model, the model taken from the statistics of the parse before,
// repeated, with random changes to the statistics once it stops improving.

type symbolStats struct {
	litlens   [numLL]uint64
	dists     [numD]uint64
	llSymbols [numLL]float64
	dSymbols  [numD]float64
}

func addWeighedStatFreqs(s1 *symbolStats, w1 float64, s2 *symbolStats, w2 float64, result *symbolStats) {
	for i := 0; i < numLL; i++ {
		result.litlens[i] = uint64(float64(float64(s1.litlens[i])*w1) + float64(float64(s2.litlens[i])*w2))
	}
	for i := 0; i < numD; i++ {
		result.dists[i] = uint64(float64(float64(s1.dists[i])*w1) + float64(float64(s2.dists[i])*w2))
	}
	result.litlens[256] = 1
}

type ranState struct{ w, z uint32 }

func (r *ranState) next() uint32 {
	r.z = 36969*(r.z&65535) + (r.z >> 16)
	r.w = 18000*(r.w&65535) + (r.w >> 16)
	return r.z<<16 + r.w
}

func (r *ranState) randomize(freqs []uint64) {
	n := uint32(len(freqs))
	for i := range freqs {
		if (r.next()>>4)%3 == 0 {
			freqs[i] = freqs[r.next()%n]
		}
	}
}

func (r *ranState) randomizeStats(s *symbolStats) {
	r.randomize(s.litlens[:])
	r.randomize(s.dists[:])
	s.litlens[256] = 1
}

func (s *symbolStats) calculate() {
	calculateEntropy(s.litlens[:], s.llSymbols[:])
	calculateEntropy(s.dists[:], s.dSymbols[:])
}

// gather takes the counts of the symbols of a parse (Zopfli counts them
// from the parse kept in a store).
func (s *symbolStats) gather(c *symbolCounts) {
	s.litlens, s.dists = c.ll, c.d
	s.litlens[256] = 1
	s.calculate()
}

// codeCosts sets the costs of the symbols to the lengths of the codes the
// block of the counts would be written with (see dynamicLengths); a symbol
// with no code costs nothing.
func (s *symbolStats) codeCosts(c *symbolCounts) {
	var llLengths [numLL]uint32
	var dLengths [numD]uint32
	ll, d := c.ll, c.d
	ll[256] = 1
	calculateBitLengths(ll[:], 15, llLengths[:])
	calculateBitLengths(d[:], 15, dLengths[:])
	patchDistanceCodes(dLengths[:])
	tryOptimizeHuffmanForRle(nil, 0, 0, ll[:], d[:], llLengths[:], dLengths[:])
	for i, l := range llLengths {
		s.llSymbols[i] = float64(l)
	}
	for i, l := range dLengths {
		s.dSymbols[i] = float64(l)
	}
}

// parseSink takes the items of a parse, in order: an lz77Store keeps them,
// symbolCounts counts their symbols, fixedWriter writes them.
type parseSink interface {
	add(length, dist uint16, pos int)
}

// symbolCounts counts the symbols of a parse.
type symbolCounts struct {
	ll [numLL]uint64
	d  [numD]uint64
	n  int
}

func (c *symbolCounts) add(length, dist uint16, _ int) {
	c.n++
	if dist == 0 {
		c.ll[length]++
		return
	}
	c.ll[lengthSymbol(int(length))]++
	c.d[distSymbol(int(dist))]++
}

// costModel prices a literal (dist 0) or a match.
type costModel func(litlen, dist int) float64

func costFixed(litlen, dist int) float64 {
	if dist == 0 {
		if litlen <= 143 {
			return 8
		}
		return 9
	}
	cost := 0
	if lengthSymbol(litlen) <= 279 {
		cost += 7
	} else {
		cost += 8
	}
	cost += 5
	return float64(cost + distExtraBits(dist) + lengthExtraBits(litlen))
}

func (s *symbolStats) cost(litlen, dist int) float64 {
	if dist == 0 {
		return s.llSymbols[litlen]
	}
	return float64(lengthExtraBits(litlen)+distExtraBits(dist)) + s.llSymbols[lengthSymbol(litlen)] + s.dSymbols[distSymbol(dist)]
}

var dSymbolStarts = [30]int{1, 2, 3, 4, 5, 7, 9, 13, 17, 25, 33, 49, 65, 97, 129, 193, 257, 385, 513,
	769, 1025, 1537, 2049, 3073, 4097, 6145, 8193, 12289, 16385, 24577}

func costModelMinCost(cm costModel) float64 {
	bestLength, bestDist := 0, 0
	minCost := largeFloat
	for i := 3; i < 259; i++ {
		if c := cm(i, 1); c < minCost {
			bestLength, minCost = i, c
		}
	}
	minCost = largeFloat
	for _, d := range dSymbolStarts {
		if c := cm(3, d); c < minCost {
			bestDist, minCost = d, c
		}
	}
	return cm(bestLength, bestDist)
}

// squeezer holds what the runs of the optimal parse of a block share. The
// cost of reaching each byte (a float32, as Zopfli keeps it) is kept by its
// bits in costs, whose room then holds the path the costs give.
type squeezer struct {
	lengths []uint16
	costs   []uint32
	path    []uint32 // in costs' room
	h       *hash
	sublen  [259]uint16

	// For the cost model of a run: each length's extra bits and the cost of
	// its symbol.
	lenBits  [maxMatch + 1]int
	lenBitsF [maxMatch + 1]float64
	lenCost  [maxMatch + 1]float64
}

// squeezerBytes is the memory of a squeezer for blocks of up to n bytes.
func squeezerBytes(n int) int64 { return int64(n+1) * (2 + 4) }

func makeSqueezer(n int, h *hash) squeezer {
	return squeezer{lengths: make([]uint16, n+1), costs: make([]uint32, n+1), h: h}
}

// bestLengths finds the cheapest way to every byte of data[inStart:inEnd]
// under the cost model of stats (nil: the fixed code's), and returns the
// cost of the whole. Zopfli calls the cost model for every length of every
// match; here a length's part of the cost is looked up once a run, and a
// distance's once for the lengths it serves, then added in Zopfli's
// order: the same costs.
func (q *squeezer) bestLengths(s *blockState, data []byte, inStart, inEnd int, stats *symbolStats) float64 {
	blockSize := inEnd - inStart
	if inStart == inEnd {
		return 0
	}
	var cm costModel = costFixed
	if stats != nil {
		cm = stats.cost
		for k := minMatch; k <= maxMatch; k++ {
			q.lenBits[k], q.lenCost[k] = lengthExtraBits(k), stats.llSymbols[lengthSymbol(k)]
			q.lenBitsF[k] = float64(q.lenBits[k])
		}
	}
	windowStart := 0
	if inStart > windowSize {
		windowStart = inStart - windowSize
	}
	minCost := costModelMinCost(cm)
	h := q.h
	h.reset()
	h.warmup(data, windowStart, inEnd)
	for i := windowStart; i < inStart; i++ {
		h.update(data, i, inEnd)
	}
	costs, lengths := q.costs[:blockSize+1], q.lengths[:blockSize+1]
	large := math.Float32bits(largeFloat)
	for i := 1; i <= blockSize; i++ {
		costs[i] = large
	}
	costs[0] = 0
	lengths[0] = 0
	cost := func(j int) float64 { return float64(math.Float32frombits(costs[j])) }
	sublen := q.sublen[:]
	for i := inStart; i < inEnd; i++ {
		j := i - inStart
		h.update(data, i, inEnd)
		if h.same[i&windowMask] > maxMatch*2 && i > inStart+maxMatch+1 && i+maxMatch*2+1 < inEnd &&
			h.same[(i-maxMatch)&windowMask] > maxMatch {
			symbolCost := cm(maxMatch, 1)
			for k := 0; k < maxMatch; k++ {
				costs[j+maxMatch] = math.Float32bits(float32(cost(j) + symbolCost))
				lengths[j+maxMatch] = maxMatch
				i++
				j++
				h.update(data, i, inEnd)
			}
		}
		seg := s.stored(i)
		var leng uint16
		if seg != nil {
			leng = uint16(seg[len(seg)-3]) + 3
		} else {
			_, leng = s.findLongestMatch(h, data, i, inEnd, maxMatch, sublen)
		}
		if i+1 <= inEnd {
			if c := cm(int(data[i]), 0) + cost(j); c < cost(j+1) {
				costs[j+1] = math.Float32bits(float32(c))
				lengths[j+1] = 1
			}
		}
		kEnd := min(int(leng), inEnd-i)
		costJ := cost(j)
		minCostJ := minCost + costJ
		if stats == nil {
			for k := 3; k <= kEnd; k++ {
				if cost(j+k) <= minCostJ {
					continue
				}
				if c := cm(k, int(sublen[k])) + costJ; c < cost(j+k) {
					costs[j+k] = math.Float32bits(float32(c))
					lengths[j+k] = uint16(k)
				}
			}
			continue
		}
		if seg != nil {
			// The lengths as the cache keeps them: for each, the distance
			// of the lengths up to it.
			k := 3
			for e := 0; e < len(seg) && k <= kEnd; e += 3 {
				hi := min(int(seg[e])+3, kEnd)
				d := int(seg[e+1]) | int(seg[e+2])<<8
				dBits, dCost := float64(distExtraBits(d)), stats.dSymbols[distSymbol(d)]
				for ; k <= hi; k++ {
					ck := float64(math.Float32frombits(costs[j+k]))
					if ck <= minCostJ {
						continue
					}
					// As stats.cost adds them (the bits are whole numbers:
					// their sum is the same either way), then the cost so far.
					if c := q.lenBitsF[k] + dBits + q.lenCost[k] + dCost + costJ; c < ck {
						costs[j+k] = math.Float32bits(float32(c))
						lengths[j+k] = uint16(k)
					}
				}
			}
			continue
		}
		lastD, dBits, dCost := -1, 0, 0.0
		for k := 3; k <= kEnd; k++ {
			ck := cost(j + k)
			if ck <= minCostJ {
				continue
			}
			if d := int(sublen[k]); d != lastD {
				lastD, dBits, dCost = d, distExtraBits(d), stats.dSymbols[distSymbol(d)]
			}
			// As stats.cost adds them, then the cost so far.
			if c := float64(q.lenBits[k]+dBits) + q.lenCost[k] + dCost + costJ; c < ck {
				costs[j+k] = math.Float32bits(float32(c))
				lengths[j+k] = uint16(k)
			}
		}
	}
	return cost(blockSize)
}

// traceBackwards sets the path from the lengths, in costs' room.
func (q *squeezer) traceBackwards(size int) {
	q.path = q.costs[:0]
	if size == 0 {
		return
	}
	for index := size; ; {
		l := q.lengths[index]
		q.path = append(q.path, uint32(l))
		index -= int(l)
		if index == 0 {
			break
		}
	}
	for i, j := 0, len(q.path)-1; i < j; i, j = i+1, j-1 {
		q.path[i], q.path[j] = q.path[j], q.path[i]
	}
}

// followPath gives sink the items of the path: the lengths, and the
// distances the match finder finds for them.
func followPath[L uint16 | uint32](s *blockState, h *hash, data []byte, inStart, inEnd int, path []L, sink parseSink) {
	if inStart == inEnd {
		return
	}
	windowStart := 0
	if inStart > windowSize {
		windowStart = inStart - windowSize
	}
	h.reset()
	h.warmup(data, windowStart, inEnd)
	for i := windowStart; i < inStart; i++ {
		h.update(data, i, inEnd)
	}
	pos := inStart
	for _, l := range path {
		length := uint16(l)
		h.update(data, pos, inEnd)
		if length >= minMatch {
			dist, _ := s.findLongestMatch(h, data, pos, inEnd, int(length), nil)
			sink.add(length, dist, pos)
		} else {
			length = 1
			sink.add(uint16(data[pos]), 0, pos)
		}
		for j := 1; j < int(length); j++ {
			h.update(data, pos+j, inEnd)
		}
		pos += int(length)
	}
}

// run is a run of the optimal parse under the cost model, its items given
// to sink; the path stays in q.path until the next run.
func (q *squeezer) run(s *blockState, data []byte, inStart, inEnd int, stats *symbolStats, sink parseSink) float64 {
	cost := q.bestLengths(s, data, inStart, inEnd, stats)
	q.traceBackwards(inEnd - inStart)
	followPath(s, q.h, data, inStart, inEnd, q.path, sink)
	return cost
}

// lz77Optimal is the optimal parse of data[inStart:inEnd] with numIterations
// runs, appended to store. Zopfli keeps each run's parse in a store of its
// own, and copies the best; here the runs are only counted, and the best
// is run again at the end, from the statistics it was run with: the same
// path, and the same items, the match cache having been filled by the
// first run.
func (w *worker) lz77Optimal(s *blockState, data []byte, inStart, inEnd, numIterations int, store parseSink) {
	q := &w.q
	var stats, bestStats, lastStats symbolStats
	ran := ranState{w: 1, z: 2}
	lastRandomStep := -1
	bestCost, lastCost := largeFloat, 0.0
	var counts symbolCounts
	s.greedy(data, inStart, inEnd, &counts, q.h)
	stats.gather(&counts)
	last := -1 // the run whose parse is the best, if it is the last made
	var bestCounts symbolCounts
	for i := 0; i < numIterations; i++ {
		counts = symbolCounts{}
		q.run(s, data, inStart, inEnd, &stats, &counts)
		cost := dynamicBlockSize(&counts)
		last = -1
		if cost < bestCost {
			bestStats = stats
			bestCost = cost
			bestCounts = counts
			last = i
		}
		lastStats = stats
		stats.gather(&counts)
		if lastRandomStep != -1 {
			addWeighedStatFreqs(&stats, 1.0, &lastStats, 0.5, &stats)
			stats.calculate()
		}
		if i > 5 && cost == lastCost {
			stats = bestStats
			ran.randomizeStats(&stats)
			stats.calculate()
			lastRandomStep = i
		}
		lastCost = cost
	}
	if numIterations > 0 && w.ultra {
		// Then, as ECT does, runs priced by the lengths of the Huffman codes
		// of the best parse (evened out for the header, as the block is
		// written), while they make it smaller.
		for {
			var model symbolStats
			model.codeCosts(&bestCounts)
			counts = symbolCounts{}
			q.run(s, data, inStart, inEnd, &model, &counts)
			cost := dynamicBlockSize(&counts)
			last = -1
			if cost >= bestCost {
				break
			}
			bestStats, bestCost, bestCounts, last = model, cost, counts, 0
		}
	}
	if numIterations > 0 {
		if last < 0 {
			q.bestLengths(s, data, inStart, inEnd, &bestStats)
			q.traceBackwards(inEnd - inStart)
		}
		followPath(s, q.h, data, inStart, inEnd, q.path, store)
	}
}

// lz77OptimalFixed is the optimal parse for the fixed code; its items go to
// sink, and its path stays in c.q.path.
func (w *worker) lz77OptimalFixed(s *blockState, data []byte, inStart, inEnd int, sink parseSink) {
	s.blockStart, s.blockEnd = inStart, inEnd
	w.q.run(s, data, inStart, inEnd, nil, sink)
}
