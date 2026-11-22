package tiffstream

import (
	"bytes"
	"image"
	"image/draw"
	"io"

	"github.com/whhhh1500/goimgtools/internal/jpegstream"
	"github.com/whhhh1500/goimgtools/internal/webpstream"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// goimgtools addition: JPEG compression (Compression 7, "new-style" JPEG
// in TIFF, as libtiff, libvips and sharp write it). Every strip or tile is
// a JPEG stream of its own; the tables they share (JPEGTables) are put in
// front of it. The pixels come out as the rows of the block: grey levels,
// or red, green and blue (the JPEG's YCbCr turned into RGB as for a JPEG
// file).

// jpegRows reads a block coded as JPEG as uncompressed rows.
type jpegRows struct {
	src   pipeline.Source
	gray  bool
	alpha bool   // four samples a pixel, of a WebP block
	buf   []byte // decoded bytes not handed out yet
	out   []byte
}

// newJPEGRows starts decoding the n bytes at off as a JPEG stream, with
// the shared tables in front.
func newJPEGRows(r io.ReaderAt, off, n int64, tables []byte, gray bool, env *pipeline.Env) (*jpegRows, error) {
	var in io.Reader = io.NewSectionReader(r, off, n)
	if len(tables) >= 4 && tables[0] == 0xff && tables[1] == 0xd8 {
		// The tables without their end (EOI), then the block without
		// its start (SOI).
		t := tables
		if t[len(t)-2] == 0xff && t[len(t)-1] == 0xd9 {
			t = t[:len(t)-2]
		}
		var head [2]byte
		if _, err := r.ReadAt(head[:], off); err == nil && head[0] == 0xff && head[1] == 0xd8 {
			in = io.MultiReader(bytes.NewReader(t), io.NewSectionReader(r, off+2, n-2))
		}
	}
	src, err := jpegstream.Open(in, env)
	if err != nil {
		return nil, err
	}
	if !gray {
		// NRGBA strips, YCbCr made RGB as for every JPEG.
		src = pipeline.Map(src, func(*image.NRGBA) error { return nil })
	}
	return &jpegRows{src: src, gray: gray}, nil
}

// newWebPRows starts decoding the n bytes at off as a WebP image, as
// libtiff stores a block with WebP compression: rows of RGB, or with alpha
// of RGBA.
func newWebPRows(r io.ReaderAt, off, n int64, alpha bool, env *pipeline.Env) (*jpegRows, error) {
	src, err := webpstream.Open(io.NewSectionReader(r, off, n), n, env)
	if err != nil {
		return nil, err
	}
	// To RGB as libwebp converts, which libtiff has it do.
	src = pipeline.Map(webpstream.ToRGB(src), func(*image.NRGBA) error { return nil })
	return &jpegRows{src: src, alpha: alpha}, nil
}

func (j *jpegRows) Read(p []byte) (int, error) {
	for len(j.buf) == 0 {
		s, err := j.src.Next()
		if err != nil {
			return 0, err // io.EOF at the end of the block
		}
		j.buf = j.rows(s)
	}
	n := copy(p, j.buf)
	j.buf = j.buf[n:]
	return n, nil
}

// rows turns a decoded strip into the bytes of its rows.
func (j *jpegRows) rows(s image.Image) []byte {
	r := s.Bounds()
	w := r.Dx()
	if j.gray {
		g, ok := s.(*image.Gray)
		if !ok {
			g = image.NewGray(r)
			draw.Draw(g, r, s, r.Min, draw.Src)
		}
		j.out = j.out[:0]
		for y := r.Min.Y; y < r.Max.Y; y++ {
			j.out = append(j.out, g.Pix[g.PixOffset(r.Min.X, y):][:w]...)
		}
		return j.out
	}
	m := s.(*image.NRGBA)
	if j.alpha {
		j.out = j.out[:0]
		for y := r.Min.Y; y < r.Max.Y; y++ {
			j.out = append(j.out, m.Pix[m.PixOffset(r.Min.X, y):][:4*w]...)
		}
		return j.out
	}
	if cap(j.out) < 3*w*r.Dy() {
		j.out = make([]byte, 0, 3*w*r.Dy())
	}
	j.out = j.out[:0]
	for y := r.Min.Y; y < r.Max.Y; y++ {
		row := m.Pix[m.PixOffset(r.Min.X, y):][:4*w]
		for x := 0; x < len(row); x += 4 {
			j.out = append(j.out, row[x], row[x+1], row[x+2])
		}
	}
	return j.out
}

func (j *jpegRows) Close() error { return j.src.Close() }
