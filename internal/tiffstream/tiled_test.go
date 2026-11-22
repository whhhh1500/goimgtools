package tiffstream

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"math"
	"testing"

	"golang.org/x/image/tiff"

	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// opaqueSource says of a source that it has no transparency.
type opaqueSource struct{ pipeline.Source }

func (o opaqueSource) Env() *pipeline.Env { return pipeline.EnvOf(o.Source) }
func (o opaqueSource) Opaque() bool       { return true }

// directories is where the directories of a little-endian TIFF are, each
// with its entries' first values by tag.
func directories(t *testing.T, data []byte) (offs []uint32, tags []map[uint16]uint32) {
	t.Helper()
	le := binary.LittleEndian
	for off := le.Uint32(data[4:]); off != 0; {
		if int(off)+2 > len(data) || len(offs) > 20 {
			t.Fatalf("directory at %d of %d bytes", off, len(data))
		}
		n := int(le.Uint16(data[off:]))
		m := map[uint16]uint32{}
		for i := 0; i < n; i++ {
			e := data[int(off)+2+12*i:][:12]
			v := le.Uint32(e[8:])
			if le.Uint16(e[2:]) == dtShort && le.Uint32(e[4:]) <= 2 {
				v = uint32(le.Uint16(e[8:]))
			}
			m[le.Uint16(e)] = v
		}
		offs, tags = append(offs, off), append(tags, m)
		off = le.Uint32(data[int(off)+2+12*n:])
	}
	return offs, tags
}

// level is the TIFF data with directory k as its first, for a reader of
// the first image.
func level(data []byte, off uint32) []byte {
	out := append([]byte(nil), data...)
	binary.LittleEndian.PutUint32(out[4:], off)
	return out
}

// halved is the image at half the size: each sample the mean of the four
// above it (rounded half up), the last column and row of an odd size left
// out, in the samples a tiled file holds (grey, RGB or RGBA as written).
func halved(pix []byte, w, h, spp int) ([]byte, int, int) {
	nw, nh := max(1, w/2), max(1, h/2)
	out := make([]byte, nw*nh*spp)
	for y := 0; y < nh; y++ {
		y1 := min(2*y+1, h-1)
		for x := 0; x < nw; x++ {
			x1 := min(2*x+1, w-1)
			for c := 0; c < spp; c++ {
				out[(y*nw+x)*spp+c] = uint8((int(pix[(2*y*w+2*x)*spp+c]) + int(pix[(2*y*w+x1)*spp+c]) +
					int(pix[(y1*w+2*x)*spp+c]) + int(pix[(y1*w+x1)*spp+c]) + 2) >> 2)
			}
		}
	}
	return out, nw, nh
}

// TestTiled: a TIFF written in tiles, and a pyramid of them, holds the
// image (every pixel type, at 8 bits; without loss but for JPEG), reads
// back with this package and with x/image, and each level of a pyramid is
// the level before at half the size (the mean of four), down to one that
// fits in a tile, marked as a reduced image.
func TestTiled(t *testing.T) {
	sizes := []image.Point{{1, 1}, {70, 1}, {1, 70}, {17, 33}, {100, 75}, {130, 67}}
	comps := []struct {
		name string
		o    Options
	}{
		{"none", Options{}},
		{"deflate", Options{Compression: Deflate}},
		{"deflate, predictor", Options{Compression: Deflate, Predictor: true}},
		{"lzw, predictor", Options{Compression: LZW, Predictor: true}},
		{"jpeg", Options{Compression: JPEG, Quality: 92}},
	}
	n := 0
	for _, size := range sizes {
		ms := wtImages(size.X, size.Y)
		for _, name := range wtNames(ms) {
			m := ms[name]
			for _, c := range comps {
				for _, tile := range []int{16, 64} {
					for _, pyramid := range []bool{false, true} {
						for _, opaque := range []bool{false, true} {
							what := fmt.Sprintf("%s %v, %s, tiles of %d, pyramid %v, opaque %v", name, size, c.name, tile, pyramid, opaque)
							_, g1 := m.(*image.Gray)
							_, g2 := m.(*image.Gray16)
							gray := g1 || g2
							if gray && opaque {
								continue // grey is opaque as it is
							}
							env := pipeline.NewEnv(0, t.TempDir())
							var src pipeline.Source = testutil.ChoppedEnv(m, 7, env)
							if opaque {
								src = opaqueSource{src}
							}
							o := c.o
							o.Tile, o.Pyramid = tile, pyramid
							var buf bytes.Buffer
							err := Encode(&buf, src, &o)
							jpeg := o.Compression == JPEG
							if jpeg && !gray && !opaque {
								if !errors.Is(err, errJPEGAlpha) {
									t.Errorf("%s: %v, want the error of JPEG with transparency", what, err)
								}
								continue
							}
							if err != nil {
								t.Errorf("%s: %v", what, err)
								continue
							}
							wtCheckClean(t, what, env)
							n++
							// The samples written: grey, RGB, or RGBA as
							// the source has it (multiplied by alpha or not).
							spp := 4
							if gray {
								spp = 1
							} else if opaque {
								spp = 3
							}
							want := make([]byte, size.X*size.Y*spp)
							for y := 0; y < size.Y; y++ {
								for x := 0; x < size.X; x++ {
									p := want[(y*size.X+x)*spp:]
									switch v := m.(type) {
									case *image.Gray:
										p[0] = v.GrayAt(x, y).Y
									case *image.Gray16:
										p[0] = uint8(v.Gray16At(x, y).Y >> 8)
									case *image.NRGBA:
										c := v.NRGBAAt(x, y)
										copy(p[:spp], []byte{c.R, c.G, c.B, c.A})
									default:
										r, g, b, a := m.At(x, y).RGBA()
										copy(p[:spp], []byte{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), uint8(a >> 8)})
									}
								}
							}
							offs, tags := directories(t, buf.Bytes())
							w, h := size.X, size.Y
							for k, off := range offs {
								got, err := decodeStream(level(buf.Bytes(), off), nil)
								if err != nil {
									t.Errorf("%s, level %d: %v", what, k, err)
									break
								}
								tg := tags[k]
								if got.Bounds() != image.Rect(0, 0, w, h) || tg[tTileWidth] != uint32(tile) || tg[tTileLength] != uint32(tile) ||
									(k > 0) != (tg[tNewSubfileType] == 1) {
									t.Errorf("%s, level %d: %v (want %dx%d), tiles of %d, reduced %d", what, k, got.Bounds(), w, h, tg[tTileWidth], tg[tNewSubfileType])
									break
								}
								// Alpha that is not multiplied in stays so.
								if _, straight := m.(*image.NRGBA); spp == 4 {
									if _, ok := got.(*image.NRGBA); ok != straight {
										t.Errorf("%s, level %d: read as %T", what, k, got)
										break
									}
								}
								// Every pixel, in the samples written.
								worst := 0
								for y := 0; y < h; y++ {
									for x := 0; x < w; x++ {
										p := want[(y*w+x)*spp:][:spp]
										var q [4]uint8
										switch v := got.(type) {
										case *image.Gray:
											q[0] = v.GrayAt(x, y).Y
										case *image.NRGBA:
											c := v.NRGBAAt(x, y)
											q = [4]uint8{c.R, c.G, c.B, c.A}
										case *image.RGBA:
											c := v.RGBAAt(x, y)
											q = [4]uint8{c.R, c.G, c.B, c.A}
										default:
											t.Fatalf("%s: read as %T", what, got)
										}
										for i := range p {
											worst = max(worst, int(math.Abs(float64(p[i])-float64(q[i]))))
										}
									}
								}
								if limit := map[bool]int{false: 0, true: 60}[jpeg]; worst > limit {
									t.Errorf("%s, level %d: samples differ by up to %d", what, k, worst)
									break
								}
								if k == 0 && !jpeg && spp != 1 {
									// Another reader agrees on the first image.
									ref, err := tiff.Decode(bytes.NewReader(buf.Bytes()))
									if err != nil {
										t.Errorf("%s: x/image: %v", what, err)
									} else if err := same(ref, got); err != nil {
										t.Errorf("%s: x/image reads another image: %v", what, err)
									}
								}
								want, w, h = halved(want, w, h, spp)
							}
							levels := 1
							for lw, lh := size.X, size.Y; pyramid && (lw > tile || lh > tile); lw, lh = max(1, lw/2), max(1, lh/2) {
								levels++
							}
							if len(offs) != levels {
								t.Errorf("%s: %d levels, want %d", what, len(offs), levels)
							}
						}
					}
				}
			}
		}
		if t.Failed() {
			return
		}
	}
	t.Logf("%d files", n)

	// What the file carries is in its first directory.
	m := testutil.Images(40, 30)["nrgba"]
	icc, xmp, iptc := bytes.Repeat([]byte{7}, 600), []byte("<x:xmpmeta/>"), []byte("\x1c\x02\x78\x00\x02hi")
	var buf bytes.Buffer
	env := pipeline.NewEnv(0, t.TempDir())
	if err := Encode(&buf, testutil.ChoppedEnv(m, 5, env), &Options{Tile: 16, Pyramid: true, Compression: Deflate, ICC: icc, Xmp: xmp, Iptc: iptc, Density: 300}); err != nil {
		t.Fatal(err)
	}
	src, err := Open(bytes.NewReader(buf.Bytes()), int64(buf.Len()), nil)
	if err != nil {
		t.Fatal(err)
	}
	meta := src.(interface{ Meta() pipeline.Meta }).Meta()
	src.Close()
	if !bytes.Equal(meta.ICC, icc) || !bytes.Equal(meta.Xmp, xmp) || !bytes.Equal(meta.Iptc, iptc) || meta.Density != 300 {
		t.Errorf("metadata of a tiled file: profile %d bytes, XMP %q, IPTC %q, density %v", len(meta.ICC), meta.Xmp, meta.Iptc, meta.Density)
	}

	// A tile's side that is not one; a writer that fails.
	for _, tile := range []int{8, 100, 8200, -16} {
		if err := Encode(&bytes.Buffer{}, testutil.Chopped(m, 5), &Options{Tile: tile}); err == nil {
			t.Errorf("tiles of %d: written", tile)
		}
	}
	buf.Reset()
	if err := Encode(&buf, testutil.Chopped(m, 5), &Options{Tile: 16, Pyramid: true, Compression: Deflate}); err != nil {
		t.Fatal(err)
	}
	for limit := 0; limit < buf.Len(); limit += 97 {
		env := pipeline.NewEnv(0, t.TempDir())
		if err := Encode(&wtFailWriter{left: limit}, testutil.ChoppedEnv(m, 5, env), &Options{Tile: 16, Pyramid: true, Compression: Deflate}); err == nil {
			t.Fatalf("a writer that fails after %d bytes: no error", limit)
		}
		wtCheckClean(t, "a failing writer", env)
	}
}

// TestTiledMemory: a tiled pyramid of a large image keeps to a row of
// tiles of each level, counted in the budget, whatever the image's height.
func TestTiledMemory(t *testing.T) {
	const w, h, tile = 3000, 2600, 256
	m := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(m.Pix); i += 4 {
		m.Pix[i], m.Pix[i+1], m.Pix[i+2], m.Pix[i+3] = uint8(i), uint8(i>>9), uint8(i>>14), 255
	}
	env := pipeline.NewEnv(24<<20, t.TempDir())
	var out wtSpillWatch
	if err := Encode(&out, opaqueSource{testutil.ChoppedEnv(m, 64, env)}, &Options{Tile: tile, Pyramid: true, Compression: Deflate, Predictor: true}); err != nil {
		t.Fatal(err)
	}
	// The rows of tiles of the levels: 3 bytes a pixel, the levels
	// together under twice the first; beside them a tile, the pairs and
	// the copy buffer.
	// (and the compressed tiles not yet written, up to their share).
	if peak, limit := env.Budget.Peak(), int64(2*w*3*tile+1<<20)+env.Budget.Limit()/pipeline.StagingShare; peak > limit {
		t.Errorf("%d bytes reserved at the peak, want under %d", peak, limit)
	} else {
		t.Logf("peak %dKB for an image of %dKB", peak>>10, w*h*3>>10)
	}
	wtCheckClean(t, "a large pyramid", env)
}
