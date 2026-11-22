package tiffstream

import (
	"bytes"
	"compress/zlib"
	"errors"
	"image"
	"image/color"
	"io"
	"math"

	"github.com/whhhh1500/goimgtools/internal/density"
	"github.com/whhhh1500/goimgtools/internal/jpegstream"
	"github.com/whhhh1500/goimgtools/internal/tiffstream/lzw"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// goimgtools addition: a TIFF in tiles, and a pyramid of them, as libvips
// writes them (tiffsave with tile and pyramid): the image cut into square
// tiles, each compressed on its own, so that a reader takes the part it
// wants; and after it the image at half the size, a quarter, and so on
// down to one that fits in a tile, each a directory of its own marked as a
// reduced image, which is what an image server or a zooming viewer reads.
//
// Samples are 8 bits: grey, RGB, or RGB with alpha (16-bit images are
// written with their high bytes). A row of tiles of each level is held in
// memory while it fills (the tile side times the width times the bytes of
// a pixel; the levels of a pyramid together as much again), counted in the
// budget; the compressed tiles of each level go to working storage until
// the image is whole, since a level's place in the file follows the ones
// before it.

const tNewSubfileType = 254

// tileLevel is the image at one size of the pyramid.
type tileLevel struct {
	w, h int
	// band is the rows of the tile row being filled; rows how many it has,
	// y how many rows went before it.
	band    []byte
	rows, y int
	sp      *pipeline.Spill
	// Where each tile is in sp, and how long.
	offsets, counts []uint32
	// For the level below: the first row of a pair, and the row made of
	// the pair.
	pair     []byte
	havePair bool
	half     []byte
	next     *tileLevel
}

// tiler cuts rows into tiles.
type tiler struct {
	env       *pipeline.Env
	tile, spp int
	comp      uint32
	predict   bool
	quality   int
	sub       int    // jpegstream's Subsample420 or Subsample444
	block     []byte // the samples of a tile
	rgba      []byte // a colour tile as the JPEG encoder takes it
	zw        *zlib.Writer
	lw        *lzw.Writer
	jbuf      bytes.Buffer
	tables    []byte
	levels    []*tileLevel
}

// encodeTiled writes src as a tiled TIFF, with the reduced levels of a
// pyramid when o.Pyramid.
func encodeTiled(w io.Writer, src pipeline.Source, o *Options) error {
	b := src.Bounds()
	dx, dy := b.Dx(), b.Dy()
	if dx <= 0 || dy <= 0 {
		return errZeroSize
	}
	tile := o.Tile
	if tile == 0 {
		tile = 256
	}
	if tile < 16 || tile > 8192 || tile%16 != 0 {
		return errors.New("goimgtools: tiff: a tile's side is a multiple of 16, from 16 to 8192")
	}
	env := pipeline.EnvOf(src)
	t := &tiler{env: env, tile: tile, comp: o.Compression.specValue(), quality: o.Quality}
	gray := false
	switch src.ColorModel() {
	case color.GrayModel, color.Gray16Model:
		gray = true
	}
	opaque := gray || pipeline.IsOpaque(src)
	switch {
	case gray:
		t.spp = 1
	case opaque:
		t.spp = 3
	default:
		t.spp = 4
	}
	if o.Compression == JPEG {
		t.comp = cJPEG
		if !opaque {
			return errJPEGAlpha
		}
		if t.quality == 0 {
			t.quality = DefaultJPEGQuality
		}
		if t.quality < 1 || t.quality > 100 {
			return errors.New("goimgtools: tiff: JPEG quality must be 1 to 100")
		}
		t.sub = jpegstream.Subsample420
		if t.quality >= 90 || gray {
			t.sub = jpegstream.Subsample444
		}
	}
	t.predict = o.Predictor && (t.comp == cDeflate || t.comp == cLZW)

	// The levels: the image, and for a pyramid its halves down to one
	// that fits in a tile.
	var need int64
	for lw, lh := dx, dy; ; lw, lh = max(1, lw/2), max(1, lh/2) {
		l := &tileLevel{w: lw, h: lh}
		need += int64(lw) * int64(t.spp) * int64(min(tile, lh))
		if n := len(t.levels); n > 0 {
			t.levels[n-1].next = l
			need += 2 * int64(lw) * int64(t.spp) // the pair's first row above, the row made of it
		}
		t.levels = append(t.levels, l)
		if !o.Pyramid || (lw <= tile && lh <= tile) {
			break
		}
	}
	need += int64(tile) * int64(tile) * int64(t.spp)
	if t.comp == cJPEG && !gray {
		need += int64(tile) * int64(tile) * 4
	}
	// A strip of a kind that is not written as it is, converted a row at
	// a time.
	need += 4 * int64(dx)
	if err := env.Budget.Reserve(need, "tiff tile rows"); err != nil {
		return err
	}
	defer env.Budget.Release(need)
	for i, l := range t.levels {
		l.band = make([]byte, l.w*t.spp*min(tile, l.h))
		l.sp = pipeline.NewSpill(env)
		defer l.sp.Close()
		if i > 0 {
			up := t.levels[i-1]
			up.pair, up.half = make([]byte, up.w*t.spp), make([]byte, l.w*t.spp)
		}
	}
	t.block = make([]byte, tile*tile*t.spp)
	if t.comp == cJPEG && !gray {
		t.rgba = make([]byte, tile*tile*4)
	}

	// The rows, as the samples written.
	rd := &stripReader{src: src, b: b, y: b.Min.Y}
	row := make([]byte, dx*t.spp)
	conv := make([]byte, 4*dx)
	extra := uint32(0)
	for {
		m, err := rd.next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		sb := m.Bounds()
		for y := sb.Min.Y; y < sb.Max.Y; y++ {
			switch p := m.(type) {
			case *image.Gray:
				copy(row, p.Pix[p.PixOffset(sb.Min.X, y):][:dx])
			case *image.Gray16:
				px := p.Pix[p.PixOffset(sb.Min.X, y):][:2*dx]
				for x := range row {
					row[x] = px[2*x]
				}
			default:
				var px []byte
				switch p := m.(type) {
				case *image.NRGBA:
					px, extra = p.Pix[p.PixOffset(sb.Min.X, y):][:4*dx], 2
				case *image.RGBA:
					px, extra = p.Pix[p.PixOffset(sb.Min.X, y):][:4*dx], 1
				default:
					genericRow(conv, m, y)
					px, extra = conv, 1
				}
				switch t.spp {
				case 4:
					copy(row, px)
				case 3:
					for x := 0; x < dx; x++ {
						row[3*x], row[3*x+1], row[3*x+2] = px[4*x], px[4*x+1], px[4*x+2]
					}
				default: // a grey model whose strips are not grey: its red
					for x := range row {
						row[x] = px[4*x]
					}
				}
			}
			if err := t.add(t.levels[0], row); err != nil {
				return err
			}
		}
	}
	if l := t.levels[0]; l.y+l.rows != l.h {
		return io.ErrUnexpectedEOF
	}
	for _, l := range t.levels {
		// A level of one row: the level below is that row, halved across.
		if nx := l.next; nx != nil && l.havePair && nx.y+nx.rows < nx.h {
			if err := t.pairUp(l, l.pair); err != nil {
				return err
			}
		}
		if l.rows > 0 {
			if err := t.flush(l); err != nil {
				return err
			}
		}
	}

	// The file: the header, the tiles of each level, the directories.
	at := int64(8)
	starts := make([]int64, len(t.levels))
	for i, l := range t.levels {
		starts[i] = at
		at += l.sp.Size() + l.sp.Size()&1
	}
	res := []uint32{72, 1}
	if o.Density > 0 {
		num, den := density.Rational(o.Density)
		res = []uint32{num, den}
	}
	sizeType := func(v int) int {
		if v > math.MaxUint16 {
			return dtLong
		}
		return dtShort
	}
	var dirs [][]byte
	first := at
	for i, l := range t.levels {
		for k := range l.offsets {
			l.offsets[k] += uint32(starts[i])
		}
		photometric, bps := uint32(pRGB), []uint32{8, 8, 8, 8}[:t.spp]
		if gray {
			photometric = pBlackIsZero
		} else if t.comp == cJPEG {
			photometric = pYCbCr
		}
		ifd := []ifdEnt{
			{tag: tImageWidth, datatype: sizeType(l.w), data: []uint32{uint32(l.w)}},
			{tag: tImageLength, datatype: sizeType(l.h), data: []uint32{uint32(l.h)}},
			{tag: tBitsPerSample, datatype: dtShort, data: bps},
			{tag: tCompression, datatype: dtShort, data: []uint32{t.comp}},
			{tag: tPhotometricInterpretation, datatype: dtShort, data: []uint32{photometric}},
			{tag: tSamplesPerPixel, datatype: dtShort, data: []uint32{uint32(t.spp)}},
			{tag: tTileWidth, datatype: dtShort, data: []uint32{uint32(tile)}},
			{tag: tTileLength, datatype: dtShort, data: []uint32{uint32(tile)}},
			{tag: tTileOffsets, datatype: dtLong, data: l.offsets},
			{tag: tTileByteCounts, datatype: dtLong, data: l.counts},
		}
		if t.spp == 4 {
			ifd = append(ifd, ifdEnt{tag: tExtraSamples, datatype: dtShort, data: []uint32{extra}})
		}
		if t.predict {
			ifd = append(ifd, ifdEnt{tag: tPredictor, datatype: dtShort, data: []uint32{prHorizontal}})
		}
		if t.comp == cJPEG {
			ifd = append(ifd, ifdEnt{tag: tJPEGTables, datatype: dtUndefined, raw: t.tables})
			if !gray {
				s := uint32(2)
				if t.sub == jpegstream.Subsample444 {
					s = 1
				}
				ifd = append(ifd,
					ifdEnt{tag: tYCbCrSubSampling, datatype: dtShort, data: []uint32{s, s}},
					ifdEnt{tag: tReferenceBlackWhite, datatype: dtRational, data: []uint32{0, 1, 255, 1, 128, 1, 255, 1, 128, 1, 255, 1}})
			}
		}
		if i == 0 {
			ifd = append(ifd,
				ifdEnt{tag: tXResolution, datatype: dtRational, data: res},
				ifdEnt{tag: tYResolution, datatype: dtRational, data: res},
				ifdEnt{tag: tResolutionUnit, datatype: dtShort, data: []uint32{resPerInch}})
			if len(o.ICC) > 0 && len(o.ICC) <= pipeline.MaxMetaBytes {
				ifd = append(ifd, ifdEnt{tag: tICCProfile, datatype: dtUndefined, raw: o.ICC})
			}
			if len(o.Xmp) > 0 && len(o.Xmp) <= pipeline.MaxMetaBytes {
				ifd = append(ifd, ifdEnt{tag: tXMP, datatype: dtByte, raw: o.Xmp})
			}
			if len(o.Iptc) > 0 && len(o.Iptc) <= pipeline.MaxMetaBytes {
				ifd = append(ifd, ifdEnt{tag: tIPTC, datatype: dtUndefined, raw: o.Iptc})
			}
			ifd = append(ifd, exifEntries(o.Exif)...)
		} else {
			ifd = append(ifd, ifdEnt{tag: tNewSubfileType, datatype: dtLong, data: []uint32{1}}) // a reduced image
		}
		dir := layoutIFD(ifd, at)
		if len(dir)%2 == 1 {
			dir = append(dir, 0)
		}
		if at += int64(len(dir)); i < len(t.levels)-1 {
			// The directory after it.
			enc.PutUint32(dir[2+ifdLen*len(ifd):], uint32(at))
		}
		dirs = append(dirs, dir)
	}
	if at > math.MaxUint32 {
		return errTooLarge
	}
	var hdr [8]byte
	copy(hdr[:], leHeader)
	enc.PutUint32(hdr[4:], uint32(first))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	if err := env.Budget.Reserve(copyBuf, "tiff copy buffer"); err != nil {
		return err
	}
	defer env.Budget.Release(copyBuf)
	buf := make([]byte, copyBuf)
	for _, l := range t.levels {
		size := l.sp.Size()
		for off := int64(0); off < size; {
			k := int(min(int64(len(buf)), size-off))
			if _, err := l.sp.ReadAt(buf[:k], off); err != nil {
				if err == io.EOF {
					err = io.ErrUnexpectedEOF
				}
				return err
			}
			if _, err := w.Write(buf[:k]); err != nil {
				return err
			}
			off += int64(k)
		}
		if size&1 == 1 {
			if _, err := w.Write([]byte{0}); err != nil {
				return err
			}
		}
	}
	for _, dir := range dirs {
		if _, err := w.Write(dir); err != nil {
			return err
		}
	}
	return nil
}

// add gives level l its next row; a full row of tiles is written, and
// every two rows make one of the level below (each sample the mean of the
// four above it, as libvips shrinks a pyramid's levels).
func (t *tiler) add(l *tileLevel, row []byte) error {
	n := l.w * t.spp
	if l.y+l.rows >= l.h {
		return nil // beyond what the level has (the level above was odd)
	}
	copy(l.band[l.rows*n:], row[:n])
	l.rows++
	if l.next != nil {
		if !l.havePair {
			copy(l.pair, row[:n])
			l.havePair = true
		} else if err := t.pairUp(l, row); err != nil {
			return err
		}
	}
	if l.rows == t.tile || l.y+l.rows == l.h {
		return t.flush(l)
	}
	return nil
}

// pairUp makes of the row kept of level l and the row after it a row of
// the level below.
func (t *tiler) pairUp(l *tileLevel, row []byte) error {
	l.havePair = false
	nx, out := l.next, l.half
	for x := 0; x < nx.w; x++ {
		a, b := l.pair[2*x*t.spp:], row[2*x*t.spp:]
		// (A level one pixel wide has no second column.)
		s := t.spp
		if 2*x+1 >= l.w {
			s = 0
		}
		for c := 0; c < t.spp; c++ {
			out[x*t.spp+c] = uint8((int(a[c]) + int(a[s+c]) + int(b[c]) + int(b[s+c]) + 2) >> 2)
		}
	}
	return t.add(nx, out)
}

// flush writes the tiles of the rows level l holds: each tile whole, what
// is beyond the image a repeat of its last column and row.
func (t *tiler) flush(l *tileLevel) error {
	n, tile, spp := l.w*t.spp, t.tile, t.spp
	for x0 := 0; x0 < l.w; x0 += tile {
		cols := min(tile, l.w-x0)
		for r := 0; r < tile; r++ {
			src := l.band[min(r, l.rows-1)*n+x0*spp:][:cols*spp]
			dst := t.block[r*tile*spp:][:tile*spp]
			copy(dst, src)
			for k := cols * spp; k < len(dst); k++ {
				dst[k] = dst[k-spp]
			}
		}
		at := l.sp.Size()
		if err := t.write(l.sp); err != nil {
			return err
		}
		if l.sp.Size() > math.MaxUint32 {
			return errTooLarge
		}
		l.offsets = append(l.offsets, uint32(at))
		l.counts = append(l.counts, uint32(l.sp.Size()-at))
	}
	l.y += l.rows
	l.rows = 0
	return nil
}

// write compresses the tile in t.block into sp.
func (t *tiler) write(sp *pipeline.Spill) error {
	tile, spp := t.tile, t.spp
	switch t.comp {
	case cNone:
		_, err := sp.Write(t.block)
		return err
	case cJPEG:
		var img image.Image = &image.Gray{Pix: t.block, Stride: tile, Rect: image.Rect(0, 0, tile, tile)}
		if spp == 3 {
			for i, j := 0, 0; i < len(t.block); i, j = i+3, j+4 {
				t.rgba[j], t.rgba[j+1], t.rgba[j+2], t.rgba[j+3] = t.block[i], t.block[i+1], t.block[i+2], 0xff
			}
			img = &image.RGBA{Pix: t.rgba, Stride: 4 * tile, Rect: image.Rect(0, 0, tile, tile)}
		}
		t.jbuf.Reset()
		if err := jpegstream.Encode(&t.jbuf, &tileSource{img: img, env: t.env}, &jpegstream.Options{Quality: t.quality, Subsampling: t.sub}); err != nil {
			return err
		}
		tables, head, scan, err := splitTables(t.jbuf.Bytes())
		if err != nil {
			return err
		}
		if t.tables == nil {
			t.tables = tables
		} else if !bytes.Equal(t.tables, tables) {
			return errors.New("goimgtools: tiff: the JPEG tiles have different tables")
		}
		if _, err := sp.Write(head); err != nil {
			return err
		}
		_, err = sp.Write(scan)
		return err
	}
	if t.predict {
		for r := 0; r < tile; r++ {
			row := t.block[r*tile*spp:][:tile*spp]
			for i := len(row) - 1; i >= spp; i-- {
				row[i] -= row[i-spp]
			}
		}
	}
	var wc io.WriteCloser
	if t.comp == cLZW {
		if t.lw == nil {
			t.lw = lzw.NewWriter(sp)
		} else {
			t.lw.Reset(sp)
		}
		wc = t.lw
	} else {
		if t.zw == nil {
			t.zw = zlib.NewWriter(sp)
		} else {
			t.zw.Reset(sp)
		}
		wc = t.zw
	}
	if _, err := wc.Write(t.block); err != nil {
		return err
	}
	return wc.Close()
}

// tileSource hands a tile to the JPEG encoder as one strip.
type tileSource struct {
	img  image.Image
	env  *pipeline.Env
	done bool
}

func (s *tileSource) Bounds() image.Rectangle { return s.img.Bounds() }
func (s *tileSource) ColorModel() color.Model { return s.img.ColorModel() }
func (s *tileSource) Env() *pipeline.Env      { return s.env }
func (s *tileSource) Opaque() bool            { return true }
func (s *tileSource) Close() error            { return nil }

func (s *tileSource) Next() (image.Image, error) {
	if s.done {
		return nil, io.EOF
	}
	s.done = true
	return s.img, nil
}
