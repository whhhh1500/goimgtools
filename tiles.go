package goimgtools

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/draw"
	"io"
	"io/fs"
	"math"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

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
	// TileGoogle is the layout of Google Maps, which Leaflet, OpenLayers
	// and the other map viewers read: PATH/LEVEL/ROW/COLUMN.jpg, level 0 a
	// single tile and each level twice the one before. The image is put on
	// a canvas of the background (Background, white by default) at its top
	// left corner (or Center), the canvas as large as makes every tile of
	// every level whole; tiles that hold none of the image are not
	// written, nor (SkipBlanks) those that are all background, and
	// PATH/blank.png is a tile of the background for the viewer to show
	// in their place.
	TileGoogle
	// TileIIIF is version 2 of the IIIF Image API at level 0, which
	// Mirador, OpenSeadragon and the other IIIF viewers read from a plain
	// web server: PATH/info.json, and each tile under its address,
	// PATH/LEFT,TOP,WIDTH,HEIGHT/WIDTH,/0/default.jpg (the region of the
	// image it shows, in pixels of the image, and its own width), the
	// tiles of the smallest level down to one tile. ID gives the address
	// in info.json. Overlap is to be 0: the addresses do not allow for
	// it.
	TileIIIF
	// TileIIIF3 is TileIIIF in version 3 of the API: the size of a tile
	// is WIDTH,HEIGHT.
	TileIIIF3
	// TileZoomify is Zoomify's layout: PATH/ImageProperties.xml and
	// PATH/TileGroupN/LEVEL-COLUMN-ROW.jpg, the tiles numbered from the
	// smallest level, 256 to a TileGroup directory; the smallest level is
	// one tile.
	TileZoomify
)

// TileDepth is how far down a pyramid of tiles goes.
type TileDepth int

const (
	// TileDepthDefault is the layout's: TileDepthOnePixel for
	// TileDeepZoom, TileDepthOneTile for the others.
	TileDepthDefault TileDepth = iota
	// TileDepthOnePixel shrinks the image down to a single pixel (for
	// TileGoogle, whose tiles are all whole, it is TileDepthOneTile).
	TileDepthOnePixel
	// TileDepthOneTile shrinks it down to a size that fits in one tile.
	TileDepthOneTile
	// TileDepthOne leaves the image at its size: one level.
	TileDepthOne
)

// TileOptions are the settings of ToTiles.
type TileOptions struct {
	// Size is the side of a tile, 1 to 8192 pixels; 0 means 256.
	Size int
	// Overlap is how many pixels a tile shares with its neighbours: in
	// TileDeepZoom, how many of theirs it also holds on each side, 0 (the
	// default) to Size; in the other layouts, how many of the tile before
	// it repeats (the tiles step by Size-Overlap), 0 to Size-1.
	Overlap int
	// Layout is how the tiles are laid out: TileDeepZoom (the default),
	// TileGoogle, TileIIIF, TileIIIF3 or TileZoomify.
	Layout TileLayout
	// Depth is how far down the pyramid goes: TileDepthDefault (the
	// layout's), TileDepthOnePixel, TileDepthOneTile or TileDepthOne.
	Depth TileDepth
	// Background is the colour around the image in a TileGoogle pyramid
	// and of its blank.png, and the colour SkipBlanks compares with; nil
	// means white. Its alpha counts for an image with transparency.
	Background color.Color
	// Center puts the image at the middle of a TileGoogle pyramid, not at
	// its top left corner.
	Center bool
	// SkipBlanks leaves out the tiles whose every value (red, green, blue,
	// and alpha for an image with transparency) is within SkipBlanks of
	// the background's: 0 means the layout's default, 5 for TileGoogle and
	// none for the others; a negative value leaves out none; at most 255.
	SkipBlanks int
	// ID is the address TileIIIF and TileIIIF3 give the image in
	// info.json, without its name, which is the last element of path and
	// follows it; "" means https://example.com/iiif.
	ID string
}

// ToTiles writes the image as a pyramid of tiles for a zooming viewer, as
// sharp's tile (libvips' dzsave) does: by default Deep Zoom, see
// TileDeepZoom and the other TileLayout values. path is where it goes
// without an extension (one of .dzi or .dz is taken off): "map" gives
// map.dzi and the directory map_files for Deep Zoom, the directory map for
// the other layouts. A path ending in .zip is a ZIP archive that holds
// them, as ToTilesWriter writes it ("map.zip" holds map.dzi and
// map_files/...), written beside and renamed when it is complete, as
// ToFile writes a file.
//
// The tiles are in the output format of the recipe (JPEG, PNG, WebP, or a
// registered format), with its options; by default JPEG at quality 80 with
// coding tables made for each tile, as sharp writes them. Each level is
// the one above shrunk to half (each pixel the mean of four, an odd last
// row or column taken twice), as libvips shrinks it, so the tiles are
// sharp's to the byte, and so are the files that describe them (.dzi,
// info.json, ImageProperties.xml; not libvips' own vips-properties.xml).
// They carry no metadata.
//
// The image streams through: each level keeps the rows of one row of tiles
// (and its overlap), the levels together about twice the top one's. With
// helpers (Threads) the tiles of a row are encoded at the same time. If
// ToTiles fails, what it has written is incomplete and must be removed.
func (m *Image) ToTiles(path string, opts ...TileOptions) (err error) {
	var tmp *os.File
	defer func() {
		// After guard: whatever went wrong, the unfinished archive goes.
		if err != nil && tmp != nil {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()
	defer guard(&err)
	m.atMostOne("ToTiles", len(opts))
	if m.err != nil {
		return m.err
	}
	if !strings.EqualFold(filepath.Ext(path), ".zip") {
		return m.tiles(nil, trimTileExt(path), first(opts))
	}
	if tmp, err = createBeside(path); err != nil {
		return err
	}
	bw := bufio.NewWriterSize(tmp, 64<<10)
	zw := zip.NewWriter(bw)
	if err = m.tiles(zw, trimTileExt(filepath.Base(path)), first(opts)); err != nil {
		return err
	}
	if err = zw.Close(); err != nil {
		return err
	}
	if err = bw.Flush(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// ToTilesWriter writes the pyramid of tiles of ToTiles to w as a ZIP
// archive, as sharp's tile writes one (to a .zip file, or to a buffer):
// name is the image's name in it, a path of names separated by slashes
// (an extension .zip, .dzi or .dz is taken off), so that "map" gives
// map.dzi and map_files/LEVEL/COLUMN_ROW.jpeg for Deep Zoom, map/... for
// the other layouts. The files are stored without compression (the tiles
// are compressed already), with the dates and modes libvips gives them, in
// an order that does not depend on the threads (libvips' does; each file
// is the same). The archive streams out: a row of tiles is held at a time,
// and an entry of the archive's directory for each file.
func (m *Image) ToTilesWriter(w io.Writer, name string, opts ...TileOptions) (err error) {
	defer guard(&err)
	m.atMostOne("ToTilesWriter", len(opts))
	if m.err != nil {
		return m.err
	}
	name = trimTileExt(name)
	if !fs.ValidPath(name) || name == "." {
		return fmt.Errorf("goimgtools: tile archive name %q: names separated by slashes, without . or ..", name)
	}
	zw := zip.NewWriter(w)
	if err := m.tiles(zw, name, first(opts)); err != nil {
		return err
	}
	return zw.Close()
}

// trimTileExt takes an extension of a pyramid of tiles off a name.
func trimTileExt(name string) string {
	switch ext := strings.ToLower(filepath.Ext(name)); ext {
	case ".dzi", ".dz", ".zip":
		return name[:len(name)-len(ext)]
	}
	return name
}

// tiles writes the pyramid at base: a path of the file system, or with zw
// a name in that archive.
func (m *Image) tiles(zw *zip.Writer, base string, o TileOptions) error {
	if o.Size == 0 {
		o.Size = 256
	}
	maxOverlap := o.Size - 1
	if o.Layout == TileDeepZoom {
		maxOverlap = o.Size
	}
	switch {
	case o.Size < 1 || o.Size > 8192:
		return fmt.Errorf("goimgtools: tile size %d: it is 1 to 8192", o.Size)
	case o.Layout < TileDeepZoom || o.Layout > TileZoomify:
		return fmt.Errorf("goimgtools: unknown TileLayout %d", o.Layout)
	case o.Overlap < 0 || o.Overlap > maxOverlap:
		return fmt.Errorf("goimgtools: tile overlap %d: it is 0 to %d for tiles of %d", o.Overlap, maxOverlap, o.Size)
	case o.Depth < TileDepthDefault || o.Depth > TileDepthOne:
		return fmt.Errorf("goimgtools: unknown TileDepth %d", o.Depth)
	case o.SkipBlanks > 255:
		return fmt.Errorf("goimgtools: SkipBlanks %d: it is at most 255", o.SkipBlanks)
	}
	format, options := m.format, m.options
	if format == "" {
		format, options = "jpeg", JPEGOptions{Quality: 80, Optimize: true}
	}
	if def := formatNamed(format); def == nil || def.encode == nil {
		return fmt.Errorf("goimgtools: cannot write %s tiles (%s)", format, formatList())
	}
	if len(m.toProfile) > 0 {
		return errors.New("goimgtools: ToProfile: tiles carry no colour profile")
	}
	// Tiles are written without a profile: their colours are made sRGB,
	// as for a format that cannot carry one (a Display P3 photograph would
	// look dull, an Adobe RGB one too bright).
	c := m.Clone()
	c.toSRGB = true
	src, err := c.Stream(nil)
	if err != nil {
		return err
	}
	defer src.Close()
	b := src.Bounds()
	t := &tiler{m: m, env: pipeline.EnvOf(src), format: format, options: options, ext: format, zip: zw,
		layout: o.Layout, base: base, dir: base, name: filepath.Base(base), size: o.Size, overlap: o.Overlap,
		step: o.Size - o.Overlap, id: o.ID, w: b.Dx(), h: b.Dy(), grey: src.ColorModel() == color.GrayModel, skip: o.SkipBlanks}
	if zw != nil {
		t.name = path.Base(base)
	}
	t.alpha = !t.grey && !pipeline.IsOpaque(src)
	// The tiles are encoded with a budget of their own: what the image's
	// stages hold is no business of theirs, and counted against them it
	// left no room to stage a tile's JPEG in memory (each went to a
	// temporary file).
	t.enc = pipeline.NewEnv(t.env.Budget.Limit(), t.env.TempDir)
	defer t.release()
	if t.layout == TileDeepZoom {
		t.dir, t.margin, t.step = base+"_files", o.Overlap, o.Size
	} else if format == "jpeg" {
		t.ext = "jpg"
	}
	if t.skip == 0 {
		t.skip = -1
		if t.layout == TileGoogle {
			t.skip = 5
		}
	}
	bg := color.Color(color.White)
	if o.Background != nil {
		bg = o.Background
	}
	t.ink = color.NRGBAModel.Convert(bg).(color.NRGBA)
	if !t.alpha {
		t.ink.A = 255
	}
	if t.grey {
		y := color.GrayModel.Convert(color.NRGBA{t.ink.R, t.ink.G, t.ink.B, 255}).(color.Gray).Y
		t.ink = color.NRGBA{y, y, y, 255}
	}
	limit := 1
	switch o.Depth {
	case TileDepthDefault:
		if t.layout != TileDeepZoom {
			limit = t.size
		}
	case TileDepthOneTile:
		limit = t.size
	case TileDepthOne:
		limit = math.MaxInt // one level, of the canvas too
	}
	if t.layout == TileGoogle && limit == 1 {
		limit = t.size
	}
	// The canvas: in TileGoogle as large as makes whole tiles of the
	// smallest level, scaled up to the top.
	cw, ch := t.w, t.h
	t.save = image.Rect(0, 0, t.w, t.h)
	if t.layout == TileGoogle {
		sizes := levelSizes(t.w, t.h, limit)
		p := sizes[len(sizes)-1]
		cw = ((p[0]+t.step-1)/t.step*t.step + t.overlap) << (len(sizes) - 1)
		ch = ((p[1]+t.step-1)/t.step*t.step + t.overlap) << (len(sizes) - 1)
		if o.Center {
			t.save = t.save.Add(image.Pt((cw-t.w)/2, (ch-t.h)/2))
		}
	}
	if err := t.levels(cw, ch, limit); err != nil {
		return err
	}
	// The rows of the canvas: the background around the image's.
	// Rows of the background alone (most of a canvas) are not shrunk:
	// each level has its row of the background, shrunk once.
	var fill []byte
	if cw != t.w || ch != t.h {
		fill = make([]byte, 4*t.top.pw)
		for i := 0; i < len(fill); i += 4 {
			copy(fill[i:], []byte{t.ink.R, t.ink.G, t.ink.B, t.ink.A})
		}
		t.top.fill = fill
		for l := t.top; l.below != nil; l = l.below {
			l.below.fill = make([]byte, 4*l.below.pw)
			l.shrinkRow(l.below.fill, l.fill, l.fill)
			l.below.evenOut(l.below.fill)
		}
	}
	blank := func(n int) error {
		for range n {
			row := t.top.row()
			copy(row, fill)
			if err := t.top.push(row, true); err != nil {
				return err
			}
		}
		return nil
	}
	if err := blank(t.save.Min.Y); err != nil {
		return err
	}
	x0 := t.save.Min.X
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
			copy(row, fill)
			dst := row[4*x0 : 4*(x0+r.Dx())]
			switch m := s.(type) {
			case *image.NRGBA:
				copy(dst, m.Pix[m.PixOffset(r.Min.X, y):])
			case *image.RGBA:
				if t.alpha { // colours multiplied by alpha
					draw.Draw(&image.NRGBA{Pix: dst, Stride: len(dst), Rect: image.Rect(r.Min.X, y, r.Max.X, y+1)},
						image.Rect(r.Min.X, y, r.Max.X, y+1), s, image.Pt(r.Min.X, y), draw.Src)
				} else {
					copy(dst, m.Pix[m.PixOffset(r.Min.X, y):])
				}
			case *image.Gray:
				p := m.Pix[m.PixOffset(r.Min.X, y):]
				for i := 0; i < len(dst); i += 4 {
					v := p[i/4]
					dst[i], dst[i+1], dst[i+2], dst[i+3] = v, v, v, 0xff
				}
			default:
				draw.Draw(&image.NRGBA{Pix: dst, Stride: len(dst), Rect: image.Rect(r.Min.X, y, r.Max.X, y+1)},
					image.Rect(r.Min.X, y, r.Max.X, y+1), s, image.Pt(r.Min.X, y), draw.Src)
			}
			if err := t.top.push(row, false); err != nil {
				return err
			}
		}
	}
	if t.top.have != t.save.Min.Y+t.h {
		return io.ErrUnexpectedEOF
	}
	if err := blank(ch - t.top.have); err != nil {
		return err
	}
	for l := t.top; l != nil; l = l.below {
		if l.have != l.h || l.tileRow != l.tilesDown {
			return io.ErrUnexpectedEOF
		}
	}
	return t.describe()
}

// describe writes the files that describe the pyramid, as libvips writes
// them.
func (t *tiler) describe() error {
	var buf bytes.Buffer
	var name string
	switch t.layout {
	case TileDeepZoom:
		name = t.base + ".dzi"
		fmt.Fprintf(&buf, "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<Image xmlns=\"http://schemas.microsoft.com/deepzoom/2008\"\n"+
			"  Format=\"%s\"\n  Overlap=\"%d\"\n  TileSize=\"%d\"\n  >\n  <Size \n    Height=\"%d\"\n    Width=\"%d\"\n  />\n</Image>\n",
			t.ext, t.overlap, t.size, t.h, t.w)
	case TileZoomify:
		name = t.full("ImageProperties.xml")
		fmt.Fprintf(&buf, "<IMAGE_PROPERTIES WIDTH=\"%d\" HEIGHT=\"%d\" NUMTILES=\"%d\" NUMIMAGES=\"1\" VERSION=\"1.8\" TILESIZE=\"%d\" />\n",
			t.w, t.h, t.count.Load(), t.size)
	case TileGoogle:
		// A tile of the background, always PNG.
		r := image.Rect(0, 0, t.size, t.size)
		var img draw.Image = image.NewNRGBA(r)
		if t.grey {
			img = image.NewGray(r)
		}
		draw.Draw(img, r, &image.Uniform{t.ink}, image.Point{}, draw.Src)
		name = t.full("blank.png")
		if err := t.encode(&buf, "png", nil, img); err != nil {
			return err
		}
	default:
		id := t.id
		if id == "" {
			id = "https://example.com/iiif"
		}
		quote := func(s string) string {
			var b bytes.Buffer
			e := json.NewEncoder(&b)
			e.SetEscapeHTML(false)
			e.Encode(s)
			return strings.TrimSuffix(b.String(), "\n")
		}
		name = t.full("info.json")
		at := quote(id + "/" + t.name)
		if t.layout == TileIIIF3 {
			fmt.Fprintf(&buf, "{\n  \"@context\": \"http://iiif.io/api/image/3/context.json\",\n  \"id\": %s,\n"+
				"  \"type\": \"ImageService3\",\n  \"profile\": \"level0\",\n  \"protocol\": \"http://iiif.io/api/image\",\n", at)
		} else {
			fmt.Fprintf(&buf, "{\n  \"@context\": \"http://iiif.io/api/image/2/context.json\",\n  \"@id\": %s,\n"+
				"  \"profile\": [\n    \"http://iiif.io/api/image/2/level0.json\",\n    {\n      \"formats\": [\n        %s\n      ],\n"+
				"      \"qualities\": [\n        \"default\"\n      ]\n    }\n  ],\n  \"protocol\": \"http://iiif.io/api/image\",\n", at, quote(t.ext))
		}
		buf.WriteString("  \"tiles\": [\n    {\n      \"scaleFactors\": [\n")
		for i := 0; i <= t.top.n; i++ {
			fmt.Fprintf(&buf, "        %d", 1<<i)
			if i != t.top.n {
				buf.WriteString(",")
			}
			buf.WriteString("\n")
		}
		fmt.Fprintf(&buf, "      ],\n      \"width\": %d\n    }\n  ],\n  \"width\": %d,\n  \"height\": %d\n}\n", t.size, t.w, t.h)
	}
	if t.zip != nil {
		return t.addZip(name, buf.Bytes())
	}
	if err := os.MkdirAll(filepath.Dir(name), 0o777); err != nil {
		return err
	}
	return os.WriteFile(name, buf.Bytes(), 0o666)
}

// full is the path of a file of the pyramid (rel, separated by slashes),
// in the file system or the archive.
func (t *tiler) full(rel string) string {
	if t.zip != nil {
		return t.dir + "/" + rel
	}
	return filepath.Join(t.dir, filepath.FromSlash(rel))
}

// addZip stores a file in the archive as libvips (libarchive) stores it:
// without compression, the sizes and checksum after the data, dated
// 1980-01-01, mode 0664.
func (t *tiler) addZip(name string, data []byte) error {
	fh := &zip.FileHeader{Name: name, Method: zip.Store, CreatorVersion: 3<<8 | 10, ReaderVersion: 10,
		Flags: 0x8, ModifiedDate: 0x21, CRC32: crc32.ChecksumIEEE(data),
		CompressedSize64: uint64(len(data)), UncompressedSize64: uint64(len(data)), ExternalAttrs: 0o100664 << 16}
	for _, r := range name {
		if r >= 0x80 {
			fh.Flags |= 0x800 // the name is UTF-8
			break
		}
	}
	w, err := t.zip.CreateRaw(fh)
	if err == nil {
		_, err = w.Write(data)
	}
	return err
}

// tiler writes the levels of a pyramid.
type tiler struct {
	m       *Image
	env     *pipeline.Env
	format  string
	options any
	ext     string // of the tiles' files
	zip     *zip.Writer
	layout  TileLayout
	base    string // the pyramid's path without an extension
	dir     string // where the tiles go
	name    string // the image's name: the last element of base
	id      string
	// A tile is size pixels square plus margin on each side, and they
	// step by step: Deep Zoom's tiles overlap by their margins, the
	// others' by size-step.
	size, overlap, step, margin int
	w, h                        int             // the image's size
	save                        image.Rectangle // where the image is on the canvas
	grey, alpha                 bool
	ink                         color.NRGBA // the background
	skip                        int         // the SkipBlanks threshold, or -1
	count                       atomic.Int64
	top                         *tileLevel
	reserved                    int64
	enc                         *pipeline.Env // the tiles' encoding (see tiles)
	// Tiles' pixels to use again, one for each tile being encoded at once.
	mu  sync.Mutex
	pix [][]byte
}

// pixels returns storage for a tile's n bytes of pixels; give it back with
// done.
func (t *tiler) pixels(n int) []byte {
	t.mu.Lock()
	defer t.mu.Unlock()
	if k := len(t.pix); k > 0 {
		p := t.pix[k-1]
		t.pix = t.pix[:k-1]
		if cap(p) >= n {
			return p[:n]
		}
	}
	c := t.size + 2*t.margin
	return make([]byte, n, max(n, 4*c*c))
}

func (t *tiler) done(p []byte) {
	t.mu.Lock()
	t.pix = append(t.pix, p)
	t.mu.Unlock()
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
	n         int // the level's number, 0 the smallest
	sub       int // how many pixels of the top level one of this level is across
	w, h      int
	pw        int // the width made even (the last column taken twice)
	save      image.Rectangle
	rows      [][]byte // rows y0 to have-1, pw*4 bytes each
	y0, have  int
	tileRow   int
	tilesDown int
	tilesAcr  int
	before    int      // the tiles of the levels below (Zoomify numbers them)
	free      [][]byte // rows to use again
	below     *tileLevel
	fill      []byte // a row of the background, on a canvas
	pure      bool   // the last row is fill
}

// levelSizes is the sizes of a pyramid's levels from the top: halved
// (rounded up) until the level fits in limit.
func levelSizes(w, h, limit int) [][2]int {
	var sizes [][2]int
	for {
		sizes = append(sizes, [2]int{w, h})
		if w <= limit && h <= limit {
			return sizes
		}
		w, h = (w+1)/2, (h+1)/2
	}
}

// levels sets out the pyramid of a w x h canvas.
func (t *tiler) levels(w, h, limit int) error {
	sizes := levelSizes(w, h, limit)
	var above *tileLevel
	save := t.save
	all := make([]*tileLevel, len(sizes))
	for i, s := range sizes {
		l := &tileLevel{t: t, n: len(sizes) - 1 - i, sub: 1 << i, w: s[0], h: s[1], pw: s[0] + s[0]&1, save: save,
			tilesAcr: (s[0] + t.step - 1) / t.step, tilesDown: (s[1] + t.step - 1) / t.step}
		all[i] = l
		// The image's part of the level below, as libvips rounds it.
		at := image.Pt((save.Min.X+1)/2, (save.Min.Y+1)/2)
		save = image.Rectangle{at, at.Add(image.Pt((save.Dx()+1)/2, (save.Dy()+1)/2))}
		// A row of tiles and its overlaps, and the row held for shrinking.
		need := int64(t.size+2*t.margin+2) * int64(l.pw) * 4
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
	}
	for i := len(all) - 2; i >= 0; i-- {
		all[i].before = all[i+1].before + all[i+1].tilesAcr*all[i+1].tilesDown
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

// evenOut takes the last column of a row twice, if the width is odd.
func (l *tileLevel) evenOut(row []byte) {
	if l.pw != l.w {
		copy(row[4*l.w:], row[4*l.w-4:4*l.w])
	}
}

// push adds the next row of the level (its first w pixels set; pure if it
// is the level's fill), shrinks pairs of rows into the level below and
// writes the rows of tiles that are complete.
func (l *tileLevel) push(row []byte, pure bool) error {
	t := l.t
	l.evenOut(row)
	l.rows = append(l.rows, row)
	l.have++
	if l.below != nil {
		switch {
		case l.have%2 == 0:
			if err := l.shrink(l.rows[len(l.rows)-2], row, l.pure && pure); err != nil {
				return err
			}
		case l.have == l.h:
			// The last row twice.
			if err := l.shrink(row, row, pure); err != nil {
				return err
			}
		}
	}
	l.pure = pure
	for l.tileRow < l.tilesDown {
		bottom := min(l.h, l.tileRow*t.step+t.size+t.margin)
		if l.have < bottom {
			break
		}
		if err := l.writeRow(l.tileRow, bottom); err != nil {
			return err
		}
		l.tileRow++
		// The rows the next row of tiles does not need, and not held for
		// shrinking (an even row whose pair has not come).
		keep := l.tileRow*t.step - t.margin
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
// pixel the mean of four, as libvips takes it: rounded, or, for an image
// with transparency, the colours weighted by their alphas and both
// truncated.
func (l *tileLevel) shrink(a, b []byte, pure bool) error {
	out := l.below.row()
	if pure {
		copy(out, l.below.fill)
	} else {
		l.shrinkRow(out, a, b)
	}
	return l.below.push(out, pure)
}

// shrinkRow is shrink of rows a and b into out.
func (l *tileLevel) shrinkRow(out, a, b []byte) {
	if !l.t.alpha {
		for x := 0; x < l.below.w; x++ {
			i := 8 * x
			for c := 0; c < 4; c++ {
				s := int(a[i+c]) + int(a[i+4+c]) + int(b[i+c]) + int(b[i+4+c])
				out[4*x+c] = uint8((s + 2) >> 2)
			}
		}
		return
	}
	for x := 0; x < l.below.w; x++ {
		i := 8 * x
		a1, a2, a3, a4 := int(a[i+3]), int(a[i+7]), int(b[i+3]), int(b[i+7])
		sum := a1 + a2 + a3 + a4
		if sum == 0 {
			copy(out[4*x:4*x+4], []byte{0, 0, 0, 0})
			continue
		}
		for c := 0; c < 3; c++ {
			out[4*x+c] = uint8((a1*int(a[i+c]) + a2*int(a[i+4+c]) + a3*int(b[i+c]) + a4*int(b[i+4+c])) / sum)
		}
		out[4*x+3] = uint8(sum / 4)
	}
}

// writeRow writes row r of tiles: rows from r*step-margin to bottom.
func (l *tileLevel) writeRow(r, bottom int) error {
	t := l.t
	top := max(0, r*t.step-t.margin)
	tiles := make([]error, l.tilesAcr)
	var names []string
	var data []*bytes.Buffer
	if t.zip != nil {
		// Encoded at the same time, stored in order.
		names, data = make([]string, l.tilesAcr), make([]*bytes.Buffer, l.tilesAcr)
	}
	l.t.env.Parallel(l.tilesAcr, func(x int) {
		// libvips writes no tile that holds none of the image.
		if !image.Rect(x*t.step, r*t.step, x*t.step+t.size, r*t.step+t.size).Overlaps(l.save) {
			return
		}
		left := max(0, x*t.step-t.margin)
		right := min(l.w, x*t.step+t.size+t.margin)
		if t.skip >= 0 && l.blank(top, bottom, left, right) {
			return
		}
		rect := image.Rect(0, 0, right-left, bottom-top)
		var img image.Image
		// Every byte of the tile is written below.
		px := t.pixels(4 * rect.Dx() * rect.Dy())
		defer t.done(px)
		if t.grey {
			g := &image.Gray{Pix: px[:rect.Dx()*rect.Dy()], Stride: rect.Dx(), Rect: rect}
			for y := top; y < bottom; y++ {
				src := l.rows[y-l.y0][4*left : 4*right]
				dst := g.Pix[(y-top)*g.Stride:]
				for i := range right - left {
					dst[i] = src[4*i]
				}
			}
			img = g
		} else {
			n := &image.NRGBA{Pix: px, Stride: 4 * rect.Dx(), Rect: rect}
			for y := top; y < bottom; y++ {
				copy(n.Pix[(y-top)*n.Stride:], l.rows[y-l.y0][4*left:4*right])
			}
			img = n
		}
		name := t.full(t.tileName(l, x, r))
		if t.zip != nil {
			names[x], data[x] = name, new(bytes.Buffer)
			tiles[x] = t.encode(data[x], t.format, t.options, img)
			return
		}
		tiles[x] = t.writeTile(name, img)
	})
	if err := errors.Join(tiles...); err != nil {
		return err
	}
	for x, b := range data {
		if b != nil {
			if err := t.addZip(names[x], b.Bytes()); err != nil {
				return err
			}
		}
	}
	return nil
}

// blank reports whether every value of a tile is within the threshold of
// the background's (alpha only for an image with transparency).
func (l *tileLevel) blank(top, bottom, left, right int) bool {
	t := l.t
	ink := [4]int{int(t.ink.R), int(t.ink.G), int(t.ink.B), int(t.ink.A)}
	bands := 3
	if t.alpha {
		bands = 4
	}
	for y := top; y < bottom; y++ {
		row := l.rows[y-l.y0][4*left : 4*right]
		for i := 0; i < len(row); i += 4 {
			for c := range bands {
				if d := int(row[i+c]) - ink[c]; d > t.skip || -d > t.skip {
					return false
				}
			}
		}
	}
	return true
}

// tileName is the file of tile x, y of a level, from the top of the
// pyramid, as libvips names it.
func (t *tiler) tileName(l *tileLevel, x, y int) string {
	switch t.layout {
	case TileDeepZoom:
		return fmt.Sprintf("%d/%d_%d.%s", l.n, x, y, t.ext)
	case TileGoogle:
		return fmt.Sprintf("%d/%d/%d.%s", l.n, y, x, t.ext)
	case TileZoomify:
		t.count.Add(1)
		k := l.before + y*l.tilesAcr + x
		return fmt.Sprintf("TileGroup%d/%d-%d-%d.%s", k/256, l.n, x, y, t.ext)
	}
	// IIIF: the region in pixels of the image, clipped to it, and the size
	// in pixels of the level.
	span := int64(t.size) * int64(l.sub)
	left, top := int64(x)*span, int64(y)*span
	w, h := min(span, int64(t.w)-left), min(span, int64(t.h)-top)
	region := fmt.Sprintf("%d,%d,%d,%d", left, top, w, h)
	if left == 0 && top == 0 && w == int64(t.w) && h == int64(t.h) {
		region = "full"
	}
	size := fmt.Sprintf("%d,", min(t.size, l.w-x*t.size))
	if t.layout == TileIIIF3 {
		size += fmt.Sprint(min(t.size, l.h-y*t.size))
	}
	return region + "/" + size + "/0/default." + t.ext
}

// writeTile encodes one tile to its file.
func (t *tiler) writeTile(name string, img image.Image) error {
	if err := os.MkdirAll(filepath.Dir(name), 0o777); err != nil {
		return err
	}
	f, err := os.Create(name)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	err = t.encode(w, t.format, t.options, img)
	if err == nil {
		err = w.Flush()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// encode writes a tile in a format, without metadata.
func (t *tiler) encode(w io.Writer, format string, options any, img image.Image) error {
	src := pipeline.FromImage(img, t.enc)
	defer src.Close()
	return t.m.encodeWith(w, src, format, options, 0, pipeline.Meta{})
}
