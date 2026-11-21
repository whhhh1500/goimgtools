package goimgtools

import (
	"image/color"

	"github.com/whhhh1500/goimgtools/internal/textsrc"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// TextAlign is how lines of text of different widths are lined up.
type TextAlign int

const (
	TextLeft TextAlign = iota
	TextCenter
	TextRight
)

// TextOptions are the optional settings of Text.
type TextOptions struct {
	// Fonts are tried in order for every character, so a second font can
	// supply what the first lacks (for example a Latin font, then a Chinese
	// one). Each is a font file (.ttf, .otf, or .ttc with "#1" after the
	// name for the second font inside it), or the name of a font installed
	// on the system as SystemFonts lists it ("Microsoft YaHei", "SimHei").
	//
	// After these, or when there are none, come the fonts DefaultFonts
	// lists: a Chinese-capable system font where there is one, then the
	// built-in Go Regular font.
	Fonts []string
	// FontData are fonts already in memory, tried after Fonts.
	FontData [][]byte
	// Size is the text height in pixels; the default is 32.
	Size float64
	// Color is the colour of the text; the default is black. A colour with
	// transparency gives see-through text.
	Color color.Color
	// Background is what is behind the text; the default is transparent.
	Background color.Color
	// Width wraps lines at this many pixels (padding included). Zero never
	// wraps; a line break in the text always starts a new line.
	Width int
	Align TextAlign
	// LineSpacing is the line height as a multiple of the normal one; the
	// default is 1.
	LineSpacing float64
	// Padding is the empty margin around the text, in pixels.
	Padding int

	// StrokeWidth draws an outline this many pixels wide around every
	// character, in StrokeColor (black by default). Light text with a dark
	// outline stays readable on any picture.
	StrokeWidth float64
	StrokeColor color.Color
	// ShadowColor, when set, draws a shadow of the text behind it, moved
	// ShadowX pixels right and ShadowY down (negative: left, up) and
	// softened over ShadowBlur pixels. With no offset and some blur it is
	// a glow. The image grows by what outline and shadow need on every
	// side, so they are never cut off.
	ShadowColor      color.Color
	ShadowX, ShadowY int
	ShadowBlur       int
}

// Text makes an image of a piece of text. It is an input like Open: it can
// be resized, rotated and written out, or handed to Composite as a
// watermark.
//
//	goimgtools.Open("in.jpg").
//	    Composite(goimgtools.Text("© 2026 示例", goimgtools.TextOptions{Size: 48, Color: color.White}),
//	        goimgtools.CompositeOptions{Gravity: goimgtools.SouthEast, Left: -24, Top: -24}).
//	    ToFile("out.jpg")
//
// A font file is never loaded whole: only the glyphs the text uses are read
// from it, so a 20MB Chinese font costs about 0.1MB. Fallback fonts are
// opened only when a character needs them. What a font takes, and the
// buffers its glyphs are drawn in (they grow with the square of Size: about
// 6MB at 1000 pixels), count against the memory limit. The text is drawn a
// line at a time.
//
// Without an output format the result is written as PNG.
func Text(text string, opts ...TextOptions) *Image {
	var o TextOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	to := textsrc.Options{
		Fonts: o.Fonts, FontData: o.FontData, Size: o.Size, Color: o.Color, Background: o.Background,
		Width: o.Width, Align: textsrc.Align(o.Align), LineSpacing: o.LineSpacing, Padding: o.Padding,
		StrokeWidth: o.StrokeWidth, StrokeColor: o.StrokeColor,
		ShadowColor: o.ShadowColor, ShadowX: o.ShadowX, ShadowY: o.ShadowY, ShadowBlur: o.ShadowBlur,
	}
	m := &Image{open: func() (*input, error) {
		return &input{kind: "text", gen: func(env *pipeline.Env) (pipeline.Source, error) { return textsrc.New(text, to, env) }}, nil
	}}
	m.atMostOne("Text", len(opts))
	return m
}

// FontInfo describes a font SystemFonts or DefaultFonts found.
type FontInfo struct {
	// Name is how to ask for the font in TextOptions.Fonts: its full name,
	// such as "Microsoft YaHei Bold".
	Name   string
	Family string // "Microsoft YaHei"
	Style  string // "Regular", "Bold", ...
	// File is the font file, with "#1" appended for the second font of a
	// collection; it can be used in TextOptions.Fonts as well. Empty for
	// the built-in font.
	File string
	// Chinese reports whether the font has Chinese characters.
	Chinese bool
}

func fontInfos(in []textsrc.FontInfo) []FontInfo {
	out := make([]FontInfo, len(in))
	for i, f := range in {
		out[i] = FontInfo{Name: f.Full, Family: f.Family, Style: f.Style, Chinese: f.CJK}
		if f.Path != "" {
			out[i].File = f.Spec()
		}
	}
	return out
}

// SystemFonts lists the fonts installed on this system, sorted by name. It
// reads the system font directories (once per process), which takes a
// moment on a system with many fonts.
func SystemFonts() []FontInfo { return fontInfos(textsrc.System()) }

// DefaultFonts lists, in the order they are tried, the fonts Text uses for
// characters that TextOptions.Fonts does not cover, or for everything when
// no font is named: Chinese-capable fonts found in their usual places on
// this system, then fonts of symbols and other scripts found in theirs
// (opened only for a character the fonts before them lack), then the
// built-in Go Regular font (Latin, Greek and Cyrillic only). It does not
// read the other fonts of the system.
func DefaultFonts() []FontInfo { return fontInfos(textsrc.Defaults()) }
