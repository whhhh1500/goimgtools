package textsrc

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"io"
	"math"
	"strings"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
	"golang.org/x/image/vector"

	"github.com/whhhh1500/goimgtools/internal/bidi"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// Align is how lines of different widths are lined up.
type Align int

const (
	AlignLeft Align = iota
	AlignCenter
	AlignRight
)

// Options describe the text image.
type Options struct {
	Fonts    []string // files or installed font names, tried in order
	FontData [][]byte // fonts held in memory, tried after Fonts
	// Size is the height of the text in pixels (of the font's em square).
	Size        float64
	Color       color.Color // of the text
	Background  color.Color // behind it; nil is transparent
	Width       int         // wrap lines at this many pixels; 0 never wraps
	Align       Align
	LineSpacing float64 // line height as a multiple of the font's own; 0 is 1
	Padding     int     // pixels left empty around the text

	// StrokeWidth draws an outline this many pixels wide around every
	// character, in StrokeColor (black when nil), under the text itself.
	StrokeWidth float64
	StrokeColor color.Color
	// ShadowColor, when set, draws a shadow of the text (and its outline)
	// moved by ShadowX, ShadowY pixels and softened by ShadowBlur.
	ShadowColor      color.Color
	ShadowX, ShadowY int
	ShadowBlur       int
}

// MaxSide is the largest width or height a text image may have.
const MaxSide = 16383

type glyph struct {
	font int
	r    rune
	x    fixed.Int26_6 // from the start of the line
	sh   *shaped       // of shaped text: the cluster's glyphs; nil laid out by itself
}

type line struct {
	glyphs []glyph
	width  fixed.Int26_6
}

type source struct {
	env    *pipeline.Env
	set    *Set
	faces  []font.Face
	lines  []line
	w, h   int
	lineH  int
	ascent int // from the top of a line to its baseline
	pad    int
	align  Align
	fg, bg *image.Uniform
	opaque bool
	next   int // next line

	// win holds the rows from base on. A line with an outline or a shadow
	// reaches m rows into its neighbours, so rows are handed out only once
	// no later line can touch them, and the window then moves down.
	win       *image.RGBA
	winH      int
	base      int // the row of the image that win's first row is
	emitted   int // rows handed out by the last Next, dropped by the next
	m         int // how far an outline or shadow reaches beyond its line
	stroke    float64
	strokeC   *image.Uniform
	shadowC   *image.Uniform
	shadowOff image.Point
	shadowR   int
	maskA     *image.Alpha // the line's shape
	maskB     *image.Alpha // its outline or its shadow
	size      float64
	gmask     image.Alpha // a glyph of shaped text, drawn
	grast     *vector.Rasterizer
	tmp       []uint8
	reserved  int64
	closed    bool
}

// New lays text out and returns it as a Source of RGBA strips, one line of
// text per strip, so its memory is one line however long the text is.
//
// Fonts are read from their files as glyphs are needed, never whole; every
// open font and the buffers glyphs are drawn in are counted in env's
// budget.
func New(text string, o Options, env *pipeline.Env) (pipeline.Source, error) {
	if env == nil {
		env = pipeline.NewEnv(0, "")
	}
	if o.Size == 0 {
		o.Size = 32
	}
	if !(o.Size >= 1 && o.Size <= 4096) {
		return nil, errors.New("goimgtools: text size must be 1 to 4096 pixels")
	}
	if o.LineSpacing == 0 {
		o.LineSpacing = 1
	}
	if !(o.LineSpacing >= 0.5 && o.LineSpacing <= 10) {
		return nil, errors.New("goimgtools: line spacing must be 0.5 to 10")
	}
	if o.Padding < 0 || o.Width < 0 || o.Padding > MaxSide || o.Width > MaxSide {
		return nil, errors.New("goimgtools: text padding and width must be 0 to 16383")
	}
	if !(o.StrokeWidth >= 0 && o.StrokeWidth <= 200) || o.ShadowBlur < 0 || o.ShadowBlur > 200 ||
		o.ShadowX < -1000 || o.ShadowX > 1000 || o.ShadowY < -1000 || o.ShadowY > 1000 {
		return nil, errors.New("goimgtools: text outline must be 0 to 200 pixels, shadow blur 0 to 200, shadow offset within 1000")
	}
	if o.Align < AlignLeft || o.Align > AlignRight {
		return nil, errors.New("goimgtools: unknown text alignment")
	}
	if strings.TrimSpace(text) == "" {
		return nil, errors.New("goimgtools: there is no text to draw")
	}
	s := &source{env: env, align: o.Align, stroke: o.StrokeWidth, size: o.Size}
	// How far the effects reach. The image grows by that much on every
	// side, so nothing of them is cut off.
	s.m = int(math.Ceil(o.StrokeWidth))
	if o.ShadowColor != nil {
		s.shadowC = image.NewUniform(o.ShadowColor)
		s.shadowOff, s.shadowR = image.Pt(o.ShadowX, o.ShadowY), o.ShadowBlur
		// The blur runs twice, so it spreads twice its radius; the shadow
		// is of the outlined text.
		reach := max(o.ShadowX, -o.ShadowX, o.ShadowY, -o.ShadowY) + 2*o.ShadowBlur + int(math.Ceil(o.StrokeWidth))
		s.m = max(s.m, reach)
	}
	if o.StrokeWidth > 0 {
		var c color.Color = color.Black
		if o.StrokeColor != nil {
			c = o.StrokeColor
		}
		s.strokeC = image.NewUniform(c)
	}
	s.pad = o.Padding + s.m
	ok := false
	defer func() {
		if !ok {
			s.Close()
		}
	}()
	reserve := func(n int64) error {
		// A font, plus the buffers its glyphs are drawn in: a coverage
		// accumulator and a mask the size of the largest glyph, which grow
		// with the square of the text size (measured: 5.5MB at 1000 pixels).
		n += int64(6 * o.Size * o.Size)
		if err := env.Budget.Reserve(n, "font"); err != nil {
			return err
		}
		s.reserved += n
		return nil
	}
	set, err := Load(o.Fonts, o.FontData, reserve)
	if err != nil {
		return nil, err
	}
	s.set = set
	if err := s.layout(text, o); err != nil {
		return nil, err
	}

	var fg color.Color = color.Black
	if o.Color != nil {
		fg = o.Color
	}
	var bg color.Color = color.Transparent
	if o.Background != nil {
		bg = o.Background
	}
	s.fg, s.bg = image.NewUniform(fg), image.NewUniform(bg)
	_, _, _, a := bg.RGBA()
	s.opaque = a == 0xffff

	s.winH = s.lineH + 2*s.pad
	need := int64(s.w) * 4 * int64(s.winH)
	effects := s.strokeC != nil || s.shadowC != nil
	maskH := s.lineH + 2*s.m
	if effects {
		need += 2*int64(s.w)*int64(maskH) + 3*int64(max(s.w, maskH))
	}
	if err := env.Budget.Reserve(need, "text lines"); err != nil {
		return nil, err
	}
	s.reserved += need
	s.win = image.NewRGBA(image.Rect(0, 0, s.w, s.winH))
	draw.Draw(s.win, s.win.Rect, s.bg, image.Point{}, draw.Src)
	if effects {
		s.maskA = image.NewAlpha(image.Rect(0, 0, s.w, maskH))
		s.maskB = image.NewAlpha(image.Rect(0, 0, s.w, maskH))
		s.tmp = make([]uint8, 3*max(s.w, maskH))
	}
	ok = true
	return s, nil
}

func (s *source) face(i int, size float64) (font.Face, error) {
	for len(s.faces) <= i {
		s.faces = append(s.faces, nil)
	}
	if s.faces[i] == nil {
		f, err := opentype.NewFace(s.set.fonts[i].f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingNone})
		if err != nil {
			return nil, fmt.Errorf("goimgtools: font %s: %w", s.set.fonts[i].Info.Full, err)
		}
		s.faces[i] = f
	}
	return s.faces[i], nil
}

// breakable reports whether a line may be broken before or after r: text
// in Chinese, Japanese and Korean is not divided into words by spaces.
func breakable(r rune) bool {
	return r >= 0x2E80 && (r <= 0xD7FF || (r >= 0xF900 && r <= 0xFAFF) || (r >= 0xFE30 && r <= 0xFE4F) || (r >= 0xFF00 && r <= 0xFFEF) || r >= 0x20000)
}

// layout places every character and sizes the image.
func (s *source) layout(text string, o Options) error {
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	text = strings.ReplaceAll(text, "\t", "    ")
	limit := fixed.Int26_6(math.MaxInt32)
	if o.Width > 0 {
		limit = fixed.I(max(1, o.Width-2*s.pad))
	}
	var ascent, descent fixed.Int26_6
	used := map[int]bool{}
	for _, para := range strings.Split(text, "\n") {
		var cur line
		var pen fixed.Int26_6
		prev, prevFont := rune(-1), -1
		brk, brkWidth := -1, fixed.Int26_6(0) // a place the line may end: glyph count, width there
		// The font of the hyphen a line broken at brk ends with (at a soft
		// hyphen); -1 for none.
		brkHyphen := -1
		// A paragraph with text of right-to-left scripts: its Arabic letters
		// joined, the levels of its characters resolved, each of its lines
		// put in the order it is seen. start is the character a line starts
		// at.
		rtl, base := false, false
		var levels []int8
		start := 0
		flush := func() {
			taken := len(cur.glyphs) // characters of the paragraph this line takes
			// Spaces at the end of a line take no room.
			for n := len(cur.glyphs); n > 0 && cur.glyphs[n-1].r == ' '; n-- {
				cur.glyphs = cur.glyphs[:n-1]
				cur.width = cur.glyphs0End(n-1, s)
			}
			if rtl {
				s.reorder(&cur, levels[start:start+len(cur.glyphs)], base)
			}
			start += taken
			s.lines = append(s.lines, cur)
			cur, pen, prev, prevFont, brk, brkHyphen = line{}, 0, -1, -1, -1, -1
		}
		// Control characters draw nothing and are left out, so that glyph
		// k of a line is always character start+k of the paragraph.
		runes := []rune(strings.Map(func(r rune) rune {
			if r < 0x20 || r == 0x7f {
				return -1
			}
			return r
		}, para))
		if rtl = bidi.HasRTL(runes); rtl {
			base = bidi.BaseRTL(runes)
			levels = bidi.Levels(runes, base)
		}
		// Text of scripts that join, reorder or stack is shaped by the
		// tables of its font.
		shapedAt, err := s.shapeParagraph(runes, levels, o.Size)
		if err != nil {
			return err
		}
		for i := 0; i < len(runes); i++ {
			r := runes[i]
			var sh *shaped
			if shapedAt != nil {
				sh = shapedAt[i]
			}
			if sh == nil && bidi.Ignorable(r) {
				// Drawn as nothing and taking no room, in no font of its own;
				// kept for the order of the line (marks of direction).
				fi := max(prevFont, 0)
				if _, err := s.face(fi, o.Size); err != nil {
					return err
				}
				cur.glyphs = append(cur.glyphs, glyph{font: fi, r: r, x: pen})
				switch r {
				case 0x200B: // zero width space: a place to break
					brk, brkWidth, brkHyphen = len(cur.glyphs), pen, -1
				case 0x00AD: // soft hyphen: a place to break, with a hyphen
					hf, adv, err := s.hyphen(fi, o.Size)
					if err != nil {
						return err
					}
					if pen+adv <= limit {
						brk, brkWidth, brkHyphen = len(cur.glyphs), pen+adv, hf
					}
				}
				continue
			}
			var fi int
			if sh != nil {
				fi = sh.font
			} else if fi, err = s.set.pick(r); err != nil {
				return err
			}
			face, err := s.face(fi, o.Size)
			if err != nil {
				return err
			}
			if !used[fi] {
				used[fi] = true
				m := face.Metrics()
				ascent, descent = max(ascent, m.Ascent), max(descent, m.Descent)
			}
			x := pen
			var adv fixed.Int26_6
			if sh != nil {
				// A cluster of shaped text: its first character carries it,
				// kerned by the font's tables.
				adv = sh.adv
			} else {
				adv, _ = face.GlyphAdvance(r)
				if prevFont == fi {
					x += face.Kern(prev, r)
				}
			}
			if x+adv > limit && len(cur.glyphs) > 0 && r != ' ' && (sh == nil || sh.head) {
				// The line is full. End it at the last allowed place, and
				// lay the characters after that place out again.
				if brk > 0 {
					back := len(cur.glyphs) - brk
					cur.glyphs, cur.width = cur.glyphs[:brk], brkWidth
					i -= back
					if brkHyphen >= 0 {
						cur.glyphs[brk-1].font, cur.glyphs[brk-1].r = brkHyphen, '-'
						if !used[brkHyphen] {
							used[brkHyphen] = true
							m := s.faces[brkHyphen].Metrics()
							ascent, descent = max(ascent, m.Ascent), max(descent, m.Descent)
						}
					}
				}
				flush()
				i--
				continue
			}
			if sh == nil && breakable(r) && len(cur.glyphs) > 0 {
				brk, brkWidth, brkHyphen = len(cur.glyphs), pen, -1
			}
			cur.glyphs = append(cur.glyphs, glyph{font: fi, r: r, x: x, sh: sh})
			pen = x + adv
			cur.width = pen
			prev, prevFont = r, fi
			if sh != nil {
				prev, prevFont = -1, -1 // no kerning but the tables'
			} else if r == ' ' || breakable(r) {
				brk, brkWidth, brkHyphen = len(cur.glyphs), pen, -1
			}
		}
		flush()
	}
	if len(used) == 0 { // only control characters
		return errors.New("goimgtools: there is no text to draw")
	}
	var widest fixed.Int26_6
	for _, l := range s.lines {
		widest = max(widest, l.width)
	}
	s.w = widest.Ceil() + 2*s.pad
	if o.Width > 0 {
		s.w = max(o.Width, s.w)
	}
	s.w = max(s.w, 1)
	natural := (ascent + descent).Ceil()
	s.lineH = max(1, int(math.Ceil(float64(natural)*o.LineSpacing)))
	s.ascent = (s.lineH-natural)/2 + ascent.Ceil()
	s.h = len(s.lines)*s.lineH + 2*s.pad
	if s.w > MaxSide || s.h > MaxSide {
		return fmt.Errorf("goimgtools: the text image would be %dx%d, more than %d pixels a side", s.w, s.h, MaxSide)
	}
	return nil
}

// reorder puts the glyphs of a line in the order they are seen (lv: the
// levels of its characters; rtl: of a paragraph that runs right to left),
// and places them again.
func (s *source) reorder(l *line, lv []int8, rtl bool) {
	rs := make([]rune, len(l.glyphs))
	for i, g := range l.glyphs {
		rs[i] = g.r
	}
	order, mirrored := bidi.Reorder(rs, lv, rtl)
	out := make([]glyph, len(order))
	var pen fixed.Int26_6
	prev, prevFont := rune(-1), -1
	for i, k := range order {
		g := l.glyphs[k]
		if g.sh != nil {
			// Shaped text: its clusters come in the order they are seen,
			// their glyphs within them too (and mirrored) as the shaping
			// gave them.
			g.x = pen
			pen += g.sh.adv
			prev, prevFont = -1, -1
			out[i] = g
			continue
		}
		// A mirrored glyph its font does not have stays as it is.
		if m := mirrored[k]; m != g.r && s.set.fonts[g.font].has(&s.set.buf, m) {
			g.r = m
		}
		if bidi.Ignorable(g.r) {
			g.x = pen
			out[i] = g
			continue
		}
		face := s.faces[g.font]
		x := pen
		if prevFont == g.font {
			x += face.Kern(prev, g.r)
		}
		g.x = x
		adv, _ := face.GlyphAdvance(g.r)
		pen = x + adv
		prev, prevFont = g.r, g.font
		out[i] = g
	}
	l.glyphs, l.width = out, pen
}

// hyphen is the font of the hyphen shown where a line breaks at a soft
// hyphen (that of the text before it when it has one), and its advance.
func (s *source) hyphen(fi int, size float64) (int, fixed.Int26_6, error) {
	if !s.set.fonts[fi].has(&s.set.buf, '-') {
		var err error
		if fi, err = s.set.pick('-'); err != nil {
			return 0, 0, err
		}
	}
	face, err := s.face(fi, size)
	if err != nil {
		return 0, 0, err
	}
	adv, _ := face.GlyphAdvance('-')
	return fi, adv, nil
}

// glyphs0End is the width of the line's first n glyphs.
func (l line) glyphs0End(n int, s *source) fixed.Int26_6 {
	if n == 0 {
		return 0
	}
	g := l.glyphs[n-1]
	if g.sh != nil {
		return g.x + g.sh.adv
	}
	if bidi.Ignorable(g.r) {
		return g.x
	}
	adv, _ := s.faces[g.font].GlyphAdvance(g.r)
	return g.x + adv
}

func (s *source) Env() *pipeline.Env      { return s.env }
func (s *source) Bounds() image.Rectangle { return image.Rect(0, 0, s.w, s.h) }
func (s *source) ColorModel() color.Model { return color.RGBAModel }
func (s *source) Opaque() bool            { return s.opaque }

func (s *source) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	for _, f := range s.faces {
		if f != nil {
			f.Close()
		}
	}
	if s.set != nil {
		s.set.Close(nil)
	}
	s.env.Budget.Release(s.reserved)
	s.reserved, s.win, s.maskA, s.maskB = 0, nil, nil, nil
	return nil
}

func (s *source) Next() (image.Image, error) {
	if s.closed {
		return nil, errors.New("goimgtools: text source closed")
	}
	if s.emitted > 0 {
		// Move the window down past the rows handed out last time.
		n := s.emitted * s.win.Stride
		copy(s.win.Pix, s.win.Pix[n:])
		tail := image.Rect(0, s.winH-s.emitted, s.w, s.winH)
		draw.Draw(s.win, tail, s.bg, image.Point{}, draw.Src)
		s.base += s.emitted
		s.emitted = 0
	}
	for s.base < s.h {
		if s.next < len(s.lines) {
			s.drawLine(s.next)
			s.next++
		}
		// Rows above the reach of the next line are final.
		end := s.h
		if s.next < len(s.lines) {
			end = s.pad + s.next*s.lineH - s.m
		}
		if end > s.base {
			out := *s.win
			out.Rect = image.Rect(0, s.base, s.w, end)
			s.emitted = end - s.base
			return &out, nil
		}
	}
	return nil, io.EOF
}

// drawLine draws line i into the window: its shadow, its outline, then the
// text itself.
func (s *source) drawLine(i int) {
	top := s.pad + i*s.lineH
	l := s.lines[i]
	room := fixed.I(s.w-2*s.pad) - l.width
	start := fixed.I(s.pad)
	switch s.align {
	case AlignCenter:
		start += room / 2
	case AlignRight:
		start += room
	}
	glyphs := func(dst draw.Image, src image.Image) {
		d := font.Drawer{Dst: dst, Src: src}
		for _, g := range l.glyphs {
			if g.sh != nil {
				for _, sg := range g.sh.glyphs {
					dot := fixed.Point26_6{X: start + g.x + sg.dx, Y: fixed.I(top+s.ascent) + sg.dy}
					s.drawGlyph(dst, src, g.font, sg.id, dot, s.size)
				}
				continue
			}
			if g.r == ' ' || bidi.Ignorable(g.r) {
				continue
			}
			d.Face = s.faces[g.font]
			d.Dot = fixed.Point26_6{X: start + g.x, Y: fixed.I(top + s.ascent)}
			d.DrawString(string(g.r))
		}
	}
	// The window, in the coordinates of the image.
	win := *s.win
	win.Rect = image.Rect(0, s.base, s.w, s.base+s.winH)
	if s.maskA == nil {
		glyphs(&win, s.fg)
		return
	}
	// The shape of the line, with room for the effects above and below.
	area := image.Rect(0, top-s.m, s.w, top+s.lineH+s.m)
	shape, other := *s.maskA, *s.maskB
	shape.Rect, other.Rect = area, area
	clear(shape.Pix)
	glyphs(&shape, image.Opaque)
	paint := func(c *image.Uniform, mask *image.Alpha, off image.Point) {
		r := area.Add(off).Intersect(win.Rect)
		draw.DrawMask(&win, r, c, image.Point{}, mask, r.Min.Sub(off), draw.Over)
	}
	outlined := &shape
	if s.strokeC != nil {
		dilate(&other, &shape, s.stroke, s.tmp)
		outlined = &other
	}
	if s.shadowC != nil {
		// The shadow is cast by the outlined text. It is painted from a
		// copy, softened, that then makes way for the outline again.
		shadow := outlined
		if s.shadowR > 0 {
			if outlined == &shape {
				copy(other.Pix, shape.Pix)
			}
			blur(&other, s.shadowR, s.tmp)
			shadow = &other
		}
		paint(s.shadowC, shadow, s.shadowOff)
		if s.strokeC != nil && s.shadowR > 0 {
			dilate(&other, &shape, s.stroke, s.tmp)
		}
	}
	if s.strokeC != nil {
		paint(s.strokeC, &other, image.Point{})
	}
	paint(s.fg, &shape, image.Point{})
}
