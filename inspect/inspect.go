// Package inspect looks at images without changing them: whether a file is
// intact, and how much two images differ.
//
// It is built on goimgtools the way any program is, through its public
// interface, so everything here works for every input the library reads,
// including formats added with RegisterFormat, and within the same memory
// limit: the images are read a few rows at a time, never held whole.
//
// What a file is (its format, size, colours, metadata, frames) is told by
// the library itself: goimgtools.Image.Metadata and Image.Frames.
package inspect

import (
	"errors"
	"fmt"
	"image"
	"io"
	"math"

	"github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// Verify reads the image to its end and reports the first thing wrong with
// it: nil means every pixel decoded and the file's own checks (checksums,
// the end of the data) passed. Metadata only looks at the header, so a file
// that was cut short or damaged further in passes it; Verify finds that.
//
// A file that is not a readable image gives an error that wraps
// goimgtools.ErrFormat, like every other use of it. For an animated GIF
// the pixels of the first frame are decoded and the structure of the other
// frames is walked through.
//
// The operations of img, if it has any, are run too: Verify(img) says
// whether writing img would succeed, short of the writing itself.
func Verify(img *goimgtools.Image) error {
	if img == nil {
		return errors.New("goimgtools/inspect: Verify needs an image")
	}
	src, err := img.Clone().Stream(nil)
	if err != nil {
		return err
	}
	for {
		if _, err = src.Next(); err != nil {
			break
		}
	}
	cerr := src.Close()
	if err != io.EOF {
		return err
	}
	if cerr != nil {
		return cerr
	}
	// The frames after the first, when the input is something that has
	// them. (An input that can be read only once has been used up by now.)
	if info, err := img.Metadata(); err == nil && info.Format == "gif" {
		if _, err := img.Frames(); err != nil {
			return err
		}
	}
	return nil
}

// Difference is what Compare found.
type Difference struct {
	// SameSize reports whether the two images have the same width and
	// height. When they do not, nothing else is compared and the other
	// fields are zero.
	SameSize bool
	// Width and Height are the size of the first image.
	Width, Height int
	// Identical reports whether every pixel is the same.
	Identical bool
	// DifferentPixels is the number of pixels that differ in any channel.
	DifferentPixels int64
	// MaxDiff is the largest difference found in one channel of one
	// pixel, on the scale of 0 to 255.
	MaxDiff int
	// PSNR is the peak signal-to-noise ratio in decibels, the usual
	// measure of how close a compressed picture is to its original:
	// higher is closer, +Inf for identical images. As a rule of thumb,
	// above 40 the difference is hard to see, below 30 it is plain.
	PSNR float64
}

// String puts the difference in a line of words.
func (d Difference) String() string {
	switch {
	case !d.SameSize:
		return "different sizes"
	case d.Identical:
		return "identical"
	}
	total := float64(d.Width) * float64(d.Height)
	return fmt.Sprintf("PSNR %.2f dB, largest difference %d of 255, %.1f%% of the pixels differ",
		d.PSNR, d.MaxDiff, 100*float64(d.DifferentPixels)/total)
}

// Compare measures how much b differs from a, pixel by pixel: the way to
// see what a compression setting costs, or to check that a conversion kept
// every pixel.
//
// Pixels are compared as 8-bit red, green, blue and alpha, whatever the
// two files store, so a JPEG can be held against the PNG it was made from.
// Two pixels that are both fully transparent count as equal whatever their
// colour, which cannot be seen. The noise is averaged over red, green and
// blue, and over alpha too when either image has transparency.
//
// Both images are read a few rows at a time and share one memory limit,
// that of a. Their operations, if they have any, are run first, so two
// recipes can be compared as well as two files.
func Compare(a, b *goimgtools.Image) (d Difference, err error) {
	if a == nil || b == nil {
		return Difference{}, errors.New("goimgtools/inspect: Compare needs two images")
	}
	sa, err := a.Clone().Stream(nil)
	if err != nil {
		return Difference{}, err
	}
	defer func() { sa.Close() }()
	sb, err := b.Clone().Stream(pipeline.EnvOf(sa))
	if err != nil {
		return Difference{}, fmt.Errorf("second image: %w", err)
	}
	defer func() { sb.Close() }()

	ba, bb := sa.Bounds(), sb.Bounds()
	d.Width, d.Height = ba.Dx(), ba.Dy()
	if ba.Size() != bb.Size() {
		return d, nil
	}
	d.SameSize = true
	alpha := !pipeline.IsOpaque(sa) || !pipeline.IsOpaque(sb)

	// Both as 8-bit NRGBA, and row by row whatever the strips are.
	keep := func(*image.NRGBA) error { return nil }
	sa, sb = pipeline.Map(sa, keep), pipeline.Map(sb, keep)
	ra, rb := pipeline.NewRowReader(sa), pipeline.NewRowReader(sb)
	var sum float64 // of squared differences
	n := d.Width * 4
	for y := 0; y < d.Height; y++ {
		pa, err := ra.Row(ba.Min.Y + y)
		if err != nil {
			return Difference{}, err
		}
		pb, err := rb.Row(bb.Min.Y + y)
		if err != nil {
			return Difference{}, fmt.Errorf("second image: %w", err)
		}
		ia, ib := pa.(*image.NRGBA), pb.(*image.NRGBA)
		rowA := ia.Pix[ia.PixOffset(ba.Min.X, ba.Min.Y+y):][:n]
		rowB := ib.Pix[ib.PixOffset(bb.Min.X, bb.Min.Y+y):][:n]
		var rowSum uint64
		for x := 0; x < n; x += 4 {
			p, q := rowA[x:x+4:x+4], rowB[x:x+4:x+4]
			if p[0] == q[0] && p[1] == q[1] && p[2] == q[2] && p[3] == q[3] {
				continue
			}
			if p[3] == 0 && q[3] == 0 {
				continue // both invisible
			}
			d.DifferentPixels++
			for c := 0; c < 4; c++ {
				diff := int(p[c]) - int(q[c])
				if diff < 0 {
					diff = -diff
				}
				d.MaxDiff = max(d.MaxDiff, diff)
				if c < 3 || alpha {
					rowSum += uint64(diff * diff)
				}
			}
		}
		sum += float64(rowSum)
	}
	// Let both decoders finish checking their files.
	if err := ra.Drain(); err != nil {
		return Difference{}, err
	}
	if err := rb.Drain(); err != nil {
		return Difference{}, fmt.Errorf("second image: %w", err)
	}
	d.Identical = d.DifferentPixels == 0
	channels := 3.0
	if alpha {
		channels = 4
	}
	d.PSNR = math.Inf(1)
	if sum > 0 {
		mse := sum / (float64(d.Width) * float64(d.Height) * channels)
		d.PSNR = 10 * math.Log10(255*255/mse)
	}
	return d, nil
}
