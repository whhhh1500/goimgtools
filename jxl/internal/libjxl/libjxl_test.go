package libjxl

import (
	"bytes"
	"crypto/sha256"
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
// sum is the one amd64 gives), and so are its decoded pixels; they come back
// close to the picture.
func TestSameOutput(t *testing.T) {
	src := picture(64, 48, true)
	out, err := Encode(src, 64, 48, Options{Distance: 1, Effort: 7, Opaque: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(out))[:16]; got != "db9cafdc32e33c9a" {
		t.Errorf("file %s, %d bytes", got, len(out))
	}
	im, err := Decode(out, false)
	if err != nil {
		t.Fatal(err)
	}
	if im.Width != 64 || im.Height != 48 || im.Depth != 8 || im.Alpha || im.Gray || im.ICC != nil {
		t.Errorf("%+v", im)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(im.Pix))[:16]; got != "754b2a41e31a1fee" {
		t.Errorf("pixels %s", got)
	}
	var se float64
	for i := range src {
		d := float64(src[i]) - float64(im.Pix[i])
		se += d * d
	}
	if mse := se / float64(len(src)); mse > 10 {
		t.Errorf("mean squared error %.1f", mse)
	}
}

// TestLossless: with transparency and metadata, the pixels and the blocks
// come back as they went in; 8 and 16 bits.
func TestLossless(t *testing.T) {
	src := picture(37, 29, false)
	exif := []byte("II*\x00\x08\x00\x00\x00\x00\x00")
	xmp := []byte("<x:xmpmeta/>")
	out, err := Encode(src, 37, 29, Options{Effort: 7, Lossless: true, Exif: exif, XMP: xmp})
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
	// The EXIF box holds the offset of the TIFF header before it.
	if !bytes.Equal(im.Exif, append([]byte{0, 0, 0, 0}, exif...)) || !bytes.Equal(im.XMP, xmp) {
		t.Errorf("metadata: %q %q", im.Exif, im.XMP)
	}
	deep := make([]byte, 2*len(src))
	for i, v := range src {
		deep[2*i], deep[2*i+1] = v, byte(i)
	}
	out, err = Encode(deep, 37, 29, Options{Effort: 3, Lossless: true, Depth16: true})
	if err != nil {
		t.Fatal(err)
	}
	im, err = Decode(out, false)
	if err != nil {
		t.Fatal(err)
	}
	if im.Depth != 16 || !bytes.Equal(im.Pix, deep) || im.Stride != 8*37 {
		t.Errorf("16 bits: depth %d, the pixels changed: %v", im.Depth, !bytes.Equal(im.Pix, deep))
	}
}

// TestErrors: files that are not JPEG XL, or are cut short; settings
// libjxl refuses.
func TestErrors(t *testing.T) {
	if _, err := Decode([]byte("\xff\x0abroken"), false); err == nil {
		t.Error("a broken file decoded")
	}
	if _, err := Decode(nil, true); err == nil {
		t.Error("nothing decoded")
	}
	src := picture(16, 16, true)
	out, err := Encode(src, 16, 16, Options{Distance: 1, Effort: 7, Opaque: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(out[:len(out)/2], false); err == nil {
		t.Error("half a file decoded")
	}
	if _, err := Encode(src, 16, 16, Options{Distance: 30, Effort: 7}); err == nil {
		t.Error("distance 30 accepted")
	}
	if _, err := Encode(src[:10], 16, 16, Options{}); err == nil {
		t.Error("too few pixels accepted")
	}
}
