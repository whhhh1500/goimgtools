package gifstream

import (
	"errors"
	"image"
	"image/color"
	"io"
	"slices"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// An animation whose frames each have at most 256 colours, but more than
// 256 together, is written without loss with a palette per frame (a local
// colour table), as GIF makers that choose a palette for every frame write
// them, instead of with one palette chosen for all the frames, which would
// change pixels and, the colours being fewer than the frames need, make
// the file larger too. frameTee counts the colours of the frames as the
// quantizer reads them, so that this can be decided once they are all
// read; the frames are then written from the rows the quantizer stored
// (quant's Stored), not from a copy of them.

// noColour is the key of a transparent pixel.
const noColour = 0

// colourKey is the key of a pixel made all or nothing (see store).
func colourKey(r, g, b, a uint8) uint32 {
	if a < 0x80 {
		return noColour
	}
	return 1<<24 | uint32(r)<<16 | uint32(g)<<8 | uint32(b)
}

// frameTee passes the frames (one under the other, as animFrames hands them
// out, with what stays as it was made transparent) on to the quantizer,
// and counts the colours written of each.
type frameTee struct {
	src pipeline.Source
	fh  int
	// For each frame: the colours of its changed pixels (up to 257), and
	// whether it has transparent pixels.
	written []int16
	transp  []bool
	set     map[uint32]struct{} // of the frame being read
	setK    int
	all     map[uint32]struct{} // of every frame, up to 257
}

func newFrameTee(src pipeline.Source, fh, frames int) *frameTee {
	return &frameTee{src: src, fh: fh, all: map[uint32]struct{}{},
		written: make([]int16, frames), transp: make([]bool, frames), set: map[uint32]struct{}{}, setK: -1}
}

func (t *frameTee) Env() *pipeline.Env      { return t.src.Env() }
func (t *frameTee) Bounds() image.Rectangle { return t.src.Bounds() }
func (t *frameTee) ColorModel() color.Model { return t.src.ColorModel() }
func (t *frameTee) Close() error            { return t.src.Close() }

func (t *frameTee) Next() (image.Image, error) {
	m, err := t.src.Next()
	if err != nil {
		return m, err
	}
	p := m.(*image.NRGBA)
	b := p.Rect
	for y := b.Min.Y; y < b.Max.Y; y++ {
		row := p.Pix[p.PixOffset(b.Min.X, y):][:4*b.Dx()]
		if k := y / t.fh; k != t.setK {
			clear(t.set)
			t.setK = k
		}
		for i := 0; i < len(row); i += 4 {
			c := colourKey(row[i], row[i+1], row[i+2], row[i+3])
			if c == noColour {
				t.transp[t.setK] = true
			} else if len(t.set) <= 256 {
				t.set[c] = struct{}{}
			}
			if len(t.all) <= 256 {
				t.all[c] = struct{}{}
			}
		}
		t.written[t.setK] = int16(len(t.set))
	}
	return m, nil
}

// local tells whether the frames are to be written with palettes of their
// own: together more than 256 colours, but each frame can be written
// without loss in 256 (see writeLocal).
func (t *frameTee) local(infos []frameInfo) bool {
	if len(t.all) <= 256 {
		return false // one palette loses nothing
	}
	for k, in := range infos {
		n := int(t.written[k])
		if t.transp[k] {
			n++
		}
		if n > 256 && (!in.diff || in.colours > 256) {
			return false
		}
	}
	return true
}

// writeLocal writes the animation from the frames as stored (their rows of
// RGBA, one frame under the other), each frame with a palette of its own:
// every pixel as it is. A frame written as what changed is
// written whichever way of the three bestFrame tries codes smallest, of
// those whose colours fit in a palette; the colours of the pixels left as
// they were are what the picture shows. plan says how each frame is
// written, as EncodeAnimation decides it.
func (t *frameTee) writeLocal(w io.Writer, env *pipeline.Env, stored *pipeline.Spill, o AnimOptions, fw int, infos []frameInfo, plan func(k int) (image.Rectangle, byte, int)) error {
	aw, err := NewAnimWriter(w, AnimConfig{Width: fw, Height: t.fh, LoopCount: o.LoopCount, Lossy: o.Lossy, Effort: o.Effort})
	if err != nil {
		return err
	}
	px := int64(fw) * int64(t.fh)
	need := 4*px + 4*px + 3*px + int64(fw)*4 + 64<<10
	if err := env.Budget.Reserve(need, "animation frame"); err != nil {
		return err
	}
	defer env.Budget.Release(need)
	screen := make([]uint32, px) // what the picture shows
	keys := make([]uint32, 0, px)
	row := make([]byte, 4*fw)
	var bufs [3][]byte
	rd := pipeline.NewSeqReader(stored, 0, stored.Size(), 64<<10)
	prevDisposal := byte(disposalBackground)
	for k := range infos {
		rect, disposal, delay := plan(k)
		keys = keys[:0]
		for y := 0; y < t.fh; y++ {
			if err := rd.ReadFull(row); err != nil {
				return err
			}
			if y < rect.Min.Y || y >= rect.Max.Y {
				continue
			}
			seg := row[4*rect.Min.X : 4*rect.Max.X]
			for i := 0; i < len(seg); i += 4 {
				keys = append(keys, colourKey(seg[i], seg[i+1], seg[i+2], seg[i+3]))
			}
		}
		if infos[k].merged {
			continue // the frame before shows longer
		}
		// The ways to write the frame, as colour keys: as it is, and, for
		// a frame of changes, with no transparency or with it over runs.
		ways := [][]uint32{keys}
		if k > 0 && infos[k].diff && prevDisposal == disposalNone {
			opaque, runs := waysOf(keys, rect, screen, fw)
			ways = append(ways, opaque, runs)
		}
		var best []byte
		var bestPal color.Palette
		bestTI, bestSize, bestPolicy := -1, -1, 0
		for i, way := range ways {
			pal, index, ti := paletteOf(way)
			if len(pal) > 256 {
				continue
			}
			pix := bufs[i][:0]
			for _, c := range way {
				pix = append(pix, index[c])
			}
			bufs[i] = pix
			for policy := range clearPolicies {
				if policy > 0 && (o.Lossy > 0 || aw.effort >= 1 && aw.effort <= 8) {
					break
				}
				size, err := aw.codedSize(pal, true, rect, ti, pix, policy)
				if err != nil {
					return err
				}
				if bestSize < 0 || size < bestSize {
					best, bestPal, bestTI, bestSize, bestPolicy = pix, pal, ti, size, policy
				}
			}
		}
		if bestSize < 0 {
			return errLocalPalette
		}
		info := FrameInfo{Delay: delay, Disposal: disposal, Transparent: bestTI >= 0, TransparentIndex: byte(max(bestTI, 0)), ClearPolicy: bestPolicy}
		if err := aw.WriteFrame(&pixelsSource{rect: rect, pal: bestPal, pix: best, env: env}, info); err != nil {
			return err
		}
		for y := rect.Min.Y; y < rect.Max.Y; y++ {
			dst := screen[y*fw+rect.Min.X:][:rect.Dx()]
			for x := range dst {
				c := keys[(y-rect.Min.Y)*rect.Dx()+x]
				switch {
				case disposal == disposalBackground:
					dst[x] = noColour
				case c != noColour:
					dst[x] = c
				}
			}
		}
		prevDisposal = disposal
	}
	return aw.Close()
}

// waysOf returns a frame of changes (its unchanged pixels transparent) with
// those pixels as the picture shows them instead, and with transparency
// only over stretches of them of more than one colour (see bestFrame).
func waysOf(keys []uint32, r image.Rectangle, screen []uint32, sw int) (opaque, runs []uint32) {
	opaque = slices.Clone(keys)
	runs = slices.Clone(keys)
	w := r.Dx()
	same, start := 0, 0
	for i, c := range keys {
		if c != noColour {
			same = 0
			continue
		}
		shown := screen[(r.Min.Y+i/w)*sw+r.Min.X+i%w]
		opaque[i] = shown
		switch {
		case same == 0:
			start, same = i, 1
			runs[i] = shown
		case same == 1 && shown != runs[i-1]:
			for j := start; j < i; j++ {
				runs[j] = noColour
			}
			same = 2
			runs[i] = noColour
		case same > 1:
			runs[i] = noColour
		default:
			runs[i] = shown
		}
	}
	return opaque, runs
}

// paletteOf returns the palette of the colour keys (sorted, the transparent
// entry last when there is one), the index of each key and the transparent
// index (-1 for none). The palette may come out longer than 256.
func paletteOf(keys []uint32) (color.Palette, map[uint32]byte, int) {
	seen := map[uint32]struct{}{}
	for _, c := range keys {
		seen[c] = struct{}{}
		if len(seen) > 257 {
			break
		}
	}
	sorted := make([]uint32, 0, len(seen))
	for c := range seen {
		sorted = append(sorted, c)
	}
	slices.Sort(sorted) // noColour first, when there
	pal := make(color.Palette, 0, len(sorted))
	index := make(map[uint32]byte, len(sorted))
	ti := -1
	for _, c := range sorted {
		if c != noColour {
			index[c] = byte(len(pal))
			pal = append(pal, color.RGBA{uint8(c >> 16), uint8(c >> 8), uint8(c), 0xff})
		}
	}
	if len(sorted) > 0 && sorted[0] == noColour {
		ti = len(pal)
		index[noColour] = byte(ti)
		pal = append(pal, color.RGBA{})
	}
	if len(pal) == 0 {
		pal = append(pal, color.RGBA{})
	}
	return pal, index, ti
}

var errLocalPalette = errors.New("goimgtools: gif: internal error: a frame does not fit a palette of its own")
