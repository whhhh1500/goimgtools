package resize

import (
	"errors"
	"image"
	"image/color"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// Shrink reduces a Gray or YCbCr source by an integer factor s (2, 4 or 8)
// by averaging s × s cells of every plane, which is what a JPEG decoder's
// "shrink on load" produces. It is a cheap first step before Scale when an
// image is reduced by 2× or more: Scale then reads a quarter (or less) of
// the pixels. The result is ceil(w/s) × ceil(h/s); cells cut by the right or
// bottom edge average the pixels they have.
//
// The source must start at (0, 0) and deliver strips whose first row is a
// multiple of s times the vertical chroma subsampling, as a JPEG decoder's
// MCU rows are. The result differs slightly from scaling the full image, so
// callers offer it as an option.
func Shrink(src pipeline.Source, s int) (pipeline.Source, error) {
	b := src.Bounds()
	if s != 2 && s != 4 && s != 8 {
		return nil, errors.New("goimgtools: shrink factor must be 2, 4 or 8")
	}
	if b.Min != (image.Point{}) || b.Empty() {
		return nil, errors.New("goimgtools: shrink needs an image that starts at 0,0")
	}
	return &shrinker{src: src, env: pipeline.EnvOf(src), s: s,
		w: (b.Dx() + s - 1) / s, h: (b.Dy() + s - 1) / s}, nil
}

type shrinker struct {
	src      pipeline.Source
	env      *pipeline.Env
	s, w, h  int
	gray     *image.Gray
	ycc      *image.YCbCr
	rows     int // rows the band holds
	acc      []uint32
	reserved int64
	err      error
}

func (k *shrinker) Env() *pipeline.Env      { return k.env }
func (k *shrinker) Bounds() image.Rectangle { return image.Rect(0, 0, k.w, k.h) }
func (k *shrinker) ColorModel() color.Model { return k.src.ColorModel() }
func (k *shrinker) Opaque() bool            { return pipeline.IsOpaque(k.src) }

func (k *shrinker) Close() error {
	if k.reserved != 0 {
		k.env.Budget.Release(k.reserved)
		k.reserved = 0
	}
	if k.err == nil {
		k.err = errors.New("goimgtools: shrink closed")
	}
	return k.src.Close()
}

// subsampling returns the horizontal and vertical chroma divisors.
func subsampling(r image.YCbCrSubsampleRatio) (hs, vs int, ok bool) {
	switch r {
	case image.YCbCrSubsampleRatio444:
		return 1, 1, true
	case image.YCbCrSubsampleRatio422:
		return 2, 1, true
	case image.YCbCrSubsampleRatio420:
		return 2, 2, true
	case image.YCbCrSubsampleRatio440:
		return 1, 2, true
	}
	return 0, 0, false
}

// ShrinkRatio reports whether Shrink handles YCbCr strips of ratio r.
func ShrinkRatio(r image.YCbCrSubsampleRatio) bool {
	_, _, ok := subsampling(r)
	return ok
}

func (k *shrinker) alloc(m image.Image, rows int) error {
	if rows <= k.rows {
		return nil
	}
	k.env.Budget.Release(k.reserved)
	k.reserved, k.rows, k.gray, k.ycc = 0, 0, nil, nil
	full := image.Rect(0, 0, k.w, rows)
	need := pipeline.BandBytes(m, full) + int64(k.w)*4
	if err := k.env.Budget.Reserve(need, "shrink band"); err != nil {
		return err
	}
	k.reserved, k.rows = need, rows
	if y, ok := m.(*image.YCbCr); ok {
		k.ycc = image.NewYCbCr(full, y.SubsampleRatio)
	} else {
		k.gray = image.NewGray(full)
	}
	if k.acc == nil {
		k.acc = make([]uint32, k.w)
	}
	return nil
}

func (k *shrinker) Next() (image.Image, error) {
	if k.err != nil {
		return nil, k.err
	}
	m, err := k.src.Next()
	if err != nil {
		k.err = err
		return nil, err
	}
	out, err := k.strip(m)
	if err != nil {
		k.err = err
		return nil, err
	}
	return out, nil
}

func (k *shrinker) strip(m image.Image) (image.Image, error) {
	r, s := m.Bounds(), k.s
	sb := k.src.Bounds()
	hs, vs := 1, 1
	switch p := m.(type) {
	case *image.Gray:
	case *image.YCbCr:
		var ok bool
		if hs, vs, ok = subsampling(p.SubsampleRatio); !ok {
			return nil, errors.New("goimgtools: shrink: unsupported chroma subsampling")
		}
	default:
		return nil, errors.New("goimgtools: shrink: unsupported pixel type")
	}
	if r.Min.X != 0 || r.Max.X != sb.Max.X || r.Min.Y%(s*vs) != 0 || (r.Max.Y != sb.Max.Y && r.Dy()%(s*vs) != 0) {
		return nil, errors.New("goimgtools: shrink: strips are not aligned")
	}
	rows := (r.Dy() + s - 1) / s
	if err := k.alloc(m, rows); err != nil {
		return nil, err
	}
	rect := image.Rect(0, r.Min.Y/s, k.w, r.Min.Y/s+rows)
	switch p := m.(type) {
	case *image.Gray:
		boxPlane(k.gray.Pix, k.gray.Stride, p.Pix[p.PixOffset(0, r.Min.Y):], p.Stride, r.Dx(), r.Dy(), s, k.acc)
		out := *k.gray
		out.Rect = rect
		return &out, nil
	case *image.YCbCr:
		d := k.ycc
		boxPlane(d.Y, d.YStride, p.Y[p.YOffset(0, r.Min.Y):], p.YStride, r.Dx(), r.Dy(), s, k.acc)
		cw, ch := (r.Dx()+hs-1)/hs, (r.Dy()+vs-1)/vs
		co := p.COffset(0, r.Min.Y)
		boxPlane(d.Cb, d.CStride, p.Cb[co:], p.CStride, cw, ch, s, k.acc)
		boxPlane(d.Cr, d.CStride, p.Cr[co:], p.CStride, cw, ch, s, k.acc)
		out := *d
		out.Rect = rect
		return &out, nil
	}
	panic("unreachable")
}

// boxPlane averages s × s cells of the w × h plane src into dst, which is
// ceil(w/s) × ceil(h/s). acc is scratch space of at least ceil(w/s) entries.
func boxPlane(dst []uint8, dstride int, src []uint8, sstride, w, h, s int, acc []uint32) {
	dw, fullW := (w+s-1)/s, w/s
	acc = acc[:dw]
	for j := 0; j*s < h; j++ {
		clear(acc)
		n := min(s, h-j*s) // rows in this cell row
		for y := j * s; y < j*s+n; y++ {
			row := src[y*sstride : y*sstride+w]
			switch s {
			case 2:
				for x := 0; x < fullW; x++ {
					p := row[2*x : 2*x+2 : 2*x+2]
					acc[x] += uint32(p[0]) + uint32(p[1])
				}
			case 4:
				for x := 0; x < fullW; x++ {
					p := row[4*x : 4*x+4 : 4*x+4]
					acc[x] += uint32(p[0]) + uint32(p[1]) + uint32(p[2]) + uint32(p[3])
				}
			default:
				for x := 0; x < fullW; x++ {
					p := row[8*x : 8*x+8 : 8*x+8]
					acc[x] += uint32(p[0]) + uint32(p[1]) + uint32(p[2]) + uint32(p[3]) +
						uint32(p[4]) + uint32(p[5]) + uint32(p[6]) + uint32(p[7])
				}
			}
			if fullW < dw { // the cell cut by the right edge
				var t uint32
				for _, v := range row[fullW*s:] {
					t += uint32(v)
				}
				acc[fullW] += t
			}
		}
		out := dst[j*dstride : j*dstride+dw]
		cells := uint32(n * s)
		for x := 0; x < fullW; x++ {
			out[x] = uint8((acc[x] + cells/2) / cells)
		}
		if fullW < dw {
			c := uint32(n * (w - fullW*s))
			out[fullW] = uint8((acc[fullW] + c/2) / c)
		}
	}
}
