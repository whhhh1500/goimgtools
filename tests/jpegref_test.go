package tests

import (
	"bytes"
	"image"
	"image/jpeg"
	"io"

	"github.com/whhhh1500/goimgtools/internal/iccprof"
	"github.com/whhhh1500/goimgtools/internal/jpegstream"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// decodeJPEG decodes a whole JPEG with this package's decoder, whose pixels
// are libjpeg-turbo's (Pillow's and sharp's; jpegstream's TestPillowCorpus
// holds them to that), where the standard library's jpeg.Decode, whose
// verdict (an error or none) it keeps, gives others: the reference that
// what goes through the pipeline is held to. A CMYK one is converted
// through its profile, or libvips's CMYK profile (iccprof's tests hold the
// conversions to LittleCMS's).
func decodeJPEG(r io.Reader) (image.Image, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	if _, err := jpeg.Decode(bytes.NewReader(data)); err != nil {
		return nil, err
	}
	src, err := jpegstream.Open(bytes.NewReader(data), nil)
	if err != nil {
		return nil, err
	}
	defer src.Close()
	m, err := pipeline.Collect(src)
	c, ok := m.(*image.CMYK)
	if err != nil || !ok {
		return m, err
	}
	p, perr := iccprof.Parse(pipeline.MetaOf(src).ICC)
	if perr != nil || p.Channels() != 4 {
		p = iccprof.DefaultCMYK()
	}
	t := p.ToSRGB()
	out := image.NewNRGBA(c.Rect)
	for i, j := 0, 0; i < len(c.Pix); i, j = i+4, j+4 {
		out.Pix[j], out.Pix[j+1], out.Pix[j+2] = t.ConvertCMYK(c.Pix[i], c.Pix[i+1], c.Pix[i+2], c.Pix[i+3])
		out.Pix[j+3] = 255
	}
	return out, nil
}
