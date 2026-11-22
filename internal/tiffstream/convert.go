package tiffstream

import (
	"math"
	"sync"

	"github.com/whhhh1500/goimgtools/internal/iccprof"
	"github.com/whhhh1500/goimgtools/internal/trig"
)

// goimgtools addition: files whose samples are not the unsigned integers
// of grey or RGB that the rest of this package reads. Their rows are made
// such samples before decode sees them:
//
//   - Floating point samples (SampleFormat 3, of 16, 32 or 64 bits), grey
//     or RGB, with or without alpha: linear light, 0 for black and 1 for
//     white, as libvips reads a TIFF of floats (scRGB) and as this module
//     reads PFM; made 16-bit samples of sRGB (the alpha as it is).
//   - CIELAB (photometric 8, and 9 with the offsets of ICC), of 8 or 16
//     bits (said to be signed, too, as libvips says of its 16 bits: L is
//     on 0 to 65535 all the same)
//     or of floats: made sRGB of 8 bits (from 8) or of 16, through D50
//     XYZ (the white libtiff and Photoshop take a Lab TIFF to have) and
//     sRGB's colorants adapted to D50, the colours sRGB does not have
//     clipped, as a Lab profile is converted. The file's profile, one of
//     Lab, is not the image's any more.
//
// The predictor (the horizontal one, of whole samples, or for floats the
// one of libtiff, predictor 3) is undone here on the samples as stored.

const (
	pICCLab = 9 // CIELAB with a and b offset by half the range, as ICC encodes it

	prFloat = 3 // libtiff's floating point predictor

	sfUint  = 1
	sfInt   = 2
	sfFloat = 3
)

// stored describes the samples of a file that convert reads.
type stored struct {
	bits    int  // of a sample: 8, 16, 32 or 64
	samples int  // a pixel
	float   bool // floating point
	lab     uint // the photometric interpretation, when it is a Lab
	pred    uint // the predictor
	out     int  // bits of a sample handed to decode: 8 or 16
}

// rowBits is the bits of a pixel in the file.
func (st *stored) rowBits() int { return st.bits * st.samples }

// setupStored looks at the sample format and the photometric
// interpretation the directory gave; for a file convert has to read, it
// sets d.stored and rewrites the features into those of the samples
// convert makes (BitsPerSample, the photometric interpretation of a Lab,
// no predictor), so that the rest sees a plain file.
func (d *decoder) setupStored() error {
	float, signed := false, false
	for i, v := range d.features[tSampleFormat] {
		switch {
		case v == sfUint && !float && !signed:
		case v == sfFloat && (i == 0 || float):
			float = true
		case v == sfInt && (i == 0 || signed):
			signed = true
		default:
			return UnsupportedError("sample format")
		}
	}
	photo := d.firstVal(tPhotometricInterpretation)
	lab := photo == pCIELab || photo == pICCLab
	if signed && (photo != pCIELab || d.firstVal(tBitsPerSample) != 16) {
		return UnsupportedError("sample format")
	}
	if !float && !lab {
		return nil
	}
	bps := d.features[tBitsPerSample]
	st := &stored{bits: int(d.firstVal(tBitsPerSample)), samples: len(bps), float: float, pred: d.firstVal(tPredictor), out: 16}
	for _, b := range bps {
		if int(b) != st.bits {
			return UnsupportedError("samples of different sizes")
		}
	}
	switch {
	case float && (st.bits == 16 || st.bits == 32 || st.bits == 64):
	case !float && (st.bits == 8 || st.bits == 16):
	default:
		return UnsupportedError("samples of this size")
	}
	switch {
	case lab:
		if st.samples != 3 || (photo == pICCLab && float) {
			return UnsupportedError("Lab of this kind")
		}
		st.lab = photo
		if st.bits == 8 {
			st.out = 8
		}
		d.features[tPhotometricInterpretation] = []uint{pRGB}
		d.icc = nil
	case photo == pRGB:
		if st.samples != 3 && st.samples != 4 {
			return UnsupportedError("floats of this kind")
		}
	case photo == pBlackIsZero || photo == pWhiteIsZero:
		if st.samples != 1 {
			return UnsupportedError("floats of this kind")
		}
	default:
		return UnsupportedError("floats of this kind")
	}
	switch st.pred {
	case 0, prNone, prHorizontal:
	case prFloat:
		if !float {
			return UnsupportedError("a floating point predictor on integers")
		}
	default:
		return UnsupportedError("predictor")
	}

	out := make([]uint, st.samples)
	for i := range out {
		out[i] = uint(st.out)
	}
	d.features[tBitsPerSample] = out
	delete(d.features, tPredictor)
	d.stored = st
	return nil
}

// gamma is the tone curve of sRGB from linear light, 0 to 1, in two
// tables its values are interpolated in: 4096 steps up to 1/16, where it
// bends most, and 4096 from there to 1 (within 0.01 of a 16-bit step of
// the curve itself). Made with trig.Pow: the same on every processor.
var gamma = sync.OnceValue(func() *[2][4097]float32 {
	var t [2][4097]float32
	enc := func(v float64) float32 {
		if v <= 0.0031308 {
			return float32(v * 12.92)
		}
		return float32(float64(1.055*trig.Pow(v, 1/2.4)) - 0.055)
	}
	for i := range t[0] {
		t[0][i] = enc(float64(i) / (16 * 4096))
		t[1][i] = enc(1.0/16 + float64(float64(float64(i)*(15.0/16))/4096))
	}
	return &t
})

// encode is the sRGB value, 0 to 1, of linear light v; what is not a
// number is black.
func encode(t *[2][4097]float32, v float64) float64 {
	if !(v > 0) {
		return 0
	}
	if v >= 1 {
		return 1
	}
	k, x := 0, float64(v*(16*4096))
	if v >= 1.0/16 {
		k, x = 1, float64((v-1.0/16)*(4096*16.0/15))
	}
	i := min(int(x), 4095)
	lo, hi := float64(t[k][i]), float64(t[k][i+1])
	return lo + float64((x-float64(i))*(hi-lo))
}

// unit is v held to 0..1, what is not a number 0.
func unit(v float64) float64 {
	if !(v > 0) {
		return 0
	}
	return min(v, 1)
}

// half is the value of a 16-bit float.
func half(h uint16) float64 {
	sign, exp, frac := 1.0, int(h>>10)&0x1f, float64(h&0x3ff)
	if h&0x8000 != 0 {
		sign = -1
	}
	switch exp {
	case 0:
		return sign * float64(frac/(1<<24)) // subnormal: frac × 2^-24
	case 31:
		if frac != 0 {
			return math.NaN()
		}
		return math.Inf(int(sign))
	}
	return sign * math.Ldexp(float64(1+float64(frac/1024)), exp-15)
}

// convert turns the stored row (of w pixels; the predictor still on it)
// into the samples decode reads, in dst, and returns them; what the row
// lacks of w pixels is left out, which decode reports.
func (d *decoder) convert(dst, row []byte, w int) []byte {
	st := d.stored
	bytes := st.bits / 8
	if n := len(row) / (bytes * st.samples); n < w {
		// A row cut short: its whole pixels (none under the float
		// predictor, which spreads every sample over the row).
		if w = n; st.pred == prFloat {
			w = 0
		}
	}
	count := w * st.samples
	row = row[:count*bytes]
	order := d.byteOrder

	// sample k as the bits stored.
	var bits func(k int) uint64
	switch st.pred {
	case prHorizontal:
		for i := st.samples; i < count; i++ {
			p, q := row[i*bytes:], row[(i-st.samples)*bytes:]
			switch bytes {
			case 1:
				p[0] += q[0]
			case 2:
				order.PutUint16(p, order.Uint16(p)+order.Uint16(q))
			case 4:
				order.PutUint32(p, order.Uint32(p)+order.Uint32(q))
			default:
				order.PutUint64(p, order.Uint64(p)+order.Uint64(q))
			}
		}
	case prFloat:
		// Each byte the difference from the byte a pixel's samples
		// before; then the samples' bytes one plane after the other, the
		// most significant first, whatever the file's byte order.
		for i := st.samples; i < len(row); i++ {
			row[i] += row[i-st.samples]
		}
		bits = func(k int) (v uint64) {
			for b := 0; b < bytes; b++ {
				v = v<<8 | uint64(row[b*count+k])
			}
			return v
		}
	}
	if bits == nil {
		bits = func(k int) uint64 {
			switch p := row[k*bytes:]; bytes {
			case 1:
				return uint64(p[0])
			case 2:
				return uint64(order.Uint16(p))
			case 4:
				return uint64(order.Uint32(p))
			default:
				return order.Uint64(p)
			}
		}
	}
	// value is sample k of floats.
	value := func(k int) float64 {
		switch v := bits(k); bytes {
		case 2:
			return half(uint16(v))
		case 4:
			return float64(math.Float32frombits(uint32(v)))
		default:
			return math.Float64frombits(v)
		}
	}
	put16 := func(i int, v float64) { // v on 0..1
		order.PutUint16(dst[2*i:], uint16(float64(v*65535)+0.5))
	}

	t := gamma()
	dst = dst[:count*st.out/8]
	if st.lab == 0 {
		// Floats: colour through the tone curve, alpha as it is.
		colours := min(st.samples, 3)
		for k := 0; k < count; k++ {
			if k%st.samples < colours {
				put16(k, encode(t, value(k)))
			} else {
				put16(k, unit(value(k)))
			}
		}
		return dst
	}
	for i := 0; i < w; i++ {
		var L, a, b float64
		switch {
		case st.float:
			L, a, b = value(3*i), value(3*i+1), value(3*i+2)
			if L != L || a != a || b != b {
				L, a, b = 0, 0, 0
			}
			L, a, b = max(0, min(L, 100)), max(-1000, min(a, 1000)), max(-1000, min(b, 1000))
		case st.bits == 8 && st.lab == pCIELab:
			L, a, b = float64(float64(bits(3*i))*100)/255, float64(int8(bits(3*i+1))), float64(int8(bits(3*i+2)))
		case st.bits == 8:
			L, a, b = float64(float64(bits(3*i))*100)/255, float64(bits(3*i+1))-128, float64(bits(3*i+2))-128

		case st.lab == pCIELab:
			L, a, b = float64(float64(bits(3*i))*100)/65535, float64(int16(bits(3*i+1)))/256, float64(int16(bits(3*i+2)))/256
		default:
			L, a, b = float64(float64(bits(3*i))*100)/65535, float64(float64(bits(3*i+1))/256)-128, float64(float64(bits(3*i+2))/256)-128
		}
		lin := iccprof.LabToLinear(L, a, b)
		for c := 0; c < 3; c++ {
			if v := encode(t, lin[c]); st.out == 8 {
				dst[3*i+c] = uint8(float64(v*255) + 0.5)
			} else {
				put16(3*i+c, v)
			}
		}
	}
	return dst
}
