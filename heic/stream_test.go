package heic

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"io"
	"os"
	"runtime"
	"testing"

	gheic "github.com/gen2brain/h265/heic"
	"github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// texture is a picture whose colour changes at every pixel, so that the
// chroma interpolation across the rows of tiles shows in the pixels.
func texture(w, h int) *image.NRGBA {
	m := image.NewNRGBA(image.Rect(0, 0, w, h))
	seed := uint32(3)
	for i := 0; i < len(m.Pix); i += 4 {
		seed = seed*1664525 + 1013904223
		x, y := (i/4)%w, (i/4)/w
		m.Pix[i], m.Pix[i+1], m.Pix[i+2], m.Pix[i+3] = byte(x*7+y), byte(y*5)^byte(seed>>28), byte((x+y)*3), 255
	}
	return m
}

// streamed decodes a file with the stream, without helpers and with three
// (tiles decoded ahead, rows converted at once), and checks both give the
// same; nil if it would not take it.
func streamed(t *testing.T, data []byte) *image.NRGBA {
	t.Helper()
	a, _, err := streamedWith(data, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := streamedWith(data, 3, 0)
	if err != nil {
		t.Fatal(err)
	}
	if (a == nil) != (b == nil) || a != nil && !bytes.Equal(a.Pix, b.Pix) {
		t.Fatal("other pixels with helpers")
	}
	return a
}

// streamedWith decodes a file with the stream, under a memory limit (0 for
// the default), and says how many rows of tiles it held.
func streamedWith(data []byte, helpers int, limit int64) (*image.NRGBA, int, error) {
	env := pipeline.NewEnv(limit, "")
	env.SetHelpers(helpers)
	s, err := newStream(data, env)
	if err != nil || s == nil {
		return nil, 0, err
	}
	defer s.Close()
	out := image.NewNRGBA(s.Bounds())
	for {
		strip, err := s.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, 0, err
		}
		m := strip.(*image.NRGBA)
		for y := m.Rect.Min.Y; y < m.Rect.Max.Y; y++ {
			copy(out.Pix[y*out.Stride:][:4*out.Rect.Dx()], m.Pix[(y-m.Rect.Min.Y)*m.Stride:])
		}
	}
	if env.Budget.Used() != s.reserve {
		return nil, 0, fmt.Errorf("%d bytes reserved, %d counted", env.Budget.Used(), s.reserve)
	}
	return out, s.slots, nil
}

// whole decodes a file with gen2brain/h265, as the module did before.
func whole(t *testing.T, data []byte) *image.NRGBA {
	t.Helper()
	m, _, err := gheic.DecodeColor(bytes.NewReader(data), gheic.Options{AutoRotate: true})
	if err != nil {
		t.Fatal(err)
	}
	n, ok := m.(*image.NRGBA)
	if !ok {
		t.Fatalf("%T", m)
	}
	return n
}

func sum(m *image.NRGBA) string { return fmt.Sprintf("%x", sha256.Sum256(m.Pix))[:16] }

func testFile(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// heicOf writes a texture as HEIC: in tiles of the side given (cut where
// the image ends), or as one picture for 0.
func heicOf(t testing.TB, w, h, tile int, o Options) []byte {
	t.Helper()
	saved, savedWhole := tileSide, wholePixels
	defer func() { tileSide, wholePixels = saved, savedWhole }()
	if tile > 0 {
		tileSide, wholePixels = tile, 0
	}
	b, err := goimgtools.FromImage(texture(w, h)).ToFormat("heic", o).ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestStreamSameAsWhole: a grid decoded a row of tiles at a time, and a
// picture made into pixels a strip at a time, give the pixels of
// gen2brain/h265's whole decoding as it converts them with SIMD: grids of
// small tiles cut by odd sizes, at 4:2:0 and 4:4:4, single pictures (with
// a clean aperture, as odd sizes are written), and the test files it
// takes (4:2:0, 4:2:2, 4:4:4 and 4:0:0, one turned by irot). The sums hold
// on every processor (the conversion rounds every product); the whole
// decoding is compared where it converts that way too.
func TestStreamSameAsWhole(t *testing.T) {
	check := func(name string, b []byte, want string) {
		t.Helper()
		got := streamed(t, b)
		if got == nil {
			t.Fatalf("%s: not streamed", name)
		}
		if s := sum(got); s != want {
			t.Errorf("%s: sum %s, want %s", name, s, want)
		}
		if wholeSame {
			if w := whole(t, b); got.Rect != w.Rect || !bytes.Equal(got.Pix, w.Pix) {
				t.Errorf("%s: not the pixels of the whole decoding", name)
			}
		}
	}
	for _, c := range []struct {
		w, h, tile int
		o          Options
		sum        string
	}{
		{301, 203, 64, Options{Effort: 1}, "ceab92d002b096e1"},
		{256, 128, 64, Options{Effort: 1}, "914f7a02200ddec5"},
		{97, 251, 32, Options{Effort: 1}, "f525bfde0cd03d34"},
		{301, 203, 64, Options{Effort: 1, Chroma444: true}, "72f89741930d2c2d"},
		{131, 77, 0, Options{Effort: 1}, "54e4b0aab20683ce"},
		{131, 77, 0, Options{Effort: 1, Chroma444: true}, "3e9445cde663c954"},
	} {
		check(fmt.Sprintf("%dx%d in tiles of %d %+v", c.w, c.h, c.tile, c.o), heicOf(t, c.w, c.h, c.tile, c.o), c.sum)
	}
	// A clean aperture moved off the corner (x 4, y 2 of the 132 x 78
	// picture: offsets of -2 from the centre), and one off the chroma grid
	// (x 3), which h265 cuts from the pixels and so decodes whole.
	b := heicOf(t, 131, 77, 0, Options{Effort: 1})
	at := bytes.Index(b, []byte("clap"))
	if at < 0 {
		t.Fatal("no clap")
	}
	for _, c := range []struct {
		v     [8]int32
		whole bool
		sum   string
	}{
		{[8]int32{120, 1, 70, 1, -2, 1, -2, 1}, false, "021d73a71581003c"},
		{[8]int32{120, 1, 70, 1, -3, 1, -2, 1}, true, ""},
	} {
		m := bytes.Clone(b)
		for i, v := range c.v {
			binary.BigEndian.PutUint32(m[at+4+4*i:], uint32(v))
		}
		if c.whole {
			if streamed(t, m) != nil {
				t.Errorf("clap %v: streamed", c.v)
			}
			continue
		}
		check(fmt.Sprintf("clap %v", c.v), m, c.sum)
	}
	// A picture smaller than its ispe box says (which h265 decodes to the
	// smaller size after saying the larger one) is an error.
	m := bytes.Clone(b)
	binary.BigEndian.PutUint32(m[bytes.Index(m, []byte("ispe"))+8:], 140)
	if _, _, err := streamedWith(m, 0, 0); !errors.Is(err, goimgtools.ErrFormat) {
		t.Errorf("ispe larger than the picture: %v", err)
	}
	for _, c := range []struct{ name, sum string }{
		{"basic.heic", "e84abe406c7ac4d8"},
		{"chroma444.heic", "295efa275cc41885"},
		{"test_exif.heic", "bfec4c500d7df317"},
		{"chroma422.heic", "e41cdcb08b940c16"},
		{"gray.heic", "716f627794cd2262"},
	} {
		check(c.name, testFile(t, c.name), c.sum)
	}
	// Transparency is decoded whole.
	for _, name := range []string{"alpha.heic", "grid_alpha.heic"} {
		if streamed(t, testFile(t, name)) != nil {
			t.Errorf("%s: streamed", name)
		}
	}
}

// TestStreamLimits: under memory limits from too small to enough, a grid
// either decodes to the same pixels, holding as many rows of tiles as the
// limit has room for (two to four), or fails with ErrMemoryLimit; and the
// memory it counts is what it reserved.
func TestStreamLimits(t *testing.T) {
	for _, c := range []struct{ w, h, tile int }{{301, 203, 64}, {131, 77, 0}} {
		b := heicOf(t, c.w, c.h, c.tile, Options{Effort: 1})
		want, _, err := streamedWith(b, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		seen := map[int]bool{}
		for limit := int64(20 << 10); limit <= 600<<10; limit += 2 << 10 {
			for _, helpers := range []int{0, 3} {
				got, slots, err := streamedWith(b, helpers, limit)
				if err != nil {
					if !errors.Is(err, pipeline.ErrMemoryLimit) {
						t.Fatalf("%dx%d, limit %d: %v", c.w, c.h, limit, err)
					}
					continue
				}
				if !bytes.Equal(got.Pix, want.Pix) {
					t.Fatalf("%dx%d, limit %d, %d rows of tiles: other pixels", c.w, c.h, limit, slots)
				}
				seen[slots] = true
			}
		}
		if c.tile > 0 && !(seen[2] && seen[3] && seen[4]) {
			t.Errorf("%dx%d: rows of tiles held %v, want 2, 3 and 4", c.w, c.h, seen)
		}
		if len(seen) == 0 {
			t.Errorf("%dx%d: never decoded", c.w, c.h)
		}
	}
}

// TestStreamClose: a stream closed before it is read, or part way with
// helpers decoding ahead, returns its memory and leaves no goroutine
// behind.
func TestStreamClose(t *testing.T) {
	b := heicOf(t, 301, 203, 64, Options{Effort: 1})
	before := runtime.NumGoroutine()
	for strips := range 3 {
		env := pipeline.NewEnv(0, "")
		env.SetHelpers(3)
		s, err := newStream(b, env)
		if err != nil || s == nil {
			t.Fatal(err)
		}
		for range strips {
			if _, err := s.Next(); err != nil && err != io.EOF {
				t.Fatal(err)
			}
		}
		s.Close()
		if u := env.Budget.Used(); u != 0 {
			t.Errorf("after %d strips: %d bytes still reserved", strips, u)
		}
	}
	if n := runtime.NumGoroutine(); n > before {
		t.Errorf("%d goroutines, %d before", n, before)
	}
}

// turnRef turns and flips m as gen2brain/h265 does (its transform.go):
// turn anticlockwise quarter turns, then flip 0 top and bottom, 1 left and
// right.
func turnRef(m *image.NRGBA, turn, flip int) *image.NRGBA {
	w, h := m.Rect.Dx(), m.Rect.Dy()
	ow, oh := w, h
	if turn&1 != 0 {
		ow, oh = h, w
	}
	out := image.NewNRGBA(image.Rect(0, 0, ow, oh))
	for j := range h {
		for i := range w {
			var dx, dy int
			switch turn {
			case 0:
				dx, dy = i, j
			case 1:
				dx, dy = j, w-1-i
			case 2:
				dx, dy = w-1-i, h-1-j
			default:
				dx, dy = h-1-j, i
			}
			switch flip {
			case 0:
				dy = oh - 1 - dy
			case 1:
				dx = ow - 1 - dx
			}
			copy(out.Pix[dy*out.Stride+4*dx:][:4], m.Pix[j*m.Stride+4*i:])
		}
	}
	return out
}

// TestStreamTurned: an image turned by irot and flipped by imir, every way,
// is the image as it would be without them, turned as h265 turns it: a
// grid held whole (at 4:2:0 and 4:4:4) and a single picture cut by a clean
// aperture; the same as h265's whole decoding where it converts as the
// stream does; and under a memory limit too small to hold the grid, an
// ErrMemoryLimit.
func TestStreamTurned(t *testing.T) {
	defer func() { primaryProps = nil }()
	for _, c := range []struct {
		w, h, tile int
		o          Options
	}{
		{301, 203, 64, Options{Effort: 1}},
		{301, 203, 64, Options{Effort: 1, Chroma444: true}},
		{131, 77, 0, Options{Effort: 1}},
	} {
		primaryProps = nil
		plain := streamed(t, heicOf(t, c.w, c.h, c.tile, c.o))
		for turn := range 4 {
			for flip := -1; flip <= 1; flip++ {
				if turn == 0 && flip < 0 {
					continue
				}
				primaryProps = nil
				if turn != 0 {
					primaryProps = append(primaryProps, property{box("irot", []byte{byte(turn)}), true})
				}
				if flip >= 0 {
					primaryProps = append(primaryProps, property{box("imir", []byte{byte(flip)}), true})
				}
				b := heicOf(t, c.w, c.h, c.tile, c.o)
				name := fmt.Sprintf("%dx%d in tiles of %d %+v, turned %d, flipped %d", c.w, c.h, c.tile, c.o, turn, flip)
				got := streamed(t, b)
				if got == nil {
					t.Fatalf("%s: not streamed", name)
				}
				if want := turnRef(plain, turn, flip); got.Rect != want.Rect || !bytes.Equal(got.Pix, want.Pix) {
					t.Errorf("%s: %v, not the image turned", name, got.Rect)
				}
				if wholeSame {
					if w := whole(t, b); got.Rect != w.Rect || !bytes.Equal(got.Pix, w.Pix) {
						t.Errorf("%s: not the pixels of the whole decoding", name)
					}
				}
				if c.tile > 0 {
					if _, _, err := streamedWith(b, 3, 150<<10); !errors.Is(err, pipeline.ErrMemoryLimit) {
						t.Errorf("%s, a limit of 150KB: %v", name, err)
					}
				}
			}
		}
	}
}
