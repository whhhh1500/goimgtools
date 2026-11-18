package tests

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"strings"
	"sync"
	"testing"

	. "github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// TestAroundSeesEveryStep: a hook sees every step of a recipe, built-in or
// not, by name and arguments in the words Do takes, in order; and those
// words, given to Do, make the same recipe.
func TestAroundSeesEveryStep(t *testing.T) {
	in := pngBytes(t, photoLike(160, 120))
	recipe := func(m *Image) *Image {
		return m.Rotate(90).Flip().Flop().Trim(5).Crop(2, 3, 100, 140).
			Resize(50, 60, ResizeOptions{Fit: FitCover, Kernel: KernelLanczos3}).
			Extend(1, 2, 3, 4, ExtendOptions{Background: color.NRGBA{1, 2, 3, 200}}).Grayscale().Modulate(1.1, 0.9, 30).Blur(1.5).
			Sharpen(SharpenOptions{Sigma: 1, Amount: 2, Threshold: 3}).Sharpen().Rotate(12.5, RotateOptions{Background: color.White}).
			Flatten(color.NRGBA{9, 8, 7, 255}).Pixels(func(*image.NRGBA) error { return nil })
	}
	var seen []Step
	hook := func(step Step, in pipeline.Source, run func(pipeline.Source) (pipeline.Source, error)) (pipeline.Source, error) {
		seen = append(seen, step)
		return run(in)
	}
	withHook, err := recipe(FromBytes(in)).Around(hook).PNG().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"rotate:90", "flip", "flop", "trim:5", "crop:2,3,100,140", "resize:50,60,cover,lanczos3",
		"extend:1,2,3,4,#010203c8", "grayscale", "modulate:1.1,0.9,30", "blur:1.5", "sharpen:1,2,3", "sharpen",
		"rotate:12.5,#ffffff", "flatten:#090807", "pixels"}
	var got []string
	for i, s := range seen {
		got = append(got, s.String())
		if s.Index != i {
			t.Errorf("step %d has Index %d", i, s.Index)
		}
	}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("steps\n got %v\nwant %v", got, want)
	}
	// The same recipe made with Do from the words the hook saw (but the
	// last, a function): the same file; and the same as without hooks.
	m := FromBytes(in)
	for _, s := range seen[:len(seen)-1] {
		m.Do(s.Name, s.Args...)
	}
	viaDo, err := m.Pixels(func(*image.NRGBA) error { return nil }).PNG().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	plain, err := recipe(FromBytes(in)).PNG().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(viaDo, withHook) {
		t.Error("Do with the steps' words made another file")
	}
	// Without hooks a turn before a resize is done after it, on the smaller
	// image; the picture is the same, to rounding.
	if d, _, _ := channelDiff(t, decodePNG(t, plain), decodePNG(t, withHook)); d > 2 {
		t.Errorf("with a hook the picture differs by up to %d", d)
	}
}

// TestAroundChangesSteps: a hook can change the input of a step, its
// result, or do it its own way; hooks nest, the first added outermost.
func TestAroundChangesSteps(t *testing.T) {
	in := pngBytes(t, photoLike(64, 48))
	red := func(src pipeline.Source) pipeline.Source {
		return pipeline.Map(src, func(s *image.NRGBA) error {
			for i := 0; i < len(s.Pix); i += 4 {
				s.Pix[i], s.Pix[i+1], s.Pix[i+2] = 255, 0, 0
			}
			return nil
		})
	}
	// Blur done another way: here, not at all (its input passes through).
	noBlur := func(step Step, in pipeline.Source, run func(pipeline.Source) (pipeline.Source, error)) (pipeline.Source, error) {
		if step.Name == "blur" {
			return in, nil
		}
		return run(in)
	}
	a, err := FromBytes(in).Blur(5).Around(noBlur).PNG().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := FromBytes(in).PNG().ToBytes()
	if d, _, _ := channelDiff(t, decodePNG(t, a), decodePNG(t, b)); d != 0 {
		t.Error("the replaced blur still blurred")
	}
	// After a resize, everything red; the size is the resize's.
	afterResize := func(step Step, in pipeline.Source, run func(pipeline.Source) (pipeline.Source, error)) (pipeline.Source, error) {
		out, err := run(in)
		if err != nil || step.Name != "resize" {
			return out, err
		}
		return red(out), nil
	}
	c, err := FromBytes(in).Resize(20, 0).Around(afterResize).PNG().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	cm := decodePNG(t, c)
	if cm.Bounds().Dx() != 20 || color.RGBAModel.Convert(cm.At(3, 3)) != (color.RGBA{255, 0, 0, 255}) {
		t.Errorf("after the resize: %v, %v", cm.Bounds(), cm.At(3, 3))
	}
	// Nesting: the outer hook sees what the inner one did.
	var order []string
	mark := func(name string) Hook {
		return func(step Step, in pipeline.Source, run func(pipeline.Source) (pipeline.Source, error)) (pipeline.Source, error) {
			order = append(order, name+">")
			out, err := run(in)
			order = append(order, "<"+name)
			return out, err
		}
	}
	if _, err := FromBytes(in).Flip().Around(mark("outer")).Around(mark("inner")).PNG().ToBytes(); err != nil {
		t.Fatal(err)
	}
	if strings.Join(order, " ") != "outer> inner> <inner <outer" {
		t.Errorf("nesting: %v", order)
	}
	// Hooks also see the steps of OutputSize.
	grow := func(step Step, in pipeline.Source, run func(pipeline.Source) (pipeline.Source, error)) (pipeline.Source, error) {
		out, err := run(in)
		if err != nil {
			return nil, err
		}
		return pipeline.Map(out, func(*image.NRGBA) error { return nil }), nil
	}
	if w, h, err := FromBytes(in).Resize(10, 0).Around(grow).OutputSize(); err != nil || w != 10 || h != 8 {
		t.Errorf("OutputSize with a hook: %dx%d, %v", w, h, err)
	}
}

// TestAroundErrors: a hook that fails, runs its step twice, returns
// nothing, or panics, is an error of the run; whatever it had made is
// closed, the memory budget is all given back and no temporary file is
// left; also when the step had already run.
func TestAroundErrors(t *testing.T) {
	in := pngBytes(t, photoLike(300, 200))
	boom := errors.New("boom")
	hooks := map[string]Hook{
		"fails before": func(step Step, in pipeline.Source, run func(pipeline.Source) (pipeline.Source, error)) (pipeline.Source, error) {
			return nil, boom
		},
		"fails after": func(step Step, in pipeline.Source, run func(pipeline.Source) (pipeline.Source, error)) (pipeline.Source, error) {
			if _, err := run(in); err != nil {
				return nil, err
			}
			return nil, boom
		},
		"runs twice": func(step Step, in pipeline.Source, run func(pipeline.Source) (pipeline.Source, error)) (pipeline.Source, error) {
			if _, err := run(in); err != nil {
				return nil, err
			}
			return run(in)
		},
		"returns nothing": func(step Step, in pipeline.Source, run func(pipeline.Source) (pipeline.Source, error)) (pipeline.Source, error) {
			return nil, nil
		},
		"panics": func(step Step, in pipeline.Source, run func(pipeline.Source) (pipeline.Source, error)) (pipeline.Source, error) {
			panic("hook bug")
		},
	}
	for name, h := range hooks {
		for _, at := range []string{"rotate", "blur", "resize"} {
			dir := t.TempDir()
			env := pipeline.NewEnv(2<<20, dir)
			only := func(step Step, in pipeline.Source, run func(pipeline.Source) (pipeline.Source, error)) (pipeline.Source, error) {
				if step.Name == at {
					return h(step, in, run)
				}
				return run(in)
			}
			m := FromBytes(in).Rotate(90).Blur(3).Resize(100, 0).Around(only)
			src, err := m.Stream(env)
			if err == nil {
				_, err = pipeline.Collect(src)
				src.Close()
			}
			if err == nil {
				t.Errorf("%s at %s: no error", name, at)
			}
			if name == "panics" {
				var p *PanicError
				if !errors.As(err, &p) {
					t.Errorf("%s at %s: %v, want a PanicError", name, at, err)
				}
			}
			if u := env.Budget.Used(); u != 0 {
				t.Errorf("%s at %s: %d bytes still reserved", name, at, u)
			}
			if n := tempFiles(dir); n != 0 {
				t.Errorf("%s at %s: %d temporary files left", name, at, n)
			}
		}
	}
	if _, err := FromBytes(in).Around(nil).ToBytes(); err == nil {
		t.Error("Around(nil) accepted")
	}
	// Without hooks: an operation that panics while the chain is set up,
	// after steps that took memory and temporary files.
	explode := OperationFunc(func(pipeline.Source) (pipeline.Source, error) { panic("made a mess") })
	dir := t.TempDir()
	env := pipeline.NewEnv(2<<20, dir)
	_, err := FromBytes(in).Blur(40).Sharpen(SharpenOptions{Sigma: 30}).Pipe(explode).Stream(env)
	var p *PanicError
	if !errors.As(err, &p) {
		t.Errorf("panicking Pipe: %v", err)
	}
	if u := env.Budget.Used(); u != 0 {
		t.Errorf("panicking Pipe: %d bytes still reserved", u)
	}
	if n := tempFiles(dir); n != 0 {
		t.Errorf("panicking Pipe: %d temporary files left", n)
	}
}

// TestRegisterOperation: an operation registered by name is there for Do
// and in Operations; one registered under a built-in name replaces it for
// Do (the method stays), until it is unregistered; names are checked.
func TestRegisterOperation(t *testing.T) {
	in := pngBytes(t, photoLike(64, 48))
	invert := OperationDef{Name: "test-invert", Usage: "[AMOUNT]", Help: "invert the colours",
		New: func(args ...string) (Operation, error) {
			if len(args) > 1 {
				return nil, fmt.Errorf("at most one argument")
			}
			return OperationFunc(func(src pipeline.Source) (pipeline.Source, error) {
				return pipeline.Map(src, func(s *image.NRGBA) error {
					for i := 0; i < len(s.Pix); i += 4 {
						s.Pix[i], s.Pix[i+1], s.Pix[i+2] = 255-s.Pix[i], 255-s.Pix[i+1], 255-s.Pix[i+2]
					}
					return nil
				}), nil
			}), nil
		}}
	if err := RegisterOperation(invert); err != nil {
		t.Fatal(err)
	}
	defer UnregisterOperation("test-invert")
	twice, err := FromBytes(in).Do("test-invert").Do("test-invert").PNG().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	plain, _ := FromBytes(in).PNG().ToBytes()
	if !bytes.Equal(twice, plain) {
		t.Error("inverting twice changed the image")
	}
	if _, err := FromBytes(in).Do("test-invert", "1", "2").ToBytes(); err == nil {
		t.Error("New's error was not the run's")
	}
	if _, err := FromBytes(in).Do("no-such-op").ToBytes(); err == nil {
		t.Error("an unknown name was accepted")
	}
	found := false
	for _, d := range Operations() {
		found = found || d.Name == "test-invert"
	}
	if !found {
		t.Error("Operations does not list the registered one")
	}
	// The hook sees it by its name and arguments.
	var seen string
	FromBytes(in).Do("test-invert").Around(func(step Step, in pipeline.Source, run func(pipeline.Source) (pipeline.Source, error)) (pipeline.Source, error) {
		seen = step.String()
		return run(in)
	}).ToBytes()
	if seen != "test-invert" {
		t.Errorf("the hook saw %q", seen)
	}

	// Replacing a built-in name: Do uses it, Blur does not.
	blurred, _ := FromBytes(in).Blur(4).PNG().ToBytes()
	if err := RegisterOperation(OperationDef{Name: "blur", New: invert.New}); err != nil {
		t.Fatal(err)
	}
	replaced, _ := FromBytes(in).Do("blur", "4").PNG().ToBytes()
	method, _ := FromBytes(in).Blur(4).PNG().ToBytes()
	UnregisterOperation("blur")
	again, _ := FromBytes(in).Do("blur", "4").PNG().ToBytes()
	if bytes.Equal(replaced, blurred) || !bytes.Equal(method, blurred) || !bytes.Equal(again, blurred) {
		t.Error("replacing the name blur: Do, the method or the restored built-in is wrong")
	}

	for _, bad := range []OperationDef{{Name: "", New: invert.New}, {Name: "Upper", New: invert.New}, {Name: "has space", New: invert.New},
		{Name: "9lives", New: invert.New}, {Name: "no-new"}} {
		if RegisterOperation(bad) == nil {
			t.Errorf("%q accepted", bad.Name)
		}
	}
	// Registering while recipes run elsewhere.
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			name := fmt.Sprintf("test-race-%d", i%3)
			RegisterOperation(OperationDef{Name: name, New: invert.New})
			FromBytes(in).Do(name).ToBytes()
			Operations()
			UnregisterOperation(name)
		}()
	}
	wg.Wait()
}

// TestDoBuiltIns: every built-in name makes the same file as its method,
// and bad arguments are errors.
func TestDoBuiltIns(t *testing.T) {
	in := pngBytes(t, testutil.Images(90, 70)["nrgba"])
	cases := []struct {
		name string
		args []string
		m    func(*Image) *Image
	}{
		{"resize", []string{"40", "0"}, func(m *Image) *Image { return m.Resize(40, 0) }},
		{"resize", []string{"40", "40", "contain", "nearest"}, func(m *Image) *Image {
			return m.Resize(40, 40, ResizeOptions{Fit: FitContain, Kernel: KernelNearest})
		}},
		{"crop", []string{"1", "2", "30", "20"}, func(m *Image) *Image { return m.Crop(1, 2, 30, 20) }},
		{"rotate", []string{"auto"}, func(m *Image) *Image { return m.AutoOrient() }},
		{"rotate", []string{"270"}, func(m *Image) *Image { return m.Rotate(270) }},
		{"rotate", []string{"33", "transparent"}, func(m *Image) *Image { return m.Rotate(33, RotateOptions{Background: color.Transparent}) }},
		{"flip", nil, func(m *Image) *Image { return m.Flip() }},
		{"flop", nil, func(m *Image) *Image { return m.Flop() }},
		{"trim", nil, func(m *Image) *Image { return m.Trim(10) }},
		{"extend", []string{"1", "2", "3", "4", "#0f0"}, func(m *Image) *Image {
			return m.Extend(1, 2, 3, 4, ExtendOptions{Background: color.RGBA{0, 255, 0, 255}})
		}},
		{"grayscale", nil, func(m *Image) *Image { return m.Grayscale() }},
		{"modulate", []string{"1.2", "0.5", "-40"}, func(m *Image) *Image { return m.Modulate(1.2, 0.5, -40) }},
		{"blur", []string{"0.8"}, func(m *Image) *Image { return m.Blur(0.8) }},
		{"blur", []string{"1.5", "libvips"}, func(m *Image) *Image { return m.Blur(1.5, BlurOptions{Method: MethodLibvips}) }},
		{"sharpen", nil, func(m *Image) *Image { return m.Sharpen() }},
		{"sharpen", []string{"2", "1", "4"}, func(m *Image) *Image { return m.Sharpen(SharpenOptions{Sigma: 2, Amount: 1, Threshold: 4}) }},
		{"flatten", []string{"white"}, func(m *Image) *Image { return m.Flatten(color.White) }},
	}
	for _, c := range cases {
		a, err := FromBytes(in).Do(c.name, c.args...).PNG().ToBytes()
		if err != nil {
			t.Fatalf("%s %v: %v", c.name, c.args, err)
		}
		b, err := c.m(FromBytes(in)).PNG().ToBytes()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(a, b) {
			t.Errorf("Do(%s, %v) and the method made other files", c.name, c.args)
		}
		// The built-in's New, as an Operation of its own, does the same.
		d, _ := LookupOperation(c.name)
		op, err := d.New(c.args...)
		if err != nil {
			t.Fatalf("New of %s %v: %v", c.name, c.args, err)
		}
		if p, err := FromBytes(in).Pipe(op).PNG().ToBytes(); err != nil || !bytes.Equal(p, b) {
			t.Errorf("the New of %s %v made another file (%v)", c.name, c.args, err)
		}
	}
	for _, bad := range [][]string{{"resize", "40"}, {"resize", "a", "1"}, {"resize", "4", "4", "squash"}, {"crop", "1", "2"},
		{"rotate"}, {"rotate", "x"}, {"rotate", "NaN"}, {"flip", "1"}, {"extend", "1", "2", "3", "4", "pink"}, {"blur"}, {"blur", "Inf"},
		{"sharpen", "1", "1", "1.5"}, {"modulate", "1", "1"}, {"flatten"}, {"trim", "1", "2"}} {
		if _, err := FromBytes(in).Do(bad[0], bad[1:]...).ToBytes(); err == nil {
			t.Errorf("Do%v accepted", bad)
		}
		if d, _ := LookupOperation(bad[0]); d.New == nil {
			t.Errorf("%s has no New", bad[0])
		} else if op, err := d.New(bad[1:]...); err == nil || op != nil {
			t.Errorf("New%v accepted", bad)
		}
	}
	for _, d := range Operations() {
		if d.Name == "" || d.Help == "" {
			t.Errorf("operation %q without help", d.Name)
		}
	}
}

// TestDoWordsNeverPanic: every built-in name, given up to three of a few
// words in any order, makes a step or an error, never a panic.
func TestDoWordsNeverPanic(t *testing.T) {
	words := []string{"0", "1", "2.5", "-3", "300", "x", "NaN", "kernel", "alpha", "channels", "linear", "lineart",
		"white", "#0f08", "contain", "copy"}
	for _, d := range Operations() {
		var args []string
		var try func(n int)
		try = func(n int) {
			defer func() {
				if p := recover(); p != nil {
					t.Fatalf("%s %q: panic %v", d.Name, args, p)
				}
			}()
			if d.New != nil {
				d.New(args...)
			}
			FromBytes(nil).Do(d.Name, args...)
			for i := 0; n > 0 && i < len(words); i++ {
				args = append(args, words[i])
				try(n - 1)
				args = args[:len(args)-1]
			}
		}
		try(3)
	}
}

// TestParseColor reads every form and refuses the rest.
func TestParseColor(t *testing.T) {
	for s, want := range map[string]color.Color{
		"white": color.White, "BLACK": color.Black, "none": color.Transparent, "#f00": color.RGBA{255, 0, 0, 255},
		"#102030": color.RGBA{16, 32, 48, 255}, "10203080": color.NRGBA{16, 32, 48, 128},
	} {
		got, err := ParseColor(s)
		if err != nil || got != want {
			t.Errorf("%q: %v, %v; want %v", s, got, err, want)
		}
	}
	for _, s := range []string{"", "pink", "#12", "#12345", "#1234567", "#gggggg", "#123456789"} {
		if _, err := ParseColor(s); err == nil {
			t.Errorf("%q accepted", s)
		}
	}
}

// TestWrapBuiltIn: an operation registered under a built-in name can do the
// built-in one and add to it, and "rotate auto" made by New turns by the
// orientation of the file when it is the first step.
func TestWrapBuiltIn(t *testing.T) {
	in := pngBytes(t, photoLike(64, 48))
	blur, _ := LookupOperation("blur")
	err := RegisterOperation(OperationDef{Name: "blur", Usage: "SIGMA", Help: "blur, at most 1",
		New: func(args ...string) (Operation, error) {
			if len(args) == 1 && args[0] > "1" {
				args = []string{"1"}
			}
			return blur.New(args...)
		}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := FromBytes(in).Do("blur", "9").PNG().ToBytes()
	UnregisterOperation("blur")
	if err != nil {
		t.Fatal(err)
	}
	want, _ := FromBytes(in).Blur(1).PNG().ToBytes()
	if !bytes.Equal(got, want) {
		t.Error("the wrapped blur is not Blur(1)")
	}

	plain := jpegBytes(testutil.Images(60, 40)["rgba-opaque"], 92)
	jpg := withAPP(plain, app(0xe1, append([]byte("Exif\x00\x00"), exifWithOrientation(binary.BigEndian, 6)...)))
	auto, _ := LookupOperation("rotate")
	op, err := auto.New("auto")
	if err != nil {
		t.Fatal(err)
	}
	a, err := FromBytes(jpg).Pipe(op).PNG().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := FromBytes(jpg).AutoOrient().PNG().ToBytes()
	if !bytes.Equal(a, b) {
		t.Error("rotate auto made by New does not turn by the file's orientation")
	}
}
