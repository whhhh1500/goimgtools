package pipeline

import (
	"errors"
	"image"
	"image/color"
	"io"
	"testing"
)

// TestMap: every strip reaches the function once as NRGBA, what it writes
// is what comes out, and the stage gives its buffer back.
func TestMap(t *testing.T) {
	src := image.NewRGBA(image.Rect(3, 5, 203, 705))
	for i := range src.Pix {
		src.Pix[i] = uint8(i * 31)
	}
	for i := 3; i < len(src.Pix); i += 4 {
		src.Pix[i] = 255
	}
	env := NewEnv(4<<20, t.TempDir())
	rows := 0
	m := Map(FromImage(src, env), func(strip *image.NRGBA) error {
		rows += strip.Rect.Dy()
		for i := 0; i < len(strip.Pix); i += 4 {
			strip.Pix[i] ^= 0xff
		}
		return nil
	})
	// The source has no transparency and says so; the result keeps that.
	if m.Bounds() != src.Bounds() || m.ColorModel() != color.NRGBAModel || m.Env() != env || !IsOpaque(m) {
		t.Fatalf("bounds %v, opaque %v", m.Bounds(), IsOpaque(m))
	}
	got, err := Collect(m)
	if err != nil {
		t.Fatal(err)
	}
	if env.Budget.Used() == 0 {
		t.Error("the strip buffer is not counted in the budget")
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	if used := env.Budget.Used(); used != 0 {
		t.Errorf("%d bytes still reserved after Close", used)
	}
	if rows != 700 {
		t.Errorf("the function saw %d rows of 700", rows)
	}
	for y := 5; y < 705; y += 37 {
		for x := 3; x < 203; x += 11 {
			w := src.RGBAAt(x, y)
			if g := got.(*image.NRGBA).NRGBAAt(x, y); g != (color.NRGBA{w.R ^ 0xff, w.G, w.B, 255}) {
				t.Fatalf("pixel (%d,%d) = %v, source %v", x, y, g, w)
			}
		}
	}

	// A source known to be opaque stays so; WithAlpha takes that away.
	opaque := image.NewGray(image.Rect(0, 0, 8, 8))
	if !IsOpaque(Map(grayOpaque{FromImage(opaque, env)}, func(*image.NRGBA) error { return nil })) {
		t.Error("an opaque source is not opaque after Map")
	}
	if IsOpaque(WithAlpha(grayOpaque{FromImage(opaque, env)})) {
		t.Error("WithAlpha left the source opaque")
	}
	// FromImage asks the image: one translucent pixel, or an image type
	// that cannot tell, is not opaque.
	if !IsOpaque(FromImage(opaque, env)) {
		t.Error("FromImage of a Gray image is not opaque")
	}
	src.Pix[len(src.Pix)-1] = 254
	if IsOpaque(FromImage(src, env)) || IsOpaque(FromImage(struct{ image.Image }{opaque}, env)) {
		t.Error("FromImage says a translucent image, or one that cannot tell, is opaque")
	}

	// The function's error ends the stream; a strip larger than the budget
	// is refused.
	boom := errors.New("boom")
	bad := Map(FromImage(src, env), func(*image.NRGBA) error { return boom })
	if _, err := bad.Next(); !errors.Is(err, boom) {
		t.Errorf("error of the function: %v", err)
	}
	bad.Close()
	small := NewEnv(1<<10, t.TempDir())
	tight := Map(FromImage(src, small), func(*image.NRGBA) error { return nil })
	if _, err := tight.Next(); !errors.Is(err, ErrMemoryLimit) {
		t.Errorf("a strip larger than the budget: %v", err)
	}
	tight.Close()
	if small.Budget.Used() != 0 || env.Budget.Used() != 0 {
		t.Error("something is still reserved")
	}
}

type grayOpaque struct{ Source }

func (grayOpaque) Opaque() bool { return true }

// TestAsyncPanic: a panic in the goroutine of an Async stage is the error
// of the stage, not the end of the program.
func TestAsyncPanic(t *testing.T) {
	env := NewEnv(0, t.TempDir())
	n := 0
	src := Map(FromImage(image.NewRGBA(image.Rect(0, 0, 4000, 900)), env), func(*image.NRGBA) error {
		if n++; n == 3 {
			panic("three")
		}
		return nil
	})
	a := Async(src)
	var err error
	for err == nil {
		_, err = a.Next()
	}
	var pe *PanicError
	if !errors.As(err, &pe) || pe.Value != "three" || len(pe.Stack) == 0 {
		t.Fatalf("got %v, want the panic as an error", err)
	}
	if _, again := a.Next(); again != err {
		t.Errorf("the error does not stay: %v", again)
	}
	if err := a.Close(); err != nil {
		t.Error(err)
	}
	if used := env.Budget.Used(); used != 0 {
		t.Errorf("%d bytes still reserved", used)
	}
	if err == io.EOF {
		t.Error("ended without the panic")
	}
}
