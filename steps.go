package goimgtools

import (
	"errors"
	"fmt"
	"image/color"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/whhhh1500/goimgtools/internal/ops"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// Every step of a recipe has a name and its arguments in words, the same
// words the command line and Do take: "resize" 1536,0,inside,cubic,
// "blur" 2, "rotate" 90. Two things are built on that:
//
//   - Hooks (Around) go around every step of a recipe, built-in or not:
//     they see the step's name and arguments and may change its input, its
//     result, or do the step their own way. This is how built-in behaviour
//     is changed for a recipe, as overriding a method changes it for a
//     class.
//   - Named operations (RegisterOperation) can be asked for by name, with
//     Do or on the command line of a program made with package cli (-op
//     NAME:ARGS), next to the built-in ones, which are there under the
//     same names.

// Step is one step of a recipe, as a hook sees it.
type Step struct {
	// Name is the operation: one of the built-in names (see Operations),
	// the name it was registered under, "pipe" for Pipe and "pixels" for
	// Pixels.
	Name string
	// Args are the arguments in the words Do takes; for "composite",
	// "pipe" and "pixels", none.
	Args []string
	// Index is the place of the step in the recipe, from 0.
	Index int
}

func (s Step) String() string {
	if len(s.Args) == 0 {
		return s.Name
	}
	return s.Name + ":" + strings.Join(s.Args, ",")
}

// A Hook goes around a step: it gets the step, the source the step reads
// and run, which does the step on a source, and returns the source of the
// result. It may change the input before calling run, wrap or replace the
// result after it, or not call run at all and do the step its own way;
// run may be called at most once.
//
// The rules of Operation.Apply hold: the source returned closes in when
// it is closed (directly, or through what run returned), and nothing is
// read when the hook runs. A hook that returns an error need not clean up
// in or what run returned: the run closes both.
type Hook func(step Step, in pipeline.Source, run func(pipeline.Source) (pipeline.Source, error)) (pipeline.Source, error)

// Around puts h around every step of the recipe, the built-in ones
// included. Hooks added first are outermost. With hooks the steps are
// done exactly as written, one after the other (without them, turns
// directly followed by a Resize are done after it, on the smaller image);
// FastShrinkOnLoad still applies when the recipe starts with the Resize.
// Hooks run for every run of the recipe, also for OutputSize, and for
// every frame with Animated.
func (m *Image) Around(h Hook) *Image {
	if h == nil {
		return m.fail(errors.New("goimgtools: Around needs a hook"))
	}
	m.hooks = append(slices.Clip(m.hooks), h)
	return m
}

// describe names a step of a chain.
func describe(op operation) (string, []string) {
	f := func(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) }
	d := func(v int) string { return strconv.Itoa(v) }
	switch o := op.(type) {
	case turnOp:
		if o.auto {
			return "rotate", []string{"auto"}
		}
		switch o.o {
		case ops.Flip:
			return "flip", nil
		case ops.Flop:
			return "flop", nil
		case ops.Rotate90:
			return "rotate", []string{"90"}
		case ops.Rotate180:
			return "rotate", []string{"180"}
		case ops.Rotate270:
			return "rotate", []string{"270"}
		}
		return "rotate", []string{"0"}
	case resizeOp:
		w := []string{d(o.spec.width), d(o.spec.height), fitNames[o.spec.o.Fit], kernelNames[o.spec.o.Kernel]}
		lib := o.spec.o.Method != ResizeDraw
		if n, ok := interestingNames[o.spec.o.Interesting]; ok {
			w = append(w, n)
		} else if n, ok := positionNames[o.spec.o.Position]; ok {
			w = append(w, n)
		} else if o.spec.o.LinearLight || lib {
			w = append(w, "center")
		}
		if o.spec.o.LinearLight || lib {
			w = append(w, lightNames[o.spec.o.LinearLight])
		}
		if lib {
			w = append(w, methodNames[o.spec.o.Method])
		}
		if g := o.spec.o.ReducingGap; g != 0 {
			w = append(w, f(g))
		}
		return "resize", w
	case extractOp:
		return "crop", []string{d(o.left), d(o.top), d(o.width), d(o.height)}
	case flattenOp:
		return "flatten", []string{colorWord(o.background)}
	case compositeOp:
		return "composite", nil
	case grayscaleOp:
		if o.linear {
			return "grayscale", []string{"linear"}
		}
		return "grayscale", nil
	case modulateOp:
		if o.lightness != 0 {
			return "modulate", []string{f(o.brightness), f(o.saturation), f(o.hue), f(o.lightness)}
		}
		return "modulate", []string{f(o.brightness), f(o.saturation), f(o.hue)}
	case boxBlurOp:
		return "boxblur", []string{f(o.radius)}
	case blurOp:
		switch {
		case o.o.MinAmplitude != 0:
			return "blur", []string{f(o.sigma), "libvips", f(o.o.MinAmplitude)}
		case o.o.Method == BlurLibvips:
			return "blur", []string{f(o.sigma), "libvips"}
		}
		return "blur", []string{f(o.sigma)}
	case sharpenOp:
		if o.o.Sigma == 0 {
			return "sharpen", nil
		}
		if c := o.o.Curve; o.o.Method == SharpenLibvips {
			return "sharpen", []string{f(o.o.Sigma), "libvips", f(c.Flat), f(c.Jagged), f(c.JaggedFrom), f(c.MaxBrighten), f(c.MaxDarken)}
		}
		return "sharpen", []string{f(o.o.Sigma), f(o.o.Amount), d(o.o.Threshold)}
	case extendOp:
		if o.from {
			return "extend", []string{d(o.top), d(o.right), d(o.bottom), d(o.left), extensionNames[o.with]}
		}
		return "extend", []string{d(o.top), d(o.right), d(o.bottom), d(o.left), colorWord(o.background)}
	case trimOp:
		return "trim", o.words()
	case rotateAnyOp:
		w := []string{f(o.deg), colorWord(o.background)}
		if o.interp != InterpolateBilinear {
			w = append(w, interpolatorNames[o.interp])
		}
		return "rotate", w
	case rotatePillowOp:
		w := []string{f(o.deg), "transparent", "pillow"}
		if o.background != nil {
			w[1] = colorWord(o.background)
		}
		if o.o.Interpolator != InterpolateBilinear {
			w = append(w, interpolatorNames[o.o.Interpolator])
		}
		if o.o.Expand {
			w = append(w, "expand")
		}
		return "rotate", w
	case affineOp:
		return "affine", o.words()
	case transformOp:
		return "transform", o.words()
	case levelsOp:
		return o.name, o.words()
	case tintOp:
		return "tint", []string{colorWord(o.c)}
	case normalizeOp:
		return "normalize", []string{f(o.o.Lower), f(o.o.Upper)}
	case medianOp:
		return "median", []string{d(o.size)}
	case rankOp:
		return "rank", []string{d(o.size), d(o.rank)}
	case morphOp:
		if o.dilate {
			return "dilate", []string{d(o.width)}
		}
		return "erode", []string{d(o.width)}
	case claheOp:
		return "clahe", []string{d(o.o.Width), d(o.o.Height), d(o.o.MaxSlope)}
	case convolveOp:
		return "convolve", o.words()
	case recombOp:
		return "recomb", o.words()
	case channelOp:
		return o.name, o.words()
	case pillowOp:
		return o.name, o.words()
	case namedOp:
		return o.name, o.args
	case customOp:
		if o.pixels {
			return "pixels", nil
		}
		return "pipe", nil
	}
	return fmt.Sprintf("%T", op), nil
}

var (
	fitNames    = map[Fit]string{FitInside: "inside", FitCover: "cover", FitOutside: "outside", FitFill: "fill", FitContain: "contain"}
	kernelNames = map[Kernel]string{KernelCubic: "cubic", KernelNearest: "nearest", KernelLinear: "linear",
		KernelMitchell: "mitchell", KernelLanczos2: "lanczos2", KernelLanczos3: "lanczos3", KernelMKS2013: "mks2013",
		KernelMKS2021: "mks2021", KernelBox: "box", KernelHamming: "hamming"}
	positionNames = map[Position]string{NorthWest: "northwest", North: "north", NorthEast: "northeast",
		West: "west", Center: "center", East: "east", SouthWest: "southwest", South: "south", SouthEast: "southeast"}
	interestingNames = map[Interesting]string{InterestingEntropy: "entropy", InterestingAttention: "attention"}
	lightNames       = map[bool]string{false: "srgb", true: "linear"}
	methodNames      = map[ResizeMethod]string{ResizeDraw: "draw", ResizeLibvips: "libvips", ResizePillow: "pillow"}
	extensionNames   = map[ExtendMode]string{ExtendCopy: "copy", ExtendRepeat: "repeat", ExtendMirror: "mirror"}
)

// colorWord writes c as ParseColor reads it.
func colorWord(c color.Color) string {
	if c == nil {
		return "black"
	}
	n := color.NRGBAModel.Convert(c).(color.NRGBA)
	if n.A == 255 {
		return fmt.Sprintf("#%02x%02x%02x", n.R, n.G, n.B)
	}
	return fmt.Sprintf("#%02x%02x%02x%02x", n.R, n.G, n.B, n.A)
}

// ParseColor reads a colour as the command line and Do take it: white,
// black, transparent (or none), #rgb, #rrggbb or #rrggbbaa.
func ParseColor(s string) (color.Color, error) {
	switch strings.ToLower(s) {
	case "white":
		return color.White, nil
	case "black":
		return color.Black, nil
	case "transparent", "none":
		return color.Transparent, nil
	}
	h := strings.TrimPrefix(s, "#")
	if len(h) == 3 {
		h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
	}
	v, err := strconv.ParseUint(h, 16, 32)
	switch {
	case err != nil || (len(h) != 6 && len(h) != 8):
		return nil, fmt.Errorf("goimgtools: want white, black, transparent, #rgb, #rrggbb or #rrggbbaa, got %q", s)
	case len(h) == 8:
		return color.NRGBA{uint8(v >> 24), uint8(v >> 16), uint8(v >> 8), uint8(v)}, nil
	}
	return color.RGBA{uint8(v >> 16), uint8(v >> 8), uint8(v), 0xff}, nil
}

// ---- applying the steps with hooks ----

// applyHooked does the steps of list one by one, each through the hooks.
func applyHooked(list []operation, src pipeline.Source, r *run, pre preShrunk) (pipeline.Source, error) {
	for i, op := range list {
		name, args := describe(op)
		step := Step{Name: name, Args: args, Index: i}
		p := preShrunk{}
		if i == 0 {
			p = pre
		}
		do := func(in pipeline.Source) (pipeline.Source, error) { return applyOne(op, in, r, p) }
		guarded := &onceSource{Source: src}
		var made pipeline.Source // what the step itself made, for cleaning up
		used := false
		call := func(in pipeline.Source) (pipeline.Source, error) {
			if used {
				return nil, fmt.Errorf("goimgtools: a hook ran step %s twice", step)
			}
			used = true
			out, err := do(in)
			if err == nil {
				made = out
			}
			return out, err
		}
		for k := len(r.hooks) - 1; k >= 0; k-- {
			h, next := r.hooks[k], call
			call = func(in pipeline.Source) (pipeline.Source, error) { return h(step, in, next) }
		}
		out, err := func() (pipeline.Source, error) {
			// A panic is cleaned up as an error is, then goes on.
			defer func() {
				if v := recover(); v != nil {
					if made != nil {
						made.Close()
					}
					guarded.Close()
					panic(v)
				}
			}()
			return call(guarded)
		}()
		if err == nil && out == nil {
			err = fmt.Errorf("goimgtools: a hook returned neither an image nor an error for step %s", step)
		}
		if err != nil {
			if made != nil {
				made.Close()
			}
			guarded.Close()
			return closedSource{}, err
		}
		src = out
		if b := src.Bounds(); r.fits(b.Dx(), b.Dy()) != nil {
			return src, r.fits(b.Dx(), b.Dy())
		}
	}
	return src, nil
}

// applyOne does op alone on in.
func applyOne(op operation, in pipeline.Source, r *run, pre preShrunk) (pipeline.Source, error) {
	switch o := op.(type) {
	case resizeOp:
		return o.resize(in, ops.Upright, pre, r)
	case turnOp:
		return ops.Orient(in, o.orientation(r)), nil
	}
	return op.apply(in, r)
}

// onceSource is a source whose Close closes it the first time only, so
// that a hook's error can be cleaned up whatever the hook had done.
type onceSource struct {
	pipeline.Source
	closed bool
}

func (o *onceSource) Close() error {
	if o.closed {
		return nil
	}
	o.closed = true
	return o.Source.Close()
}

func (o *onceSource) Opaque() bool        { return pipeline.IsOpaque(o.Source) }
func (o *onceSource) Meta() pipeline.Meta { return pipeline.MetaOf(o.Source) }

// closedSource stands for a chain that was already closed while failing.
type closedSource struct{ pipeline.Source }

func (closedSource) Close() error { return nil }

// ---- named operations ----

// OperationDef is an operation that can be asked for by name.
type OperationDef struct {
	// Name: lower-case letters, digits and hyphens, starting with a letter.
	Name string
	// Usage shows the arguments, as help prints them: "SIGMA[,AMOUNT]".
	Usage string
	// Help says in a line what it does.
	Help string
	// New makes the operation from its arguments: the words given to Do,
	// or the command line's NAME:ARG1,ARG2 split at the commas.
	//
	// The built-in ones have it too, so that an operation registered under
	// a built-in name can do the built-in one and add to it. Done that way,
	// on its own, "rotate auto" reads the orientation from the Meta of the
	// source it is given (the decoder's, when it is the first step).
	New func(args ...string) (Operation, error)

	// builtIn makes a built-in step instead (it applies it to the image).
	builtIn func(m *Image, args []string) error
}

var (
	opMu      sync.RWMutex
	opDefs    = map[string]OperationDef{} // registered
	opNameRe  = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	errOpName = errors.New("goimgtools: an operation name is lower-case letters, digits and hyphens, starting with a letter")
)

// RegisterOperation makes d available by name, to Do and to the command
// line of a program made with package cli. A registration under a name
// already taken replaces it, the built-in names included: Do and the
// command line then use d (the methods, such as Blur, stay as they are;
// to change what those do, see Around). It is safe to call at any time
// from any goroutine; recipes made before keep what they were given.
func RegisterOperation(d OperationDef) error {
	if !opNameRe.MatchString(d.Name) {
		return errOpName
	}
	if d.New == nil {
		return errors.New("goimgtools: RegisterOperation needs New")
	}
	d.builtIn = nil
	opMu.Lock()
	defer opMu.Unlock()
	opDefs[d.Name] = d
	return nil
}

// UnregisterOperation removes the registration of name; a built-in
// operation of that name is the one used again.
func UnregisterOperation(name string) {
	opMu.Lock()
	defer opMu.Unlock()
	delete(opDefs, name)
}

// LookupOperation returns the operation of that name: a registered one
// before a built-in one.
func LookupOperation(name string) (OperationDef, bool) {
	opMu.RLock()
	d, ok := opDefs[name]
	opMu.RUnlock()
	if ok {
		return d, true
	}
	d, ok = builtInOps[name]
	return d, ok
}

// Operations lists the operations that can be asked for by name, the
// built-in ones and the registered ones, by name.
func Operations() []OperationDef {
	opMu.RLock()
	defer opMu.RUnlock()
	all := make(map[string]OperationDef, len(builtInOps)+len(opDefs))
	for n, d := range builtInOps {
		all[n] = d
	}
	for n, d := range opDefs {
		all[n] = d
	}
	out := make([]OperationDef, 0, len(all))
	for _, d := range all {
		out = append(out, d)
	}
	slices.SortFunc(out, func(a, b OperationDef) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// Do adds the operation of that name to the recipe, made from args (see
// Operations for the names and what each takes). Do("blur", "2") is
// Blur(2), unless an operation registered as "blur" replaces it.
func (m *Image) Do(name string, args ...string) *Image {
	d, ok := LookupOperation(name)
	if !ok {
		return m.fail(fmt.Errorf("goimgtools: no operation %q (see Operations)", name))
	}
	if d.builtIn != nil {
		if err := d.builtIn(m, args); err != nil {
			return m.fail(fmt.Errorf("goimgtools: %s: %w", name, err))
		}
		return m
	}
	op, err := d.New(args...)
	if err != nil {
		return m.fail(fmt.Errorf("goimgtools: %s: %w", name, err))
	}
	if op == nil {
		return m.fail(fmt.Errorf("goimgtools: %s made no operation", name))
	}
	return m.add(namedOp{customOp{op: op}, name, slices.Clone(args)})
}

// namedOp is an operation added by name.
type namedOp struct {
	customOp
	name string
	args []string
}

// argument readers for the built-in operations

func wantArgs(args []string, lo, hi int) error {
	if len(args) < lo || len(args) > hi {
		if lo == hi {
			return fmt.Errorf("want %d arguments, got %d", lo, len(args))
		}
		return fmt.Errorf("want %d to %d arguments, got %d", lo, hi, len(args))
	}
	return nil
}

func argInts(args []string) ([]int, error) {
	v := make([]int, len(args))
	for i, a := range args {
		n, err := strconv.Atoi(strings.TrimSpace(a))
		if err != nil {
			return nil, fmt.Errorf("want a whole number, got %q", a)
		}
		v[i] = n
	}
	return v, nil
}

func argFloats(args []string) ([]float64, error) {
	v := make([]float64, len(args))
	for i, a := range args {
		f, err := strconv.ParseFloat(strings.TrimSpace(a), 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, fmt.Errorf("want a number, got %q", a)
		}
		v[i] = f
	}
	return v, nil
}

func pick[T comparable](names map[T]string, word, what string) (T, error) {
	for v, n := range names {
		if n == strings.ToLower(word) {
			return v, nil
		}
	}
	var zero T
	return zero, fmt.Errorf("unknown %s %q", what, word)
}

// builtInOps are the methods of Image that take arguments in words.
var builtInOps = map[string]OperationDef{
	"resize": {Name: "resize", Usage: "WIDTH,HEIGHT[,FIT[,KERNEL[,POSITION[,LIGHT[,METHOD[,GAP]]]]]]",
		Help: "Resize: 0 for a side keeps the proportions; FIT inside, cover, contain, outside, fill; KERNEL cubic, nearest, linear, mitchell, lanczos2, lanczos3, mks2013, mks2021, box, hamming; POSITION (what cover keeps, where contain puts the image) northwest ... center ... southeast, or entropy or attention for cover; LIGHT srgb (the levels as they are) or linear (in linear light); METHOD draw (the default), libvips (sharp's resize, to the byte) or pillow (Pillow's, to the byte); GAP Pillow's reducing_gap (with pillow; 2 as its thumbnail)",
		builtIn: func(m *Image, a []string) error {
			if err := wantArgs(a, 2, 8); err != nil {
				return err
			}
			v, err := argInts(a[:2])
			if err != nil {
				return err
			}
			var o ResizeOptions
			if len(a) > 2 {
				if o.Fit, err = pick(fitNames, a[2], "fit"); err != nil {
					return err
				}
			}
			if len(a) > 3 {
				if o.Kernel, err = pick(kernelNames, a[3], "kernel"); err != nil {
					return err
				}
			}
			if len(a) > 4 {
				if o.Interesting, err = pick(interestingNames, a[4], "position"); err != nil {
					if o.Position, err = pick(positionNames, a[4], "position"); err != nil {
						return err
					}
				}
			}
			if len(a) > 5 {
				if o.LinearLight, err = pick(lightNames, a[5], "light"); err != nil {
					return err
				}
			}
			if len(a) > 6 {
				if o.Method, err = pick(methodNames, a[6], "method"); err != nil {
					return err
				}
			}
			if len(a) > 7 {
				g, err := argFloats(a[7:8])
				if err != nil {
					return err
				}
				o.ReducingGap = g[0]
			}
			m.Resize(v[0], v[1], o)
			return nil
		}},
	"crop": {Name: "crop", Usage: "LEFT,TOP,WIDTH,HEIGHT", Help: "Extract: cut out a rectangle",
		builtIn: func(m *Image, a []string) error {
			if err := wantArgs(a, 4, 4); err != nil {
				return err
			}
			v, err := argInts(a)
			if err != nil {
				return err
			}
			m.Extract(v[0], v[1], v[2], v[3])
			return nil
		}},
	"rotate": {Name: "rotate", Usage: "auto|DEGREES[,BACKGROUND][,pillow][,nearest|bilinear|bicubic][,expand]",
		Help: "Rotate (auto: by the EXIF orientation) or RotateBy, clockwise; pillow: as Pillow's rotate, expand: the canvas holds the whole image",
		builtIn: func(m *Image, a []string) error {
			if err := wantArgs(a, 1, 5); err != nil {
				return err
			}
			if strings.EqualFold(a[0], "auto") && len(a) == 1 {
				m.Rotate()
				return nil
			}
			v, err := argFloats(a[:1])
			if err != nil {
				return err
			}
			var bg color.Color
			var o RotateOptions
			seen := map[string]bool{}
			for _, w := range a[1:] {
				kind := w
				switch {
				case w == "pillow":
					o.Method = RotatePillow
				case w == "expand":
					o.Expand = true
				case w == "nearest" || w == "bilinear" || w == "bicubic":
					kind = "interpolator"
					o.Interpolator, _ = pick(interpolatorNames, w, "interpolator")
				default:
					kind = "background"
					if bg, err = ParseColor(w); err != nil {
						return err
					}
				}
				if seen[kind] {
					return fmt.Errorf("%s given twice", kind)
				}
				seen[kind] = true
			}
			m.RotateBy(v[0], bg, o)
			return nil
		}},
	"affine": {Name: "affine", Usage: "A,B,C,D[,BACKGROUND[,IDX,IDY,ODX,ODY]][,bilinear|nearest|bicubic]", Help: "Affine: (x, y) moves to (A·x + B·y, C·x + D·y), as sharp's affine",
		builtIn: func(m *Image, a []string) error {
			var interp Interpolator
			if n := len(a); n > 4 {
				if v, err := pick(interpolatorNames, a[n-1], ""); err == nil {
					interp, a = v, a[:n-1]
				}
			}
			if len(a) != 4 && len(a) != 5 && len(a) != 9 {
				return errors.New("want A,B,C,D, then BACKGROUND, then IDX,IDY,ODX,ODY")
			}
			v, err := argFloats(append(append([]string{}, a[:4]...), a[min(5, len(a)):]...))
			if err != nil {
				return err
			}
			var o AffineOptions
			if len(a) >= 5 {
				if o.Background, err = ParseColor(a[4]); err != nil {
					return err
				}
			}
			if len(v) == 8 {
				o.InputX, o.InputY, o.OutputX, o.OutputY = v[4], v[5], v[6], v[7]
			}
			o.Interpolator = interp
			m.Affine(v[0], v[1], v[2], v[3], o)
			return nil
		}},
	"transform": {Name: "transform", Usage: "WIDTH,HEIGHT,affine|extent|perspective|quad,NUMBERS...[,nearest|bilinear|bicubic][,BACKGROUND]",
		Help: "Transform: Pillow's Image.transform to WIDTH x HEIGHT, with 6, 4, 8 or 8 numbers (TransformAffine, TransformExtent, TransformPerspective, TransformQuad)",
		builtIn: func(m *Image, a []string) error {
			if len(a) < 3 {
				return errors.New("want WIDTH,HEIGHT, the kind of transform and its numbers")
			}
			size, err := argInts(a[:2])
			if err != nil {
				return err
			}
			kind, err := pick(transformNames, a[2], "transform")
			if err != nil {
				return err
			}
			n := transformArgs[kind]
			if len(a) < 3+n || len(a) > 5+n {
				return fmt.Errorf("%s takes %d numbers, then an interpolator and a background", a[2], n)
			}
			v, err := argFloats(a[3 : 3+n])
			if err != nil {
				return err
			}
			t := Transformation{kind: kind}
			copy(t.data[:], v)
			var o TransformOptions
			seen := map[string]bool{}
			for _, w := range a[3+n:] {
				what := "background"
				if i, err := pick(interpolatorNames, w, ""); err == nil {
					what, o.Interpolator = "interpolator", i
				} else if o.Background, err = ParseColor(w); err != nil {
					return err
				}
				if seen[what] {
					return fmt.Errorf("%s given twice", what)
				}
				seen[what] = true
			}
			m.Transform(size[0], size[1], t, o)
			return nil
		}},
	"flip": {Name: "flip", Help: "Flip: upside down", builtIn: func(m *Image, a []string) error {
		if err := wantArgs(a, 0, 0); err != nil {
			return err
		}
		m.Flip()
		return nil
	}},
	"flop": {Name: "flop", Help: "Flop: left to right", builtIn: func(m *Image, a []string) error {
		if err := wantArgs(a, 0, 0); err != nil {
			return err
		}
		m.Flop()
		return nil
	}},
	"trim": {Name: "trim", Usage: "[THRESHOLD[,BACKGROUND[,MARGIN[,lineart]]]]", Help: "Trim the edges of the colour of the top-left pixel or BACKGROUND (a colour, or auto), threshold 10 by default, keeping MARGIN pixels; lineart: without the median filter that passes over noise",
		builtIn: func(m *Image, a []string) error {
			if err := wantArgs(a, 0, 4); err != nil {
				return err
			}
			t := 10.0
			if len(a) >= 1 {
				v, err := argFloats(a[:1])
				if err != nil {
					return err
				}
				t = v[0]
			}
			var o TrimOptions
			if len(a) >= 2 && a[1] != "auto" {
				c, err := ParseColor(a[1])
				if err != nil {
					return err
				}
				o.Background = c
			}
			if len(a) >= 3 {
				v, err := argInts(a[2:3])
				if err != nil {
					return err
				}
				o.Margin = v[0]
			}
			if len(a) == 4 {
				if a[3] != "lineart" {
					return errors.New("the fourth word is lineart")
				}
				o.LineArt = true
			}
			m.Trim(t, o)
			return nil
		}},
	"extend": {Name: "extend", Usage: "TOP,RIGHT,BOTTOM,LEFT[,COLOR]", Help: "Extend: add a border of COLOR, or (ExtendWith) copy, repeat or mirror for one made of the image",
		builtIn: func(m *Image, a []string) error {
			if err := wantArgs(a, 4, 5); err != nil {
				return err
			}
			v, err := argInts(a[:4])
			if err != nil {
				return err
			}
			var bg color.Color
			if len(a) == 5 {
				if how, err := pick(extensionNames, a[4], "extension"); err == nil {
					m.ExtendWith(v[0], v[1], v[2], v[3], how)
					return nil
				}
				if bg, err = ParseColor(a[4]); err != nil {
					return err
				}
			}
			m.Extend(v[0], v[1], v[2], v[3], bg)
			return nil
		}},
	"grayscale": {Name: "grayscale", Usage: "[linear]", Help: "Grayscale: Rec. 601 brightness (as Pillow), or linear: the luminance of linear light (as sharp)", builtIn: func(m *Image, a []string) error {
		if err := wantArgs(a, 0, 1); err != nil {
			return err
		}
		if len(a) == 1 && a[0] != "linear" {
			return errors.New("the word after grayscale is linear")
		}
		m.Grayscale(GrayscaleOptions{Linear: len(a) == 1})
		return nil
	}},
	"modulate": {Name: "modulate", Usage: "BRIGHTNESS,SATURATION,HUE[,LIGHTNESS]", Help: "Modulate: brightness and saturation factors, hue in degrees, lightness added (L*, 0 to 100)",
		builtIn: func(m *Image, a []string) error {
			if err := wantArgs(a, 3, 4); err != nil {
				return err
			}
			v, err := argFloats(a)
			if err != nil {
				return err
			}
			var o ModulateOptions
			if len(v) == 4 {
				o.Lightness = v[3]
			}
			m.Modulate(v[0], v[1], v[2], o)
			return nil
		}},
	"boxblur": {Name: "boxblur", Usage: "RADIUS", Help: "Box blur: each pixel the mean of the square of side 2*RADIUS+1 around it, as Pillow's BoxBlur",
		builtIn: func(m *Image, a []string) error {
			if err := wantArgs(a, 1, 1); err != nil {
				return err
			}
			v, err := argFloats(a)
			if err != nil {
				return err
			}
			m.BoxBlur(v[0])
			return nil
		}},
	"blur": {Name: "blur", Usage: "SIGMA[,libvips[,MIN_AMPLITUDE]]", Help: "Blur: Gaussian, sigma in pixels, as Pillow's; libvips: as sharp's, cut off below MIN_AMPLITUDE of its peak (0.2 by default)",
		builtIn: func(m *Image, a []string) error {
			if err := wantArgs(a, 1, 3); err != nil {
				return err
			}
			if len(a) >= 2 && a[1] != "libvips" {
				return errors.New("the word after SIGMA is libvips")
			}
			nums := a[:1:1]
			if len(a) == 3 {
				nums = append(nums, a[2])
			}
			v, err := argFloats(nums)
			if err != nil {
				return err
			}
			var o BlurOptions
			if len(a) >= 2 {
				o.Method = BlurLibvips
			}
			if len(v) == 2 {
				o.MinAmplitude = v[1]
			}
			m.Blur(v[0], o)
			return nil
		}},
	"sharpen": {Name: "sharpen", Usage: "[SIGMA[,AMOUNT[,THRESHOLD]]] or SIGMA,libvips[,FLAT,JAGGED,JAGGED_FROM,MAX_BRIGHTEN,MAX_DARKEN]",
		Help: "Sharpen: mild without arguments, else an unsharp mask (Pillow's), or sharp's on the lightness",
		builtIn: func(m *Image, a []string) error {
			if len(a) >= 2 && a[1] == "libvips" {
				if len(a) != 2 && len(a) != 7 {
					return errors.New("want SIGMA,libvips, then FLAT,JAGGED,JAGGED_FROM,MAX_BRIGHTEN,MAX_DARKEN")
				}
				v, err := argFloats(append(a[:1:1], a[2:]...))
				if err != nil {
					return err
				}
				o := SharpenOptions{Sigma: v[0], Method: SharpenLibvips}
				if len(v) == 6 {
					o.Curve = &SharpenCurve{Flat: v[1], Jagged: v[2], JaggedFrom: v[3], MaxBrighten: v[4], MaxDarken: v[5]}
				}
				m.Sharpen(o)
				return nil
			}
			if err := wantArgs(a, 0, 3); err != nil {
				return err
			}
			if len(a) == 0 {
				m.Sharpen()
				return nil
			}
			v, err := argFloats(a)
			if err != nil {
				return err
			}
			o := SharpenOptions{Sigma: v[0]}
			if len(v) > 1 {
				o.Amount = v[1]
			}
			if len(v) > 2 {
				if v[2] != math.Trunc(v[2]) {
					return fmt.Errorf("the threshold is a whole number, got %v", v[2])
				}
				o.Threshold = int(v[2])
			}
			m.Sharpen(o)
			return nil
		}},
	"flatten": {Name: "flatten", Usage: "COLOR", Help: "Flatten: lay the image on a colour",
		builtIn: func(m *Image, a []string) error {
			if err := wantArgs(a, 1, 1); err != nil {
				return err
			}
			c, err := ParseColor(a[0])
			if err != nil {
				return err
			}
			m.Flatten(c)
			return nil
		}},
}

func init() {
	for name, d := range builtInOps {
		d.New = builtInNew(name, d.builtIn)
		builtInOps[name] = d
	}
}

// builtInNew makes the New of a built-in operation: the step the method
// adds (each adds one), as an Operation.
func builtInNew(name string, add func(m *Image, args []string) error) func(args ...string) (Operation, error) {
	return func(args ...string) (Operation, error) {
		m := &Image{}
		if err := add(m, args); err != nil {
			return nil, fmt.Errorf("goimgtools: %s: %w", name, err)
		}
		if m.err != nil {
			return nil, m.err
		}
		if len(m.chain) != 1 {
			return nil, fmt.Errorf("goimgtools: %s made %d steps", name, len(m.chain))
		}
		return builtInOp{m.chain[0]}, nil
	}
}

// builtInOp is a built-in operation made by its New.
type builtInOp struct{ op operation }

func (b builtInOp) Apply(src pipeline.Source) (pipeline.Source, error) {
	r := &run{exifOrient: pipeline.ExifOrientation(pipeline.MetaOf(src).Exif)}
	return applyOne(b.op, src, r, preShrunk{})
}

func isResize(op operation) bool {
	_, ok := op.(resizeOp)
	return ok
}
