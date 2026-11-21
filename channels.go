package goimgtools

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
	"math"
	"strconv"

	"github.com/whhhh1500/goimgtools/internal/ops"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// The operations on channels, after sharp's: Recomb, Unflatten,
// ExtractChannel, JoinChannel and RemoveAlpha. They work a few rows at a time.

// Recomb mixes the channels of every pixel by a matrix, as sharp's recomb
// (to the byte): each row of matrix makes one channel of the result, the
// sum of the red, green and blue levels (and alpha, for a matrix of 4
// rows of 4) times the numbers of the row, kept within 0 to 255 with its
// fraction dropped. A matrix of 3 rows of 3 keeps transparency; the
// fourth row of a 4 x 4 matrix makes the alpha. Sepia:
//
//	m.Recomb([][]float64{
//		{0.393, 0.769, 0.189},
//		{0.349, 0.686, 0.168},
//		{0.272, 0.534, 0.131},
//	})
func (m *Image) Recomb(matrix [][]float64) *Image {
	n := len(matrix)
	if n != 3 && n != 4 {
		return m.fail(errors.New("goimgtools: Recomb takes 3 rows of 3 numbers or 4 rows of 4"))
	}
	var o recombOp
	o.alpha = n == 4
	for i, row := range matrix {
		if len(row) != n {
			return m.fail(errors.New("goimgtools: Recomb takes 3 rows of 3 numbers or 4 rows of 4"))
		}
		for j, v := range row {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return m.fail(errors.New("goimgtools: Recomb takes finite numbers"))
			}
			o.m[i][j] = v
		}
	}
	return m.add(o)
}

// Unflatten makes the white pixels transparent, as sharp's unflatten (to
// the byte): those whose grey is 255 (libvips' greyscale, by which a few
// colours a level or two off white, such as 255,255,254, are white too).
// The other pixels keep their transparency. After Threshold, every pixel
// at or above the threshold becomes transparent.
func (m *Image) Unflatten() *Image { return m.add(channelOp{name: "unflatten"}) }

// RemoveAlpha makes every pixel opaque, showing the colour it stores, as
// sharp's removeAlpha. Unlike Flatten nothing is blended with a
// background: what transparency hid (often black) shows.
func (m *Image) RemoveAlpha() *Image { return m.add(channelOp{name: "remove-alpha"}) }

// EnsureAlpha gives an image without transparency an alpha channel of the
// given opacity, 0 (clear) to 1 (opaque), as sharp's ensureAlpha, to the
// byte: alpha·255 with its fraction dropped (0.5 is 127). An image whose
// format can hold transparency keeps the transparency it has, as with
// sharp (Flatten or RemoveAlpha first to replace it).
func (m *Image) EnsureAlpha(alpha float64) *Image {
	if !(alpha >= 0 && alpha <= 1) {
		return m.fail(errors.New("goimgtools: EnsureAlpha alpha must be 0 to 1"))
	}
	return m.add(channelOp{name: "ensure-alpha", alpha: alpha})
}

// Channel is a channel of the pixels of an image, for ExtractChannel.
type Channel int

const (
	ChannelRed Channel = iota
	ChannelGreen
	ChannelBlue
	// ChannelAlpha is the opacity: 0 transparent, 255 opaque.
	ChannelAlpha
)

// ExtractChannel makes a greyscale image of one channel, as sharp's
// extractChannel: its levels become the grey levels (the alpha of an
// image without transparency is white throughout). The result has no
// transparency.
func (m *Image) ExtractChannel(c Channel) *Image {
	if c < ChannelRed || c > ChannelAlpha {
		return m.fail(errors.New("goimgtools: unknown Channel"))
	}
	return m.add(channelOp{name: "extract-channel", ch: c})
}

// ---- the operations ----

type recombOp struct {
	m     [4][4]float64
	alpha bool
}

func (o recombOp) apply(src pipeline.Source, r *run) (pipeline.Source, error) {
	if r.sizeOnly {
		return src, nil
	}
	return ops.Recomb(src, o.m, o.alpha), nil
}

func (o recombOp) words() []string {
	n := 3
	if o.alpha {
		n = 4
	}
	var w []string
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			w = append(w, strconv.FormatFloat(o.m[i][j], 'g', -1, 64))
		}
	}
	return w
}

// channelOp is Unflatten, RemoveAlpha or ExtractChannel.
type channelOp struct {
	name  string
	ch    Channel
	alpha float64 // for ensure-alpha
}

func (o channelOp) apply(src pipeline.Source, r *run) (pipeline.Source, error) {
	if r.sizeOnly {
		return src, nil
	}
	switch o.name {
	case "unflatten":
		return ops.Unflatten(src), nil
	case "remove-alpha":
		return ops.RemoveAlpha(src), nil
	case "ensure-alpha":
		return ops.EnsureAlpha(src, uint8(o.alpha*255)), nil
	}
	return ops.Channel(src, int(o.ch)), nil
}

func (o channelOp) words() []string {
	switch o.name {
	case "extract-channel":
		return []string{channelNames[o.ch]}
	case "ensure-alpha":
		return []string{strconv.FormatFloat(o.alpha, 'g', -1, 64)}
	}
	return nil
}

var channelNames = map[Channel]string{ChannelRed: "red", ChannelGreen: "green", ChannelBlue: "blue", ChannelAlpha: "alpha"}

// channelOps are the named operations of this file (see builtInOps).
var channelOps = map[string]OperationDef{
	"recomb": {Name: "recomb", Usage: "M11,M12,M13,M21,...", Help: "Recomb: mix the channels by a matrix of 3 rows of 3 numbers (or 4 of 4, with alpha), row by row",
		builtIn: func(m *Image, a []string) error {
			if len(a) != 9 && len(a) != 16 {
				return errors.New("want 9 or 16 numbers")
			}
			v, err := argFloats(a)
			if err != nil {
				return err
			}
			n := 3
			if len(v) == 16 {
				n = 4
			}
			rows := make([][]float64, n)
			for i := range rows {
				rows[i] = v[i*n : i*n+n]
			}
			m.Recomb(rows)
			return nil
		}},
	"unflatten": {Name: "unflatten", Help: "Unflatten: white pixels become transparent",
		builtIn: func(m *Image, a []string) error {
			if err := wantArgs(a, 0, 0); err != nil {
				return err
			}
			m.Unflatten()
			return nil
		}},
	"remove-alpha": {Name: "remove-alpha", Help: "RemoveAlpha: every pixel opaque, showing the colour it stores",
		builtIn: func(m *Image, a []string) error {
			if err := wantArgs(a, 0, 0); err != nil {
				return err
			}
			m.RemoveAlpha()
			return nil
		}},
	"ensure-alpha": {Name: "ensure-alpha", Usage: "[ALPHA]", Help: "EnsureAlpha: an image without transparency gets the opacity ALPHA, 0 to 1 (1 by default)",
		builtIn: func(m *Image, a []string) error {
			if err := wantArgs(a, 0, 1); err != nil {
				return err
			}
			alpha := 1.0
			if len(a) == 1 {
				v, err := argFloats(a)
				if err != nil {
					return err
				}
				alpha = v[0]
			}
			m.EnsureAlpha(alpha)
			return nil
		}},
	"extract-channel": {Name: "extract-channel", Usage: "CHANNEL", Help: "ExtractChannel: a greyscale image of CHANNEL: red, green, blue or alpha",
		builtIn: func(m *Image, a []string) error {
			if err := wantArgs(a, 1, 1); err != nil {
				return err
			}
			c, err := pick(channelNames, a[0], "channel")
			if err != nil {
				return err
			}
			m.ExtractChannel(c)
			return nil
		}},
}

func init() {
	for n, d := range channelOps {
		builtInOps[n] = d
	}
}

// JoinChannel takes mask as the alpha channel of the image, as sharp's
// joinChannel adds a channel: the inverse of ExtractChannel(ChannelAlpha).
// mask must be the size of the image at that point of the chain (its
// grey, laid on black where it is transparent: a grey picture as it is,
// white where the image is to show); an image that has transparency of its
// own keeps it too (the two multiplied). Both are read a few rows at a
// time, side by side.
func (m *Image) JoinChannel(mask *Image) *Image {
	if mask == nil {
		return m.fail(errors.New("goimgtools: JoinChannel needs a mask"))
	}
	return m.add(joinOp{mask})
}

// joinOp is JoinChannel as a step of the chain.
type joinOp struct{ mask *Image }

func (j joinOp) apply(src pipeline.Source, r *run) (pipeline.Source, error) {
	if r.sizeOnly {
		return src, nil
	}
	env := pipeline.EnvOf(src)
	ms, closer, _, _, err := j.mask.build(env, false)
	if err != nil {
		return nil, fmt.Errorf("mask: %w", err)
	}
	b, mb := src.Bounds(), ms.Bounds()
	if b.Size() != mb.Size() {
		ms.Close()
		closer.Close()
		return nil, fmt.Errorf("goimgtools: JoinChannel: the mask is %dx%d, the image %dx%d", mb.Dx(), mb.Dy(), b.Dx(), b.Dy())
	}
	g := &maskRows{src: ms, closer: closer, env: env, w: b.Dx(), y: mb.Min.Y, dy: b.Min.Y - mb.Min.Y}
	out := pipeline.Map(pipeline.WithAlpha(src), func(s *image.NRGBA) error {
		w := s.Rect.Dx()
		for y := s.Rect.Min.Y; y < s.Rect.Max.Y; y++ {
			grey, err := g.row(y)
			if err != nil {
				return fmt.Errorf("mask: %w", err)
			}
			px := s.Pix[s.PixOffset(s.Rect.Min.X, y):][:4*w]
			for x, v := range grey[:w] {
				a := &px[4*x+3]
				*a = uint8((uint32(*a)*uint32(v) + 127) / 255)
			}
		}
		return nil
	})
	return &joinSource{Source: out, mask: g}, nil
}

// maskRows reads the mask a row at a time as grey levels, in step with
// the image.
type maskRows struct {
	src    pipeline.Source
	closer io.Closer
	env    *pipeline.Env
	w      int
	strip  image.Image // the mask's strip being read, its rows from y on
	y      int         // the next row of the mask
	dy     int         // image row minus mask row
	grey   []uint8     // a row: grey levels, or RGBA with rgba
	rgba   bool
	held   int64
	closed bool
}

// row returns the grey of the mask's row for image row y; rows come in
// order.
func (g *maskRows) row(y int) ([]uint8, error) {
	want := y - g.dy
	for g.strip == nil || want >= g.strip.Bounds().Max.Y {
		s, err := g.src.Next()
		if err != nil {
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return nil, err
		}
		g.strip = s
	}
	if g.grey == nil {
		n := g.w
		if g.rgba {
			n *= 4
		}
		if err := g.env.Budget.Reserve(int64(n), "mask row"); err != nil {
			return nil, err
		}
		g.held, g.grey = int64(n), make([]uint8, n)
	}
	sb := g.strip.Bounds()
	if g.rgba {
		// The pixels as non-premultiplied RGBA.
		for x := 0; x < g.w; x++ {
			c := color.NRGBAModel.Convert(g.strip.At(sb.Min.X+x, want)).(color.NRGBA)
			g.grey[4*x], g.grey[4*x+1], g.grey[4*x+2], g.grey[4*x+3] = c.R, c.G, c.B, c.A
		}
		return g.grey, nil
	}
	if p, ok := g.strip.(*image.Gray); ok {
		copy(g.grey, p.Pix[p.PixOffset(sb.Min.X, want):][:g.w])
		return g.grey, nil
	}
	for x := 0; x < g.w; x++ {
		// The grey of the colour laid on black.
		r, gr, b, _ := g.strip.At(sb.Min.X+x, want).RGBA()
		g.grey[x] = uint8((19595*r + 38470*gr + 7471*b + 1<<15) >> 24)
	}
	return g.grey, nil
}

func (g *maskRows) close() error {
	if g.closed {
		return nil
	}
	g.closed = true
	g.env.Budget.Release(g.held)
	err := g.src.Close()
	if cerr := g.closer.Close(); err == nil {
		err = cerr
	}
	return err
}

// joinSource closes the mask with the image.
type joinSource struct {
	pipeline.Source
	mask *maskRows
}

func (j *joinSource) Env() *pipeline.Env { return pipeline.EnvOf(j.Source) }
func (j *joinSource) Opaque() bool       { return pipeline.IsOpaque(j.Source) }

func (j *joinSource) Close() error {
	err := j.Source.Close()
	if merr := j.mask.close(); err == nil {
		err = merr
	}
	return err
}

// BoolOp is a bitwise operation of Boolean and Bandbool.
type BoolOp int

const (
	BoolAnd BoolOp = iota // bits set in both
	BoolOr                // bits set in either
	BoolEor               // bits set in one only (exclusive or)
)

func (op BoolOp) apply(a, b uint8) uint8 {
	switch op {
	case BoolAnd:
		return a & b
	case BoolOr:
		return a | b
	}
	return a ^ b
}

// Boolean sets the red, green and blue of every pixel to the bitwise
// operation op of them and those of the pixel of operand, an image of the
// same size, as sharp's boolean; the transparency stays the image's. Both
// are read a few rows at a time, side by side.
func (m *Image) Boolean(operand *Image, op BoolOp) *Image {
	if operand == nil {
		return m.fail(errors.New("goimgtools: Boolean needs an operand"))
	}
	if op < BoolAnd || op > BoolEor {
		return m.fail(errors.New("goimgtools: unknown Boolean operation"))
	}
	return m.add(booleanOp{operand, op})
}

// booleanOp is Boolean as a step of the chain.
type booleanOp struct {
	operand *Image
	op      BoolOp
}

func (o booleanOp) apply(src pipeline.Source, r *run) (pipeline.Source, error) {
	if r.sizeOnly {
		return src, nil
	}
	env := pipeline.EnvOf(src)
	os, closer, _, _, err := o.operand.build(env, false)
	if err != nil {
		return nil, fmt.Errorf("operand: %w", err)
	}
	b, ob := src.Bounds(), os.Bounds()
	if b.Size() != ob.Size() {
		os.Close()
		closer.Close()
		return nil, fmt.Errorf("goimgtools: Boolean: the operand is %dx%d, the image %dx%d", ob.Dx(), ob.Dy(), b.Dx(), b.Dy())
	}
	g := &maskRows{src: os, closer: closer, env: env, w: b.Dx(), y: ob.Min.Y, dy: b.Min.Y - ob.Min.Y, rgba: true}
	out := pipeline.Map(src, func(s *image.NRGBA) error {
		w := s.Rect.Dx()
		for y := s.Rect.Min.Y; y < s.Rect.Max.Y; y++ {
			row, err := g.row(y)
			if err != nil {
				return fmt.Errorf("operand: %w", err)
			}
			px := s.Pix[s.PixOffset(s.Rect.Min.X, y):][:4*w]
			for x := 0; x < w; x++ {
				for k := 0; k < 3; k++ {
					px[4*x+k] = o.op.apply(px[4*x+k], row[4*x+k])
				}
			}
		}
		return nil
	})
	return &joinSource{Source: out, mask: g}, nil
}

// Bandbool makes every pixel grey, the bitwise operation op of its red,
// green and blue (red op green op blue), as sharp's bandbool; the
// transparency stays.
func (m *Image) Bandbool(op BoolOp) *Image {
	if op < BoolAnd || op > BoolEor {
		return m.fail(errors.New("goimgtools: unknown Bandbool operation"))
	}
	return m.Pixels(func(s *image.NRGBA) error {
		for i := 0; i < len(s.Pix); i += 4 {
			v := op.apply(op.apply(s.Pix[i], s.Pix[i+1]), s.Pix[i+2])
			s.Pix[i], s.Pix[i+1], s.Pix[i+2] = v, v, v
		}
		return nil
	})
}
