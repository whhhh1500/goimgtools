package tiffstream

import (
	"encoding/binary"
	"fmt"
	"image"
	"math"
	"math/rand"
	"sort"
	"testing"

	"github.com/whhhh1500/goimgtools/internal/iccprof"
)

// rawSpec describes a TIFF made of rows of samples given as they are
// stored (before any predictor).
type rawSpec struct {
	w, h    int
	bits    int // of a sample
	samples int
	photo   uint32
	format  uint32 // SampleFormat; 0 leaves the tag out
	extra   uint32
	pred    uint32 // 0, prHorizontal or prFloat
	comp    uint32
	bo      binary.ByteOrder
	rps     int // rows of a strip; 0 for one strip
	tile    int // the side of a tile; 0 for strips
}

func (s rawSpec) String() string {
	bo := "LE"
	if s.bo == binary.BigEndian {
		bo = "BE"
	}
	return fmt.Sprintf("%dx%d/%dx%d bits/photo%d/format%d/extra%d/pred%d/comp%d/%s/rps%d/tile%d",
		s.w, s.h, s.samples, s.bits, s.photo, s.format, s.extra, s.pred, s.comp, bo, s.rps, s.tile)
}

// predict puts the predictor on a block's rows of w pixels, as a writer
// does: each sample the difference from the same sample of the pixel
// before, or for floats libtiff's predictor (the bytes of the samples in
// planes, the most significant first, then each byte the difference from
// the byte a pixel's samples before).
func (s rawSpec) predict(block []byte, w int) []byte {
	bytes := s.bits / 8
	rowLen := w * s.samples * bytes
	out := append([]byte(nil), block...)
	for at := 0; at+rowLen <= len(out); at += rowLen {
		row := out[at : at+rowLen]
		switch s.pred {
		case prHorizontal:
			for i := w*s.samples - 1; i >= s.samples; i-- {
				p, q := row[i*bytes:], row[(i-s.samples)*bytes:]
				switch bytes {
				case 1:
					p[0] -= q[0]
				case 2:
					s.bo.PutUint16(p, s.bo.Uint16(p)-s.bo.Uint16(q))
				case 4:
					s.bo.PutUint32(p, s.bo.Uint32(p)-s.bo.Uint32(q))
				default:
					s.bo.PutUint64(p, s.bo.Uint64(p)-s.bo.Uint64(q))
				}
			}
		case prFloat:
			count := w * s.samples
			tmp := make([]byte, rowLen)
			for k := 0; k < count; k++ {
				for b := 0; b < bytes; b++ {
					// byte b of the sample, from the most significant
					src := row[k*bytes+b]
					if s.bo == binary.LittleEndian {
						src = row[k*bytes+bytes-1-b]
					}
					tmp[b*count+k] = src
				}
			}
			for i := rowLen - 1; i >= s.samples; i-- {
				tmp[i] -= tmp[i-s.samples]
			}
			copy(row, tmp)
		}
	}
	return out
}

// buildRaw writes the TIFF of s whose rows are rows (h of them, each of
// w pixels as stored).
func buildRaw(s rawSpec, rows [][]byte) []byte {
	out := []byte(leHeader + "\x00\x00\x00\x00")
	if s.bo == binary.BigEndian {
		out = []byte(beHeader + "\x00\x00\x00\x00")
	}
	px := s.samples * s.bits / 8
	var offsets, counts []uint32
	add := func(raw []byte, w int) {
		c := compressBlock(s.comp, s.predict(raw, w))
		offsets = append(offsets, uint32(len(out)))
		counts = append(counts, uint32(len(c)))
		out = append(out, c...)
		if len(out)%2 != 0 {
			out = append(out, 0)
		}
	}
	if s.tile != 0 {
		for ty := 0; ty < s.h; ty += s.tile {
			for tx := 0; tx < s.w; tx += s.tile {
				block := make([]byte, s.tile*s.tile*px)
				for y := ty; y < min(ty+s.tile, s.h); y++ {
					copy(block[(y-ty)*s.tile*px:][:s.tile*px], rows[y][tx*px:])
				}
				add(block, s.tile)
			}
		}
	} else {
		rps := s.rps
		if rps == 0 {
			rps = s.h
		}
		for y := 0; y < s.h; y += rps {
			var block []byte
			for _, r := range rows[y:min(y+rps, s.h)] {
				block = append(block, r...)
			}
			add(block, s.w)
		}
	}
	rep := func(v uint32) []uint32 {
		out := make([]uint32, s.samples)
		for i := range out {
			out[i] = v
		}
		return out
	}
	ifd := []ifdEntry{
		{tImageWidth, dtLong, []uint32{uint32(s.w)}},
		{tImageLength, dtLong, []uint32{uint32(s.h)}},
		{tBitsPerSample, dtShort, rep(uint32(s.bits))},
		{tPhotometricInterpretation, dtShort, []uint32{s.photo}},
		{tSamplesPerPixel, dtShort, []uint32{uint32(s.samples)}},
	}
	if s.comp != 0 {
		ifd = append(ifd, ifdEntry{tCompression, dtShort, []uint32{s.comp}})
	}
	if s.tile != 0 {
		ifd = append(ifd, ifdEntry{tTileWidth, dtShort, []uint32{uint32(s.tile)}}, ifdEntry{tTileLength, dtShort, []uint32{uint32(s.tile)}},
			ifdEntry{tTileOffsets, dtLong, offsets}, ifdEntry{tTileByteCounts, dtLong, counts})
	} else {
		if s.rps != 0 {
			ifd = append(ifd, ifdEntry{tRowsPerStrip, dtShort, []uint32{uint32(s.rps)}})
		}
		ifd = append(ifd, ifdEntry{tStripOffsets, dtLong, offsets}, ifdEntry{tStripByteCounts, dtLong, counts})
	}
	if s.pred != 0 {
		ifd = append(ifd, ifdEntry{tPredictor, dtShort, []uint32{s.pred}})
	}
	if s.extra != 0 {
		ifd = append(ifd, ifdEntry{tExtraSamples, dtShort, []uint32{s.extra}})
	}
	if s.format != 0 {
		ifd = append(ifd, ifdEntry{tSampleFormat, dtShort, rep(s.format)})
	}
	sort.Slice(ifd, func(i, j int) bool { return ifd[i].tag < ifd[j].tag })
	at := len(out)
	s.bo.PutUint32(out[4:8], uint32(at))
	table := make([]byte, 2+ifdLen*len(ifd)+4)
	s.bo.PutUint16(table, uint16(len(ifd)))
	var extra []byte
	for i, e := range ifd {
		p := table[2+ifdLen*i:]
		s.bo.PutUint16(p[0:], e.tag)
		s.bo.PutUint16(p[2:], e.typ)
		s.bo.PutUint32(p[4:], uint32(len(e.vals)))
		var val []byte
		for _, v := range e.vals {
			if e.typ == dtLong {
				val = s.bo.(binary.AppendByteOrder).AppendUint32(val, v)
			} else {
				val = s.bo.(binary.AppendByteOrder).AppendUint16(val, uint16(v))
			}
		}
		if len(val) <= 4 {
			copy(p[8:12], val)
		} else {
			s.bo.PutUint32(p[8:], uint32(at+len(table)+len(extra)))
			extra = append(extra, val...)
		}
	}
	return append(append(out, table...), extra...)
}

// toHalf is the 16-bit float nearest below v, for v on 0 to 2: enough to
// write test values.
func toHalf(v float64) uint16 {
	if v <= 0 {
		return 0
	}
	frac, exp := math.Frexp(v) // v = frac × 2^exp, frac in [0.5, 1)
	e := exp - 1 + 15
	if e <= 0 {
		return uint16(v * (1 << 24)) // subnormal
	}
	return uint16(e)<<10 | uint16((frac*2-1)*1024)
}

// sixteen reads the 16-bit samples of pixel (x, y) of an image of 16 bits.
func sixteen(t *testing.T, m image.Image, x, y int) []uint16 {
	t.Helper()
	var pix []uint8
	n := 4
	switch p := m.(type) {
	case *image.Gray16:
		pix, n = p.Pix[p.PixOffset(x, y):], 1
	case *image.RGBA64:
		pix = p.Pix[p.PixOffset(x, y):]
	case *image.NRGBA64:
		pix = p.Pix[p.PixOffset(x, y):]
	default:
		t.Fatalf("an image of %T", m)
	}
	out := make([]uint16, n)
	for i := range out {
		out[i] = binary.BigEndian.Uint16(pix[2*i:])
	}
	return out
}

// TestFloats: TIFFs of floating point samples (16, 32 and 64 bits; grey,
// RGB, RGB with alpha) are read as linear light: each colour sample the
// sRGB value of 16 bits its light has (within 1 of 65535 of the curve),
// alpha as it is, what is below 0 or not a number 0 and what is above 1
// the most; in both byte orders, strips and tiles, with each predictor
// and compression.
func TestFloats(t *testing.T) {
	r := rand.New(rand.NewSource(43))
	curve := func(v float64) float64 {
		switch {
		case !(v > 0):
			return 0
		case v >= 1:
			return 65535
		case v <= 0.0031308:
			return v * 12.92 * 65535
		}
		return (1.055*math.Pow(v, 1/2.4) - 0.055) * 65535
	}
	const w, h = 37, 21
	n := 0
	for _, bits := range []int{16, 32, 64} {
		for _, kind := range []struct {
			samples int
			photo   uint32
			extra   uint32
		}{{1, pBlackIsZero, 0}, {1, pWhiteIsZero, 0}, {3, pRGB, 0}, {4, pRGB, 2}, {4, pRGB, 1}} {
			for _, bo := range bothOrders {
				for _, l := range []struct {
					pred, comp uint32
					rps, tile  int
				}{{0, 0, 0, 0}, {prFloat, cDeflate, 4, 0}, {prHorizontal, cLZW, 0, 16}, {prFloat, cZstd, 0, 16}, {0, cPackBits, 1, 0}} {
					s := rawSpec{w: w, h: h, bits: bits, samples: kind.samples, photo: kind.photo, format: sfFloat, extra: kind.extra,
						pred: l.pred, comp: l.comp, bo: bo, rps: l.rps, tile: l.tile}
					vals := make([]float64, w*h*s.samples)
					rows := make([][]byte, h)
					for y := range rows {
						rows[y] = make([]byte, w*s.samples*bits/8)
						for i := 0; i < w*s.samples; i++ {
							v := r.Float64()*1.5 - 0.25
							switch r.Intn(12) {
							case 0:
								v = 0
							case 1:
								v = 1
							case 2:
								v = r.Float64() / 200 // the straight part of the curve
							case 3:
								v = math.NaN()
							case 4:
								v = math.Inf(1 - 2*r.Intn(2))
							}
							p := rows[y][i*bits/8:]
							switch bits {
							case 16:
								if v != v || v > 2 { // not written by toHalf
									v = 1.5
								}
								hb := toHalf(v)
								// its value, not by the code under test
								if e := int(hb >> 10); e == 0 {
									v = math.Ldexp(float64(hb&0x3ff), -24)
								} else {
									v = math.Ldexp(float64(1024+int(hb&0x3ff)), e-25)
								}
								bo.PutUint16(p, hb)
							case 32:
								bo.PutUint32(p, math.Float32bits(float32(v)))
								v = float64(float32(v))
							default:
								bo.PutUint64(p, math.Float64bits(v))
							}
							vals[(y*w)*s.samples+i] = v
						}
					}
					got, err := decodeStream(buildRaw(s, rows), nil)
					if err != nil {
						t.Errorf("%s: %v", s, err)
						continue
					}
					n++
				pixels:
					for y := 0; y < h; y++ {
						for x := 0; x < w; x++ {
							px := sixteen(t, got, x, y)
							v := vals[(y*w+x)*s.samples:]
							var want [4]float64
							switch {
							case s.samples == 1:
								want[0] = curve(v[0])
								if kind.photo == pWhiteIsZero {
									want[0] = 65535 - want[0]
								}
							default:
								want = [4]float64{curve(v[0]), curve(v[1]), curve(v[2]), 65535}
								if s.samples == 4 {
									want[3] = math.Round(max(0, min(1, v[3])) * 65535)
									if v[3] != v[3] {
										want[3] = 0
									}
								}
							}
							for c := range px {
								if math.Abs(float64(px[c])-want[c]) > 1 {
									t.Errorf("%s: pixel (%d, %d) sample %d is %d, want %.2f (from %v)", s, x, y, c, px[c], want[c], v[:s.samples])
									break pixels
								}
							}
						}
					}
				}
			}
		}
	}
	t.Logf("%d files", n)

	// Not read: floats in a palette, of 8 bits, with the float predictor
	// on integers; integers that are signed.
	for _, s := range []rawSpec{
		{w: 4, h: 4, bits: 32, samples: 1, photo: pPaletted, format: sfFloat, bo: binary.LittleEndian},
		{w: 4, h: 4, bits: 8, samples: 3, photo: pRGB, format: sfFloat, bo: binary.LittleEndian},
		{w: 4, h: 4, bits: 16, samples: 3, photo: pCIELab, pred: prFloat, bo: binary.LittleEndian},
		{w: 4, h: 4, bits: 16, samples: 3, photo: pRGB, format: sfInt, bo: binary.LittleEndian},
		{w: 4, h: 4, bits: 32, samples: 2, photo: pBlackIsZero, format: sfFloat, extra: 2, bo: binary.LittleEndian},
	} {
		rows := make([][]byte, s.h)
		for y := range rows {
			rows[y] = make([]byte, s.w*s.samples*s.bits/8)
		}
		if _, err := decodeStream(buildRaw(s, rows), nil); err == nil {
			t.Errorf("%s: read", s)
		}
	}

	// A row cut short is an error.
	s := rawSpec{w: 8, h: 8, bits: 32, samples: 3, photo: pRGB, format: sfFloat, bo: binary.LittleEndian}
	rows := make([][]byte, 8)
	for y := range rows {
		rows[y] = make([]byte, 8*12)
	}
	rows[7] = rows[7][:50]
	if _, err := decodeStream(buildRaw(s, rows), nil); err == nil {
		t.Error("a float file whose last row is short: read")
	}
}

// TestLab: CIELAB TIFFs (8 and 16 bits, the 16 bits said to be signed as
// libvips says, floats; a and b signed, or offset as ICC has them) are
// read as the sRGB their colours have by the formulas (D50), within 1 of
// 255 (of 8 bits) or 2 of 65535.
func TestLab(t *testing.T) {
	r := rand.New(rand.NewSource(47))
	const w, h = 29, 17
	n := 0
	for _, kind := range []struct {
		name   string
		bits   int
		photo  uint32
		format uint32
	}{
		{"8 bits", 8, pCIELab, 0}, {"8 bits, ICC", 8, pICCLab, 0},
		{"16 bits", 16, pCIELab, sfUint}, {"16 bits, signed", 16, pCIELab, sfInt}, {"16 bits, ICC", 16, pICCLab, 0},
		{"floats", 32, pCIELab, sfFloat}, {"doubles", 64, pCIELab, sfFloat},
	} {
		for _, bo := range bothOrders {
			for _, l := range []struct {
				pred, comp uint32
				rps, tile  int
			}{{0, 0, 0, 0}, {prHorizontal, cDeflate, 3, 0}, {0, cLZW, 0, 16}} {
				s := rawSpec{w: w, h: h, bits: kind.bits, samples: 3, photo: kind.photo, format: kind.format,
					pred: l.pred, comp: l.comp, bo: bo, rps: l.rps, tile: l.tile}
				lab := make([][3]float64, w*h)
				rows := make([][]byte, h)
				for y := range rows {
					rows[y] = make([]byte, w*3*kind.bits/8)
					for x := 0; x < w; x++ {
						L, a, b := r.Float64()*100, r.Float64()*160-80, r.Float64()*160-80
						if r.Intn(6) == 0 {
							a, b = 0, 0 // a grey
						}
						p := rows[y][x*3*kind.bits/8:]
						switch {
						case kind.bits == 8:
							p[0] = uint8(L * 255 / 100)
							L = float64(p[0]) * 100 / 255
							a, b = math.Round(a), math.Round(b)
							p[1], p[2] = uint8(int8(a)), uint8(int8(b))
							if kind.photo == pICCLab {
								p[1], p[2] = uint8(a+128), uint8(b+128)
							}
						case kind.bits == 16:
							v := uint16(L * 65535 / 100)
							L = float64(v) * 100 / 65535
							a, b = math.Round(a*256)/256, math.Round(b*256)/256
							bo.PutUint16(p, v)
							bo.PutUint16(p[2:], uint16(int16(a*256)))
							bo.PutUint16(p[4:], uint16(int16(b*256)))
							if kind.photo == pICCLab {
								bo.PutUint16(p[2:], uint16((a+128)*256))
								bo.PutUint16(p[4:], uint16((b+128)*256))
							}
						case kind.bits == 32:
							for i, v := range []*float64{&L, &a, &b} {
								bo.PutUint32(p[4*i:], math.Float32bits(float32(*v)))
								*v = float64(float32(*v))
							}
						default:
							for i, v := range []float64{L, a, b} {
								bo.PutUint64(p[8*i:], math.Float64bits(v))
							}
						}
						lab[y*w+x] = [3]float64{L, a, b}
					}
				}
				got, err := decodeStream(buildRaw(s, rows), nil)
				if err != nil {
					t.Errorf("%s (%s): %v", kind.name, s, err)
					continue
				}
				n++
			pixels:
				for y := 0; y < h; y++ {
					for x := 0; x < w; x++ {
						c := lab[y*w+x]
						want := iccprof.LabToSRGB(c[0], c[1], c[2])
						for i := 0; i < 3; i++ {
							var have, limit float64
							if kind.bits == 8 {
								m, ok := got.(*image.RGBA)
								if !ok {
									t.Fatalf("%s: an image of %T", kind.name, got)
								}
								have, limit = float64(m.Pix[m.PixOffset(x, y)+i]), 1
							} else {
								have, limit = float64(sixteen(t, got, x, y)[i])/257, 2.0/257
							}
							if math.Abs(have-want[i]) > limit {
								t.Errorf("%s (%s): pixel (%d, %d) of Lab %v: sample %d is %.3f, want %.3f", kind.name, s, x, y, c, i, have, want[i])
								break pixels
							}
						}
					}
				}
			}
		}
	}
	t.Logf("%d files", n)

	// Rows of any bits: an image or an error, never a panic.
	for i := 0; i < 400; i++ {
		s := rawSpec{w: 9, h: 5, bits: []int{16, 32, 64}[i%3], samples: 3, photo: []uint32{pRGB, pCIELab}[i/3%2], format: sfFloat,
			pred: []uint32{0, prHorizontal, prFloat}[r.Intn(3)], bo: bothOrders[i%2]}
		rows := make([][]byte, s.h)
		for y := range rows {
			rows[y] = make([]byte, s.w*3*s.bits/8)
			r.Read(rows[y])
		}
		if _, err := decodeStream(buildRaw(s, rows), nil); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
}
