package tiffstream

import (
	"encoding/binary"
	"image"
	"math/rand"
	"os"
	"testing"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// TestZstd: blocks compressed with zstd (compression 50000, as libtiff
// numbers it) give the image the same blocks give uncompressed, in every
// mode, byte order and layout, with the predictor; and a file whose strips
// libzstd compressed (testdata/zstd.tif, see internal/zstd/testdata) is
// the picture it was made from.
func TestZstd(t *testing.T) {
	modes := append([]pixelMode{
		{"gray4", []uint32{4}, pBlackIsZero, 0},
		{"graya8", []uint32{8, 8}, pBlackIsZero, 2},
		{"cmyk8", []uint32{8, 8, 8, 8}, pCMYK, 0},
	}, pixelModes...)
	n := 0
	for _, m := range modes {
		for _, bo := range bothOrders {
			for _, l := range []layout{{}, {rps: 3}, {tw: 16, th: 16}, {rps: 5}} {
				s := spec{w: 52, h: 37, bps: m.bps, photo: m.photo, extra: m.extra, bo: bo,
					rps: l.rps, tw: l.tw, th: l.th, pred: l.rps == 5 && m.bps[0] >= 8}
				// The same samples twice: the random source starts alike.
				want, err := decodeStream(build(s, rand.New(rand.NewSource(31))), nil)
				if err != nil {
					t.Fatalf("%s: %v", s, err)
				}
				s.comp = cZstd
				got, err := decodeStream(build(s, rand.New(rand.NewSource(31))), nil)
				if err != nil {
					t.Errorf("%s: %v", s, err)
				} else if err := sameImage(want, got); err != nil {
					t.Errorf("%s: %v", s, err)
				}
				n++
			}
		}
	}
	t.Logf("%d files", n)

	// Blocks of more than one zstd block, and of more than the window a
	// reader is given: a strip of 300,000 bytes, three stored blocks.
	big := spec{w: 1000, h: 100, bps: []uint32{8, 8, 8}, photo: pRGB, bo: binary.LittleEndian}
	want, err := decodeStream(build(big, rand.New(rand.NewSource(37))), nil)
	if err != nil {
		t.Fatal(err)
	}
	big.comp = cZstd
	got, err := decodeStream(build(big, rand.New(rand.NewSource(37))), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := same(want, got); err != nil {
		t.Errorf("%s: %v", big, err)
	}

	data, err := os.ReadFile("testdata/zstd.tif")
	if err != nil {
		t.Fatal(err)
	}
	pic := image.NewRGBA(image.Rect(0, 0, 96, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 96; x++ {
			p := pic.Pix[pic.PixOffset(x, y):]
			p[0], p[1], p[2], p[3] = uint8(x*3+y*2), uint8((x/8)*29+(y/8)*17+(x*y)%5), uint8((x&15)*16+(y*y/9)%7), 0xff
		}
	}
	for _, mem := range []int64{0, 1 << 20} {
		var env *pipeline.Env
		if mem != 0 {
			env = pipeline.NewEnv(mem, t.TempDir())
		}
		got, err := decodeStream(data, env)
		if err != nil {
			t.Fatalf("testdata/zstd.tif (limit %d): %v", mem, err)
		}
		if err := same(pic, got); err != nil {
			t.Errorf("testdata/zstd.tif (limit %d): %v", mem, err)
		}
	}

	// Damaged: an error, or an image, never a panic.
	r := rand.New(rand.NewSource(41))
	for i := 0; i < 3000; i++ {
		bad := append([]byte(nil), data...)
		for k := 1 + r.Intn(3); k > 0; k-- {
			bad[8+r.Intn(len(bad)-8)] ^= byte(1 << r.Intn(8))
		}
		decodeStream(bad, nil)
	}
	for n := 8; n < len(data)-200; n += 3 {
		// (The directory is at the end: cut before it, nothing reads.)
		if _, err := decodeStream(data[:n], nil); err == nil {
			t.Errorf("cut at %d of %d: read", n, len(data))
		}
	}
}
