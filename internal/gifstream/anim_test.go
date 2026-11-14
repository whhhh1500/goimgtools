package gifstream

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"io"
	"math/rand"
	"reflect"
	"sort"
	"testing"

	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// copyAnimation is the pipeline under test: every frame of data read with
// Animation and written with AnimWriter, strip by strip.
func copyAnimation(data []byte, env *pipeline.Env) ([]byte, error) {
	a, err := OpenAnimation(bytes.NewReader(data), env)
	if err != nil {
		return nil, err
	}
	defer a.Close()
	var out bytes.Buffer
	aw, err := NewAnimWriter(&out, a.Config())
	if err != nil {
		return nil, err
	}
	for {
		src, info, err := a.NextFrame()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if err := aw.WriteFrame(src, info); err != nil {
			return nil, err
		}
	}
	if err := aw.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func encodeAll(t testing.TB, g *gif.GIF) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, g); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// sameGIF compares what gif.DecodeAll makes of two files: frames (bounds,
// pixels and, if palettes is set, palettes), delays, disposals, loop count, screen, background.
func sameGIF(want, got []byte, palettes bool) error {
	w, err := gif.DecodeAll(bytes.NewReader(want))
	if err != nil {
		return fmt.Errorf("reference does not decode: %v", err)
	}
	g, err := gif.DecodeAll(bytes.NewReader(got))
	if err != nil {
		return fmt.Errorf("output does not decode: %v", err)
	}
	if len(w.Image) != len(g.Image) {
		return fmt.Errorf("%d frames, want %d", len(g.Image), len(w.Image))
	}
	for i := range w.Image {
		if w.Image[i].Rect != g.Image[i].Rect {
			return fmt.Errorf("frame %d: bounds %v, want %v", i, g.Image[i].Rect, w.Image[i].Rect)
		}
		if palettes && !reflect.DeepEqual(w.Image[i].Palette, g.Image[i].Palette) {
			return fmt.Errorf("frame %d: palette differs", i)
		}
		if !bytes.Equal(w.Image[i].Pix, g.Image[i].Pix) || w.Image[i].Stride != g.Image[i].Stride {
			return fmt.Errorf("frame %d: pixels differ", i)
		}
	}
	if !reflect.DeepEqual(w.Delay, g.Delay) {
		return fmt.Errorf("delays %v, want %v", g.Delay, w.Delay)
	}
	if !reflect.DeepEqual(w.Disposal, g.Disposal) {
		return fmt.Errorf("disposals %v, want %v", g.Disposal, w.Disposal)
	}
	if w.LoopCount != g.LoopCount {
		return fmt.Errorf("loop count %d, want %d", g.LoopCount, w.LoopCount)
	}
	if w.BackgroundIndex != g.BackgroundIndex {
		return fmt.Errorf("background index %d, want %d", g.BackgroundIndex, w.BackgroundIndex)
	}
	if !reflect.DeepEqual(w.Config, g.Config) {
		return fmt.Errorf("config differs")
	}
	return nil
}

func withTransparent(p color.Palette, i int) color.Palette {
	q := append(color.Palette(nil), p...)
	q[i] = color.RGBA{}
	return q
}

// animations builds the test animations. interlaced lists, per animation,
// the frames that are to be stored interlaced.
func animations() (gifs map[string]*gif.GIF, interlaced map[string][]int) {
	gifs = map[string]*gif.GIF{}

	// Every frame covers the screen and uses the global palette.
	global := testPalette(256, false, 1)
	g := &gif.GIF{LoopCount: 0, Config: image.Config{ColorModel: global, Width: 60, Height: 40}}
	for i := 0; i < 5; i++ {
		g.Image = append(g.Image, testImage(image.Rect(0, 0, 60, 40), global, int64(i)))
		g.Delay = append(g.Delay, 3*i)
		g.Disposal = append(g.Disposal, 0)
	}
	gifs["global"] = g

	// Frames of different rectangles, every disposal method, some frames
	// with a transparent color (a copy of the global palette with one
	// entry cleared, which is what gif.DecodeAll hands out).
	global = testPalette(64, false, 2)
	g = &gif.GIF{LoopCount: 3, BackgroundIndex: 7, Config: image.Config{ColorModel: global, Width: 100, Height: 80}}
	rects := []image.Rectangle{
		image.Rect(0, 0, 100, 80), image.Rect(10, 20, 50, 61), image.Rect(99, 79, 100, 80),
		image.Rect(0, 30, 100, 31), image.Rect(37, 0, 38, 80), image.Rect(5, 5, 95, 75), image.Rect(60, 10, 100, 80),
	}
	for i, r := range rects {
		pal := global
		if i%3 == 1 {
			pal = withTransparent(global, 5+i)
		}
		g.Image = append(g.Image, testImage(r, pal, int64(10+i)))
		g.Delay = append(g.Delay, []int{0, 1, 10, 65535}[i%4])
		g.Disposal = append(g.Disposal, []byte{0, gif.DisposalNone, gif.DisposalBackground, gif.DisposalPrevious}[i%4])
	}
	gifs["rects"] = g

	// No global palette: every frame has a local one, of various sizes.
	g = &gif.GIF{LoopCount: -1, Config: image.Config{Width: 50, Height: 70}}
	for i, n := range []int{2, 16, 37, 256, 1, 100} {
		g.Image = append(g.Image, testImage(image.Rect(i, 2*i, 50-i, 70-i), testPalette(n, i%2 == 1, 20+i), int64(20+i)))
		g.Delay = append(g.Delay, 7)
		g.Disposal = append(g.Disposal, gif.DisposalBackground)
	}
	gifs["local"] = g

	// Global and local palettes mixed, and frames tall enough to arrive in
	// several strips.
	global = testPalette(128, false, 3)
	g = &gif.GIF{LoopCount: 65535, BackgroundIndex: 127, Config: image.Config{ColorModel: global, Width: 300, Height: 700}}
	for i := 0; i < 6; i++ {
		pal := global
		switch i % 3 {
		case 1:
			pal = testPalette(256, true, 30+i)
		case 2:
			pal = withTransparent(global, 100)
		}
		r := image.Rect(0, 0, 300, 700)
		if i >= 3 {
			r = image.Rect(11, 13, 289, 650)
		}
		g.Image = append(g.Image, testImage(r, pal, int64(30+i)))
		g.Delay = append(g.Delay, 2)
		g.Disposal = append(g.Disposal, byte(i%4))
	}
	gifs["mixed"] = g

	// A single frame is an animation too.
	global = testPalette(16, true, 4)
	gifs["single"] = &gif.GIF{
		Image: []*image.Paletted{testImage(image.Rect(3, 4, 30, 20), global, 40)}, Delay: []int{9}, Disposal: []byte{gif.DisposalNone},
		LoopCount: -1, Config: image.Config{ColorModel: global, Width: 31, Height: 21},
	}

	interlaced = map[string][]int{"rects": {0, 1, 3, 5}, "mixed": {1, 2, 3}, "local": {0, 1, 2, 3, 4, 5}, "single": {0}}
	return gifs, interlaced
}

// imageDescriptors returns the offset of every image descriptor in data.
func imageDescriptors(t testing.TB, data []byte) []int {
	var descs []int
	p := 13
	if data[10]&fColorTable != 0 {
		p += 3 << (1 + data[10]&fColorTableBitsMask)
	}
	skipBlocks := func() {
		for data[p] != 0 {
			p += 1 + int(data[p])
		}
		p++
	}
	for {
		switch data[p] {
		case sImageDescriptor:
			descs = append(descs, p)
			fields := data[p+9]
			p += 10
			if fields&fColorTable != 0 {
				p += 3 << (1 + fields&fColorTableBitsMask)
			}
			p++ // LZW code size
			skipBlocks()
		case sExtension:
			p += 2
			skipBlocks()
		case sTrailer:
			return descs
		default:
			t.Fatalf("unknown block 0x%02x at %d", data[p], p)
		}
	}
}

// interlacedFile encodes g with the listed frames stored interlaced, which
// the standard encoder cannot do itself: their rows are reordered before
// encoding and the interlace flag is set afterwards.
func interlacedFile(t testing.TB, g *gif.GIF, frames []int) []byte {
	h := *g
	h.Image = append([]*image.Paletted(nil), g.Image...)
	for _, i := range frames {
		h.Image[i] = interlaceRows(g.Image[i])
	}
	data := encodeAll(t, &h)
	descs := imageDescriptors(t, data)
	if len(descs) != len(g.Image) {
		t.Fatalf("found %d image descriptors, want %d", len(descs), len(g.Image))
	}
	for _, i := range frames {
		data[descs[i]+9] |= fInterlace
	}
	return data
}

func sortedGIFs(gifs map[string]*gif.GIF) []string {
	var names []string
	for name := range gifs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// TestAnimRoundTrip reads files written by gif.EncodeAll and writes them
// again. The result must decode to the same animation and, beyond that, be
// the same bytes.
func TestAnimRoundTrip(t *testing.T) {
	gifs, interlaced := animations()
	for _, name := range sortedGIFs(gifs) {
		want := encodeAll(t, gifs[name])
		inputs := map[string][]byte{"plain": want}
		if frames := interlaced[name]; frames != nil {
			inputs["interlaced"] = interlacedFile(t, gifs[name], frames)
			if err := sameGIF(want, inputs["interlaced"], true); err != nil {
				t.Fatalf("%s: interlaced test file is wrong: %v", name, err)
			}
		}
		for kind, data := range inputs {
			for _, limit := range []int64{0, 1 << 20} {
				dir := t.TempDir()
				env := pipeline.NewEnv(limit, dir)
				got, err := copyAnimation(data, env)
				if err != nil {
					t.Errorf("%s %s limit %d: %v", name, kind, limit, err)
					continue
				}
				if err := sameGIF(want, got, true); err != nil {
					t.Errorf("%s %s limit %d: %v", name, kind, limit, err)
				}
				if !bytes.Equal(got, want) {
					t.Errorf("%s %s limit %d: %d bytes differ from the %d of gif.EncodeAll", name, kind, limit, len(got), len(want))
				}
				if env.Budget.Used() != 0 {
					t.Errorf("%s %s: %d bytes still reserved", name, kind, env.Budget.Used())
				}
				if left := dirEntries(t, dir); len(left) != 0 {
					t.Errorf("%s %s: temp files left behind: %v", name, kind, left)
				}
			}
		}
	}
}

// TestAnimReader checks what Animation reports against gif.DecodeAll,
// frame by frame, including the strip contract of every frame.
func TestAnimReader(t *testing.T) {
	gifs, interlaced := animations()
	for _, name := range sortedGIFs(gifs) {
		data := encodeAll(t, gifs[name])
		if frames := interlaced[name]; frames != nil {
			data = interlacedFile(t, gifs[name], frames)
		}
		want, err := gif.DecodeAll(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		env := pipeline.NewEnv(0, t.TempDir())
		a, err := OpenAnimation(bytes.NewReader(data), env)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		cfg := a.Config()
		if cfg.Width != want.Config.Width || cfg.Height != want.Config.Height || cfg.LoopCount != want.LoopCount ||
			cfg.BackgroundIndex != want.BackgroundIndex || !reflect.DeepEqual(cfg.GlobalPalette, want.Config.ColorModel) {
			t.Errorf("%s: config %+v", name, cfg)
		}
		for i := 0; ; i++ {
			src, info, err := a.NextFrame()
			if err == io.EOF {
				if i != len(want.Image) {
					t.Errorf("%s: %d frames, want %d", name, i, len(want.Image))
				}
				break
			}
			if err != nil {
				t.Fatalf("%s frame %d: %v", name, i, err)
			}
			if i >= len(want.Image) {
				t.Fatalf("%s: more than %d frames", name, len(want.Image))
			}
			m, _, err := collect(src)
			if err != nil {
				t.Fatalf("%s frame %d: %v", name, i, err)
			}
			if err := same(want.Image[i], m); err != nil {
				t.Errorf("%s frame %d: %v", name, i, err)
			}
			if info.Delay != want.Delay[i] || info.Disposal != want.Disposal[i] {
				t.Errorf("%s frame %d: delay %d disposal %d, want %d and %d", name, i, info.Delay, info.Disposal, want.Delay[i], want.Disposal[i])
			}
			ti, _ := transparentIndexOf(want.Image[i].Palette)
			if info.Transparent != (ti >= 0) || (info.Transparent && int(info.TransparentIndex) != ti) {
				t.Errorf("%s frame %d: transparent %t index %d, want index %d", name, i, info.Transparent, info.TransparentIndex, ti)
			}
			if pipeline.IsOpaque(src) != (ti < 0) {
				t.Errorf("%s frame %d: opaque %t", name, i, pipeline.IsOpaque(src))
			}
		}
		if _, _, err := a.NextFrame(); err != io.EOF {
			t.Errorf("%s: NextFrame after io.EOF: %v", name, err)
		}
		a.Close()
		if env.Budget.Used() != 0 {
			t.Errorf("%s: %d bytes still reserved", name, env.Budget.Used())
		}
	}
}

// TestAnimWriterMatchesStdlib writes the frames of a *gif.GIF in strips of
// awkward heights and requires the bytes of gif.EncodeAll.
func TestAnimWriterMatchesStdlib(t *testing.T) {
	gifs, _ := animations()
	for _, name := range sortedGIFs(gifs) {
		g := gifs[name]
		want := encodeAll(t, g)
		for _, n := range []int{1, 7, 300} {
			env := pipeline.NewEnv(0, t.TempDir())
			var got bytes.Buffer
			global, _ := g.Config.ColorModel.(color.Palette)
			aw, err := NewAnimWriter(&got, AnimConfig{
				Width: g.Config.Width, Height: g.Config.Height, LoopCount: g.LoopCount,
				BackgroundIndex: g.BackgroundIndex, GlobalPalette: global,
			})
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			for i, m := range g.Image {
				if err := aw.WriteFrame(testutil.ChoppedEnv(m, n, env), FrameInfo{Delay: g.Delay[i], Disposal: g.Disposal[i]}); err != nil {
					t.Fatalf("%s frame %d: %v", name, i, err)
				}
			}
			if err := aw.Close(); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if !bytes.Equal(got.Bytes(), want) {
				t.Errorf("%s strips=%d: %d bytes differ from the %d of gif.EncodeAll", name, n, got.Len(), len(want))
			}
			if env.Budget.Peak() != 0 {
				t.Errorf("%s: writer reserved %d bytes", name, env.Budget.Peak())
			}
		}
	}
}

// TestAnimLoopCountSingleFrame pins the one known difference from
// gif.EncodeAll: it drops the loop count of a single-frame file, AnimWriter
// keeps it.
func TestAnimLoopCountSingleFrame(t *testing.T) {
	gifs, _ := animations()
	g := *gifs["single"]
	g.LoopCount = 4
	std := encodeAll(t, &g)
	var got bytes.Buffer
	aw, err := NewAnimWriter(&got, AnimConfig{Width: g.Config.Width, Height: g.Config.Height, LoopCount: 4, GlobalPalette: g.Config.ColorModel.(color.Palette)})
	if err != nil {
		t.Fatal(err)
	}
	if err := aw.WriteFrame(testutil.Chopped(g.Image[0], 5), FrameInfo{Delay: 9, Disposal: gif.DisposalNone}); err != nil {
		t.Fatal(err)
	}
	if err := aw.Close(); err != nil {
		t.Fatal(err)
	}
	if got.Len() != len(std)+19 {
		t.Errorf("%d bytes, want the %d of gif.EncodeAll plus a 19-byte looping extension", got.Len(), len(std))
	}
	d, err := gif.DecodeAll(&got)
	if err != nil {
		t.Fatal(err)
	}
	if d.LoopCount != 4 || len(d.Image) != 1 || !bytes.Equal(d.Image[0].Pix, g.Image[0].Pix) {
		t.Errorf("loop count %d, %d frames", d.LoopCount, len(d.Image))
	}
}

// TestAnimTruncated cuts files short: at every length near the start, the
// end and each frame's image descriptor, and at many lengths in between.
// The pipeline must fail exactly where gif.DecodeAll fails.
func TestAnimTruncated(t *testing.T) {
	gifs, interlaced := animations()
	rnd := rand.New(rand.NewSource(5))
	for _, name := range []string{"rects", "local", "single"} {
		for _, data := range [][]byte{encodeAll(t, gifs[name]), interlacedFile(t, gifs[name], interlaced[name])} {
			env := pipeline.NewEnv(0, t.TempDir())
			cuts := append(cutPoints(len(data), 300, 300, rnd), len(data))
			for _, d := range imageDescriptors(t, data) {
				for n := max(d-40, 0); n < min(d+40, len(data)); n++ {
					cuts = append(cuts, n)
				}
			}
			failed := 0
			for _, n := range cuts {
				_, wantErr := gif.DecodeAll(bytes.NewReader(data[:n]))
				_, err := copyAnimation(data[:n], env)
				if (err != nil) != (wantErr != nil) {
					t.Fatalf("%s cut at %d of %d: error %v, gif.DecodeAll: %v", name, n, len(data), err, wantErr)
				}
				if wantErr != nil {
					failed++
				}
				if env.Budget.Used() != 0 {
					t.Fatalf("%s cut at %d: %d bytes still reserved", name, n, env.Budget.Used())
				}
			}
			t.Logf("%s: %d cut points, gif.DecodeAll failed at %d", name, len(cuts), failed)
		}
	}
}

// TestAnimCorrupted damages files at random places. Whether the result is
// an error must agree with gif.DecodeAll, and so must what is decoded when
// it is not.
func TestAnimCorrupted(t *testing.T) {
	gifs, interlaced := animations()
	rnd := rand.New(rand.NewSource(5))
	for _, name := range []string{"rects", "local", "single", "global"} {
		files := [][]byte{encodeAll(t, gifs[name])}
		if frames := interlaced[name]; frames != nil {
			files = append(files, interlacedFile(t, gifs[name], frames))
		}
		for _, data := range files {
			env := pipeline.NewEnv(0, t.TempDir())
			failed, refused, same, padded := 0, 0, 0, 0
			for i := 0; i < 500; i++ {
				bad := append([]byte(nil), data...)
				at := rnd.Intn(len(bad))
				bad[at] ^= byte(1 << rnd.Intn(8))
				ref, wantErr := gif.DecodeAll(bytes.NewReader(bad))
				got, err := copyAnimation(bad, env)
				if env.Budget.Used() != 0 {
					t.Fatalf("%s byte %d: %d bytes still reserved", name, at, env.Budget.Used())
				}
				if wantErr != nil {
					if err == nil {
						t.Errorf("%s byte %d: no error, gif.DecodeAll: %v", name, at, wantErr)
					}
					failed++
					continue
				}
				if err != nil {
					// The writer refuses a few things the decoder lets
					// through; they are checked on the reading side below.
					if rerr := readAnimation(bad, env); rerr != nil {
						t.Errorf("%s byte %d: reading fails: %v, gif.DecodeAll is fine", name, at, rerr)
					}
					t.Logf("%s byte %d: writer refuses: %v", name, at, err)
					refused++
					continue
				}
				// What gif.EncodeAll makes of the decoded file is the
				// reference: the same encoder, fed all at once.
				var want bytes.Buffer
				if err := gif.EncodeAll(&want, ref); err != nil {
					t.Errorf("%s byte %d: gif.EncodeAll refuses (%v) what AnimWriter wrote", name, at, err)
					continue
				}
				if bytes.Equal(got, want.Bytes()) {
					same++
					continue
				}
				// The one way to get here: the damage moved a frame's
				// transparent index outside its color table. The decoder
				// then pads the palette with transparent entries up to
				// that index. gif.EncodeAll makes the first of them the
				// transparent index; AnimWriter keeps the file's own, so
				// the two palettes differ, and nothing else does.
				if !paddedPalette(ref) {
					t.Errorf("%s byte %d: bytes differ from gif.EncodeAll", name, at)
				}
				if err := sameGIF(want.Bytes(), got, false); err != nil {
					t.Errorf("%s byte %d: %v", name, at, err)
				}
				padded++
			}
			t.Logf("%s: gif.DecodeAll failed %d times; of the rest %d identical to gif.EncodeAll, %d with a padded palette, %d refused by the writer",
				name, failed, same, padded, refused)
		}
	}
}

// paddedPalette reports whether a frame has more than one transparent
// palette entry, which is what a transparent index outside the color table
// decodes to.
func paddedPalette(g *gif.GIF) bool {
	for _, m := range g.Image {
		n := 0
		for _, c := range m.Palette {
			if _, _, _, a := c.RGBA(); a == 0 {
				n++
			}
		}
		if n > 1 {
			return true
		}
	}
	return false
}

// readAnimation reads every frame of data and discards it.
func readAnimation(data []byte, env *pipeline.Env) error {
	a, err := OpenAnimation(bytes.NewReader(data), env)
	if err != nil {
		return err
	}
	defer a.Close()
	for {
		src, _, err := a.NextFrame()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if _, _, err := collect(src); err != nil {
			return err
		}
	}
}

// TestAnimSkipFrames: a frame that is not read, read in part, or closed
// early does not disturb the frames after it.
func TestAnimSkipFrames(t *testing.T) {
	gifs, interlaced := animations()
	for _, name := range []string{"mixed", "rects"} {
		data := interlacedFile(t, gifs[name], interlaced[name])
		want, err := gif.DecodeAll(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		for mode := 0; mode < 4; mode++ {
			env := pipeline.NewEnv(0, t.TempDir())
			a, err := OpenAnimation(bytes.NewReader(data), env)
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; ; i++ {
				src, _, err := a.NextFrame()
				if err == io.EOF {
					if i != len(want.Image) {
						t.Errorf("%s mode %d: %d frames", name, mode, i)
					}
					break
				}
				if err != nil {
					t.Fatalf("%s mode %d frame %d: %v", name, mode, i, err)
				}
				if i%2 == 1 {
					m, _, err := collect(src)
					if err != nil {
						t.Fatalf("%s mode %d frame %d: %v", name, mode, i, err)
					}
					if err := same(want.Image[i], m); err != nil {
						t.Errorf("%s mode %d frame %d: %v", name, mode, i, err)
					}
					continue
				}
				switch mode {
				case 0: // not touched
				case 1: // one strip, then left alone
					if _, err := src.Next(); err != nil {
						t.Fatal(err)
					}
				case 2: // closed unread
					src.Close()
				case 3: // one strip, then closed
					if _, err := src.Next(); err != nil {
						t.Fatal(err)
					}
					src.Close()
					if m, err := src.Next(); err == nil {
						t.Errorf("Next after Close returns a strip %v", m.Bounds())
					}
				}
			}
			a.Close()
			if env.Budget.Used() != 0 {
				t.Errorf("%s mode %d: %d bytes still reserved", name, mode, env.Budget.Used())
			}
		}
	}
}

// TestAnimMemory: the budget's peak is one frame's strip (and the pass
// buffers of an interlaced frame), however many frames there are.
func TestAnimMemory(t *testing.T) {
	const w, h, frames = 500, 400, 40
	pal := testPalette(256, false, 1)
	g := &gif.GIF{Config: image.Config{ColorModel: pal, Width: w, Height: h}}
	for i := 0; i < frames; i++ {
		g.Image = append(g.Image, testImage(image.Rect(0, 0, w, h), pal, int64(i)))
		g.Delay = append(g.Delay, 1)
		g.Disposal = append(g.Disposal, 0)
	}
	data := encodeAll(t, g)
	env := pipeline.NewEnv(0, t.TempDir())
	got, err := copyAnimation(data, env)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("bytes differ from gif.EncodeAll")
	}
	if peak, limit := env.Budget.Peak(), int64(w*pipeline.StripRows(w, h)); peak > limit {
		t.Errorf("peak %d bytes, want at most one strip (%d); all frames are %d", peak, limit, w*h*frames)
	}
	if env.Budget.Used() != 0 {
		t.Errorf("%d bytes still reserved", env.Budget.Used())
	}
}

func TestAnimWriterErrors(t *testing.T) {
	pal := testPalette(16, false, 1)
	big := make(color.Palette, 257)
	for i := range big {
		big[i] = color.RGBA{uint8(i), 0, 0, 0xff}
	}
	withNil := testPalette(16, false, 1)
	withNil[2] = nil
	for name, cfg := range map[string]AnimConfig{
		"zero width":         {Width: 0, Height: 5, GlobalPalette: pal},
		"zero height":        {Width: 5, Height: 0, GlobalPalette: pal},
		"negative":           {Width: -1, Height: 5},
		"too wide":           {Width: 65536, Height: 5},
		"too tall":           {Width: 5, Height: 65536},
		"loop count":         {Width: 5, Height: 5, LoopCount: 65536},
		"big global palette": {Width: 5, Height: 5, GlobalPalette: big},
		"nil global entry":   {Width: 5, Height: 5, GlobalPalette: withNil},
	} {
		if _, err := NewAnimWriter(io.Discard, cfg); err == nil {
			t.Errorf("%s: no error", name)
		}
	}

	pm := testImage(image.Rect(2, 3, 22, 13), pal, 1)
	top := pm.SubImage(image.Rect(2, 3, 22, 7))
	rest := *pm.SubImage(image.Rect(2, 7, 22, 13)).(*image.Paletted)
	rest.Palette = testPalette(16, false, 2)
	bigPM := image.NewPaletted(pm.Rect, big)
	nilPM := image.NewPaletted(pm.Rect, withNil)
	emptyPM := image.NewPaletted(pm.Rect, nil)
	rgba := image.NewRGBA(pm.Rect)
	errSrc := errors.New("source failed")
	cases := []struct {
		name string
		src  *fakeSource
		info FrameInfo
		want error
	}{
		{"out of bounds", &fakeSource{bounds: image.Rect(20, 20, 41, 30), strips: []image.Image{pm}}, FrameInfo{}, nil},
		{"negative origin", &fakeSource{bounds: image.Rect(-1, 0, 5, 5), strips: []image.Image{pm}}, FrameInfo{}, nil},
		{"no strips", &fakeSource{bounds: pm.Rect}, FrameInfo{}, io.ErrUnexpectedEOF},
		{"ends early", &fakeSource{bounds: pm.Rect, strips: []image.Image{top}}, FrameInfo{}, io.ErrUnexpectedEOF},
		{"source fails", &fakeSource{bounds: pm.Rect, strips: []image.Image{top}, err: errSrc}, FrameInfo{}, errSrc},
		{"palette changes", &fakeSource{bounds: pm.Rect, strips: []image.Image{top, &rest}}, FrameInfo{}, errPaletteChanged},
		{"not paletted", &fakeSource{bounds: pm.Rect, strips: []image.Image{rgba}}, FrameInfo{}, nil},
		{"type changes", &fakeSource{bounds: pm.Rect, strips: []image.Image{top, rgba.SubImage(image.Rect(2, 7, 22, 13))}}, FrameInfo{}, nil},
		{"big palette", &fakeSource{bounds: pm.Rect, strips: []image.Image{bigPM}}, FrameInfo{}, nil},
		{"nil palette entry", &fakeSource{bounds: pm.Rect, strips: []image.Image{nilPM}}, FrameInfo{}, nil},
		{"empty palette", &fakeSource{bounds: pm.Rect, strips: []image.Image{emptyPM}}, FrameInfo{}, nil},
		{"gap", &fakeSource{bounds: pm.Rect, strips: []image.Image{&rest}}, FrameInfo{}, nil},
		{"delay", &fakeSource{bounds: pm.Rect, strips: []image.Image{pm}}, FrameInfo{Delay: 65536}, nil},
		{"negative delay", &fakeSource{bounds: pm.Rect, strips: []image.Image{pm}}, FrameInfo{Delay: -1}, nil},
		{"disposal", &fakeSource{bounds: pm.Rect, strips: []image.Image{pm}}, FrameInfo{Disposal: 8}, nil},
		{"transparent index", &fakeSource{bounds: pm.Rect, strips: []image.Image{pm}}, FrameInfo{Transparent: true, TransparentIndex: 16}, nil},
		{"empty frame without palette", &fakeSource{bounds: image.Rect(3, 3, 3, 9), model: color.RGBAModel}, FrameInfo{}, nil},
	}
	for _, c := range cases {
		aw, err := NewAnimWriter(io.Discard, AnimConfig{Width: 40, Height: 30, GlobalPalette: pal})
		if err != nil {
			t.Fatal(err)
		}
		err = func() (err error) {
			defer func() {
				if r := recover(); r != nil {
					err = nil
					t.Errorf("%s: panic: %v", c.name, r)
				}
			}()
			return aw.WriteFrame(c.src, c.info)
		}()
		if err == nil {
			t.Errorf("%s: no error", c.name)
			continue
		}
		if c.want != nil && !errors.Is(err, c.want) {
			t.Errorf("%s: error %v, want %v", c.name, err, c.want)
		}
		// The writer stays failed.
		if err2 := aw.WriteFrame(&fakeSource{bounds: pm.Rect, strips: []image.Image{pm}}, FrameInfo{}); err2 == nil {
			t.Errorf("%s: WriteFrame after an error succeeds", c.name)
		}
		if aw.Close() == nil {
			t.Errorf("%s: Close after an error succeeds", c.name)
		}
	}

	// No frames at all, as gif.EncodeAll.
	aw, _ := NewAnimWriter(io.Discard, AnimConfig{Width: 40, Height: 30, GlobalPalette: pal})
	if aw.Close() == nil {
		t.Errorf("Close without frames succeeds")
	}
	// WriteFrame after Close.
	aw, _ = NewAnimWriter(io.Discard, AnimConfig{Width: 40, Height: 30, GlobalPalette: pal})
	if err := aw.WriteFrame(&fakeSource{bounds: pm.Rect, strips: []image.Image{pm}}, FrameInfo{}); err != nil {
		t.Fatal(err)
	}
	if err := aw.Close(); err != nil {
		t.Fatal(err)
	}
	if aw.WriteFrame(&fakeSource{bounds: pm.Rect, strips: []image.Image{pm}}, FrameInfo{}) == nil {
		t.Errorf("WriteFrame after Close succeeds")
	}
	// A failing writer.
	for _, n := range []int{0, 30, 100} {
		aw, err := NewAnimWriter(&failWriter{n: n}, AnimConfig{Width: 40, Height: 30, GlobalPalette: pal})
		if err == nil {
			if err = aw.WriteFrame(&fakeSource{bounds: pm.Rect, strips: []image.Image{pm}}, FrameInfo{}); err == nil {
				err = aw.Close()
			}
		}
		if !errors.Is(err, errWrite) {
			t.Errorf("writer failing after %d bytes: %v", n, err)
		}
	}
}

// TestAnimEmptyFrame: a frame without pixels, which gif.DecodeAll accepts
// and gif.EncodeAll writes, goes through.
func TestAnimEmptyFrame(t *testing.T) {
	pal := testPalette(16, false, 1)
	g := &gif.GIF{
		Image: []*image.Paletted{
			testImage(image.Rect(0, 0, 20, 10), pal, 1),
			image.NewPaletted(image.Rect(5, 5, 5, 9), pal),
			image.NewPaletted(image.Rect(5, 5, 9, 5), pal),
			testImage(image.Rect(1, 1, 20, 10), pal, 2),
		},
		Delay: []int{1, 2, 3, 4}, Disposal: []byte{0, 0, 0, 0},
		Config: image.Config{ColorModel: pal, Width: 20, Height: 10},
	}
	want := encodeAll(t, g)
	if _, err := gif.DecodeAll(bytes.NewReader(want)); err != nil {
		t.Skipf("gif.DecodeAll rejects empty frames: %v", err)
	}
	got, err := copyAnimation(want, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("bytes differ from gif.EncodeAll")
	}
	// A source that has no strip for an empty frame.
	var buf bytes.Buffer
	aw, err := NewAnimWriter(&buf, AnimConfig{Width: 20, Height: 10, GlobalPalette: pal})
	if err != nil {
		t.Fatal(err)
	}
	for i, m := range g.Image {
		var src pipeline.Source = testutil.Chopped(m, 4)
		if m.Rect.Empty() {
			src = &fakeSource{bounds: m.Rect, model: pal}
		}
		if err := aw.WriteFrame(src, FrameInfo{Delay: g.Delay[i]}); err != nil {
			t.Fatal(err)
		}
	}
	if err := aw.Close(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Errorf("bytes differ from gif.EncodeAll (no strip for empty frames)")
	}
}

// TestAnimLoopCountLate: a looping extension after the first frame is seen
// by the time the frames have been read.
func TestAnimLoopCountLate(t *testing.T) {
	gifs, _ := animations()
	g := *gifs["global"]
	g.LoopCount = -1
	data := encodeAll(t, &g)
	descs := imageDescriptors(t, data)
	ext := append([]byte{0x21, 0xff, 0x0b}, "NETSCAPE2.0"...)
	ext = append(ext, 3, 1, 9, 0, 0)
	late := append(append(append([]byte(nil), data[:descs[2]]...), ext...), data[descs[2]:]...)
	want, err := gif.DecodeAll(bytes.NewReader(late))
	if err != nil || want.LoopCount != 9 {
		t.Fatalf("test file: loop count %d, %v", want.LoopCount, err)
	}
	a, err := OpenAnimation(bytes.NewReader(late), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if n := a.Config().LoopCount; n != -1 {
		t.Errorf("loop count %d before the first frame", n)
	}
	for {
		if _, _, err := a.NextFrame(); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
	}
	if n := a.Config().LoopCount; n != 9 {
		t.Errorf("loop count %d after the last frame, want 9", n)
	}
}
