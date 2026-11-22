// Package colorfont reads the colour glyphs of a font made of layers: the
// COLR table's version 0 records (also in fonts of version 1), each glyph
// a stack of other glyphs, each in a colour of the first palette of CPAL,
// as the colour emoji of Windows (Segoe UI Emoji) are made.
package colorfont

import (
	"encoding/binary"
	"errors"
	"image/color"
	"io"
	"sort"

	"github.com/whhhh1500/goimgtools/internal/otdir"
)

// MaxBytes is the most COLR and CPAL may hold (Segoe UI Emoji: 93KB).
const MaxBytes = 8 << 20

// Font is the colour layers of a font.
type Font struct {
	colr               []byte
	baseOff, layerOff  int
	numBase, numLayers int
	palette            []color.NRGBA
	Bytes              int64 // the memory it holds
}

// Layer is a layer of a colour glyph: a glyph, drawn in a colour of the
// palette or, Foreground, in the text's own.
type Layer struct {
	Glyph      uint16
	Color      color.NRGBA
	Foreground bool
}

// Open reads the colour layers of font number index of a font or
// collection file: nil and no error for a font without them.
func Open(r io.ReaderAt, index int) (*Font, error) {
	dir, err := otdir.Read(r, index)
	if err != nil {
		return nil, err
	}
	if _, ok := dir.Tables["COLR"]; !ok {
		return nil, nil
	}
	colr, err := dir.ReadTable(r, "COLR", MaxBytes)
	if err != nil {
		return nil, err
	}
	cpal, err := dir.ReadTable(r, "CPAL", MaxBytes)
	if err != nil || cpal == nil {
		return nil, err
	}
	if len(colr) < 14 || len(cpal) < 12 {
		return nil, errors.New("colorfont: COLR or CPAL too short")
	}
	be := binary.BigEndian
	f := &Font{
		colr:      colr,
		numBase:   int(be.Uint16(colr[2:])),
		baseOff:   int(be.Uint32(colr[4:])),
		layerOff:  int(be.Uint32(colr[8:])),
		numLayers: int(be.Uint16(colr[12:])),
	}
	if f.numBase == 0 {
		return nil, nil // colour glyphs of version 1 only
	}
	if f.baseOff+6*f.numBase > len(colr) || f.layerOff+4*f.numLayers > len(colr) {
		return nil, errors.New("colorfont: COLR records beyond the table")
	}
	entries := int(be.Uint16(cpal[2:]))
	numPalettes := int(be.Uint16(cpal[4:]))
	numRecords := int(be.Uint16(cpal[6:]))
	recordsOff := int(be.Uint32(cpal[8:]))
	if numPalettes == 0 || 12+2*numPalettes > len(cpal) || recordsOff+4*numRecords > len(cpal) {
		return nil, errors.New("colorfont: CPAL records beyond the table")
	}
	first := int(be.Uint16(cpal[12:]))
	if first+entries > numRecords {
		return nil, errors.New("colorfont: CPAL palette beyond its records")
	}
	f.palette = make([]color.NRGBA, entries)
	for i := range f.palette {
		c := cpal[recordsOff+4*(first+i):]
		f.palette[i] = color.NRGBA{R: c[2], G: c[1], B: c[0], A: c[3]} // stored blue, green, red, alpha
	}
	f.Bytes = int64(len(colr)) + 4*int64(entries)
	return f, nil
}

// Layers appends the layers of glyph g to buf, bottom first; none for a
// glyph that is not a colour one.
func (f *Font) Layers(g uint16, buf []Layer) []Layer {
	if f == nil {
		return buf
	}
	be := binary.BigEndian
	rec := func(i int) []byte { return f.colr[f.baseOff+6*i:] }
	i := sort.Search(f.numBase, func(i int) bool { return be.Uint16(rec(i)) >= g })
	if i == f.numBase || be.Uint16(rec(i)) != g {
		return buf
	}
	firstLayer, n := int(be.Uint16(rec(i)[2:])), int(be.Uint16(rec(i)[4:]))
	if firstLayer+n > f.numLayers {
		return buf
	}
	for k := firstLayer; k < firstLayer+n; k++ {
		l := f.colr[f.layerOff+4*k:]
		layer := Layer{Glyph: be.Uint16(l)}
		if p := int(be.Uint16(l[2:])); p == 0xFFFF {
			layer.Foreground = true
		} else if p < len(f.palette) {
			layer.Color = f.palette[p]
		} else {
			continue
		}
		buf = append(buf, layer)
	}
	return buf
}
