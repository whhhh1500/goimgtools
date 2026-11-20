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
// helpers, when there are any), joined bit by bit; the pixels come back;
// the file is the same with helpers and with the memory limit only just
// enough, and the budget is given back.
func TestLevel10(t *testing.T) {
	// One run of the optimal parse, and blocks of 50000 bytes: the joining
	// of the blocks does not depend on either, which take the time.
	defer func(o zopfli.Options, b int) { zopfliOptions, zopfliBlock = o, b }(zopfliOptions, zopfliBlock)
	zopfliOptions.NumIterations = 1
	zopfliBlock = 50000
	// More than two blocks of data: a gradient with some noise, flat
	// stretches and a translucent band (so that the rows are RGBA).
	w := 400
	h := 2*zopfliBlock/(1+4*w) + 5
	m := image.NewNRGBA(image.Rect(0, 0, w, h))
	seed := uint32(7)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			seed = seed*1664525 + 1013904223
			p := m.Pix[y*m.Stride+4*x:]
			switch {
			case y < h/3 && x < w/2:
				copy(p, []byte{200, 210, 220, 255})
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
			t.Fatalf("limit %d, %d helpers: %v", limit, helpers, err)
		}
		if u := env.Budget.Used(); u != 0 {
			t.Errorf("limit %d, %d helpers: %d bytes still reserved", limit, helpers, u)
		}
		return buf.Bytes()
	}
	want := encode(0, 0)
	got, err := png.Decode(bytes.NewReader(want))
	if err != nil {
		t.Fatal(err)
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if g, e := color.NRGBAModel.Convert(got.At(x, y)), m.At(x, y); g != e {
				t.Fatalf("pixel (%d,%d) %v, want %v", x, y, g, e)
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
	tc := newTenCompressor(zopfliBlock)
	var want10 []byte
	var pend byte
	pendN := 0
	for s := 0; s < len(rows); s += zopfliBlock {
		e := min(len(rows), s+zopfliBlock)
		from := max(0, s-zWindow)
		blk, bits := tc.CompressBits(nil, rows[from:e], s-from, e == len(rows))
		for i := 0; i < 8*(len(blk)-1)+bits; i++ {
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
		t.Error("the data is not the blocks compressed one after the other")
	}

	for _, c := range []struct {
		limit   int64
		helpers int
	}{{0, 2}, {nearBytes(10, w) + 4<<20, 3}} {
		if got := encode(c.limit, c.helpers); !bytes.Equal(got, want) {
			t.Errorf("limit %d, %d helpers: another file", c.limit, c.helpers)
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
		samePixels(t, fmt.Sprintf("%s level 10", name), pics[0], pics[1])
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
	c := newTenCompressor(len(rows))
	for _, data := range [][]byte{rows, bytes.Repeat([]byte("abcdefgh"), 3000)} {
		for _, final := range []bool{true, false} {
			got, bits := c.CompressBits(nil, data, 0, final)
			z, zbits := zopfli.New(zopfliOptions, len(data)).CompressBits(nil, data, 0, final)
			d, dbits := deflate.New(nearLevels[9]).CompressBits(nil, data, 0, final)
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
