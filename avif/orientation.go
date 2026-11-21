package avif

import "image"

// orientation is the EXIF orientation that irot (anticlockwise quarter
// turns) and imir (axis 0: top and bottom exchanged, 1: left and right)
// together stand for, as libavif maps them (avifexif.c).
func orientation(rot bool, angle int, mir bool, axis int) int {
	switch {
	case rot && angle == 1:
		if mir {
			if axis != 0 {
				return 7
			}
			return 5
		}
		return 8
	case rot && angle == 2:
		if mir {
			if axis != 0 {
				return 4
			}
			return 2
		}
		return 3
	case rot && angle == 3:
		if mir {
			if axis != 0 {
				return 5
			}
			return 7
		}
		return 6
	case mir:
		if axis != 0 {
			return 2
		}
		return 4
	}
	return 1
}

// orient returns m (NRGBA or NRGBA64) turned upright from EXIF orientation
// o; m itself for 1.
func orient(m image.Image, o int) image.Image {
	if o <= 1 || o > 8 {
		return m
	}
	var pix []byte
	var stride, bpp int
	var r image.Rectangle
	switch s := m.(type) {
	case *image.NRGBA:
		pix, stride, bpp, r = s.Pix, s.Stride, 4, s.Rect
	case *image.NRGBA64:
		pix, stride, bpp, r = s.Pix, s.Stride, 8, s.Rect
	default:
		return m
	}
	sw, sh := r.Dx(), r.Dy()
	dw, dh := sw, sh
	if o >= 5 {
		dw, dh = sh, sw
	}
	ds := dw * bpp
	dst := make([]byte, ds*dh)
	for sy := range sh {
		row := pix[sy*stride:]
		for sx := range sw {
			var dx, dy int
			switch o {
			case 2:
				dx, dy = sw-1-sx, sy
			case 3:
				dx, dy = sw-1-sx, sh-1-sy
			case 4:
				dx, dy = sx, sh-1-sy
			case 5:
				dx, dy = sy, sx
			case 6:
				dx, dy = sh-1-sy, sx
			case 7:
				dx, dy = sh-1-sy, sw-1-sx
			case 8:
				dx, dy = sy, sw-1-sx
			}
			copy(dst[dy*ds+dx*bpp:][:bpp], row[sx*bpp:][:bpp])
		}
	}
	rect := image.Rect(0, 0, dw, dh)
	if bpp == 8 {
		return &image.NRGBA64{Pix: dst, Stride: ds, Rect: rect}
	}
	return &image.NRGBA{Pix: dst, Stride: ds, Rect: rect}
}
