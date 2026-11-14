package inspect

import (
	"errors"
	"fmt"
	"image/color"
	"math/bits"
	"strconv"

	"github.com/whhhh1500/goimgtools"
)

// Fingerprint is a picture boiled down to 64 bits in such a way that
// pictures that look alike get fingerprints that are alike: the same photo
// at another size, in another format, compressed harder or with a small
// watermark differs in a few bits, a different photo in about half of
// them. It is for finding duplicates and near-duplicates; it is not a
// checksum (two different files of the same picture share it) and not a
// secret.
type Fingerprint uint64

// Distance is the number of bits in which two fingerprints differ, 0 to
// 64. As a rule of thumb: up to about 5 it is the same picture, up to
// about 10 probably the same picture changed a little, above 20 another
// picture.
func (f Fingerprint) Distance(other Fingerprint) int {
	return bits.OnesCount64(uint64(f ^ other))
}

// String writes the fingerprint as sixteen hexadecimal digits.
func (f Fingerprint) String() string { return fmt.Sprintf("%016x", uint64(f)) }

// ParseFingerprint reads what String wrote.
func ParseFingerprint(s string) (Fingerprint, error) {
	if len(s) != 16 {
		return 0, errors.New("goimgtools/inspect: a fingerprint is sixteen hexadecimal digits")
	}
	v, err := strconv.ParseUint(s, 16, 64)
	if err != nil {
		return 0, errors.New("goimgtools/inspect: a fingerprint is sixteen hexadecimal digits")
	}
	return Fingerprint(v), nil
}

// Hash computes the fingerprint of an image.
//
// The method is the "difference hash": the picture, turned upright by its
// EXIF orientation and laid on white where it is transparent, is reduced
// to nine by eight grey pixels, and each bit says whether a pixel is
// brighter than its right-hand neighbour. What it looks at is the coarse
// layout of light and dark, which survives resizing and recompression. It
// does not survive cropping, rotating or mirroring; and pictures without
// such a layout (a plain colour, a smooth gradient from one side to the
// other) all look alike to it.
//
// A large JPEG is reduced while it is decoded: a 24 megapixel photo takes
// about a fifth of a second and a few megabytes. The operations of img, if
// it has any, are run first.
func Hash(img *goimgtools.Image) (Fingerprint, error) {
	if img == nil {
		return 0, errors.New("goimgtools/inspect: Hash needs an image")
	}
	small, err := img.Clone().
		Rotate().
		Resize(9, 8, goimgtools.ResizeOptions{Fit: goimgtools.FitFill, Kernel: goimgtools.KernelLinear, FastShrinkOnLoad: true}).
		Flatten(color.White).
		ToImage()
	if err != nil {
		return 0, err
	}
	b := small.Bounds()
	if b.Dx() != 9 || b.Dy() != 8 {
		return 0, fmt.Errorf("goimgtools/inspect: the image came out as %dx%d", b.Dx(), b.Dy())
	}
	var grey [8][9]int
	for y := 0; y < 8; y++ {
		for x := 0; x < 9; x++ {
			r, g, bl, _ := small.At(b.Min.X+x, b.Min.Y+y).RGBA()
			grey[y][x] = int(299*(r>>8) + 587*(g>>8) + 114*(bl>>8))
		}
	}
	var f Fingerprint
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			f <<= 1
			if grey[y][x] > grey[y][x+1] {
				f |= 1
			}
		}
	}
	return f, nil
}
