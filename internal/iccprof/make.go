package iccprof

import (
	"encoding/binary"
	"errors"
	"math"
)

// The conversion into a profile, and profiles made from primaries.

var errTarget = errors.New("iccprof: a profile to convert into must be an RGB profile of primaries and tone curves")

// Between returns the conversion of 8-bit values in profile a (nil for
// sRGB) into profile b: through D50 XYZ, linear light, the colours b cannot
// show clipped (relative colorimetric). a and b must be RGB profiles of
// primaries and tone curves; a conversion from another kind goes through
// sRGB first.
func Between(a, b *Profile) (*Transform, error) {
	if b == nil || b.lut != nil || b.gray != nil {
		return nil, errTarget
	}
	from := srgbXYZ
	t := &Transform{}
	if a != nil {
		if a.lut != nil || a.gray != nil {
			return nil, errTarget
		}
		from = a.toXYZ
	}
	t.m = mul(inverse(b.toXYZ), from)
	for c := 0; c < 3; c++ {
		for v := 0; v < 256; v++ {
			if a == nil {
				t.lin[c][v] = srgbDecode(float64(v) / 255)
			} else {
				t.lin[c][v] = a.trc[c].at(float64(v) / 255)
			}
		}
	}
	t.enc = new([3][gammaSteps + 1]uint8)
	for c := 0; c < 3; c++ {
		inverseCurve(&b.trc[c], &t.enc[c])
	}
	return t, nil
}

// inverseCurve fills out with the 8-bit value whose light, through c, is
// i/gammaSteps: the curve sampled, then searched.
func inverseCurve(c *curve, out *[gammaSteps + 1]uint8) {
	const n = 4096
	var fwd [n + 1]float64
	for k := range fwd {
		v := c.at(float64(k) / n)
		if k > 0 && v < fwd[k-1] {
			v = fwd[k-1] // kept rising
		}
		fwd[k] = v
	}
	k := 0
	for i := range out {
		y := float64(i) / gammaSteps
		for k < n && fwd[k] < y {
			k++
		}
		var v float64
		switch {
		case k == 0:
			v = 0
		case fwd[k] < y:
			v = 1
		default:
			lo, hi := fwd[k-1], fwd[k]
			f := 0.0
			if hi > lo {
				f = (y - lo) / (hi - lo)
			}
			v = (float64(k-1) + f) / n
		}
		out[i] = uint8(math.Round(math.Max(0, math.Min(1, v)) * 255))
	}
}

// bradford is the Bradford cone response matrix.
var bradford = [3][3]float64{
	{0.8951, 0.2664, -0.1614},
	{-0.7502, 1.7135, 0.0367},
	{0.0389, -0.0685, 1.0296},
}

// colorants is the matrix from linear RGB to D50 XYZ of primaries (x, y)
// and a white point, adapted from that white to D50 by Bradford.
func colorants(r, g, b, white [2]float64) [3][3]float64 {
	xyz := func(c [2]float64) [3]float64 {
		return [3]float64{c[0] / c[1], 1, (1 - c[0] - c[1]) / c[1]}
	}
	pr, pg, pb, w := xyz(r), xyz(g), xyz(b), xyz(white)
	p := [3][3]float64{{pr[0], pg[0], pb[0]}, {pr[1], pg[1], pb[1]}, {pr[2], pg[2], pb[2]}}
	pi := inverse(p)
	var s [3]float64
	for i := 0; i < 3; i++ {
		s[i] = float64(pi[i][0]*w[0]) + float64(pi[i][1]*w[1]) + float64(pi[i][2]*w[2])
	}
	var m [3][3]float64
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			m[i][j] = float64(p[i][j] * s[j])
		}
	}
	// Adapt: B^-1 diag(d50/white in cone space) B.
	cone := func(v [3]float64) [3]float64 {
		var o [3]float64
		for i := 0; i < 3; i++ {
			o[i] = float64(bradford[i][0]*v[0]) + float64(bradford[i][1]*v[1]) + float64(bradford[i][2]*v[2])
		}
		return o
	}
	cs, cd := cone(w), cone(d50)
	var dg [3][3]float64
	for i := 0; i < 3; i++ {
		dg[i][i] = cd[i] / cs[i]
	}
	return mul(mul(mul(inverse(bradford), dg), bradford), m)
}

// RGBProfile writes an ICC profile (version 2.1, a display's) of the
// primaries and white given (CIE xy), the tone curve a gamma (or, with
// gamma 0, the sRGB curve as a table of 1024), named desc.
func RGBProfile(desc string, r, g, b, white [2]float64, gamma float64) []byte {
	m := colorants(r, g, b, white)
	s15 := func(v float64) []byte {
		return binary.BigEndian.AppendUint32(nil, uint32(int32(math.Round(v*65536))))
	}
	xyzTag := func(x, y, z float64) []byte {
		t := append([]byte("XYZ \x00\x00\x00\x00"), s15(x)...)
		t = append(t, s15(y)...)
		return append(t, s15(z)...)
	}
	var trc []byte
	if gamma > 0 {
		trc = append([]byte("curv\x00\x00\x00\x00\x00\x00\x00\x01"), byte(int(gamma*256)>>8), byte(int(gamma*256)))
	} else {
		trc = append([]byte("curv\x00\x00\x00\x00"), binary.BigEndian.AppendUint32(nil, 1024)...)
		for i := 0; i < 1024; i++ {
			v := srgbDecode(float64(i) / 1023)
			trc = binary.BigEndian.AppendUint16(trc, uint16(math.Round(v*65535)))
		}
	}
	descTag := append([]byte("desc\x00\x00\x00\x00"), binary.BigEndian.AppendUint32(nil, uint32(len(desc)+1))...)
	descTag = append(append(descTag, desc...), 0)
	descTag = append(descTag, make([]byte, 4+4+2+1+67)...) // no Unicode, no ScriptCode
	cprt := append([]byte("text\x00\x00\x00\x00"), "No copyright, use freely\x00"...)
	wx, wy := white[0]/white[1], (1-white[0]-white[1])/white[1]
	tags := []struct {
		sig  string
		data []byte
	}{
		{"desc", descTag}, {"cprt", cprt}, {"wtpt", xyzTag(wx, 1, wy)},
		{"rXYZ", xyzTag(m[0][0], m[1][0], m[2][0])},
		{"gXYZ", xyzTag(m[0][1], m[1][1], m[2][1])},
		{"bXYZ", xyzTag(m[0][2], m[1][2], m[2][2])},
		{"rTRC", trc}, {"gTRC", trc}, {"bTRC", trc},
	}
	// Tags after the header and the table, each on four bytes; the three
	// curves share their data.
	off := 128 + 4 + 12*len(tags)
	var body []byte
	type ent struct{ off, size int }
	ents := make([]ent, len(tags))
	for i, t := range tags {
		if i > 6 { // gTRC and bTRC: rTRC's data
			ents[i] = ents[6]
			continue
		}
		for len(body)%4 != 0 {
			body = append(body, 0)
		}
		ents[i] = ent{off + len(body), len(t.data)}
		body = append(body, t.data...)
	}
	size := off + len(body)
	h := make([]byte, 128)
	binary.BigEndian.PutUint32(h[0:], uint32(size))
	binary.BigEndian.PutUint32(h[8:], 0x02100000)
	copy(h[12:], "mntrRGB XYZ ")
	// 2026-01-01, so that the bytes do not change from one run to another.
	for i, v := range []uint16{2026, 1, 1, 0, 0, 0} {
		binary.BigEndian.PutUint16(h[24+2*i:], v)
	}
	copy(h[36:], "acsp")
	copy(h[68:], s15(d50[0]))
	copy(h[72:], s15(d50[1]))
	copy(h[76:], s15(d50[2]))
	out := append(h, binary.BigEndian.AppendUint32(nil, uint32(len(tags)))...)
	for i, t := range tags {
		out = append(out, t.sig...)
		out = binary.BigEndian.AppendUint32(out, uint32(ents[i].off))
		out = binary.BigEndian.AppendUint32(out, uint32(ents[i].size))
	}
	return append(out, body...)
}
