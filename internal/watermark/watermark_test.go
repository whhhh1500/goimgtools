package watermark

import (
	"bytes"
	"image"
	"image/draw"
	"image/jpeg"
	"math"
	"sync"
	"testing"

	"github.com/whhhh1500/goimgtools/internal/testutil"
)

// mark marks an NRGBA image in memory as the pipeline does, row by row
// with the rows around each repeated past the edges.
func mark(t *testing.T, src *image.NRGBA, payload []byte, key string, o Options) *image.NRGBA {
	t.Helper()
	w, h := src.Rect.Dx(), src.Rect.Dy()
	m, err := NewMarker(payload, NewKey(key), o, w, h)
	if err != nil {
		t.Fatal(err)
	}
	out := image.NewNRGBA(image.Rect(0, 0, w, h))
	row := m.NewRow()
	r := m.Radius()
	win := make([][]uint8, 2*r+1)
	for y := range h {
		for i := range win {
			sy := max(0, min(h-1, y-r+i))
			win[i] = src.Pix[sy*src.Stride : sy*src.Stride+4*w]
		}
		row(y, win, out.Pix[y*out.Stride:y*out.Stride+4*w])
	}
	return out
}

func luma(img *image.NRGBA) ([]float64, int, int) {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	l := make([]float64, w*h)
	for y := range h {
		for x := range w {
			p := img.Pix[y*img.Stride+4*x:]
			l[y*w+x] = float64(0.299*float64(p[0])) + float64(0.587*float64(p[1])) + float64(0.114*float64(p[2]))
		}
	}
	return l, w, h
}

// TestCode: the payload and its check come back from the convolutional
// code with weak and wrong bits, and not with another key.
func TestCode(t *testing.T) {
	k := NewKey("k")
	for _, payload := range [][]byte{{0}, []byte("user-1234"), bytes.Repeat([]byte{0xa5}, MaxPayload)} {
		coded := encode(frameBits(payload, k))
		if len(coded) != codedLen(len(payload)) {
			t.Fatalf("coded length %d, want %d", len(coded), codedLen(len(payload)))
		}
		soft := make([]float64, len(coded))
		for i, b := range coded {
			soft[i] = 1 - 2*float64(b)
			// A quarter of the bits weak and one in 37 wrong.
			if i%4 == 1 {
				soft[i] *= 0.2
			}
			if i%37 == 5 {
				soft[i] = -soft[i]
			}
		}
		got, ok := unframe(forward(nil, soft, 8*(len(payload)+4)).list(1)[0], k)
		if !ok || !bytes.Equal(got, payload) {
			t.Errorf("%x: decoded %x, %v", payload, got, ok)
		}
		if _, ok := unframe(forward(nil, soft, 8*(len(payload)+4)).list(1)[0], NewKey("other")); ok {
			t.Errorf("%x: checks with another key", payload)
		}
	}
}

// TestList: the list's first sequence has the best metric, the others are
// different and each no likelier than the one before, none is likelier
// than the last (checked against every sequence differing from the best
// in up to two bits), and a sequence whose best decoding is wrong is often
// later in the list.
func TestList(t *testing.T) {
	r := &rng{3}
	k := NewKey("k")
	payload := []byte("list-test")
	coded := encode(frameBits(payload, k))
	nbits := 8 * (len(payload) + 4)
	metric := func(bits []uint8, soft []float64) float64 {
		c := encode(bits)
		var m float64
		for i, b := range c {
			m += (1 - 2*float64(b)) * soft[i]
		}
		return m
	}
	rescued := 0
	for trial := range 200 {
		soft := make([]float64, len(coded))
		for i, b := range coded {
			// Heavy noise: about one bit in four wrong.
			noise := (float64(r.intn(2001)) - 1000) / 1000 * 2.4
			soft[i] = 1 - 2*float64(b) + noise
		}
		tr := forward(nil, soft, nbits)
		list := tr.list(listSize)
		if len(list) != listSize {
			t.Fatalf("trial %d: %d sequences", trial, len(list))
		}
		if m := metric(list[0], soft); math.Abs(m-tr.best()) > 1e-9 {
			t.Fatalf("trial %d: the first sequence's metric %v, the best %v", trial, m, tr.best())
		}
		for i := 1; i < len(list); i++ {
			if bytes.Equal(list[i], list[i-1]) || metric(list[i], soft) > metric(list[i-1], soft)+1e-9 {
				t.Fatalf("trial %d: sequence %d repeated or likelier than the one before", trial, i)
			}
		}
		if trial < 20 {
			// No sequence near the best outside the list is likelier than
			// its last.
			last := metric(list[len(list)-1], soft)
			in := map[string]bool{}
			for _, s := range list {
				in[string(s)] = true
			}
			for i := range nbits {
				for j := i; j < nbits; j++ {
					s := append([]uint8{}, list[0]...)
					s[i] ^= 1
					if j > i {
						s[j] ^= 1
					}
					if !in[string(s)] && metric(s, soft) > last+1e-9 {
						t.Fatalf("trial %d: a sequence outside the list is likelier than its last", trial)
					}
				}
			}
		}
		if _, ok := unframe(list[0], k); !ok {
			for _, bits := range list[1:] {
				if got, ok := unframe(bits, k); ok && bytes.Equal(got, payload) {
					rescued++
					break
				}
			}
		}
	}
	if rescued == 0 {
		t.Error("the list never found the payload where the best sequence missed it")
	}
}

// TestRoundTrip: a mark of each kind is read back with its key, not with
// another, and nothing is read from the picture unmarked.
func TestRoundTrip(t *testing.T) {
	src := testutil.Natural(900, 600, 1)
	for _, kind := range []Kind{Blind, Screen} {
		marked := mark(t, src, []byte("user-1234"), "secret", Options{Kind: kind})
		l, w, h := luma(marked)
		got, ok := Detect(l, w, h, NewKey("secret"), nil)
		if !ok || string(got) != "user-1234" {
			t.Errorf("kind %d: read %q, %v", kind, got, ok)
		}
		if _, ok := Detect(l, w, h, NewKey("wrong"), nil); ok {
			t.Errorf("kind %d: read with the wrong key", kind)
		}
	}
	l, w, h := luma(src)
	if _, ok := Detect(l, w, h, NewKey("secret"), nil); ok {
		t.Error("read a mark from an unmarked picture")
	}
	if _, ok := Detect(make([]float64, 20*20), 20, 20, NewKey("secret"), nil); ok {
		t.Error("read a mark from a tiny blank picture")
	}
	// A flat picture, which has nothing to even out (once a panic).
	flat := make([]float64, 300*200)
	for i := range flat {
		flat[i] = 200
	}
	if _, ok := Detect(flat, 300, 200, NewKey("secret"), nil); ok {
		t.Error("read a mark from a flat picture")
	}
}

// TestShape: shape weighs a pattern's spectrum by shapeWeight (against
// the transforms of fft2) and keeps its power.
func TestShape(t *testing.T) {
	v := make([]float64, N*N)
	r := newRNG(NewKey("k"), "test")
	var p0 float64
	for i := range v {
		v[i] = r.sign()
		p0 += v[i] * v[i]
	}
	a := make([]complex64, N*N)
	for i, x := range v {
		a[i] = complex(float32(x), 0)
	}
	shape(v)
	fft2(a, N, N, false)
	for i := range a {
		w := float32(shapeWeight(i/N, i%N))
		a[i] = complex(real(a[i])*w, imag(a[i])*w)
	}
	fft2(a, N, N, true)
	var p1, pa, dot float64
	for i, x := range v {
		p1 += x * x
		y := float64(real(a[i]))
		pa += y * y
		dot += x * y
	}
	if math.Abs(p1-p0) > 1e-9*p0 {
		t.Errorf("power %v, want %v", p1, p0)
	}
	if c := dot / math.Sqrt(p1*pa); c < 1-1e-6 {
		t.Errorf("correlation with the FFT's %v", c)
	}
}

// TestDetectParallel: Detect finds the same with its work shared out as
// without, read or not (saved as JPEG ever harder, with the slow ways
// tried; unmarked).
func TestDetectParallel(t *testing.T) {
	allAtOnce := func(n int, fn func(i int)) {
		var wg sync.WaitGroup
		for i := range n {
			wg.Add(1)
			go func() {
				defer wg.Done()
				fn(i)
			}()
		}
		wg.Wait()
	}
	src := testutil.Natural(700, 500, 4)
	marked := mark(t, src, []byte("user-1234"), "secret", Options{})
	pics := []*image.NRGBA{marked, src}
	for _, q := range []int{60, 30, 15} {
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, marked, &jpeg.Options{Quality: q}); err != nil {
			t.Fatal(err)
		}
		dec, err := jpeg.Decode(&buf)
		if err != nil {
			t.Fatal(err)
		}
		n := image.NewNRGBA(dec.Bounds())
		draw.Draw(n, n.Rect, dec, dec.Bounds().Min, draw.Src)
		pics = append(pics, n)
	}
	for i, p := range pics {
		l, w, h := luma(p)
		a, oka := Detect(l, w, h, NewKey("secret"), nil)
		b, okb := Detect(l, w, h, NewKey("secret"), allAtOnce)
		if oka != okb || !bytes.Equal(a, b) {
			t.Errorf("picture %d: %q %v alone, %q %v shared out", i, a, oka, b, okb)
		}
	}
}

// TestLocalWhiten: whitened block by block, a busy half and a flat half
// with a little noise come out alike without a floor, and the flat half
// far quieter with one; shared out, the same.
func TestLocalWhiten(t *testing.T) {
	const w, h = 160, 96
	y := make([]float64, w*h)
	r := uint32(1)
	for i := range y {
		r = r*1664525 + 1013904223
		n := float64(r>>24) - 127.5
		if i%w < w/2 {
			y[i] = 128 + n/256 // flat, noise of under a level
		} else {
			y[i] = 128 + n/2.5 // busy
		}
	}
	quiet := func(o []float64) float64 {
		var a, b float64
		for i, v := range o {
			// Away from the halves' border, which blocks there see both.
			if x := i % w; x < w/2-32 {
				a += v * v
			} else if x >= w/2+32 {
				b += v * v
			}
		}
		return math.Sqrt(a / b)
	}
	d := &detector{w: w, h: h}
	if q := quiet(d.localWhiten(y, 0)); q < 0.5 {
		t.Errorf("without a floor, the flat half %.2f of the busy one", q)
	}
	want := d.localWhiten(y, localFloorFirst)
	if q := quiet(want); q > 0.1 {
		t.Errorf("with a floor, the flat half %.2f of the busy one", q)
	}
	d.par = func(n int, fn func(i int)) {
		var wg sync.WaitGroup
		for i := range n {
			wg.Add(1)
			go func() {
				defer wg.Done()
				fn(i)
			}()
		}
		wg.Wait()
	}
	got := d.localWhiten(y, localFloorFirst)
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("shared out, %v at %d against %v", got[i], i, want[i])
		}
	}
}

// TestTexture: a flat picture's texture is 0, a checkerboard of 0 and 90's
// (the luma's deviation over every 3 x 3 window, 4 of one and 5 of the
// other) about 44.7; a small busy picture's blind mark gets chips of a
// pixel, a smooth one's and a large one's do not.
func TestTexture(t *testing.T) {
	const w, h = 40, 30
	row := make([]uint8, 4*w)
	flat := NewTextureMeter(w)
	board := NewTextureMeter(w)
	for y := range h {
		for x := range w {
			v := uint8(0)
			if (x+y)%2 == 1 {
				v = 90
			}
			row[4*x], row[4*x+1], row[4*x+2], row[4*x+3] = v, v, v, 255
		}
		board.Row(row)
		flat.Row(make([]uint8, 4*w))
	}
	if flat.Mean() != 0 {
		t.Errorf("flat texture %v", flat.Mean())
	}
	if got := board.Mean(); math.Abs(got-90*math.Sqrt(20.0/81)) > 0.01 {
		t.Errorf("checkerboard texture %v, want %v", got, 90*math.Sqrt(20.0/81))
	}
	k := NewKey("k")
	for _, c := range []struct {
		w, h    int
		texture float64
		chip    float64
	}{
		{240, 160, fineTexture, 1},
		{240, 160, fineTexture - 0.1, 2},
		{600, 400, fineTexture, 2},
	} {
		if !NeedsTexture(Options{}, c.w, c.h) && c.w < smallSide {
			t.Errorf("%d x %d: texture not wanted", c.w, c.h)
		}
		m, err := NewMarker([]byte("x"), k, Options{Texture: c.texture}, c.w, c.h)
		if err != nil {
			t.Fatal(err)
		}
		if m.chip != c.chip {
			t.Errorf("%d x %d, texture %v: chip %v, want %v", c.w, c.h, c.texture, m.chip, c.chip)
		}
	}
}

// TestNewMarker: the settings are checked.
func TestNewMarker(t *testing.T) {
	k := NewKey("")
	for _, c := range []struct {
		payload []byte
		o       Options
	}{
		{nil, Options{}},
		{make([]byte, MaxPayload+1), Options{}},
		{[]byte{1}, Options{Kind: 2}},
		{[]byte{1}, Options{Kind: -1}},
		{[]byte{1}, Options{Strength: -1}},
		{[]byte{1}, Options{Strength: 17}},
		{[]byte{1}, Options{Density: 0.5}},
		{[]byte{1}, Options{Density: MaxDensity + 1}},
	} {
		if _, err := NewMarker(c.payload, k, c.o, 100, 100); err == nil {
			t.Errorf("%d bytes, %+v: accepted", len(c.payload), c.o)
		}
	}
}

// TestCleanChroma: a grey mark over a colourful texture whose brightness
// follows its colour; cleaned, the texture left in the brightness is less
// and the mark is kept. A grey picture is left as it is.
func TestCleanChroma(t *testing.T) {
	const w, h = 300, 200
	l := make([]float64, w*h)
	cb, cr := make([]float32, w*h), make([]float32, w*h)
	mk := make([]float64, w*h)
	r := uint32(7)
	next := func() float64 {
		r = r*1664525 + 1013904223
		return float64(r>>8)/float64(1<<24) - 0.5
	}
	for i := range l {
		red, blue := 40*next(), 40*next()
		grey := 5 * next()
		mk[i] = 4 * next()
		v := 128 + float64(0.299*red) + float64(0.114*blue) + grey
		l[i] = v + mk[i]
		cb[i], cr[i] = float32(128+blue-v), float32(128+red-v)
	}
	dot := func(a, b []float64) float64 { // of a less its mean with b
		var s, mean float64
		for _, v := range a {
			mean += v
		}
		mean /= float64(len(a))
		for i := range a {
			s += float64((a[i] - mean) * b[i])
		}
		return s
	}
	resid := func(v []float64) float64 {
		var s, mean float64
		for i := range v {
			mean += v[i] - mk[i]
		}
		mean /= float64(len(v))
		for i := range v {
			d := v[i] - mk[i] - mean
			s += float64(d * d)
		}
		return s
	}
	before, mBefore := resid(l), dot(l, mk)
	c := append([]float64(nil), l...)
	if !CleanChroma(c, cb, cr, w, h) {
		t.Fatal("a colourful picture not cleaned")
	}
	after, mAfter := resid(c), dot(c, mk)
	if !(after < 0.8*before) {
		t.Errorf("texture left %.3g of %.3g", after, before)
	}
	if !(mAfter > 0.9*mBefore) {
		t.Errorf("mark %.3g of %.3g", mAfter, mBefore)
	}
	for i := range cb {
		cb[i], cr[i] = 0, 0
	}
	g := append([]float64(nil), l...)
	if CleanChroma(g, cb, cr, w, h) || !slicesEqual(g, l) {
		t.Error("a grey picture was changed")
	}
}

func slicesEqual(a, b []float64) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return len(a) == len(b)
}
