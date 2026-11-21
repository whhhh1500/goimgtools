package heic

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
	"sync"

	gheic "github.com/gen2brain/h265/heic"
	"github.com/gen2brain/h265/hevc"
	"github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// stream decodes the primary image of a HEIC a row of tiles at a time, and
// hands it out in strips (NRGBA, or NRGBA64 above 8 bits, as h265 gives
// them): what a phone writes (a grid of 512 x 512 tiles, no transparency)
// takes a few rows of tiles of memory instead of the whole image twice over
// (its planes, and its pixels). The tiles are
// decoded in the order they are stored, by the goroutine that needs a row
// and by the helpers free, up to window rows ahead of the rows being read.
// A single picture is decoded whole, its pixels made a strip at a time; so
// is a grid to be turned (irot) or flipped (imir), each strip made from the
// rows, or the band of columns, it is turned from. The pixels are those
// gen2brain/h265 gives, which decodes the grid into one picture, converts
// it and then turns it: a row of output needs the chroma of the rows next
// to it, so the row of tiles below is decoded before the last rows of a row
// are converted. Files with transparency, luma and chroma of different
// depths, or a clean aperture h265 cuts from the pixels (or on a grid) are
// decoded whole as before.
type stream struct {
	env     *pipeline.Env
	file    *heif
	meta    pipeline.Meta
	w, h    int // the image
	clap    *heifProp
	picture bool // a single picture, not a grid
	ox, oy  int  // where its clean aperture starts
	rows    int  // rows of tiles
	cols    int
	tiles   []*heifItem
	tw, th  int // a tile's size, known once the first is decoded
	chroma  int
	sw, sh  int
	colour  *colourOf // from the file's nclx box, or the first tile's VUI
	conv    converter
	depth   int // bits a sample
	bpp     int // bytes a pixel of output: 4 (NRGBA), 8 (NRGBA64) above 8 bits
	single  *hevc.Picture
	held    []tileRow // row r of tiles in held[r%len(held)]
	slots   int
	turn    int // anticlockwise quarter turns (irot)
	flip    int // 0 top and bottom, 1 left and right (imir); -1 none
	queue   tileQueue
	y       int
	strip   []uint8 // rows of pixels, stride bpp*width
	stride  int
	scratch []*rowScratch
	reserve int64
	err     error
}

// tileRow is a row of tiles put together: planes of the image's width, of
// 8-bit samples or (y16, cb16, cr16) of more.
type tileRow struct {
	y, cb, cr       []uint8
	y16, cb16, cr16 []uint16
	ys, cs          int // strides, in samples
}

// maxTileSamples bounds what a tile's stream may ask a decoder for (HEVC's
// level 6.2 allows 35,651,584 luma samples).
const maxTileSamples = 36 << 20

// newStream returns the stream of data's primary image, or nil when the
// file is not one it decodes (gen2brain/h265 then decodes it whole).
func newStream(data []byte, env *pipeline.Env) (*stream, error) {
	h, err := parseHEIF(data)
	if err != nil {
		return nil, nil
	}
	it := h.items[h.primary]
	if it == nil || it.unsupported || h.hasAlpha(it.id) {
		return nil, nil
	}
	// The pictures: coded as HEVC, their luma and chroma of the same depth.
	depth := func(t *heifItem) int {
		if t == nil || t.unsupported || t.typ != "hvc1" {
			return 0
		}
		p := h.prop(t, "hvcC")
		if p == nil || p.hvcC == nil || p.hvcC.bitDepthLuma != p.hvcC.bitDepthChroma || p.hvcC.bitDepthLuma > 16 {
			return 0
		}
		return int(p.hvcC.bitDepthLuma)
	}
	s := &stream{env: env, file: h, clap: h.prop(it, "clap"), flip: -1}
	// Turned by irot, then flipped by imir, as h265 turns the pixels.
	if p := h.prop(it, "irot"); p != nil {
		s.turn = int(p.angle)
	}
	if p := h.prop(it, "imir"); p != nil {
		s.flip = int(p.axis)
	}
	nclx, icc := h.colors(it)
	switch it.typ {
	case "hvc1":
		sp := h.prop(it, "ispe")
		s.depth = depth(it)
		if s.depth == 0 || sp == nil || sp.w == 0 || sp.h == 0 || sp.w > 1<<20 || sp.h > 1<<20 {
			return nil, nil
		}
		// Its size is the one ispe gives, cut to the clean aperture, as
		// h265 reports it before decoding; the picture is decoded when the
		// first strip is asked for. An aperture off the chroma grid of
		// 4:2:0, which h265 cuts from the pixels, or one that is not valid,
		// which it refuses, is left to it.
		s.tiles, s.rows, s.cols, s.picture = []*heifItem{it}, 1, 1, true
		s.w, s.h = int(sp.w), int(sp.h)
		if s.clap != nil {
			x, y, w, h, ok := cropRect(&s.clap.clap, s.w, s.h)
			if !ok || x%2 != 0 || y%2 != 0 {
				return nil, nil
			}
			s.ox, s.oy, s.w, s.h = x, y, w, h
		}
	case "grid":
		b, err := h.itemData(it)
		if err != nil {
			return nil, nil
		}
		g, err := parseHEIFGrid(b)
		if err != nil {
			return nil, nil
		}
		ids := h.refsFrom("dimg", it.id)
		if len(ids) != g.rows*g.cols {
			return nil, nil
		}
		for _, id := range ids {
			t := h.items[id]
			if d := depth(t); d == 0 || d != depth(h.items[ids[0]]) || h.hasAlpha(id) {
				return nil, nil
			}
			s.tiles = append(s.tiles, t)
		}
		s.depth = depth(s.tiles[0])
		// The grid is cut to the size ispe gives, as h265 cuts it. (No
		// writer known puts a clean aperture on a grid: one that has it is
		// decoded whole.)
		s.rows, s.cols, s.w, s.h = g.rows, g.cols, g.w, g.h
		if p := h.prop(it, "ispe"); p != nil {
			s.w, s.h = min(s.w, int(min(p.w, 1<<30))), min(s.h, int(min(p.h, 1<<30)))
		}
		if s.w > 1<<20 || s.h > 1<<20 || s.w <= 0 || s.h <= 0 || s.clap != nil {
			return nil, nil
		}
		// A grid without a description of its own takes its first
		// tile's, as libheif reads it.
		tn, ti := h.colors(s.tiles[0])
		if nclx == nil {
			nclx = tn
		}
		if icc == nil {
			icc = ti
		}
	default:
		return nil, nil
	}
	if nclx != nil {
		s.colour = &colourOf{matrix: int(nclx.matrix), fullRange: nclx.fullRange}
	}
	s.bpp = 4
	if s.depth > 8 {
		s.bpp = 8
	}
	// The metadata, as the whole decoder keeps it.
	if x, err := gheic.RawExif(bytes.NewReader(data)); err == nil && len(x) <= pipeline.MaxMetaBytes {
		s.meta.Exif = pipeline.ExifUpright(bytes.Clone(x))
	}
	if x, err := gheic.RawXMP(bytes.NewReader(data)); err == nil && len(x) <= pipeline.MaxMetaBytes {
		s.meta.Xmp = bytes.Clone(x)
	}
	switch {
	case len(icc) > 0 && len(icc) <= pipeline.MaxMetaBytes:
		s.meta.ICC = bytes.Clone(icc)
	case nclx != nil && nclx.primaries == 12 && (nclx.transfer == 13 || nclx.transfer == 1 || nclx.transfer == 6):
		s.meta.ICC = displayP3
	}
	return s, nil
}

func (s *stream) Bounds() image.Rectangle {
	if s.turn&1 != 0 {
		return image.Rect(0, 0, s.h, s.w)
	}
	return image.Rect(0, 0, s.w, s.h)
}
func (s *stream) ColorModel() color.Model {
	if s.depth > 8 {
		return color.NRGBA64Model
	}
	return color.NRGBAModel
}
func (s *stream) Env() *pipeline.Env  { return s.env }
func (s *stream) Meta() pipeline.Meta { return s.meta }
func (s *stream) Opaque() bool        { return true }

func (s *stream) Close() error {
	// The helpers decoding ahead stop after the tiles they are on.
	q := &s.queue
	q.mu.Lock()
	q.limit = q.next
	waits := q.waits
	q.waits = nil
	q.mu.Unlock()
	defer func() { q.free = nil }()
	for _, wait := range waits {
		wait()
	}
	s.held = nil
	if s.single != nil {
		s.single.Release()
		s.single = nil
	}
	s.strip, s.scratch = nil, nil
	s.env.Budget.Release(s.reserve)
	s.reserve = 0
	return nil
}

// tileDecoder is a decoder carried across the tiles of a grid, as h265
// carries it: its buffers are kept, and the parameter sets the tiles share
// are fed to it once.
type tileDecoder struct {
	d   hevc.Decoder
	cfg *hvcConfig
}

func newTileDecoder(threads int) *tileDecoder {
	dec := &tileDecoder{}
	dec.d.FrameSizeLimit(maxTileSamples)
	dec.d.Threads(max(threads, 1))
	return dec
}

// decodeHEVC decodes an hvc1 item. After an error dec is not to be used
// again.
func decodeHEVC(dec *tileDecoder, h *heif, it *heifItem) (*hevc.Picture, error) {
	p := h.prop(it, "hvcC")
	data, err := h.itemData(it)
	if p == nil || p.hvcC == nil || err != nil {
		return nil, errHEIF
	}
	d := &dec.d
	if dec.cfg != p.hvcC {
		for _, raw := range p.hvcC.paramSets {
			u, ok := hevc.ParseNAL(raw)
			if !ok {
				return nil, errHEIF
			}
			if _, err := d.DecodeNAL(u); err != nil {
				return nil, err
			}
		}
		dec.cfg = p.hvcC
	}
	var out []*hevc.Picture
	for _, u := range hevc.SplitHVCC(data, p.hvcC.lengthSize) {
		pics, err := d.DecodeNAL(u)
		if err != nil {
			return nil, err
		}
		out = append(out, pics...)
	}
	out = append(out, d.Flush()...)
	if len(out) == 0 {
		return nil, errHEIF
	}
	for _, q := range out[1:] {
		q.Release()
	}
	// Luma and chroma of one depth, in the planes of that depth.
	if p := out[0]; p.BitDepthC != p.BitDepth && p.ChromaFormat != 0 || p.BitDepth < 8 || p.BitDepth > 16 ||
		p.BitDepth == 8 && p.Y == nil || p.BitDepth > 8 && p.Y16 == nil {
		out[0].Release()
		return nil, errHEIF
	}
	return out[0], nil
}

// setColour prepares the conversion once a picture of the image is known.
func (s *stream) setColour(p *hevc.Picture) error {
	if p.BitDepth != s.depth {
		return fmt.Errorf("%w: heic: a picture of %d bits, its configuration says %d", goimgtools.ErrFormat, p.BitDepth, s.depth)
	}
	c := colourOf{matrix: int(p.ColorMatrix), fullRange: p.FullRange}
	if s.colour != nil {
		c = *s.colour
	}
	var conv converter
	ok := false
	if s.depth > 8 {
		conv, ok = newRowConverter(s.rowsOf16, c, p.ChromaFormat, s.depth, s.w)
	} else {
		conv, ok = newRowConverter(s.rowsOf8, c, p.ChromaFormat, s.depth, s.w)
	}
	if !ok {
		// h265 refuses these too.
		return fmt.Errorf("%w: heic: unsupported colour matrix %d", goimgtools.ErrFormat, c.matrix)
	}
	s.conv, s.chroma = conv, p.ChromaFormat
	s.sw, s.sh = 1, 1
	switch p.ChromaFormat {
	case 1:
		s.sw, s.sh = 2, 2
	case 2:
		s.sw = 2
	}
	return nil
}

// sampleBytes is the size of a sample of the planes.
func (s *stream) sampleBytes() int64 {
	if s.depth > 8 {
		return 2
	}
	return 1
}

// decodeSingle decodes the single picture, which must be of the size ispe
// gives (h265 would cut a larger one to it).
func (s *stream) decodeSingle() error {
	// The planes, held while the pixels are handed out: reserved by the
	// size the file gives before they are decoded.
	sp, cp := s.file.prop(s.tiles[0], "ispe"), s.file.prop(s.tiles[0], "hvcC")
	need := int64(sp.w) * int64(sp.h) * [4]int64{2, 3, 4, 6}[cp.hvcC.chromaFormat] / 2 * s.sampleBytes()
	if err := s.env.Budget.Reserve(need, "heic planes"); err != nil {
		return err
	}
	s.reserve += need
	dec := newTileDecoder(s.env.Helpers() + 1)
	p, err := decodeHEVC(dec, s.file, s.tiles[0])
	// The decoder's own buffers (as large again as the planes) go now, not
	// when the picture does: the picture keeps the decoder.
	dec.d.Reset()
	if err != nil {
		return fmt.Errorf("%w: heic: %v", goimgtools.ErrFormat, err)
	}
	s.single = p
	if p.CropW < int(sp.w) || p.CropH < int(sp.h) {
		return fmt.Errorf("%w: heic: the picture is %dx%d, its ispe box says %dx%d", goimgtools.ErrFormat, p.CropW, p.CropH, sp.w, sp.h)
	}
	p.CropX += s.ox
	p.CropY += s.oy
	p.CropW, p.CropH = s.w, s.h
	return s.setColour(p)
}

// rowsOf8 and rowsOf16 give the converter its rows: of the single
// picture, or of the rows of tiles held.
func (s *stream) rowsOf8(plane, y int) []uint8 {
	if p := s.single; p != nil {
		return pictureRow(s, p, [3][]uint8{p.Y, p.Cb, p.Cr}, plane, y)
	}
	t, at := s.heldRow(plane, y)
	return [3][]uint8{t.y, t.cb, t.cr}[plane][at:]
}

func (s *stream) rowsOf16(plane, y int) []uint16 {
	if p := s.single; p != nil {
		return pictureRow(s, p, [3][]uint16{p.Y16, p.Cb16, p.Cr16}, plane, y)
	}
	t, at := s.heldRow(plane, y)
	return [3][]uint16{t.y16, t.cb16, t.cr16}[plane][at:]
}

// pictureRow is row y of a plane of the single picture, from its crop.
func pictureRow[T sample](s *stream, p *hevc.Picture, planes [3][]T, plane, y int) []T {
	if plane == 0 {
		return planes[0][(p.CropY+y)*p.StrideY+p.CropX:]
	}
	return planes[plane][(p.CropY/s.sh+y)*p.StrideC+p.CropX/s.sw:]
}

// heldRow is the row of tiles that row y of a plane is in, and where in its
// plane the row starts.
func (s *stream) heldRow(plane, y int) (*tileRow, int) {
	rowsPer := s.th
	if plane != 0 {
		rowsPer = s.th / s.sh
	}
	t := &s.held[(y/rowsPer)%s.slots]
	if plane == 0 {
		return t, (y % rowsPer) * t.ys
	}
	return t, (y % rowsPer) * t.cs
}

// window is how many rows of tiles are held at most: the two being read,
// and those decoded ahead.
const window = 4

// tileQueue hands out the tiles to decode in raster order, to the
// goroutine that needs a row and to the helpers that decode ahead, no
// further than the rows the window has room for.
type tileQueue struct {
	mu      sync.Mutex
	done    sync.Cond      // a tile is decoded
	next    int            // the next tile to decode
	limit   int            // the tiles below it may be decoded
	running int            // helpers working
	decoded []int          // tiles decoded, of the row in each slot
	err     []error        // the first error of the row in each slot
	waits   []func()       // of the helpers started
	free    []*tileDecoder // decoders not in use
}

// need has row r of tiles decoded and held, and lets the helpers decode
// ahead up to the rows the window has room for once rows before lo are no
// longer read.
func (s *stream) need(r, lo int) error {
	if s.tw == 0 {
		if err := s.firstTile(); err != nil {
			return err
		}
	}
	q := &s.queue
	q.mu.Lock()
	q.limit = max(q.limit, min(s.rows, lo+s.slots)*s.cols)
	for q.running < s.env.Helpers() && q.next < q.limit {
		wait, ok := s.env.TryGo(s.ahead)
		if !ok {
			break
		}
		q.running++
		q.waits = append(q.waits, wait)
	}
	// This goroutine decodes the tiles of row r left; once all are handed
	// out, it waits for those the helpers are on.
	for q.next/s.cols <= r {
		s.decodeNext()
	}
	defer q.mu.Unlock()
	for {
		if e := q.err[r%s.slots]; e != nil {
			return e
		}
		if q.decoded[r%s.slots] == s.cols {
			return nil
		}
		q.done.Wait()
	}
}

// ahead decodes tiles on a helper while there are tiles the window has room
// for.
func (s *stream) ahead() {
	q := &s.queue
	q.mu.Lock()
	for q.next < q.limit {
		s.decodeNext()
	}
	q.running--
	q.mu.Unlock()
}

// decodeNext decodes the next tile, with the queue locked (and unlocked
// while it is decoded).
func (s *stream) decodeNext() {
	q := &s.queue
	i := q.next
	q.next++
	r, c := i/s.cols, i%s.cols
	if c == 0 {
		// The slot's previous row, s.slots rows back, is no longer read:
		// all of its tiles were decoded before it was.
		q.decoded[r%s.slots], q.err[r%s.slots] = 0, nil
	}
	q.mu.Unlock()
	err := s.decodeTile(r, c)
	q.mu.Lock()
	q.decoded[r%s.slots]++
	if err != nil && q.err[r%s.slots] == nil {
		q.err[r%s.slots] = fmt.Errorf("%w: heic: %v", goimgtools.ErrFormat, err)
	}
	q.done.Broadcast()
}

// decodeTile decodes the tile in row r, column c, into its row's slot. A
// panic of the decoder is an error, so that the goroutines waiting for the
// row are not left waiting.
func (s *stream) decodeTile(r, c int) (err error) {
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("the decoder failed: %v", v)
		}
	}()
	// A decoder of those free, put back unless it failed.
	q := &s.queue
	q.mu.Lock()
	var dec *tileDecoder
	if n := len(q.free); n > 0 {
		dec, q.free = q.free[n-1], q.free[:n-1]
	}
	q.mu.Unlock()
	if dec == nil {
		dec = newTileDecoder(s.perTile())
	}
	p, err := decodeHEVC(dec, s.file, s.tiles[r*s.cols+c])
	if err != nil {
		return err
	}
	if p.CropW != s.tw || p.CropH != s.th || p.ChromaFormat != s.chroma || p.BitDepth != s.depth {
		err = errors.New("the tiles differ")
	} else {
		s.blit(&s.held[r%s.slots], p, r, c)
	}
	// The picture goes back to its decoder's pool before the decoder is
	// handed to another goroutine.
	p.Release()
	q.mu.Lock()
	q.free = append(q.free, dec)
	q.mu.Unlock()
	return err
}

// firstTile decodes the first tile, which gives the size of all, and makes
// room for the rows of tiles.
func (s *stream) firstTile() error {
	// Its planes, by the size the file gives, are reserved while it is
	// decoded.
	var pre int64
	if sp, cp := s.file.prop(s.tiles[0], "ispe"), s.file.prop(s.tiles[0], "hvcC"); sp != nil {
		pre = int64(sp.w) * int64(sp.h) * [4]int64{2, 3, 4, 6}[cp.hvcC.chromaFormat] / 2 * s.sampleBytes()
	}
	if err := s.env.Budget.Reserve(pre, "heic tile"); err != nil {
		return err
	}
	dec := newTileDecoder(s.perTile())
	p, err := decodeHEVC(dec, s.file, s.tiles[0])
	s.env.Budget.Release(pre)
	if err != nil {
		return fmt.Errorf("%w: heic: %v", goimgtools.ErrFormat, err)
	}
	defer func() {
		p.Release()
		s.queue.free = append(s.queue.free, dec)
	}()
	s.tw, s.th = p.CropW, p.CropH
	if s.tw <= 0 || s.th <= 0 || s.tw*s.cols < s.w || s.th*s.rows < s.h ||
		(s.cols-1)*s.tw >= s.w || (s.rows-1)*s.th >= s.h {
		return fmt.Errorf("%w: heic: the tiles do not cover the grid", goimgtools.ErrFormat)
	}
	if err := s.setColour(p); err != nil {
		return err
	}
	if s.th%s.sh != 0 {
		return fmt.Errorf("%w: heic: odd tiles", goimgtools.ErrFormat)
	}
	cw := (s.w + s.sw - 1) / s.sw
	if s.chroma == 0 {
		cw = 0
	}
	// The rows of tiles held, and the pictures of the tiles being decoded,
	// one for each goroutine: window rows when the memory limit has room
	// for them, down to the two that are read; all of them for an image
	// to be turned or flipped.
	one := (int64(s.w)*int64(s.th) + 2*int64(cw)*int64(s.th/s.sh)) * s.sampleBytes()
	tile := int64(len(p.Y)+len(p.Cb)+len(p.Cr)) + 2*int64(len(p.Y16)+len(p.Cb16)+len(p.Cr16))
	pics := int64(min(s.env.Helpers()+1, len(s.tiles))) * tile
	least := min(2, s.rows)
	s.slots = min(window, s.rows)
	if s.turned() {
		least, s.slots = s.rows, s.rows
	}
	for ; ; s.slots-- {
		need := int64(s.slots)*one + pics
		err := s.env.Budget.Reserve(need, "heic rows of tiles")
		if err == nil {
			s.reserve += need
			break
		}
		if s.slots <= least {
			return err
		}
	}
	s.held = make([]tileRow, s.slots)
	for i := range s.held {
		t := &s.held[i]
		if s.depth > 8 {
			t.y16, t.cb16, t.cr16 = make([]uint16, s.w*s.th), make([]uint16, cw*(s.th/s.sh)), make([]uint16, cw*(s.th/s.sh))
		} else {
			t.y, t.cb, t.cr = make([]uint8, s.w*s.th), make([]uint8, cw*(s.th/s.sh)), make([]uint8, cw*(s.th/s.sh))
		}
		t.ys, t.cs = s.w, cw
	}
	s.blit(&s.held[0], p, 0, 0)
	q := &s.queue
	q.done.L = &q.mu
	q.decoded, q.err = make([]int, s.slots), make([]error, s.slots)
	q.next, q.decoded[0] = 1, 1
	return nil
}

// turned: the image is turned or flipped (irot, imir).
func (s *stream) turned() bool { return s.turn != 0 || s.flip >= 0 }

// perTile is how many goroutines a tile coded in wavefronts may take: what
// is left over when the tiles of a row are decoded at once.
func (s *stream) perTile() int { return max((s.env.Helpers()+1)/s.cols, 1) }

// blit copies a tile into the row of tiles, clipped to the image.
func (s *stream) blit(t *tileRow, p *hevc.Picture, r, c int) {
	if s.depth > 8 {
		blitPlanes(s, [3][]uint16{t.y16, t.cb16, t.cr16}, [3][]uint16{p.Y16, p.Cb16, p.Cr16}, t, p, r, c)
		return
	}
	blitPlanes(s, [3][]uint8{t.y, t.cb, t.cr}, [3][]uint8{p.Y, p.Cb, p.Cr}, t, p, r, c)
}

func blitPlanes[T sample](s *stream, dsts, srcs [3][]T, t *tileRow, p *hevc.Picture, r, c int) {
	for pl := range 3 {
		sx, sy := c*s.tw, r*s.th
		w, h := s.tw, s.th
		ow, oh := s.w, s.h
		ss, ds := p.StrideY, t.ys
		ox, oy := p.CropX, p.CropY
		if pl != 0 {
			if s.chroma == 0 {
				return
			}
			sx, sy, w, h = sx/s.sw, sy/s.sh, w/s.sw, h/s.sh
			ow, oh = (s.w+s.sw-1)/s.sw, (s.h+s.sh-1)/s.sh
			ss, ds = p.StrideC, t.cs
			ox, oy = ox/s.sw, oy/s.sh
		}
		src, dst := srcs[pl], dsts[pl]
		w, h = min(w, ow-sx), min(h, oh-sy)
		for y := range max(h, 0) {
			copy(dst[y*ds+sx:][:w], src[(oy+y)*ss+ox:][:w])
		}
	}
}
func (s *stream) Next() (image.Image, error) {
	if s.err != nil {
		return nil, s.err
	}
	b := s.Bounds()
	ow, oh := b.Dx(), b.Dy()
	if s.y >= oh {
		return nil, io.EOF
	}
	rows := pipeline.StripRows(int64(s.bpp)*int64(ow), oh)
	if s.turn&1 != 0 {
		// A strip of a turned image is made from a band of columns of the
		// image, read row by row: a band of at least 128 (with 64, 3.5%
		// slower on a photograph of 12 megapixels).
		rows = max(rows, min(128, oh))
	}
	n := min(rows, oh-s.y)
	if s.strip == nil {
		// The strip, and what each goroutine converting rows writes; then
		// the rows of tiles, as many as the memory limit has room for.
		k := min(s.env.Helpers()+1, max(rows, s.h))
		need := int64(s.bpp)*int64(ow)*int64(rows) + int64(k)*scratchBytes(s.w, s.bpp)
		if err := s.env.Budget.Reserve(need, "heic strip"); err != nil {
			s.err = err
			return nil, err
		}
		s.reserve += need
		var err error
		if s.picture {
			err = s.decodeSingle()
		} else {
			// The first tile gives the tiles' size, and the colours.
			err = s.need(0, 0)
			// An image to be turned is decoded whole first.
			for r := 1; r < s.rows && s.turned() && err == nil; r++ {
				err = s.need(r, 0)
			}
		}
		if err != nil {
			s.err = err
			return nil, err
		}
		s.stride = s.bpp * ow
		s.strip = make([]uint8, s.stride*rows)
		s.scratch = make([]*rowScratch, k)
	}
	if s.turned() {
		s.convertTurned(n)
	} else {
		for i := 0; i < n; {
			// The rows that read no more than two rows of tiles, which are
			// held while they are converted.
			j := n
			if !s.picture {
				lo, hi := s.tileRows(s.y + i)
				for j = i + 1; j < n; j++ {
					_, h := s.tileRows(s.y + j)
					if h > lo+1 {
						break
					}
					hi = max(hi, h)
				}
				for _, r := range [2]int{lo, hi} {
					if err := s.need(r, lo); err != nil {
						s.err = err
						return nil, err
					}
				}
			}
			s.convertRows(i, j)
			i = j
		}
	}
	pix, rect := s.strip[:n*s.stride], image.Rect(0, s.y, ow, s.y+n)
	s.y += n
	if s.bpp == 8 {
		return &image.NRGBA64{Pix: pix, Stride: s.stride, Rect: rect}, nil
	}
	return &image.NRGBA{Pix: pix, Stride: s.stride, Rect: rect}, nil
}

// source is the line of the image row y of the output is: a row (byRow)
// or a column of it, and where along that line the output's x = 0 is and
// which way x goes. The image is turned anticlockwise by turn quarters and
// then flipped, as h265 does it.
func (s *stream) source(y int) (byRow bool, line, at0, step int) {
	w1, h1 := s.w, s.h
	if s.turn&1 != 0 {
		w1, h1 = s.h, s.w
	}
	// (x, y) of the output is (a*x + b, dy) of the turned image.
	dy, a, b := y, 1, 0
	switch s.flip {
	case 0:
		dy = h1 - 1 - y
	case 1:
		a, b = -1, w1-1
	}
	switch s.turn {
	case 0:
		return true, dy, b, a
	case 1:
		return false, s.w - 1 - dy, b, a
	case 2:
		return true, s.h - 1 - dy, s.w - 1 - b, -a
	}
	return false, dy, s.h - 1 - b, -a
}

// convertTurned makes the next n rows of a turned or flipped image: each
// from a row of the image, or for a quarter turn the n rows from a band of
// columns of all its rows; on as many goroutines as there are helpers free.
func (s *stream) convertTurned(n int) {
	stride, bpp := s.stride, s.bpp
	scratch := func(c int) *rowScratch {
		if s.scratch[c] == nil {
			s.scratch[c] = s.conv.newScratch()
		}
		return s.scratch[c]
	}
	if byRow, _, _, _ := s.source(s.y); byRow {
		k := min(len(s.scratch), n)
		s.env.Parallel(k, func(c int) {
			sc := scratch(c)
			for i := c * n / k; i < (c+1)*n/k; i++ {
				_, j, at0, step := s.source(s.y + i)
				dst := s.strip[i*stride:]
				if step == 1 {
					s.conv.convert(j, 0, s.w, s.h, dst, sc)
					continue
				}
				s.conv.convert(j, 0, s.w, s.h, sc.row, sc)
				for x := range s.w {
					copy(dst[bpp*x:bpp*x+bpp], sc.row[bpp*(at0-x):])
				}
			}
		})
		return
	}
	// Rows s.y to s.y+n of the output are columns lo to hi of the image.
	_, first, at0, step := s.source(s.y)
	_, last, _, _ := s.source(s.y + n - 1)
	lo, hi := min(first, last), max(first, last)+1
	col := make([]int, n) // of each output row, in the band
	for i := range col {
		_, c, _, _ := s.source(s.y + i)
		col[i] = bpp * (c - lo)
	}
	k := min(len(s.scratch), s.h)
	s.env.Parallel(k, func(c int) {
		sc := scratch(c)
		for j := c * s.h / k; j < (c+1)*s.h/k; j++ {
			s.conv.convert(j, lo, hi, s.h, sc.row, sc)
			at := bpp * (j - at0) * step
			for i, o := range col {
				copy(s.strip[i*stride+at:][:bpp], sc.row[o:])
			}
		}
	})
}

// tileRows are the first and last row of tiles that row y of the image is
// made from: its own, and for 4:2:0 that of the chroma row next to it.
func (s *stream) tileRows(y int) (int, int) {
	r := y / s.th
	if s.chroma == 0 {
		return r, r
	}
	a, b := s.conv.chromaRows(y, s.h)
	per := s.th / s.sh
	return min(r, a/per, b/per), max(r, a/per, b/per)
}

// convertRows converts rows a to b of the strip, on as many goroutines as
// there are helpers free.
func (s *stream) convertRows(a, b int) {
	k := min(len(s.scratch), b-a)
	s.env.Parallel(k, func(c int) {
		if s.scratch[c] == nil {
			s.scratch[c] = s.conv.newScratch()
		}
		for i := a + c*(b-a)/k; i < a+(c+1)*(b-a)/k; i++ {
			s.conv.convert(s.y+i, 0, s.w, s.h, s.strip[i*s.stride:], s.scratch[c])
		}
	})
}
