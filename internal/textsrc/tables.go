package textsrc

import (
	"bytes"
	"encoding/binary"
	"io"

	"golang.org/x/image/font/sfnt"

	"github.com/whhhh1500/goimgtools/internal/otdir"
)

// vmetrics is where a font's vertical metrics are, read the first time they
// are asked for.
type vmetrics struct {
	tried bool
	ok    bool
	long  int   // the glyphs with an advance of their own (numOfLongVerMetrics)
	at    int64 // where vmtx is in the file
	size  int64 // its length
}

// VerticalMetrics is how far glyph gi advances down a vertical line and its
// top side bearing (from the top of its advance to the top of its outline),
// in font units, as the font's vhea and vmtx tables give them; ok is false
// for a font without them.
func (f *Font) VerticalMetrics(gi sfnt.GlyphIndex) (advance, topBearing int, ok bool) {
	r := f.reader()
	if r == nil {
		return 0, 0, false
	}
	vm := &f.vm
	if !vm.tried {
		vm.tried = true
		dir, err := otdir.Read(r, f.Info.Index)
		if err != nil {
			return 0, 0, false
		}
		vhea, ok1 := dir.Tables["vhea"]
		vmtx, ok2 := dir.Tables["vmtx"]
		if !ok1 || !ok2 || vhea.Length < 36 || vmtx.Length < 4 {
			return 0, 0, false
		}
		var b [2]byte
		if _, err := r.ReadAt(b[:], vhea.Offset+34); err != nil {
			return 0, 0, false
		}
		vm.long = int(binary.BigEndian.Uint16(b[:]))
		vm.at, vm.size = vmtx.Offset, vmtx.Length
		vm.ok = vm.long > 0 && int64(4*vm.long) <= vm.size
	}
	if !vm.ok {
		return 0, 0, false
	}
	var b [4]byte
	i := int64(min(int(gi), vm.long-1))
	if _, err := r.ReadAt(b[:], vm.at+4*i); err != nil {
		return 0, 0, false
	}
	advance = int(binary.BigEndian.Uint16(b[:]))
	topBearing = int(int16(binary.BigEndian.Uint16(b[2:])))
	if int(gi) >= vm.long {
		// Past the long metrics, the bearings alone, after them.
		o := 4*int64(vm.long) + 2*(int64(gi)-int64(vm.long))
		if o+2 > vm.size {
			return advance, 0, true
		}
		if _, err := r.ReadAt(b[:2], vm.at+o); err != nil {
			return advance, 0, true
		}
		topBearing = int(int16(binary.BigEndian.Uint16(b[:2])))
	}
	return advance, topBearing, true
}

// reader is where the font's bytes are read from.
func (f *Font) reader() io.ReaderAt {
	switch {
	case f.file != nil:
		return f.file
	case f.data != nil:
		return bytes.NewReader(f.data)
	}
	return nil
}

// Variable reports whether the font has variations (an fvar table), from
// its table directory, read the first time it is asked.
func (f *Font) Variable() bool {
	if !f.varTried {
		f.varTried = true
		if r := f.reader(); r != nil {
			if dir, err := otdir.Read(r, f.Info.Index); err == nil {
				_, f.variable = dir.Tables["fvar"]
			}
		}
	}
	return f.variable
}
