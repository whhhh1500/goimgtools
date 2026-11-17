package goimgtools

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// TileLayout is how ToTiles lays out its tiles.
type TileLayout int

const (
	// TileDeepZoom is Deep Zoom (DZI), which OpenSeadragon and the other
	// zooming viewers read: PATH.dzi, an XML file that gives the size, and
	// PATH_files/LEVEL/COLUMN_ROW.jpeg (or .png, .webp), level 0 being the
	// image shrunk to one pixel and each level twice the one before, the
	// last the image itself.
	TileDeepZoom TileLayout = iota
)

// TileOptions are the settings of ToTiles.
type TileOptions struct {
	// Size is the side of a tile, 1 to 8192 pixels; 0 means 256.
	Size int
	// Overlap is how many pixels of its neighbours a tile also holds on
	// each side, 0 (the default) to Size.
	Overlap int
	// Layout is how the tiles are laid out: TileDeepZoom, the only one so
	// far.
	Layout TileLayout
}

// ToTiles writes the image as a pyramid of tiles, as sharp's tile (libvips'
// dzsave) does, for a zooming viewer: see TileDeepZoom. path is where it
// goes without an extension (one of .dzi or .dz is taken off): "map" gives
// map.dzi and the directory map_files.
//
// The tiles are in the output format of the recipe (JPEG, PNG, WebP, or a
// registered format), with its options; by default JPEG at quality 80 with
// coding tables made for each tile, as sharp writes them. Each level is
// the one above shrunk to half (each pixel the mean of four, an odd last
// row or column taken twice), as libvips shrinks it, so the tiles are
// sharp's to the byte. They carry no metadata.
//
// The image streams through: each level keeps the rows of one row of tiles
// (and its overlap), the levels together about twice the top one's. With
// helpers (Threads) the tiles of a row are encoded at the same time. If
// ToTiles fails, what it has written is incomplete and must be removed.
func (m *Image) ToTiles(path string, opts ...TileOptions) (err error) {
	defer guard(&err)
	m.atMostOne("ToTiles", len(opts))
	if m.err != nil {
		return m.err
	}
	o := first(opts)
	if o.Size == 0 {
		o.Size = 256
	}
	switch {
	case o.Size < 1 || o.Size > 8192:
		return fmt.Errorf("goimgtools: tile size %d: it is 1 to 8192", o.Size)
	case o.Overlap < 0 || o.Overlap > o.Size:
		return fmt.Errorf("goimgtools: tile overlap %d: it is 0 to the tile size, %d", o.Overlap, o.Size)
	case o.Layout != TileDeepZoom:
		return fmt.Errorf("goimgtools: unknown TileLayout %d", o.Layout)
	}
	if ext := strings.ToLower(filepath.Ext(path)); ext == ".dzi" || ext == ".dz" {
		path = path[:len(path)-len(ext)]
	}
	format, options := m.format, m.options
	if format == "" {
		format, options = "jpeg", JPEGOptions{Quality: 80, Optimize: true}
	}
	if def := formatNamed(format); def == nil || def.encode == nil {
		return fmt.Errorf("goimgtools: cannot write %s tiles (%s)", format, formatList())
	}
	src, err := m.Stream(nil)
	if err != nil {
		return err
	}
	defer src.Close()
	b := src.Bounds()
	t := &tiler{m: m, env: pipeline.EnvOf(src), format: format, options: options, dir: path + "_files",
		size: o.Size, overlap: o.Overlap, grey: src.ColorModel() == color.GrayModel}
	defer t.release()
	if err := t.levels(b.Dx(), b.Dy()); err != nil {
		return err
	}
	for {
		s, err := src.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		r := s.Bounds()
		for y := r.Min.Y; y < r.Max.Y; y++ {
			row := t.top.row()
			draw.Draw(&image.NRGBA{Pix: row, Stride: len(row), Rect: image.Rect(r.Min.X, y, r.Max.X, y+1)},
				image.Rect(r.Min.X, y, r.Max.X, y+1), s, image.Pt(r.Min.X, y), draw.Src)
			if err := t.top.push(row); err != nil {
				return err
			}
		}
	}
	for l := t.top; l != nil; l = l.below {
		if l.have != l.h || l.tileRow != l.tilesDown {
			return io.ErrUnexpectedEOF
		}
	}
	// The description: as libvips writes it.
	ext := format
	var dzi bytes.Buffer
	fmt.Fprintf(&dzi, "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<Image xmlns=\"http://schemas.microsoft.com/deepzoom/2008\"\n"+
		"  Format=\"%s\"\n  Overlap=\"%d\"\n  TileSize=\"%d\"\n  >\n  <Size \n    Height=\"%d\"\n    Width=\"%d\"\n  />\n</Image>\n",
		ext, o.Overlap, o.Size, b.Dy(), b.Dx())
	return os.WriteFile(path+".dzi", dzi.Bytes(), 0o666)
}

// tiler writes the levels of a pyramid.
type tiler struct {
	m             *Image
	env           *pipeline.Env
	format        string
	options       any
	dir           string
	size, overlap int
	grey          bool
	top           *tileLevel
	reserved      int64
}

func (t *tiler) release() {
	t.env.Budget.Release(t.reserved)
	t.reserved = 0
}

// tileLevel is a level of the pyramid as its rows arrive: those of the row
// of tiles being made, and the even row waiting for the odd one to be
// shrunk into the level below.
type tileLevel struct {
	t         *tiler
	n         int // the Deep Zoom level
	w, h      int
	pw        int      // the width made even (the last column taken twice)
	rows      [][]byte // rows y0 to have-1, pw*4 bytes each
	y0, have  int
	tileRow   int
	tilesDown int
	tilesAcr  int
	free      [][]byte // rows to use again
	below     *tileLevel
}

// levels sets out the pyramid of a w x h image: halved (rounded up) down to
// one pixel.
func (t *tiler) levels(w, h int) error {
	var sizes [][2]int
	for {
		sizes = append(sizes, [2]int{w, h})
		if w <= 1 && h <= 1 {
			break
		}
		w, h = (w+1)/2, (h+1)/2
	}
	var above *tileLevel
	for i, s := range sizes {
		l := &tileLevel{t: t, n: len(sizes) - 1 - i, w: s[0], h: s[1], pw: s[0] + s[0]&1,
			tilesAcr: (s[0] + t.size - 1) / t.size, tilesDown: (s[1] + t.size - 1) / t.size}
		// A row of tiles and its overlaps, and the row held for shrinking.
		need := int64(t.size+2*t.overlap+2) * int64(l.pw) * 4
		if err := t.env.Budget.Reserve(need, "tile rows"); err != nil {
			return err
		}
		t.reserved += need
		if above == nil {
			t.top = l
		} else {
			above.below = l
		}
		above = l
		if err := os.MkdirAll(filepath.Join(t.dir, fmt.Sprint(l.n)), 0o777); err != nil {
			return err
		}
	}
	return nil
}

// row returns a row buffer of the level.
func (l *tileLevel) row() []byte {
	if n := len(l.free); n > 0 {
		r := l.free[n-1]
		l.free = l.free[:n-1]
		return r
	}
	return make([]byte, 4*l.pw)
}

// push adds the next row of the level (its first w pixels set), shrinks
// pairs of rows into the level below and writes the rows of tiles that are
// complete.
func (l *tileLevel) push(row []byte) error {
	if l.pw != l.w {
		copy(row[4*l.w:], row[4*l.w-4:4*l.w]) // the last column twice
	}
	l.rows = append(l.rows, row)
	l.have++
	if l.below != nil {
		switch {
		case l.have%2 == 0:
			if err := l.shrink(l.rows[len(l.rows)-2], row); err != nil {
				return err
			}
		case l.have == l.h:
			// The last row twice.
			if err := l.shrink(row, row); err != nil {
				return err
			}
		}
	}
	for l.tileRow < l.tilesDown {
		bottom := min(l.h, (l.tileRow+1)*l.t.size+l.t.overlap)
		if l.have < bottom {
			break
		}
		if err := l.writeRow(l.tileRow, bottom); err != nil {
			return err
		}
		l.tileRow++
		// The rows the next row of tiles does not need, and not held for
		// shrinking (an even row whose pair has not come).
		keep := l.tileRow*l.t.size - l.t.overlap
		if l.below != nil && l.have%2 == 1 && l.have < l.h {
			keep = min(keep, l.have-1)
		}
		for l.y0 < keep && len(l.rows) > 0 {
			l.free = append(l.free, l.rows[0])
			l.rows = l.rows[1:]
			l.y0++
		}
	}
	return nil
}

// shrink makes a row of the level below of two rows of this one: each
// pixel the mean of four, rounded as libvips rounds it.
func (l *tileLevel) shrink(a, b []byte) error {
	out := l.below.row()
	for x := 0; x < l.below.w; x++ {
		i := 8 * x
		for c := 0; c < 4; c++ {
			s := int(a[i+c]) + int(a[i+4+c]) + int(b[i+c]) + int(b[i+4+c])
			out[4*x+c] = uint8((s + 2) >> 2)
		}
	}
	return l.below.push(out)
}

// writeRow writes row r of tiles: rows from r*size-overlap to bottom.
func (l *tileLevel) writeRow(r, bottom int) error {
	t := l.t
	top := max(0, r*t.size-t.overlap)
	tiles := make([]error, l.tilesAcr)
	l.t.env.Parallel(l.tilesAcr, func(x int) {
		left := max(0, x*t.size-t.overlap)
		right := min(l.w, (x+1)*t.size+t.overlap)
		rect := image.Rect(0, 0, right-left, bottom-top)
		var img image.Image
		if t.grey {
			g := image.NewGray(rect)
			for y := top; y < bottom; y++ {
				src := l.rows[y-l.y0][4*left : 4*right]
				dst := g.Pix[(y-top)*g.Stride:]
				for i := range right - left {
					dst[i] = src[4*i]
				}
			}
			img = g
		} else {
			n := image.NewNRGBA(rect)
			for y := top; y < bottom; y++ {
				copy(n.Pix[(y-top)*n.Stride:], l.rows[y-l.y0][4*left:4*right])
			}
			img = n
		}
		tiles[x] = t.writeTile(filepath.Join(t.dir, fmt.Sprint(l.n), fmt.Sprintf("%d_%d.%s", x, r, t.format)), img)
	})
	return errors.Join(tiles...)
}

// writeTile encodes one tile to its file.
func (t *tiler) writeTile(name string, img image.Image) error {
	f, err := os.Create(name)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	src := pipeline.FromImage(img, t.env)
	err = t.m.encodeWith(w, src, t.format, t.options, 0, pipeline.Meta{})
	src.Close()
	if err == nil {
		err = w.Flush()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}
