package gifstream

// deferLZW is a lossless LZW encoder that, when its dictionary is full,
// keeps coding with it instead of clearing it (the "deferred clear" GIF
// decoders accept: the standard library's, browsers', gifsicle's). On an
// image whose patterns stay the same, the full dictionary keeps finding
// long strings where a cleared one would start over; on one whose patterns
// change, clearing is better: the animation encoder codes a frame both
// ways and keeps the smaller (as gifsicle does).
//
// Codes and widths are compress/lzw's up to the point it would clear; the
// encoder never defines code 4095, which a decoder does define, and so
// never uses an entry the decoder lacks.
type deferLZW struct {
	bw       blockWriter
	lit      uint
	width    uint
	clear    uint32
	hi       uint32 // the last code defined (or implied), as compress/lzw's
	overflow uint32
	full     bool
	acc      uint32
	nacc     uint
	cur      uint32
	started  bool
	// Once the dictionary is full it is cleared when the pixels per code
	// (an average over the last codes, in 1/256) fall below clearBelow/256
	// of what they were then; never with clearBelow 0.
	clearBelow uint32
	run, avg   uint32 // pixels of the code being built; their average
	atFull     uint32
	table      [deferTableSize]uint32 // key<<12 | code; 0 for none
}

const (
	deferTableBits = 14
	deferTableSize = 1 << deferTableBits
	deferTableMask = deferTableSize - 1
)

func (z *deferLZW) reset(bw blockWriter, lit uint, clearBelow uint32) {
	z.bw, z.lit, z.clearBelow = bw, lit, clearBelow
	z.run, z.avg = 0, 0
	z.clear = 1 << lit
	z.acc, z.nacc, z.started = 0, 0, false
	z.forget()
	z.put(z.clear)
}

// forget empties the dictionary and starts the codes again.
func (z *deferLZW) forget() {
	clear(z.table[:])
	z.width = z.lit + 1
	z.hi, z.overflow = z.clear+1, z.clear<<1
	z.full, z.atFull = false, 0
}

func (z *deferLZW) put(code uint32) {
	z.acc |= code << z.nacc
	z.nacc += z.width
	for z.nacc >= 8 {
		z.bw.WriteByte(byte(z.acc))
		z.acc >>= 8
		z.nacc -= 8
	}
}

func (z *deferLZW) write(pix []byte) {
	for _, p := range pix {
		lit := uint32(p)
		if !z.started {
			z.cur, z.started, z.run = lit, true, 1
			continue
		}
		z.run++
		key := z.cur<<8 | lit
		h := (key>>12 ^ key) & deferTableMask
		found := false
		for t := z.table[h]; t != 0; t = z.table[h] {
			if t>>12 == key {
				z.cur, found = t&lzwMaxCode, true
				break
			}
			h = (h + 1) & deferTableMask
		}
		if found {
			continue
		}
		z.put(z.cur)
		z.cur = lit
		// The average length of the codes, over about the last sixteen.
		z.avg += (z.run-1)<<8/16 - z.avg/16
		z.run = 1
		if z.full && z.avg*256 < z.atFull*z.clearBelow {
			z.put(z.clear)
			z.forget()
			continue
		}
		// Define the string just ended plus lit, as the decoder does.
		if z.inc() {
			z.table[h] = key<<12 | z.hi
		} else if z.full && z.atFull == 0 {
			z.atFull = max(z.avg, 1)
		}
	}
}

// inc implies the next code, as compress/lzw's incHi but for clearing:
// false once the dictionary is full (code 4095 is never defined here).
func (z *deferLZW) inc() bool {
	if z.full {
		return false
	}
	z.hi++
	if z.hi == z.overflow {
		z.width++
		z.overflow <<= 1
	}
	if z.hi == lzwMaxCode {
		z.full = true
		return false
	}
	return true
}

func (z *deferLZW) close() {
	if z.started {
		z.put(z.cur)
		z.inc() // the decoder counts this code before the end
	}
	z.put(z.clear + 1)
	if z.nacc > 0 {
		z.bw.WriteByte(byte(z.acc))
	}
}
