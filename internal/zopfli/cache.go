package zopfli

// The longest match cache (cache.c): for each position of a block, the
// longest match found and, for up to cacheLength lengths, the distance of
// the shortest match of each length (sublen), 3 bytes an entry.

// The slices are made once, for the longest block a Compressor takes, and
// cut to each block.
type matchCache struct {
	length []uint16
	dist   []uint16
	sublen []byte
}

// cacheBytes is the memory of a cache for blocks of up to n bytes.
func cacheBytes(n int) int64 { return int64(n) * (2 + 2 + cacheLength*3) }

func makeMatchCache(n int) matchCache {
	return matchCache{length: make([]uint16, n), dist: make([]uint16, n), sublen: make([]byte, cacheLength*3*n)}
}

// init readies the cache for a block of blockSize bytes.
func (c *matchCache) init(blockSize int) {
	c.length, c.dist, c.sublen = c.length[:blockSize], c.dist[:blockSize], c.sublen[:cacheLength*3*blockSize]
	for i := range c.length {
		c.length[i] = 1 // length > 0 with dist 0: not filled in yet
	}
	clear(c.dist)
	clear(c.sublen)
}

// sublenToCache stores the distances of sublen[3:length+1] at pos.
func (c *matchCache) sublenToCache(sublen []uint16, pos, length int) {
	if length < 3 {
		return
	}
	cache := c.sublen[cacheLength*pos*3:]
	j := 0
	bestLength := 0
	for i := 3; i <= length; i++ {
		if i == length || sublen[i] != sublen[i+1] {
			cache[j*3] = byte(i - 3)
			cache[j*3+1] = byte(sublen[i])
			cache[j*3+2] = byte(sublen[i] >> 8)
			bestLength = i
			j++
			if j >= cacheLength {
				break
			}
		}
	}
	if j < cacheLength {
		cache[(cacheLength-1)*3] = byte(bestLength - 3)
	}
}

// cacheToSublen fills sublen up to the longest length cached at pos.
func (c *matchCache) cacheToSublen(pos, length int, sublen []uint16) {
	if length < 3 {
		return
	}
	maxLength := c.maxCachedSublen(pos)
	prevLength := 0
	cache := c.sublen[cacheLength*pos*3:]
	for j := 0; j < cacheLength; j++ {
		l := int(cache[j*3]) + 3
		dist := uint16(cache[j*3+1]) + 256*uint16(cache[j*3+2])
		for i := prevLength; i <= l; i++ {
			sublen[i] = dist
		}
		if l == maxLength {
			break
		}
		prevLength = l + 1
	}
}

// maxCachedSublen is the length up to which sublen is cached at pos (0 for
// none).
func (c *matchCache) maxCachedSublen(pos int) int {
	cache := c.sublen[cacheLength*pos*3:]
	if cache[1] == 0 && cache[2] == 0 {
		return 0
	}
	return int(cache[(cacheLength-1)*3]) + 3
}
