package svg

import (
	"encoding/base64"
	"image"
	"math"
	"net/url"
	"strings"

	"github.com/whhhh1500/goimgtools/internal/raster"
)

// DecodeImage decodes the picture of an image element, a data URI's
// bytes, as non-premultiplied RGBA; reserve takes the memory it is held in.
// The package that reads files sets it; without it images are not drawn.
var DecodeImage func(data []byte, maxPixels int64) (*image.NRGBA, error)

// maxImagePixels bounds the pictures an SVG holds, all together.
const maxImagePixels = 64 << 20

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
	if DecodeImage == nil || !st.visible || st.opacity <= 0 {
		return
	}
	href, ok := n.attr("href")
	if !ok {
		href, ok = n.attr("xlink:href")
	}
	data, ok := dataURI(href)
	if !ok || len(data) == 0 {
		return
	}
	pic, err := DecodeImage(data, maxImagePixels-b.imagePixels)
	if err != nil || pic == nil {
		return
	}
	iw, ih := float64(pic.Rect.Dx()), float64(pic.Rect.Dy())
	b.imagePixels += int64(iw * ih)
	if err := b.reserve(4 * int64(iw*ih)); err != nil {
		b.err = err
		return
	}
	b.sc.imageBytes += 4 * int64(iw*ih)
	x, _ := parseLength(n.attrs["x"], st.fontSize, vp, axisX)
	y, _ := parseLength(n.attrs["y"], st.fontSize, vp, axisY)
	w, okW := parseLength(attrOr(n, "width", "auto"), st.fontSize, vp, axisX)
	h, okH := parseLength(attrOr(n, "height", "auto"), st.fontSize, vp, axisY)
	switch {
	case !okW && !okH:
		w, h = iw, ih
	case !okW:
		w = h * iw / ih
	case !okH:
		h = w * ih / iw
	}
	if !(w > 0) || !(h > 0) {
		return
	}
	// The picture fitted into the rectangle as preserveAspectRatio says.
	fit := viewBoxTransform([4]float64{0, 0, iw, ih}, n.attrs["preserveAspectRatio"], w, h)
	pm := m.mul(matrix{1, 0, 0, 1, x, y}).mul(fit)
	inv, ok := pm.invert()
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
