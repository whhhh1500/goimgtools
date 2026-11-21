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
	// whole: the decoder has scaled the whole image to the size of the
	// source instead (a WebP, as libwebp scales it), k being 0.
	whole bool
	// exact: and that size is the one the resize scales to (an SVG drawn
	// at it), which leaves it nothing to scale.
	exact bool
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
	if r.o.Method == MethodPillow && r.width > 0 && r.height > 0 {
		if w, h, ok := r.pillowSize(sw, sh); ok {
			return w, h
		}
	}
	if r.o.Method == MethodPillow && r.o.Fit == FitInside {
		return r.thumbSize(sw, sh)
	}
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

// pillowSize is the size MethodPillow resizes a sw × sh image to for a box
// of both sides: Pillow's ImageOps.contain (FitInside without ReducingGap,
// and FitContain, before ImageOps.pad pads it), ImageOps.cover
// (FitOutside), ImageOps.fit (FitCover: the box itself) and Image.thumbnail
// (FitInside with ReducingGap); the other side rounded half to even, as
// Python's round. ok is false for FitFill, and where WithoutEnlargement
// leaves the image as it is.
func (r resizeSpec) pillowSize(sw, sh int) (w, h int, ok bool) {
	w, h = r.width, r.height
	imRatio, destRatio := float64(sw)/float64(sh), float64(w)/float64(h)
	switch {
	case r.o.Fit == FitInside && r.o.ReducingGap > 0:
		w, h = r.thumbSize(sw, sh)
	case r.o.Fit == FitInside || r.o.Fit == FitContain || r.o.Fit == FitOutside:
		contain := r.o.Fit != FitOutside
		if imRatio != destRatio {
			if (imRatio > destRatio) == contain {
				h = max(1, int(math.RoundToEven(float64(float64(sh)/float64(sw))*float64(w))))
			} else {
				w = max(1, int(math.RoundToEven(float64(float64(sw)/float64(sh))*float64(h))))
			}
		}
	case r.o.Fit == FitCover && r.o.Interesting == InterestingNone:
	default:
		return 0, 0, false
	}
	if r.o.WithoutEnlargement && (w > sw || h > sh) {
		return 0, 0, false
	}
	return w, h, true
}

// fitBox is the part of a sw × sh image Pillow's ImageOps.fit resizes to
// the box: the largest of the box's aspect ratio, placed by Position (its
// centering).
func (r resizeSpec) fitBox(sw, sh int) [4]float64 {
	gx, gy := r.o.Position.gridOr(1, 1)
	cx, cy := float64(gx)/2, float64(gy)/2
	W, H := float64(sw), float64(sh)
	live, out := W/H, float64(r.width)/float64(r.height)
	cw, ch := W, H
	switch {
	case live == out:
	case live >= out:
		cw = float64(out * H)
	default:
		ch = W / out
	}
	left, top := float64((W-cw)*cx), float64((H-ch)*cy)
	return [4]float64{left, top, left + cw, top + ch}
}

// thumbSize is FitInside with MethodPillow: the size Pillow's
// Image.thumbnail gives a sw × sh image for the box (each side whole,
// floor or ceiling, whichever keeps the aspect ratio closer); a side of 0
// follows the other. With WithoutEnlargement a box the image fits in leaves
// it as it is, as thumbnail.
func (r resizeSpec) thumbSize(sw, sh int) (int, int) {
	x, y := r.width, r.height
	if r.o.WithoutEnlargement && (x == 0 || x >= sw) && (y == 0 || y >= sh) {
		return sw, sh
	}
	aspect := float64(sw) / float64(sh)
	// round_aspect: of floor and ceiling the one key prefers, the floor on
	// a tie; at least 1.
	round := func(v float64, key func(n float64) float64) int {
		f, c := math.Floor(v), math.Ceil(v)
		if key(c) < key(f) {
			f = c
		}
		return max(int(f), 1)
	}
	if x != 0 && (y == 0 || float64(x)/float64(y) < aspect) {
		y = round(float64(x)/aspect, func(n float64) float64 {
			if n == 0 {
				return 0
			}
			return math.Abs(aspect - float64(x)/n)
		})
	} else {
		x = round(float64(y)*aspect, func(n float64) float64 { return math.Abs(aspect - n/float64(y)) })
	}
	return x, y
}

// resizeOp is Resize.
type resizeOp struct {
	spec   resizeSpec
	kernel *draw.Kernel
	vips   resize.VipsKernel   // for MethodLibvips
	pillow resize.PillowFilter // for MethodPillow
}

func (z resizeOp) apply(src pipeline.Source, r *run) (pipeline.Source, error) {
	return z.resize(src, ops.Upright, preShrunk{}, r)
}

// resize scales src and then turns the result by turn; the size asked for
// is that of the turned image. pre tells when src is an image the decoder
// has already reduced: the size aimed for is then the one the full image
// would have given.
func (z resizeOp) resize(src pipeline.Source, turn ops.Orientation, pre preShrunk, r *run) (pipeline.Source, error) {
	switch z.spec.o.Method {
	case MethodLibvips:
		return z.resizeVips(src, turn, pre, r)
	case MethodPillow:
		return z.resizePillow(src, turn, pre, r)
	}
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	ew, eh := float64(sw), float64(sh) // the part of src the result spans
	switch {
	case pre.k > 1:
		sw, sh = pre.orig.X, pre.orig.Y
		ew, eh = float64(sw)/float64(pre.k), float64(sh)/float64(pre.k)
	case pre.whole:
		sw, sh = pre.orig.X, pre.orig.Y
	}
	dw, dh := z.spec.target(turn.Size(sw, sh))
	iw, ih := turn.Size(dw, dh) // the target, before the turn
	// Before anything is set up for it: the buffers of a resize grow with
	// the width it scales to.
	if err := r.fits(dw, dh); err != nil {
		return nil, err
	}
	out := src
	if pre.exact && iw == b.Dx() && ih == b.Dy() {
		// Drawn at the size wanted.
	} else if iw != b.Dx() || ih != b.Dy() || pre.k > 1 || pre.whole {
		var err error
		switch {
		case z.spec.o.LinearLight:
			out, err = resize.ScaleLinear(src, iw, ih, z.kernel, ew, eh)
		case pre.k > 1 && pre.planes:
			// The pixels of FastShrinkOnLoad already differ a little:
			// the planes are scaled as they are, in fixed point.
			out, err = resize.ScalePlanes(src, iw, ih, z.kernel, ew, eh, pre.ratio, pre.gray)
		default:
			out, err = resize.ScaleExtent(src, iw, ih, z.kernel, ew, eh)
		}
		if err != nil {
			return nil, err
		}
	}
	return z.fitBox(ops.Orient(out, turn), dw, dh, false)
}

// resizeVips is resize with MethodLibvips: sharp's resize of src, which is
// then turned by turn (sharp's EXIF turn, which it does after the resize).
// src may be a JPEG the decoder has reduced (pre): libvips keeps of it
// floor(w/k) × floor(h/k), what libjpeg makes of the last partial block
// cut off.
func (z resizeOp) resizeVips(src pipeline.Source, turn ops.Orientation, pre preShrunk, r *run) (pipeline.Source, error) {
	if pre.k > 1 {
		w, h := pre.orig.X/pre.k, pre.orig.Y/pre.k
		if b := src.Bounds(); w < b.Dx() || h < b.Dy() {
			var err error
			if src, err = ops.Crop(src, 0, 0, min(w, b.Dx()), min(h, b.Dy())); err != nil {
				return nil, err
			}
		}
	}
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	// The box, before the turn.
	tw, th := turn.Size(z.spec.width, z.spec.height)
	hs, vs := z.spec.shrinks(sw, sh, tw, th)
	iw, ih, err := resize.VipsSize(sw, sh, hs, vs, z.vips)
	if err != nil {
		return nil, err
	}
	dw, dh := turn.Size(iw, ih)
	if err := r.fits(dw, dh); err != nil {
		return nil, err
	}
	out := src
	if hs != 1 || vs != 1 {
		if out, err = resize.Vips(src, hs, vs, z.vips); err != nil {
			return nil, err
		}
	}
	// sharp multiplies the colours by alpha only for a resize.
	return z.fitBox(ops.Orient(out, turn), dw, dh, hs != 1 || vs != 1)
}

// resizePillow is resize with MethodPillow: Pillow's resize of src to the
// size the box asks of the image (of the image before the decoder reduced
// it, pre, as Pillow's draft keeps the size asked for), then turned by turn
// (no turn: turns are done first for it).
func (z resizeOp) resizePillow(src pipeline.Source, turn ops.Orientation, pre preShrunk, r *run) (pipeline.Source, error) {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	if pre.k > 1 {
		sw, sh = pre.orig.X, pre.orig.Y
	}
	dw, dh := z.spec.target(turn.Size(sw, sh))
	iw, ih := turn.Size(dw, dh)
	if err := r.fits(dw, dh); err != nil {
		return nil, err
	}
	o := resize.PillowOptions{Gap: z.spec.o.ReducingGap}
	if pre.k > 1 && o.Gap > 0 {
		// Image.thumbnail resizes the part of the reduced image draft
		// says the image is: its size over the factor.
		o.Box = [4]float64{0, 0, float64(pre.orig.X) / float64(pre.k), float64(pre.orig.Y) / float64(pre.k)}
	}
	spec := z.spec
	pillowFit := spec.width > 0 && spec.height > 0 && turn == ops.Upright && !(spec.o.WithoutEnlargement && (dw > b.Dx() || dh > b.Dy()))
	if pillowFit && spec.o.Fit == FitCover && spec.o.Interesting == InterestingNone {
		// ImageOps.fit: the part of the image of the box's aspect ratio,
		// resized to the box.
		o.Box = spec.fitBox(b.Dx(), b.Dy())
		return resize.Pillow(src, iw, ih, z.pillow, o)
	}
	out, err := resize.Pillow(src, iw, ih, z.pillow, o)
	if err != nil {
		return nil, err
	}
	if pillowFit && spec.o.Fit == FitContain && (dw != spec.width || dh != spec.height) {
		// ImageOps.pad: the image placed by Position (its centering),
		// across if it is narrower, else down, rounded half to even.
		gx, gy := spec.o.Position.gridOr(1, 1)
		x, y := 0, 0
		if dw != spec.width {
			x = int(math.RoundToEven(float64(spec.width-dw) * float64(gx) / 2))
		} else {
			y = int(math.RoundToEven(float64(spec.height-dh) * float64(gy) / 2))
		}
		var bg color.Color = color.Black
		if spec.o.Background != nil {
			bg = spec.o.Background
		}
		return ops.Extend(out, x, y, max(0, spec.width-dw-x), max(0, spec.height-dh-y), bg)
	}
	return z.fitBox(ops.Orient(out, turn), dw, dh, false)
}

// pillowDraft is the factor Pillow's draft(mode, size) reduces a JPEG by for
// the resize z after turn: of 8, 4 and 2 the largest at most the whole
// part of the smaller of the factors the two sides are reduced by; 1 for
// none, or when the decoder cannot reduce the image.
func pillowDraft(src pipeline.Source, z resizeSpec, turn ops.Orientation) int {
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
	w, h := turn.Size(z.target(turn.Size(b.Dx(), b.Dy())))
	if g := z.o.ReducingGap; g > 0 {
		// Image.thumbnail asks for the box times the gap.
		bw, bh := turn.Size(z.width, z.height)
		if bw == 0 {
			bw = w
		}
		if bh == 0 {
			bh = h
		}
		w, h = max(1, int(float64(bw)*g)), max(1, int(float64(bh)*g))
	}
	scale := min(b.Dx()/w, b.Dy()/h)
	for _, k := range []int{8, 4, 2} {
		if scale >= k {
			return k
		}
	}
	return 1
}

// shrinks are the factors sharp reduces a sw × sh image by for a box of
// tw × th (its ResolveShrink): each side's size over the box's, the larger
// of the two for FitInside and FitContain, the smaller for FitCover and
// FitOutside; with a side of 0 the other's; at least 1 with
// WithoutEnlargement, and at most the size.
func (r resizeSpec) shrinks(sw, sh, tw, th int) (hs, vs float64) {
	hs, vs = 1, 1
	switch {
	case tw > 0 && th > 0:
		hs, vs = float64(sw)/float64(tw), float64(sh)/float64(th)
		switch r.o.Fit {
		case FitCover, FitOutside:
			hs = min(hs, vs)
			vs = hs
		case FitInside, FitContain:
			hs = max(hs, vs)
			vs = hs
		}
	case tw > 0:
		hs = float64(sw) / float64(tw)
		vs = hs
	case th > 0:
		vs = float64(sh) / float64(th)
		hs = vs
	}
	if r.o.WithoutEnlargement {
		hs, vs = max(1, hs), max(1, vs)
	}
	return min(hs, float64(sw)), min(vs, float64(sh))
}

// fitBox crops or pads out, a dw × dh image, to the box of z: what
// FitCover and FitContain do after the resize. premultiplied says that
// sharp pads the image while its colours are multiplied by alpha (see
// vipsBackground).
func (z resizeOp) fitBox(out pipeline.Source, dw, dh int, premultiplied bool) (pipeline.Source, error) {
	o, width, height := z.spec.o, z.spec.width, z.spec.height
	switch {
	case o.Fit == FitCover && width > 0 && height > 0 && (dw > width || dh > height):
		cw, ch := min(dw, width), min(dh, height)
		switch o.Interesting {
		case InterestingEntropy:
			return ops.SmartCrop(out, cw, ch, ops.InterestingEntropy)
		case InterestingAttention:
			return ops.SmartCrop(out, cw, ch, ops.InterestingAttention)
		}
		// As sharp: the middle rounded up.
		gx, gy := o.Position.gridOr(1, 1)
		return ops.Crop(out, (dw-cw+gx%2)*gx/2, (dh-ch+gy%2)*gy/2, cw, ch)
	case o.Fit == FitContain && width > 0 && height > 0 && (dw < width || dh < height):
		var bg color.Color = color.Black
		if o.Background != nil {
			bg = o.Background
		}
		if premultiplied && !pipeline.IsOpaque(out) {
			bg = vipsBackground(bg)
		}
		gx, gy := o.Position.gridOr(1, 1)
		left, top := (width-dw)*gx/2, (height-dh)*gy/2
		return ops.Extend(out, left, top, width-dw-left, height-dh-top, bg)
	}
	return out, nil
}

// vipsBackground is the colour sharp pads an image with transparency with:
// as sharp pads the image while its colours are multiplied by alpha, the
// colour is multiplied by alpha (in double precision, the fraction dropped)
// and divided again with the image (255/alpha in single precision, the
// fraction dropped).
func vipsBackground(c color.Color) color.Color {
	n := color.NRGBAModel.Convert(c).(color.NRGBA)
	if n.A == 255 || n.A == 0 {
		return color.NRGBA{R: n.R * (n.A / 255), G: n.G * (n.A / 255), B: n.B * (n.A / 255), A: n.A}
	}
	na, f := float64(n.A)/255, float32(255/float64(n.A))
	ch := func(v uint8) uint8 { return uint8(min(float32(uint8(float64(v)*na))*f, 255)) }
	return color.NRGBA{R: ch(n.R), G: ch(n.G), B: ch(n.B), A: n.A}
}

// extractOp is Crop.
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
	px := float64(W*float64(gx)/2) + float64(o.Left) + float64(o.LeftRatio*W)
	py := float64(H*float64(gy)/2) + float64(o.Top) + float64(o.TopRatio*H)
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
			if z.spec.o.Method == MethodPillow || z.spec.o.Method == MethodLibvips && !autoTurns(list[i:end]) {
				// As sharp: turns asked for before the resize are done
				// before it; with Pillow every turn.
				if p.k > 1 {
					p.orig.X, p.orig.Y = turn.Size(p.orig.X, p.orig.Y)
				}
				src, turn = ops.Orient(src, turn), ops.Upright
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

// firstResizeAt is the index of the operation after the turns a chain
// starts with.
func firstResizeAt(list []operation, r *run) int {
	_, end := turnsFrom(list, 0, r)
	return end
}

// autoTurns reports whether the turns of list all follow the EXIF
// orientation (Rotate without an angle).
func autoTurns(list []operation) bool {
	for _, op := range list {
		if t, ok := op.(turnOp); ok && !t.auto {
			return false
		}
	}
	return true
}

// vipsShrinkOnLoad is the factor sharp has libjpeg reduce a JPEG by while
// it decodes it, for the resize z (with MethodLibvips) after turn: of the
// factors the resize reduces the two sides by, the smaller one; 8, 4 or 2,
// the largest it reaches (twice that without FastShrinkOnLoad, which
// leaves at least 2 to the resize), halved when the factor is that whole
// number; 1 for none, or when the decoder cannot reduce the image.
func vipsShrinkOnLoad(src pipeline.Source, z resizeSpec, turn ops.Orientation) int {
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
	tw, th := turn.Size(z.width, z.height)
	hs, vs := z.shrinks(b.Dx(), b.Dy(), tw, th)
	shrink := min(hs, vs)
	f := 2.0
	if z.o.FastShrinkOnLoad {
		f = 1
	}
	k := 1
	switch {
	case shrink >= 8*f:
		k = 8
	case shrink >= 4*f:
		k = 4
	case shrink >= 2*f:
		k = 2
	}
	if k > 1 && int(shrink) == k {
		k /= 2
	}
	return k
}

// vipsWebPScale is the size sharp has libwebp decode a WebP at, for the
// resize z (with MethodLibvips and FastShrinkOnLoad) after turn: the image
// scaled by 1/s, s the smaller of the factors the resize reduces the two
// sides by, each side rounded to the nearest (half to even, as libvips'
// rint); 0, 0 when s is not above 1.
func vipsWebPScale(src pipeline.Source, z resizeSpec, turn ops.Orientation) (int, int) {
	b := src.Bounds()
	tw, th := turn.Size(z.width, z.height)
	hs, vs := z.shrinks(b.Dx(), b.Dy(), tw, th)
	shrink := min(hs, vs)
	if !(shrink > 1) {
		return 0, 0
	}
	scale := 1 / shrink
	w := math.RoundToEven(float64(b.Dx()) * scale)
	h := math.RoundToEven(float64(b.Dy()) * scale)
	if w < 1 || h < 1 {
		return 0, 0
	}
	return int(w), int(h)
}

// webpScale is the size a lossy WebP is decoded at, as libwebp scales it
// while decoding it, for the resize z (with MethodDefault and FastShrinkOnLoad)
// after turn: twice the size of the result, which leaves the kernel a
// reduction of 2, when that is at most a third of the image each way; 0, 0
// otherwise.
func webpScale(src pipeline.Source, z resizeSpec, turn ops.Orientation) (int, int) {
	b := src.Bounds()
	dw, dh := turn.Size(z.target(turn.Size(b.Dx(), b.Dy())))
	if dw < 1 || dh < 1 || b.Dx() < 3*dw || b.Dy() < 3*dh {
		return 0, 0
	}
	return 2 * dw, 2 * dh
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
	BlendClear: ops.BlendClear, BlendSource: ops.BlendSource, BlendIn: ops.BlendIn, BlendOut: ops.BlendOut,
	BlendAtop: ops.BlendAtop, BlendDest: ops.BlendDest, BlendDestOver: ops.BlendDestOver, BlendDestIn: ops.BlendDestIn,
	BlendDestOut: ops.BlendDestOut, BlendDestAtop: ops.BlendDestAtop, BlendXor: ops.BlendXor, BlendAdd: ops.BlendAdd,
	BlendSaturate: ops.BlendSaturate,
}
