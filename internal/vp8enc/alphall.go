package vp8enc

import (
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"io"

	"github.com/whhhh1500/goimgtools/internal/vp8lenc"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// The alpha plane is coded the way libwebp codes it (src/enc/alpha_enc.c):
// through one of the format's four filters, as the green channel of a VP8L
// (WebP lossless) image coded by this module's lossless encoder, whose
// stream, less its five bytes of header (the size is the frame's), is the
// data of the ALPH chunk. That brings in everything the lossless encoder
// does: the colour table (an alpha plane has at most 256 values, packed
// several to a pixel when they are few), LZ77 at any distance, the colour
// cache and the Huffman codes of each region.
//
// The plane is coded with more than one filter and the smallest kept. The
// best filter is hard to foresee (libwebp's estimate,
// src/utils/filters_utils.c: WebPEstimateBestFilter, cannot tell the
// filters apart on smooth gradients, and the entropy of what they leave
// points the wrong way: the lossless encoder has predictors of its own):
// on icons, drawings and masks, no filter or the horizontal one has
// always come out smallest, so from effort 3 those two are tried, and from
// effort 7 all four, as libwebp does with -alpha_filter best. Below effort
// 3 the plane is coded once, with the filter libwebp's estimate gives. A
// plane of 16 values or fewer is coded without a filter only.
//
// The lossless encoder takes less effort on its own when memory is short,
// and when there is too little for it at all the plane is coded by the
// simple coder of alpha.go. So under a low memory limit the alpha chunk can
// come out larger than with the default one; the pixels are the same.

// alphaEffort is the lossless encoder's effort for alpha planes: the
// highest without the optimal parse, which on the large flat areas of
// alpha planes takes several times longer for files 10 to 30% smaller.
const alphaEffort = 4

// alphaBudget is the most memory the lossless encoder takes for an alpha
// plane. It is held for as long as the plane is coded, just after the
// image is read, when little else is.
const alphaBudget = 8 << 20

// The filters of the ALPH chunk.
const (
	alphaFilterNone = iota
	alphaFilterHorizontal
	alphaFilterVertical
	alphaFilterGradient
)

// alphaLL is a plane coded by the lossless encoder: its data in st, from
// off, n bytes long, with filter method.
type alphaLL struct {
	st     *store
	off, n int64
	method int
}

// codeAlpha codes the alpha plane with the lossless encoder, when it has
// transparent pixels, and keeps the result for write. When memory is too
// short for the lossless encoder it leaves the plane to alpha.go's coder.
func (e *encoder) codeAlpha() error {
	if e.alpha == nil || !e.alpha.transparent() {
		return nil
	}
	methods := []int{alphaFilterNone}
	switch {
	case e.alpha.colours() <= 16:
	case e.effort >= 7:
		// As libwebp with -alpha_filter best: every filter.
		methods = append(methods, alphaFilterHorizontal, alphaFilterVertical, alphaFilterGradient)
	case e.effort >= 3:
		methods = append(methods, alphaFilterHorizontal)
	default:
		methods[0] = e.alpha.bestFilter()
	}
	if e.dbg != nil && e.dbg.alphaMethod > 0 {
		methods = []int{e.dbg.alphaMethod - 1}
	}
	for _, m := range methods {
		ll, err := e.codeAlphaWith(m)
		if errors.Is(err, pipeline.ErrMemoryLimit) {
			continue
		}
		if err != nil {
			return err
		}
		if e.alphaLL == nil || ll.n < e.alphaLL.n {
			if e.alphaLL != nil {
				e.alphaLL.st.Close()
			}
			e.alphaLL = ll
		} else {
			ll.st.Close()
		}
	}
	return nil
}

func (e *encoder) codeAlphaWith(method int) (*alphaLL, error) {
	rows := pipeline.StripRows(4*int64(e.w), e.h)
	need := 4*int64(e.w)*int64(rows) + 2*int64(e.w)
	if err := e.env.Budget.Reserve(need, "webp alpha strip"); err != nil {
		return nil, err
	}
	defer e.env.Budget.Release(need)
	// The lossless encoder gets a share of the budget of its own, at most
	// alphaBudget, and fits what it does to that (alpha planes of up to a
	// few megapixels come out the same as with all it would take).
	share := min(alphaBudget, e.env.Budget.Limit()-e.env.Budget.Used())
	if share <= 0 {
		return nil, pipeline.ErrMemoryLimit
	}
	if err := e.env.Budget.Reserve(share, "webp alpha coder"); err != nil {
		return nil, err
	}
	defer e.env.Budget.Release(share)
	env := pipeline.NewEnv(share, e.env.TempDir)
	src := &alphaSource{sp: e.alpha, env: env, w: e.w, h: e.h, method: method, rows: rows,
		pix: make([]uint8, 4*e.w*rows), cur: make([]uint8, e.w), prev: make([]uint8, e.w)}
	e.alpha.rewind(e.readBuf)
	st := newStore(e.env)
	if err := vp8lenc.Encode(st, src, &vp8lenc.Options{Effort: alphaEffort}); err != nil {
		st.Close()
		return nil, err
	}
	// RIFF, size, WEBP, VP8L, size: the chunk's data follows at 20, and
	// its first five bytes are the VP8L header.
	var hdr [25]byte
	if _, err := st.ReadAt(hdr[:], 0); err != nil && err != io.EOF {
		st.Close()
		return nil, err
	}
	if string(hdr[12:16]) != "VP8L" || hdr[20] != 0x2f {
		st.Close()
		return nil, errors.New("goimgtools: webp: unexpected output of the lossless encoder")
	}
	size := int64(binary.LittleEndian.Uint32(hdr[16:20]))
	return &alphaLL{st: st, off: 25, n: size - 5, method: method}, nil
}

// alphaSource gives the alpha plane, read from the spool and filtered, as
// the green channel of opaque NRGBA strips.
type alphaSource struct {
	sp        *alphaSpool
	env       *pipeline.Env
	w, h, y   int
	method    int
	rows      int
	pix       []uint8
	cur, prev []uint8
	strip     image.NRGBA
	firstDone bool
}

func (a *alphaSource) Bounds() image.Rectangle { return image.Rect(0, 0, a.w, a.h) }
func (a *alphaSource) ColorModel() color.Model { return color.NRGBAModel }
func (a *alphaSource) Env() *pipeline.Env      { return a.env }
func (a *alphaSource) Opaque() bool            { return true }
func (a *alphaSource) Close() error            { return nil }

func (a *alphaSource) Next() (image.Image, error) {
	if a.y >= a.h {
		return nil, io.EOF
	}
	n := min(a.rows, a.h-a.y)
	a.strip = image.NRGBA{Pix: a.pix[:4*a.w*n], Stride: 4 * a.w, Rect: image.Rect(0, a.y, a.w, a.y+n)}
	for k := 0; k < n; k++ {
		a.prev, a.cur = a.cur, a.prev
		if err := a.sp.next(a.cur); err != nil {
			return nil, err
		}
		filterAlphaRow(a.cur, a.prev, a.y+k == 0, a.method, a.pix[4*a.w*k:4*a.w*(k+1)])
	}
	a.y += n
	return &a.strip, nil
}

// filterAlphaRow writes row cur, filtered with method against the row above
// (prev, unless first), as the green channel of opaque pixels in out
// (libwebp's src/dsp/filters.c: the first row is predicted from the left,
// the first column from above, the top left value from 0).
func filterAlphaRow(cur, prev []uint8, first bool, method int, out []uint8) {
	put := func(x int, v uint8) {
		o := out[4*x : 4*x+4]
		o[0], o[1], o[2], o[3] = 0, v, 0, 0xff
	}
	if method == alphaFilterNone {
		for x, v := range cur {
			put(x, v)
		}
		return
	}
	if first {
		put(0, cur[0])
		for x := 1; x < len(cur); x++ {
			put(x, cur[x]-cur[x-1])
		}
		return
	}
	switch method {
	case alphaFilterHorizontal:
		put(0, cur[0]-prev[0])
		for x := 1; x < len(cur); x++ {
			put(x, cur[x]-cur[x-1])
		}
	case alphaFilterVertical:
		for x, v := range cur {
			put(x, v-prev[x])
		}
	default:
		put(0, cur[0]-prev[0])
		for x := 1; x < len(cur); x++ {
			g := int(cur[x-1]) + int(prev[x]) - int(prev[x-1])
			put(x, cur[x]-uint8(min(max(g, 0), 255)))
		}
	}
}

// writeAlphaLL copies the coded plane to w.
func (e *encoder) writeAlphaLL(w io.Writer) error {
	ll := e.alphaLL
	off, n := ll.off, ll.n
	for n > 0 {
		k := int(min(n, int64(len(e.readBuf))))
		if _, err := ll.st.ReadAt(e.readBuf[:k], off); err != nil && err != io.EOF {
			return err
		}
		if _, err := w.Write(e.readBuf[:k]); err != nil {
			return err
		}
		off += int64(k)
		n -= int64(k)
	}
	return nil
}
