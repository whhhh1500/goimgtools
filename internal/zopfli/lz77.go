package zopfli

// blockState is what the parse of a block of data shares (lz77.c): its
// range and the longest match cache, if any.
type blockState struct {
	opts       *Options
	blockStart int
	blockEnd   int
	lmc        *matchCache
}

// blockState starts the parse of data[start:end], with the compressor's
// longest match cache, emptied, when addLMC is set.
func (c *Compressor) blockState(start, end int, addLMC bool) *blockState {
	s := &blockState{opts: &c.opts, blockStart: start, blockEnd: end}
	if addLMC {
		c.lmc.init(end - start)
		s.lmc = &c.lmc
	}
	return s
}

// lengthScore is the greedy parse's heuristic worth of a match.
func lengthScore(length, distance int) int {
	if distance > 1024 {
		return length - 1
	}
	return length
}

func (s *blockState) tryFromCache(pos int, limit *int, sublen []uint16) (dist, length uint16, ok bool) {
	if s.lmc == nil {
		return 0, 0, false
	}
	lp := pos - s.blockStart
	available := s.lmc.length[lp] == 0 || s.lmc.dist[lp] != 0
	limitOK := available && (*limit == maxMatch || int(s.lmc.length[lp]) <= *limit ||
		(sublen != nil && s.lmc.maxCachedSublen(lp) >= *limit))
	if limitOK && available {
		if sublen == nil || int(s.lmc.length[lp]) <= s.lmc.maxCachedSublen(lp) {
			length = s.lmc.length[lp]
			if int(length) > *limit {
				length = uint16(*limit)
			}
			if sublen != nil {
				s.lmc.cacheToSublen(lp, int(length), sublen)
				dist = sublen[length]
			} else {
				dist = s.lmc.dist[lp]
			}
			return dist, length, true
		}
		*limit = int(s.lmc.length[lp])
	}
	return 0, 0, false
}

func (s *blockState) storeInCache(pos, limit int, sublen []uint16, dist, length uint16) {
	if s.lmc == nil {
		return
	}
	lp := pos - s.blockStart
	available := s.lmc.length[lp] == 0 || s.lmc.dist[lp] != 0
	if limit == maxMatch && sublen != nil && !available {
		if length < minMatch {
			s.lmc.dist[lp], s.lmc.length[lp] = 0, 0
		} else {
			s.lmc.dist[lp], s.lmc.length[lp] = dist, length
		}
		s.lmc.sublenToCache(sublen, lp, int(length))
	}
}

// findLongestMatch finds the longest match at pos (data ends at size), up
// to limit, and with sublen the distance of the shortest match of every
// length.
func (s *blockState) findLongestMatch(h *hash, data []byte, pos, size, limit int, sublen []uint16) (dist, length uint16) {
	hpos := uint16(pos & windowMask)
	if d, l, ok := s.tryFromCache(pos, &limit, sublen); ok {
		return d, l
	}
	if size-pos < minMatch {
		return 0, 0
	}
	if pos+limit > size {
		limit = size - pos
	}
	arrayEnd := pos + limit
	bestDist, bestLength := uint16(0), uint16(1)
	chainCounter := maxChainHits

	hhead, hprev, hval := h.head, h.prev, h.val
	pp := uint16(hhead[hval])
	p := hprev[pp]
	var d uint32
	if p < pp {
		d = uint32(pp - p)
	} else {
		d = uint32(windowSize-int(p)) + uint32(pp)
	}
	usingSecond := false
	for d < windowSize {
		var cur uint16
		if d > 0 {
			scan, match := pos, pos-int(d)
			if pos+int(bestLength) >= size || data[scan+int(bestLength)] == data[match+int(bestLength)] {
				same0 := h.same[pos&windowMask]
				if same0 > 2 && data[scan] == data[match] {
					same1 := h.same[(pos-int(d))&windowMask]
					same := min(same0, same1)
					if int(same) > limit {
						same = uint16(limit)
					}
					scan += int(same)
					match += int(same)
				}
				for scan < arrayEnd && data[scan] == data[match] {
					scan++
					match++
				}
				cur = uint16(scan - pos)
			}
			if cur > bestLength {
				if sublen != nil {
					for j := bestLength + 1; j <= cur; j++ {
						sublen[j] = uint16(d)
					}
				}
				bestDist, bestLength = uint16(d), cur
				if int(cur) >= limit {
					break
				}
			}
		}
		if !usingSecond && bestLength >= h.same[hpos] && h.val2 == h.hashVal2[p] {
			usingSecond = true
			hprev = h.prev2
		}
		pp = p
		p = hprev[p]
		if p == pp {
			break
		}
		if p < pp {
			d += uint32(pp - p)
		} else {
			d += uint32(windowSize-int(p)) + uint32(pp)
		}
		chainCounter--
		if chainCounter <= 0 {
			break
		}
	}
	s.storeInCache(pos, limit, sublen, bestDist, bestLength)
	return bestDist, bestLength
}

// greedy makes a lazy-matching LZ77 parse of data[inStart:inEnd] into
// store, data before inStart (32KB of it at most) being the history.
func (s *blockState) greedy(data []byte, inStart, inEnd int, store parseSink, h *hash) {
	if inStart == inEnd {
		return
	}
	windowStart := 0
	if inStart > windowSize {
		windowStart = inStart - windowSize
	}
	var dummy [259]uint16
	h.reset()
	h.warmup(data, windowStart, inEnd)
	for i := windowStart; i < inStart; i++ {
		h.update(data, i, inEnd)
	}
	prevLength, prevMatch := 0, 0
	matchAvailable := false
	for i := inStart; i < inEnd; i++ {
		h.update(data, i, inEnd)
		dist, leng := s.findLongestMatch(h, data, i, inEnd, maxMatch, dummy[:])
		score := lengthScore(int(leng), int(dist))
		prevScore := lengthScore(prevLength, prevMatch)
		if matchAvailable {
			matchAvailable = false
			if score > prevScore+1 {
				store.add(uint16(data[i-1]), 0, i-1)
				if score >= minMatch && leng < maxMatch {
					matchAvailable = true
					prevLength, prevMatch = int(leng), int(dist)
					continue
				}
			} else {
				leng, dist = uint16(prevLength), uint16(prevMatch)
				store.add(leng, dist, i-1)
				for j := 2; j < int(leng); j++ {
					i++
					h.update(data, i, inEnd)
				}
				continue
			}
		} else if score >= minMatch && leng < maxMatch {
			matchAvailable = true
			prevLength, prevMatch = int(leng), int(dist)
			continue
		}
		if score >= minMatch {
			store.add(leng, dist, i)
		} else {
			leng = 1
			store.add(uint16(data[i]), 0, i)
		}
		for j := 1; j < int(leng); j++ {
			i++
			h.update(data, i, inEnd)
		}
	}
}
