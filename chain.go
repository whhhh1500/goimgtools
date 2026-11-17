package goimgtools

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"math"

	"golang.org/x/image/draw"

	"github.com/whhhh1500/goimgtools/internal/ops"
	"github.com/whhhh1500/goimgtools/internal/resize"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// The operations of a recipe are kept as data, in the order they were asked
// for. A run can therefore look at the whole list before it starts, and
// the two places where it does things in another order than written are in
// one function, applyOps, instead of being arranged between the operations
// behind each other's backs.

// operation is one step of a chain.
type operation interface {
	// apply puts the operation on src and returns the source of its result.
	apply(src pipeline.Source, r *run) (pipeline.Source, error)
}

// run is what the operations of one run have in common.
type run struct {
	// exifOrient is the EXIF orientation of the input, 1 to 8.
	exifOrient int
	// sizeOnly is set when only the size of the result is wanted
	// (OutputSize): an operation that does not change it leaves the source
	// alone.
	sizeOnly bool
	// maxPixels is the largest image a step may make (LimitOutputPixels);
	// zero for no limit.
	maxPixels int64
	// hooks go around every step (see Around).
	hooks []Hook
}

// fits holds the size of what a step makes against the limit.
func (r *run) fits(w, h int) error {
	if w <= 0 || h <= 0 {
		return errors.New("goimgtools: an operation left an image without pixels")
	}
	if r.maxPixels > 0 && int64(w)*int64(h) > r.maxPixels {
		return fmt.Errorf("%w: the chain makes an image of %dx%d, more than the limit of %d pixels (see LimitOutputPixels)", ErrPixelLimit, w, h, r.maxPixels)
	}
	return nil
}

// preShrunk says that the decoder has already reduced the image for the
// resize the chain starts with (see FastShrinkOnLoad): by the factor k,
// from the size orig. The zero value says it has not.
type preShrunk struct {
	orig image.Point
	k    int
	// planes: the strips are Gray (gray) or YCbCr of ratio, which the
	// resize can scale plane by plane (resize.ScalePlanes).
	planes bool
	gray   bool
	ratio  image.YCbCrSubsampleRatio
}

// turnOp is Rotate, Flip or Flop.
type turnOp struct {
	auto bool            // follow the EXIF orientation of the input
	o    ops.Orientation // otherwise
}

func (t turnOp) orientation(r *run) ops.Orientation {
	if t.auto {
		return ops.FromExif(r.exifOrient)
	}
	return t.o
}

func (t turnOp) apply(src pipeline.Source, r *run) (pipeline.Source, error) {
	return ops.Orient(src, t.orientation(r)), nil
}

// resizeSpec is the size a Resize asks for.
type resizeSpec struct {
	width, height int
	o             ResizeOptions
}

// target returns the size a sw × sh image is resized to.
func (r resizeSpec) target(sw, sh int) (dw, dh int) {
	fsw, fsh := float64(sw), float64(sh)
	fx, fy := float64(r.width)/fsw, float64(r.height)/fsh
	switch {
	case r.width == 0:
		fx = fy
	case r.height == 0:
		fy = fx
	case r.o.Fit == FitInside, r.o.Fit == FitContain:
		fx = math.Min(fx, fy)
		fy = fx
	case r.o.Fit == FitCover, r.o.Fit == FitOutside:
		fx = math.Max(fx, fy)
		fy = fx
	}
	if r.o.WithoutEnlargement && (fx > 1 || fy > 1) {
		fx, fy = math.Min(fx, 1), math.Min(fy, 1)
		if r.o.Fit != FitFill {
			fx = math.Min(fx, fy)
			fy = fx
		}
	}
	return max(1, int(math.Round(fsw*fx))), max(1, int(math.Round(fsh*fy)))
}

// resizeOp is Resize.
type resizeOp struct {
	spec   resizeSpec
	kernel *draw.Kernel
}

func (z resizeOp) apply(src pipeline.Source, r *run) (pipeline.Source, error) {
	return z.resize(src, ops.Upright, preShrunk{}, r)
}

// resize scales src and then turns the result by turn; the size asked for
// is that of the turned image. pre tells when src is an image the decoder
// has already reduced: the size aimed for is then the one the full image
// would have given.
func (z resizeOp) resize(src pipeline.Source, turn ops.Orientation, pre preShrunk, r *run) (pipeline.Source, error) {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	ew, eh := float64(sw), float64(sh) // the part of src the result spans
	if pre.k > 1 {
		sw, sh = pre.orig.X, pre.orig.Y
		ew, eh = float64(sw)/float64(pre.k), float64(sh)/float64(pre.k)
	}
	dw, dh := z.spec.target(turn.Size(sw, sh))
	iw, ih := turn.Size(dw, dh) // the target, before the turn
	// Before anything is set up for it: the buffers of a resize grow with
	// the width it scales to.
	if err := r.fits(dw, dh); err != nil {
		return nil, err
	}
	out := src
	if iw != b.Dx() || ih != b.Dy() || pre.k > 1 {
		var err error
		if pre.k > 1 && pre.planes {
			// The pixels of FastShrinkOnLoad already differ a little:
			// the planes are scaled as they are, in fixed point.
			out, err = resize.ScalePlanes(src, iw, ih, z.kernel, ew, eh, pre.ratio, pre.gray)
		} else {
			out, err = resize.ScaleExtent(src, iw, ih, z.kernel, ew, eh)
		}
		if err != nil {
			return nil, err
		}
	}
	out = ops.Orient(out, turn)
	o, width, height := z.spec.o, z.spec.width, z.spec.height
	switch {
	case o.Fit == FitCover && width > 0 && height > 0 && (dw > width || dh > height):
		cw, ch := min(dw, width), min(dh, height)
		return ops.Crop(out, (dw-cw)/2, (dh-ch)/2, cw, ch)
	case o.Fit == FitContain && width > 0 && height > 0 && (dw < width || dh < height):
		var bg color.Color = color.Black
		if o.Background != nil {
			bg = o.Background
		}
		left, top := (width-dw)/2, (height-dh)/2
		return ops.Extend(out, left, top, width-dw-left, height-dh-top, bg)
	}
	return out, nil
}

// extractOp is Extract.
type extractOp struct{ left, top, width, height int }

func (e extractOp) apply(src pipeline.Source, _ *run) (pipeline.Source, error) {
	return ops.Crop(src, e.left, e.top, e.width, e.height)
}

// flattenOp is Flatten.
type flattenOp struct{ background color.Color }

func (f flattenOp) apply(src pipeline.Source, _ *run) (pipeline.Source, error) {
	return ops.Flatten(src, f.background), nil
}

// compositeOp is Composite.
type compositeOp struct {
	overlay *Image
	o       CompositeOptions
}

func (c compositeOp) apply(src pipeline.Source, r *run) (pipeline.Source, error) {
	// The overlay changes neither the size nor the kind of the result, so
	// asking for the size does not open (or use up) the overlay.
	if r.sizeOnly {
		return src, nil
	}
	o := c.o
	env := pipeline.EnvOf(src)
	overlay := c.overlay
	if o.WidthRatio != 0 || o.HeightRatio != 0 {
		for _, v := range []float64{o.WidthRatio, o.HeightRatio} {
			if !(v >= 0 && v <= 10) {
				return nil, errors.New("goimgtools: Composite WidthRatio and HeightRatio must be 0 to 10")
			}
		}
		b := src.Bounds()
		share := func(n int, r float64) int {
			if r == 0 {
				return 0
			}
			return max(1, int(math.Round(float64(n)*r)))
		}
		overlay = overlay.Clone().Resize(share(b.Dx(), o.WidthRatio), share(b.Dy(), o.HeightRatio))
	}
	osrc, closer, _, _, err := overlay.build(env, false)
	if err != nil {
		return nil, fmt.Errorf("overlay: %w", err)
	}
	top, err := pipeline.Collect(osrc)
	osrc.Close()
	closer.Close()
	if err != nil {
		return nil, fmt.Errorf("overlay: %w", err)
	}
	if !(o.Opacity >= 0 && o.Opacity <= 1) {
		return nil, errors.New("goimgtools: Composite opacity must be 0 to 1")
	}
	mode, ok := blendModes[o.Blend]
	if !ok {
		return nil, errors.New("goimgtools: unknown Composite blend mode")
	}
	place := func(m image.Image, x, y int) (pipeline.Source, error) {
		// Faded last: a turned overlay is resampled first, at full
		// strength, so its edges cannot come out stronger than asked.
		if o.Opacity != 0 && o.Opacity != 1 {
			m = ops.Fade(m, o.Opacity)
		}
		if o.Tile {
			// A gap beyond any image leaves the one copy; capped so that
			// adding it to a size cannot overflow.
			const far = 1 << 30
			return ops.OverlayTiled(src, m, x, y, min(o.TileGapX, far), min(o.TileGapY, far), mode)
		}
		return ops.Overlay(src, m, x, y, mode)
	}
	b, tb := src.Bounds(), top.Bounds()
	gx, gy := 0, 0 // the top-left corner
	if o.Gravity != DefaultPosition {
		var ok bool
		if gx, gy, ok = o.Gravity.grid(); !ok {
			return nil, errors.New("goimgtools: Gravity is one of the nine positions NorthWest to SouthEast")
		}
	}
	simple := o.LeftRatio == 0 && o.TopRatio == 0 && o.Anchor == DefaultPosition && o.Angle == 0
	if simple {
		x := []int{0, (b.Dx() - tb.Dx()) / 2, b.Dx() - tb.Dx()}[gx]
		y := []int{0, (b.Dy() - tb.Dy()) / 2, b.Dy() - tb.Dy()}[gy]
		return place(top, x+o.Left, y+o.Top)
	}
	for _, v := range []float64{o.LeftRatio, o.TopRatio, o.AnchorX, o.AnchorY, o.Angle, o.PivotX, o.PivotY} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, errors.New("goimgtools: Composite options must be finite numbers")
		}
	}
	W, H, w, h := float64(b.Dx()), float64(b.Dy()), float64(tb.Dx()), float64(tb.Dy())
	// The position on the image.
	px := W*float64(gx)/2 + float64(o.Left) + o.LeftRatio*W
	py := H*float64(gy)/2 + float64(o.Top) + o.TopRatio*H
	// The point of the overlay that goes there: by default the one named
	// like Gravity.
	ax, ay, ok := o.Anchor.point(w, h, w*float64(gx)/2, h*float64(gy)/2, o.AnchorX, o.AnchorY)
	if !ok {
		return nil, errors.New("goimgtools: unknown Anchor position")
	}
	if math.Mod(o.Angle, 360) == 0 {
		return place(top, int(math.Floor(px-ax+0.5)), int(math.Floor(py-ay+0.5)))
	}
	// The point it turns around: by default the anchor.
	vx, vy, ok := o.Pivot.point(w, h, ax, ay, o.PivotX, o.PivotY)
	if !ok {
		return nil, errors.New("goimgtools: unknown Pivot position")
	}
	// Before the turn the pivot is at this place of the image; it stays.
	turned, x, y := ops.RotateOverlay(top, o.Angle, vx, vy, px-ax+vx, py-ay+vy)
	return place(turned, x, y)
}

// turnsFrom combines the turns that stand one after the other from
// list[i] on. end is the index of the first operation after them (i when
// list[i] is no turn).
func turnsFrom(list []operation, i int, r *run) (turn ops.Orientation, end int) {
	for end = i; end < len(list); end++ {
		t, ok := list[end].(turnOp)
		if !ok {
			break
		}
		turn = turn.Then(t.orientation(r))
	}
	return turn, end
}

// firstResize returns the resize a chain starts with, when nothing but
// turns comes before it, and those turns combined.
func firstResize(list []operation, r *run) (resizeOp, ops.Orientation, bool) {
	turn, end := turnsFrom(list, 0, r)
	if end < len(list) {
		if z, ok := list[end].(resizeOp); ok {
			return z, turn, true
		}
	}
	return resizeOp{}, turn, false
}

// applyOps puts the operations of a chain on src, in the order they were
// asked for, with one exception: turns (Rotate, Flip, Flop) directly
// followed by a Resize are done after it. The picture is the same, but
// anything other than a plain mirror has to store the whole image to turn
// it, and the resized image is the smaller one to store.
//
// pre tells that the decoder has already reduced the image for the resize
// the chain starts with.
//
// It returns the last source it made also when it fails, so that the
// caller can close what there is.
func applyOps(list []operation, src pipeline.Source, r *run, pre preShrunk) (_ pipeline.Source, err error) {
	if len(r.hooks) > 0 {
		return applyHooked(list, src, r, pre)
	}
	// A panic in a step (an operation of the caller's, or a bug) leaves the
	// chain so far: it is closed before the panic goes on to become the
	// run's error, so that what it holds (memory, temporary files) is given
	// back.
	defer func() {
		if v := recover(); v != nil {
			src.Close()
			panic(v)
		}
	}()
	for i := 0; i < len(list); {
		var next pipeline.Source
		var err error
		turn, end := turnsFrom(list, i, r)
		z, resizeNext := resizeOp{}, false
		if end < len(list) {
			z, resizeNext = list[end].(resizeOp)
		}
		switch {
		case resizeNext:
			p := preShrunk{}
			if i == 0 {
				p = pre
			}
			next, err = z.resize(src, turn, p, r)
			i = end + 1
		case end > i:
			next, i = ops.Orient(src, turn), end
		default:
			next, err = list[i].apply(src, r)
			i++
		}
		if err != nil {
			return src, err
		}
		src = next
		if b := src.Bounds(); r.fits(b.Dx(), b.Dy()) != nil {
			return src, r.fits(b.Dx(), b.Dy())
		}
	}
	return src, nil
}

// shrinkFactor picks the largest of 8, 4 and 2 that still leaves the resize
// z a reduction to do in both directions, or 1 when there is none or the
// decoder's strips cannot be shrunk. turn is what is done to the image
// before the resize.
func shrinkFactor(src pipeline.Source, z resizeSpec, turn ops.Orientation) int {
	j, ok := src.(interface {
		Subsampling() (image.YCbCrSubsampleRatio, bool, bool)
	})
	if !ok {
		return 1
	}
	ratio, gray, ok := j.Subsampling()
	if !ok || (!gray && !resize.ShrinkRatio(ratio)) {
		return 1
	}
	b := src.Bounds()
	dw, dh := turn.Size(z.target(turn.Size(b.Dx(), b.Dy())))
	for _, k := range []int{8, 4, 2} {
		if b.Dx() >= k*dw && b.Dy() >= k*dh {
			return k
		}
	}
	return 1
}

// blendModes are the blend modes of ops for those of the package.
var blendModes = map[BlendMode]int{
	BlendOver: ops.BlendOver, BlendMultiply: ops.BlendMultiply, BlendScreen: ops.BlendScreen,
	BlendOverlay: ops.BlendOverlay, BlendDarken: ops.BlendDarken, BlendLighten: ops.BlendLighten,
	BlendColorDodge: ops.BlendColorDodge, BlendColorBurn: ops.BlendColorBurn, BlendHardLight: ops.BlendHardLight,
	BlendSoftLight: ops.BlendSoftLight, BlendDifference: ops.BlendDifference, BlendExclusion: ops.BlendExclusion,
}
