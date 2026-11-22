package tiffstream

import (
	"bytes"
	"compress/bzip2"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"

	"golang.org/x/image/tiff"

	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// xImageDir returns the directory of the golang.org/x/image module in use.
func xImageDir(t testing.TB) string {
	dir := testutil.XImageDir()
	if dir == "" {
		t.Fatal("golang.org/x/image module directory not found")
	}
	return dir
}

// corpus is every TIFF in x/image's testdata directories, by name.
func corpus(t testing.TB) map[string][]byte {
	dir := xImageDir(t)
	files := map[string][]byte{}
	for _, pat := range []string{"testdata/*.tiff", "tiff/testdata/*.tiff", "tiff/testdata/*.tiff.bz2"} {
		names, _ := filepath.Glob(filepath.Join(dir, filepath.FromSlash(pat)))
		for _, f := range names {
			data, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasSuffix(f, ".bz2") {
				if data, err = io.ReadAll(bzip2.NewReader(bytes.NewReader(data))); err != nil {
					t.Fatal(err)
				}
			}
			files[filepath.Base(f)] = data
		}
	}
	if len(files) < 30 {
		t.Fatalf("x/image testdata not found in %s (%d files)", dir, len(files))
	}
	return files
}

func sortedNames(m map[string][]byte) []string {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// decodeStream decodes data with this package and checks the Source
// contract on the way: full-width strips, contiguous, covering the bounds.
func decodeStream(data []byte, env *pipeline.Env) (image.Image, error) {
	src, err := Open(bytes.NewReader(data), int64(len(data)), env)
	if err != nil {
		if err == io.EOF {
			return nil, fmt.Errorf("Open returned a bare io.EOF")
		}
		return nil, err
	}
	defer src.Close()
	b := src.Bounds()
	var out image.Image
	y := b.Min.Y
	for {
		s, err := src.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		sb := s.Bounds()
		if sb.Min.X != b.Min.X || sb.Max.X != b.Max.X || sb.Min.Y != y || sb.Max.Y <= y || sb.Max.Y > b.Max.Y {
			panic(fmt.Sprintf("strip %v does not continue row %d of %v", sb, y, b))
		}
		y = sb.Max.Y
		if out == nil {
			out = pipeline.NewBand(s, b)
		}
		pipeline.CopyRows(out, s, sb.Min.Y, sb.Max.Y)
	}
	if y != b.Max.Y {
		panic(fmt.Sprintf("strips end at row %d of %v", y, b))
	}
	if _, err := src.Next(); err != io.EOF {
		panic(fmt.Sprintf("Next after the end: %v", err))
	}
	if src.ColorModel() == nil {
		panic("nil ColorModel")
	}
	return out, nil
}

func pixOf(m image.Image) ([]byte, color.Palette) {
	switch p := m.(type) {
	case *image.Gray:
		return p.Pix, nil
	case *image.Gray16:
		return p.Pix, nil
	case *image.RGBA:
		return p.Pix, nil
	case *image.RGBA64:
		return p.Pix, nil
	case *image.NRGBA:
		return p.Pix, nil
	case *image.NRGBA64:
		return p.Pix, nil
	case *image.Paletted:
		return p.Pix, p.Palette
	}
	panic(fmt.Sprintf("unexpected image type %T", m))
}

// same reports how got differs from want: concrete type, bounds, palette
// and every pixel byte must match.
func same(want, got image.Image) error {
	if reflect.TypeOf(want) != reflect.TypeOf(got) {
		return fmt.Errorf("type %T, want %T", got, want)
	}
	if want.Bounds() != got.Bounds() {
		return fmt.Errorf("bounds %v, want %v", got.Bounds(), want.Bounds())
	}
	wp, wpal := pixOf(want)
	gp, gpal := pixOf(got)
	if !reflect.DeepEqual(wpal, gpal) {
		return fmt.Errorf("palettes differ")
	}
	if !bytes.Equal(wp, gp) {
		for i := range wp {
			if wp[i] != gp[i] {
				return fmt.Errorf("pixel byte %d of %d = %#x, want %#x", i, len(wp), gp[i], wp[i])
			}
		}
		return fmt.Errorf("pixel buffers differ in length: %d, want %d", len(gp), len(wp))
	}
	return nil
}

// compare decodes data with the reference and with this package. It fails
// the test unless both error or both give the same image, and reports
// whether the reference decoded.
func compare(t *testing.T, name string, data []byte) bool {
	t.Helper()
	want, werr := tiff.Decode(bytes.NewReader(data))
	got, gerr := decodeStream(data, nil)
	if werr != nil {
		if gerr == nil {
			t.Errorf("%s: reference error %q, stream decoded fine", name, werr)
		}
		return false
	}
	if gerr != nil {
		t.Errorf("%s: stream error %q, reference decoded fine", name, gerr)
		return false
	}
	if err := same(want, got); err != nil {
		t.Errorf("%s: %v", name, err)
	}
	return true
}

// TestXImageTestdata decodes every TIFF of x/image's test corpus.
func TestXImageTestdata(t *testing.T) {
	files := corpus(t)
	ok, bad := 0, 0
	for _, name := range sortedNames(files) {
		if compare(t, name, files[name]) {
			ok++
		} else {
			bad++
		}
	}
	t.Logf("%d files matched, %d rejected by both decoders", ok, bad)
	if ok < 25 {
		t.Errorf("only %d files decoded", ok)
	}
}

func randomImages(w, h int) map[string]image.Image {
	r := rand.New(rand.NewSource(1))
	rect := image.Rect(0, 0, w, h)
	// Noise with some flat runs, so the compressors have something to do.
	fill := func(pix []byte) {
		for i := 0; i < len(pix); {
			n := 1
			if r.Intn(4) == 0 {
				n = 1 + r.Intn(40)
			}
			v := byte(r.Intn(256))
			for ; n > 0 && i < len(pix); n-- {
				pix[i] = v
				i++
			}
		}
	}
	gray := image.NewGray(rect)
	fill(gray.Pix)
	gray16 := image.NewGray16(rect)
	fill(gray16.Pix)
	rgba := image.NewRGBA(rect)
	fill(rgba.Pix)
	nrgba := image.NewNRGBA(rect)
	fill(nrgba.Pix)
	nrgba64 := image.NewNRGBA64(rect)
	fill(nrgba64.Pix)
	rgba64 := image.NewRGBA64(rect)
	fill(rgba64.Pix)
	pal := make(color.Palette, 256)
	for i := range pal {
		pal[i] = color.RGBA{uint8(i), uint8(255 - i), uint8(i * 3), 0xff}
	}
	paletted := image.NewPaletted(rect, pal)
	fill(paletted.Pix)
	return map[string]image.Image{
		"Gray": gray, "Gray16": gray16, "Paletted": paletted, "NRGBA": nrgba,
		"RGBA": rgba, "RGBA64": rgba64, "NRGBA64": nrgba64,
	}
}

var encoderOptions = []struct {
	name string
	opt  *tiff.Options
}{
	{"uncompressed", &tiff.Options{Compression: tiff.Uncompressed}},
	{"uncompressed+predictor", &tiff.Options{Compression: tiff.Uncompressed, Predictor: true}},
	{"deflate", &tiff.Options{Compression: tiff.Deflate}},
	{"deflate+predictor", &tiff.Options{Compression: tiff.Deflate, Predictor: true}},
}

// TestEncoded decodes the output of x/image's encoder, which writes one
// giant strip, for every image type and encoder option. The images are tall
// enough to come out as many strips.
func TestEncoded(t *testing.T) {
	for _, size := range []image.Point{{397, 611}, {2000, 1500}} {
		imgs := randomImages(size.X, size.Y)
		for _, kind := range []string{"Gray", "Gray16", "Paletted", "NRGBA", "RGBA", "RGBA64", "NRGBA64"} {
			m := imgs[kind]
			for _, o := range encoderOptions {
				name := fmt.Sprintf("%dx%d/%s/%s", size.X, size.Y, kind, o.name)
				var buf bytes.Buffer
				if err := tiff.Encode(&buf, m, o.opt); err != nil {
					t.Fatalf("%s: encode: %v", name, err)
				}
				data := buf.Bytes()
				want, err := tiff.Decode(bytes.NewReader(data))
				if err != nil {
					t.Fatalf("%s: reference: %v", name, err)
				}
				env := pipeline.NewEnv(0, t.TempDir())
				got, err := decodeStream(data, env)
				if err != nil {
					t.Errorf("%s: %v", name, err)
					continue
				}
				if err := same(want, got); err != nil {
					t.Errorf("%s: %v", name, err)
				}
				if env.Budget.Used() != 0 {
					t.Errorf("%s: %d bytes still reserved after Close", name, env.Budget.Used())
				}
				if size.X == 2000 && !o.opt.Predictor {
					t.Logf("%-34s file %8d bytes, budget peak %7d bytes", name, len(data), env.Budget.Peak())
				}
			}
		}
	}
}

// ---- hand-built TIFF files ----

type ifdEntry struct {
	tag  uint16
	typ  uint16 // dtShort or dtLong
	vals []uint32
}

// spec describes a TIFF to build by hand from random sample data.
type spec struct {
	w, h       int
	bps        []uint32 // BitsPerSample, one per sample
	photo      uint32
	extra      uint32 // ExtraSamples value, 0 for none
	pred       bool
	comp       uint32 // 0 leaves the Compression tag out
	bo         binary.ByteOrder
	rps        int // RowsPerStrip; 0 leaves the tag out (one strip)
	tw, th     int // tile size; 0 for a stripped file
	shortTiles bool
	ifdFirst   bool // put the IFD before the pixel data
}

func (s spec) String() string {
	layout := fmt.Sprintf("rps%d", s.rps)
	if s.tw != 0 {
		layout = fmt.Sprintf("tile%dx%d", s.tw, s.th)
		if s.shortTiles {
			layout += "-short"
		}
	}
	bo := "LE"
	if s.bo == binary.BigEndian {
		bo = "BE"
	}
	return fmt.Sprintf("%dx%d/bps%v/photo%d/extra%d/pred%v/comp%d/%s/%s",
		s.w, s.h, s.bps, s.photo, s.extra, s.pred, s.comp, bo, layout)
}

// lzwLiteral is a minimal TIFF LZW encoder: literal codes only, with a
// ClearCode often enough that the code width stays at 9 bits.
func lzwLiteral(data []byte) []byte {
	var out []byte
	var acc uint32
	var nbits uint
	put := func(code uint32) {
		acc = acc<<9 | code
		nbits += 9
		for nbits >= 8 {
			out = append(out, byte(acc>>(nbits-8)))
			nbits -= 8
		}
	}
	put(256)
	n := 0
	for _, b := range data {
		if n == 200 {
			put(256)
			n = 0
		}
		put(uint32(b))
		n++
	}
	put(257)
	if nbits > 0 {
		out = append(out, byte(acc<<(8-nbits)))
	}
	return out
}

// packBits is a PackBits encoder with both literal and replicate packets.
func packBits(data []byte) []byte {
	var out []byte
	for i := 0; i < len(data); {
		run := 1
		for i+run < len(data) && run < 128 && data[i+run] == data[i] {
			run++
		}
		if run >= 3 {
			out = append(out, byte(int8(1-run)), data[i])
			i += run
			continue
		}
		j := i
		for j < len(data) && j-i < 128 {
			if j+2 < len(data) && data[j] == data[j+1] && data[j] == data[j+2] {
				break
			}
			j++
		}
		out = append(out, byte(j-i-1))
		out = append(out, data[i:j]...)
		i = j
	}
	return out
}

func compressBlock(comp uint32, data []byte) []byte {
	switch comp {
	case cDeflate, cDeflateOld:
		var buf bytes.Buffer
		zw := zlib.NewWriter(&buf)
		zw.Write(data)
		zw.Close()
		return buf.Bytes()
	case cLZW:
		return lzwLiteral(data)
	case cPackBits:
		return packBits(data)
	case cZstd:
		return zstdStored(data)
	}
	return data
}

// zstdStored is data as a zstd frame of blocks that are not compressed (a
// window of 1MB, no checksum, blocks of at most 100,000 bytes).
func zstdStored(data []byte) []byte {
	out := []byte{0x28, 0xb5, 0x2f, 0xfd, 0x00, 0x50}
	for first := true; first || len(data) > 0; first = false {
		n := min(len(data), 100000)
		h := uint32(n) << 3
		if n == len(data) {
			h |= 1 // the last block
		}
		out = append(out, byte(h), byte(h>>8), byte(h>>16))
		out = append(out, data[:n]...)
		data = data[n:]
	}
	return out
}

// build writes the TIFF described by s, with random samples from r.
func build(s spec, r *rand.Rand) []byte {
	rowBytes := func(w int) int { return (w*len(s.bps)*int(s.bps[0]) + 7) / 8 }
	random := func(n int) []byte {
		p := make([]byte, n)
		for i := 0; i < n; {
			k := 1
			if r.Intn(4) == 0 {
				k = 1 + r.Intn(40)
			}
			v := byte(r.Intn(256))
			for ; k > 0 && i < n; k-- {
				p[i] = v
				i++
			}
		}
		return p
	}

	out := []byte(leHeader + "\x00\x00\x00\x00")
	if s.bo == binary.BigEndian {
		out = []byte(beHeader + "\x00\x00\x00\x00")
	}
	var offsets, counts []uint32
	add := func(raw []byte) {
		c := compressBlock(s.comp, raw)
		offsets = append(offsets, uint32(len(out)))
		counts = append(counts, uint32(len(c)))
		out = append(out, c...)
		if len(out)%2 != 0 {
			out = append(out, 0)
		}
	}
	if s.tw != 0 {
		for j := 0; j*s.th < s.h; j++ {
			rows := s.th
			if s.shortTiles {
				rows = min(s.th, s.h-j*s.th)
			}
			for i := 0; i*s.tw < s.w; i++ {
				add(random(rows * rowBytes(s.tw)))
			}
		}
	} else {
		rps := s.rps
		if rps == 0 {
			rps = s.h
		}
		for y := 0; y < s.h; y += rps {
			add(random(min(rps, s.h-y) * rowBytes(s.w)))
		}
	}

	ifd := []ifdEntry{
		{tImageWidth, dtLong, []uint32{uint32(s.w)}},
		{tImageLength, dtLong, []uint32{uint32(s.h)}},
		{tBitsPerSample, dtShort, s.bps},
		{tPhotometricInterpretation, dtShort, []uint32{s.photo}},
		{tSamplesPerPixel, dtShort, []uint32{uint32(len(s.bps))}},
	}
	if s.comp != 0 {
		ifd = append(ifd, ifdEntry{tCompression, dtShort, []uint32{s.comp}})
	}
	if s.tw != 0 {
		ifd = append(ifd,
			ifdEntry{tTileWidth, dtShort, []uint32{uint32(s.tw)}},
			ifdEntry{tTileLength, dtShort, []uint32{uint32(s.th)}},
			ifdEntry{tTileOffsets, dtLong, offsets},
			ifdEntry{tTileByteCounts, dtLong, counts})
	} else {
		if s.rps != 0 {
			ifd = append(ifd, ifdEntry{tRowsPerStrip, dtShort, []uint32{uint32(s.rps)}})
		}
		ifd = append(ifd,
			ifdEntry{tStripOffsets, dtLong, offsets},
			ifdEntry{tStripByteCounts, dtLong, counts})
	}
	if s.pred {
		ifd = append(ifd, ifdEntry{tPredictor, dtShort, []uint32{prHorizontal}})
	}
	if s.extra != 0 {
		ifd = append(ifd, ifdEntry{tExtraSamples, dtShort, []uint32{s.extra}})
	}
	if s.photo == pPaletted {
		n := 1 << min(s.bps[0], 8)
		cm := make([]uint32, 3*n)
		for i := range cm {
			cm[i] = uint32(r.Intn(65536))
		}
		ifd = append(ifd, ifdEntry{tColorMap, dtShort, cm})
	}
	sort.Slice(ifd, func(i, j int) bool { return ifd[i].tag < ifd[j].tag })

	// serialize lays the IFD out at ifdOffset, with the values that do not
	// fit in an entry right after it.
	serialize := func(ifdOffset int) (table, extra []byte) {
		table = make([]byte, 2+ifdLen*len(ifd)+4)
		s.bo.PutUint16(table, uint16(len(ifd)))
		extraOffset := ifdOffset + len(table)
		for i, e := range ifd {
			p := table[2+ifdLen*i:]
			s.bo.PutUint16(p[0:], e.tag)
			s.bo.PutUint16(p[2:], e.typ)
			s.bo.PutUint32(p[4:], uint32(len(e.vals)))
			size := 2
			if e.typ == dtLong {
				size = 4
			}
			val := make([]byte, size*len(e.vals))
			for k, v := range e.vals {
				if e.typ == dtLong {
					s.bo.PutUint32(val[4*k:], v)
				} else {
					s.bo.PutUint16(val[2*k:], uint16(v))
				}
			}
			if len(val) <= 4 {
				copy(p[8:12], val)
			} else {
				s.bo.PutUint32(p[8:], uint32(extraOffset+len(extra)))
				extra = append(extra, val...)
			}
		}
		return table, extra
	}
	if s.ifdFirst {
		// Header, IFD, values, then the blocks: a truncated file of this
		// shape still has its whole IFD.
		table, extra := serialize(8)
		shift := uint32(len(table) + len(extra))
		for i := range offsets {
			offsets[i] += shift
		}
		table, extra = serialize(8)
		blocks := out[8:]
		s.bo.PutUint32(out[4:8], 8)
		res := append([]byte(nil), out[:8]...)
		res = append(res, table...)
		res = append(res, extra...)
		return append(res, blocks...)
	}
	ifdOffset := len(out)
	s.bo.PutUint32(out[4:8], uint32(ifdOffset))
	table, extra := serialize(ifdOffset)
	out = append(out, table...)
	return append(out, extra...)
}

type pixelMode struct {
	name  string
	bps   []uint32
	photo uint32
	extra uint32
}

// Every mode x/image's decoder accepts (BitsPerSample of 1, 8 or 16).
var pixelModes = []pixelMode{
	{"gray1", []uint32{1}, pBlackIsZero, 0},
	{"gray1-invert", []uint32{1}, pWhiteIsZero, 0},
	{"gray8", []uint32{8}, pBlackIsZero, 0},
	{"gray8-invert", []uint32{8}, pWhiteIsZero, 0},
	{"gray16", []uint32{16}, pBlackIsZero, 0},
	{"gray16-invert", []uint32{16}, pWhiteIsZero, 0},
	{"paletted1", []uint32{1}, pPaletted, 0},
	{"paletted8", []uint32{8}, pPaletted, 0},
	{"paletted16", []uint32{16}, pPaletted, 0},
	{"rgb8", []uint32{8, 8, 8}, pRGB, 0},
	{"rgb16", []uint32{16, 16, 16}, pRGB, 0},
	{"rgba8", []uint32{8, 8, 8, 8}, pRGB, 1},
	{"nrgba8", []uint32{8, 8, 8, 8}, pRGB, 2},
	{"rgba16", []uint32{16, 16, 16, 16}, pRGB, 1},
	{"nrgba16", []uint32{16, 16, 16, 16}, pRGB, 2},
}

type layout struct {
	rps, tw, th int
	shortTiles  bool
}

func runSpecs(t *testing.T, sizes []image.Point, comps []uint32, orders []binary.ByteOrder, layouts []layout) {
	r := rand.New(rand.NewSource(7))
	ok, rejected := 0, 0
	for _, size := range sizes {
		for _, m := range pixelModes {
			for _, comp := range comps {
				for _, pred := range []bool{false, true} {
					for _, bo := range orders {
						for _, l := range layouts {
							s := spec{w: size.X, h: size.Y, bps: m.bps, photo: m.photo, extra: m.extra,
								pred: pred, comp: comp, bo: bo, rps: l.rps, tw: l.tw, th: l.th, shortTiles: l.shortTiles}
							if compare(t, s.String(), build(s, r)) {
								ok++
							} else {
								rejected++
								// Only these are expected to be rejected by the reference.
								if !(pred && m.bps[0] == 1) && !(pred && l.shortTiles) {
									t.Errorf("%s: reference rejects a file that should be valid", s)
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
	}
	t.Logf("%d files matched, %d rejected by both decoders", ok, rejected)
}

var bothOrders = []binary.ByteOrder{binary.LittleEndian, binary.BigEndian}

// TestHandBuiltSmall runs every mode, compression, predictor setting, byte
// order and block layout on small images, including strips of one row and
// tiles that do not divide the image size (and tiles larger than the image).
func TestHandBuiltSmall(t *testing.T) {
	runSpecs(t,
		[]image.Point{{100, 75}, {53, 41}, {1, 9}},
		[]uint32{0, cNone, cLZW, cDeflate, cPackBits, cDeflateOld},
		bothOrders,
		[]layout{
			{rps: 0}, {rps: 1}, {rps: 7}, {rps: 64},
			{tw: 16, th: 16}, {tw: 40, th: 24}, {tw: 64, th: 48},
			{tw: 40, th: 24, shortTiles: true},
		})
}

// TestHandBuiltManyStrips uses images tall enough that the output strips do
// not line up with the TIFF strips or tiles.
func TestHandBuiltManyStrips(t *testing.T) {
	runSpecs(t,
		[]image.Point{{397, 611}},
		[]uint32{cNone, cDeflate, cPackBits},
		bothOrders[:1],
		[]layout{
			{rps: 1}, {rps: 7}, {rps: 100}, {rps: 611},
			{tw: 64, th: 64}, {tw: 150, th: 200},
		})
}

// TestHandBuiltLarge is the multi-strip and tiled 2000x1500 case for
// uncompressed and deflate RGB and gray.
func TestHandBuiltLarge(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	for _, m := range []pixelMode{pixelModes[2], pixelModes[9]} { // gray8, rgb8
		for _, comp := range []uint32{cNone, cDeflate} {
			for _, l := range []layout{{rps: 64}, {rps: 1}, {tw: 256, th: 256}, {tw: 304, th: 176}} {
				s := spec{w: 2000, h: 1500, bps: m.bps, photo: m.photo, pred: comp == cDeflate,
					comp: comp, bo: binary.LittleEndian, rps: l.rps, tw: l.tw, th: l.th}
				data := build(s, r)
				want, err := tiff.Decode(bytes.NewReader(data))
				if err != nil {
					t.Fatalf("%s: reference: %v", s, err)
				}
				env := pipeline.NewEnv(0, t.TempDir())
				got, err := decodeStream(data, env)
				if err != nil {
					t.Errorf("%s: %v", s, err)
					continue
				}
				if err := same(want, got); err != nil {
					t.Errorf("%s: %v", s, err)
				}
				t.Logf("%-70s budget peak %8d bytes", s, env.Budget.Peak())
			}
		}
	}
}

// ---- truncated and corrupt input ----

// tooBig reports whether a damaged header asks for an image so large that
// running the reference decoder (which allocates it whole) is not sensible.
func tooBig(data []byte) bool {
	cfg, err := tiff.DecodeConfig(bytes.NewReader(data))
	if err != nil || int64(cfg.Width)*int64(cfg.Height) <= 8<<20 {
		return false
	}
	// Still run the stream decoder, under a small budget: it must fail or
	// finish without holding the image.
	decodeStream(data, pipeline.NewEnv(8<<20, ""))
	return true
}

// agree decodes damaged data both ways and requires an error from this
// package exactly when the reference errors, and the same pixels otherwise.
func agree(t *testing.T, what string, data []byte) (refOK bool) {
	t.Helper()
	want, werr := tiff.Decode(bytes.NewReader(data))
	got, gerr := decodeStream(data, nil)
	if (werr == nil) != (gerr == nil) {
		t.Errorf("%s: reference err %v, stream err %v", what, werr, gerr)
		return werr == nil
	}
	if werr == nil {
		if err := same(want, got); err != nil {
			t.Errorf("%s: %v", what, err)
		}
	}
	return werr == nil
}

func damageFiles(t *testing.T) map[string][]byte {
	files := map[string][]byte{}
	for name, data := range corpus(t) {
		if len(data) > 0 {
			files[name] = data
		}
	}
	r := rand.New(rand.NewSource(11))
	le := binary.LittleEndian
	rgb, gray, nrgba16, pal := pixelModes[9], pixelModes[2], pixelModes[14], pixelModes[7]
	add := func(m pixelMode, comp uint32, pred bool, l layout) {
		s := spec{w: 120, h: 90, bps: m.bps, photo: m.photo, extra: m.extra, pred: pred, comp: comp,
			bo: le, rps: l.rps, tw: l.tw, th: l.th, shortTiles: l.shortTiles}
		files[s.String()] = build(s, r)
		s.ifdFirst = true
		files[s.String()+"/ifd-first"] = build(s, r)
	}
	for _, comp := range []uint32{cNone, cDeflate, cLZW, cPackBits} {
		add(rgb, comp, false, layout{rps: 0})
		add(rgb, comp, true, layout{rps: 16})
		add(gray, comp, false, layout{rps: 7})
		add(gray, comp, true, layout{tw: 48, th: 32})
		add(rgb, comp, false, layout{tw: 48, th: 32})
		add(nrgba16, comp, true, layout{tw: 32, th: 64})
		add(pal, comp, false, layout{rps: 5})
		add(pal, comp, false, layout{tw: 48, th: 32, shortTiles: true})
	}
	var buf bytes.Buffer
	if err := tiff.Encode(&buf, randomImages(397, 611)["NRGBA"], &tiff.Options{Compression: tiff.Deflate}); err != nil {
		t.Fatal(err)
	}
	files["encoded-397x611-nrgba-deflate"] = buf.Bytes()
	return files
}

// TestTruncated cuts every file at many lengths.
func TestTruncated(t *testing.T) {
	files := damageFiles(t)
	cases, decoded, skipped := 0, 0, 0
	for _, name := range sortedNames(files) {
		data := files[name]
		step := max(1, len(data)/400)
		for cut := 0; cut < len(data); cut += step {
			if tooBig(data[:cut]) {
				skipped++
				continue
			}
			cases++
			if agree(t, fmt.Sprintf("%s cut at %d of %d", name, cut, len(data)), data[:cut]) {
				decoded++
			}
		}
		if t.Failed() {
			return
		}
	}
	t.Logf("%d files, %d cuts (%d still decode in both), %d skipped as too big", len(files), cases, decoded, skipped)
}

// TestCorrupt flips single bytes all over every file: densely in the header
// and the IFD, and at regular steps in the pixel data.
func TestCorrupt(t *testing.T) {
	files := damageFiles(t)
	cases, decoded, skipped := 0, 0, 0
	for _, name := range sortedNames(files) {
		data := files[name]
		var positions []int
		for i := 0; i < min(16, len(data)); i++ {
			positions = append(positions, i)
		}
		// The IFD and its out-of-line values.
		if len(data) >= 8 {
			var bo binary.ByteOrder = binary.LittleEndian
			if string(data[:4]) == beHeader {
				bo = binary.BigEndian
			}
			off := int(bo.Uint32(data[4:8]))
			for i := off; i < min(off+400, len(data)); i++ {
				positions = append(positions, i)
			}
		}
		step := max(1, len(data)/150)
		for i := 16; i < len(data); i += step {
			positions = append(positions, i)
		}
		bad := make([]byte, len(data))
		for k, i := range positions {
			for _, mask := range []byte{0x5a, 0x01}[:1+k%2] {
				copy(bad, data)
				bad[i] ^= mask
				if tooBig(bad) {
					skipped++
					continue
				}
				cases++
				if agree(t, fmt.Sprintf("%s byte %d ^ %#x", name, i, mask), bad) {
					decoded++
				}
			}
		}
		if t.Failed() {
			return
		}
	}
	t.Logf("%d files, %d corruptions (%d still decode in both), %d skipped as too big", len(files), cases, decoded, skipped)
}

// TestSmallMemoryLimit decodes a 2000x1500 single-strip deflate RGBA TIFF
// (12MB of samples in one block) under a 4MB budget, comparing strip by
// strip so the test itself never holds the stream's output whole.
func TestSmallMemoryLimit(t *testing.T) {
	m := randomImages(2000, 1500)["RGBA"].(*image.RGBA)
	var buf bytes.Buffer
	if err := tiff.Encode(&buf, m, &tiff.Options{Compression: tiff.Deflate}); err != nil {
		t.Fatal(err)
	}
	data := buf.Bytes()
	want, err := tiff.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	wantPix := want.(*image.RGBA)

	env := pipeline.NewEnv(4<<20, t.TempDir())
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	src, err := Open(bytes.NewReader(data), int64(len(data)), env)
	if err != nil {
		t.Fatal(err)
	}
	if got := pipeline.EnvOf(src); got != env {
		t.Errorf("Env() = %p, want %p", got, env)
	}
	if pipeline.IsOpaque(src) {
		t.Errorf("an RGBA TIFF must not report Opaque")
	}
	strips, y := 0, 0
	for {
		s, err := src.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		p := s.(*image.RGBA)
		b := p.Bounds()
		if b.Min.Y != y || b.Dx() != 2000 {
			t.Fatalf("strip %v after row %d", b, y)
		}
		n := b.Dy() * p.Stride
		if !bytes.Equal(p.Pix[:n], wantPix.Pix[y*wantPix.Stride:][:n]) {
			t.Fatalf("strip %v differs from the reference", b)
		}
		y = b.Max.Y
		strips++
	}
	runtime.ReadMemStats(&after)
	if y != 1500 {
		t.Fatalf("got %d rows", y)
	}
	peak := env.Budget.Peak()
	src.Close()
	if env.Budget.Used() != 0 {
		t.Errorf("%d bytes still reserved after Close", env.Budget.Used())
	}
	alloc := after.TotalAlloc - before.TotalAlloc
	t.Logf("file %d bytes, %d strips, budget peak %d bytes, heap allocated while decoding %d bytes",
		len(data), strips, peak, alloc)
	if peak > 1<<20 {
		t.Errorf("budget peak %d bytes, want well under the 12MB block", peak)
	}
	if alloc > 2<<20 {
		t.Errorf("%d bytes allocated while decoding: a block-sized buffer?", alloc)
	}
}

// TestOpaque checks Opaque for the modes without an alpha channel.
func TestOpaque(t *testing.T) {
	r := rand.New(rand.NewSource(5))
	for _, m := range pixelModes {
		s := spec{w: 20, h: 10, bps: m.bps, photo: m.photo, extra: m.extra, comp: cNone, bo: binary.LittleEndian}
		data := build(s, r)
		src, err := Open(bytes.NewReader(data), int64(len(data)), nil)
		if err != nil {
			t.Fatalf("%s: %v", m.name, err)
		}
		if got, want := pipeline.IsOpaque(src), m.extra == 0; got != want {
			t.Errorf("%s: Opaque = %v, want %v", m.name, got, want)
		}
		src.Close()
		if _, err := src.Next(); err == nil || err == io.EOF {
			t.Errorf("%s: Next after Close = %v", m.name, err)
		}
	}
}
