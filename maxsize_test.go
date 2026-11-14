package goimgtools

import (
	"bytes"
	"errors"
	"image"
	"image/jpeg"
	"path/filepath"
	"testing"

	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// TestRecording: an image recorded and replayed gives the strips it was
// given, as often as asked, for every pixel type.
func TestRecording(t *testing.T) {
	for name, m := range testutil.Images(67, 41) {
		for _, rows := range []int{1, 7, 16, 500} {
			for _, limit := range []int64{0, 600 << 10} {
				dir := t.TempDir()
				env := pipeline.NewEnv(limit, dir)
				src := testutil.ChoppedEnv(m, rows, env)
				rec, err := pipeline.Record(src)
				if err != nil {
					t.Fatalf("%s: %v", name, err)
				}
				for round := 0; round < 3; round++ {
					s, err := rec.Source()
					if err != nil {
						t.Fatal(err)
					}
					y := 0
					for {
						strip, err := s.Next()
						if err != nil {
							break
						}
						b := strip.Bounds()
						if b.Min.Y != y || b.Dy() != min(rows, 41-y) || b.Min.X != 0 || b.Max.X != 67 {
							t.Fatalf("%s rows=%d: strip %v at row %d", name, rows, b, y)
						}
						for yy := b.Min.Y; yy < b.Max.Y; yy++ {
							for x := 0; x < 67; x++ {
								if strip.At(x, yy) != m.At(x, yy) {
									t.Fatalf("%s rows=%d round %d: pixel (%d,%d) = %v, want %v", name, rows, round, x, yy, strip.At(x, yy), m.At(x, yy))
								}
							}
						}
						y = b.Max.Y
					}
					if y != 41 {
						t.Fatalf("%s: replay ended at row %d", name, y)
					}
					s.Close()
				}
				rec.Close()
				left, _ := filepath.Glob(filepath.Join(dir, "*"))
				if env.Budget.Used() != 0 || len(left) != 0 {
					t.Fatalf("%s: %d bytes reserved, %d temp files left", name, env.Budget.Used(), len(left))
				}
			}
		}
	}
}

// TestMaxFileSize: the file fits, at the highest quality that fits, and is
// exactly what asking for that quality directly gives.
func TestMaxFileSize(t *testing.T) {
	src := testutil.Images(613, 457)["rgba-opaque"]
	inPNG := pngBytes(t, src)
	inJPG := jpegBytes(src, 95)
	for _, c := range []struct {
		name   string
		in     []byte
		format func(m *Image, q int) *Image
		top    int
	}{
		{"png to jpeg", inPNG, func(m *Image, q int) *Image { return m.JPEG(q) }, 80},
		{"jpeg to jpeg", inJPG, func(m *Image, q int) *Image { return m.JPEG(q) }, 80},
		{"png to webp", inPNG, func(m *Image, q int) *Image { return m.WebP(WebPOptions{Quality: q}) }, 75},
	} {
		// The size at every quality, to know what the right answer is.
		size := map[int]int{}
		at := func(q int) int {
			if size[q] == 0 {
				b, err := c.format(FromBytes(c.in), q).ToBytes()
				if err != nil {
					t.Fatalf("%s q%d: %v", c.name, q, err)
				}
				size[q] = len(b)
			}
			return size[q]
		}
		full := at(c.top)
		for _, limit := range []int{full + 1000, full, full * 3 / 4, full / 2, full / 3, at(1) + 10, at(1)} {
			testTries.Store(0)
			got, err := c.format(FromBytes(c.in), 0).MaxFileSize(int64(limit)).ToBytes()
			if err != nil {
				t.Fatalf("%s limit %d: %v", c.name, limit, err)
			}
			// Halving the range of qualities would take eight encodes.
			if n := testTries.Load(); n > 9 {
				t.Errorf("%s limit %d: %d encodes", c.name, limit, n)
			}
			if len(got) > limit {
				t.Fatalf("%s limit %d: file is %d bytes", c.name, limit, len(got))
			}
			// Which quality was used: the file equals a direct encode at it.
			q := 0
			for try := c.top; try >= 1; try-- {
				if at(try) == len(got) {
					if want, _ := c.format(FromBytes(c.in), try).ToBytes(); bytes.Equal(want, got) {
						q = try
						break
					}
				}
			}
			if q == 0 {
				t.Fatalf("%s limit %d: the file is not a plain encode at any quality", c.name, limit)
			}
			if q < c.top && at(q+1) <= limit {
				t.Errorf("%s limit %d: used quality %d (%d bytes) although %d fits (%d bytes)", c.name, limit, q, len(got), q+1, at(q+1))
			}
		}
		// Smaller than quality 1 can make.
		_, err := c.format(FromBytes(c.in), 0).MaxFileSize(int64(at(1) - 1)).ToBytes()
		if !errors.Is(err, ErrFileSize) {
			t.Errorf("%s: limit below the smallest file: %v", c.name, err)
		}
	}
	// With a chain, a small memory limit and a file: the file fits and
	// nothing else is left.
	dir := t.TempDir()
	out := filepath.Join(dir, "out.jpg")
	if err := FromBytes(inPNG).Resize(300, 0).MaxFileSize(6000).MemoryLimit(2 << 20).TempDir(dir).ToFile(out); err != nil {
		t.Fatal(err)
	}
	if info, err := Open(out).Metadata(); err != nil || info.Width != 300 {
		t.Fatalf("out.jpg: %+v, %v", info, err)
	}
	if names, _ := filepath.Glob(filepath.Join(dir, "*")); len(names) != 1 {
		t.Errorf("files left: %v", names)
	}
	// The quality asked for is the ceiling.
	low, _ := FromBytes(inPNG).JPEG(30).ToBytes()
	capped, err := FromBytes(inPNG).JPEG(30).MaxFileSize(1 << 30).ToBytes()
	if err != nil || !bytes.Equal(low, capped) {
		t.Errorf("a generous limit changed the output (err %v)", err)
	}
	if m, err := jpeg.Decode(bytes.NewReader(capped)); err != nil || m.Bounds().Size() != image.Pt(613, 457) {
		t.Errorf("capped file: %v", err)
	}
	// Formats without a quality, and a bad limit.
	if _, err := FromBytes(inPNG).PNG().MaxFileSize(1000).ToBytes(); err == nil {
		t.Error("MaxFileSize accepted for PNG")
	}
	if _, err := FromBytes(inPNG).WebP(WebPOptions{Lossless: true}).MaxFileSize(1000).ToBytes(); err == nil {
		t.Error("MaxFileSize accepted for lossless WebP")
	}
	if _, err := FromBytes(inPNG).JPEG(80).MaxFileSize(0).ToBytes(); err == nil {
		t.Error("MaxFileSize(0) accepted")
	}
}
