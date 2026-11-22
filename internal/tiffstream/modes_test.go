package tiffstream

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"math/rand"
	"testing"
)

// samplesOf is the samples of an uncompressed, stripped file build made
// (its strips one after the other from byte 8), with a horizontal
// predictor undone.
func samplesOf(t *testing.T, data []byte, s spec) []byte {
	t.Helper()
	rowBytes := (s.w*len(s.bps)*int(s.bps[0]) + 7) / 8
	raw := append([]byte(nil), data[8:8+s.h*rowBytes]...)
	if !s.pred {
		return raw
	}
	n := len(s.bps) * int(s.bps[0]) / 8
	for y := 0; y < s.h; y++ {
		row := raw[y*rowBytes : (y+1)*rowBytes]
		for i := n; i < len(row); i++ {
			if s.bps[0] == 8 {
				row[i] += row[i-n]
			} else if i%2 == 0 {
				s.bo.PutUint16(row[i:], s.bo.Uint16(row[i:])+s.bo.Uint16(row[i-n:]))
			}
		}
	}
	return raw
}

// TestMoreModes: the kinds of TIFF read beyond x/image's: greys and
// palettes of 2 and 4 bits, grey with alpha (straight and multiplied, of 8
// and 16 bits, black or white as zero) and CMYK of 8 and 16 bits, in both
// byte orders, in one strip and in several, with the predictor.
func TestMoreModes(t *testing.T) {
	r := rand.New(rand.NewSource(11))
	bits := func(raw []byte, n, at int) uint32 { // sample at of n bits, from the high bits of each byte
		var v uint32
		for k := 0; k < n; k++ {
			v = v<<1 | uint32(raw[(at*n+k)/8]>>(7-uint((at*n+k)%8))&1)
		}
		return v
	}
	check := func(name string, s spec, want func(raw []byte) image.Image) {
		t.Helper()
		data := build(s, r)
		got, err := decodeStream(data, nil)
		if err != nil {
			t.Errorf("%s (%s): %v", name, s, err)
			return
		}
		if c, ok := got.(*image.CMYK); ok {
			if w := want(samplesOf(t, data, s)).(*image.CMYK); !bytes.Equal(c.Pix, w.Pix) || c.Rect != w.Rect {
				t.Errorf("%s (%s): the inks differ", name, s)
			}
			return
		}
		if err := same(want(samplesOf(t, data, s)), got); err != nil {
			t.Errorf("%s (%s): %v", name, s, err)
		}
	}
	const w, h = 12, 8 // rows of a whole number of bytes at every depth
	rect := image.Rect(0, 0, w, h)
	for _, bo := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
		for _, rps := range []int{0, 2} {
			u16 := func(raw []byte, i int) uint16 { return bo.Uint16(raw[2*i:]) }
			for _, n := range []uint32{2, 4} {
				for _, photo := range []uint32{pBlackIsZero, pWhiteIsZero} {
					check("grey", spec{w: w, h: h, bps: []uint32{n}, photo: photo, bo: bo, rps: rps}, func(raw []byte) image.Image {
						m := image.NewGray(rect)
						for i := range m.Pix {
							v := bits(raw, int(n), i) * 0xff / (1<<n - 1)
							if photo == pWhiteIsZero {
								v = 0xff - v
							}
							m.Pix[i] = uint8(v)
						}
						return m
					})
				}
				s := spec{w: w, h: h, bps: []uint32{n}, photo: pPaletted, bo: bo, rps: rps}
				data := build(s, r)
				got, err := decodeStream(data, nil)
				if p, ok := got.(*image.Paletted); err != nil || !ok || len(p.Palette) != 1<<n {
					t.Errorf("palette of %d bits (%s): %v, %T", n, s, err, got)
				} else {
					raw := samplesOf(t, data, s)
					for i := range p.Pix {
						if uint32(p.Pix[i]) != bits(raw, int(n), i) {
							t.Errorf("palette of %d bits (%s): pixel %d is %d", n, s, i, p.Pix[i])
							break
						}
					}
				}
			}
			for _, photo := range []uint32{pBlackIsZero, pWhiteIsZero} {
				for _, extra := range []uint32{1, 2} {
					for _, pred := range []bool{false, true} {
						grey := func(g, a, top uint32) uint32 {
							switch {
							case photo == pBlackIsZero:
								return g
							case extra == 1:
								return a - min(g, a)
							}
							return top - g
						}
						check("grey with alpha", spec{w: w, h: h, bps: []uint32{8, 8}, photo: photo, extra: extra, pred: pred, bo: bo, rps: rps}, func(raw []byte) image.Image {
							if extra == 1 {
								m := image.NewRGBA(rect)
								for i := 0; i < w*h; i++ {
									a := raw[2*i+1]
									g := uint8(grey(uint32(raw[2*i]), uint32(a), 0xff))
									copy(m.Pix[4*i:], []uint8{g, g, g, a})
								}
								return m
							}
							m := image.NewNRGBA(rect)
							for i := 0; i < w*h; i++ {
								a := raw[2*i+1]
								g := uint8(grey(uint32(raw[2*i]), uint32(a), 0xff))
								copy(m.Pix[4*i:], []uint8{g, g, g, a})
							}
							return m
						})
						check("grey with alpha, 16 bits", spec{w: w, h: h, bps: []uint32{16, 16}, photo: photo, extra: extra, pred: pred, bo: bo, rps: rps}, func(raw []byte) image.Image {
							var m image.Image = image.NewNRGBA64(rect)
							if extra == 1 {
								m = image.NewRGBA64(rect)
							}
							for i := 0; i < w*h; i++ {
								a := u16(raw, 2*i+1)
								g := uint16(grey(uint32(u16(raw, 2*i)), uint32(a), 0xffff))
								if extra == 1 {
									m.(*image.RGBA64).SetRGBA64(i%w, i/w, color.RGBA64{g, g, g, a})
								} else {
									m.(*image.NRGBA64).SetNRGBA64(i%w, i/w, color.NRGBA64{g, g, g, a})
								}
							}
							return m
						})
					}
				}
			}
			for _, pred := range []bool{false, true} {
				check("CMYK", spec{w: w, h: h, bps: []uint32{8, 8, 8, 8}, photo: pCMYK, pred: pred, bo: bo, rps: rps}, func(raw []byte) image.Image {
					m := image.NewCMYK(rect)
					copy(m.Pix, raw)
					return m
				})
				check("CMYK, 16 bits", spec{w: w, h: h, bps: []uint32{16, 16, 16, 16}, photo: pCMYK, pred: pred, bo: bo, rps: rps}, func(raw []byte) image.Image {
					m := image.NewCMYK(rect)
					for i := range m.Pix {
						m.Pix[i] = uint8(u16(raw, i) >> 8)
					}
					return m
				})
			}
		}
	}
	// Not read: grey with alpha of 4 bits, CMYK of three inks, inks that
	// are not CMYK's; a predictor on samples below 8 bits.
	for _, s := range []spec{
		{w: w, h: h, bps: []uint32{4, 4}, photo: pBlackIsZero, extra: 2, bo: binary.LittleEndian},
		{w: w, h: h, bps: []uint32{8, 8, 8}, photo: pCMYK, bo: binary.LittleEndian},
		{w: w, h: h, bps: []uint32{4}, photo: pBlackIsZero, pred: true, bo: binary.LittleEndian},
	} {
		if _, err := decodeStream(build(s, r), nil); err == nil {
			t.Errorf("%s: read", s)
		}
	}
}
