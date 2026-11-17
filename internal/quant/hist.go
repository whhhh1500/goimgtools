package quant

import (
	"image"
	"image/color"
)

// The colour statistics of pass 1 are a histogram of fixed size, so that
// its memory does not depend on the image:
//
//   - opaque pixels (alpha 255): 65536 bins, red 5 bits, green 6, blue 5;
//   - translucent pixels (alpha 1..254): 15 alpha levels of 17 values each,
//     times 4096 bins (4 bits per channel). Colour matters less the more
//     transparent a pixel is, and such pixels are few (soft edges, shadows),
//     so coarser bins do;
//   - fully transparent pixels: one bin. Their colour cannot be seen, they
//     are all stored as (0,0,0,0).
//
// A bin keeps its pixel count and, per channel, the sum of the pixels'
// offsets from the bin's lowest value. The mean colour of a bin is therefore
// exact however coarse the bin is, and the palette is built from those means
// rather than from bin centres. Offsets are at most 16, and a bin stops
// counting at 2^27 (pixels count 1 to 4 times, see smoothWeight), so the
// sums fit 32 bits for any image size.
//
// 126977 bins of 20 bytes are 2.5MB. After pass 1 the occupied bins are
// turned, in place, into the weighted points the palette is computed from,
// so building the palette needs no further memory.
const (
	opaqueBins     = 1 << 16
	transLevels    = 15
	transBins      = transLevels << 12
	binTransparent = opaqueBins + transBins
	numBins        = binTransparent + 1
	binBytes       = 20
	binCap         = 1 << 27
)

// bin is a histogram bin during pass 1 (n pixels, v the channel offset
// sums) and a weighted point afterwards (v its coordinates, see coords).
type bin struct {
	n uint32
	v [4]int32
}

// Colours are compared in a space with premultiplied colour channels, as
// pngquant does: the more transparent two pixels are, the less their colour
// difference counts, and at alpha 0 it does not count at all. Coordinates
// are fixed point: red, green and blue times alpha/255 times 16, and alpha
// times 20. The larger factor of alpha is a weight of about 1.5 on its
// squared difference: an alpha error shows in all three channels over a
// light background and not at all over a dark one.
const (
	colorScale = 16
	alphaScale = 20
	maxC       = 255 * colorScale
	maxA       = 255 * alphaScale
)

// coords returns the coordinates of an 8-bit non-premultiplied colour.
func coords(r, g, b, a uint8) [4]int32 {
	if a == 255 {
		return [4]int32{int32(r) * colorScale, int32(g) * colorScale, int32(b) * colorScale, maxA}
	}
	k := int32(a) * colorScale
	return [4]int32{
		(int32(r)*k + 127) / 255,
		(int32(g)*k + 127) / 255,
		(int32(b)*k + 127) / 255,
		int32(a) * alphaScale,
	}
}

// smoothWeight is how much the pixel at row[i:] counts in the statistics:
// 1 to 4, more the smoother the row is around it (measured by the second
// difference along the row, so an even gradient counts as smooth). Errors
// show most in smooth areas, as banding or as dither noise, and are hidden
// by texture, so smooth areas get a larger share of the palette than their
// pixel count alone would give them. It looks at the row only, not at the
// rows above and below, so it does not depend on how the source is cut
// into strips.
func smoothWeight(row []byte, i int) int32 {
	l, r := max(i-4, 0), min(i+4, len(row)-4)
	var a int32
	for c := 0; c < 4; c++ {
		h := 2*int32(row[i+c]) - int32(row[l+c]) - int32(row[r+c])
		a += max(h, -h)
	}
	return max(1, 4-a/2)
}

// distinctMax is the most distinct colours that are tracked exactly.
const distinctMax = 256

// The exact colours of an image of up to exactMax of them (smooth
// gradients, graphics, screenshots) are counted as well: their palette is
// then chosen from the colours themselves rather than from bin means,
// which matters where the colours are closer together than the bins are
// wide (a sky of 300 shades falls into a few dozen bins). An open
// addressing table at most half full, 256KB, given back as soon as an
// image turns out to have more colours.
const (
	exactMax   = 1 << 14
	exactBits  = 15
	exactSlots = 1 << exactBits
	exactBytes = exactSlots * 8
)

// exactHist counts the weighted pixels of each colour while there are no
// more than exactMax colours. Fully transparent pixels are not counted, so
// key 0 marks an empty slot.
type exactHist struct {
	keys  []uint32
	n     []uint32
	count int
	over  bool
}

func newExactHist() *exactHist {
	return &exactHist{keys: make([]uint32, exactSlots), n: make([]uint32, exactSlots)}
}

func (e *exactHist) add(key, wt uint32) {
	h := key * 2654435761 >> (32 - exactBits)
	for {
		switch e.keys[h] {
		case key:
			if e.n[h] < binCap {
				e.n[h] += wt
			}
			return
		case 0:
			if e.count == exactMax {
				e.over, e.keys, e.n = true, nil, nil
				return
			}
			e.keys[h], e.n[h] = key, wt
			e.count++
			return
		}
		h = (h + 1) & (exactSlots - 1)
	}
}

// exactPoints turns the counted colours into weighted points at the front
// of bins and returns them; the first opaque of them are those of opaque
// colours.
func exactPoints(bins []bin, e *exactHist) (pts []bin, opaque int) {
	n := 0
	for pass := 0; pass < 2; pass++ {
		for h, key := range e.keys {
			if key == 0 || (key>>24 == 255) != (pass == 0) {
				continue
			}
			bins[n] = bin{n: e.n[h], v: coords(uint8(key), uint8(key>>8), uint8(key>>16), uint8(key>>24))}
			n++
		}
		if pass == 0 {
			opaque = n
		}
	}
	return bins[:n], opaque
}

// distinct collects the exact colours of the image while there are no more
// than limit of them.
type distinct struct {
	limit int
	over  bool
	keys  []uint32
	table [1024]int16 // index into keys plus one; 0 is empty
}

func (d *distinct) slot(key uint32) int {
	h := int(key * 2654435761 >> 22)
	for {
		i := d.table[h]
		if i == 0 || d.keys[i-1] == key {
			return h
		}
		h = (h + 1) & 1023
	}
}

func (d *distinct) add(key uint32) {
	h := d.slot(key)
	if d.table[h] != 0 {
		return
	}
	if len(d.keys) == d.limit {
		d.over = true
		return
	}
	d.keys = append(d.keys, key)
	d.table[h] = int16(len(d.keys))
}

// index returns the position of key in keys; key must have been added.
func (d *distinct) index(key uint32) int { return int(d.table[d.slot(key)]) - 1 }

func pixKey(p []byte) uint32 {
	return uint32(p[0]) | uint32(p[1])<<8 | uint32(p[2])<<16 | uint32(p[3])<<24
}

// addRow counts the pixels of one NRGBA row, after setting the fully
// transparent ones to (0,0,0,0).
func addRow(bins []bin, d *distinct, e *exactHist, row []byte) {
	for i := 3; i < len(row); i += 4 {
		if row[i] == 0 {
			row[i-3], row[i-2], row[i-1] = 0, 0, 0
		}
	}
	prev, have := uint32(0), false
	for i := 0; i+3 < len(row); i += 4 {
		p := row[i : i+4 : i+4]
		r, g, b, a := p[0], p[1], p[2], p[3]
		var k int
		var o [4]int32
		switch a {
		case 255:
			k = int(r>>3)<<11 | int(g>>2)<<5 | int(b>>3)
			o = [4]int32{int32(r & 7), int32(g & 3), int32(b & 7), 0}
		case 0:
			k = binTransparent
		default:
			l := int(a-1) / 17
			k = opaqueBins + l<<12 | int(r>>4)<<8 | int(g>>4)<<4 | int(b>>4)
			o = [4]int32{int32(r & 15), int32(g & 15), int32(b & 15), int32(int(a-1) - l*17)}
		}
		wt := smoothWeight(row, i)
		if q := &bins[k]; q.n < binCap {
			q.n += uint32(wt)
			q.v[0] += o[0] * wt
			q.v[1] += o[1] * wt
			q.v[2] += o[2] * wt
			q.v[3] += o[3] * wt
		}
		if !e.over && a != 0 {
			e.add(pixKey(p), uint32(wt))
		}
		if !d.over {
			if key := pixKey(p); !have || key != prev {
				prev, have = key, true
				d.add(key)
			}
		}
	}
}

// points turns the occupied bins, except the transparent one, into weighted
// points at the front of bins and returns them; the first opaque of them
// are those of the opaque pixels.
func points(bins []bin) (pts []bin, opaque int) {
	n := 0
	for k := 0; k < binTransparent; k++ {
		if k == opaqueBins {
			opaque = n
		}
		q := bins[k]
		if q.n == 0 {
			continue
		}
		var r, g, b, a float64
		w := float64(q.n)
		if k < opaqueBins {
			r = float64(k>>11<<3) + float64(q.v[0])/w
			g = float64(k>>5&63<<2) + float64(q.v[1])/w
			b = float64(k&31<<3) + float64(q.v[2])/w
			a = 255
		} else {
			j := k - opaqueBins
			r = float64(j>>8&15<<4) + float64(q.v[0])/w
			g = float64(j>>4&15<<4) + float64(q.v[1])/w
			b = float64(j&15<<4) + float64(q.v[2])/w
			a = float64(j>>12*17+1) + float64(q.v[3])/w
		}
		f := a / 255 * colorScale
		bins[n] = bin{n: q.n, v: [4]int32{
			int32(float64(r*f) + 0.5), int32(float64(g*f) + 0.5), int32(float64(b*f) + 0.5), int32(float64(a*alphaScale) + 0.5),
		}}
		n++
	}
	return bins[:n], opaque
}

// rowFunc returns a function that writes row y of strip s, columns
// [x0, x0+len(dst)/4), to dst as 8-bit non-premultiplied RGBA.
func rowFunc(s image.Image, x0 int) func(dst []byte, y int) {
	switch m := s.(type) {
	case *image.NRGBA:
		return func(dst []byte, y int) {
			o := m.PixOffset(x0, y)
			copy(dst, m.Pix[o:o+len(dst)])
		}
	case *image.RGBA:
		return func(dst []byte, y int) {
			o := m.PixOffset(x0, y)
			src := m.Pix[o : o+len(dst)]
			for i := 0; i+3 < len(dst); i += 4 {
				p, q := src[i:i+4:i+4], dst[i:i+4:i+4]
				switch a := uint32(p[3]); a {
				case 255:
					q[0], q[1], q[2], q[3] = p[0], p[1], p[2], 255
				case 0:
					q[0], q[1], q[2], q[3] = 0, 0, 0, 0
				default:
					// The arithmetic of color.NRGBAModel.
					a *= 0x101
					q[0] = uint8((uint32(p[0]) * 0x101 * 0xffff / a) >> 8)
					q[1] = uint8((uint32(p[1]) * 0x101 * 0xffff / a) >> 8)
					q[2] = uint8((uint32(p[2]) * 0x101 * 0xffff / a) >> 8)
					q[3] = p[3]
				}
			}
		}
	case *image.Gray:
		return func(dst []byte, y int) {
			src := m.Pix[m.PixOffset(x0, y):]
			for i := 0; i+3 < len(dst); i += 4 {
				v := src[i/4]
				dst[i], dst[i+1], dst[i+2], dst[i+3] = v, v, v, 255
			}
		}
	case *image.Gray16:
		return func(dst []byte, y int) {
			src := m.Pix[m.PixOffset(x0, y):]
			for i := 0; i+3 < len(dst); i += 4 {
				v := src[i/2]
				dst[i], dst[i+1], dst[i+2], dst[i+3] = v, v, v, 255
			}
		}
	case *image.NRGBA64:
		return func(dst []byte, y int) {
			src := m.Pix[m.PixOffset(x0, y):]
			for i := 0; i+3 < len(dst); i += 4 {
				p := src[2*i : 2*i+8 : 2*i+8]
				dst[i], dst[i+1], dst[i+2], dst[i+3] = p[0], p[2], p[4], p[6]
			}
		}
	case *image.RGBA64:
		return func(dst []byte, y int) {
			src := m.Pix[m.PixOffset(x0, y):]
			for i := 0; i+3 < len(dst); i += 4 {
				p, q := src[2*i:2*i+8:2*i+8], dst[i:i+4:i+4]
				a := uint32(p[6])<<8 | uint32(p[7])
				switch a {
				case 0xffff:
					q[0], q[1], q[2], q[3] = p[0], p[2], p[4], 255
				case 0:
					q[0], q[1], q[2], q[3] = 0, 0, 0, 0
				default:
					q[0] = uint8(min((uint32(p[0])<<8|uint32(p[1]))*0xffff/a, 0xffff) >> 8)
					q[1] = uint8(min((uint32(p[2])<<8|uint32(p[3]))*0xffff/a, 0xffff) >> 8)
					q[2] = uint8(min((uint32(p[4])<<8|uint32(p[5]))*0xffff/a, 0xffff) >> 8)
					q[3] = p[6]
				}
			}
		}
	case *image.Paletted:
		var lut [256][4]byte
		for i, c := range m.Palette {
			if i == 256 {
				break
			}
			n := color.NRGBAModel.Convert(c).(color.NRGBA)
			lut[i] = [4]byte{n.R, n.G, n.B, n.A}
		}
		return func(dst []byte, y int) {
			src := m.Pix[m.PixOffset(x0, y):]
			for i := 0; i+3 < len(dst); i += 4 {
				copy(dst[i:i+4], lut[src[i/4]][:])
			}
		}
	case *image.YCbCr:
		shift := -1
		if x0 >= 0 {
			switch m.SubsampleRatio {
			case image.YCbCrSubsampleRatio444, image.YCbCrSubsampleRatio440:
				shift = 0
			case image.YCbCrSubsampleRatio422, image.YCbCrSubsampleRatio420:
				shift = 1
			case image.YCbCrSubsampleRatio411, image.YCbCrSubsampleRatio410:
				shift = 2
			}
		}
		return func(dst []byte, y int) {
			yo := m.YOffset(x0, y)
			co := m.COffset(x0, y)
			for i, x := 0, x0; i+3 < len(dst); i, x = i+4, x+1 {
				ci := co
				if shift >= 0 {
					ci += x>>shift - x0>>shift
				} else {
					ci = m.COffset(x, y)
				}
				dst[i], dst[i+1], dst[i+2] = color.YCbCrToRGB(m.Y[yo+i/4], m.Cb[ci], m.Cr[ci])
				dst[i+3] = 255
			}
		}
	case *image.CMYK:
		return func(dst []byte, y int) {
			src := m.Pix[m.PixOffset(x0, y):]
			for i := 0; i+3 < len(dst); i += 4 {
				dst[i], dst[i+1], dst[i+2] = color.CMYKToRGB(src[i], src[i+1], src[i+2], src[i+3])
				dst[i+3] = 255
			}
		}
	}
	return func(dst []byte, y int) {
		for i, x := 0, x0; i+3 < len(dst); i, x = i+4, x+1 {
			n := color.NRGBAModel.Convert(s.At(x, y)).(color.NRGBA)
			dst[i], dst[i+1], dst[i+2], dst[i+3] = n.R, n.G, n.B, n.A
		}
	}
}
