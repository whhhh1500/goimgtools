package ops

import (
	"errors"
	"image"
	"image/color"
	"image/draw"
	"io"
	"math"
	"sync"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// RotateAny turns src clockwise by deg degrees, any angle, onto a canvas
// just large enough to hold all of it; the corners it leaves are bg. The
// pixels are interpolated bilinearly, at 8 bits a channel, premultiplied;
// the result is RGBA. (A multiple of 90 degrees is done exactly by Orient.)
//
// The source has to be at hand in any order, so it is first stored in
// working storage (a temporary file beyond its share of the limit), in
// tiles of 32 x 32 pixels; what the output rows pass over is read back
// through a small cache of tiles.
func RotateAny(src pipeline.Source, deg float64, bg color.Color) (pipeline.Source, error) {
	if math.IsNaN(deg) || math.IsInf(deg, 0) {
		return nil, errors.New("goimgtools: the angle must be a finite number")
	}
	b := src.Bounds()
	w, h := float64(b.Dx()), float64(b.Dy())
	sin, cos := math.Sincos(math.Mod(deg, 360) * math.Pi / 180)
	ow := int(math.Ceil(float64(w*math.Abs(cos)) + float64(h*math.Abs(sin)) - 1e-9))
	oh := int(math.Ceil(float64(w*math.Abs(sin)) + float64(h*math.Abs(cos)) - 1e-9))
	ow, oh = max(ow, 1), max(oh, 1)
	if int64(ow) > 1<<24 || int64(oh) > 1<<24 {
		return nil, errors.New("goimgtools: the turned image would be too large")
	}
	r, g, bl, a := bg.RGBA()
	env := pipeline.EnvOf(src)
	return &rotated{
		src: src, env: env, sin: sin, cos: cos,
		rect:     image.Rect(0, 0, ow, oh),
		bg:       [4]uint32{r >> 8, g >> 8, bl >> 8, a >> 8},
		bgOpaque: a == 0xffff,
		band:     rgbaBand{env: env},
	}, nil
}

const tileSide = 32

type rotated struct {
	src      pipeline.Source
	env      *pipeline.Env
	sin, cos float64
	rect     image.Rectangle
	bg       [4]uint32 // premultiplied
	bgOpaque bool
	band     rgbaBand

	sc             *pipeline.Scratch
	sw, sh         int // source size
	tilesX, tilesY int
	// The output columns are shared out in parts, each with a cache of
	// its own.
	caches   []*tileCache
	reserved int64
	n, y     int
	loaded   bool
}

// tileCache holds some of the tiles of the stored source, read in as they
// are needed and replaced by the clock.
type tileCache struct {
	// The slot of each tile (-1 when not held), the tile of each slot,
	// whether a slot was used since the clock hand last passed.
	slotOf []int32
	tileOf []int32
	used   []bool
	data   []uint8
	hand   int
}

func newTileCache(nTiles, slots int) *tileCache {
	c := &tileCache{slotOf: make([]int32, nTiles), tileOf: make([]int32, slots), used: make([]bool, slots),
		data: make([]uint8, slots*tileBytes)}
	for i := range c.slotOf {
		c.slotOf[i] = -1
	}
	for i := range c.tileOf {
		c.tileOf[i] = -1
	}
	return c
}

// columns is the range of output columns of part p.
func (t *rotated) columns(p int) (int, int) {
	ow, parts := t.rect.Dx(), len(t.caches)
	return p * ow / parts, (p + 1) * ow / parts
}

func (t *rotated) Env() *pipeline.Env      { return t.env }
func (t *rotated) Bounds() image.Rectangle { return t.rect }
func (t *rotated) ColorModel() color.Model { return color.RGBAModel }
func (t *rotated) Opaque() bool            { return t.bgOpaque && pipeline.IsOpaque(t.src) }

func (t *rotated) Close() error {
	if t.sc != nil {
		t.sc.Close()
		t.sc = nil
	}
	t.band.release()
	t.env.Budget.Release(t.reserved)
	t.reserved, t.caches = 0, nil
	return t.src.Close()
}

func (t *rotated) reserve(n int64, what string) error {
	if err := t.env.Budget.Reserve(n, what); err != nil {
		return err
	}
	t.reserved += n
	return nil
}

const tileBytes = tileSide * tileSide * 4

// load stores the source in tiles.
func (t *rotated) load() error {
	b := t.src.Bounds()
	t.sw, t.sh = b.Dx(), b.Dy()
	t.tilesX = (t.sw + tileSide - 1) / tileSide
	t.tilesY = (t.sh + tileSide - 1) / tileSide
	nTiles := t.tilesX * t.tilesY
	// A row of tiles is gathered, laid out as it is stored (tile after
	// tile, each row after row), and written in one go.
	rowsBytes := int64(t.tilesX) * tileBytes
	if err := t.reserve(rowsBytes+int64(nTiles)*4, "rotation tiles"); err != nil {
		return err
	}
	gather := make([]uint8, rowsBytes)
	var err error
	if t.sc, err = pipeline.NewScratch(t.env, int64(nTiles)*tileBytes); err != nil {
		return err
	}
	conv := rgbaBand{env: t.env}
	defer conv.release()
	flush := func(ty int) error {
		if _, err := t.sc.WriteAt(gather, int64(ty*t.tilesX)*tileBytes); err != nil {
			return err
		}
		clear(gather)
		return nil
	}
	y := 0
	for {
		s, err := t.src.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		r := s.Bounds()
		dst, err := conv.get(r)
		if err != nil {
			return err
		}
		draw.Draw(dst, r, s, r.Min, draw.Src)
		for sy := r.Min.Y; sy < r.Max.Y; sy++ {
			// Row y of the image is row y%32 of each tile of the row.
			src := dst.Pix[dst.PixOffset(b.Min.X, sy):][:4*t.sw]
			ry := y % tileSide
			for tx := 0; tx < t.tilesX; tx++ {
				x0 := tx * tileSide
				x1 := min(x0+tileSide, t.sw)
				copy(gather[tx*tileBytes+ry*tileSide*4:], src[4*x0:4*x1])
			}
			y++
			if y%tileSide == 0 {
				if err := flush(y/tileSide - 1); err != nil {
					return err
				}
			}
		}
	}
	if y != t.sh {
		return io.ErrUnexpectedEOF
	}
	if y%tileSide != 0 {
		if err := flush(y / tileSide); err != nil {
			return err
		}
	}
	// With helpers the output columns are shared out, in parts at least
	// 256 pixels wide. The cache of a part holds what the line of its
	// columns of an output row passes through the source, twice over, and
	// a little more.
	parts := max(1, min(t.env.Helpers()+1, t.rect.Dx()/256))
	t.caches = make([]*tileCache, parts)
	for p := range t.caches {
		t.caches[p] = &tileCache{}
		x0, x1 := t.columns(p)
		span := int(math.Ceil(float64(x1-x0)*(math.Abs(t.cos)+math.Abs(t.sin))/tileSide)) + 4
		slots := min(nTiles, 2*span+8)
		if err := t.reserve(int64(slots)*(tileBytes+5)+int64(nTiles)*4, "rotation tile cache"); err != nil {
			return err
		}
		t.caches[p] = newTileCache(nTiles, slots)
	}
	t.n = pipeline.StripRows(int64(t.rect.Dx())*4, t.rect.Dy())
	t.loaded = true
	return nil
}

// tile returns the pixels of tile ti, reading it from sc into the cache if
// needed. (Reading a Scratch from several goroutines at once is safe.)
func (c *tileCache) tile(sc *pipeline.Scratch, ti int) ([]uint8, error) {
	if s := c.slotOf[ti]; s >= 0 {
		c.used[s] = true
		return c.data[int(s)*tileBytes:][:tileBytes], nil
	}
	// The clock: the first slot not used since the hand last passed.
	for c.used[c.hand] {
		c.used[c.hand] = false
		c.hand = (c.hand + 1) % len(c.tileOf)
	}
	s := c.hand
	c.hand = (c.hand + 1) % len(c.tileOf)
	if old := c.tileOf[s]; old >= 0 {
		c.slotOf[old] = -1
	}
	buf := c.data[s*tileBytes:][:tileBytes]
	if _, err := sc.ReadAt(buf, int64(ti)*tileBytes); err != nil {
		c.tileOf[s] = -1
		return nil, err
	}
	c.tileOf[s], c.slotOf[ti], c.used[s] = int32(ti), int32(s), true
	return buf, nil
}

// pixel returns the premultiplied pixel at (x, y) through cache c, or nil
// when it is outside the source.
func (t *rotated) pixel(c *tileCache, x, y int) ([]uint8, error) {
	if x < 0 || y < 0 || x >= t.sw || y >= t.sh {
		return nil, nil
	}
	tl, err := c.tile(t.sc, (y/tileSide)*t.tilesX+x/tileSide)
	if err != nil {
		return nil, err
	}
	o := ((y%tileSide)*tileSide + x%tileSide) * 4
	return tl[o : o+4 : o+4], nil
}

func (t *rotated) Next() (image.Image, error) {
	if !t.loaded {
		if err := t.load(); err != nil {
			return nil, err
		}
	}
	oh := t.rect.Dy()
	if t.y >= oh {
		return nil, io.EOF
	}
	y1 := min(t.y+t.n, oh)
	r := image.Rect(0, t.y, t.rect.Dx(), y1)
	out, err := t.band.get(r)
	if err != nil {
		return nil, err
	}
	var failed error
	var mu sync.Mutex
	t.env.Parallel(len(t.caches), func(p int) {
		x0, x1 := t.columns(p)
		if err := t.render(out, x0, x1, t.caches[p]); err != nil {
			mu.Lock()
			if failed == nil {
				failed = err
			}
			mu.Unlock()
		}
	})
	if failed != nil {
		return nil, failed
	}
	t.y = y1
	return out, nil
}

// render works out columns x0 to x1 of the rows of out through cache c.
// The source point of every pixel is worked out on its own, not by adding
// up steps, so that it does not depend on where a part of the columns
// begins; and every product is rounded before it is added (the explicit
// conversions), so that processors that could fuse the two give the same
// result as those that cannot.
func (t *rotated) render(out *image.RGBA, x0, x1 int, c *tileCache) error {
	ow, oh := t.rect.Dx(), t.rect.Dy()
	cx, cy := float64(float64(ow)/2), float64(float64(oh)/2)
	scx, scy := float64(float64(t.sw)/2)-0.5, float64(float64(t.sh)/2)-0.5
	fw, fh := float64(t.sw), float64(t.sh)
	bg := [4]uint8{uint8(t.bg[0]), uint8(t.bg[1]), uint8(t.bg[2]), uint8(t.bg[3])}
	// The tile of the last pixel; a read into the cache may replace it.
	last, lastTi := []uint8(nil), -1
	for oy := out.Rect.Min.Y; oy < out.Rect.Max.Y; oy++ {
		dst := out.Pix[(oy-out.Rect.Min.Y)*out.Stride:][:4*ow]
		dy := float64(oy) + 0.5 - cy
		ys, yc := float64(dy*t.sin), float64(dy*t.cos)
		for ox := x0; ox < x1; ox++ {
			// The centre of output pixel (ox, oy), turned back by the angle.
			dx := float64(ox) + 0.5 - cx
			sx := float64(dx*t.cos) + ys + scx
			sy := yc - float64(dx*t.sin) + scy
			d := dst[4*ox : 4*ox+4 : 4*ox+4]
			if !(sx > -1 && sy > -1 && sx < fw && sy < fh) {
				d[0], d[1], d[2], d[3] = bg[0], bg[1], bg[2], bg[3]
				continue
			}
			// floor, for values above -1
			sx0, sy0 := int(sx+1)-1, int(sy+1)-1
			wx := uint32(float64((sx-float64(sx0))*weightOne) + 0.5)
			wy := uint32(float64((sy-float64(sy0))*weightOne) + 0.5)
			if sx0 < 0 || sy0 < 0 || sx0+1 >= t.sw || sy0+1 >= t.sh || sx0%tileSide == tileSide-1 || sy0%tileSide == tileSide-1 {
				// At an edge of the source or of a tile.
				lastTi = -1
				if err := t.sampleEdge(c, d, sx0, sy0, wx, wy); err != nil {
					return err
				}
				continue
			}
			if ti := (sy0/tileSide)*t.tilesX + sx0/tileSide; ti != lastTi {
				var err error
				if last, err = c.tile(t.sc, ti); err != nil {
					return err
				}
				lastTi = ti
			}
			o := ((sy0%tileSide)*tileSide + sx0%tileSide) * 4
			q := last[o : o+tileSide*4+8 : o+tileSide*4+8]
			ix, iy := weightOne-wx, weightOne-wy
			for ch := 0; ch < 4; ch++ {
				top := uint32(q[ch])*ix + uint32(q[4+ch])*wx
				bot := uint32(q[tileSide*4+ch])*ix + uint32(q[tileSide*4+4+ch])*wx
				d[ch] = uint8((uint64(top)*uint64(iy) + uint64(bot)*uint64(wy) + 1<<31) >> 32)
			}
		}
	}
	return nil
}

// weightOne is a whole pixel in the fixed point of the interpolation
// weights, 16 bits: the result is within a few thousandths of a level of
// the exact one, and the same on every processor (floating point may fuse
// a multiplication and an addition on some and not on others).
const weightOne = 1 << 16

// sampleEdge interpolates between the four source pixels from (x0, y0),
// some of which may lie outside the source (the background stands in) or
// in other tiles, with the same arithmetic as Next. Premultiplied colours
// stay at most their alpha.
func (t *rotated) sampleEdge(c *tileCache, d []uint8, x0, y0 int, wx, wy uint32) error {
	var q [4][4]uint32
	for k := range q {
		p, err := t.pixel(c, x0+k%2, y0+k/2)
		if err != nil {
			return err
		}
		if p == nil {
			q[k] = t.bg
			continue
		}
		q[k] = [4]uint32{uint32(p[0]), uint32(p[1]), uint32(p[2]), uint32(p[3])}
	}
	ix, iy := weightOne-wx, weightOne-wy
	for ch := 0; ch < 4; ch++ {
		top := q[0][ch]*ix + q[1][ch]*wx
		bot := q[2][ch]*ix + q[3][ch]*wx
		d[ch] = uint8((uint64(top)*uint64(iy) + uint64(bot)*uint64(wy) + 1<<31) >> 32)
	}
	return nil
}
