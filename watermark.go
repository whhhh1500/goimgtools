package goimgtools

import (
	"errors"
	"fmt"

	"github.com/whhhh1500/goimgtools/internal/ops"
	"github.com/whhhh1500/goimgtools/internal/watermark"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// WatermarkKind is what an invisible watermark is made to survive (see
// InvisibleWatermark).
type WatermarkKind int

const (
	// WatermarkBlind is a mark that only says who or where a picture
	// comes from, read without the original: it survives what is done to
	// a file (saving again as JPEG or WebP, resizing, cropping, turning,
	// brightness and contrast, screenshots) and cannot be seen.
	WatermarkBlind WatermarkKind = iota
	// WatermarkScreen is a mark against leaks: it also survives a
	// photograph of a screen showing the picture (perspective, the lens'
	// blur, moire, the camera's own processing), so that a picture
	// shown to someone and photographed or screenshotted can be traced to
	// them. Its chips are larger and it is stronger, at the cost of a
	// faint texture that can be seen up close in flat areas.
	WatermarkScreen
)

// MaxWatermarkBytes is the most bytes an invisible watermark carries.
const MaxWatermarkBytes = watermark.MaxPayload

// InvisibleWatermarkOptions are the optional settings of
// InvisibleWatermark.
type InvisibleWatermarkOptions struct {
	// Kind is WatermarkBlind (the default) or WatermarkScreen.
	Kind WatermarkKind
	// Key is the secret the mark is made with: only the same key finds
	// it and reads it, and a mark cannot be forged without it. The
	// empty key is a key like any other.
	Key string
	// Strength scales the mark's amplitude: 1 (the default; 0 means 1)
	// is the kind's balance between being seen and surviving; 0.5 is
	// harder to see and to find; 2 survives more and is easier to see;
	// at most 16.
	Strength float64
	// Density is about how many times the mark repeats along the shorter
	// side of the picture, 1 to 16; 0 means 4. Denser, a smaller piece of
	// the picture still carries a whole mark (a crop must keep about two
	// repeats each way), and the mark's details are finer, so that a
	// picture reduced much more is harder to read (each detail is at least
	// 2 pixels, 3 for WatermarkScreen).
	Density float64
}

// InvisibleWatermark hides mark (1 to MaxWatermarkBytes bytes: a user ID,
// a serial number) in the picture, invisibly, to be read back with
// inspect.ReadWatermark and the same key, without the original. Fewer
// bytes are read back from more damaged copies.
//
// The mark is a tile repeated over the picture's brightness: chips of a
// few pixels carrying the bytes (with a convolutional code and a CRC of
// the key and the bytes), a reference of sinusoids, which tell the size,
// angle and shear of the tiling in a copy, and a sync pattern, which tells
// where the tiles start; all drawn from the key. It is added where the
// picture hides it best: more in busy areas, less in flat ones, and pushed
// back from pure white and black, which it could not otherwise go past.
// The rows are marked as they come, from a window of a few rows around
// each, as 8-bit colour; transparency is kept.
func (m *Image) InvisibleWatermark(mark []byte, opts ...InvisibleWatermarkOptions) *Image {
	m.atMostOne("InvisibleWatermark", len(opts))
	o := first(opts)
	switch {
	case len(mark) < 1 || len(mark) > MaxWatermarkBytes:
		return m.fail(fmt.Errorf("goimgtools: InvisibleWatermark takes 1 to %d bytes, got %d", MaxWatermarkBytes, len(mark)))
	case o.Kind != WatermarkBlind && o.Kind != WatermarkScreen:
		return m.fail(fmt.Errorf("goimgtools: unknown WatermarkKind %d", o.Kind))
	case o.Strength != 0 && !(o.Strength > 0 && o.Strength <= 16):
		return m.fail(errors.New("goimgtools: InvisibleWatermark Strength must be above 0 and at most 16"))
	case o.Density != 0 && !(o.Density >= 1 && o.Density <= watermark.MaxDensity):
		return m.fail(errors.New("goimgtools: InvisibleWatermark Density must be 1 to 16"))
	}
	return m.add(invisibleWatermarkOp{mark: append([]byte(nil), mark...), o: o})
}

type invisibleWatermarkOp struct {
	mark []byte
	o    InvisibleWatermarkOptions
}

func (o invisibleWatermarkOp) apply(src pipeline.Source, r *run) (pipeline.Source, error) {
	if r.sizeOnly {
		return src, nil
	}
	return ops.Watermark(src, o.mark, watermark.NewKey(o.o.Key), watermark.Options{
		Kind: watermark.Kind(o.o.Kind), Strength: o.o.Strength, Density: o.o.Density})
}
