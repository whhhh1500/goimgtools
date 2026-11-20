package pngstream

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"testing"

	"github.com/whhhh1500/goimgtools/internal/deflate"
	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/internal/zopfli"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// idatData is the zlib stream of a PNG's IDAT chunks.
func idatData(t *testing.T, file []byte) []byte {
	t.Helper()
	var z []byte
	for p := 8; p+8 <= len(file); {
		n := int(binary.BigEndian.Uint32(file[p:]))
		if string(file[p+4:p+8]) == "IDAT" {
			z = append(z, file[p+8:p+8+n]...)
		}
		p += 12 + n
	}
	return z
}

// TestLevel10: at level 10 the image data is the filtered rows in blocks,
// each compressed after the 32KB before it by a tenCompressor (on the
// helpers, when there are any), joined bit by bit, a stored block's data
// on a byte; the pixels come back; the file is the same with helpers and
// with the memory limit only just enough, and the budget is given back.
func TestLevel10(t *testing.T) {
	// One run of the optimal parse, and blocks of 50000 bytes: the joining
	// of the blocks does not depend on either, which take the time.
	defer func(o zopfli.Options, b int) { zopfliOptions, zopfliBlock = o, b }(zopfliOptions, zopfliBlock)
	zopfliOptions.NumIterations = 1
	zopfliBlock = 50000
	// More than two blocks of data: a gradient with some noise, flat
	// stretches, a translucent band (so that the rows are RGBA) and noise,
	// which is stored, in the second block. The first block is to end in a
	// byte, for the stored block to come after bits pending: of the flat
	// colours tried, the first with which it does.
	w := 400
	h := 2*zopfliBlock/(1+4*w) + 5
	for blue := byte(221); ; blue++ {
		if blue == 241 {
			t.Fatal("no stored block after bits pending")
		}
		if level10Case(t, w, h, blue) {
			break
		}
	}
}

// level10Case is TestLevel10 with the flat colour's blue; it tells whether
// a stored block came after bits pending (and then checks the file with
// helpers and the memory limit too).
func level10Case(t *testing.T, w, h int, blue byte) bool {
	m := image.NewNRGBA(image.Rect(0, 0, w, h))
	seed := uint32(7)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			seed = seed*1664525 + 1013904223
			p := m.Pix[y*m.Stride+4*x:]
			switch {
			case y >= 33 && y < 62:
				binary.LittleEndian.PutUint32(p, seed)
			case y < h/3 && x < w/2:
				copy(p, []byte{200, 210, blue, 255})
			case y > h/2 && y < h/2+20:
				copy(p, []byte{byte(x), byte(y), 90, 128})
			default:
				copy(p, []byte{byte(x/8 + y/4 + int(seed>>29)), byte(y/2 + int(seed>>30)), byte(x / 16), 255})
			}
		}
	}
	encode := func(limit int64, helpers int) []byte {
		env := pipeline.NewEnv(limit, t.TempDir())
		env.SetHelpers(helpers)
		var buf bytes.Buffer
		if err := Encode(&buf, testutil.ChoppedEnv(m, 64, env), &Options{Level: 10}); err != nil {
			t.Fatalf("blue %d, limit %d, %d helpers: %v", blue, limit, helpers, err)
		}
		if u := env.Budget.Used(); u != 0 {
			t.Errorf("blue %d, limit %d, %d helpers: %d bytes still reserved", blue, limit, helpers, u)
		}
		return buf.Bytes()
	}
	want := encode(0, 0)
	got, err := png.Decode(bytes.NewReader(want))
	if err != nil {
		t.Fatalf("blue %d: %v", blue, err)
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if g, e := color.NRGBAModel.Convert(got.At(x, y)), m.At(x, y); g != e {
				t.Fatalf("blue %d: pixel (%d,%d) %v, want %v", blue, x, y, g, e)
			}
		}
	}

	z := idatData(t, want)
	r, err := zlib.NewReader(bytes.NewReader(z))
	if err != nil {
		t.Fatal(err)
	}
	rows, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) <= 2*zopfliBlock {
		t.Fatalf("%d bytes of data: not more than two blocks", len(rows))
	}
	// The blocks compressed one after the other, and their bits joined.
	tc := newTenCompressor(zopfliBlock, tenRuns(int64(len(rows))), nil, 1)
	var want10 []byte
	var pend byte
	pendN, shifted := 0, 0
	for s := 0; s < len(rows); s += zopfliBlock {
		e := min(len(rows), s+zopfliBlock)
		from := max(0, s-zWindow)
		blk, bits, stored := tc.CompressBits(nil, rows[from:e], s-from, e == len(rows))
		if stored >= 0 && pendN != 0 {
			shifted++
		}
		n := 8*(len(blk)-1) + bits
		for i := 0; i < n; i++ {
			if stored >= 0 && i == stored+3 {
				// The stored block's data starts on a byte.
				if pendN != 0 {
					want10, pend, pendN = append(want10, pend), 0, 0
				}
				if i = (i + 7) &^ 7; i == n {
					break
				}
			}
			pend |= (blk[i/8] >> (i % 8) & 1) << pendN
			if pendN++; pendN == 8 {
				want10, pend, pendN = append(want10, pend), 0, 0
			}
		}
	}
	if pendN != 0 {
		want10 = append(want10, pend)
	}
	if !bytes.Equal(z[2:len(z)-4], want10) {
		t.Errorf("blue %d: the data is not the blocks compressed one after the other", blue)
	}
	if shifted == 0 {
		return false
	}
	for _, c := range []struct {
		limit   int64
		helpers int
	}{{0, 2}, {nearBytes(10, w) + 4<<20, 3}} {
		if got := encode(c.limit, c.helpers); !bytes.Equal(got, want) {
			t.Errorf("blue %d, limit %d, %d helpers: another file", blue, c.limit, c.helpers)
		}
	}
	return true
}

// TestIDATChunks: from level 8 the zlib stream is in IDAT chunks of
// idatBytes, the last shorter (a stream of up to that in one), the same
// with helpers; the budget is given back.
func TestIDATChunks(t *testing.T) {
	noise := image.NewNRGBA(image.Rect(0, 0, 800, 400)) // more than idatBytes compressed
	seed := uint32(1)
	for i := range noise.Pix {
		seed = seed*1664525 + 1013904223
		noise.Pix[i] = byte(seed >> 24)
	}
	for _, c := range []struct {
		name   string
		m      image.Image
		chunks int
	}{{"natural", testutil.Natural(160, 120, 3), 1}, {"noise", noise, 2}} {
		var want []byte
		for _, helpers := range []int{0, 3} {
			env := pipeline.NewEnv(0, t.TempDir())
			env.SetHelpers(helpers)
			var buf bytes.Buffer
			if err := Encode(&buf, testutil.ChoppedEnv(c.m, 64, env), &Options{Level: 8}); err != nil {
				t.Fatalf("%s, %d helpers: %v", c.name, helpers, err)
			}
			if u := env.Budget.Used(); u != 0 {
				t.Errorf("%s, %d helpers: %d bytes still reserved", c.name, helpers, u)
			}
			if helpers > 0 {
				if !bytes.Equal(buf.Bytes(), want) {
					t.Errorf("%s, %d helpers: another file", c.name, helpers)
				}
				continue
			}
			want = buf.Bytes()
			var sizes []int
			file := buf.Bytes()
			for p := 8; p+8 <= len(file); {
				n := int(binary.BigEndian.Uint32(file[p:]))
				if string(file[p+4:p+8]) == "IDAT" {
					sizes = append(sizes, n)
				}
				p += 12 + n
			}
			if len(sizes) != c.chunks {
				t.Fatalf("%s: IDAT chunks of %v bytes, want %d", c.name, sizes, c.chunks)
			}
			for _, n := range sizes[:len(sizes)-1] {
				if n != idatBytes {
					t.Errorf("%s: IDAT chunks of %v bytes, want %d but the last", c.name, sizes, idatBytes)
				}
			}
			if _, err := png.Decode(bytes.NewReader(file)); err != nil {
				t.Errorf("%s: %v", c.name, err)
			}
		}
	}
}

// TestLevel10Small: a small image at level 10 has the pixels it has at
// level 9, for every pixel type, and a photograph-like one is not larger.
// (On some of the synthetic test images level 10 is the larger, by up to
// 0.4%: its choices of filter are not better on them.)
func TestLevel10Small(t *testing.T) {
	imgs := testutil.Images(97, 41)
	imgs["natural"] = testutil.Natural(160, 120, 3)
	for name, m := range imgs {
		var files [2][]byte
		var pics [2]image.Image
		for i, level := range []int{9, 10} {
			var buf bytes.Buffer
			if err := Encode(&buf, testutil.Chopped(m, 7), &Options{Level: level}); err != nil {
				t.Fatalf("%s level %d: %v", name, level, err)
			}
			pic, err := decodeStream(buf.Bytes())
			if err != nil {
				t.Fatalf("%s level %d: %v", name, level, err)
			}
			files[i], pics[i] = buf.Bytes(), pic
		}
		if p0, ok := pics[0].(*image.Paletted); ok {
			// Level 10 writes the palette in another order.
			p1 := pics[1].(*image.Paletted)
			for i, v := range p0.Pix {
				if p0.Palette[v] != p1.Palette[p1.Pix[i]] {
					t.Fatalf("%s level 10: pixel %d differs", name, i)
				}
			}
		} else {
			samePixels(t, fmt.Sprintf("%s level 10", name), pics[0], pics[1])
		}
		if name == "natural" && len(files[1]) > len(files[0]) {
			t.Errorf("%s: %d bytes at level 10, %d at 9", name, len(files[1]), len(files[0]))
		}
	}
}

// TestTenCompressor: each block is the smaller of Zopfli's and level 9's
// (Zopfli's on a tie), and either joins on to the bits before.
func TestTenCompressor(t *testing.T) {
	defer func(o zopfli.Options) { zopfliOptions = o }(zopfliOptions)
	zopfliOptions.NumIterations = 2
	nat := testutil.Natural(300, 60, 5)
	rows := nat.Pix // noise-like rows, on which level 9's compressor wins
	c := newTenCompressor(len(rows), zopfliOptions.NumIterations, nil, 1)
	for _, data := range [][]byte{rows, bytes.Repeat([]byte("abcdefgh"), 3000)} {
		for _, final := range []bool{true, false} {
			got, bits, _ := c.CompressBits(nil, data, 0, final)
			z, zbits, _ := zopfli.New(zopfliOptions, len(data)).CompressBits(nil, data, 0, final)
			d, dbits, _ := deflate.New(nearLevels[9]).CompressBits(nil, data, 0, final)
			want, wbits := z, zbits
			if 8*len(d)+dbits < 8*len(z)+zbits {
				want, wbits = d, dbits
			}
			if !bytes.Equal(got, want) || bits != wbits {
				t.Errorf("%d bytes, final %v: not the smaller of Zopfli's (%d) and level 9's (%d)", len(data), final, len(z), len(d))
			}
		}
	}
}

// TestLevel10Palette: at level 10 a palette is written in the order its
// colours first appear, without the ones no pixel uses, and with the bits
// a pixel the colours left need; every pixel keeps its colour.
func TestLevel10Palette(t *testing.T) {
	pal := make(color.Palette, 256)
	for i := range pal {
		pal[i] = color.NRGBA{uint8(i), uint8(255 - i), uint8(i * 7), uint8(255 - i%3)}
	}
	m := image.NewPaletted(image.Rect(0, 0, 37, 23), pal)
	used := []uint8{200, 3, 77, 3, 200, 9, 250} // six colours, first seen in this order
	for i := range m.Pix {
		m.Pix[i] = used[(i*5+i/37)%len(used)]
	}
	var buf bytes.Buffer
	if err := Encode(&buf, testutil.Chopped(m, 5), &Options{Level: 10}); err != nil {
		t.Fatal(err)
	}
	if depth := buf.Bytes()[24]; depth != 4 {
		t.Errorf("bit depth %d, want 4 for 6 colours", depth)
	}
	got, err := decodeStream(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	p := got.(*image.Paletted)
	var order []color.Color
	seen := map[uint8]bool{}
	for _, v := range m.Pix {
		if !seen[v] {
			seen[v] = true
			order = append(order, pal[v])
		}
	}
	if len(p.Palette) != len(order) {
		t.Fatalf("%d colours in the palette, want %d", len(p.Palette), len(order))
	}
	for i := range order {
		if p.Palette[i] != order[i] {
			t.Errorf("palette entry %d is %v, want %v", i, p.Palette[i], order[i])
		}
	}
	for i, v := range m.Pix {
		if p.Palette[p.Pix[i]] != pal[v] {
			t.Fatalf("pixel %d: %v, want %v", i, p.Palette[p.Pix[i]], pal[v])
		}
	}
}
