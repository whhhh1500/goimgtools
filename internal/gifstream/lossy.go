package gifstream

import "image/color"

// Lossy LZW: the LZW encoder lengthens the current string with a pixel
// whose colour is close enough to the image's, not only with the exact
// one, and so finds longer strings in its dictionary: fewer codes for the
// same pixels. This is the idea of the lossy mode of gifsicle (by Kornel
// Lesiński, described at kornel.ski/lossygif); the code here was written
// from that description.
//
// At each pixel the encoder looks at the strings the dictionary has that
// continue the current one. The exact pixel is taken when one of them has
// it; otherwise the one whose colour is nearest the pixel's, if it is
// within the threshold. What the substitution changed is carried, halved,
// to the next pixel, so that small errors do not add up to a shift of
// colour along a run. A transparent pixel, and the transparent index, are
// only ever matched exactly. All the arithmetic is on integers, so that
// the output is the same on every processor.
//
// The codes, their widths and the clear codes follow compress/lzw's
// encoder: a decoder sees an ordinary GIF stream.

// lzwMaxCode is the last code a GIF stream may use.
const lzwMaxCode = 1<<12 - 1

type lzwChild struct {
	pix  uint8
	code uint16
}

// lossyLZW encodes the pixels of one frame at a time.
type lossyLZW struct {
	bw          blockWriter
	lit         uint
	width       uint
	clear       uint32
	hi          uint32
	overflow    uint32
	acc         uint32
	nacc        uint
	cur         uint32
	started     bool
	threshold   int32 // squared colour distance
	transparent int
	pal         [256][3]int32
	err         [3]int32
	// children[c] are the strings that continue string c by one pixel.
	children [1 << 12][]lzwChild
}

// reset starts a frame of palette pal written with literal width lit;
// transparent is -1 when the frame has no transparent index.
func (z *lossyLZW) reset(bw blockWriter, lit uint, pal color.Palette, transparent, threshold int) {
	z.bw = bw
	z.lit = lit
	z.clear = 1 << lit
	z.width = lit + 1
	z.hi, z.overflow = z.clear+1, z.clear<<1
	z.acc, z.nacc = 0, 0
	z.started = false
	z.threshold = int32(threshold * threshold)
	z.transparent = transparent
	z.err = [3]int32{}
	for i := range z.pal {
		z.pal[i] = [3]int32{}
	}
	for i, c := range pal {
		if i >= len(z.pal) {
			break
		}
		r, g, b, _ := c.RGBA()
		z.pal[i] = [3]int32{int32(r >> 8), int32(g >> 8), int32(b >> 8)}
	}
	z.forget()
	z.put(z.clear)
}

// forget empties the dictionary.
func (z *lossyLZW) forget() {
	for i := range z.children {
		z.children[i] = z.children[i][:0]
	}
}

func (z *lossyLZW) put(code uint32) {
	z.acc |= code << z.nacc
	z.nacc += z.width
	for z.nacc >= 8 {
		z.bw.WriteByte(byte(z.acc))
		z.acc >>= 8
		z.nacc -= 8
	}
}

// inc counts the code just implied, as compress/lzw's incHi; false when
// the dictionary was full and has been cleared.
func (z *lossyLZW) inc() bool {
	z.hi++
	if z.hi == z.overflow {
		z.width++
		z.overflow <<= 1
	}
	if z.hi == lzwMaxCode {
		z.put(z.clear)
		z.width, z.hi, z.overflow = z.lit+1, z.clear+1, z.clear<<1
		z.forget()
		return false
	}
	return true
}

func (z *lossyLZW) decay() {
	for k := range z.err {
		z.err[k] >>= 1
	}
}

// write encodes the next pixels of the frame.
func (z *lossyLZW) write(pix []byte) {
	if len(pix) == 0 {
		return
	}
	if !z.started {
		z.started = true
		z.cur = uint32(pix[0])
		pix = pix[1:]
	}
	for _, p := range pix {
		children := z.children[z.cur]
		next := -1
		for _, c := range children {
			if c.pix == p {
				next = int(c.code)
				break
			}
		}
		if next >= 0 {
			z.cur = uint32(next)
			z.decay()
			continue
		}
		if z.threshold > 0 && int(p) != z.transparent && len(children) > 0 {
			want := z.pal[p]
			for k := range want {
				want[k] += z.err[k]
			}
			best, q := z.threshold+1, -1
			for i, c := range children {
				if int(c.pix) == z.transparent {
					continue
				}
				v := z.pal[c.pix]
				dr, dg, db := v[0]-want[0], v[1]-want[1], v[2]-want[2]
				if d := dr*dr + dg*dg + db*db; d < best {
					best, q = d, i
				}
			}
			if q >= 0 {
				c := children[q]
				v := z.pal[c.pix]
				for k := range z.err {
					z.err[k] = (want[k] - v[k]) >> 1
				}
				z.cur = uint32(c.code)
				continue
			}
		}
		z.put(z.cur)
		if z.inc() {
			z.children[z.cur] = append(z.children[z.cur], lzwChild{p, uint16(z.hi)})
		}
		z.cur = uint32(p)
		z.decay()
	}
}

// close ends the frame: the last string, the end code, the last bits.
func (z *lossyLZW) close() {
	if z.started {
		z.put(z.cur)
		z.inc()
	}
	z.put(z.clear + 1)
	if z.nacc > 0 {
		z.bw.WriteByte(byte(z.acc))
		z.acc, z.nacc = 0, 0
	}
}
