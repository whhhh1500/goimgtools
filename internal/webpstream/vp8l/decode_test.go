package vp8l

import (
	"bytes"
	"encoding/binary"
	"image"
	"io"
	"os"
	"path/filepath"
	"testing"

	xvp8l "golang.org/x/image/vp8l"

	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// vp8lChunk returns the data of the VP8L chunk of a WebP file, or nil.
func vp8lChunk(data []byte) []byte {
	for pos := 12; pos+8 <= len(data); {
		n := int(binary.LittleEndian.Uint32(data[pos+4:]))
		if pos+8+n > len(data) {
			return nil
		}
		if string(data[pos:pos+4]) == "VP8L" {
			return data[pos+8 : pos+8+n]
		}
		pos += 8 + n + n&1
	}
	return nil
}

// decodeRows decodes chunk row by row and compares every row with want. It
// returns errWindow when a backward reference does not fit the test window.
func decodeRows(t *testing.T, chunk []byte, want *image.NRGBA) error {
	env := pipeline.NewEnv(0, t.TempDir())
	z := NewDecoder(bytes.NewReader(chunk), 0, int64(len(chunk)), env)
	defer func() {
		z.Close()
		if used := env.Budget.Used(); used != 0 {
			t.Errorf("%d bytes reserved after Close", used)
		}
	}()
	w, h, err := z.DecodeHeader()
	if err != nil {
		return err
	}
	if w != want.Rect.Dx() || h != want.Rect.Dy() {
		t.Fatalf("size %dx%d, want %v", w, h, want.Rect.Size())
	}
	for y := 0; y < h; y++ {
		row, err := z.NextRow()
		if err != nil {
			return err
		}
		if !bytes.Equal(row, want.Pix[y*want.Stride:][:4*w]) {
			t.Fatalf("row %d differs", y)
		}
	}
	if _, err := z.NextRow(); err != io.EOF {
		t.Errorf("after the last row: %v, want io.EOF", err)
	}
	return nil
}

// TestSmallWindow shrinks the pixel ring so that it wraps many times on the
// small test images. Files with references that reach beyond the shrunk ring
// cannot be decoded that way and are skipped.
func TestSmallWindow(t *testing.T) {
	dir := testutil.XImageTestdata()
	if dir == "" {
		t.Skip("golang.org/x/image testdata not found")
	}
	names, _ := filepath.Glob(filepath.Join(dir, "*.lossless.webp"))
	if len(names) == 0 {
		t.Fatal("no lossless test files")
	}
	defer func() { testWindow = 0 }()
	for _, window := range []int64{0, 1, 700, 5000, 40000} {
		wrapped := 0
		for _, name := range names {
			data, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			chunk := vp8lChunk(data)
			if chunk == nil {
				t.Fatalf("%s: no VP8L chunk", name)
			}
			m, err := xvp8l.Decode(bytes.NewReader(chunk))
			if err != nil {
				t.Fatal(err)
			}
			want := m.(*image.NRGBA)
			testWindow = window
			err = decodeRows(t, chunk, want)
			if err == errWindow && window != 0 {
				continue
			}
			if err != nil {
				t.Errorf("%s, window %d: %v", filepath.Base(name), window, err)
				continue
			}
			b := want.Rect
			if window != 0 && window+int64(b.Dx())+maxLZ77Length < int64(b.Dx())*int64(b.Dy()) {
				wrapped++
			}
		}
		t.Logf("window %d: %d of %d files decoded with a wrapping ring", window, wrapped, len(names))
		if window >= 5000 && wrapped == 0 {
			t.Errorf("window %d: no file exercised the wrapping ring", window)
		}
	}
}

// TestDistanceBound checks the constants the ring size relies on.
func TestDistanceBound(t *testing.T) {
	// The largest values lz77Param can produce need no bits beyond the
	// extra bits, which are all ones here.
	d := &decoder{bits: 0xffffffff, nBits: 32}
	length, err := d.lz77Param(nLengthCodes - 1)
	if err != nil || length != maxLZ77Length {
		t.Errorf("largest length %d, %v; want %d", length, err, maxLZ77Length)
	}
	d = &decoder{bits: 0xffffffff, nBits: 32}
	code, err := d.lz77Param(nDistanceCodes - 1)
	if err != nil || code != maxLZ77Distance {
		t.Errorf("largest distance code %d, %v; want %d", code, err, maxLZ77Distance)
	}
	const maxWidth = 1 << 14
	most := int32(0)
	for c := uint32(1); c <= 120; c++ {
		most = max(most, distanceMap(maxWidth, c))
	}
	most = max(most, distanceMap(maxWidth, code))
	if most > maxLZ77Distance {
		t.Errorf("largest distance %d exceeds %d", most, maxLZ77Distance)
	}
}
