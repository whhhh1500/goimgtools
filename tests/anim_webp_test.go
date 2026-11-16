package tests

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"path/filepath"
	"testing"
	"time"

	. "github.com/whhhh1500/goimgtools"
)

// webpFrame is a frame of an animated WebP as the file states it.
type webpFrame struct {
	rect    image.Rectangle
	ms      int
	blend   bool
	dispose bool
	chunks  []byte // its ALPH, VP8 and VP8L chunks
}

// readWebPAnimation splits an animated WebP into its canvas size, loop
// count and frames.
func readWebPAnimation(t *testing.T, data []byte) (w, h, loop int, frames []webpFrame) {
	t.Helper()
	u24 := func(b []byte) int { return int(b[0]) | int(b[1])<<8 | int(b[2])<<16 }
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WEBP" || int(binary.LittleEndian.Uint32(data[4:]))+8 != len(data) {
		t.Fatal("not a WebP file of the size it states")
	}
	for pos := 12; pos+8 <= len(data); {
		id, n := string(data[pos:pos+4]), int(binary.LittleEndian.Uint32(data[pos+4:]))
		body := data[pos+8 : pos+8+n]
		switch id {
		case "VP8X":
			if body[0]&(1<<1) == 0 {
				t.Fatal("no animation flag")
			}
			w, h = u24(body[4:])+1, u24(body[7:])+1
		case "ANIM":
			loop = int(binary.LittleEndian.Uint16(body[4:]))
		case "ANMF":
			x, y := 2*u24(body[0:]), 2*u24(body[3:])
			frames = append(frames, webpFrame{
				rect:    image.Rect(x, y, x+u24(body[6:])+1, y+u24(body[9:])+1),
				ms:      u24(body[12:]),
				blend:   body[15]&2 == 0,
				dispose: body[15]&1 != 0,
				chunks:  body[16:],
			})
		default:
			t.Fatalf("unexpected chunk %q", id)
		}
		pos += 8 + n + n&1
	}
	return w, h, loop, frames
}

// playWebP renders every frame of an animated WebP as a viewer shows it:
// each frame decoded on its own (by this package, as a still file) and put
// on the canvas as its header says.
func playWebP(t *testing.T, data []byte) (shown []*image.NRGBA, ms []int, loop int) {
	t.Helper()
	w, h, loop, frames := readWebPAnimation(t, data)
	canvas := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i, f := range frames {
		body := f.chunks
		if string(body[:4]) == "ALPH" {
			vp8x := make([]byte, 10)
			vp8x[0] = 1 << 4
			put := func(b []byte, v int) { b[0], b[1], b[2] = byte(v), byte(v>>8), byte(v>>16) }
			put(vp8x[4:], f.rect.Dx()-1)
			put(vp8x[7:], f.rect.Dy()-1)
			body = append(riffChunk("VP8X", vp8x), body...)
		}
		still := riffChunk("RIFF", append([]byte("WEBP"), body...))
		m, err := FromBytes(still).ToImage()
		if err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
		if m.Bounds().Size() != f.rect.Size() {
			t.Fatalf("frame %d: image %v, header %v", i, m.Bounds(), f.rect)
		}
		if !f.rect.In(canvas.Rect) {
			t.Fatalf("frame %d at %v: off the canvas", i, f.rect)
		}
		for y := 0; y < f.rect.Dy(); y++ {
			for x := 0; x < f.rect.Dx(); x++ {
				src := color.NRGBAModel.Convert(m.At(m.Bounds().Min.X+x, m.Bounds().Min.Y+y)).(color.NRGBA)
				px, py := f.rect.Min.X+x, f.rect.Min.Y+y
				if f.blend {
					// Only the two cases this package writes: a pixel
					// that covers or one that leaves the picture be.
					switch src.A {
					case 0:
						continue
					case 255:
					default:
						t.Fatalf("frame %d: a blended pixel of alpha %d", i, src.A)
					}
				}
				canvas.SetNRGBA(px, py, src)
			}
		}
		snap := image.NewNRGBA(canvas.Rect)
		copy(snap.Pix, canvas.Pix)
		shown = append(shown, snap)
		ms = append(ms, f.ms)
		if f.dispose {
			for y := f.rect.Min.Y; y < f.rect.Max.Y; y++ {
				for x := f.rect.Min.X; x < f.rect.Max.X; x++ {
					canvas.SetNRGBA(x, y, color.NRGBA{})
				}
			}
		}
	}
	return shown, ms, loop
}

// TestAnimatedWebP: an animated GIF written as an animated WebP shows the
// same pictures for the same time, losslessly exactly; only what changes
// is stored, and a frame that changes nothing lengthens the one before.
func TestAnimatedWebP(t *testing.T) {
	g := testAnimation(64, 48)
	in := encodeAll(t, g)
	want := coalesce(g)
	// Delays 10, 20, 5, 7, 30 and 1 hundredths; 10ms or less shows as 100.
	wantMS := []int{100, 200, 50, 70, 300, 100}

	lossless, err := FromBytes(in).Animated().WebP(WebPOptions{Lossless: true}).ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	shown, ms, loop := playWebP(t, lossless)
	if len(shown) != len(want) || fmt.Sprint(ms) != fmt.Sprint(wantMS) || loop != 4 {
		t.Fatalf("lossless: %d frames, %v ms, loop %d; want %d, %v, 4 (the GIF repeats 3 times)", len(shown), ms, loop, len(want), wantMS)
	}
	for i := range shown {
		sameImage(t, fmt.Sprintf("lossless frame %d", i), shown[i], want[i])
	}
	fr, err := FromBytes(lossless).Frames()
	if err != nil || fr != (Frames{Count: 6, Duration: 820 * time.Millisecond, LoopCount: 4}) {
		t.Errorf("Frames: %+v, %v", fr, err)
	}
	first, err := FromBytes(lossless).ToImage()
	if err != nil {
		t.Fatal(err)
	}
	sameImage(t, "first frame", first, want[0])

	// Lossy: transparency is kept exactly (the alpha plane is lossless).
	lossy, err := FromBytes(in).Animated().WebP(WebPOptions{Quality: 90}).ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	shown, ms, _ = playWebP(t, lossy)
	if len(shown) != len(want) || fmt.Sprint(ms) != fmt.Sprint(wantMS) {
		t.Fatalf("lossy: %d frames, %v ms", len(shown), ms)
	}
	for i := range shown {
		for y := 0; y < shown[i].Rect.Dy(); y++ {
			for x := 0; x < shown[i].Rect.Dx(); x++ {
				if a, b := shown[i].NRGBAAt(x, y).A, want[i].RGBAAt(x, y).A; a != b {
					t.Fatalf("lossy frame %d: alpha %d at (%d,%d), want %d", i, a, x, y, b)
				}
			}
		}
	}
	// And close to the frames on a picture of smooth colours (the frames
	// above are patterns of single pixels, which 4:2:0 colour does not
	// keep).
	smooth := movingAnimation(200, 150, 5, true)
	lossy, err = FromBytes(encodeAll(t, smooth)).Animated().WebP(WebPOptions{Quality: 90}).ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	shown, _, _ = playWebP(t, lossy)
	for i, w := range coalesce(smooth) {
		got := image.NewRGBA(shown[i].Rect)
		for y := 0; y < got.Rect.Dy(); y++ {
			for x := 0; x < got.Rect.Dx(); x++ {
				got.Set(x, y, shown[i].At(x, y))
			}
		}
		if p := framePSNR(got, w); p < 30 { // each frame alone as a still file: 31 to 37
			t.Errorf("lossy frame %d: PSNR %.1f", i, p)
		}
	}

	// A frame like the one before is not stored: its time goes to that one.
	dup := testAnimation(64, 48)
	dup.Image = append(dup.Image[:2], dup.Image[1:]...)
	dup.Delay = append(dup.Delay[:2], dup.Delay[1:]...)
	dup.Disposal = append(dup.Disposal[:2], dup.Disposal[1:]...)
	b, err := FromBytes(encodeAll(t, dup)).Animated().WebP(WebPOptions{Lossless: true}).ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	if _, ms, _ := playWebP(t, b); fmt.Sprint(ms) != fmt.Sprint([]int{100, 400, 50, 70, 300, 100}) {
		t.Errorf("repeated frame: %v ms", ms)
	}

	// Only a small square moves: later frames are small, and lossless ones
	// leave the rest of their rectangle transparent, blended.
	mv := movingAnimation(200, 150, 5, false)
	b, err = FromBytes(encodeAll(t, mv)).Animated().WebP(WebPOptions{Lossless: true}).ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, frames := readWebPAnimation(t, b)
	for i, f := range frames[1:] {
		if f.rect.Dx()*f.rect.Dy() > 200*150/4 || !f.blend || f.rect.Min.X%2 != 0 || f.rect.Min.Y%2 != 0 {
			t.Errorf("frame %d: %v blend %v", i+1, f.rect, f.blend)
		}
	}
	shown, _, _ = playWebP(t, b)
	for i, w := range coalesce(mv) {
		sameImage(t, fmt.Sprintf("moving frame %d", i), shown[i], w)
	}

	// Operations apply to every frame.
	b, err = FromBytes(in).Animated().Resize(32, 0).WebP().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	if w, h, _, frames := readWebPAnimation(t, b); w != 32 || h != 24 || len(frames) != 6 {
		t.Errorf("resized: %dx%d, %d frames", w, h, len(frames))
	}

	// The loop count: none in the GIF plays once; for ever stays.
	for _, c := range []struct{ gif, webp int }{{-1, 1}, {0, 0}, {1, 2}, {65535, 65535}} {
		a := testAnimation(16, 12)
		a.LoopCount = c.gif
		b, err := FromBytes(encodeAll(t, a)).Animated().WebP().ToBytes()
		if err != nil {
			t.Fatal(err)
		}
		if _, _, loop, _ := readWebPAnimation(t, b); loop != c.webp {
			t.Errorf("GIF loop count %d: WebP %d, want %d", c.gif, loop, c.webp)
		}
	}
}

// TestAnimatedWebPEverywhere: the file is the same whatever the memory
// limit (pictures kept in temporary files) and the number of threads, and
// nothing is left behind. (The lossless encoder tries fewer variants when
// memory is short: its file then differs, its pixels do not.)
func TestAnimatedWebPEverywhere(t *testing.T) {
	in := encodeAll(t, movingAnimation(400, 300, 6, true))
	for _, o := range []WebPOptions{{}, {Lossless: true}} {
		ref, err := FromBytes(in).Animated().WebP(o).ToBytes()
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range []struct {
			limit   int64
			threads int
		}{{6 << 20, 1}, {0, 6}, {8 << 20, 4}} {
			dir := t.TempDir()
			got, err := FromReader(bytes.NewReader(in)).MemoryLimit(c.limit).TempDir(dir).Threads(c.threads).Animated().WebP(o).ToBytes()
			if err != nil {
				t.Fatalf("%+v %+v: %v", o, c, err)
			}
			if o.Lossless && c.limit != 0 {
				a, _, _ := playWebP(t, got)
				b, _, _ := playWebP(t, ref)
				for i := range b {
					sameImage(t, fmt.Sprintf("%+v %+v frame %d", o, c, i), a[i], b[i])
				}
			} else if !bytes.Equal(got, ref) {
				t.Errorf("%+v %+v: another file", o, c)
			}
			if left, _ := filepath.Glob(filepath.Join(dir, "*")); len(left) != 0 {
				t.Errorf("%+v %+v: temp files left: %v", o, c, left)
			}
		}
	}
	// Too large a canvas for WebP, and frames that come out larger.
	big := &gif.GIF{Image: []*image.Paletted{image.NewPaletted(image.Rect(0, 0, 20000, 2), color.Palette{color.Black, color.White}),
		image.NewPaletted(image.Rect(0, 0, 20000, 2), color.Palette{color.Black, color.White})}, Delay: []int{5, 5}}
	if _, err := FromBytes(encodeAll(t, big)).Animated().WebP().ToBytes(); err == nil {
		t.Error("a 20000-pixel-wide animation written as WebP")
	}
}

// TestAnimatedStillAndSize: a GIF of one frame, or of frames all alike,
// becomes a still WebP; MaxFileSize, which chooses the quality of one
// picture, is refused for an animation and kept for a single frame.
func TestAnimatedStillAndSize(t *testing.T) {
	g := movingAnimation(120, 90, 3, false)
	frame0 := coalesce(g)[0]
	one := &gif.GIF{Image: g.Image[:1], Delay: g.Delay[:1], Config: g.Config}
	same := &gif.GIF{Image: []*image.Paletted{g.Image[0], g.Image[0], g.Image[0]}, Delay: []int{5, 5, 5}, Config: g.Config}
	for name, a := range map[string]*gif.GIF{"one frame": one, "frames alike": same} {
		for _, o := range []WebPOptions{{Lossless: true}, {Quality: 90}} {
			b, err := FromBytes(encodeAll(t, a)).Animated().WebP(o).ToBytes()
			if err != nil {
				t.Fatalf("%s %+v: %v", name, o, err)
			}
			if bytes.Contains(b[:40], []byte("ANIM")) || len(b) < 12 || int(binary.LittleEndian.Uint32(b[4:]))+8 != len(b) {
				t.Errorf("%s %+v: not a still WebP file", name, o)
			}
			if fr, err := FromBytes(b).Frames(); err != nil || fr != (Frames{Count: 1}) {
				t.Errorf("%s %+v: Frames %+v, %v", name, o, fr, err)
			}
			if o.Lossless {
				m, err := FromBytes(b).ToImage()
				if err != nil {
					t.Fatal(err)
				}
				sameImage(t, name, m, frame0)
			}
		}
	}

	anim := encodeAll(t, g)
	for _, m := range []*Image{FromBytes(anim).Animated().WebP().MaxFileSize(1 << 20), FromBytes(anim).Animated().GIF().MaxFileSize(1 << 20)} {
		if _, err := m.ToBytes(); !errors.Is(err, errors.ErrUnsupported) || errors.Is(err, ErrFormat) {
			t.Errorf("MaxFileSize with an animation: %v", err)
		}
	}
	b, err := FromReader(bytes.NewReader(encodeAll(t, one))).Animated().WebP(WebPOptions{Quality: 95}).MaxFileSize(2000).ToBytes()
	if err != nil || len(b) > 2000 {
		t.Errorf("MaxFileSize with one frame: %d bytes, %v", len(b), err)
	}
}
