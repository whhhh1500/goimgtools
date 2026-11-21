package vp8enc

// Lossy coding of the alpha plane, as libwebp does it with an alpha quality
// below 100 (src/enc/alpha_enc.c, src/utils/quant_levels_utils.c, BSD
// licence): before the plane is filtered and coded, its values are reduced
// to a few levels chosen for it by k-means over its histogram (the lowest
// and the highest value kept), which the lossless coder then codes in far
// fewer bits. The ALPH chunk says so (pre-processing 1, level reduction).

// alphaLevels is the number of levels libwebp reduces an alpha plane to at
// quality q (1 to 99): 16 levels already leave little error and are
// quality 70, so 0 to 70 is 2 to 16 levels and 70 to 100 16 to 256.
func alphaLevels(q int) int {
	if q <= 70 {
		return 2 + q/5
	}
	return 16 + (q-70)*8
}

// quantizeLevels is the map of each value to its level for a plane with
// histogram freq reduced to n levels (libwebp's QuantizeLevels), or nil
// when the plane has no more than n values. The arithmetic is libwebp's,
// in float64 with every product rounded before it is added (no fused
// multiply-add), so the map is the same on every processor.
func quantizeLevels(freq *[256]uint32, n int) *[256]uint8 {
	minS, maxS, in := 255, 0, 0
	var size float64
	for s, f := range freq {
		if f != 0 {
			in++
			minS, maxS = min(minS, s), max(maxS, s)
			size += float64(f)
		}
	}
	if in <= n || n < 2 {
		return nil
	}
	const (
		maxIter        = 6
		errorThreshold = 1e-4 // mean squared error at which to stop
	)
	var inv [256]float64 // the level of each class
	var q [256]int       // the class of each value
	// Classes evenly spread to begin with; the first and the last stay.
	for i := 0; i < n; i++ {
		inv[i] = float64(minS) + float64(maxS-minS)*float64(i)/float64(n-1)
	}
	q[minS], q[maxS] = 0, n-1
	lastErr, threshold := 1e38, errorThreshold*size
	for range maxIter {
		var sum, count [256]float64
		slot := 0
		for s := minS; s <= maxS; s++ {
			// The nearest level: the classes are in order.
			for slot < n-1 && float64(2*s) > inv[slot]+inv[slot+1] {
				slot++
			}
			if f := freq[s]; f > 0 {
				sum[slot] += float64(int64(s) * int64(f))
				count[slot] += float64(f)
			}
			q[s] = slot
		}
		for slot := 1; slot < n-1; slot++ {
			if count[slot] > 0 {
				inv[slot] = sum[slot] / count[slot]
			}
		}
		var err float64
		for s := minS; s <= maxS; s++ {
			e := float64(s) - inv[q[s]]
			err += float64(float64(freq[s]) * e * e)
		}
		// Stop as soon as the error no longer improves.
		if lastErr-err < threshold {
			break
		}
		lastErr = err
	}
	var m [256]uint8
	for s := minS; s <= maxS; s++ {
		m[s] = uint8(inv[q[s]] + .5)
	}
	return &m
}

// reduceLevels reduces the spooled alpha plane to the levels of quality q
// (1 to 99): the rows are mapped as they are read back, and the statistics
// that choose how to code the plane are those of the mapped values (the
// filtered ones, which only the fallback coder looks at, as near as the
// counts allow).
func (s *alphaSpool) reduceLevels(q int) {
	m := quantizeLevels(&s.raw, alphaLevels(q))
	if m == nil {
		return
	}
	s.levels = m
	var raw [256]uint32
	for v, c := range s.raw {
		raw[m[v]] += c
	}
	s.raw = raw
}
