package tests

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/draw"
	"image/png"
	"os"
	"testing"

	. "github.com/whhhh1500/goimgtools"
)

// apngFile is an APNG taken apart by a reader of its own: every frame's
// chunks made into a PNG for image/png, composited as the specification
// says (blend_op OVER only over pixels that are opaque or transparent,
// which is all this writer blends).
type apngFile struct {
	colorType byte
	frames    int // from acTL; 0 for a still PNG
	plays     int
	pics      []*image.NRGBA
	ms        []int
}

func readAPNG(t *testing.T, b []byte) apngFile {
	t.Helper()
	if !bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatal("not a PNG")
	}
	var f apngFile
	var ihdr, plte, trns []byte
	var cv *image.NRGBA
	var fctl []byte
	var data []byte
	seq := uint32(0)
	chunk := func(name string, body []byte) []byte {
		var c []byte
		c = binary.BigEndian.AppendUint32(c, uint32(len(body)))
		c = append(c, name...)
		c = append(c, body...)
		return binary.BigEndian.AppendUint32(c, crc32.ChecksumIEEE(c[4:]))
	}
	flush := func() {
		if data == nil {
			return
		}
		w, h := int(binary.BigEndian.Uint32(ihdr[0:])), int(binary.BigEndian.Uint32(ihdr[4:]))
		x, y, blend, dispose := 0, 0, byte(0), byte(0)
		ms := 0
		if fctl != nil {
			w, h = int(binary.BigEndian.Uint32(fctl[4:])), int(binary.BigEndian.Uint32(fctl[8:]))
			x, y = int(binary.BigEndian.Uint32(fctl[12:])), int(binary.BigEndian.Uint32(fctl[16:]))
			num, den := int(binary.BigEndian.Uint16(fctl[20:])), int(binary.BigEndian.Uint16(fctl[22:]))
			ms = num * 1000 / den
			dispose, blend = fctl[24], fctl[25]
			if dispose > 2 || blend > 1 {
				t.Fatalf("dispose_op %d, blend_op %d", dispose, blend)
			}
		}
		before := append([]byte(nil), cv.Pix...)
		hdr := append([]byte(nil), ihdr...)
		binary.BigEndian.PutUint32(hdr[0:], uint32(w))
		binary.BigEndian.PutUint32(hdr[4:], uint32(h))
		p := []byte("\x89PNG\r\n\x1a\n")
		p = append(p, chunk("IHDR", hdr)...)
		if plte != nil {
			p = append(p, chunk("PLTE", plte)...)
		}
		if trns != nil {
			p = append(p, chunk("tRNS", trns)...)
		}
		p = append(append(p, chunk("IDAT", data)...), chunk("IEND", nil)...)
		m, err := png.Decode(bytes.NewReader(p))
		if err != nil {
			t.Fatalf("frame %d: %v", len(f.pics)+1, err)
		}
		fr := image.NewNRGBA(image.Rect(0, 0, w, h))
		draw.Draw(fr, fr.Rect, m, image.Point{}, draw.Src)
		for yy := 0; yy < h; yy++ {
			for xx := 0; xx < w; xx++ {
				s := fr.Pix[fr.PixOffset(xx, yy):][:4]
				d := cv.Pix[cv.PixOffset(x+xx, y+yy):][:4]
				switch {
				case blend == 0:
					copy(d, s)
				case s[3] == 255:
					copy(d, s)
				case s[3] != 0:
					t.Fatalf("frame %d blends a pixel of alpha %d", len(f.pics)+1, s[3])
				}
			}
		}
		pic := image.NewNRGBA(cv.Rect)
		copy(pic.Pix, cv.Pix)
		f.pics = append(f.pics, pic)
		f.ms = append(f.ms, ms)
		data = nil
		// The frame's rectangle cleared, or as it was before the frame.
		for yy := y; yy < y+h && dispose != 0; yy++ {
			o := cv.PixOffset(x, yy)
			if dispose == 1 {
				clear(cv.Pix[o : o+4*w])
			} else {
				copy(cv.Pix[o:o+4*w], before[o:o+4*w])
			}
		}
	}
	for p := 8; p+12 <= len(b); {
		n := int(binary.BigEndian.Uint32(b[p:]))
		name, body := string(b[p+4:p+8]), b[p+8:p+8+n]
		p += 12 + n
		switch name {
		case "IHDR":
			ihdr = append([]byte(nil), body...)
			f.colorType = ihdr[9]
			cv = image.NewNRGBA(image.Rect(0, 0, int(binary.BigEndian.Uint32(body[0:])), int(binary.BigEndian.Uint32(body[4:]))))
		case "PLTE":
			plte = body
		case "tRNS":
			trns = body
		case "acTL":
			f.frames, f.plays = int(binary.BigEndian.Uint32(body)), int(binary.BigEndian.Uint32(body[4:]))
		case "fcTL", "fdAT":
			if got := binary.BigEndian.Uint32(body); got != seq {
				t.Fatalf("%s sequence number %d, want %d", name, got, seq)
			}
			seq++
			if name == "fcTL" {
				flush()
				fctl = body
			} else {
				data = append(data, body[4:]...)
			}
		case "IDAT":
			data = append(data, body...)
		case "IEND":
			flush()
		}
	}
	return f
}

// framesOf runs in through the chain with Animated, capturing every frame
// it hands to the encoder, and writes it as out.
func framesOf(t *testing.T, in []byte, out func(*Image) *Image) ([]byte, []*image.NRGBA) {
	t.Helper()
	var pics []*image.NRGBA
	img := FromBytes(in).Animated().Pixels(func(s *image.NRGBA) error {
		if s.Rect.Min.Y == 0 {
			pics = append(pics, nil)
		}
		cur := pics[len(pics)-1]
		if cur == nil {
			cur = image.NewNRGBA(image.Rect(0, 0, s.Rect.Dx(), 0))
		}
		cur.Rect.Max.Y = s.Rect.Max.Y
		for y := 0; y < s.Rect.Dy(); y++ {
			row := s.Pix[y*s.Stride:][:4*s.Rect.Dx()]
			for i := 0; i < len(row); i += 4 {
				if row[i+3] == 0 {
					cur.Pix = append(cur.Pix, 0, 0, 0, 0)
				} else {
					cur.Pix = append(cur.Pix, row[i:i+4]...)
				}
			}
		}
		cur.Stride = 4 * s.Rect.Dx()
		pics[len(pics)-1] = cur
		return nil
	})
	b, err := out(img).ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	return b, pics
}

// TestAPNGOutput: Animated with PNG output writes an APNG that a reader of
// its own (image/png for every frame) composites into the very frames the
// chain made, consecutive frames alike merged: with one palette for an
// animation of few colours (a GIF with transparency), RGB for an opaque one
// of many, RGBA for an APNG of many colours with transparency that uses
// every dispose and blend op. The playing time and the plays are kept.
func TestAPNGOutput(t *testing.T) {
	opsAPNG, err := os.ReadFile("testdata/png/anim-ops.png")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name      string
		in        []byte
		colorType byte
	}{
		{"gif, few colours", encodeAll(t, testAnimation(64, 48)), 3},
		{"gif, many colours", encodeAll(t, movingAnimation(80, 40, 6, true)), 2},
		{"apng with transparency", opsAPNG, 6},
	} {
		for _, level := range []int{0, 9} {
			out, pics := framesOf(t, c.in, func(m *Image) *Image { return m.PNG(PNGOptions{CompressionLevel: level}) })
			f := readAPNG(t, out)
			if f.colorType != c.colorType {
				t.Errorf("%s: colour type %d, want %d", c.name, f.colorType, c.colorType)
			}
			var want []*image.NRGBA
			for _, p := range pics {
				if len(want) > 0 && bytes.Equal(want[len(want)-1].Pix, p.Pix) {
					continue
				}
				want = append(want, p)
			}
			if f.frames != len(f.pics) || len(f.pics) != len(want) {
				t.Fatalf("%s: acTL says %d frames, %d read, %d made (%d before merging)", c.name, f.frames, len(f.pics), len(want), len(pics))
			}
			for i := range want {
				if !bytes.Equal(f.pics[i].Pix, want[i].Pix) {
					t.Fatalf("%s, level %d: frame %d differs", c.name, level, i+1)
				}
			}
			in, err := FromBytes(c.in).Frames()
			if err != nil {
				t.Fatal(err)
			}
			got, err := FromBytes(out).Frames()
			if err != nil || got.Count != len(want) || got.Duration != in.Duration || got.LoopCount != in.LoopCount {
				t.Errorf("%s: Frames %+v (%v), the input's %+v", c.name, got, err, in)
			}
		}
	}

	// Frames all alike make a still PNG; Colors does not apply.
	g := testAnimation(30, 20)
	g.Image, g.Delay, g.Disposal = g.Image[:1], g.Delay[:1], g.Disposal[:1]
	g.Image = append(g.Image, g.Image[0])
	g.Delay = append(g.Delay, 50)
	g.Disposal = append(g.Disposal, 0)
	out := must(FromBytes(encodeAll(t, g)).Animated().PNG().ToBytes())
	if f := readAPNG(t, out); f.frames != 0 || len(f.pics) != 1 {
		t.Errorf("frames all alike: acTL %d, %d pictures", f.frames, len(f.pics))
	}
	if _, err := FromBytes(encodeAll(t, testAnimation(30, 20))).Animated().PNG(PNGOptions{Colors: 16}).ToBytes(); !errors.Is(err, errors.ErrUnsupported) {
		t.Errorf("Colors with an animation: %v", err)
	}
}
