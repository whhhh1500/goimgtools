package libavif

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"testing"
)

// picture is RGBA of a gradient with a little texture, the same on every
// run; alpha varies unless opaque.
func picture(w, h int, opaque bool) []byte {
	p := make([]byte, 4*w*h)
	seed := uint32(1)
	for i := 0; i < len(p); i += 4 {
		seed = seed*1664525 + 1013904223
		x, y := (i/4)%w, (i/4)/w
		p[i], p[i+1], p[i+2], p[i+3] = byte(x*5), byte(y*3)+byte(seed>>29), byte((x+y)*2), 255
		if !opaque {
			p[i+3] = byte(40 + (x*7+y)%200)
		}
	}
	return p
}

// TestSameOutput: the file of a picture is the same on every processor (the
// sum is the one amd64 gives), and decodes close to the picture.
func TestSameOutput(t *testing.T) {
	src := picture(64, 48, true)
	out, err := Encode(src, 64, 48, Options{Quality: 60, QualityAlpha: 60, Speed: 6, Opaque: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(out))[:16]; got != "95c00a91a206c3a0" {
		t.Errorf("file %s, %d bytes", got, len(out))
	}
	im, err := Decode(out, false)
	if err != nil {
		t.Fatal(err)
	}
	if im.Width != 64 || im.Height != 48 || im.Depth != 8 || im.Alpha || im.Primaries != 1 || im.Transfer != 13 {
		t.Errorf("%+v", im)
	}
	var se float64
	for i := range src {
		d := float64(src[i]) - float64(im.Pix[i])
		se += d * d
	}
	if mse := se / float64(len(src)); mse > 30 {
		t.Errorf("mean squared error %.1f", mse)
	}
}

// TestLossless: with transparency and metadata, the pixels and the blocks
// come back as they went in.
func TestLossless(t *testing.T) {
	src := picture(37, 29, false)
	exif := []byte("II*\x00\x08\x00\x00\x00\x00\x00")
	icc, xmp := bytes.Repeat([]byte("profile "), 20), []byte("<x:xmpmeta/>")
	out, err := Encode(src, 37, 29, Options{Speed: 6, Lossless: true, ICC: icc, Exif: exif, XMP: xmp})
	if err != nil {
		t.Fatal(err)
	}
	im, err := Decode(out, false)
	if err != nil {
		t.Fatal(err)
	}
	if !im.Alpha || !bytes.Equal(im.Pix, src) || im.Stride != 4*37 {
		t.Error("the pixels changed")
	}
	if !bytes.Equal(im.ICC, icc) || !bytes.Equal(im.Exif, exif) || !bytes.Equal(im.XMP, xmp) {
		t.Errorf("metadata: %q %q %q", im.ICC, im.Exif, im.XMP)
	}
}

// TestErrors: a file that is not AVIF, and an option aom does not know.
func TestErrors(t *testing.T) {
	if _, err := Decode([]byte("\x00\x00\x00\x18ftypavif\x00\x00\x00\x00broken"), false); err == nil {
		t.Error("a broken file decoded")
	}
	if _, err := Decode(nil, true); err == nil {
		t.Error("nothing decoded")
	}
	src := picture(16, 16, true)
	if _, err := Encode(src, 16, 16, Options{Quality: 60, Speed: 6, CodecOptions: "no-such-option=1\n"}); !errors.Is(err, ErrOption) {
		t.Errorf("unknown option: %v", err)
	}
	if _, err := Encode(src[:10], 16, 16, Options{}); err == nil {
		t.Error("too few pixels accepted")
	}
}
