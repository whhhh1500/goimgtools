package ops

import (
	"errors"
	"image"
	"image/color"
	"image/draw"
	"io"
	"math"

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
	ow := int(math.Ceil(w*math.Abs(cos) + h*math.Abs(sin) - 1e-9))
	oh := int(math.Ceil(w*math.Abs(sin) + h*math.Abs(cos) - 1e-9))
	ow, oh = max(ow, 1), max(oh, 1)
	if int64(ow) > 1<<24 || int64(oh) > 1<<24 {
		return nil, errors.New("goimgtools: the turned image would be too large")
	}
	r, g, bl, a := bg.RGBA()
	env := pipeline.EnvOf(src)
	return &rotated{
		src: src, env: env, sin: sin, cos: cos,
		rect:     image.Rect(0, 0, ow, oh),
		bg:       [4]float32{float32(r >> 8), float32(g >> 8), float32(bl >> 8), float32(a >> 8)},
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
	bg       [4]float32
	bgOpaque bool
	band     rgbaBand

	sc             *pipeline.Scratch
	sw, sh         int // source size
	tilesX, tilesY int
	// The cache: slot of each tile (-1 when not held), tile of each slot,
	// whether a slot was used since the clock hand last passed.
	slotOf   []int32
	tileOf   []int32
	used     []bool
	cache    []uint8
	hand     int
	reserved int64
	n, y     int
	loaded   bool
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
	t.reserved, t.cache = 0, nil
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
	// A row of tiles is gathered before it is written.
	rowsBytes := int64(t.tilesX) * tileBytes
	if err := t.reserve(rowsBytes+tileBytes+int64(nTiles)*4, "rotation tiles"); err != nil {
		return err
	}
	gather := make([]uint8, rowsBytes)
	tile := make([]uint8, tileBytes)
	var err error
	if t.sc, err = pipeline.NewScratch(t.env, int64(nTiles)*tileBytes); err != nil {
		return err
	}
	conv := rgbaBand{env: t.env}
	defer conv.release()
	flush := func(ty int) error {
		for tx := 0; tx < t.tilesX; tx++ {
			for row := 0; row < tileSide; row++ {
				copy(tile[row*tileSide*4:(row+1)*tileSide*4], gather[(row*t.tilesX+tx)*tileSide*4:])
			}
			if _, err := t.sc.WriteAt(tile, int64(ty*t.tilesX+tx)*tileBytes); err != nil {
				return err
			}
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
			// Row y of the image goes to row y%32 of the gathered tile row,
			// which is laid out as tilesX tiles side by side, row by row.
			src := dst.Pix[dst.PixOffset(b.Min.X, sy):][:4*t.sw]
			ry := y % tileSide
			for tx := 0; tx < t.tilesX; tx++ {
				x0 := tx * tileSide
				x1 := min(x0+tileSide, t.sw)
				copy(gather[(ry*t.tilesX+tx)*tileSide*4:], src[4*x0:4*x1])
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
	// The cache holds what an output row's line through the source
	// passes, twice over, and a little more.
	ow := float64(t.rect.Dx())
	span := int(math.Ceil(ow*(math.Abs(t.cos)+math.Abs(t.sin))/tileSide)) + 4
	slots := min(nTiles, 2*span+8)
	if err := t.reserve(int64(slots)*(tileBytes+5), "rotation tile cache"); err != nil {
		return err
	}
	t.cache = make([]uint8, slots*tileBytes)
	t.tileOf = make([]int32, slots)
	t.used = make([]bool, slots)
	for i := range t.tileOf {
		t.tileOf[i] = -1
	}
	t.slotOf = make([]int32, nTiles)
	for i := range t.slotOf {
		t.slotOf[i] = -1
	}
	t.n = pipeline.StripRows(int64(t.rect.Dx())*4, t.rect.Dy())
	t.loaded = true
	return nil
}

// tile returns the pixels of tile ti, reading it into the cache if needed.
func (t *rotated) tile(ti int) ([]uint8, error) {
	if s := t.slotOf[ti]; s >= 0 {
		t.used[s] = true
		return t.cache[int(s)*tileBytes:][:tileBytes], nil
	}
	// The clock: the first slot not used since the hand last passed.
	for t.used[t.hand] {
		t.used[t.hand] = false
		t.hand = (t.hand + 1) % len(t.tileOf)
	}
	s := t.hand
	t.hand = (t.hand + 1) % len(t.tileOf)
	if old := t.tileOf[s]; old >= 0 {
		t.slotOf[old] = -1
	}
	buf := t.cache[s*tileBytes:][:tileBytes]
	if _, err := t.sc.ReadAt(buf, int64(ti)*tileBytes); err != nil {
		t.tileOf[s] = -1
		return nil, err
	}
	t.tileOf[s], t.slotOf[ti], t.used[s] = int32(ti), int32(s), true
	return buf, nil
}

// pixel returns the premultiplied pixel at (x, y), or nil when it is
// outside the source.
func (t *rotated) pixel(x, y int) ([]uint8, error) {
	if x < 0 || y < 0 || x >= t.sw || y >= t.sh {
		return nil, nil
	}
	tl, err := t.tile((y/tileSide)*t.tilesX + x/tileSide)
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
	ow := t.rect.Dx()
	cx, cy := float64(ow)/2, float64(oh)/2
	scx, scy := float64(t.sw)/2-0.5, float64(t.sh)/2-0.5
	for oy := t.y; oy < y1; oy++ {
		dst := out.Pix[(oy-t.y)*out.Stride:][:4*ow]
		dy := float64(oy) + 0.5 - cy
		// The source point of the centre of output pixel (ox, oy): the
		// output centre turned back by the angle.
		dx0 := 0.5 - cx
		sx := dx0*t.cos + dy*t.sin + scx
		sy := -dx0*t.sin + dy*t.cos + scy
		for ox := 0; ox < ow; ox++ {
			if err := t.sample(dst[4*ox:4*ox+4], sx, sy); err != nil {
				return nil, err
			}
			sx += t.cos
			sy -= t.sin
		}
	}
	t.y = y1
	return out, nil
}

// sample writes the bilinear interpolation at (sx, sy) of the source, the
// background standing in for what is outside it.
func (t *rotated) sample(dst []uint8, sx, sy float64) error {
	if sx <= -1 || sy <= -1 || sx >= float64(t.sw) || sy >= float64(t.sh) {
		dst[0], dst[1], dst[2], dst[3] = uint8(t.bg[0]), uint8(t.bg[1]), uint8(t.bg[2]), uint8(t.bg[3])
		return nil
	}
	x0, y0 := int(math.Floor(sx)), int(math.Floor(sy))
	fx, fy := float32(sx-float64(x0)), float32(sy-float64(y0))
	var acc [4]float32
	for k, wgt := range [4]float32{(1 - fx) * (1 - fy), fx * (1 - fy), (1 - fx) * fy, fx * fy} {
		if wgt == 0 {
			continue
		}
		p, err := t.pixel(x0+k%2, y0+k/2)
		if err != nil {
			return err
		}
		if p == nil {
			for c := range acc {
				acc[c] += wgt * t.bg[c]
			}
			continue
		}
		for c := range acc {
			acc[c] += wgt * float32(p[c])
		}
	}
	a := min(255, acc[3]+0.5)
	dst[3] = uint8(a)
	for c := 0; c < 3; c++ {
		dst[c] = uint8(min(a, acc[c]+0.5))
	}
	return nil
}
