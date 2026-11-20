package zopfli

// lz77Store holds an LZ77 parse (lz77.c): per item a literal (dist 0) or a
// length and distance, the position in the data, and every countsEvery
// items a cumulative histogram of the symbols. Its slices are made once,
// for the longest parse a Compressor takes, and used again. Zopfli also
// keeps the symbols of each item, which are found again here from the
// length and distance, and its histograms of the distances every 32
// items: the memory is about 12.5 bytes an item rather than 32.
type lz77Store struct {
	litlens  []uint16
	dists    []uint16
	pos      []int32
	llCounts []uint32
	dCounts  []uint32
	data     []byte
}

// countsEvery is how many items the histograms are apart.
const countsEvery = numLL

// storeBytes is the memory of a store of up to n items.
func storeBytes(n int) int64 {
	return int64(n)*(2+2+4) + 4*int64((numLL+numD)*(n/countsEvery+1))
}

func makeStore(n int) lz77Store {
	return lz77Store{
		litlens:  make([]uint16, 0, n),
		dists:    make([]uint16, 0, n),
		pos:      make([]int32, 0, n),
		llCounts: make([]uint32, 0, numLL*(n/countsEvery+1)),
		dCounts:  make([]uint32, 0, numD*(n/countsEvery+1)),
	}
}

func (s *lz77Store) size() int { return len(s.litlens) }

// reset empties the store for a parse of data.
func (s *lz77Store) reset(data []byte) {
	s.litlens, s.dists, s.pos = s.litlens[:0], s.dists[:0], s.pos[:0]
	s.llCounts, s.dCounts = s.llCounts[:0], s.dCounts[:0]
	s.data = data
}

// llSymbol and dSymbol are the symbols of item i.
func (s *lz77Store) llSymbol(i int) int {
	if s.dists[i] == 0 {
		return int(s.litlens[i])
	}
	return lengthSymbol(int(s.litlens[i]))
}

func (s *lz77Store) dSymbol(i int) int { return distSymbol(int(s.dists[i])) }

// add appends a literal (dist 0) or a match at pos.
func (s *lz77Store) add(length, dist uint16, pos int) {
	orig := len(s.litlens)
	start := orig / countsEvery
	if orig%countsEvery == 0 {
		s.llCounts = nextCounts(s.llCounts, numLL)
		s.dCounts = nextCounts(s.dCounts, numD)
	}
	s.litlens = append(s.litlens, length)
	s.dists = append(s.dists, dist)
	s.pos = append(s.pos, int32(pos))
	if dist == 0 {
		s.llCounts[numLL*start+int(length)]++
	} else {
		s.llCounts[numLL*start+lengthSymbol(int(length))]++
		s.dCounts[numD*start+distSymbol(int(dist))]++
	}
}

// nextCounts starts the next histogram of counts, k symbols each: a copy of
// the last, or zeros for the first.
func nextCounts(counts []uint32, k int) []uint32 {
	n := len(counts)
	if n+k > cap(counts) {
		counts = append(counts, make([]uint32, k)...)
	} else {
		counts = counts[:n+k]
	}
	if n == 0 {
		clear(counts)
	} else {
		copy(counts[n:], counts[n-k:n])
	}
	return counts
}

// truncate drops the items from n on.
func (s *lz77Store) truncate(n int) {
	if n >= s.size() {
		return
	}
	s.litlens, s.dists, s.pos = s.litlens[:n], s.dists[:n], s.pos[:n]
	if n == 0 {
		s.llCounts, s.dCounts = s.llCounts[:0], s.dCounts[:0]
		return
	}
	// The last histogram counts the items up to n alone.
	c := (n - 1) / countsEvery
	s.llCounts = s.llCounts[:numLL*(c+1)]
	s.dCounts = s.dCounts[:numD*(c+1)]
	ll, d := s.llCounts[numLL*c:], s.dCounts[numD*c:]
	if c == 0 {
		clear(ll)
		clear(d)
	} else {
		copy(ll, s.llCounts[numLL*(c-1):numLL*c])
		copy(d, s.dCounts[numD*(c-1):numD*c])
	}
	for i := countsEvery * c; i < n; i++ {
		ll[s.llSymbol(i)]++
		if s.dists[i] != 0 {
			d[s.dSymbol(i)]++
		}
	}
}

// appendFrom appends the items of src, a parse of the data from pos on.
func (s *lz77Store) appendFrom(src *lz77Store, pos int) {
	for i := range src.litlens {
		l, d := src.litlens[i], src.dists[i]
		s.add(l, d, pos)
		if d == 0 {
			pos++
		} else {
			pos += int(l)
		}
	}
}

// byteRange is how many bytes of data items lstart to lend cover.
func (s *lz77Store) byteRange(lstart, lend int) int {
	if lstart == lend {
		return 0
	}
	l := lend - 1
	n := 1
	if s.dists[l] != 0 {
		n = int(s.litlens[l])
	}
	return int(s.pos[l]) + n - int(s.pos[lstart])
}

func (s *lz77Store) histogramAt(lpos int, llCounts, dCounts []uint64) {
	c := lpos / countsEvery
	for i, v := range s.llCounts[numLL*c : numLL*(c+1)] {
		llCounts[i] = uint64(v)
	}
	for i, v := range s.dCounts[numD*c : numD*(c+1)] {
		dCounts[i] = uint64(v)
	}
	for i := lpos + 1; i < countsEvery*(c+1) && i < s.size(); i++ {
		llCounts[s.llSymbol(i)]--
		if s.dists[i] != 0 {
			dCounts[s.dSymbol(i)]--
		}
	}
}

// histogram counts the symbols of items lstart to lend.
func (s *lz77Store) histogram(lstart, lend int, llCounts, dCounts []uint64) {
	if lstart+numLL*3 > lend {
		clear(llCounts[:numLL])
		clear(dCounts[:numD])
		for i := lstart; i < lend; i++ {
			llCounts[s.llSymbol(i)]++
			if s.dists[i] != 0 {
				dCounts[s.dSymbol(i)]++
			}
		}
		return
	}
	s.histogramAt(lend-1, llCounts, dCounts)
	if lstart > 0 {
		var ll2 [numLL]uint64
		var d2 [numD]uint64
		s.histogramAt(lstart-1, ll2[:], d2[:])
		for i := 0; i < numLL; i++ {
			llCounts[i] -= ll2[i]
		}
		for i := 0; i < numD; i++ {
			dCounts[i] -= d2[i]
		}
	}
}
