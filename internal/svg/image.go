package svg

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"io"
	"math"
	"net/url"
	"strings"

	"github.com/whhhh1500/goimgtools/internal/raster"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// DecodeImage decodes the picture of an image element, a data URI's
// bytes, as non-premultiplied RGBA. The package that reads files sets it;
// without it images are not drawn.
var DecodeImage func(data []byte, o DecodeOptions) (*image.NRGBA, error)

// DecodeOptions is what DecodeImage is asked for.
type DecodeOptions struct {
	// Fit gives the size to decode a picture of w x h pixels at: smaller
	// where it is drawn much smaller (a resize, better than sampling it,
	// and less to hold). Nil: its own size.
	Fit func(w, h int) (int, int)
	// MaxPixels bounds the pixels of the decoded picture, MaxInputPixels
	// those of the picture in its file.
	MaxPixels, MaxInputPixels int64
	// Memory bounds the memory decoding takes (0: the default limit).
	Memory int64
}

// maxImagePixels bounds the pictures an SVG holds, all together, and
// maxInputPixels those its files hold (what they take to decode).
const (
	maxImagePixels = 64 << 20
	maxInputPixels = 1 << 28
)

// sampler colours a shape with a picture: bilinear, premultiplied, its
// edges held.
type sampler struct {
	img *image.NRGBA
	inv matrix // device to picture pixels
}

func (s *sampler) at(px, py float64) [4]uint8 {
	u, v := s.inv.apply(px, py)
	u, v = u-0.5, v-0.5
	b := s.img.Rect
	w, h := b.Dx(), b.Dy()
	x0, y0 := int(math.Floor(u)), int(math.Floor(v))
	fx, fy := u-float64(x0), v-float64(y0)
	clampi := func(i, n int) int { return max(0, min(n-1, i)) }
	var acc [4]float64
	for k := 0; k < 4; k++ {
		xi, yi := clampi(x0+k%2, w), clampi(y0+k/2, h)
		wx, wy := 1-fx, 1-fy
		if k%2 == 1 {
			wx = fx
		}
		if k/2 == 1 {
			wy = fy
		}
		wgt := float64(wx * wy)
		p := s.img.Pix[s.img.PixOffset(b.Min.X+xi, b.Min.Y+yi):]
		a := float64(p[3]) / 255
		acc[0] += float64(float64(float64(p[0])*a) * wgt)
		acc[1] += float64(float64(float64(p[1])*a) * wgt)
		acc[2] += float64(float64(float64(p[2])*a) * wgt)
		acc[3] += float64(float64(p[3]) * wgt)
	}
	return [4]uint8{uint8(math.Round(acc[0])), uint8(math.Round(acc[1])), uint8(math.Round(acc[2])), uint8(math.Round(acc[3]))}
}

// dataURI decodes the bytes of a data: URI.
func dataURI(href string) ([]byte, bool) {
	h := strings.TrimSpace(href)
	if !strings.HasPrefix(h, "data:") {
		return nil, false
	}
	comma := strings.IndexByte(h, ',')
	if comma < 0 {
		return nil, false
	}
	meta, body := h[5:comma], h[comma+1:]
	if strings.HasSuffix(meta, ";base64") {
		body = strings.Map(func(r rune) rune {
			if r == ' ' || r == '\n' || r == '\r' || r == '\t' {
				return -1
			}
			return r
		}, body)
		b, err := base64.StdEncoding.DecodeString(strings.TrimRight(body, "="))
		if err != nil {
			b, err = base64.RawStdEncoding.DecodeString(strings.TrimRight(body, "="))
		}
		return b, err == nil
	}
	s, err := url.PathUnescape(body)
	return []byte(s), err == nil
}

// image draws an image element whose picture is a data URI (files it
// names are not read).
func (b *builder) image(n *node, m matrix, st style, vp viewport, own, outer *clip) {
	if !st.visible || st.opacity <= 0 {
		return
	}
	href, ok := n.attr("href")
	if !ok {
		href, ok = n.attr("xlink:href")
	}
	// The picture's bytes, held while it is decoded: fewer than the
	// characters of the URI (base64 and percent-encoding are longer).
	held := int64(len(href))
	if err := b.reserve(held); err != nil {
		b.err = err
		return
	}
	defer b.release(held)
	data, ok := dataURI(href)
	if !ok || len(data) == 0 {
		return
	}
	x, _ := parseLength(n.attrs["x"], st.fontSize, vp, axisX)
	y, _ := parseLength(n.attrs["y"], st.fontSize, vp, axisY)
	w0, okW := parseLength(attrOr(n, "width", "auto"), st.fontSize, vp, axisX)
	h0, okH := parseLength(attrOr(n, "height", "auto"), st.fontSize, vp, axisY)
	// placed is the transform of a picture of iw x ih to the device, and
	// the rectangle it is fitted into.
	placed := func(iw, ih float64) (pm matrix, w, h float64) {
		w, h = w0, h0
		switch {
		case !okW && !okH:
			w, h = iw, ih
		case !okW:
			w = h * iw / ih
		case !okH:
			h = w * ih / iw
		}
		// The picture fitted into the rectangle as preserveAspectRatio says.
		fit := viewBoxTransform([4]float64{0, 0, iw, ih}, n.attrs["preserveAspectRatio"], w, h)
		return m.mul(matrix{1, 0, 0, 1, x, y}).mul(fit), w, h
	}
	var iw, ih float64 // the picture's own size
	o := DecodeOptions{MaxPixels: maxImagePixels - b.imagePixels, MaxInputPixels: maxInputPixels - b.inputPixels}
	o.Fit = func(pw, ph int) (int, int) {
		iw, ih = float64(pw), float64(ph)
		pm, w, h := placed(iw, ih)
		if !(w > 0) || !(h > 0) {
			return pw, ph
		}
		// Shrunk to less than half along an axis of the picture, or at all
		// when the whole of it would not fit in half the memory: decoded at
		// the size it is drawn.
		big := o.Memory > 0 && 4*int64(pw)*int64(ph) > o.Memory/2
		tw, th := pw, ph
		if sx := math.Sqrt(float64(pm.a*pm.a) + float64(pm.b*pm.b)); sx < 0.5 || big && sx < 1 {
			tw = max(1, int(math.Ceil(float64(iw*sx))))
		}
		if sy := math.Sqrt(float64(pm.c*pm.c) + float64(pm.d*pm.d)); sy < 0.5 || big && sy < 1 {
			th = max(1, int(math.Ceil(float64(ih*sy))))
		}
		return tw, th
	}
	if b.avail != nil {
		pending := int64(b.sc.points)*pointBytes + int64(b.sc.itemCount)*itemBytes - b.sc.charged
		if o.Memory = b.avail() - pending; o.Memory < 1 {
			b.err = fmt.Errorf("%w: the pictures of an SVG document", pipeline.ErrMemoryLimit)
			return
		}
	}
	var pic *image.NRGBA
	var err error
	if doc, ok := svgPicture(data); ok {
		// An SVG document: drawn at the size it takes on the device.
		pic, iw, ih, err = b.svgImage(doc, o, placed)
	} else if DecodeImage != nil {
		pic, err = DecodeImage(data, o)
	}
	if errors.Is(err, pipeline.ErrMemoryLimit) {
		b.err = err // not skipped as a broken picture is: the document is not drawn
		return
	}
	if err != nil || pic == nil {
		return
	}
	px := int64(pic.Rect.Dx()) * int64(pic.Rect.Dy())
	if iw == 0 {
		iw, ih = float64(pic.Rect.Dx()), float64(pic.Rect.Dy())
	}
	b.inputPixels += int64(iw * ih)
	b.imagePixels += px
	if err := b.reserve(4 * px); err != nil {
		b.err = err
		return
	}
	b.sc.imageBytes += 4 * px
	pm, w, h := placed(iw, ih)
	if !(w > 0) || !(h > 0) {
		return
	}
	// From the decoded picture's pixels, which may be fewer.
	inv, ok := pm.mul(matrix{iw / float64(pic.Rect.Dx()), 0, 0, ih / float64(pic.Rect.Dy()), 0, 0}).invert()
	if !ok {
		return
	}
	// The pixels it covers: the picture's rectangle, within the element's.
	area := fillPath(flatten(rectSegs(0, 0, iw, ih, 0, 0), pm), pm, false)
	var cut *clip
	if strings.Contains(n.attrs["preserveAspectRatio"], "slice") {
		box := fillPath(flatten(rectSegs(x, y, w, h, 0, 0), m), m, false)
		cut = &clip{paths: []*raster.Path{box}, bounds: pathBounds(box, b.full)}
	}
	sh := &shader{img: &sampler{img: pic, inv: inv}}
	if st.opacity < 1 {
		sh.opacity = st.opacity
	}
	it := item{op: opShape, path: area, paint: sh, bounds: pathBounds(area, b.full)}
	c := chainClip(cut, chainClip(own, outer))
	it.clip = c
	if c != nil {
		it.bounds = it.bounds.Intersect(c.bounds)
	}
	b.add(it)
}

// maxNesting bounds SVG pictures within SVG pictures.
const maxNesting = 4

// svgPicture is the document of a picture that is SVG (or SVG compressed
// by gzip, as .svgz files are).
func svgPicture(data []byte) ([]byte, bool) {
	if len(data) > 2 && data[0] == 0x1f && data[1] == 0x8b {
		zr, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, false
		}
		head := make([]byte, 512)
		n, _ := io.ReadFull(zr, head)
		if !Match(head[:n]) {
			return nil, false
		}
		rest, err := io.ReadAll(io.LimitReader(zr, MaxBytes+1-int64(n)))
		if err != nil || int64(n+len(rest)) > MaxBytes {
			return nil, false
		}
		return append(head[:n:n], rest...), true
	}
	return data, Match(data)
}

// svgImage draws an SVG picture at the size placed gives it on the device
// (within the pixels left to the pictures), with its own size.
func (b *builder) svgImage(data []byte, o DecodeOptions, placed func(iw, ih float64) (matrix, float64, float64)) (*image.NRGBA, float64, float64, error) {
	if b.opts.nesting >= maxNesting {
		return nil, 0, 0, errors.New("goimgtools: svg: pictures nested too deeply")
	}
	s, err := Open(data, pipeline.NewEnv(o.Memory, ""))
	if err != nil {
		return nil, 0, 0, err
	}
	defer s.Close()
	s.opts.nesting = b.opts.nesting + 1
	iw, ih := s.iw, s.ih
	pm, w, h := placed(iw, ih)
	if !(w > 0) || !(h > 0) {
		return nil, iw, ih, nil
	}
	sx := math.Sqrt(float64(pm.a*pm.a) + float64(pm.b*pm.b))
	sy := math.Sqrt(float64(pm.c*pm.c) + float64(pm.d*pm.d))
	tw := math.Min(maxDimension, math.Max(1, math.Ceil(float64(iw*sx))))
	th := math.Min(maxDimension, math.Max(1, math.Ceil(float64(ih*sy))))
	if lim := float64(o.MaxPixels); float64(tw*th) > lim {
		if lim < 1 {
			return nil, iw, ih, nil
		}
		k := math.Sqrt(lim / float64(tw*th))
		tw, th = math.Max(1, math.Floor(float64(tw*k))), math.Max(1, math.Floor(float64(th*k)))
	}
	pw, ph := int(tw), int(th)
	s.ScaleOnLoad(pw, ph)
	// The pictures it holds are counted with those of the documents it is in.
	s.opts.usedPixels = b.imagePixels + int64(pw)*int64(ph)
	// The picture is counted in the document's budget while it is drawn.
	px := 4 * int64(pw) * int64(ph)
	if err := s.env.Budget.Reserve(px, "svg picture"); err != nil {
		return nil, iw, ih, err
	}
	defer s.env.Budget.Release(px)
	pic := image.NewNRGBA(image.Rect(0, 0, pw, ph))
	for {
		band, err := s.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, iw, ih, err
		}
		src := band.(*image.RGBA)
		r := src.Rect
		for y := r.Min.Y; y < r.Max.Y; y++ {
			sp := src.Pix[(y-r.Min.Y)*src.Stride:][:4*pw]
			dp := pic.Pix[y*pic.Stride:][:4*pw]
			for i := 0; i < len(sp); i += 4 {
				a := uint32(sp[i+3])
				if a == 0 {
					continue
				}
				dp[i+3] = uint8(a)
				for q := 0; q < 3; q++ {
					dp[i+q] = uint8(min(255, (uint32(sp[i+q])*255+a/2)/a))
				}
			}
		}
	}
	b.imagePixels = s.r.sc.usedPixels - int64(pw)*int64(ph) // its own canvas is counted as a picture's
	return pic, iw, ih, nil
}
