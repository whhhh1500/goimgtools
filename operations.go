package goimgtools

import (
	"errors"
	"fmt"
	"image/color"
	"math"

	"golang.org/x/image/draw"

	"github.com/whhhh1500/goimgtools/internal/ops"
	"github.com/whhhh1500/goimgtools/internal/resize"
)

// The operations of a chain, as the caller asks for them: their options
// and the methods that add them. How they are carried out is in chain.go.

// Fit says how Resize fits the image into the requested width × height.
type Fit int

const (
	// FitInside keeps the proportions and makes the image as large as
	// possible while fitting inside the box. This is the default: nothing
	// is cut off and nothing is distorted.
	FitInside Fit = iota
	// FitCover keeps the proportions, fills the whole box and crops what
	// sticks out, centred (the default of sharp).
	FitCover
	// FitOutside keeps the proportions and makes the image as small as
	// possible while covering the box.
	FitOutside
	// FitFill stretches to exactly width × height, ignoring proportions.
	FitFill
	// FitContain keeps the proportions, fits the image inside the box like
	// FitInside, and fills the rest of the box with ResizeOptions.Background
	// so the result is exactly width × height.
	FitContain
)

// Kernel is the filter Resize interpolates with.
type Kernel int

const (
	// KernelCubic is Catmull-Rom, the default: sharp, with little ringing.
	KernelCubic Kernel = iota
	// KernelNearest takes the nearest pixel when enlarging and a plain
	// average when shrinking. Fastest, blocky.
	KernelNearest
	// KernelLinear is bilinear interpolation: fast and soft.
	KernelLinear
	// KernelMitchell is the Mitchell-Netravali cubic: a little softer than
	// KernelCubic, with less ringing.
	KernelMitchell
	// KernelLanczos2 and KernelLanczos3 are windowed sinc filters;
	// KernelLanczos3 is the sharpest and the slowest.
	KernelLanczos2
	KernelLanczos3
)

// ResizeOptions are the optional settings of Resize.
type ResizeOptions struct {
	Fit Fit
	// WithoutEnlargement leaves the image at its size when the requested
	// size would make it larger.
	WithoutEnlargement bool
	// FastShrinkOnLoad lets a JPEG that is being reduced to half its size
	// or less be shrunk by 2, 4 or 8 while it is decoded (each 8×8 block
	// decoded straight to the averages of its pixels), leaving less for the
	// resize itself. It is markedly faster and uses less memory; the pixels
	// differ slightly from a resize of the full image, the size of the
	// result does not. It applies when Resize is the first operation on a
	// JPEG, and is ignored otherwise.
	FastShrinkOnLoad bool
	// Background fills the bars FitContain adds. The default is black.
	Background color.Color
	// Kernel is the interpolation filter; the default is KernelCubic.
	Kernel Kernel
	// Filter, when set, is an interpolation filter of your own, used
	// instead of Kernel.
	Filter *Filter
}

// Filter is a resampling filter for Resize that this package does not have
// (ResizeOptions.Filter): the weight a source pixel is given by its
// distance from the place a result pixel is computed for.
type Filter struct {
	// Support is the distance, in source pixels at the scale of the
	// result, beyond which the weight is zero: 1 for a linear filter, 2
	// for the cubic ones, 3 for Lanczos3. A larger support is slower.
	Support float64
	// At returns the weight at distance t, 0 <= t <= Support. The weights
	// are normalised, so only their proportions matter.
	At func(t float64) float64
}

// Position names a point of a rectangle: a corner, the middle of an edge or
// the centre. One set of names serves the three places CompositeOptions
// needs a point: where on the image (Gravity), which point of the overlay
// is put there (Anchor), and which point the overlay turns around (Pivot).
type Position int

const (
	// DefaultPosition is the zero value. What it stands for is said at
	// each field: the top-left corner for Gravity, the point named like
	// Gravity for Anchor, the anchor for Pivot.
	DefaultPosition Position = iota
	NorthWest                // top-left
	North
	NorthEast
	West
	Center
	East
	SouthWest
	South
	SouthEast // bottom-right
	// CustomPosition is the point given in pixels beside the field
	// (AnchorX and AnchorY, PivotX and PivotY). It is not for Gravity,
	// where Left and Top already move the position by pixels.
	CustomPosition
)

// grid returns the column and row (0, 1 or 2) of one of the nine named
// positions.
func (p Position) grid() (col, row int, ok bool) {
	if p < NorthWest || p > SouthEast {
		return 0, 0, false
	}
	i := int(p - NorthWest)
	return i % 3, i / 3, true
}

// CompositeOptions place the overlay. The overlay is anchored at Gravity,
// then moved Left pixels right and Top pixels down (negative values move it
// left / up, e.g. a margin from the bottom-right corner).
//
// The overlay is placed in three steps. A position on the image is chosen:
// the Gravity point (a corner, the middle of an edge or the centre), moved
// by Left and Top pixels and by LeftRatio and TopRatio of the image's width
// and height. The overlay's Anchor point is put on that position. Then the
// overlay is turned by Angle around its Pivot point.
//
// With only Gravity, Left and Top set, the overlay's own corner (edge,
// centre) of the same name is the anchor: SouthEast with Left: -24, Top: -24
// puts the overlay in the bottom right corner, 24 pixels from both edges.
type CompositeOptions struct {
	// Gravity is the point of the image the position starts from; the
	// default is the top-left corner (NorthWest).
	Gravity   Position
	Left, Top int
	// LeftRatio and TopRatio move the position by a share of the image's
	// width and height, so the same options suit images of any size:
	// LeftRatio: 1.0/3, TopRatio: 1.0/3 is the point a third of the way
	// across and down from the Gravity point.
	LeftRatio, TopRatio float64
	// Anchor is the point of the overlay that is put on the position. The
	// default is the overlay's point named like Gravity. With
	// CustomPosition it is (AnchorX, AnchorY), in pixels from the overlay's
	// top-left corner.
	Anchor           Position
	AnchorX, AnchorY float64
	// Angle turns the overlay clockwise, in degrees (any value). It turns
	// around Pivot, which stays where it is: by default the anchor, else
	// another point of the overlay, or with CustomPosition (PivotX,
	// PivotY) in pixels from the overlay's top-left corner.
	Angle          float64
	Pivot          Position
	PivotX, PivotY float64
	// Opacity makes the whole overlay more transparent: 0.3 leaves 30% of
	// it. Zero means 1, fully as it is. It multiplies whatever transparency
	// the overlay has itself.
	Opacity float64
	// Tile repeats the overlay over the whole image. One copy is where the
	// options above put it; the others follow at the overlay's width plus
	// TileGapX across and its height plus TileGapY down. With Angle, every
	// copy is turned and the copies stay in straight rows and columns
	// (spaced by the turned overlay's width and height).
	Tile               bool
	TileGapX, TileGapY int
	// WidthRatio scales the overlay, before anything else, to this share
	// of the image's width, keeping its proportions: 0.2 makes it a fifth
	// as wide as the image, whatever size the image is, which keeps a
	// watermark the same to the eye over images of different sizes.
	// HeightRatio does the same by height; with both, the overlay fits
	// inside both. Zero leaves the overlay its own size; otherwise above 0
	// and at most 10. (The overlay is resized with Resize's default kernel,
	// enlarged if need be.)
	WidthRatio, HeightRatio float64
	// Blend is how the overlay's colours mix with those under it: BlendOver
	// (the default) puts them over; the others are the blend modes of CSS,
	// PDF and sharp (see BlendMode).
	Blend BlendMode
}

// BlendMode is how an overlay's colours mix with the image's under them,
// where both are there: as CSS's mix-blend-mode and PDF define them (and
// libvips' composite, so sharp's blend). The mix is then put over the
// image as BlendOver puts the overlay, with its transparency.
type BlendMode int

// The blend modes.
const (
	BlendOver       BlendMode = iota // the overlay's colour (normal)
	BlendMultiply                    // the product: darker, white leaves it be
	BlendScreen                      // the inverse of the product of inverses: lighter
	BlendOverlay                     // multiply or screen by the image's colour: more contrast
	BlendDarken                      // the darker of the two, channel by channel
	BlendLighten                     // the lighter of the two
	BlendColorDodge                  // the image brightened to reflect the overlay
	BlendColorBurn                   // the image darkened to reflect the overlay
	BlendHardLight                   // multiply or screen by the overlay's colour
	BlendSoftLight                   // a softer hard light
	BlendDifference                  // the difference, larger less smaller
	BlendExclusion                   // like difference, of lower contrast
)

var blendNames = [...]string{"over", "multiply", "screen", "overlay", "darken", "lighten",
	"color-dodge", "color-burn", "hard-light", "soft-light", "difference", "exclusion"}

// String returns the CSS name of the mode ("multiply", "color-dodge"), or
// "BlendMode(n)" for no mode.
func (b BlendMode) String() string {
	if b >= 0 && int(b) < len(blendNames) {
		return blendNames[b]
	}
	return fmt.Sprintf("BlendMode(%d)", int(b))
}

// point resolves p to pixels from the top-left corner of a w × h overlay.
// def is what DefaultPosition stands for; x and y what CustomPosition does.
func (p Position) point(w, h, defX, defY, x, y float64) (float64, float64, bool) {
	switch p {
	case DefaultPosition:
		return defX, defY, true
	case CustomPosition:
		return x, y, true
	}
	col, row, ok := p.grid()
	return w * float64(col) / 2, h * float64(row) / 2, ok
}

// Resize scales the image to width × height. A zero width or height is
// computed from the other to keep the proportions.
func (m *Image) Resize(width, height int, opts ...ResizeOptions) *Image {
	m.atMostOne("Resize", len(opts))
	var o ResizeOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	if width < 0 || height < 0 || (width == 0 && height == 0) {
		return m.fail(errors.New("goimgtools: Resize needs a positive width or height"))
	}
	if o.Fit < FitInside || o.Fit > FitContain {
		return m.fail(errors.New("goimgtools: unknown resize Fit"))
	}
	kernel, err := resizeKernel(o)
	if err != nil {
		return m.fail(err)
	}
	return m.add(resizeOp{resizeSpec{width, height, o}, kernel})
}

func resizeKernel(o ResizeOptions) (*draw.Kernel, error) {
	if f := o.Filter; f != nil {
		if f.At == nil || !(f.Support > 0) || math.IsInf(f.Support, 0) {
			return nil, errors.New("goimgtools: a resize Filter needs an At function and a positive Support")
		}
		return &draw.Kernel{Support: f.Support, At: f.At}, nil
	}
	switch o.Kernel {
	case KernelCubic:
		return draw.CatmullRom, nil
	case KernelNearest:
		return resize.Nearest, nil
	case KernelLinear:
		return draw.BiLinear, nil
	case KernelMitchell:
		return resize.Mitchell, nil
	case KernelLanczos2:
		return resize.Lanczos2, nil
	case KernelLanczos3:
		return resize.Lanczos3, nil
	}
	return nil, errors.New("goimgtools: unknown resize kernel")
}

// Rotate turns the image clockwise by angle, a multiple of 90 degrees.
// Without an argument it turns the image upright according to the EXIF
// orientation the camera recorded (and does nothing when there is none).
//
// Anything but a plain left-right mirror needs the last rows of the image
// first, so the pixels are kept in working storage (a temporary file when
// they do not fit in the memory limit). When a Resize follows directly, the
// image is resized first and turned afterwards, with the same result.
func (m *Image) Rotate(angle ...int) *Image {
	if len(angle) > 1 {
		return m.fail(fmt.Errorf("goimgtools: Rotate takes at most one angle, got %d", len(angle)))
	}
	if len(angle) == 0 {
		m.autoOrient = true
		return m.add(turnOp{auto: true})
	}
	var o ops.Orientation
	switch ((angle[0] % 360) + 360) % 360 {
	case 0:
	case 90:
		o = ops.Rotate90
	case 180:
		o = ops.Rotate180
	case 270:
		o = ops.Rotate270
	default:
		return m.fail(errors.New("goimgtools: Rotate takes a multiple of 90 degrees"))
	}
	return m.add(turnOp{o: o})
}

// Flip mirrors the image top to bottom.
func (m *Image) Flip() *Image { return m.add(turnOp{o: ops.Flip}) }

// Flop mirrors the image left to right.
func (m *Image) Flop() *Image { return m.add(turnOp{o: ops.Flop}) }

// Extract crops to the given region (in pixels of the image at this point
// in the chain).
func (m *Image) Extract(left, top, width, height int) *Image {
	return m.add(extractOp{left, top, width, height})
}

// Flatten removes transparency by placing the image on a background colour.
func (m *Image) Flatten(background color.Color) *Image {
	if background == nil {
		return m.fail(errors.New("goimgtools: Flatten needs a background colour"))
	}
	return m.add(flattenOp{background})
}

// Composite draws overlay (for example a watermark) on top of the image.
// The overlay may have its own chain, e.g. Open("logo.png").Resize(200, 0).
// It is held in memory whole, so it should be small.
func (m *Image) Composite(overlay *Image, opts ...CompositeOptions) *Image {
	m.atMostOne("Composite", len(opts))
	if overlay == nil {
		return m.fail(errors.New("goimgtools: Composite needs an overlay image"))
	}
	if overlay == m {
		return m.fail(errors.New("goimgtools: an image cannot be composited onto itself"))
	}
	var o CompositeOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	return m.add(compositeOp{overlay, o})
}
