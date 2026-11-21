package heic

// The HEIF boxes are read as github.com/gen2brain/h265 reads them
// (heic/isobmff.go and heic/grid.go, MIT licence, Copyright (c) 2025
// roticv, Copyright (c) 2026 Karpeles Lab Inc.; licenses/LICENSE-H265 here),
// from which this is taken, cut down to what decoding the primary image a
// row of tiles at a time needs (see stream.go). A file this reader cannot
// take is decoded whole by gen2brain/h265 instead.

import (
	"errors"
	"slices"
)

var errHEIF = errors.New("heic: invalid file")

type boxReader struct {
	b   []byte
	i   int
	err bool
}

func (r *boxReader) remaining() int {
	if r.err {
		return 0
	}
	return len(r.b) - r.i
}

func (r *boxReader) bytes(n int) []byte {
	if n < 0 || r.remaining() < n {
		r.err, r.i = true, len(r.b)
		return nil
	}
	v := r.b[r.i : r.i+n]
	r.i += n
	return v
}

func (r *boxReader) skip(n int) { r.bytes(n) }

func (r *boxReader) u8() uint8 {
	if b := r.bytes(1); b != nil {
		return b[0]
	}
	return 0
}

func (r *boxReader) uint(n int) uint64 {
	var v uint64
	for _, c := range r.bytes(n) {
		v = v<<8 | uint64(c)
	}
	return v
}

func (r *boxReader) u16() uint16 { return uint16(r.uint(2)) }
func (r *boxReader) u32() uint32 { return uint32(r.uint(4)) }

func (r *boxReader) str4() string { return string(r.bytes(4)) }

func (r *boxReader) cstr() string {
	for j := r.i; j < len(r.b); j++ {
		if r.b[j] == 0 {
			s := string(r.b[r.i:j])
			r.i = j + 1
			return s
		}
	}
	r.err, r.i = true, len(r.b)
	return ""
}

func (r *boxReader) fullBox() (uint8, uint32) {
	v := r.u32()
	return uint8(v >> 24), v & 0xffffff
}

// eachBox calls fn for every box in b.
func eachBox(b []byte, fn func(typ string, payload []byte) error) error {
	r := &boxReader{b: b}
	for r.remaining() >= 8 {
		start := r.i
		size := uint64(r.u32())
		typ := r.str4()
		hdr := 8
		switch size {
		case 1:
			size = r.uint(8)
			hdr = 16
		case 0:
			size = uint64(len(b) - start)
		}
		if r.err || size < uint64(hdr) || size > uint64(len(b)-start) {
			return errHEIF
		}
		if typ != "uuid" {
			if err := fn(typ, b[start+hdr:start+int(size)]); err != nil {
				return err
			}
		}
		r.i = start + int(size)
	}
	if r.err {
		return errHEIF
	}
	return nil
}

type heifExtent struct{ off, len uint64 }

type heifItem struct {
	id          uint32
	typ         string
	extents     []heifExtent
	method      uint8
	baseOffset  uint64
	props       []heifItemProp
	unsupported bool
}

type heifItemProp struct {
	idx       int
	essential bool
}

type heifRef struct {
	typ  string
	from uint32
	to   []uint32
}

type heifProp struct {
	typ  string
	w, h uint32
	hvcC *hvcConfig
	auxC string
	colr *heifColor
	clap [8]uint32
	// irot: anticlockwise quarter turns; imir: 0 flips top and bottom, 1
	// left and right.
	angle, axis uint8
}

// hvcConfig is the HEVCDecoderConfigurationRecord of ISO/IEC 14496-15.
type hvcConfig struct {
	chromaFormat   uint8
	bitDepthLuma   uint8
	bitDepthChroma uint8
	lengthSize     int
	paramSets      [][]byte
}

type heifColor struct {
	icc                         []byte
	primaries, transfer, matrix uint16
	fullRange, nclx             bool
}

type heif struct {
	data    []byte
	primary uint32
	items   map[uint32]*heifItem
	props   []heifProp
	refs    []heifRef
	idat    []byte
}

// parseHEIF reads the meta box of a file.
func parseHEIF(data []byte) (*heif, error) {
	var h *heif
	err := eachBox(data, func(typ string, b []byte) error {
		if typ != "meta" || h != nil {
			return nil
		}
		var err error
		h, err = parseHEIFMeta(b)
		return err
	})
	if err != nil {
		return nil, err
	}
	if h == nil {
		return nil, errHEIF
	}
	h.data = data
	return h, nil
}

func parseHEIFMeta(payload []byte) (*heif, error) {
	r := &boxReader{b: payload}
	r.fullBox()
	if r.err {
		return nil, errHEIF
	}
	h := &heif{items: map[uint32]*heifItem{}}
	err := eachBox(payload[r.i:], func(typ string, b []byte) error {
		switch typ {
		case "pitm":
			rr := &boxReader{b: b}
			if v, _ := rr.fullBox(); v == 0 {
				h.primary = uint32(rr.u16())
			} else {
				h.primary = rr.u32()
			}
			if rr.err {
				return errHEIF
			}
		case "iloc":
			return h.parseIloc(b)
		case "iinf":
			return h.parseIinf(b)
		case "iref":
			return h.parseIref(b)
		case "idat":
			h.idat = b
		case "iprp":
			return eachBox(b, func(typ string, b []byte) error {
				switch typ {
				case "ipco":
					return h.parseIpco(b)
				case "ipma":
					return h.parseIpma(b)
				}
				return nil
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	// An item with an essential property this reader does not know must
	// not be shown.
	known := map[string]bool{"ispe": true, "hvcC": true, "pixi": true, "auxC": true,
		"irot": true, "imir": true, "clap": true, "colr": true}
	for _, it := range h.items {
		for _, ip := range it.props {
			if ip.essential && (ip.idx >= len(h.props) || !known[h.props[ip.idx].typ]) {
				it.unsupported = true
				break
			}
		}
	}
	return h, nil
}

func (h *heif) item(id uint32) *heifItem {
	it := h.items[id]
	if it == nil {
		it = &heifItem{id: id}
		h.items[id] = it
	}
	return it
}

func (h *heif) parseIloc(b []byte) error {
	r := &boxReader{b: b}
	v, _ := r.fullBox()
	sizes := r.u8()
	offSize, lenSize := int(sizes>>4), int(sizes&0xf)
	sizes = r.u8()
	baseSize, idxSize := int(sizes>>4), int(sizes&0xf)
	if v < 1 {
		idxSize = 0
	}
	for _, n := range []int{offSize, lenSize, baseSize, idxSize} {
		if n != 0 && n != 4 && n != 8 {
			return errHEIF
		}
	}
	var count int
	if v == 2 {
		count = int(r.u32())
	} else {
		count = int(r.u16())
	}
	for range count {
		var id uint32
		if v < 2 {
			id = uint32(r.u16())
		} else {
			id = r.u32()
		}
		it := h.item(id)
		if v >= 1 {
			it.method = uint8(r.u16() & 0xf)
		}
		r.skip(2)
		it.baseOffset = r.uint(baseSize)
		n := int(r.u16())
		for range n {
			r.skip(idxSize)
			off, l := r.uint(offSize), r.uint(lenSize)
			if r.err {
				return errHEIF
			}
			it.extents = append(it.extents, heifExtent{off: off, len: l})
		}
		if r.err {
			return errHEIF
		}
	}
	return nil
}

func (h *heif) parseIinf(b []byte) error {
	r := &boxReader{b: b}
	if v, _ := r.fullBox(); v == 0 {
		r.u16()
	} else {
		r.u32()
	}
	if r.err {
		return errHEIF
	}
	return eachBox(b[r.i:], func(typ string, b []byte) error {
		if typ != "infe" {
			return nil
		}
		rr := &boxReader{b: b}
		v, _ := rr.fullBox()
		if v < 2 {
			return nil
		}
		var id uint32
		if v == 2 {
			id = uint32(rr.u16())
		} else {
			id = rr.u32()
		}
		rr.skip(2)
		typ = rr.str4()
		if rr.err {
			return errHEIF
		}
		h.item(id).typ = typ
		return nil
	})
}

func (h *heif) parseIref(b []byte) error {
	r := &boxReader{b: b}
	v, _ := r.fullBox()
	if r.err {
		return errHEIF
	}
	return eachBox(b[r.i:], func(typ string, b []byte) error {
		rr := &boxReader{b: b}
		ref := heifRef{typ: typ}
		if v == 0 {
			ref.from = uint32(rr.u16())
		} else {
			ref.from = rr.u32()
		}
		n := int(rr.u16())
		for range n {
			if v == 0 {
				ref.to = append(ref.to, uint32(rr.u16()))
			} else {
				ref.to = append(ref.to, rr.u32())
			}
		}
		if rr.err {
			return errHEIF
		}
		h.refs = append(h.refs, ref)
		return nil
	})
}

func (h *heif) parseIpco(b []byte) error {
	return eachBox(b, func(typ string, b []byte) error {
		p := heifProp{typ: typ}
		r := &boxReader{b: b}
		switch typ {
		case "ispe":
			r.fullBox()
			p.w, p.h = r.u32(), r.u32()
		case "hvcC":
			c, err := parseHvcConfig(r)
			if err != nil {
				return err
			}
			p.hvcC = c
		case "auxC":
			r.fullBox()
			p.auxC = r.cstr()
		case "colr":
			switch r.str4() {
			case "nclx":
				p.colr = &heifColor{nclx: true, primaries: r.u16(), transfer: r.u16(), matrix: r.u16()}
				p.colr.fullRange = r.u8()>>7 != 0
			case "rICC", "prof":
				p.colr = &heifColor{icc: b[4:]}
			}
		case "clap":
			for i := range p.clap {
				p.clap[i] = r.u32()
			}
		case "irot":
			p.angle = r.u8() & 3
		case "imir":
			p.axis = r.u8() & 1
		}
		if r.err {
			return errHEIF
		}
		h.props = append(h.props, p)
		return nil
	})
}

func (h *heif) parseIpma(b []byte) error {
	r := &boxReader{b: b}
	v, flags := r.fullBox()
	count := int(r.u32())
	if r.err {
		return errHEIF
	}
	for range count {
		var id uint32
		if v < 1 {
			id = uint32(r.u16())
		} else {
			id = r.u32()
		}
		it := h.item(id)
		n := int(r.u8())
		it.props = slices.Grow(it.props, n)
		for range n {
			var idx int
			var essential bool
			if flags&1 != 0 {
				v := r.u16()
				idx, essential = int(v&0x7fff), v&0x8000 != 0
			} else {
				v := r.u8()
				idx, essential = int(v&0x7f), v&0x80 != 0
			}
			if r.err {
				return errHEIF
			}
			if idx != 0 {
				it.props = append(it.props, heifItemProp{idx: idx - 1, essential: essential})
			}
		}
	}
	return nil
}

// itemData gathers an item's extents out of the file or, for construction
// method 1, out of idat.
func (h *heif) itemData(it *heifItem) ([]byte, error) {
	src := h.data
	switch it.method {
	case 0:
	case 1:
		src = h.idat
	default:
		return nil, errHEIF
	}
	size := uint64(len(src))
	var out []byte
	for _, e := range it.extents {
		off, n := it.baseOffset+e.off, e.len
		if off > size {
			return nil, errHEIF
		}
		if n == 0 {
			n = size - off
		}
		if n > size-off {
			return nil, errHEIF
		}
		if len(it.extents) == 1 {
			return src[off : off+n], nil
		}
		out = append(out, src[off:off+n]...)
	}
	if out == nil {
		return nil, errHEIF
	}
	return out, nil
}

// prop returns an item's property of a type, or nil.
func (h *heif) prop(it *heifItem, typ string) *heifProp {
	if it == nil {
		return nil
	}
	for _, ip := range it.props {
		if ip.idx < len(h.props) && h.props[ip.idx].typ == typ {
			return &h.props[ip.idx]
		}
	}
	return nil
}

// refsFrom returns the items a reference of a type from an item points to.
func (h *heif) refsFrom(typ string, from uint32) []uint32 {
	for _, r := range h.refs {
		if r.typ == typ && r.from == from {
			return r.to
		}
	}
	return nil
}

// hasAlpha says whether an auxiliary item gives an item alpha.
func (h *heif) hasAlpha(id uint32) bool {
	for _, r := range h.refs {
		if r.typ != "auxl" || len(r.to) == 0 || r.to[0] != id {
			continue
		}
		if p := h.prop(h.items[r.from], "auxC"); p != nil &&
			(p.auxC == "urn:mpeg:mpegB:cicp:systems:auxiliary:alpha" || p.auxC == "urn:mpeg:hevc:2015:auxid:1") {
			return true
		}
	}
	return false
}

// colors returns an item's nclx description and ICC profile, if it has them.
func (h *heif) colors(it *heifItem) (nclx *heifColor, icc []byte) {
	if it == nil {
		return nil, nil
	}
	for _, ip := range it.props {
		if ip.idx >= len(h.props) || h.props[ip.idx].colr == nil {
			continue
		}
		c := h.props[ip.idx].colr
		if c.nclx {
			nclx = c
		}
		if c.icc != nil {
			icc = c.icc
		}
	}
	return nclx, icc
}

// parseHvcConfig reads the HEVCDecoderConfigurationRecord.
func parseHvcConfig(r *boxReader) (*hvcConfig, error) {
	if r.u8() != 1 {
		return nil, errHEIF
	}
	r.skip(12 + 2 + 1)
	c := &hvcConfig{}
	c.chromaFormat = r.u8() & 3
	c.bitDepthLuma = r.u8()&7 + 8
	c.bitDepthChroma = r.u8()&7 + 8
	r.skip(2)
	c.lengthSize = int(r.u8()&3) + 1
	arrays := int(r.u8())
	if r.err {
		return nil, errHEIF
	}
	for range arrays {
		r.skip(1)
		n := int(r.u16())
		for range n {
			nal := r.bytes(int(r.u16()))
			if r.err {
				return nil, errHEIF
			}
			c.paramSets = append(c.paramSets, nal)
		}
	}
	if r.err {
		return nil, errHEIF
	}
	return c, nil
}

// heifGrid is the descriptor of a grid item.
type heifGrid struct{ rows, cols, w, h int }

func parseHEIFGrid(b []byte) (heifGrid, error) {
	r := &boxReader{b: b}
	r.u8()
	flags := r.u8()
	g := heifGrid{rows: int(r.u8()) + 1, cols: int(r.u8()) + 1}
	if flags&1 != 0 {
		g.w, g.h = int(r.u32()), int(r.u32())
	} else {
		g.w, g.h = int(r.u16()), int(r.u16())
	}
	if r.err || g.w <= 0 || g.h <= 0 {
		return g, errHEIF
	}
	return g, nil
}

// cropRect is the clean aperture of a clap box within a picture of w x h
// (h265's transform.go), in fractions throughout.
func cropRect(clap *[8]uint32, w, h int) (int, int, int, int, bool) {
	widthN, widthD := int64(int32(clap[0])), int64(int32(clap[1]))
	heightN, heightD := int64(int32(clap[2])), int64(int32(clap[3]))
	horizN, horizD := int64(int32(clap[4])), int64(int32(clap[5]))
	vertN, vertD := int64(int32(clap[6])), int64(int32(clap[7]))
	if widthD <= 0 || heightD <= 0 || horizD <= 0 || vertD <= 0 ||
		widthN < 0 || heightN < 0 {
		return 0, 0, 0, 0, false
	}
	if widthN%widthD != 0 || heightN%heightD != 0 {
		return 0, 0, 0, 0, false
	}
	clapW, clapH := widthN/widthD, heightN/heightD
	numX := int64(w)*horizD + 2*horizN - clapW*horizD
	denX := 2 * horizD
	numY := int64(h)*vertD + 2*vertN - clapH*vertD
	denY := 2 * vertD
	if numX%denX != 0 || numY%denY != 0 {
		return 0, 0, 0, 0, false
	}
	x, y := numX/denX, numY/denY
	if x < 0 || y < 0 || clapW <= 0 || clapH <= 0 ||
		x+clapW > int64(w) || y+clapH > int64(h) {
		return 0, 0, 0, 0, false
	}
	return int(x), int(y), int(clapW), int(clapH), true
}
