package vp8lenc

import "sort"

// The colour table is stored as differences between consecutive entries, so
// its order decides how well it compresses: 256 colours in arbitrary order
// take close to a kilobyte, which is a lot for a small image.

// brightness orders colours by alpha, then luma.
func brightness(c uint32) uint32 {
	return c>>24<<24 | (299*(c>>16&0xff) + 587*(c>>8&0xff) + 114*(c&0xff))
}

// orderBy returns the indices of pal sorted by key.
func orderBy(pal []uint32, key func(uint32) uint32) []int {
	order := make([]int, len(pal))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(i, j int) bool {
		a, b := pal[order[i]], pal[order[j]]
		if ka, kb := key(a), key(b); ka != kb {
			return ka < kb
		}
		return a < b
	})
	return order
}

// channelDistance is the sum of the channel differences of two colours,
// each taken modulo 256 the short way round, as the delta coding sees it.
func channelDistance(a, b uint32) uint32 {
	var s uint32
	for sh := 0; sh < 32; sh += 8 {
		d := (a>>sh - b>>sh) & 0xff
		if d > 128 {
			d = 256 - d
		}
		s += d
	}
	return s
}

// orderNearest starts from the darkest colour and keeps appending the
// colour closest to the last one.
func orderNearest(pal []uint32) []int {
	order := orderBy(pal, brightness)
	for i := 0; i+2 < len(order); i++ {
		last := pal[order[i]]
		best, bestD := i+1, ^uint32(0)
		for j := i + 1; j < len(order); j++ {
			if d := channelDistance(pal[order[j]], last); d < bestD {
				best, bestD = j, d
			}
		}
		order[i+1], order[best] = order[best], order[i+1]
	}
	return order
}

// reorder applies an order: it returns the new table and the old-to-new
// index map.
func reorder(pal []uint32, order []int) ([]uint32, []uint8) {
	out := make([]uint32, len(pal))
	remap := make([]uint8, 256)
	for n, o := range order {
		out[n] = pal[o]
		remap[o] = uint8(n)
	}
	return out, remap
}

// writePalette codes the colour indexing transform's table.
func (e *encoder) writePalette(bw *bitWriter, pal []uint32) error {
	n := len(pal)
	bw.put(uint32(n-1), 8)
	delta := make([]uint32, n)
	for i, c := range pal {
		if i == 0 {
			delta[i] = c
		} else {
			delta[i] = subPixels(c, pal[i-1])
		}
	}
	return e.subImage(bw, delta, n, false, 0)
}

// cheapestPalette tries a few orders of the table and returns the one that
// codes smallest. It is for variants whose pixels do not care about the
// order of the indices.
func (e *encoder) cheapestPalette(pal []uint32) ([]uint32, []uint8, error) {
	var bestPal []uint32
	var bestMap []uint8
	bestBits := int64(-1)
	for i := 0; i < 4; i++ {
		p, m := pal, []uint8(nil)
		switch i {
		case 1:
			p, m = reorder(pal, orderBy(pal, brightness))
		case 2:
			p, m = reorder(pal, orderBy(pal, func(c uint32) uint32 { return c }))
		case 3:
			p, m = reorder(pal, orderNearest(pal))
		}
		var bw bitWriter
		if err := e.writePalette(&bw, p); err != nil {
			return nil, nil, err
		}
		if b := bw.bitLen(); bestBits < 0 || b < bestBits {
			bestPal, bestMap, bestBits = p, m, b
		}
	}
	return bestPal, bestMap, nil
}
