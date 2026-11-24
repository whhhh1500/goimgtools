package qoistream

import (
	"bytes"
	"image"
	"io"
	"testing"

	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// FuzzOpen: any file is read or refused, without a panic, within the
// memory it reserves.
func FuzzOpen(f *testing.F) {
	for _, m := range testutil.Images(9, 4) {
		var b bytes.Buffer
		if err := Encode(&b, testutil.Chopped(m, 3)); err == nil {
			f.Add(b.Bytes())
		}
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		env := pipeline.NewEnv(4<<20, "")
		src, err := Open(bytes.NewReader(data), env)
		if err != nil {
			return
		}
		if b := src.Bounds(); int64(b.Dx())*int64(b.Dy()) > 1<<22 {
			src.Close()
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

// longEncode is the encoder of the specification (qoi.h), a chunk appended
// at a time.
func longEncode(m *image.NRGBA, channels int) []byte {
	w, h := m.Rect.Dx(), m.Rect.Dy()
	out := []byte{'q', 'o', 'i', 'f', byte(w >> 24), byte(w >> 16), byte(w >> 8), byte(w), byte(h >> 24), byte(h >> 16), byte(h >> 8), byte(h), byte(channels), 0}
	var index [64][4]uint8
	prev := [4]uint8{0, 0, 0, 255}
	run := 0
	for i := 0; i < w*h; i++ {
		px := [4]uint8(m.Pix[4*i : 4*i+4])
		if channels == 3 {
			px[3] = 255
		}
		if px == prev {
			run++
			if run == 62 || i == w*h-1 {
				out = append(out, opRun|byte(run-1))
				run = 0
			}
			continue
		}
		if run > 0 {
			out = append(out, opRun|byte(run-1))
			run = 0
		}
		k := (int(px[0])*3 + int(px[1])*5 + int(px[2])*7 + int(px[3])*11) % 64
		if index[k] == px {
			out = append(out, opIndex|byte(k))
		} else {
			index[k] = px
			vr, vg, vb := int8(px[0]-prev[0]), int8(px[1]-prev[1]), int8(px[2]-prev[2])
			vgr, vgb := vr-vg, vb-vg
			switch {
			case px[3] != prev[3]:
				out = append(out, opRGBA, px[0], px[1], px[2], px[3])
			case vr > -3 && vr < 2 && vg > -3 && vg < 2 && vb > -3 && vb < 2:
				out = append(out, opDiff|byte(vr+2)<<4|byte(vg+2)<<2|byte(vb+2))
			case vgr > -9 && vgr < 8 && vg > -33 && vg < 32 && vgb > -9 && vgb < 8:
				out = append(out, opLuma|byte(vg+32), byte(vgr+8)<<4|byte(vgb+8))
			default:
				out = append(out, opRGB, px[0], px[1], px[2])
			}
		}
		prev = px
	}
	return append(out, 0, 0, 0, 0, 0, 0, 0, 1)
}

// pictures are a photograph-like picture as it is (every pixel another
// colour), one with flat parts (runs, longer than a chunk holds and across
// the ends of rows, and colours seen before), each opaque and with
// transparency that changes, and one of a single colour.
func pictures() map[string]*image.NRGBA {
	photo := testutil.Natural(160, 120, 11)
	flat := testutil.Natural(160, 120, 12)
	for y := 0; y < 120; y++ {
		for x := 0; x < 160; x++ {
			p := flat.Pix[flat.PixOffset(x, y):][:4]
			switch {
			case y%40 < 9:
				p[0], p[1], p[2] = 30, 60, 90
			case (x/8+y/5)%3 == 0:
				p[0], p[1], p[2] = uint8(x/8*8), uint8(y/5*5), 7
			}
		}
	}
	alpha := func(m *image.NRGBA) *image.NRGBA {
		c := image.NewNRGBA(m.Rect)
		copy(c.Pix, m.Pix)
		for y := 0; y < m.Rect.Dy(); y++ {
			for x := 0; x < m.Rect.Dx(); x++ {
				if (x/13+y/7)%4 != 0 {
					c.Pix[c.PixOffset(x, y)+3] = uint8(x/13*16 + y/7)
				}
			}
		}
		return c
	}
	one := image.NewNRGBA(image.Rect(0, 0, 300, 41))
	for i := 0; i < len(one.Pix); i += 4 {
		one.Pix[i], one.Pix[i+1], one.Pix[i+2], one.Pix[i+3] = 9, 8, 7, 255
	}
	return map[string]*image.NRGBA{"photograph": photo, "flat parts": flat, "photograph with transparency": alpha(photo),
		"flat parts with transparency": alpha(flat), "one colour": one, "one colour, translucent": alpha(one)}
}

// TestEncodeSame: the file is the one of the specification's encoder, to
// the byte, whatever the size of the buffer the chunks are gathered in
// (so that it fills at every kind of chunk) and the strips the picture
// comes in; and it reads back as the picture.
func TestEncodeSame(t *testing.T) {
	defer func(n int) { encodeBuffer = n }(encodeBuffer)
	for name, m := range pictures() {
		// A source that does not say it is opaque is written with four
		// channels, one that does (an image without transparency) with three.
		want := longEncode(m, 4)
		known := want
		if m.Opaque() {
			known = longEncode(m, 3)
		}
		for _, size := range []int{14, 15, 16, 17, 19, 20, 23, 64, 4096, 64 << 10} {
			encodeBuffer = size
			for _, strip := range []int{1, 7, 64} {
				var b bytes.Buffer
				if err := Encode(&b, testutil.Chopped(m, strip)); err != nil {
					t.Fatal(name, err)
				}
				if !bytes.Equal(b.Bytes(), want) {
					t.Fatalf("%s, a buffer of %d, strips of %d: not the file of the specification's encoder (%d bytes, its %d)", name, size, strip, b.Len(), len(want))
				}
			}
			var b bytes.Buffer
			if err := Encode(&b, pipeline.FromImage(m, nil)); err != nil {
				t.Fatal(name, err)
			}
			if !bytes.Equal(b.Bytes(), known) {
				t.Fatalf("%s, a buffer of %d, as an image: not the file of the specification's encoder (%d bytes, its %d)", name, size, b.Len(), len(known))
			}
		}
		src, err := Open(bytes.NewReader(want), nil)
		if err != nil {
			t.Fatal(name, err)
		}
		got, err := pipeline.Collect(src)
		src.Close()
		if err != nil {
			t.Fatal(name, err)
		}
		if g := got.(*image.NRGBA); !bytes.Equal(g.Pix, m.Pix) {
			t.Errorf("%s: read back as another picture", name)
		}
	}
}

// failAfter fails the write that would take it past n bytes.
type failAfter struct{ n int }

func (f *failAfter) Write(p []byte) (int, error) {
	if f.n -= len(p); f.n < 0 {
		return 0, io.ErrShortWrite
	}
	return len(p), nil
}

// TestEncodeWriteError: an error of the writer is Encode's, wherever in
// the file it comes.
func TestEncodeWriteError(t *testing.T) {
	defer func(n int) { encodeBuffer = n }(encodeBuffer)
	encodeBuffer = 64
	m := pictures()["flat parts with transparency"]
	size := len(longEncode(m, 4))
	for _, n := range []int{0, 13, 14, 100, size / 2, size - 9, size - 1} {
		if err := Encode(&failAfter{n}, testutil.Chopped(m, 16)); err != io.ErrShortWrite {
			t.Errorf("a writer that fails after %d bytes of %d: %v", n, size, err)
		}
	}
	if err := Encode(&failAfter{size}, testutil.Chopped(m, 16)); err != nil {
		t.Errorf("a writer that takes all %d bytes: %v", size, err)
	}
	if err := Encode(shortWriter{}, testutil.Chopped(m, 16)); err != io.ErrShortWrite {
		t.Errorf("a writer that takes less than it is given and says nothing: %v", err)
	}
	// A writer that fails once and takes everything after: the error is
	// still Encode's, whichever write it is (it is not left for a later
	// write to report), the one before the end marker too.
	for _, buffer := range []int{14, 23, 64} {
		encodeBuffer = buffer
		var all failOnce
		if err := Encode(&all, testutil.Chopped(m, 16)); err != nil || all.n < size/buffer {
			t.Fatalf("a buffer of %d: %v after %d writes", buffer, err, all.n)
		}
		for k := 1; k <= all.n; k++ {
			if err := Encode(&failOnce{at: k}, testutil.Chopped(m, 16)); err != io.ErrClosedPipe {
				t.Fatalf("a buffer of %d, a writer whose write %d of %d alone fails: %v", buffer, k, all.n, err)
			}
		}
	}
}

// failOnce fails its write number at (from 1), and no other.
type failOnce struct{ n, at int }

func (f *failOnce) Write(p []byte) (int, error) {
	if f.n++; f.n == f.at {
		return 0, io.ErrClosedPipe
	}
	return len(p), nil
}

// shortWriter takes one byte less than it is given, without an error.
type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) { return len(p) - 1, nil }

// TestNoAllocationPerPixel: writing and reading a photograph allocates a
// few buffers, not something for every chunk (a photograph's pixels are
// nearly all chunks of their own: 19,200 here).
func TestNoAllocationPerPixel(t *testing.T) {
	m := pictures()["photograph with transparency"]
	var file bytes.Buffer
	if err := Encode(&file, testutil.Chopped(m, 16)); err != nil {
		t.Fatal(err)
	}
	if n := testing.AllocsPerRun(5, func() {
		if err := Encode(io.Discard, testutil.Chopped(m, 16)); err != nil {
			t.Fatal(err)
		}
	}); n > 100 {
		t.Errorf("writing: %v allocations", n)
	}
	if n := testing.AllocsPerRun(5, func() {
		src, err := Open(bytes.NewReader(file.Bytes()), nil)
		if err != nil {
			t.Fatal(err)
		}
		for {
			if _, err := src.Next(); err != nil {
				break
			}
		}
		src.Close()
	}); n > 100 {
		t.Errorf("reading: %v allocations", n)
	}
}
