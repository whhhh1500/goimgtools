// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the licenses/LICENSE-GO file.

package pngstream

import (
	"bufio"
	"compress/zlib"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"io"
	"strconv"

	"github.com/whhhh1500/goimgtools/internal/deflate"
	"github.com/whhhh1500/goimgtools/pipeline"
)

type encoder struct {
	env    *pipeline.Env // for the budget and the helpers of compression
	w      io.Writer
	bounds image.Rectangle             // of the whole image
	next   func() (image.Image, error) // the strips, top to bottom
	level  int                         // zlib level
	cb     int
	err    error
	header [8]byte
	footer [4]byte
	tmp    [4 * 256]byte
	cr     [nFilter][]uint8
	pr     []uint8
	bw     *bufio.Writer
}

// The absolute value of a byte interpreted as a signed int8. Without a
// branch: on a photograph the sign is as good as random.
func abs8(d uint8) int {
	return abs(int(int8(d)))
}

func (e *encoder) writeChunk(b []byte, name string) {
	if e.err != nil {
		return
	}
	n := uint32(len(b))
	if int(n) != len(b) {
		e.err = UnsupportedError(name + " chunk is too large: " + strconv.Itoa(len(b)))
		return
	}
	binary.BigEndian.PutUint32(e.header[:4], n)
	e.header[4] = name[0]
	e.header[5] = name[1]
	e.header[6] = name[2]
	e.header[7] = name[3]
	crc := crc32.NewIEEE()
	crc.Write(e.header[4:8])
	crc.Write(b)
	binary.BigEndian.PutUint32(e.footer[:4], crc.Sum32())

	_, e.err = e.w.Write(e.header[:8])
	if e.err != nil {
		return
	}
	_, e.err = e.w.Write(b)
	if e.err != nil {
		return
	}
	_, e.err = e.w.Write(e.footer[:4])
}

func (e *encoder) writeIHDR() {
	b := e.bounds
	binary.BigEndian.PutUint32(e.tmp[0:4], uint32(b.Dx()))
	binary.BigEndian.PutUint32(e.tmp[4:8], uint32(b.Dy()))
	// Set bit depth and color type.
	switch e.cb {
	case cbG8:
		e.tmp[8] = 8
		e.tmp[9] = ctGrayscale
	case cbTC8:
		e.tmp[8] = 8
		e.tmp[9] = ctTrueColor
	case cbP8:
		e.tmp[8] = 8
		e.tmp[9] = ctPaletted
	case cbP4:
		e.tmp[8] = 4
		e.tmp[9] = ctPaletted
	case cbP2:
		e.tmp[8] = 2
		e.tmp[9] = ctPaletted
	case cbP1:
		e.tmp[8] = 1
		e.tmp[9] = ctPaletted
	case cbTCA8:
		e.tmp[8] = 8
		e.tmp[9] = ctTrueColorAlpha
	case cbG16:
		e.tmp[8] = 16
		e.tmp[9] = ctGrayscale
	case cbTC16:
		e.tmp[8] = 16
		e.tmp[9] = ctTrueColor
	case cbTCA16:
		e.tmp[8] = 16
		e.tmp[9] = ctTrueColorAlpha
	}
	e.tmp[10] = 0 // default compression method
	e.tmp[11] = 0 // default filter method
	e.tmp[12] = 0 // non-interlaced
	e.writeChunk(e.tmp[:13], "IHDR")
}

func (e *encoder) writePLTEAndTRNS(p color.Palette) {
	if len(p) < 1 || len(p) > 256 {
		e.err = FormatError("bad palette length: " + strconv.Itoa(len(p)))
		return
	}
	last := -1
	for i, c := range p {
		c1 := color.NRGBAModel.Convert(c).(color.NRGBA)
		e.tmp[3*i+0] = c1.R
		e.tmp[3*i+1] = c1.G
		e.tmp[3*i+2] = c1.B
		if c1.A != 0xff {
			last = i
		}
		e.tmp[3*256+i] = c1.A
	}
	e.writeChunk(e.tmp[:3*len(p)], "PLTE")
	if last != -1 {
		e.writeChunk(e.tmp[3*256:3*256+1+last], "tRNS")
	}
}

// An encoder is an io.Writer that satisfies writes by writing PNG IDAT chunks,
// including an 8-byte header and 4-byte CRC checksum per Write call. Such calls
// should be relatively infrequent, since writeIDATs uses a [bufio.Writer].
//
// This method should only be called from writeIDATs (via writeImage).
// No other code should treat an encoder as an io.Writer.
func (e *encoder) Write(b []byte) (int, error) {
	e.writeChunk(b, "IDAT")
	if e.err != nil {
		return 0, e.err
	}
	return len(b), nil
}

// Chooses the filter to use for encoding the current row, and applies it.
// The return value is the index of the filter and also of the row in cr that has had it applied.
//
// It makes the choice of image/png's filter function, in its order, and
// writes the same bytes; what differs is how the loops are written. A
// filter that cannot win any more is given up at the end of a block of
// bytes instead of at the byte where its sum passes the best one (the sum
// only grows, so the verdict is the same), which leaves the inner loops
// without a test, and the Paeth predictor picks its value without
// branches.
func filter(cr *[nFilter][]byte, pr []byte, bpp int) int {
	if useSIMD {
		if f, ok := filterSIMD(cr, pr, bpp); ok {
			return f
		}
	}
	return filterGo(cr, pr, bpp)
}

// filterSIMD is filter with filterRunSIMD for the bulk of the row: every
// filter is worked out over the whole row (filterGo stops one as soon as
// it cannot win), which chooses the same one, filtered the same way. It
// reports whether the row was long enough.
func filterSIMD(cr *[nFilter][]byte, pr []byte, bpp int) (int, bool) {
	cdat0 := cr[0][1:]
	n := len(cdat0)
	chunks := (n - bpp) / 32
	if chunks <= 0 {
		return 0, false
	}
	cdat1, cdat2, cdat3, cdat4 := cr[1][1:][:n], cr[2][1:][:n], cr[3][1:][:n], cr[4][1:][:n]
	pdat := pr[1:][:n]
	var sums [5]uint64 // up, paeth, none, sub, average
	add := func(k int, d uint8) { sums[k] += uint64(abs8(d)) }
	for i := 0; i < bpp; i++ {
		c, u := cdat0[i], pdat[i]
		cdat2[i], cdat4[i], cdat1[i], cdat3[i] = c-u, c-u, c, c-u/2
		add(0, c-u)
		add(1, c-u)
		add(2, c)
		add(3, c)
		add(4, c-u/2)
	}
	end := bpp + 32*chunks
	filterRunSIMD(&cdat0[bpp], &pdat[bpp], &cdat2[bpp], &cdat1[bpp], &cdat3[bpp], &cdat4[bpp], bpp, chunks, &sums)
	for i := end; i < n; i++ {
		c, u, l, ul := int(cdat0[i]), int(pdat[i]), int(cdat0[i-bpp]), int(pdat[i-bpp])
		pa, pb := u-ul, l-ul
		pc := abs(pa + pb)
		pa, pb = abs(pa), abs(pb)
		pred := ul
		if pb <= pc {
			pred = u
		}
		if pa <= min(pb, pc) {
			pred = l
		}
		cdat2[i], cdat4[i], cdat1[i], cdat3[i] = uint8(c-u), uint8(c-pred), uint8(c-l), uint8(c-(l+u)/2)
		add(0, cdat2[i])
		add(1, cdat4[i])
		add(2, cdat0[i])
		add(3, cdat1[i])
		add(4, cdat3[i])
	}
	f, best := ftUp, sums[0]
	for k, ft := range [...]int{ftPaeth, ftNone, ftSub, ftAverage} {
		if sums[k+1] < best {
			f, best = ft, sums[k+1]
		}
	}
	return f, true
}

// filterGo is filter in Go.
func filterGo(cr *[nFilter][]byte, pr []byte, bpp int) int {
	// We try all five filter types, and pick the one that minimizes the sum of absolute differences.
	// This is the same heuristic that libpng uses, although the filters are attempted in order of
	// estimated most likely to be minimal (ftUp, ftPaeth, ftNone, ftSub, ftAverage), rather than
	// in their enumeration order (ftNone, ftSub, ftUp, ftAverage, ftPaeth).
	cdat0 := cr[0][1:]
	n := len(cdat0)
	cdat1 := cr[1][1:][:n]
	cdat2 := cr[2][1:][:n]
	cdat3 := cr[3][1:][:n]
	cdat4 := cr[4][1:][:n]
	pdat := pr[1:][:n]
	const block = 64

	// The up filter.
	sum := 0
	for i, c := range cdat0 {
		d := c - pdat[i]
		cdat2[i] = d
		sum += abs8(d)
	}
	best := sum
	filter := ftUp

	// The Paeth filter.
	sum = 0
	for i := 0; i < bpp; i++ {
		cdat4[i] = cdat0[i] - pdat[i]
		sum += abs8(cdat4[i])
	}
	for i := bpp; i < n && sum < best; {
		end := min(i+block, n)
		cur, left, up, upLeft, out := cdat0[i:end], cdat0[i-bpp:end-bpp], pdat[i:end], pdat[i-bpp:end-bpp], cdat4[i:end]
		left, up, upLeft, out = left[:len(cur)], up[:len(cur)], upLeft[:len(cur)], out[:len(cur)]
		for j, v := range cur {
			a, b, c := int(left[j]), int(up[j]), int(upLeft[j])
			pa := b - c
			pb := a - c
			pc := abs(pa + pb)
			pa = abs(pa)
			pb = abs(pb)
			pred := c
			if pb <= pc {
				pred = b
			}
			if pa <= min(pb, pc) {
				pred = a
			}
			d := v - uint8(pred)
			out[j] = d
			sum += abs8(d)
		}
		i = end
	}
	if sum < best {
		best = sum
		filter = ftPaeth
	}

	// The none filter.
	sum = 0
	for i := 0; i < n && sum < best; {
		end := min(i+block, n)
		for _, v := range cdat0[i:end] {
			sum += abs8(v)
		}
		i = end
	}
	if sum < best {
		best = sum
		filter = ftNone
	}

	// The sub filter.
	sum = 0
	for i := 0; i < bpp; i++ {
		cdat1[i] = cdat0[i]
		sum += abs8(cdat1[i])
	}
	for i := bpp; i < n && sum < best; {
		end := min(i+block, n)
		cur, left, out := cdat0[i:end], cdat0[i-bpp:end-bpp], cdat1[i:end]
		left, out = left[:len(cur)], out[:len(cur)]
		for j, v := range cur {
			d := v - left[j]
			out[j] = d
			sum += abs8(d)
		}
		i = end
	}
	if sum < best {
		best = sum
		filter = ftSub
	}

	// The average filter.
	sum = 0
	for i := 0; i < bpp; i++ {
		cdat3[i] = cdat0[i] - pdat[i]/2
		sum += abs8(cdat3[i])
	}
	for i := bpp; i < n && sum < best; {
		end := min(i+block, n)
		cur, left, up, out := cdat0[i:end], cdat0[i-bpp:end-bpp], pdat[i:end], cdat3[i:end]
		left, up, out = left[:len(cur)], up[:len(cur)], out[:len(cur)]
		for j, v := range cur {
			d := v - uint8((int(left[j])+int(up[j]))/2)
			out[j] = d
			sum += abs8(d)
		}
		i = end
	}
	if sum < best {
		filter = ftAverage
	}

	return filter
}

// cbBits is the bits of a pixel of the colour type and depth.
func cbBits(cb int) int {
	switch cb {
	case cbG8, cbP8:
		return 8
	case cbTC8:
		return 24
	case cbP4:
		return 4
	case cbP2:
		return 2
	case cbP1:
		return 1
	case cbTCA8:
		return 32
	case cbTC16:
		return 48
	case cbTCA16:
		return 64
	case cbG16:
		return 16
	}
	return 0
}

// rowBytes is the bytes of a row of the image, its filter type byte
// included.
func (e *encoder) rowBytes(cb int) int {
	return 1 + (cbBits(cb)*e.bounds.Dx()+7)/8
}

func (e *encoder) writeImage(w io.Writer, cb int, level int) error {
	bitsPerPixel := cbBits(cb)

	// cr[*] and pr are the bytes for the current and previous row.
	// cr[0] is unfiltered (or equivalently, filtered with the ftNone filter).
	// cr[ft], for non-zero filter types ft, are buffers for transforming cr[0] under the
	// other PNG filter types. These buffers are allocated once and re-used for each row.
	// The +1 is for the per-row filter type, which is at cr[*][0].
	b := e.bounds
	sz := e.rowBytes(cb)
	for i := range e.cr {
		if cap(e.cr[i]) < sz {
			e.cr[i] = make([]uint8, sz)
		} else {
			e.cr[i] = e.cr[i][:sz]
		}
		e.cr[i][0] = uint8(i)
	}
	cr := e.cr

	// Compressed in blocks, on the helpers of the pipeline where it has
	// any; the same file either way (see zBlocks).
	zw, err := newZBlocks(w, level, e.env, int64(b.Dy())*int64(sz))
	if zw != nil {
		defer zw.release()
	}
	if err != nil {
		return err
	}
	if cap(e.pr) < sz {
		e.pr = make([]uint8, sz)
	} else {
		e.pr = e.pr[:sz]
		clear(e.pr)
	}
	pr := e.pr

	// From level 7 the filters are tried against no filter, a segment at a
	// time (see filterTrial); up to 6 the file is the one png.Encode writes.
	filtering := level != zlib.NoCompression && cb != cbP8 && cb != cbP4 && cb != cbP2 && cb != cbP1
	var trial *filterTrial
	var brute *bruteFilter
	if filtering && level >= 8 && !testNoTrial {
		chain := bruteChain
		if level >= 9 {
			chain = bruteChain9
		}
		if brute, err = newBruteFilter(e.env, sz, chain, level >= zopfliLevel, zw.ahead()); err != nil {
			return err
		}
		defer brute.release()
	}
	if filtering && level >= 7 && !testNoTrial {
		var reals []*deflate.Compressor
		if brute != nil {
			reals = brute.reals
		}
		if trial, err = newFilterTrial(e.env, sz, segmentBytes(level), brute != nil, reals); err != nil {
			return err
		}
		defer trial.release()
	}

	// The standard library loops over the rows of one image here; the rows
	// now come strip by strip. sb is the current strip's bounds.
	for {
		m, err := e.next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		sb := m.Bounds()
		gray, _ := m.(*image.Gray)
		rgba, _ := m.(*image.RGBA)
		paletted, _ := m.(*image.Paletted)
		nrgba, _ := m.(*image.NRGBA)

		for y := sb.Min.Y; y < sb.Max.Y; y++ {
			// Convert from colors to bytes.
			i := 1
			switch cb {
			case cbG8:
				if gray != nil {
					offset := (y - sb.Min.Y) * gray.Stride
					copy(cr[0][1:], gray.Pix[offset:offset+b.Dx()])
				} else {
					for x := b.Min.X; x < b.Max.X; x++ {
						c := color.GrayModel.Convert(m.At(x, y)).(color.Gray)
						cr[0][i] = c.Y
						i++
					}
				}
			case cbTC8:
				// We have previously verified that the alpha value is fully opaque.
				cr0 := cr[0]
				stride, pix := 0, []byte(nil)
				if rgba != nil {
					stride, pix = rgba.Stride, rgba.Pix
				} else if nrgba != nil {
					stride, pix = nrgba.Stride, nrgba.Pix
				}
				if stride != 0 {
					j0 := (y - sb.Min.Y) * stride
					j1 := j0 + b.Dx()*4
					for j := j0; j < j1; j += 4 {
						cr0[i+0] = pix[j+0]
						cr0[i+1] = pix[j+1]
						cr0[i+2] = pix[j+2]
						i += 3
					}
				} else {
					for x := b.Min.X; x < b.Max.X; x++ {
						r, g, b, _ := m.At(x, y).RGBA()
						cr0[i+0] = uint8(r >> 8)
						cr0[i+1] = uint8(g >> 8)
						cr0[i+2] = uint8(b >> 8)
						i += 3
					}
				}
			case cbP8:
				if paletted != nil {
					offset := (y - sb.Min.Y) * paletted.Stride
					copy(cr[0][1:], paletted.Pix[offset:offset+b.Dx()])
				} else {
					pi := m.(image.PalettedImage)
					for x := b.Min.X; x < b.Max.X; x++ {
						cr[0][i] = pi.ColorIndexAt(x, y)
						i += 1
					}
				}

			case cbP4, cbP2, cbP1:
				pi := m.(image.PalettedImage)

				var a uint8
				var c int
				pixelsPerByte := 8 / bitsPerPixel
				for x := b.Min.X; x < b.Max.X; x++ {
					a = a<<uint(bitsPerPixel) | pi.ColorIndexAt(x, y)
					c++
					if c == pixelsPerByte {
						cr[0][i] = a
						i += 1
						a = 0
						c = 0
					}
				}
				if c != 0 {
					for c != pixelsPerByte {
						a = a << uint(bitsPerPixel)
						c++
					}
					cr[0][i] = a
				}

			case cbTCA8:
				if nrgba != nil {
					offset := (y - sb.Min.Y) * nrgba.Stride
					copy(cr[0][1:], nrgba.Pix[offset:offset+b.Dx()*4])
				} else if rgba != nil {
					dst := cr[0][1:]
					src := rgba.Pix[rgba.PixOffset(b.Min.X, y):rgba.PixOffset(b.Max.X, y)]
					for ; len(src) >= 4; dst, src = dst[4:], src[4:] {
						d := (*[4]byte)(dst)
						s := (*[4]byte)(src)
						if s[3] == 0x00 {
							d[0] = 0
							d[1] = 0
							d[2] = 0
							d[3] = 0
						} else if s[3] == 0xff {
							copy(d[:], s[:])
						} else {
							// This code does the same as color.NRGBAModel.Convert(
							// rgba.At(x, y)).(color.NRGBA) but with no extra memory
							// allocations or interface/function call overhead.
							//
							// The multiplier m combines 0x101 (which converts
							// 8-bit color to 16-bit color) and 0xffff (which, when
							// combined with the division-by-a, converts from
							// alpha-premultiplied to non-alpha-premultiplied).
							const m = 0x101 * 0xffff
							a := uint32(s[3]) * 0x101
							d[0] = uint8((uint32(s[0]) * m / a) >> 8)
							d[1] = uint8((uint32(s[1]) * m / a) >> 8)
							d[2] = uint8((uint32(s[2]) * m / a) >> 8)
							d[3] = s[3]
						}
					}
				} else {
					// Convert from image.Image (which is alpha-premultiplied) to PNG's non-alpha-premultiplied.
					for x := b.Min.X; x < b.Max.X; x++ {
						c := color.NRGBAModel.Convert(m.At(x, y)).(color.NRGBA)
						cr[0][i+0] = c.R
						cr[0][i+1] = c.G
						cr[0][i+2] = c.B
						cr[0][i+3] = c.A
						i += 4
					}
				}
			case cbG16:
				for x := b.Min.X; x < b.Max.X; x++ {
					c := color.Gray16Model.Convert(m.At(x, y)).(color.Gray16)
					cr[0][i+0] = uint8(c.Y >> 8)
					cr[0][i+1] = uint8(c.Y)
					i += 2
				}
			case cbTC16:
				// We have previously verified that the alpha value is fully opaque.
				for x := b.Min.X; x < b.Max.X; x++ {
					r, g, b, _ := m.At(x, y).RGBA()
					cr[0][i+0] = uint8(r >> 8)
					cr[0][i+1] = uint8(r)
					cr[0][i+2] = uint8(g >> 8)
					cr[0][i+3] = uint8(g)
					cr[0][i+4] = uint8(b >> 8)
					cr[0][i+5] = uint8(b)
					i += 6
				}
			case cbTCA16:
				// Convert from image.Image (which is alpha-premultiplied) to PNG's non-alpha-premultiplied.
				for x := b.Min.X; x < b.Max.X; x++ {
					c := color.NRGBA64Model.Convert(m.At(x, y)).(color.NRGBA64)
					cr[0][i+0] = uint8(c.R >> 8)
					cr[0][i+1] = uint8(c.R)
					cr[0][i+2] = uint8(c.G >> 8)
					cr[0][i+3] = uint8(c.G)
					cr[0][i+4] = uint8(c.B >> 8)
					cr[0][i+5] = uint8(c.B)
					cr[0][i+6] = uint8(c.A >> 8)
					cr[0][i+7] = uint8(c.A)
					i += 8
				}
			}

			// Apply the filter.
			// Skip filter for NoCompression and paletted images (cbP8) as
			// "filters are rarely useful on palette images" and will result
			// in larger files (see http://www.libpng.org/pub/png/book/chapter09.html).
			f := ftNone
			if filtering {
				// Since we skip paletted images we don't have to worry about
				// bitsPerPixel not being a multiple of 8
				bpp := bitsPerPixel / 8
				f = filter(&cr, pr, bpp)
			}

			// Write the compressed bytes.
			if trial != nil {
				var b []byte
				if brute != nil {
					b = brute.choose(cr[0][1:], pr[1:], bitsPerPixel/8)
				}
				if err := trial.add(cr[0], cr[f], b, zw); err != nil {
					return err
				}
			} else if _, err := zw.Write(cr[f]); err != nil {
				return err
			}

			// The current row for y is the previous row for y+1.
			pr, cr[0] = cr[0], pr
		}
	}
	if trial != nil {
		if err := trial.flush(zw); err != nil {
			return err
		}
	}
	return zw.Close()
}

// idatBytes is the most of the zlib stream levels 8 to 10 write in one IDAT
// chunk: a stream of up to that is one chunk, as ECT and oxipng write it,
// and APNG frames are written in chunks of the same size (see
// chunkWriter.data). Up to level 7 the chunks are of 32KB, as png.Encode
// writes them, or of the size of the compressor's larger writes. Each chunk
// takes 12 bytes: on the 17 test images level 9 is 0.023% smaller with
// chunks of 1MB, level 10 0.007%.
const idatBytes = 1 << 20

// idatChunker writes the zlib stream in IDAT chunks of cap(buf) bytes, the
// last shorter: where the chunks end depends on the stream alone.
type idatChunker struct {
	e   *encoder
	buf []byte
}

func (c *idatChunker) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 && c.e.err == nil {
		if len(c.buf) == cap(c.buf) {
			c.flush()
		}
		k := copy(c.buf[len(c.buf):cap(c.buf)], p)
		c.buf, p = c.buf[:len(c.buf)+k], p[k:]
	}
	if c.e.err != nil {
		return 0, c.e.err
	}
	return n, nil
}

func (c *idatChunker) flush() {
	if len(c.buf) > 0 {
		c.e.writeChunk(c.buf, "IDAT")
		c.buf = c.buf[:0]
	}
}

// Write the actual image data to one or more IDAT chunks.
func (e *encoder) writeIDATs() {
	if e.err != nil {
		return
	}
	if e.level >= 8 {
		// The chunk is made no longer than the stream can be: the data,
		// and stored blocks' 5 bytes in every 64KB, the zlib header and
		// checksum.
		raw := int64(e.bounds.Dy()) * int64(e.rowBytes(e.cb))
		n := min(idatBytes, raw+raw/1024+64)
		if e.err = e.env.Budget.Reserve(n, "png data chunk"); e.err != nil {
			return
		}
		defer e.env.Budget.Release(n)
		c := &idatChunker{e: e, buf: make([]byte, 0, n)}
		if err := e.writeImage(c, e.cb, e.level); err != nil {
			if e.err == nil {
				e.err = err
			}
			return
		}
		c.flush()
		return
	}
	if e.bw == nil {
		e.bw = bufio.NewWriterSize(e, 1<<15)
	} else {
		e.bw.Reset(e)
	}
	e.err = e.writeImage(e.bw, e.cb, e.level)
	if e.err != nil {
		return
	}
	e.err = e.bw.Flush()
}

func (e *encoder) writeIEND() { e.writeChunk(nil, "IEND") }
