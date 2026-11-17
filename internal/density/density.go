// Package density converts the resolutions image files record (pixels per
// inch, centimetre or metre) to and from pixels per inch, and reads and
// updates the resolution of an EXIF block.
package density

import (
	"encoding/binary"
	"math"
)

// FromPerInch, FromPerCm and FromPerMetre convert a resolution to pixels
// per inch. A value that a whole number of pixels per inch is stored as
// gives that whole number (2835 per metre: 72, not 72.009), as the file
// was most likely written from it. One pixel per millimetre (25.4 per
// inch) or less is taken for none, as sharp takes it: it is what libvips
// writes when it knows no resolution.
func FromPerInch(v float64) float64 { return clean(v, 1) }

func FromPerCm(v float64) float64 { return clean(v, 2.54) }

func FromPerMetre(v float64) float64 { return clean(v, 0.0254) }

func clean(v, toInch float64) float64 {
	if !(v > 0) || math.IsInf(v, 0) {
		return 0
	}
	d := v * toInch
	if !(d > 25.4) {
		return 0
	}
	if r := math.Round(d); r > 0 && math.Round(r/toInch) == math.Round(v) && math.Abs(v-math.Round(v)) < 1e-9 {
		return r
	}
	return d
}

// PerMetre is dpi in whole pixels per metre (PNG, BMP).
func PerMetre(dpi float64) uint32 {
	return uint32(math.Min(math.Round(dpi/0.0254), math.MaxInt32))
}

// Rational is dpi as a fraction, exact for whole and most decimal numbers
// (TIFF and EXIF).
func Rational(dpi float64) (num, den uint32) {
	for _, d := range []uint32{1, 10, 100, 1000, 10000} {
		if v := dpi * float64(d); v == math.Round(v) && v <= math.MaxUint32 {
			return uint32(v), d
		}
	}
	return uint32(math.Min(math.Round(dpi*10000), math.MaxUint32)), 10000
}

// The tags of the first image directory of an EXIF block that record the
// resolution, and the units of ResolutionUnit.
const (
	tagXResolution    = 0x011a
	tagYResolution    = 0x011b
	tagResolutionUnit = 0x0128
	unitInch          = 2
	unitCm            = 3
)

// entry is a field of the first directory: its type, count and the offset
// of its value (or of the four bytes holding it).
type entry struct {
	typ   uint16
	count uint32
	at    int
}

// ifd0 returns the fields of the first directory of an EXIF block (in its
// TIFF form) that record the resolution.
func ifd0(exif []byte) (bo binary.ByteOrder, x, y, unit entry, ok bool) {
	if len(exif) < 8 {
		return nil, x, y, unit, false
	}
	switch string(exif[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return nil, x, y, unit, false
	}
	off := int64(bo.Uint32(exif[4:8]))
	if off < 8 || off+2 > int64(len(exif)) {
		return nil, x, y, unit, false
	}
	n := int64(bo.Uint16(exif[off:]))
	for i := int64(0); i < n; i++ {
		p := off + 2 + 12*i
		if p+12 > int64(len(exif)) {
			break
		}
		e := entry{typ: bo.Uint16(exif[p+2:]), count: bo.Uint32(exif[p+4:]), at: int(p + 8)}
		size := map[uint16]int64{3: 2, 5: 8}[e.typ] * int64(e.count)
		if size > 4 {
			v := int64(bo.Uint32(exif[p+8:]))
			if v < 8 || v+size > int64(len(exif)) {
				continue
			}
			e.at = int(v)
		}
		switch bo.Uint16(exif[p:]) {
		case tagXResolution:
			x = e
		case tagYResolution:
			y = e
		case tagResolutionUnit:
			unit = e
		}
	}
	return bo, x, y, unit, true
}

// FromExif returns the horizontal resolution an EXIF block records, in
// pixels per inch, or 0 when it records none (or none in inches or
// centimetres).
func FromExif(exif []byte) float64 {
	bo, x, _, unit, ok := ifd0(exif)
	if !ok || x.typ != 5 || x.count < 1 {
		return 0
	}
	u := uint16(unitInch) // the default of EXIF
	if unit.typ == 3 && unit.count >= 1 {
		u = bo.Uint16(exif[unit.at:])
	}
	num, den := bo.Uint32(exif[x.at:]), bo.Uint32(exif[x.at+4:])
	if den == 0 {
		return 0
	}
	switch u {
	case unitInch:
		return FromPerInch(float64(num) / float64(den))
	case unitCm:
		return FromPerCm(float64(num) / float64(den))
	}
	return 0
}

// SetExif returns a copy of an EXIF block whose resolution fields, those
// it has, record dpi pixels per inch.
func SetExif(exif []byte, dpi float64) []byte {
	bo, x, y, unit, ok := ifd0(exif)
	if !ok || dpi <= 0 {
		return exif
	}
	out := append([]byte(nil), exif...)
	num, den := Rational(dpi)
	for _, e := range []entry{x, y} {
		if e.typ == 5 && e.count >= 1 {
			bo.PutUint32(out[e.at:], num)
			bo.PutUint32(out[e.at+4:], den)
		}
	}
	if unit.typ == 3 && unit.count >= 1 {
		bo.PutUint16(out[unit.at:], unitInch)
	}
	return out
}
