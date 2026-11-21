package tests

import (
	"errors"
	"image/color"
	"testing"

	. "github.com/whhhh1500/goimgtools"
)

// TestTIFFJPEGOutput: JPEG compression makes a photograph's TIFF far
// smaller than deflate, reads back near it, takes the quality and
// MaxFileSize; transparency is an error.
func TestTIFFJPEGOutput(t *testing.T) {
	photo := noisyPhoto(640, 480, false)
	in := pngBytes(t, photo)
	deflate := must(FromBytes(in).TIFF(TIFFOptions{Compression: TIFFDeflate, Predictor: true}).ToBytes())
	jpeg := must(FromBytes(in).TIFF(TIFFOptions{Compression: TIFFJPEG}).ToBytes())
	if len(jpeg)*3 > len(deflate) {
		t.Errorf("JPEG %d bytes, deflate %d", len(jpeg), len(deflate))
	}
	if info, err := FromBytes(jpeg).Metadata(); err != nil || info.Format != "tiff" || info.Width != 640 || info.Color != "rgb" {
		t.Errorf("Metadata %+v, %v", info, err)
	}
	// As near as a JPEG file of the same quality.
	ref := must(FromBytes(in).JPEG(JPEGOptions{Quality: 80}).ToBytes())
	if d, r := meanDiff(decodeNRGBA(t, jpeg), photo), meanDiff(decodeNRGBA(t, ref), photo); d > r+0.02 {
		t.Errorf("mean difference %.3f, a JPEG's %.3f", d, r)
	}
	q30 := must(FromBytes(in).TIFF(TIFFOptions{Compression: TIFFJPEG, Quality: 30}).ToBytes())
	if len(q30) >= len(jpeg) {
		t.Errorf("quality 30: %d bytes, 80: %d", len(q30), len(jpeg))
	}
	limit := int64(len(jpeg)) * 2 / 3
	if b, err := FromBytes(in).TIFF(TIFFOptions{Compression: TIFFJPEG}).MaxFileSize(limit).ToBytes(); err != nil || int64(len(b)) > limit {
		t.Errorf("MaxFileSize(%d): %d bytes, %v", limit, len(b), err)
	}
	cut := pngBytes(t, noisyPhoto(64, 48, true))
	if _, err := FromBytes(cut).TIFF(TIFFOptions{Compression: TIFFJPEG}).ToBytes(); err == nil {
		t.Error("transparency: no error")
	} else if errors.Is(err, ErrFileSize) {
		t.Errorf("transparency: %v", err)
	}
	if _, err := FromBytes(cut).Flatten(color.White).TIFF(TIFFOptions{Compression: TIFFJPEG}).ToBytes(); err != nil {
		t.Errorf("flattened: %v", err)
	}
}
