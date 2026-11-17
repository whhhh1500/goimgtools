package ops

import (
	"image"
	"image/color"
	"image/draw"
	"io"
	"math"
	"sync"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// The stage behind Affine (and RotateBy, which is an Affine): the source
// has to be at hand in any order, so it is first stored in working storage
// (a temporary file beyond its share of the limit), in tiles of 32 x 32
// pixels; what the output rows pass over is read back through a small
// cache of tiles.

const tileSide = 32

type rotated struct {
	src      pipeline.Source
	env      *pipeline.Env
	rect     image.Rectangle
	bgOpaque bool
	band     rgbaBand
	// aff is the transform; the source arrives as NRGBA and is stored as
	// it is (not premultiplied). span is how far the source point moves
	// for each output column.
	aff  *affineMap
	span float64

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
func (t *rotated) ColorModel() color.Model {
	if t.aff != nil && t.aff.float {
		return color.NRGBAModel
	}
	return color.RGBAModel
}
func (t *rotated) Opaque() bool { return t.bgOpaque && pipeline.IsOpaque(t.src) }

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
		var pix []uint8
		var stride int
		if n, ok := s.(*image.NRGBA); ok && t.aff != nil {
			pix, stride = n.Pix[n.PixOffset(b.Min.X, r.Min.Y):], n.Stride
		} else {
			dst, err := conv.get(r)
			if err != nil {
				return err
			}
			draw.Draw(dst, r, s, r.Min, draw.Src)
			pix, stride = dst.Pix[dst.PixOffset(b.Min.X, r.Min.Y):], dst.Stride
		}
		for sy := r.Min.Y; sy < r.Max.Y; sy++ {
			// Row y of the image is row y%32 of each tile of the row.
			src := pix[(sy-r.Min.Y)*stride:][:4*t.sw]
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
		span := int(math.Ceil(float64(x1-x0)*t.span/tileSide)) + 4
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
		if err := t.renderAffine(out, x0, x1, t.caches[p]); err != nil {
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
	if t.aff != nil && t.aff.float {
		return &image.NRGBA{Pix: out.Pix, Stride: out.Stride, Rect: out.Rect}, nil
	}
	return out, nil
}
