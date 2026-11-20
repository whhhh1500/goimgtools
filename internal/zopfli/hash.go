package zopfli

// The hash chains of the match finder (hash.c), with Zopfli's two
// options it compiles in: "same" (the run of the byte at each position)
// and a second hash of the 3 bytes and that run.

const (
	maxMatch     = 258
	minMatch     = 3
	numLL        = 288
	numD         = 32
	windowSize   = 32768
	windowMask   = windowSize - 1
	masterBlock  = 1000000
	largeFloat   = 1e30
	maxChainHits = 8192
	hashShift    = 5
	hashMask     = 32767
)

type hash struct {
	head    []int32
	prev    []uint16
	hashVal []int32
	val     int32

	head2    []int32
	prev2    []uint16
	hashVal2 []int32
	val2     int32

	same []uint16
}

func newHash(size int) *hash {
	return &hash{
		head: make([]int32, 65536), prev: make([]uint16, size), hashVal: make([]int32, size),
		head2: make([]int32, 65536), prev2: make([]uint16, size), hashVal2: make([]int32, size),
		same: make([]uint16, size),
	}
}

func (h *hash) reset() {
	h.val = 0
	for i := range h.head {
		h.head[i] = -1
		h.head2[i] = -1
	}
	for i := range h.prev {
		h.prev[i] = uint16(i)
		h.hashVal[i] = -1
		h.same[i] = 0
		h.prev2[i] = uint16(i)
		h.hashVal2[i] = -1
	}
	h.val2 = 0
}

func (h *hash) updateValue(c byte) {
	h.val = ((h.val << hashShift) ^ int32(c)) & hashMask
}

// update enters position pos of data (which ends at end).
func (h *hash) update(data []byte, pos, end int) {
	hpos := uint16(pos & windowMask)
	c := byte(0)
	if pos+minMatch <= end {
		c = data[pos+minMatch-1]
	}
	h.updateValue(c)
	h.hashVal[hpos] = h.val
	if hd := h.head[h.val]; hd != -1 && h.hashVal[hd] == h.val {
		h.prev[hpos] = uint16(hd)
	} else {
		h.prev[hpos] = hpos
	}
	h.head[h.val] = int32(hpos)

	amount := 0
	if s := h.same[(pos-1)&windowMask]; s > 1 {
		amount = int(s) - 1
	}
	for pos+amount+1 < end && data[pos] == data[pos+amount+1] && amount < 65535 {
		amount++
	}
	h.same[hpos] = uint16(amount)

	h.val2 = ((int32(h.same[hpos]) - minMatch) & 255) ^ h.val
	h.hashVal2[hpos] = h.val2
	if hd := h.head2[h.val2]; hd != -1 && h.hashVal2[hd] == h.val2 {
		h.prev2[hpos] = uint16(hd)
	} else {
		h.prev2[hpos] = hpos
	}
	h.head2[h.val2] = int32(hpos)
}

func (h *hash) warmup(data []byte, pos, end int) {
	h.updateValue(data[pos])
	if pos+1 < end {
		h.updateValue(data[pos+1])
	}
}
