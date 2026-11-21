package icostream

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"io"
	"testing"

	"github.com/whhhh1500/goimgtools/internal/pngstream"
	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// dib makes the bitmap of an icon entry: colours of bits bits (palette
// pal for 1, 4 and 8), bottom row first, then the mask (1: transparent).
func dib(w, h, bits int, pal []color.NRGBA, px func(x, y int) (idx int, c color.NRGBA), mask func(x, y int) bool) []byte {
	le := binary.LittleEndian
	hdr := make([]byte, 40)
	le.PutUint32(hdr[0:], 40)
	le.PutUint32(hdr[4:], uint32(w))
	le.PutUint32(hdr[8:], uint32(2*h))
	le.PutUint16(hdr[12:], 1)
	le.PutUint16(hdr[14:], uint16(bits))
	le.PutUint32(hdr[32:], uint32(len(pal)))
	out := hdr
	for _, c := range pal {
		out = append(out, c.B, c.G, c.R, 0)
	}
	row := (w*bits + 31) / 32 * 4
	for y := h - 1; y >= 0; y-- {
		r := make([]byte, row)
		for x := 0; x < w; x++ {
			i, c := px(x, y)
			switch bits {
			case 32:
				copy(r[4*x:], []byte{c.B, c.G, c.R, c.A})
			case 24:
				copy(r[3*x:], []byte{c.B, c.G, c.R})
			default:
				r[x*bits/8] |= byte(i << (8 - bits - (x*bits)%8))
			}
		}
		out = append(out, r...)
	}
	mrow := (w + 31) / 32 * 4
	for y := h - 1; y >= 0; y-- {
		r := make([]byte, mrow)
		for x := 0; x < w; x++ {
			if mask(x, y) {
				r[x/8] |= 0x80 >> (x % 8)
			}
		}
		out = append(out, r...)
	}
	return out
}

// file makes an icon file of the entries (their directory sizes w, h, bits).
func file(entries ...struct {
	w, h, bits int
	data       []byte
}) []byte {
	le := binary.LittleEndian
	out := make([]byte, 6+16*len(entries))
	le.PutUint16(out[2:], 1)
	le.PutUint16(out[4:], uint16(len(entries)))
	off := len(out)
	for i, e := range entries {
		d := out[6+16*i:]
		d[0], d[1] = byte(e.w), byte(e.h)
		le.PutUint16(d[4:], 1)
		le.PutUint16(d[6:], uint16(e.bits))
		le.PutUint32(d[8:], uint32(len(e.data)))
		le.PutUint32(d[12:], uint32(off))
		off += len(e.data)
	}
	for _, e := range entries {
		out = append(out, e.data...)
	}
	return out
}

func decode(t *testing.T, data []byte) *image.NRGBA {
	t.Helper()
	env := pipeline.NewEnv(0, t.TempDir())
	src, err := Open(bytes.NewReader(data), int64(len(data)), env)
	if err != nil {
		t.Fatal(err)
	}
	m, err := pipeline.Collect(src)
	src.Close()
	if err != nil {
		t.Fatal(err)
	}
	if env.Budget.Used() != 0 {
		t.Fatalf("%d bytes still reserved", env.Budget.Used())
	}
	n := image.NewNRGBA(m.Bounds())
	for y := 0; y < m.Bounds().Dy(); y++ {
		for x := 0; x < m.Bounds().Dx(); x++ {
			n.Set(x, y, m.At(m.Bounds().Min.X+x, m.Bounds().Min.Y+y))
		}
	}
	return n
}

// TestBitmaps: every depth of bitmap, the mask for those without alpha and
// for 32-bit ones whose alpha is all 0, alpha otherwise.
func TestBitmaps(t *testing.T) {
	const w, h = 37, 21
	pal := []color.NRGBA{{10, 20, 30, 255}, {200, 100, 50, 255}, {0, 255, 0, 255}, {255, 255, 255, 255}}
	pal16 := append(append([]color.NRGBA(nil), pal...), make([]color.NRGBA, 12)...)
	for i := 4; i < 16; i++ {
		pal16[i] = color.NRGBA{uint8(i * 15), uint8(255 - i*15), uint8(i * 7), 255}
	}
	mask := func(x, y int) bool { return (x+2*y)%5 == 0 }
	for _, c := range []struct {
		bits  int
		pal   []color.NRGBA
		alpha bool
	}{{1, pal[:2], false}, {4, pal16, false}, {8, pal16, false}, {24, nil, false}, {32, nil, false}, {32, nil, true}} {
		want := image.NewNRGBA(image.Rect(0, 0, w, h))
		px := func(x, y int) (int, color.NRGBA) {
			if c.pal != nil {
				i := (x*3 + y) % len(c.pal)
				return i, c.pal[i]
			}
			col := color.NRGBA{uint8(x * 6), uint8(y * 11), uint8(x ^ y), 0}
			if c.alpha {
				col.A = uint8(x * 7)
			}
			return 0, col
		}
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				_, col := px(x, y)
				switch {
				case c.alpha:
					if col.A == 0 {
						col = color.NRGBA{}
					}
				case mask(x, y):
					col = color.NRGBA{}
				default:
					col.A = 255
				}
				want.SetNRGBA(x, y, col)
			}
		}
		data := file(struct {
			w, h, bits int
			data       []byte
		}{w, h, c.bits, dib(w, h, c.bits, c.pal, px, mask)})
		got := decode(t, data)
		for i := 0; i < len(want.Pix); i += 4 {
			if want.Pix[i+3] == 0 && got.Pix[i+3] == 0 {
				continue
			}
			if !bytes.Equal(want.Pix[i:i+4], got.Pix[i:i+4]) {
				t.Fatalf("%d bits alpha %v: pixel %d is %v, want %v", c.bits, c.alpha, i/4, got.Pix[i:i+4], want.Pix[i:i+4])
			}
		}
		if cw, ch, err := Config(bytes.NewReader(data), int64(len(data))); err != nil || cw != w || ch != h {
			t.Errorf("%d bits: Config %dx%d %v", c.bits, cw, ch, err)
		}
	}
}

// TestRoundTrip: what Encode writes, Open reads back, the largest image;
// of two the same size, the deeper one.
func TestRoundTrip(t *testing.T) {
	var imgs []Image
	var biggest *image.NRGBA
	for _, s := range []int{16, 48, 256, 32} {
		m := testutil.Images(s, s)["nrgba"]
		var buf bytes.Buffer
		if err := pngstream.Encode(&buf, testutil.Chopped(m, 7), nil); err != nil {
			t.Fatal(err)
		}
		imgs = append(imgs, Image{W: s, H: s, PNG: buf.Bytes()})
		if s == 256 {
			biggest = m.(*image.NRGBA)
		}
	}
	var out bytes.Buffer
	if err := Encode(&out, imgs); err != nil {
		t.Fatal(err)
	}
	got := decode(t, out.Bytes())
	if got.Rect.Dx() != 256 {
		t.Fatalf("decoded %v", got.Rect)
	}
	for i := 0; i < len(got.Pix); i += 4 {
		if biggest.Pix[i+3] != 0 && !bytes.Equal(got.Pix[i:i+4], biggest.Pix[i:i+4]) {
			t.Fatalf("pixel %d: %v, want %v", i/4, got.Pix[i:i+4], biggest.Pix[i:i+4])
		}
	}
	// Two entries of 32x32: 8 bits and 32 bits.
	px := func(x, y int) (int, color.NRGBA) { return x % 2, color.NRGBA{uint8(x), 9, 9, 255} }
	none := func(x, y int) bool { return false }
	data := file(struct {
		w, h, bits int
		data       []byte
	}{32, 32, 8, dib(32, 32, 8, []color.NRGBA{{1, 1, 1, 255}, {2, 2, 2, 255}}, px, none)}, struct {
		w, h, bits int
		data       []byte
	}{32, 32, 32, dib(32, 32, 32, nil, px, none)})
	if m := decode(t, data); m.Pix[4] != 1 || m.Pix[8] != 2 {
		t.Errorf("the 8-bit entry was decoded: %v", m.Pix[:12])
	}
}

// TestDamaged: entries outside the file are passed over; a file with none
// left, a truncated one, an unknown depth and the wrong magic are errors;
// Encode refuses what a directory cannot hold.
func TestDamaged(t *testing.T) {
	px := func(x, y int) (int, color.NRGBA) { return 0, color.NRGBA{1, 2, 3, 255} }
	none := func(x, y int) bool { return false }
	good := dib(8, 8, 24, nil, px, none)
	data := file(struct {
		w, h, bits int
		data       []byte
	}{8, 8, 24, good})
	bad := append([]byte(nil), data...)
	binary.LittleEndian.PutUint32(bad[18:], 1<<30) // offset beyond the file
	if _, err := Open(bytes.NewReader(bad), int64(len(bad)), pipeline.NewEnv(0, t.TempDir())); err == nil {
		t.Error("an entry beyond the file: no error")
	}
	for _, d := range [][]byte{data[:30], []byte("\x00\x00\x01\x00\x00\x00"), []byte("not an icon file at all")} {
		if _, err := Open(bytes.NewReader(d), int64(len(d)), pipeline.NewEnv(0, t.TempDir())); err == nil {
			t.Errorf("%q: no error", d[:min(len(d), 12)])
		}
	}
	odd := append([]byte(nil), data...)
	binary.LittleEndian.PutUint16(odd[22+14:], 16) // 16 bits
	if _, err := Open(bytes.NewReader(odd), int64(len(odd)), pipeline.NewEnv(0, t.TempDir())); err == nil {
		t.Error("16-bit bitmap: no error")
	}
	for _, imgs := range [][]Image{nil, {{W: 257, H: 10, PNG: []byte("x")}}, {{W: 0, H: 10}}} {
		if err := Encode(io.Discard, imgs); err == nil {
			t.Errorf("%v: no error", imgs)
		}
	}
	if !Match([]byte("\x00\x00\x02\x00\x01\x00")) || Match([]byte("\x00\x00\x01\x00\x00\x00")) || Match([]byte("\x00\x00\x03\x00\x01\x00")) {
		t.Error("Match")
	}
}

// FuzzOpen: any file is read or refused, without a panic, within the memory
// it reserves.
func FuzzOpen(f *testing.F) {
	px := func(x, y int) (int, color.NRGBA) {
		return (x + y) % 2, color.NRGBA{uint8(x), uint8(y), 7, uint8(x * y)}
	}
	mask := func(x, y int) bool { return x == y }
	for _, bits := range []int{1, 4, 8, 24, 32} {
		var pal []color.NRGBA
		if bits <= 8 {
			pal = []color.NRGBA{{1, 2, 3, 255}, {4, 5, 6, 255}}
		}
		f.Add(file(struct {
			w, h, bits int
			data       []byte
		}{9, 5, bits, dib(9, 5, bits, pal, px, mask)}))
	}
	var buf bytes.Buffer
	pngstream.Encode(&buf, testutil.Chopped(testutil.Images(5, 4)["nrgba"], 2), nil)
	var ico bytes.Buffer
	Encode(&ico, []Image{{W: 5, H: 4, PNG: buf.Bytes()}})
	f.Add(ico.Bytes())
	f.Fuzz(func(t *testing.T, data []byte) {
		env := pipeline.NewEnv(4<<20, t.TempDir())
		src, err := Open(bytes.NewReader(data), int64(len(data)), env)
		if err != nil {
			return
		}
		for {
			if _, err := src.Next(); err != nil {
				break
			}
		}
		src.Close()
		if env.Budget.Used() != 0 {
			t.Fatalf("%d bytes reserved", env.Budget.Used())
		}
	})
}
