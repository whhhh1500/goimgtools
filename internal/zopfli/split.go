package zopfli

import "slices"

// Block splitting (blocksplitter.c): where splitting the parse in two
// makes it smaller, found by a search of the cost along the parse.

func findMinimum(f func(i int) float64, start, end int) (int, float64) {
	if end-start < 1024 {
		best, result := largeFloat, start
		for i := start; i < end; i++ {
			if v := f(i); v < best {
				best, result = v, i
			}
		}
		return result, best
	}
	const num = 9
	var p [num]int
	var vp [num]float64
	lastBest := largeFloat
	pos := start
	for end-start > num {
		for i := 0; i < num; i++ {
			p[i] = start + (i+1)*((end-start)/(num+1))
			vp[i] = f(p[i])
		}
		besti, best := 0, vp[0]
		for i := 1; i < num; i++ {
			if vp[i] < best {
				best, besti = vp[i], i
			}
		}
		if best > lastBest {
			break
		}
		if besti != 0 {
			start = p[besti-1]
		}
		if besti != num-1 {
			end = p[besti+1]
		}
		pos = p[besti]
		lastBest = best
	}
	return pos, lastBest
}

func addSorted(value int, out []int) []int {
	out = append(out, value)
	for i := 0; i+1 < len(out); i++ {
		if out[i] > value {
			copy(out[i+1:], out[i:len(out)-1])
			out[i] = value
			break
		}
	}
	return out
}

// findLargestSplittable finds the longest part, between the points of
// split, that does not start at one of done (the parts found not to gain
// by a split; Zopfli marks them in an array as long as the parse).
func findLargestSplittable(lz77Size int, done []int, split []int) (lstart, lend int, found bool) {
	longest := 0
	for i := 0; i <= len(split); i++ {
		start, end := 0, lz77Size-1
		if i > 0 {
			start = split[i-1]
		}
		if i < len(split) {
			end = split[i]
		}
		if !slices.Contains(done, start) && end-start > longest {
			lstart, lend, found, longest = start, end, true, end-start
		}
	}
	return
}

// blockSplitLZ77 gives the item indices where the parse should be split.
func (c *Compressor) blockSplitLZ77(s *lz77Store, maxBlocks int) []int {
	var split []int
	if s.size() < 10 {
		return nil
	}
	var done []int
	lstart, lend := 0, s.size()
	numBlocks := 1
	for {
		if maxBlocks > 0 && numBlocks >= maxBlocks {
			break
		}
		ls, le := lstart, lend
		llpos, splitCost := findMinimum(func(i int) float64 {
			return calculateBlockSizeAutoType(s, ls, i) + calculateBlockSizeAutoType(s, i, le)
		}, lstart+1, lend)
		origCost := calculateBlockSizeAutoType(s, lstart, lend)
		if splitCost > origCost || llpos == lstart+1 || llpos == lend {
			done = append(done, lstart)
		} else {
			split = addSorted(llpos, split)
			numBlocks++
		}
		var found bool
		if lstart, lend, found = findLargestSplittable(s.size(), done, split); !found {
			break
		}
		if lend-lstart < 10 {
			break
		}
	}
	return split
}

// blockSplit gives the byte positions where data[inStart:inEnd] should be
// split, judged on a greedy parse.
func (c *Compressor) blockSplit(data []byte, inStart, inEnd, maxBlocks int) []int {
	store := &c.whole
	store.reset(data)
	bs := c.blockState(inStart, inEnd, false)
	bs.greedy(data, inStart, inEnd, store, c.h)
	lzSplit := c.blockSplitLZ77(store, maxBlocks)
	var split []int
	pos := inStart
	if len(lzSplit) > 0 {
		for i := 0; i < store.size(); i++ {
			length := 1
			if store.dists[i] != 0 {
				length = int(store.litlens[i])
			}
			if lzSplit[len(split)] == i {
				split = append(split, pos)
				if len(split) == len(lzSplit) {
					break
				}
			}
			pos += length
		}
	}
	return split
}
