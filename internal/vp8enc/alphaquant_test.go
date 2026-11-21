package vp8enc

import (
	"bytes"
	"image"
	"math"
	"testing"

	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// TestAlphaLevels: libwebp's number of levels for each quality.
func TestAlphaLevels(t *testing.T) {
	for q, want := range map[int]int{1: 2, 20: 6, 50: 12, 70: 16, 71: 24, 80: 96, 99: 248} {
		if got := alphaLevels(q); got != want {
			t.Errorf("quality %d: %d levels, want %d", q, got, want)
		}
	}
}

// TestQuantizeLevels: at most n levels, the lowest and the highest value
// kept, every value mapped to a level near it, nothing done to a plane of
// no more values; and a case worked out by hand.
func TestQuantizeLevels(t *testing.T) {
	var freq [256]uint32
	for v := 0; v < 256; v++ {
		freq[v] = uint32(1 + (v*37)%11)
	}
	freq[0], freq[255] = 5000, 9000
	for _, n := range []int{2, 3, 6, 12, 16, 96, 248} {
		m := quantizeLevels(&freq, n)
		if m == nil {
			t.Fatalf("%d levels: no map", n)
		}
		if m[0] != 0 || m[255] != 255 {
			t.Fatalf("%d levels: 0 -> %d, 255 -> %d", n, m[0], m[255])
		}
		levels := map[uint8]bool{}
		for v := 0; v < 256; v++ {
			levels[m[v]] = true
			if d := math.Abs(float64(m[v]) - float64(v)); d > 256/float64(n) {
				t.Fatalf("%d levels: %d -> %d", n, v, m[v])
			}
			if v > 0 && m[v] < m[v-1] {
				t.Fatalf("%d levels: not in order at %d", n, v)
			}
		}
		if len(levels) > n {
			t.Fatalf("%d levels: %d used", n, len(levels))
		}
	}
	// Three values into two levels: 10 and 20 stay, 12 goes to 10.
	var few [256]uint32
	few[10], few[12], few[20] = 4, 1, 4
	m := quantizeLevels(&few, 2)
	if m == nil || m[10] != 10 || m[12] != 10 || m[20] != 20 {
		t.Fatalf("10, 12, 20 into 2 levels: %v", m)
	}
	if quantizeLevels(&few, 3) != nil {
		t.Error("3 values into 3 levels: a map")
	}
}

// TestAlphaQuality: the plane decodes to the reduced levels (no more of
// them than the quality allows, 0 and 255 kept), the ALPH header says it is
// pre-processed, the chunk is smaller, and the colour is the same.
func TestAlphaQuality(t *testing.T) {
	for name, m := range alphaImages() {
		b := m.Bounds()
		full := mustEncodeOpts(t, testutil.Chopped(m, 7), &Options{Quality: 70})
		for _, aq := range []int{20, 50, 90} {
			data := mustEncodeOpts(t, testutil.Chopped(m, 7), &Options{Quality: 70, AlphaQuality: aq})
			d := decodeX(t, data)
			levels := map[uint8]bool{}
			src := map[uint8]bool{}
			for y := 0; y < b.Dy(); y++ {
				for x := 0; x < b.Dx(); x++ {
					got, was := d.alpha[y*b.Dx()+x], m.Pix[m.PixOffset(x, y)+3]
					levels[got], src[was] = true, true
					if (was == 0 || was == 255) && got != was {
						t.Fatalf("%s aq %d: alpha %d came back %d", name, aq, was, got)
					}
				}
			}
			hdr := data[bytes.Index(data, []byte("ALPH"))+8]
			reduced := len(src) > alphaLevels(aq)
			if reduced != (hdr>>4&3 == 1) {
				t.Errorf("%s aq %d: %d values, ALPH header %#x", name, aq, len(src), hdr)
			}
			if reduced && len(levels) > alphaLevels(aq) {
				t.Errorf("%s aq %d: %d levels", name, aq, len(levels))
			}
			if !reduced && !bytes.Equal(data, full) {
				t.Errorf("%s aq %d: nothing to reduce, another file", name, aq)
			}
			fd := decodeX(t, full)
			if !bytes.Equal(d.yc.Y, fd.yc.Y) || !bytes.Equal(d.yc.Cb, fd.yc.Cb) {
				t.Errorf("%s aq %d: the colour changed", name, aq)
			}
		}
	}
	// A large smooth plane: much smaller.
	m := image.NewNRGBA(image.Rect(0, 0, 300, 200))
	for y := 0; y < 200; y++ {
		for x := 0; x < 300; x++ {
			d := math.Hypot(float64(x-150), float64(y-100)) / 90
			m.Pix[m.PixOffset(x, y)+3] = uint8(max(0, min(255, (1.3-d)*300)))
			m.Pix[m.PixOffset(x, y)+1] = uint8(x)
		}
	}
	alphaLen := func(data []byte) int {
		i := bytes.Index(data, []byte("ALPH"))
		return int(data[i+4]) | int(data[i+5])<<8 | int(data[i+6])<<16
	}
	full := alphaLen(mustEncodeOpts(t, testutil.Chopped(m, 9), nil))
	low := alphaLen(mustEncodeOpts(t, testutil.Chopped(m, 9), &Options{AlphaQuality: 30}))
	if low*2 > full {
		t.Errorf("smooth plane: ALPH %d bytes at alpha quality 30, %d exact", low, full)
	}
	var buf bytes.Buffer
	if err := Encode(&buf, testutil.Chopped(m, 9), &Options{AlphaQuality: 101}); err == nil {
		t.Error("alpha quality 101: no error")
	}
}

func mustEncodeOpts(t testing.TB, src pipeline.Source, o *Options) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := Encode(&buf, src, o); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
