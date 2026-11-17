package tests

import (
	"bytes"
	"image"
	"image/jpeg"
	"io"

	"github.com/whhhh1500/goimgtools/internal/jpegstream"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// decodeJPEG decodes a whole JPEG with this package's decoder, whose pixels
// are libjpeg-turbo's (Pillow's and sharp's; jpegstream's TestPillowCorpus
// holds them to that), where the standard library's jpeg.Decode, whose
// verdict (an error or none) it keeps, gives others: the reference that
// what goes through the pipeline is held to.
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
	return pipeline.Collect(src)
}
