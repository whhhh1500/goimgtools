package pngstream

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/whhhh1500/goimgtools/pipeline"
)

func stdTestdata(t *testing.T) []string {
	dir := filepath.Join(runtime.GOROOT(), "src", "image", "png", "testdata")
	a, _ := filepath.Glob(filepath.Join(dir, "*.png"))
	b, _ := filepath.Glob(filepath.Join(dir, "pngsuite", "*.png"))
	files := append(a, b...)
	if len(files) < 30 {
		t.Fatalf("stdlib testdata not found in %s", dir)
	}
	return files
}

func decodeStream(data []byte) (image.Image, error) {
	src, err := Open(bytes.NewReader(data), nil)
	if err != nil {
		return nil, err
	}
	defer src.Close()
	return pipeline.Collect(src)
}

func samePixels(t *testing.T, name string, want, got image.Image) {
	t.Helper()
	if reflect.TypeOf(want) != reflect.TypeOf(got) {
		t.Fatalf("%s: type %T, want %T", name, got, want)
	}
	if want.Bounds() != got.Bounds() {
		t.Fatalf("%s: bounds %v, want %v", name, got.Bounds(), want.Bounds())
	}
	switch w := want.(type) {
	case *image.Paletted:
		g := got.(*image.Paletted)
		if !bytes.Equal(w.Pix, g.Pix) || !reflect.DeepEqual(w.Palette, g.Palette) {
			t.Fatalf("%s: paletted pixels or palette differ", name)
		}
		return
	}
	b := want.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if want.At(x, y) != got.At(x, y) {
				t.Fatalf("%s: pixel (%d,%d) = %v, want %v", name, x, y, got.At(x, y), want.At(x, y))
			}
		}
	}
}

func isInterlaced(data []byte) bool { return len(data) > 28 && data[28] == 1 }

// TestStdlibTestdata decodes every PNG in Go's own test corpus (PngSuite plus
// the regression files) and requires the same pixels, or an error whenever
// png.Decode errors.
func TestStdlibTestdata(t *testing.T) {
	n := 0
	for _, f := range stdTestdata(t) {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		want, werr := png.Decode(bytes.NewReader(data))
		got, gerr := decodeStream(data)
		name := filepath.Base(f)
		if werr != nil {
			if gerr == nil {
				t.Errorf("%s: stdlib error %v, stream decoded fine", name, werr)
			}
			continue
		}
		if gerr != nil {
			t.Errorf("%s: stream error %v", name, gerr)
			continue
		}
		samePixels(t, name, want, got)
		n++
	}
	t.Logf("%d files matched", n)
}

func randomImages(w, h int) map[string]image.Image {
	r := rand.New(rand.NewSource(1))
	rect := image.Rect(0, 0, w, h)
	fill := func(pix []byte, alpha bool, bpp int) {
		for i := range pix {
			pix[i] = byte(r.Intn(256))
		}
		_ = alpha
		_ = bpp
	}
	gray := image.NewGray(rect)
	fill(gray.Pix, false, 1)
	gray16 := image.NewGray16(rect)
	fill(gray16.Pix, false, 2)
	rgbaOpaque := image.NewRGBA(rect)
	fill(rgbaOpaque.Pix, false, 4)
	for i := 3; i < len(rgbaOpaque.Pix); i += 4 {
		rgbaOpaque.Pix[i] = 0xff
	}
	nrgba := image.NewNRGBA(rect)
	fill(nrgba.Pix, true, 4)
	nrgba64 := image.NewNRGBA64(rect)
	fill(nrgba64.Pix, true, 8)
	rgba64 := image.NewRGBA64(rect)
	for i := range rgba64.Pix {
		rgba64.Pix[i] = byte(r.Intn(256))
	}
	for i := 6; i < len(rgba64.Pix); i += 8 {
		rgba64.Pix[i], rgba64.Pix[i+1] = 0xff, 0xff
	}
	pal := make(color.Palette, 200)
	for i := range pal {
		pal[i] = color.NRGBA{uint8(i), uint8(255 - i), uint8(i * 3), uint8(128 + i/2)}
	}
	paletted := image.NewPaletted(rect, pal)
	for i := range paletted.Pix {
		paletted.Pix[i] = uint8(r.Intn(len(pal)))
	}
	pal4 := image.NewPaletted(rect, pal[:16])
	for i := range pal4.Pix {
		pal4.Pix[i] = uint8(r.Intn(16))
	}
	// A smooth image so filters other than None get chosen.
	smooth := image.NewNRGBA(rect)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			smooth.SetNRGBA(x, y, color.NRGBA{uint8(x), uint8(y), uint8(x + y), uint8(255 - x/3)})
		}
	}
	return map[string]image.Image{
		"gray": gray, "gray16": gray16, "rgba-opaque": rgbaOpaque, "nrgba": nrgba,
		"nrgba64": nrgba64, "rgba64-opaque": rgba64, "paletted": paletted,
		"paletted16": pal4, "smooth": smooth,
	}
}

// TestManyStrips uses images tall enough to span many strips, at every
// compression level (which changes the per-row filters chosen).
func TestManyStrips(t *testing.T) {
	levels := []png.CompressionLevel{png.DefaultCompression, png.NoCompression, png.BestSpeed, png.BestCompression}
	for name, m := range randomImages(397, 611) {
		for _, lv := range levels {
			var buf bytes.Buffer
			if err := (&png.Encoder{CompressionLevel: lv}).Encode(&buf, m); err != nil {
				t.Fatal(err)
			}
			want, err := png.Decode(bytes.NewReader(buf.Bytes()))
			if err != nil {
				t.Fatal(err)
			}
			got, err := decodeStream(buf.Bytes())
			if err != nil {
				t.Fatalf("%s/%d: %v", name, lv, err)
			}
			samePixels(t, name, want, got)
		}
	}
}

// TestTruncatedAndCorrupt requires an error wherever png.Decode errors.
func TestTruncatedAndCorrupt(t *testing.T) {
	m := randomImages(120, 300)["smooth"]
	var buf bytes.Buffer
	png.Encode(&buf, m)
	data := buf.Bytes()
	for cut := 8; cut < len(data); cut += 97 {
		_, werr := png.Decode(bytes.NewReader(data[:cut]))
		_, gerr := decodeStream(data[:cut])
		if (werr == nil) != (gerr == nil) {
			t.Fatalf("cut %d: stdlib err %v, stream err %v", cut, werr, gerr)
		}
	}
	for i := 40; i < len(data); i += 131 {
		bad := append([]byte(nil), data...)
		bad[i] ^= 0x5a
		_, werr := png.Decode(bytes.NewReader(bad))
		_, gerr := decodeStream(bad)
		if (werr == nil) != (gerr == nil) {
			t.Fatalf("flip %d: stdlib err %v, stream err %v", i, werr, gerr)
		}
	}
}

// adam7Encode writes m as an interlaced PNG. The standard encoder cannot, so
// the seven reduced images are filtered (type 0) and deflated by hand.
func adam7Encode(t *testing.T, m image.Image) []byte {
	t.Helper()
	var plain bytes.Buffer
	if err := png.Encode(&plain, m); err != nil {
		t.Fatal(err)
	}
	// Take the header chunks from the standard encoding, flip the interlace
	// byte, and rebuild IDAT from the decoded rows.
	data := plain.Bytes()
	ihdr := append([]byte(nil), data[8:8+25]...)
	ihdr[8+12] = 1
	binary.BigEndian.PutUint32(ihdr[21:], crc32.ChecksumIEEE(ihdr[4:21]))
	depth, ct := int(ihdr[8+8]), int(ihdr[8+9])
	ch := map[int]int{0: 1, 2: 3, 3: 1, 4: 2, 6: 4}[ct]
	bitsPP := depth * ch
	// Raw (unfiltered) full rows from the plain file.
	var idat, mid []byte
	for off := 8; off < len(data); {
		n := int(binary.BigEndian.Uint32(data[off:]))
		typ := string(data[off+4 : off+8])
		switch typ {
		case "IDAT":
			idat = append(idat, data[off+8:off+8+n]...)
		case "IHDR", "IEND":
		default:
			mid = append(mid, data[off:off+12+n]...)
		}
		off += 12 + n
	}
	zr, err := zlib.NewReader(bytes.NewReader(idat))
	if err != nil {
		t.Fatal(err)
	}
	b := m.Bounds()
	w, h := b.Dx(), b.Dy()
	rowBytes := (bitsPP*w + 7) / 8
	bppBytes := (bitsPP + 7) / 8
	rows := make([][]byte, h)
	prev := make([]byte, rowBytes)
	for y := range rows {
		line := make([]byte, 1+rowBytes)
		if _, err := io.ReadFull(zr, line); err != nil {
			t.Fatal(err)
		}
		cur := line[1:]
		for i := range cur { // undo the filter
			var a, c byte
			if i >= bppBytes {
				a, c = cur[i-bppBytes], prev[i-bppBytes]
			}
			up := prev[i]
			switch line[0] {
			case 1:
				cur[i] += a
			case 2:
				cur[i] += up
			case 3:
				cur[i] += byte((int(a) + int(up)) / 2)
			case 4:
				p := int(a) + int(up) - int(c)
				pa, pb, pc := abs(p-int(a)), abs(p-int(up)), abs(p-int(c))
				switch {
				case pa <= pb && pa <= pc:
					cur[i] += a
				case pb <= pc:
					cur[i] += up
				default:
					cur[i] += c
				}
			}
		}
		rows[y], prev = cur, cur
	}
	getPx := func(row []byte, x int) uint64 { // pixel x as bitsPP bits
		var v uint64
		for i := 0; i < bitsPP; i++ {
			bit := x*bitsPP + i
			v = v<<1 | uint64(row[bit/8]>>(7-bit%8)&1)
		}
		return v
	}
	var raw bytes.Buffer
	for _, p := range interlacing {
		pw := (w - p.xOffset + p.xFactor - 1) / p.xFactor
		ph := (h - p.yOffset + p.yFactor - 1) / p.yFactor
		if pw == 0 || ph == 0 {
			continue
		}
		for py := 0; py < ph; py++ {
			line := make([]byte, 1+(bitsPP*pw+7)/8)
			for px := 0; px < pw; px++ {
				v := getPx(rows[py*p.yFactor+p.yOffset], px*p.xFactor+p.xOffset)
				for i := 0; i < bitsPP; i++ {
					bit := px*bitsPP + i
					line[1+bit/8] |= byte(v>>(bitsPP-1-i)&1) << (7 - bit%8)
				}
			}
			raw.Write(line)
		}
	}
	var z bytes.Buffer
	zw := zlib.NewWriter(&z)
	zw.Write(raw.Bytes())
	zw.Close()
	out := append([]byte(nil), data[:8]...)
	out = append(out, ihdr...)
	out = append(out, mid...)
	chunk := func(typ string, body []byte) {
		var hdr [8]byte
		binary.BigEndian.PutUint32(hdr[:], uint32(len(body)))
		copy(hdr[4:], typ)
		out = append(out, hdr[:]...)
		out = append(out, body...)
		c := crc32.NewIEEE()
		c.Write(hdr[4:])
		c.Write(body)
		out = binary.BigEndian.AppendUint32(out, c.Sum32())
	}
	// Several IDAT chunks, as real files have.
	zb := z.Bytes()
	for len(zb) > 0 {
		n := min(len(zb), 8000)
		chunk("IDAT", zb[:n])
		zb = zb[n:]
	}
	chunk("IEND", nil)
	return out
}

// TestInterlaced decodes interlaced images of every pixel type, in memory
// and with the spill forced to a temporary file.
func TestInterlaced(t *testing.T) {
	for _, sz := range [][2]int{{397, 611}, {1, 1}, {3, 2}, {5, 9}, {1500, 700}} {
		for name, m := range randomImages(sz[0], sz[1]) {
			if sz[0] == 1500 && name != "nrgba" && name != "paletted16" && name != "gray16" {
				continue // the hand-made encoder is slow; three types cover the disk path
			}
			data := adam7Encode(t, m)
			want, err := png.Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatalf("%s %v: reference: %v", name, sz, err)
			}
			for _, limit := range []int64{0, 2 << 20} {
				dir := t.TempDir()
				env := pipeline.NewEnv(limit, dir)
				src, err := Open(bytes.NewReader(data), env)
				if err != nil {
					t.Fatalf("%s %v: %v", name, sz, err)
				}
				got, err := pipeline.Collect(src)
				if err != nil {
					t.Fatalf("%s %v limit %d: %v", name, sz, limit, err)
				}
				onDisk := src.(*stream).ilace.spill.OnDisk()
				src.Close()
				samePixels(t, name, want, got)
				if limit != 0 && sz[0] == 1500 && !onDisk {
					t.Errorf("%s: a %d-byte limit did not send the passes to disk", name, limit)
				}
				if left, _ := filepath.Glob(filepath.Join(dir, "*")); len(left) != 0 {
					t.Errorf("%s: temp files left behind: %v", name, left)
				}
				if env.Budget.Used() != 0 {
					t.Errorf("%s: %d bytes still reserved after Close", name, env.Budget.Used())
				}
			}
			if sz[0] == 397 {
				for cut := 8; cut < len(data); cut += 997 {
					_, werr := png.Decode(bytes.NewReader(data[:cut]))
					_, gerr := decodeStream(data[:cut])
					if (werr == nil) != (gerr == nil) {
						t.Fatalf("%s cut %d: stdlib err %v, stream err %v", name, cut, werr, gerr)
					}
				}
				for i := 40; i < len(data); i += 1777 {
					bad := append([]byte(nil), data...)
					bad[i] ^= 0x5a
					_, werr := png.Decode(bytes.NewReader(bad))
					_, gerr := decodeStream(bad)
					if (werr == nil) != (gerr == nil) {
						t.Fatalf("%s flip %d: stdlib err %v, stream err %v", name, i, werr, gerr)
					}
				}
			}
		}
	}
}
