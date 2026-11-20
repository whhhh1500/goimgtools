package zopfli

// The longest match cache (cache.c): for each position of a block, the
// longest match found and the distance of the shortest match of each
// length up to it (sublen), as the lengths at which that distance changes,
// 3 bytes each. Zopfli keeps 8 of them a position, and searches again at a
// position with more each time it is asked for them (3% of the positions
// of a photograph, half the time of the optimal parse); here they are kept
// one after the other in an area of their own, all of a position's or none,
// and a position is searched again only once the area is full. The matches
// are the same either way.
//
// The slices are made once, for the longest block a Compressor takes, and
// cut to each block.
type matchCache struct {
	length []uint16
	dist   []uint16
	at     []uint32 // where a position's lengths start in sub, plus 1; 0 for none
	sub    []byte   // for each position stored: the number of lengths less 1, then each length less 3 and its distance
	used   int
}

// subPerByte is the room in sub for each byte of a block: a photograph
// takes 12.4 bytes a position, a screenshot 5.4.
const subPerByte = 16

// cacheBytes is the memory of a cache for blocks of up to n bytes.
func cacheBytes(n int) int64 { return int64(n) * (2 + 2 + 4 + subPerByte) }

func makeMatchCache(n int) matchCache {
	return matchCache{length: make([]uint16, n), dist: make([]uint16, n), at: make([]uint32, n), sub: make([]byte, subPerByte*n)}
}

// init readies the cache for a block of blockSize bytes.
func (c *matchCache) init(blockSize int) {
	c.length, c.dist, c.at = c.length[:blockSize], c.dist[:blockSize], c.at[:blockSize]
	for i := range c.length {
		c.length[i] = 1 // length > 0 with dist 0: not filled in yet
	}
	clear(c.dist)
	clear(c.at)
	c.used = 0
}

// sublenToCache stores the distances of sublen[3:length+1] at pos, if the
// area has room for them all.
func (c *matchCache) sublenToCache(sublen []uint16, pos, length int) {
	if length < 3 {
		return
	}
	n := 0
	for i := 3; i <= length; i++ {
		if i == length || sublen[i] != sublen[i+1] {
			n++
		}
	}
	if c.used+1+3*n > len(c.sub) {
		return
	}
	c.at[pos] = uint32(c.used + 1)
	s := c.sub[c.used:]
	s[0] = byte(n - 1)
	j := 1
	for i := 3; i <= length; i++ {
		if i == length || sublen[i] != sublen[i+1] {
			s[j], s[j+1], s[j+2] = byte(i-3), byte(sublen[i]), byte(sublen[i]>>8)
			j += 3
		}
	}
	c.used += j
}

// cacheToSublen fills sublen up to the longest length cached at pos.
func (c *matchCache) cacheToSublen(pos, length int, sublen []uint16) {
	if length < 3 || c.at[pos] == 0 {
		return
	}
	s := c.sub[c.at[pos]-1:]
	n := int(s[0]) + 1
	prevLength := 0
	for j := 0; j < n; j++ {
		e := s[1+3*j:]
		l := int(e[0]) + 3
		dist := uint16(e[1]) | uint16(e[2])<<8
		for i := prevLength; i <= l; i++ {
			sublen[i] = dist
		}
		prevLength = l + 1
	}
}

// stored returns the lengths stored at pos, as the cache keeps them (3
// bytes each), when a search there for every length would be answered from
// them; nil otherwise.
func (s *blockState) stored(pos int) []byte {
	c := s.lmc
	if c == nil {
		return nil
	}
	lp := pos - s.blockStart
	if c.at[lp] == 0 || (c.length[lp] != 0 && c.dist[lp] == 0) {
		return nil
	}
	b := c.sub[c.at[lp]-1:]
	return b[1 : 1+3*(int(b[0])+1)]
}

// distFor is the distance of the shortest match of length l at pos, whose
// lengths are stored.
func (c *matchCache) distFor(pos, l int) uint16 {
	s := c.sub[c.at[pos]-1:]
	n := int(s[0]) + 1
	for j := 0; j < n; j++ {
		e := s[1+3*j:]
		if int(e[0])+3 >= l {
			return uint16(e[1]) | uint16(e[2])<<8
		}
	}
	return 0
}

// maxCachedSublen is the length up to which sublen is cached at pos (0 for
// none).
func (c *matchCache) maxCachedSublen(pos int) int {
	if c.at[pos] == 0 {
		return 0
	}
	s := c.sub[c.at[pos]-1:]
	return int(s[1+3*int(s[0])]) + 3
}
