package heic

// The HEIF file is written the way github.com/gen2brain/h265 writes it
// (heic/encode.go and heic/gridenc.go, MIT licence, Copyright (c) 2025
// roticv, Copyright (c) 2026 Karpeles Lab Inc.; licenses/LICENSE-H265 here),
// from which the box writing below is taken, changed to take the pictures
// as Kvazaar codes them and to give a grid's alpha a grid of its own, as
// libheif does. Which properties are essential follows libheif.

import (
	"encoding/binary"
	"errors"
	"math"

	"github.com/gen2brain/h265/hevc"
)

// alphaURN is the auxiliary type of ISO/IEC 23008-12 Annex F, which names an
// item as the alpha channel of the picture it is tied to.
const alphaURN = "urn:mpeg:hevc:2015:auxid:1"

// The colour description of the samples: BT.709 primaries and the sRGB
// curve (what an image without a profile is taken to be), with the full
// range BT.601 matrix, or the identity (G, B and R planes) for lossless
// pictures.
const (
	encPrimaries = 1
	encTransfer  = 13
	matrixBT601  = 6
	matrixGBR    = 0
)

// coded is one picture as Kvazaar coded it: the parameter sets, which hvcC
// carries, and the sample, its NAL units each behind a 4-byte length.
type coded struct {
	params [][]byte
	sample []byte
}

// splitStream splits an Annex B stream into the parameter sets and the
// sample of an item; other NAL units (SEI) are dropped.
func splitStream(s []byte) (coded, error) {
	var c coded
	i := startCode(s, 0)
	for i >= 0 && i < len(s) {
		next := startCode(s, i)
		end := len(s)
		if next >= 0 {
			end = next - 3
		}
		nal := s[i:end]
		for len(nal) > 0 && nal[len(nal)-1] == 0 {
			nal = nal[:len(nal)-1]
		}
		i = next
		if len(nal) < 2 {
			continue
		}
		switch t := hevc.NALType(nal[0] >> 1 & 0x3f); {
		case t == hevc.NALVPS || t == hevc.NALSPS || t == hevc.NALPPS:
			c.params = append(c.params, nal)
		case t.IsVCL():
			c.sample = binary.BigEndian.AppendUint32(c.sample, uint32(len(nal)))
			c.sample = append(c.sample, nal...)
		}
	}
	if len(c.params) != 3 || len(c.sample) == 0 {
		return c, errors.New("heic: the encoder gave an incomplete stream")
	}
	return c, nil
}

// startCode returns the index just past the first start code (00 00 01) at
// or after from, or -1.
func startCode(s []byte, from int) int {
	for i := from; i+2 < len(s); i++ {
		if s[i] == 0 && s[i+1] == 0 && s[i+2] == 1 {
			return i + 3
		}
	}
	return -1
}

// ref is a reference of iref: from the item that holds it to others.
type ref struct {
	typ string
	to  []uint16
}

// encItem is one item on its way into the file: what iinf declares, what iloc
// points at, which ipco properties ipma ties to it, and what iref ties it to.
type encItem struct {
	id     uint16
	typ    string
	mime   string
	hidden bool
	data   []byte
	assoc  []byte
	refs   []ref
}

// heicFile gathers the items and their properties, so that the boxes naming
// them are written once from one list.
type heicFile struct {
	items  []encItem
	props  []byte
	nProps int
	shared map[string]byte
	err    error
}

// prop returns the index (one based, with the essential bit of 23008-12)
// that ipma names a property by, adding it to ipco unless the same is there.
func (f *heicFile) prop(b []byte, essential bool) byte {
	if f.shared == nil {
		f.shared = map[string]byte{}
	}
	key := string(b)
	if essential {
		key = "!" + key
	}
	if idx, ok := f.shared[key]; ok {
		return idx
	}
	// ipma names a property by seven bits.
	if f.nProps >= 0x7f {
		f.err = errors.New("heic: too many properties")
		return 0
	}
	f.props = append(f.props, b...)
	f.nProps++
	idx := byte(f.nProps)
	if essential {
		idx |= 0x80
	}
	f.shared[key] = idx
	return idx
}

func ispe(w, h int) []byte {
	return fullBox("ispe", 0, 0, binary.BigEndian.AppendUint32(binary.BigEndian.AppendUint32(nil, uint32(w)), uint32(h)))
}

func pixi(channels int) []byte {
	body := []byte{byte(channels)}
	for range channels {
		body = append(body, 8)
	}
	return fullBox("pixi", 0, 0, body)
}

func nclx(matrix int) []byte {
	body := []byte("nclx")
	body = binary.BigEndian.AppendUint16(body, encPrimaries)
	body = binary.BigEndian.AppendUint16(body, encTransfer)
	body = binary.BigEndian.AppendUint16(body, uint16(matrix))
	return box("colr", append(body, 0x80))
}

// clap is the CleanApertureBox taking the stored picture, which ispe names,
// down to the size that went in.
func clap(w, h, sw, sh int) []byte {
	var out []byte
	for _, v := range [8]int32{int32(w), 1, int32(h), 1, -int32(sw - w), 2, -int32(sh - h), 2} {
		out = binary.BigEndian.AppendUint32(out, uint32(v))
	}
	return box("clap", out)
}

// hvcC writes the HEVCDecoderConfigurationRecord. ISO/IEC 14496-15 requires
// its profile, tier and level to be the sequence parameter set's own.
func hvcC(params [][]byte) ([]byte, error) {
	bad := errors.New("heic: the encoder gave an unreadable SPS")
	var ptl []byte
	chroma, depthY, depthC := 0, 0, 0
	for _, raw := range params {
		nal, ok := hevc.ParseNAL(raw)
		if !ok || nal.Type != hevc.NALSPS {
			continue
		}
		b, ok := hevc.ProfileTierLevel(nal.RBSP)
		if !ok {
			return nil, bad
		}
		c, dy, dc, ok := hevc.SPSFormat(nal.RBSP)
		if !ok {
			return nil, bad
		}
		ptl, chroma, depthY, depthC = b, c, dy, dc
	}
	if ptl == nil {
		return nil, bad
	}
	out := append([]byte{1}, ptl...)
	out = append(out, 0xf0, 0, 0xfc,
		0xfc|byte(chroma),
		0xf8|byte(depthY-8),
		0xf8|byte(depthC-8),
		0, 0, 0x0f, byte(len(params)))
	for _, nal := range params {
		if len(nal) > 0xffff {
			return nil, errors.New("heic: a parameter set is too long")
		}
		out = append(out, 0x80|nal[0]>>1&0x3f, 0, 1)
		out = binary.BigEndian.AppendUint16(out, uint16(len(nal)))
		out = append(out, nal...)
	}
	return box("hvcC", out), nil
}

// picture is what goes into the file: the coded tiles (one, for a picture
// coded whole) of the colour and of the alpha, and how they are laid out.
type picture struct {
	width, height    int // the image
	storedW, storedH int // the coded picture, when coded whole
	tile             int // the side of a tile; 0 for a picture coded whole
	cols, rows       int
	color, alpha     []coded
	channels         int  // of the colour: 1 or 3
	rext             bool // the colour is 4:4:4 (HEVC's range extensions)
	matrix           int
	icc, exif, xmp   []byte
}

// property is a property box and whether readers must understand it.
type property struct {
	data      []byte
	essential bool
}

// coded adds the items of pictures (tiles, or the one picture), with the
// properties every one of them has and those given, and returns their ids.
func (f *heicFile) coded(pics []coded, w, h, channels int, hidden bool, extra ...property) []uint16 {
	var ids []uint16
	for _, c := range pics {
		h0, err := hvcC(c.params)
		if err != nil {
			f.err = err
			return nil
		}
		assoc := []byte{f.prop(h0, true), f.prop(ispe(w, h), false), f.prop(pixi(channels), false)}
		for _, e := range extra {
			assoc = append(assoc, f.prop(e.data, e.essential))
		}
		id := uint16(len(f.items) + 1)
		f.items = append(f.items, encItem{id: id, typ: "hvc1", hidden: hidden, data: c.sample, assoc: assoc})
		ids = append(ids, id)
	}
	return ids
}

// marshal writes the file.
func (p *picture) marshal() ([]byte, error) {
	f := &heicFile{}
	colour := []property{{nclx(p.matrix), false}}
	if len(p.icc) > 0 {
		colour = append(colour, property{box("colr", append([]byte("prof"), p.icc...)), false})
	}
	aux := property{fullBox("auxC", 0, 0, append([]byte(alphaURN), 0)), true}
	if p.tile == 0 {
		var crop []property
		if p.storedW != p.width || p.storedH != p.height {
			crop = []property{{clap(p.width, p.height, p.storedW, p.storedH), true}}
		}
		f.coded(p.color, p.storedW, p.storedH, p.channels, false, append(colour, crop...)...)
		if p.alpha != nil {
			ids := f.coded(p.alpha, p.storedW, p.storedH, 1, true, append([]property{aux}, crop...)...)
			if len(ids) == 1 {
				f.items[ids[0]-1].refs = []ref{{"auxl", []uint16{1}}}
			}
		}
	} else {
		desc := []byte{0, 1, byte(p.rows - 1), byte(p.cols - 1)}
		desc = binary.BigEndian.AppendUint32(desc, uint32(p.width))
		desc = binary.BigEndian.AppendUint32(desc, uint32(p.height))
		grid := func(tiles []coded, channels int, hidden bool, extra ...property) uint16 {
			id := uint16(len(f.items) + 1)
			assoc := []byte{f.prop(ispe(p.width, p.height), false), f.prop(pixi(channels), false)}
			for _, e := range extra {
				assoc = append(assoc, f.prop(e.data, e.essential))
			}
			f.items = append(f.items, encItem{id: id, typ: "grid", hidden: hidden, data: desc, assoc: assoc})
			ids := f.coded(tiles, p.tile, p.tile, channels, true)
			f.items[id-1].refs = []ref{{"dimg", ids}}
			return id
		}
		primary := grid(p.color, p.channels, false, colour...)
		if p.alpha != nil {
			id := grid(p.alpha, 1, true, aux)
			f.items[id-1].refs = append(f.items[id-1].refs, ref{"auxl", []uint16{primary}})
		}
	}
	if len(p.exif) > 0 {
		f.items = append(f.items, encItem{id: uint16(len(f.items) + 1), typ: "Exif",
			data: append(make([]byte, 4), p.exif...), refs: []ref{{"cdsc", []uint16{1}}}})
	}
	if len(p.xmp) > 0 {
		f.items = append(f.items, encItem{id: uint16(len(f.items) + 1), typ: "mime", mime: "application/rdf+xml",
			data: p.xmp, refs: []ref{{"cdsc", []uint16{1}}}})
	}
	if f.err != nil {
		return nil, f.err
	}
	if len(f.items) > 0xffff {
		return nil, errors.New("heic: too many items")
	}
	// The brands libheif gives: heix for HEVC's range extensions (4:4:4),
	// heic for its Main profiles; the alpha's 4:0:0 is taken as heic.
	brand := "heic"
	if p.rext {
		brand = "heix"
	}
	ftyp := box("ftyp", []byte(brand+"\x00\x00\x00\x00mif1"+brand+"miaf"))
	meta, err := f.metaBox(0)
	if err != nil {
		return nil, err
	}
	meta, err = f.metaBox(uint64(len(ftyp) + len(meta) + 8))
	if err != nil {
		return nil, err
	}
	size := 8
	for _, it := range f.items {
		size += len(it.data)
	}
	if uint64(len(ftyp)+len(meta))+uint64(size) > math.MaxUint32 {
		return nil, errors.New("heic: the file would be 4GB or more")
	}
	out := make([]byte, 0, len(ftyp)+len(meta)+size)
	out = append(append(out, ftyp...), meta...)
	out = binary.BigEndian.AppendUint32(out, uint32(size))
	out = append(out, "mdat"...)
	for _, it := range f.items {
		out = append(out, it.data...)
	}
	return out, nil
}

func (f *heicFile) metaBox(offset uint64) ([]byte, error) {
	hdlr := fullBox("hdlr", 0, 0, append([]byte("\x00\x00\x00\x00pict"), make([]byte, 13)...))
	pitm := fullBox("pitm", 0, 0, u16(1))

	var infes, ilocs, irefs, ipmas []byte
	assoc := 0
	for _, it := range f.items {
		flags := uint32(0)
		if it.hidden {
			flags = 1
		}
		entry := append(u16(it.id), 0, 0)
		entry = append(entry, it.typ...)
		entry = append(entry, 0)
		if it.typ == "mime" {
			entry = append(entry, it.mime...)
			entry = append(entry, 0, 0)
		}
		infes = append(infes, fullBox("infe", 2, flags, entry)...)

		if uint64(len(it.data))+offset > math.MaxUint32 {
			return nil, errors.New("heic: the file would be 4GB or more")
		}
		ilocs = append(ilocs, u16(it.id)...)
		ilocs = append(ilocs, u16(0)...)
		ilocs = append(ilocs, u16(1)...)
		ilocs = binary.BigEndian.AppendUint32(ilocs, uint32(offset))
		ilocs = binary.BigEndian.AppendUint32(ilocs, uint32(len(it.data)))
		offset += uint64(len(it.data))

		for _, r := range it.refs {
			body := append(u16(it.id), u16(uint16(len(r.to)))...)
			for _, to := range r.to {
				body = append(body, u16(to)...)
			}
			irefs = append(irefs, box(r.typ, body)...)
		}

		if len(it.assoc) == 0 {
			continue
		}
		ipmas = append(ipmas, u16(it.id)...)
		ipmas = append(ipmas, byte(len(it.assoc)))
		ipmas = append(ipmas, it.assoc...)
		assoc++
	}

	iinf := fullBox("iinf", 0, 0, append(u16(uint16(len(f.items))), infes...))
	iloc := fullBox("iloc", 0, 0, append([]byte{0x44, 0}, append(u16(uint16(len(f.items))), ilocs...)...))
	iprp := box("iprp", append(box("ipco", f.props),
		fullBox("ipma", 0, 0, append(binary.BigEndian.AppendUint32(nil, uint32(assoc)), ipmas...))...))

	meta := append(hdlr, pitm...)
	meta = append(meta, iinf...)
	meta = append(meta, iloc...)
	if irefs != nil {
		meta = append(meta, fullBox("iref", 0, 0, irefs)...)
	}
	meta = append(meta, iprp...)
	return fullBox("meta", 0, 0, meta), nil
}

func box(typ string, data []byte) []byte {
	out := make([]byte, 8, 8+len(data))
	binary.BigEndian.PutUint32(out, uint32(8+len(data)))
	copy(out[4:], typ)
	return append(out, data...)
}

func fullBox(typ string, version uint8, flags uint32, data []byte) []byte {
	return box(typ, append([]byte{version, byte(flags >> 16), byte(flags >> 8), byte(flags)}, data...))
}

func u16(v uint16) []byte { return binary.BigEndian.AppendUint16(nil, v) }
