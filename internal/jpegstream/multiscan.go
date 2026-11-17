package jpegstream

import (
	"bytes"
	"io"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// A progressive image, or one whose components are in separate scans, has
// every scan cover the whole image. The standard library keeps the
// coefficients of every block between scans (scanAll does so in a
// pipeline.Scratch, a temporary file for a large image). With random access
// to the input the scans can instead be decoded in step: a first pass finds
// where each scan's entropy-coded data starts, and each MCU row is then
// decoded by every scan in turn, each read from its own place in the input
// by a cursor, into the coefficients of that one row, which then become
// pixels. Each scan visits the blocks in raster order of MCU rows (or of
// block rows, for a scan of one component, which are the same rows), so the
// row has every scan's contribution in the right order.

// maxCursors is the most scans decoded in step; an image with more is
// decoded scan after scan into the coefficient store: the first maxCursors
// by their cursors, the rest as scanAll reads them.
var maxCursors = 1024 // a variable for the tests

// testNoStep, in tests, has every planned scan read into the coefficient
// store, as when the memory for the row is short.
var testNoStep bool

// cursor is the reading state of one scan.
type cursor struct {
	r      io.Reader
	bytes  *byteBuf // allocated while the scan is being read
	bits   bits
	eobRun uint16
	scan   scanState
	huff   [maxTc + 1][maxTh + 1]*huffman
	ri     int
}

// cursorBytes is what a cursor holds while it is read; tableBytes is a
// Huffman table, which the cursors of the scans after its definition keep.
const (
	cursorBytes = 4400
	tableBytes  = 12800
)

// unused reports whether a scan changes nothing that the pixels are made
// from: a block decoded to one pixel (an image shrunk by 8, but for chroma
// libjpeg decodes at twice that) is its DC coefficient over 8, and a scan of
// AC coefficients of such blocks can go unread (a corrupt one then goes
// unnoticed).
func (d *decoder) unused(sc *scanState) bool {
	if sc.zigStart == 0 {
		return false
	}
	for i := 0; i < sc.nComp; i++ {
		if d.cscale[sc.comps[i].compIndex] != 3 {
			return false
		}
	}
	return true
}

// enter makes the decoder read c's scan.
func (d *decoder) enter(c *cursor) {
	if c.bytes == nil {
		c.bytes = new(byteBuf)
	}
	d.r, d.bytes, d.bits, d.eobRun, d.scan, d.huff, d.ri = c.r, c.bytes, c.bits, c.eobRun, c.scan, c.huff, c.ri
}

// leave keeps what reading c's scan changed.
func (d *decoder) leave(c *cursor) {
	c.bits, c.eobRun, c.scan = d.bits, d.eobRun, d.scan
}

// WithRandomAccess gives a JPEG Source from Open random access to its input,
// which lets a progressive image be decoded without storing its coefficients
// (multiscan.go). ra returns the whole input, in which Open's reader started
// at offset 0. It is called once, when the pixels are first asked for, and
// only for such an image.
func WithRandomAccess(src pipeline.Source, ra func() (io.ReaderAt, int64, error)) {
	if s, ok := src.(*stream); ok {
		s.ra = ra
	}
}

// skipEntropy moves past a scan's entropy-coded data to the next marker
// other than a restart marker, and leaves that marker unread: where the
// standard library's nextSegment finds the next segment after a scan, since
// decoding a scan stops at the first such marker.
func (d *decoder) skipEntropy() error {
	for {
		b := d.bytes
		if b.i == b.j {
			if err := d.fill(); err != nil {
				return err
			}
			continue
		}
		k := bytes.IndexByte(b.buf[b.i:b.j], 0xff)
		if k < 0 {
			b.i = b.j
			continue
		}
		i := b.i + k
		if i+1 == b.j {
			// fill keeps the last two bytes, so the 0xff stays at
			// buf[b.i-1].
			b.i = b.j
			for b.i == b.j {
				if err := d.fill(); err != nil {
					return err
				}
			}
			i = b.i - 1
		}
		// A marker, fill bytes before one included, is where nextSegment
		// takes over (and finds fault with what the standard library
		// does).
		if m := b.buf[i+1]; m == 0x00 || rst0Marker <= m && m <= rst7Marker {
			b.i = i + 2
		} else {
			b.i = i
			return nil
		}
	}
}

// planScans reads through the image from its first scan to the End Of Image
// marker, making a cursor for each scan. It reports false, having read
// nothing, if ra's bytes are not those Open's reader read. It stops at the
// scan after maxCursors, or once the cursors and the tables they keep take
// more than the share of the memory limit that may be staged, with s.rest
// set to read the rest from Open's reader.
func (s *stream) planScans(ra io.ReaderAt, size int64) (bool, error) {
	d := s.d
	off := d.offset()
	// The scan header (FF DA, the length, 6+2n bytes) ends where the
	// data starts, and the data is what the buffer holds.
	head := int64(8 + 2*d.scan.nComp)
	want := d.bytes.buf[d.bytes.i:min(d.bytes.j, d.bytes.i+16)]
	got := make([]byte, 2+len(want))
	if off < head || off+int64(len(want)) > size {
		return false, nil
	}
	if _, err := ra.ReadAt(got[:2], off-head); err != nil || got[0] != 0xff || got[1] != sosMarker {
		return false, nil
	}
	if _, err := ra.ReadAt(got[2:], off); err != nil || !bytes.Equal(got[2:], want) {
		return false, nil
	}
	tables0 := d.tables
	share := s.env.Budget.Limit() / pipeline.StagingShare
	for {
		off := d.offset()
		s.held = int64(len(s.cursors))*cursorBytes + int64(d.tables-tables0)*tableBytes
		if len(s.cursors) == maxCursors || s.held > share {
			s.rest = &cursor{r: d.r, bytes: d.bytes, bits: d.bits, eobRun: d.eobRun, scan: d.scan, huff: d.huff, ri: d.ri}
			return true, nil
		}
		s.cursors = append(s.cursors, &cursor{
			r: io.NewSectionReader(ra, off, size-off), scan: d.scan, huff: d.huff, ri: d.ri})
		if err := d.skipEntropy(); err != nil {
			return true, err
		}
		for {
			sos, eoi, err := d.nextSegment()
			if err != nil {
				return true, err
			}
			if eoi {
				return true, nil
			}
			if sos {
				break
			}
		}
	}
}

// startScans decodes the scans in step when the input allows it, and
// otherwise reads them all into the coefficient store (scanAll).
func (s *stream) startScans() error {
	d := s.d
	if s.ra == nil {
		return s.scanAll(nil)
	}
	ra, size, err := s.ra()
	if err != nil {
		return s.scanAll(nil)
	}
	ok, err := s.planScans(ra, size)
	if err != nil {
		return err
	}
	if !ok {
		return s.scanAll(nil)
	}
	// The coefficients of one MCU row, and the cursors' buffers.
	var row int64
	for i := 0; i < d.nComp; i++ {
		row += int64(d.mxx*d.comp[i].h*d.comp[i].v) * (4*blockSize + 8)
	}
	need := row + s.held
	if s.rest != nil || testNoStep || !s.env.Budget.TryReserve(need) {
		return s.scanAll(s.cursors)
	}
	s.reserved += need
	for i := 0; i < d.nComp; i++ {
		d.rowCoef[i] = make([]block, d.mxx*d.comp[i].h*d.comp[i].v)
		d.rowNZ[i] = make([]uint64, len(d.rowCoef[i]))
	}
	return nil
}

// stepRows decodes MCU rows [my0, my1) by every scan and makes their pixels
// in the band.
func (s *stream) stepRows(my0, my1 int) error {
	d := s.d
	if d.img1 != nil {
		clear(d.img1.Pix)
	} else {
		clear(d.img3.Y)
		clear(d.img3.Cb)
		clear(d.img3.Cr)
		clear(d.blackPix)
	}
	for my := my0; my < my1; my++ {
		for i := range d.rowCoef {
			clear(d.rowCoef[i])
			clear(d.rowNZ[i])
		}
		for _, c := range s.cursors {
			if d.unused(&c.scan) {
				continue
			}
			d.enter(c)
			err := d.scanRow(my)
			d.leave(c)
			if err != nil {
				return err
			}
			d.overrun = d.overrun || my == d.myy-1 && c.eobRun != 0
		}
		for i := 0; i < d.nComp; i++ {
			if !d.touched[i] {
				continue
			}
			v := 8 * d.maxV / d.comp[i].v
			h := 8 * d.maxH / d.comp[i].h
			vi, stride := d.comp[i].v, d.mxx*d.comp[i].h
			for by := my * vi; by < (my+1)*vi && by*v < d.height; by++ {
				for bx := 0; bx*h < d.width; bx++ {
					k := (by-my*vi)*stride + bx
					b := &d.rowCoef[i][k]
					d.dequantizeNZ(b, d.rowNZ[i][k], i)
					d.dcOnly = d.rowNZ[i][k]&^1 == 0
					if err := d.reconstructBlock(b, bx, by-my0*vi, i); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}
