// Package psdstream reads the composite image of a Photoshop file, PSD or
// PSB (the large document format): the picture of the whole document that
// Photoshop saves next to its layers ("maximize compatibility", on by
// default), a band of rows at a time. The layers themselves are not read.
//
// It reads every colour mode (bitmap, grey, indexed, RGB, CMYK, multichannel
// and duotone as grey, Lab) at 1, 8, 16 and 32 bits, uncompressed or
// PackBits compressed, with the colour profile, EXIF, XMP and resolution.
// The transparency of the composite is found and its colours freed of the
// white Photoshop lays them on, as psd-tools does it (Pillow takes any
// fourth channel of an RGB file for alpha, and leaves the white in).
package psdstream

import (
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
	"math"

	"github.com/whhhh1500/goimgtools/internal/iccprof"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// Match reports whether head starts a Photoshop file, PSD or PSB.
func Match(head []byte) bool {
	return len(head) >= 6 && string(head[:4]) == "8BPS" && head[4] == 0 && (head[5] == 1 || head[5] == 2)
}

var errFormat = errors.New("goimgtools: psd: not a valid Photoshop file")

// Colour modes.
const (
	modeBitmap    = 0
	modeGray      = 1
	modeIndexed   = 2
	modeRGB       = 3
	modeCMYK      = 4
	modeMulti     = 7
	modeDuotone   = 8
	modeLab       = 9
	compRaw       = 0
	compRLE       = 1
	maxAlphaIDs   = 56
	resResolution = 1005
	resICC        = 1039
	resTransIndex = 1047
	resAlphaIDs   = 1053
	resExif       = 1058
	resXmp        = 1060
)

// colourChannels is how many channels hold the colour in a mode, 0 for a
// mode not known.
func colourChannels(mode int) int {
	switch mode {
	case modeBitmap, modeGray, modeIndexed, modeMulti, modeDuotone:
		return 1
	case modeRGB, modeLab:
		return 3
	case modeCMYK:
		return 4
	}
	return 0
}

// file is what the headers of a Photoshop file say.
type file struct {
	psb                         bool
	channels, w, h, depth, mode int
	palette                     color.Palette
	transIndex                  int // the transparent colour of an indexed image, or -1
	meta                        pipeline.Meta
	layerCount                  int
	mergedAlpha                 bool // a tagged block says the composite has transparency
	alphaIDs                    []uint32
	comp                        int
	data                        int64 // where the image data starts, after its compression
}

// reader reads the headers in order.
type reader struct {
	r        io.ReaderAt
	off, end int64
	err      error
}

func (p *reader) read(n int64) []byte {
	if p.err != nil {
		return make([]byte, n)
	}
	if n < 0 || n > p.end-p.off {
		p.err = errFormat
		return make([]byte, max(n, 0))
	}
	b := make([]byte, n)
	if _, err := p.r.ReadAt(b, p.off); err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		p.err = err
	}
	p.off += n
	return b
}

func (p *reader) u16() int64 { return int64(binary.BigEndian.Uint16(p.read(2))) }
func (p *reader) u32() int64 { return int64(binary.BigEndian.Uint32(p.read(4))) }

// length reads a section's length: 8 bytes in a PSB when big is set.
func (p *reader) length(big bool) int64 {
	if big {
		v := binary.BigEndian.Uint64(p.read(8))
		if v > math.MaxInt64/2 {
			p.err = errFormat
			return 0
		}
		return int64(v)
	}
	return p.u32()
}

// skipTo moves to off, which must be within the file.
func (p *reader) skipTo(off int64) {
	if off < p.off || off > p.end {
		if p.err == nil {
			p.err = errFormat
		}
		return
	}
	p.off = off
}

// bigKeys are the tagged blocks whose length takes 8 bytes in a PSB.
var bigKeys = map[string]bool{"LMsk": true, "Lr16": true, "Lr32": true, "Layr": true, "Mt16": true, "Mt32": true,
	"Mtrn": true, "Alph": true, "FMsk": true, "lnk2": true, "lnk3": true, "lnkE": true, "FEid": true, "FXid": true,
	"FELS": true, "PxSD": true, "pths": true, "extd": true, "extn": true, "cinf": true, "artd": true}

func parse(r io.ReaderAt, size int64) (*file, error) {
	p := &reader{r: r, end: size}
	h := p.read(26)
	if p.err != nil || !Match(h) {
		return nil, errFormat
	}
	f := &file{psb: h[5] == 2, transIndex: -1}
	f.channels = int(binary.BigEndian.Uint16(h[12:]))
	f.h, f.w = int(binary.BigEndian.Uint32(h[14:])), int(binary.BigEndian.Uint32(h[18:]))
	f.depth, f.mode = int(binary.BigEndian.Uint16(h[22:])), int(binary.BigEndian.Uint16(h[24:]))
	limit := 30000
	if f.psb {
		limit = 300000
	}
	if f.channels < 1 || f.channels > 56 || f.w < 1 || f.h < 1 || f.w > limit || f.h > limit {
		return nil, errFormat
	}
	need := colourChannels(f.mode)
	switch {
	case need == 0:
		return nil, fmt.Errorf("goimgtools: psd: colour mode %d is not supported", f.mode)
	case f.depth != 1 && f.depth != 8 && f.depth != 16 && f.depth != 32:
		return nil, errFormat
	case (f.depth == 1) != (f.mode == modeBitmap):
		return nil, errFormat
	case f.depth == 32 && f.mode != modeGray && f.mode != modeRGB:
		return nil, fmt.Errorf("goimgtools: psd: 32-bit colour mode %d is not supported", f.mode)
	case f.channels < need:
		return nil, errFormat
	}

	// The colour mode data: the palette of an indexed image.
	n := p.u32()
	at := p.off
	if f.mode == modeIndexed {
		if n < 768 {
			return nil, errFormat
		}
		pal := p.read(768)
		f.palette = make(color.Palette, 256)
		for i := range f.palette {
			f.palette[i] = color.NRGBA{pal[i], pal[256+i], pal[512+i], 255}
		}
	}
	p.skipTo(at + n)

	// The image resources.
	n = p.u32()
	end := p.off + n
	if p.err == nil && n > p.end-p.off {
		return nil, errFormat
	}
	for p.err == nil && p.off+12 <= end {
		p.read(4) // the signature, "8BIM" mostly
		id := p.u16()
		name := int64(p.read(1)[0])
		p.skipTo(p.off + name + (name+1)&1)
		sz := p.u32()
		at := p.off
		if p.err != nil || sz > end-at {
			break
		}
		switch id {
		case resICC, resExif, resXmp:
			if sz <= pipeline.MaxMetaBytes {
				b := p.read(sz)
				switch id {
				case resICC:
					f.meta.ICC = b
				case resExif:
					f.meta.Exif = b
				default:
					f.meta.Xmp = b
				}
			}
		case resResolution:
			if sz >= 4 {
				if v := float64(p.u32()) / 65536; v > 0 && v < 1e6 {
					f.meta.Density = v
				}
			}
		case resTransIndex:
			if sz >= 2 {
				f.transIndex = int(p.u16())
			}
		case resAlphaIDs:
			for k := int64(0); k+4 <= sz && len(f.alphaIDs) < maxAlphaIDs; k += 4 {
				f.alphaIDs = append(f.alphaIDs, uint32(p.u32()))
			}
		}
		p.off = at
		p.skipTo(min(at+sz+sz&1, end))
	}
	if p.err == nil {
		p.off = end
	}

	// The layers and masks: only whether the composite has transparency.
	n = p.length(f.psb)
	end = p.off + n
	if p.err == nil && n > p.end-p.off {
		return nil, errFormat
	}
	if n > 0 && p.err == nil {
		li := p.length(f.psb)
		at := p.off
		if li >= 2 && li <= end-at {
			f.layerCount = int(int16(p.u16()))
		}
		p.off = at
		if li <= end-at {
			p.skipTo(at + li)
			// The global layer mask info, unless the tagged blocks follow.
			if p.off+4 <= end {
				if sig := string(p.read(4)); sig != "8BIM" && sig != "8B64" {
					p.off -= 4
					g := p.u32()
					if g <= end-p.off {
						p.skipTo(p.off + g)
					} else {
						p.off = end
					}
				} else {
					p.off -= 4
				}
			}
			for p.err == nil && p.off+12 <= end {
				sig := string(p.read(4))
				if sig != "8BIM" && sig != "8B64" {
					break
				}
				key := string(p.read(4))
				l := p.length(f.psb && bigKeys[key])
				at := p.off
				if p.err != nil || l > end-at {
					break
				}
				switch key {
				case "Mtrn", "Mt16", "Mt32":
					f.mergedAlpha = true
				case "Layr", "Lr16", "Lr32":
					if f.layerCount == 0 && l >= 2 {
						f.layerCount = int(int16(p.u16()))
					}
				}
				p.off = at
				p.skipTo(at + (l+3)&^3)
			}
		}
	}
	if p.err == nil {
		p.off = min(end, p.end)
	}
	f.comp = int(p.u16())
	f.data = p.off
	if p.err != nil {
		if p.err == errFormat {
			return nil, p.err
		}
		return nil, fmt.Errorf("goimgtools: psd: %w", p.err)
	}
	if f.comp != compRaw && f.comp != compRLE {
		return nil, fmt.Errorf("goimgtools: psd: a composite image compressed with method %d is not supported", f.comp)
	}
	if f.mode == modeLab {
		// Its pixels come out in sRGB.
		f.meta.ICC = nil
	}
	return f, nil
}

// alpha is the channel that holds the transparency of the composite, or
// -1: as psd-tools finds it.
func (f *file) alpha() int {
	need := colourChannels(f.mode)
	if f.channels <= need || f.mode == modeMulti || f.mode == modeCMYK || f.mode == modeBitmap || f.mode == modeIndexed {
		return -1
	}
	has := f.mergedAlpha || f.layerCount < 0
	if !has {
		allSet := len(f.alphaIDs) > 0
		for _, id := range f.alphaIDs {
			allSet = allSet && id > 0
		}
		has = !allSet && f.layerCount <= 0
	}
	if !has {
		return -1
	}
	if f.layerCount < 0 {
		return need
	}
	for i, id := range f.alphaIDs {
		if id == 0 {
			if c := f.channels - len(f.alphaIDs) + i; c >= need && c < f.channels {
				return c
			}
		}
	}
	return f.channels - 1
}

// rowBytes is the size of a row of one channel.
func (f *file) rowBytes() int64 {
	if f.depth == 1 {
		return int64(f.w+7) / 8
	}
	return int64(f.w) * int64(f.depth/8)
}

// plane is a channel being read.
type plane struct {
	ch   int
	off  int64  // where its next row is stored
	rows []byte // the rows of the band, unpacked
}

// stream is the composite image, decoded a band of rows at a time.
type stream struct {
	r        io.ReaderAt
	env      *pipeline.Env
	f        *file
	alphaCh  int
	planes   []*plane
	counts   int64 // where the row lengths of a PackBits image start
	cs       int64 // the size of a row length: 2, or 4 in a PSB
	rb       int64
	band     image.Image
	n, y     int
	packed   []byte
	countBuf []byte
	reserved int64
	err      error
}

// Open reads the headers of a Photoshop file of size bytes and returns its
// composite image, decoded a band of rows at a time from r.
func Open(r io.ReaderAt, size int64, env *pipeline.Env) (pipeline.Source, error) {
	if env == nil {
		env = pipeline.NewEnv(0, "")
	}
	f, err := parse(r, size)
	if err != nil {
		return nil, err
	}
	s := &stream{r: r, env: env, f: f, alphaCh: f.alpha(), rb: f.rowBytes(), cs: 2}
	if f.psb {
		s.cs = 4
	}
	use := colourChannels(f.mode)
	if f.mode == modeMulti || f.mode == modeDuotone {
		use = 1
	}
	chans := make([]int, 0, use+1)
	for c := 0; c < use; c++ {
		chans = append(chans, c)
	}
	if s.alphaCh >= 0 {
		chans = append(chans, s.alphaCh)
	}
	last := chans[len(chans)-1]
	h := int64(f.h)
	// Where each channel's rows start; a file too short for them is
	// rejected here.
	starts := make([]int64, last+1)
	switch f.comp {
	case compRaw:
		if s.rb > (size-f.data)/h/int64(f.channels) {
			return nil, io.ErrUnexpectedEOF
		}
		for c := range starts {
			starts[c] = f.data + int64(c)*h*s.rb
		}
	case compRLE:
		s.counts = f.data
		tab := int64(f.channels) * h * s.cs
		if tab > size-f.data {
			return nil, io.ErrUnexpectedEOF
		}
		off := f.data + tab
		buf := make([]byte, 64<<10)
		maxRow := 2*s.rb + 2
		for c := 0; c <= last; c++ {
			starts[c] = off
			for y := int64(0); y < h; {
				k := min(h-y, int64(len(buf))/s.cs)
				b := buf[:k*s.cs]
				if _, err := r.ReadAt(b, f.data+(int64(c)*h+y)*s.cs); err != nil {
					return nil, fmt.Errorf("goimgtools: psd: %w", err)
				}
				for i := int64(0); i < k; i++ {
					l := s.count(b[i*s.cs:])
					if l > maxRow {
						return nil, errFormat
					}
					off += l
				}
				y += k
			}
		}
		if off > size {
			return nil, io.ErrUnexpectedEOF
		}
	}
	// The band: its pixels, the unpacked rows of its channels and, packed,
	// those of one channel.
	var out int64
	switch s.kind() {
	case kindGray:
		out = int64(f.w)
	case kindGray16:
		out = 2 * int64(f.w)
	case kindPaletted:
		out = int64(f.w)
	case kindCMYK, kindNRGBA:
		out = 4 * int64(f.w)
	case kindNRGBA64:
		out = 8 * int64(f.w)
	}
	s.n = pipeline.StripRows(out+int64(len(chans))*s.rb, f.h)
	need := int64(s.n) * (out + int64(len(chans))*s.rb)
	if f.comp == compRLE {
		need += int64(s.n) * (s.rb + 2) // packed rows, most of the time
		need += int64(s.n) * s.cs
	}
	if err := env.Budget.Reserve(need, "psd band"); err != nil {
		return nil, err
	}
	s.reserved = need
	for _, c := range chans {
		s.planes = append(s.planes, &plane{ch: c, off: starts[c], rows: make([]byte, int64(s.n)*s.rb)})
	}
	if f.comp == compRLE {
		s.packed = make([]byte, int64(s.n)*(s.rb+2))
		s.countBuf = make([]byte, int64(s.n)*s.cs)
	}
	r0 := image.Rect(0, 0, f.w, s.n)
	switch s.kind() {
	case kindGray:
		s.band = image.NewGray(r0)
	case kindGray16:
		s.band = image.NewGray16(r0)
	case kindPaletted:
		pal := f.palette
		if f.transIndex >= 0 && f.transIndex < len(pal) {
			pal = append(color.Palette(nil), pal...)
			c := pal[f.transIndex].(color.NRGBA)
			c.A = 0
			pal[f.transIndex] = c
		}
		s.band = image.NewPaletted(r0, pal)
	case kindCMYK:
		s.band = image.NewCMYK(r0)
	case kindNRGBA:
		s.band = image.NewNRGBA(r0)
	case kindNRGBA64:
		s.band = image.NewNRGBA64(r0)
	}
	return s, nil
}

func (s *stream) count(b []byte) int64 {
	if s.cs == 4 {
		return int64(binary.BigEndian.Uint32(b))
	}
	return int64(binary.BigEndian.Uint16(b))
}

// The kinds of band.
const (
	kindGray = iota
	kindGray16
	kindPaletted
	kindCMYK
	kindNRGBA
	kindNRGBA64
)

func (s *stream) kind() int {
	f := s.f
	switch {
	case f.mode == modeIndexed:
		return kindPaletted
	case f.mode == modeCMYK:
		return kindCMYK
	case f.mode == modeLab || f.mode == modeRGB || s.alphaCh >= 0:
		if f.depth >= 16 {
			return kindNRGBA64
		}
		return kindNRGBA
	case f.depth >= 16:
		return kindGray16
	}
	return kindGray
}

func (s *stream) Env() *pipeline.Env      { return s.env }
func (s *stream) Bounds() image.Rectangle { return image.Rect(0, 0, s.f.w, s.f.h) }
func (s *stream) ColorModel() color.Model {
	if p, ok := s.band.(*image.Paletted); ok {
		return p.Palette
	}
	return s.band.ColorModel()
}

// Opaque reports whether the composite has no transparency.
func (s *stream) Opaque() bool {
	return s.alphaCh < 0 && (s.f.mode != modeIndexed || s.f.transIndex < 0 || s.f.transIndex >= len(s.f.palette))
}

// Meta returns the colour profile, EXIF, XMP and resolution of the file.
func (s *stream) Meta() pipeline.Meta { return s.f.meta }

func (s *stream) Close() error {
	if s.reserved != 0 {
		s.env.Budget.Release(s.reserved)
		s.reserved = 0
	}
	s.planes, s.packed = nil, nil
	if s.err == nil {
		s.err = errors.New("goimgtools: psd: closed")
	}
	return nil
}

func (s *stream) Next() (image.Image, error) {
	if s.err != nil {
		return nil, s.err
	}
	if s.y >= s.f.h {
		return nil, io.EOF
	}
	n := min(s.n, s.f.h-s.y)
	for _, p := range s.planes {
		if err := s.readPlane(p, n); err != nil {
			s.err = err
			return nil, err
		}
	}
	out := s.compose(n)
	s.y += n
	return out, nil
}

// readPlane unpacks the next n rows of a channel.
func (s *stream) readPlane(p *plane, n int) error {
	rb := s.rb
	dst := p.rows[:int64(n)*rb]
	if s.f.comp == compRaw {
		if _, err := s.r.ReadAt(dst, p.off); err != nil {
			return fmt.Errorf("goimgtools: psd: %w", err)
		}
		p.off += int64(len(dst))
		return nil
	}
	cb := s.countBuf[:int64(n)*s.cs]
	if _, err := s.r.ReadAt(cb, s.counts+(int64(p.ch)*int64(s.f.h)+int64(s.y))*s.cs); err != nil {
		return fmt.Errorf("goimgtools: psd: %w", err)
	}
	var total int64
	for i := 0; i < n; i++ {
		total += s.count(cb[int64(i)*s.cs:])
	}
	if total > int64(len(s.packed)) {
		if err := s.env.Budget.Reserve(total-int64(len(s.packed)), "psd band"); err != nil {
			return err
		}
		s.reserved += total - int64(len(s.packed))
		s.packed = make([]byte, total)
	}
	src := s.packed[:total]
	if _, err := s.r.ReadAt(src, p.off); err != nil {
		return fmt.Errorf("goimgtools: psd: %w", err)
	}
	p.off += total
	for i := 0; i < n; i++ {
		l := s.count(cb[int64(i)*s.cs:])
		if !unpackBits(dst[int64(i)*rb:int64(i+1)*rb], src[:l]) {
			return errFormat
		}
		src = src[l:]
	}
	return nil
}

// unpackBits unpacks a PackBits row into dst, which it must fill exactly
// (what is left in src past it is ignored, as Photoshop and psd-tools
// ignore it).
func unpackBits(dst, src []byte) bool {
	for len(dst) > 0 {
		if len(src) == 0 {
			return false
		}
		h := int(int8(src[0]))
		src = src[1:]
		switch {
		case h >= 0:
			k := h + 1
			if k > len(src) || k > len(dst) {
				return false
			}
			copy(dst, src[:k])
			dst, src = dst[k:], src[k:]
		case h != -128:
			k := 1 - h
			if len(src) == 0 || k > len(dst) {
				return false
			}
			v := src[0]
			src = src[1:]
			for i := range dst[:k] {
				dst[i] = v
			}
			dst = dst[k:]
		}
	}
	return true
}

// compose interleaves the n rows of the channels into the band.
func (s *stream) compose(n int) image.Image {
	f, w, rb := s.f, s.f.w, int(s.rb)
	ch := func(i, y int) []byte { return s.planes[i].rows[y*rb : (y+1)*rb] }
	alpha := -1
	if s.alphaCh >= 0 {
		alpha = len(s.planes) - 1
	}
	rect := image.Rect(0, s.y, w, s.y+n)
	switch b := s.band.(type) {
	case *image.Gray:
		for y := 0; y < n; y++ {
			row := b.Pix[y*b.Stride : y*b.Stride+w]
			if f.depth == 1 {
				// A set bit is black.
				src := ch(0, y)
				for x := range row {
					row[x] = 255 * (1 - src[x>>3]>>(7-x&7)&1)
				}
				continue
			}
			copy(row, ch(0, y))
		}
		return &image.Gray{Pix: b.Pix[:n*b.Stride], Stride: b.Stride, Rect: rect}
	case *image.Gray16:
		for y := 0; y < n; y++ {
			row := b.Pix[y*b.Stride : y*b.Stride+2*w]
			if f.depth == 32 {
				src := ch(0, y)
				for x := 0; x < w; x++ {
					binary.BigEndian.PutUint16(row[2*x:], encode16(float32frombits(src[4*x:])))
				}
				continue
			}
			copy(row, ch(0, y))
		}
		return &image.Gray16{Pix: b.Pix[:n*b.Stride], Stride: b.Stride, Rect: rect}
	case *image.Paletted:
		for y := 0; y < n; y++ {
			copy(b.Pix[y*b.Stride:y*b.Stride+w], ch(0, y))
		}
		return &image.Paletted{Pix: b.Pix[:n*b.Stride], Stride: b.Stride, Rect: rect, Palette: b.Palette}
	case *image.CMYK:
		for y := 0; y < n; y++ {
			row := b.Pix[y*b.Stride : y*b.Stride+4*w]
			for c := 0; c < 4; c++ {
				src := ch(c, y)
				for x := 0; x < w; x++ {
					v := src[x]
					if f.depth == 16 {
						v = uint8((uint32(binary.BigEndian.Uint16(src[2*x:]))*255 + 32767) / 65535)
					}
					// Photoshop stores CMYK with 0 for full ink.
					row[4*x+c] = 255 - v
				}
			}
		}
		return &image.CMYK{Pix: b.Pix[:n*b.Stride], Stride: b.Stride, Rect: rect}
	case *image.NRGBA:
		for y := 0; y < n; y++ {
			row := b.Pix[y*b.Stride : y*b.Stride+4*w]
			colours := 3
			if f.mode != modeRGB && f.mode != modeLab {
				colours = 1
			}
			for c := 0; c < 3; c++ {
				src := ch(min(c, colours-1), y)
				for x := 0; x < w; x++ {
					row[4*x+c] = src[x]
				}
			}
			if alpha >= 0 {
				src := ch(alpha, y)
				for x := 0; x < w; x++ {
					row[4*x+3] = src[x]
				}
			} else {
				for x := 0; x < w; x++ {
					row[4*x+3] = 255
				}
			}
			if f.mode == modeLab {
				for x := 0; x < w; x++ {
					p := row[4*x : 4*x+3]
					v := iccprof.LabToSRGB(float64(p[0])*100/255, float64(p[1])-128, float64(p[2])-128)
					for c := range p {
						p[c] = uint8(math.Round(v[c]))
					}
				}
			}
			if alpha >= 0 && f.mode != modeDuotone {
				unmatte8(row)
			}
		}
		return &image.NRGBA{Pix: b.Pix[:n*b.Stride], Stride: b.Stride, Rect: rect}
	case *image.NRGBA64:
		for y := 0; y < n; y++ {
			row := b.Pix[y*b.Stride : y*b.Stride+8*w]
			colours := 3
			if f.mode != modeRGB && f.mode != modeLab {
				colours = 1
			}
			get := func(i, x int) uint16 {
				src := ch(i, y)
				if f.depth == 32 {
					return encode16(float32frombits(src[4*x:]))
				}
				return binary.BigEndian.Uint16(src[2*x:])
			}
			for x := 0; x < w; x++ {
				var px [4]uint16
				for c := 0; c < 3; c++ {
					px[c] = get(min(c, colours-1), x)
				}
				px[3] = 0xffff
				if alpha >= 0 {
					if f.depth == 32 {
						// Alpha is not a light: it is not encoded.
						px[3] = uint16(math.Round(float64(clamp01(float32frombits(ch(alpha, y)[4*x:]))) * 65535))
					} else {
						px[3] = binary.BigEndian.Uint16(ch(alpha, y)[2*x:])
					}
				}
				if f.mode == modeLab {
					v := iccprof.LabToSRGB(float64(px[0])*100/65535, float64(px[1])/257-128, float64(px[2])/257-128)
					for c := 0; c < 3; c++ {
						px[c] = uint16(math.Round(v[c] * 257))
					}
				}
				if alpha >= 0 && f.mode != modeDuotone {
					a := uint32(px[3])
					for c := 0; c < 3; c++ {
						px[c] = unmatte(uint32(px[c]), a, 0xffff)
					}
				}
				for c := 0; c < 4; c++ {
					binary.BigEndian.PutUint16(row[8*x+2*c:], px[c])
				}
			}
		}
		return &image.NRGBA64{Pix: b.Pix[:n*b.Stride], Stride: b.Stride, Rect: rect}
	}
	panic("unreachable")
}

// unmatte8 takes the white out of a row of colours laid on it.
func unmatte8(row []byte) {
	for x := 0; x+3 < len(row); x += 4 {
		a := uint32(row[x+3])
		if a == 0 || a == 255 {
			continue
		}
		for c := 0; c < 3; c++ {
			row[x+c] = uint8(unmatte(uint32(row[x+c]), a, 255))
		}
	}
}

// unmatte is the colour v of alpha a showed on white (full is the white):
// (v - (full - a)) full / a, rounded, as psd-tools takes it out.
func unmatte(v, a, full uint32) uint16 {
	if a == 0 || a == full {
		return uint16(v)
	}
	if v+a <= full {
		return 0
	}
	return uint16(min(full, ((v+a-full)*full+a/2)/a))
}

func float32frombits(b []byte) float64 {
	return float64(math.Float32frombits(binary.BigEndian.Uint32(b)))
}

func clamp01(v float64) float64 {
	if !(v > 0) {
		return 0
	}
	return min(v, 1)
}

// encode16 is a linear light of a 32-bit document, 0 to 1, in the sRGB
// curve on 16 bits: Photoshop works on 32-bit documents linearly.
func encode16(v float64) uint16 {
	v = clamp01(v)
	if v <= 0.0031308 {
		v *= 12.92
	} else {
		v = float64(1.055*math.Pow(v, 1/2.4)) - 0.055
	}
	return uint16(math.Round(float64(v * 65535)))
}
