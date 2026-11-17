package density

import (
	"encoding/binary"
	"testing"
)

func TestConvert(t *testing.T) {
	for _, c := range []struct {
		got, want float64
	}{
		{FromPerMetre(2835), 72},      // stored from 72 per inch
		{FromPerMetre(3780), 96},      // from 96
		{FromPerMetre(11811), 300},    // from 300
		{FromPerMetre(3799), 96.4946}, // 96.5 stored: no whole number gives it
		{FromPerCm(118), 300},
		{FromPerCm(28.35), 72.009},
		{FromPerInch(96.5), 96.5},
		{FromPerInch(300), 300},
		{FromPerInch(25.4), 0}, // libvips' "none"
		{FromPerInch(20), 0},
		{FromPerMetre(1000), 0},
		{FromPerInch(0), 0},
		{FromPerInch(-3), 0},
	} {
		if d := c.got - c.want; d > 1e-9 || d < -1e-9 {
			t.Errorf("got %v, want %v", c.got, c.want)
		}
	}
	for _, dpi := range []float64{72, 96, 150, 300, 600, 1200, 2400, 4800} {
		if got := FromPerMetre(float64(PerMetre(dpi))); got != dpi {
			t.Errorf("%v per inch through pixels per metre: %v", dpi, got)
		}
	}
	for _, c := range []struct {
		dpi      float64
		num, den uint32
	}{{72, 72, 1}, {96.5, 965, 10}, {72.25, 7225, 100}, {1.0 / 3, 3333, 10000}} {
		if n, d := Rational(c.dpi); n != c.num || d != c.den {
			t.Errorf("Rational(%v) = %d/%d, want %d/%d", c.dpi, n, d, c.num, c.den)
		}
	}
}

// exifWith makes an EXIF block (little or big endian) with the given
// resolution and unit in its first directory.
func exifWith(bo binary.ByteOrder, num, den uint32, unit uint16, withUnit bool) []byte {
	b := make([]byte, 8)
	if bo == binary.LittleEndian {
		copy(b, "II")
	} else {
		copy(b, "MM")
	}
	bo.PutUint16(b[2:], 42)
	bo.PutUint32(b[4:], 8)
	n := 2
	if withUnit {
		n = 3
	}
	ifd := make([]byte, 2+12*n+4)
	bo.PutUint16(ifd, uint16(n))
	values := 8 + len(ifd)
	put := func(i int, tag, typ uint16, count, val uint32) {
		e := ifd[2+12*i:]
		bo.PutUint16(e, tag)
		bo.PutUint16(e[2:], typ)
		bo.PutUint32(e[4:], count)
		bo.PutUint32(e[8:], val)
	}
	put(0, tagXResolution, 5, 1, uint32(values))
	put(1, tagYResolution, 5, 1, uint32(values+8))
	if withUnit {
		e := ifd[2+12*2:]
		bo.PutUint16(e, tagResolutionUnit)
		bo.PutUint16(e[2:], 3)
		bo.PutUint32(e[4:], 1)
		bo.PutUint16(e[8:], unit)
	}
	b = append(b, ifd...)
	r := make([]byte, 16)
	bo.PutUint32(r, num)
	bo.PutUint32(r[4:], den)
	bo.PutUint32(r[8:], num)
	bo.PutUint32(r[12:], den)
	return append(b, r...)
}

func TestExif(t *testing.T) {
	for _, bo := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
		if got := FromExif(exifWith(bo, 300, 1, 2, true)); got != 300 {
			t.Errorf("%v: 300 per inch read as %v", bo, got)
		}
		if got := FromExif(exifWith(bo, 600, 2, 0, false)); got != 300 {
			t.Errorf("%v: 600/2 without a unit (inches) read as %v", bo, got)
		}
		if got := FromExif(exifWith(bo, 118, 1, 3, true)); got != 300 {
			t.Errorf("%v: 118 per cm read as %v", bo, got)
		}
		if got := FromExif(exifWith(bo, 72, 1, 1, true)); got != 0 {
			t.Errorf("%v: no unit read as %v", bo, got)
		}
		if got := FromExif(exifWith(bo, 72, 0, 2, true)); got != 0 {
			t.Errorf("%v: a zero denominator read as %v", bo, got)
		}
		e := exifWith(bo, 118, 1, 3, true)
		set := SetExif(e, 96.5)
		if got := FromExif(set); got != 96.5 {
			t.Errorf("%v: set to 96.5, read %v", bo, got)
		}
		if FromExif(e) != 300 {
			t.Errorf("%v: SetExif changed its input", bo)
		}
	}
	// Short or foreign blocks: nothing, no panic.
	for _, b := range [][]byte{nil, []byte("II*\x00"), []byte("XX*\x00\x08\x00\x00\x00"), []byte("II*\x00\xff\xff\xff\x7f")} {
		if FromExif(b) != 0 || len(SetExif(b, 72)) != len(b) {
			t.Errorf("%q", b)
		}
	}
}
