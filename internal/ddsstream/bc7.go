package ddsstream

import (
	"encoding/binary"
	"math/bits"
)

// BC7 (BPTC): a block of 16 bytes in one of eight modes. A mode splits the
// 16 pixels into one, two or three subsets (by one of 64 fixed patterns),
// gives each subset two endpoint colours and each pixel an index that says
// how far between them it lies.

// bc7Mode is the layout of one mode.
type bc7Mode struct {
	subsets   uint8 // subsets of the block
	partBits  uint8 // bits of the pattern number
	rotBits   uint8 // bits of the channel rotation
	selBit    uint8 // bits choosing which indices are the colour's
	colour    uint8 // bits of a colour channel of an endpoint
	alpha     uint8 // bits of its alpha
	endP      uint8 // an extra low bit for each endpoint
	sharedP   uint8 // an extra low bit for each subset
	index     uint8 // bits of an index
	indexMore uint8 // bits of an index of the second set
}

var bc7Modes = [8]bc7Mode{
	{3, 4, 0, 0, 4, 0, 1, 0, 3, 0},
	{2, 6, 0, 0, 6, 0, 0, 1, 3, 0},
	{3, 6, 0, 0, 5, 0, 0, 0, 2, 0},
	{2, 6, 0, 0, 7, 0, 1, 0, 2, 0},
	{1, 0, 2, 1, 5, 6, 0, 0, 2, 3},
	{1, 0, 2, 0, 7, 8, 0, 0, 2, 2},
	{1, 0, 0, 0, 7, 7, 1, 0, 4, 0},
	{2, 6, 0, 0, 5, 5, 1, 0, 2, 0},
}

// bc7Weights are the weights, of 64, of the second endpoint for an index
// of 2, 3 and 4 bits.
var bc7Weights = [5][16]uint8{
	2: {0, 21, 43, 64},
	3: {0, 9, 18, 27, 37, 46, 55, 64},
	4: {0, 4, 9, 13, 17, 21, 26, 30, 34, 38, 43, 47, 51, 55, 60, 64},
}

// partition2 is, for each pattern of two subsets, a bit a pixel: its subset.
var partition2 = [64]uint16{
	0xcccc, 0x8888, 0xeeee, 0xecc8, 0xc880, 0xfeec, 0xfec8, 0xec80,
	0xc800, 0xffec, 0xfe80, 0xe800, 0xffe8, 0xff00, 0xfff0, 0xf000,
	0xf710, 0x008e, 0x7100, 0x08ce, 0x008c, 0x7310, 0x3100, 0x8cce,
	0x088c, 0x3110, 0x6666, 0x366c, 0x17e8, 0x0ff0, 0x718e, 0x399c,
	0xaaaa, 0xf0f0, 0x5a5a, 0x33cc, 0x3c3c, 0x55aa, 0x9696, 0xa55a,
	0x73ce, 0x13c8, 0x324c, 0x3bdc, 0x6996, 0xc33c, 0x9966, 0x0660,
	0x0272, 0x04e4, 0x4e40, 0x2720, 0xc936, 0x936c, 0x39c6, 0x639c,
	0x9336, 0x9cc6, 0x817e, 0xe718, 0xccf0, 0x0fcc, 0x7744, 0xee22,
}

// partition3 is, for each pattern of three subsets, two bits a pixel.
var partition3 = [64]uint32{
	0xaa685050, 0x6a5a5040, 0x5a5a4200, 0x5450a0a8,
	0xa5a50000, 0xa0a05050, 0x5555a0a0, 0x5a5a5050,
	0xaa550000, 0xaa555500, 0xaaaa5500, 0x90909090,
	0x94949494, 0xa4a4a4a4, 0xa9a59450, 0x2a0a4250,
	0xa5945040, 0x0a425054, 0xa5a5a500, 0x55a0a0a0,
	0xa8a85454, 0x6a6a4040, 0xa4a45000, 0x1a1a0500,
	0x0050a4a4, 0xaaa59090, 0x14696914, 0x69691400,
	0xa08585a0, 0xaa821414, 0x50a4a450, 0x6a5a0200,
	0xa9a58000, 0x5090a0a8, 0xa8a09050, 0x24242424,
	0x00aa5500, 0x24924924, 0x24499224, 0x50a50a50,
	0x500aa550, 0xaaaa4444, 0x66660000, 0xa5a0a5a0,
	0x50a050a0, 0x69286928, 0x44aaaa44, 0x66666600,
	0xaa444444, 0x54a854a8, 0x95809580, 0x96969600,
	0xa85454a8, 0x80959580, 0xaa141414, 0x96960000,
	0xaaaa1414, 0xa05050a0, 0xa0a5a5a0, 0x96000000,
	0x40804080, 0xa9a8a9a8, 0xaaaaaa44, 0x2a4a5254,
}

// The anchor of a subset is the pixel whose index is stored with one bit
// fewer (its top bit is 0). That of the first subset is pixel 0; anchor2 is
// that of the second subset of two, anchor3a and anchor3b those of the
// second and third of three.
var anchor2 = [64]uint8{
	15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15,
	15, 2, 8, 2, 2, 8, 8, 15, 2, 8, 2, 2, 8, 8, 2, 2,
	15, 15, 6, 8, 2, 8, 15, 15, 2, 8, 2, 2, 2, 15, 15, 6,
	6, 2, 6, 8, 15, 15, 2, 2, 15, 15, 15, 15, 15, 2, 2, 15,
}

var anchor3a = [64]uint8{
	3, 3, 15, 15, 8, 3, 15, 15, 8, 8, 6, 6, 6, 5, 3, 3,
	3, 3, 8, 15, 3, 3, 6, 10, 5, 8, 8, 6, 8, 5, 15, 15,
	8, 15, 3, 5, 6, 10, 8, 15, 15, 3, 15, 5, 15, 15, 15, 15,
	3, 15, 5, 5, 5, 8, 5, 10, 5, 10, 8, 13, 15, 12, 3, 3,
}

var anchor3b = [64]uint8{
	15, 8, 8, 3, 15, 15, 3, 8, 15, 15, 15, 15, 15, 15, 15, 8,
	15, 8, 15, 3, 15, 8, 15, 8, 3, 15, 6, 10, 15, 15, 10, 8,
	15, 3, 15, 10, 10, 8, 9, 10, 6, 15, 8, 15, 3, 6, 6, 8,
	15, 3, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 3, 15, 15, 8,
}

// bitReader takes the 128 bits of a block from the lowest up.
type bitReader struct {
	lo, hi uint64
	pos    uint
}

// get is the next n bits, n at most 8.
func (r *bitReader) get(n uint8) uint32 {
	p := r.pos
	var v uint64
	if p < 64 {
		v = r.lo >> p
		if p+uint(n) > 64 {
			v |= r.hi << (64 - p)
		}
	} else {
		v = r.hi >> (p - 64)
	}
	r.pos += uint(n)
	return uint32(v) & (1<<n - 1)
}

// bc7 decodes a BC7 block into the 16 pixels of px. A block of no mode
// (its first byte 0) is opaque black, as Pillow reads it.
func bc7(px *[64]byte, b []byte) {
	mode := bits.TrailingZeros8(b[0])
	if mode == 8 {
		for i := 0; i < 64; i += 4 {
			px[i], px[i+1], px[i+2], px[i+3] = 0, 0, 0, 255
		}
		return
	}
	m := &bc7Modes[mode]
	r := bitReader{lo: binary.LittleEndian.Uint64(b), hi: binary.LittleEndian.Uint64(b[8:]), pos: uint(mode) + 1}
	part := r.get(m.partBits)
	rot := r.get(m.rotBits)
	sel := r.get(m.selBit)

	// The endpoints: all the reds, then the greens, the blues, the alphas.
	var e [8][4]uint32
	ends := 2 * int(m.subsets)
	for c := 0; c < 3; c++ {
		for i := 0; i < ends; i++ {
			e[i][c] = r.get(m.colour)
		}
	}
	for i := 0; i < ends; i++ {
		e[i][3] = r.get(m.alpha)
	}
	cb, ab := uint(m.colour), uint(m.alpha)
	if m.endP != 0 || m.sharedP != 0 {
		// The extra bit becomes the lowest of every channel.
		var p uint32
		for i := 0; i < ends; i++ {
			if m.endP != 0 || i&1 == 0 {
				p = r.get(1)
			}
			for c := range e[i] {
				e[i][c] = e[i][c]<<1 | p
			}
		}
		cb++
		if ab != 0 {
			ab++
		}
	}
	// Widened to eight bits by repeating the top ones.
	for i := 0; i < ends; i++ {
		for c := 0; c < 3; c++ {
			v := e[i][c] << (8 - cb)
			e[i][c] = v | v>>cb
		}
		if ab == 0 {
			e[i][3] = 255
		} else {
			v := e[i][3] << (8 - ab)
			e[i][3] = v | v>>ab
		}
	}

	// The subset of each pixel and the anchors.
	var subset [16]uint8
	anchor := [4]uint8{0, 255, 255, 255}
	switch m.subsets {
	case 2:
		for i := range subset {
			subset[i] = uint8(partition2[part] >> uint(i) & 1)
		}
		anchor[1] = anchor2[part]
	case 3:
		for i := range subset {
			subset[i] = uint8(partition3[part] >> (2 * uint(i)) & 3)
		}
		anchor[1], anchor[2] = anchor3a[part], anchor3b[part]
	}
	var first, second [16]uint8
	for i := range first {
		n := m.index
		if uint8(i) == anchor[subset[i]] {
			n--
		}
		first[i] = uint8(r.get(n))
	}
	if m.indexMore != 0 {
		for i := range second {
			n := m.indexMore
			if i == 0 {
				n--
			}
			second[i] = uint8(r.get(n))
		}
	}
	// Which indices the colour and the alpha follow.
	ci, cw, ai, aw := &first, &bc7Weights[m.index], &first, &bc7Weights[m.index]
	if m.indexMore != 0 {
		if sel == 0 {
			ai, aw = &second, &bc7Weights[m.indexMore]
		} else {
			ci, cw = &second, &bc7Weights[m.indexMore]
		}
	}
	for i := 0; i < 16; i++ {
		e0, e1 := &e[2*subset[i]], &e[2*subset[i]+1]
		w := uint32(cw[ci[i]])
		var c [4]byte
		for k := 0; k < 3; k++ {
			c[k] = byte(((64-w)*e0[k] + w*e1[k] + 32) >> 6)
		}
		w = uint32(aw[ai[i]])
		c[3] = byte(((64-w)*e0[3] + w*e1[3] + 32) >> 6)
		// The rotation swaps alpha with one of the colour channels.
		if rot != 0 {
			c[rot-1], c[3] = c[3], c[rot-1]
		}
		px[4*i], px[4*i+1], px[4*i+2], px[4*i+3] = c[0], c[1], c[2], c[3]
	}
}
