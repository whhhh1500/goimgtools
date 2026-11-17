package webpstream

import (
	"image"
	"io"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// ScaleOnLoad has the image decoded at w x h, as libwebp scales it while it
// decodes it (WebPDecoderOptions.use_scaling, src/dec/io_dec.c and
// src/dec/vp8l_dec.c), which libvips asks for when sharp shrinks a WebP on
// load. The pixels are libwebp's to the byte:
//
//   - of a lossy image, the luma, the two chroma planes (each at its own
//     size: no upsampling) and the alpha plane are each scaled to w x h
//     (see rescaler) and the Y'CbCr turned into RGB pixel by pixel; when
//     both sides shrink below three quarters, the loop filter is skipped;
//   - of a lossless image, the pixels are premultiplied by their alpha,
//     scaled, and divided by it again.
//
// The strips are then NRGBA. It returns false, and changes nothing, once
// Next has been called, or for a size larger than the image's.
func (s *source) ScaleOnLoad(w, h int) bool {
	W, H := s.fileSize()
	if s.started || s.scaled || w <= 0 || h <= 0 || w > W || h > H {
		return false
	}
	s.scaled, s.orig = true, image.Pt(W, H)
	s.bounds = image.Rect(0, 0, w, h)
	if s.lossy && w < W*3/4 && h < H*3/4 {
		s.vd.SkipLoopFilter()
	}
	return true
}

// nextScaledLossy decodes the bands of a lossy image and hands out the
// rows out that every plane has given.
func (s *source) nextScaledLossy() (image.Image, error) {
	W, H := s.orig.X, s.orig.Y
	w, h := s.bounds.Dx(), s.bounds.Dy()
	if !s.started {
		s.started = true
		if err := s.startLossy(); err != nil {
			return nil, err
		}
		// The rows a band gives out: its rows scaled, a few more for the
		// rounding and for the planes that lag behind.
		out := (int64(s.bandRows)*int64(h)+int64(H)-1)/int64(H) + 6
		planes := int64(3)
		if s.hasAlpha {
			planes = 4
		}
		if err := s.reserve(planes*(workBytes(w, 1)+int64(w)*out)+4*int64(w)*out, "webp scaled rows"); err != nil {
			return nil, err
		}
		cw, ch := (W+1)/2, (H+1)/2
		newPlane := func(sw, sh int) *plane {
			return &plane{r: newRescaler(sw, sh, w, h, 1), q: make([]byte, 0, int(out)*w)}
		}
		s.sy, s.su, s.sv = newPlane(W, H), newPlane(cw, ch), newPlane(cw, ch)
		if s.hasAlpha {
			s.sa = newPlane(W, H)
		}
		s.band = make([]byte, 0, 4*w*int(out))
	}
	for {
		n := min(s.sy.rows(), s.su.rows(), s.sv.rows())
		if s.sa != nil {
			n = min(n, s.sa.rows())
		}
		if n > 0 {
			return s.emitLossy(n), nil
		}
		if s.y >= h {
			return nil, io.EOF
		}
		m, err := s.vd.NextBand()
		if err != nil {
			return nil, err
		}
		y0, y1 := m.Rect.Min.Y, m.Rect.Max.Y
		for y := y0; y < y1; y++ {
			o := m.YOffset(0, y)
			s.sy.push(m.Y[o : o+W])
		}
		// The chroma rows of the band; it starts on an even row.
		for k := y0 / 2; k < (y1+1)/2; k++ {
			o := m.COffset(0, 2*k)
			s.su.push(m.Cb[o : o+(W+1)/2])
			s.sv.push(m.Cr[o : o+(W+1)/2])
		}
		if s.sa != nil {
			a := s.alphaBuf[:W*(y1-y0)]
			if err := s.readAlpha(a, W, y1-y0); err != nil {
				return nil, err
			}
			for y := 0; y < y1-y0; y++ {
				s.sa.push(a[y*W : y*W+W])
			}
			s.sa.flush()
		}
		s.sy.flush()
		s.su.flush()
		s.sv.flush()
	}
}

// emitLossy turns the first n rows out of the planes into RGB (libwebp's
// YuvToRgbaRow) and hands them out.
func (s *source) emitLossy(n int) image.Image {
	w := s.bounds.Dx()
	if cap(s.band) < 4*w*n {
		s.band = make([]byte, 4*w*n)
	}
	pix := s.band[:4*w*n]
	for i := 0; i < n; i++ {
		ly, lu, lv := s.sy.q[i*w:i*w+w], s.su.q[i*w:i*w+w], s.sv.q[i*w:i*w+w]
		dst := pix[4*w*i : 4*w*i+4*w]
		for x, v := range ly {
			d := dst[4*x : 4*x+4]
			u, vv := int(lu[x]), int(lv[x])
			yy := multHi(int(v), 19077)
			d[0] = clip8(yy + multHi(vv, 26149) - 14234)
			d[1] = clip8(yy - multHi(u, 6419) - multHi(vv, 13320) + 8708)
			d[2] = clip8(yy + multHi(u, 33050) - 17685)
			d[3] = 0xff
		}
		if s.sa != nil {
			for x, a := range s.sa.q[i*w : i*w+w] {
				dst[4*x+3] = a
			}
		}
	}
	s.sy.drop(n)
	s.su.drop(n)
	s.sv.drop(n)
	if s.sa != nil {
		s.sa.drop(n)
	}
	y0 := s.y
	s.y += n
	return &image.NRGBA{Pix: pix, Stride: 4 * w, Rect: image.Rect(0, y0, w, s.y)}
}

// nextScaledLossless decodes rows of a lossless image until a strip of
// rows out is ready.
func (s *source) nextScaledLossless() (image.Image, error) {
	W, H := s.orig.X, s.orig.Y
	w, h := s.bounds.Dx(), s.bounds.Dy()
	if !s.started {
		s.started = true
		s.stripRows = pipeline.StripRows(4*int64(w), h)
		if testBandRows > 0 {
			s.stripRows = min(testBandRows, h)
		}
		rows := int64(s.stripRows) + 2
		if err := s.reserve(4*int64(W)+workBytes(w, 4)+8*int64(w)*rows, "webp scaled rows"); err != nil {
			return nil, err
		}
		s.row = make([]byte, 4*W)
		s.sy = &plane{r: newRescaler(W, H, w, h, 4), q: make([]byte, 0, 4*w*int(rows))}
		s.band = make([]byte, 4*w*int(rows))
	}
	if s.y >= h {
		return nil, io.EOF
	}
	for s.sy.rows() < s.stripRows && s.sy.r.srcY < H {
		row, err := s.ld.NextRow()
		if err != nil {
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return nil, err
		}
		copy(s.row, row)
		premultiply(s.row)
		s.sy.push(s.row)
		if s.sy.r.srcY == H {
			s.sy.flush()
		}
	}
	n := min(s.sy.rows(), s.stripRows)
	if n == 0 {
		return nil, io.ErrUnexpectedEOF
	}
	pix := s.band[:4*w*n]
	copy(pix, s.sy.q)
	s.sy.drop(n)
	unpremultiply(pix)
	y0 := s.y
	s.y += n
	return &image.NRGBA{Pix: pix, Stride: 4 * w, Rect: image.Rect(0, y0, w, s.y)}, nil
}
