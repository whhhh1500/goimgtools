package tests

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"testing"
	"time"

	. "github.com/whhhh1500/goimgtools"
)

// riffChunk builds a chunk of a WebP file.
func riffChunk(id string, data []byte) []byte {
	b := binary.LittleEndian.AppendUint32([]byte(id), uint32(len(data)))
	b = append(b, data...)
	if len(data)%2 == 1 {
		b = append(b, 0)
	}
	return b
}

// webpImageChunks returns the ALPH, VP8 and VP8L chunks of a still WebP file.
func webpImageChunks(t testing.TB, data []byte) []byte {
	var out []byte
	for pos := 12; pos+8 <= len(data); {
		n := int(binary.LittleEndian.Uint32(data[pos+4:]))
		if id := string(data[pos : pos+4]); id == "ALPH" || id == "VP8 " || id == "VP8L" {
			out = append(out, data[pos:pos+8+n+n&1]...)
		}
		pos += 8 + n + n&1
	}
	if out == nil {
		t.Fatal("no image chunk")
	}
	return out
}

// webpAnimation builds an animated WebP of a w x h canvas: each frame is
// the image of a still WebP file, at a place, shown for some milliseconds.
func webpAnimation(t testing.TB, w, h, loop int, frames [][]byte, at []image.Point, ms []int) []byte {
	put24 := func(b []byte, v int) { b[0], b[1], b[2] = byte(v), byte(v>>8), byte(v>>16) }
	vp8x := make([]byte, 10)
	vp8x[0] = 1<<1 | 1<<4 // animation, alpha
	put24(vp8x[4:], w-1)
	put24(vp8x[7:], h-1)
	anim := make([]byte, 6)
	binary.LittleEndian.PutUint16(anim[4:], uint16(loop))
	body := append([]byte("WEBP"), riffChunk("VP8X", vp8x)...)
	body = append(body, riffChunk("ANIM", anim)...)
	for i, f := range frames {
		m, err := FromBytes(f).Metadata()
		if err != nil {
			t.Fatal(err)
		}
		hdr := make([]byte, 16)
		put24(hdr[0:], at[i].X/2)
		put24(hdr[3:], at[i].Y/2)
		put24(hdr[6:], m.Width-1)
		put24(hdr[9:], m.Height-1)
		put24(hdr[12:], ms[i])
		body = append(body, riffChunk("ANMF", append(hdr, webpImageChunks(t, f)...))...)
	}
	return riffChunk("RIFF", body)
}

// pngChunk builds a chunk of a PNG file.
func pngChunk(id string, data []byte) []byte {
	b := binary.BigEndian.AppendUint32(nil, uint32(len(data)))
	b = append(b, id...)
	b = append(b, data...)
	return binary.BigEndian.AppendUint32(b, crc32.ChecksumIEEE(b[4:]))
}

// apng turns the still PNG file still into an APNG of two frames: the
// image itself, then the same data again as the second frame, shown for
// 1/10 and 30/100 of a second, played plays times.
func apng(t testing.TB, still []byte, plays int) []byte {
	m, err := png.DecodeConfig(bytes.NewReader(still))
	if err != nil {
		t.Fatal(err)
	}
	fctl := func(seq uint32, num, den uint16) []byte {
		b := binary.BigEndian.AppendUint32(nil, seq)
		b = binary.BigEndian.AppendUint32(b, uint32(m.Width))
		b = binary.BigEndian.AppendUint32(b, uint32(m.Height))
		b = append(b, make([]byte, 8)...) // x and y offsets
		b = binary.BigEndian.AppendUint16(b, num)
		b = binary.BigEndian.AppendUint16(b, den)
		return append(b, 0, 0) // dispose, blend
	}
	out := append([]byte(nil), still[:8]...)
	var idat []byte
	for pos := 8; pos+12 <= len(still); {
		n := int(binary.BigEndian.Uint32(still[pos:]))
		id := string(still[pos+4 : pos+8])
		switch id {
		case "IDAT":
			if idat == nil {
				actl := binary.BigEndian.AppendUint32(nil, 2)
				actl = binary.BigEndian.AppendUint32(actl, uint32(plays))
				out = append(out, pngChunk("acTL", actl)...)
				out = append(out, pngChunk("fcTL", fctl(0, 1, 10))...)
			}
			idat = append(idat, still[pos+8:pos+8+n]...)
		case "IEND":
			out = append(out, pngChunk("fcTL", fctl(1, 30, 100))...)
			out = append(out, pngChunk("fdAT", append(binary.BigEndian.AppendUint32(nil, 2), idat...))...)
		}
		out = append(out, still[pos:pos+12+n]...)
		pos += 12 + n
	}
	return out
}

// TestOtherAnimations: an animated WebP or PNG gives its first frame, its
// frames are counted, and Animated, which cannot go through them, says so.
func TestOtherAnimations(t *testing.T) {
	red := image.NewNRGBA(image.Rect(0, 0, 40, 30))
	for i := range red.Pix {
		red.Pix[i] = []uint8{220, 30, 30, 255}[i%4]
	}
	blue := image.NewNRGBA(image.Rect(0, 0, 10, 6))
	for i := range blue.Pix {
		blue.Pix[i] = []uint8{30, 30, 220, 128}[i%4]
	}
	lossless := func(m image.Image) []byte {
		b, err := FromImage(m).WebP(WebPOptions{Lossless: true}).ToBytes()
		if err != nil {
			t.Fatal(err)
		}
		return b
	}

	// A WebP of two frames; the first one smaller than the canvas.
	anim := webpAnimation(t, 50, 40, 0, [][]byte{lossless(red), lossless(blue)}, []image.Point{{4, 6}, {0, 0}}, []int{70, 250})
	fr, err := FromBytes(anim).Frames()
	if err != nil || fr != (Frames{Count: 2, Duration: 320 * time.Millisecond, LoopCount: 0}) {
		t.Errorf("animated WebP: %+v, %v", fr, err)
	}
	want := image.NewNRGBA(image.Rect(0, 0, 50, 40))
	for y := 6; y < 36; y++ {
		for x := 4; x < 44; x++ {
			want.SetNRGBA(x, y, color.NRGBA{220, 30, 30, 255})
		}
	}
	for _, m := range []*Image{FromBytes(anim), FromReader(bytes.NewReader(anim))} {
		p, err := m.PNG().ToBytes()
		if err != nil {
			t.Fatalf("animated WebP: %v", err)
		}
		sameImage(t, "first frame of an animated WebP", decodePNG(t, p), want)
	}

	// An APNG of two frames.
	still := pngBytes(t, red)
	ap := apng(t, still, 2)
	fr, err = FromBytes(ap).Frames()
	if err != nil || fr != (Frames{Count: 2, Duration: 400 * time.Millisecond, LoopCount: 2}) {
		t.Errorf("APNG: %+v, %v", fr, err)
	}
	for _, m := range []*Image{FromBytes(ap), FromReader(bytes.NewReader(ap))} {
		p, err := m.PNG().ToBytes()
		if err != nil {
			t.Fatalf("APNG: %v", err)
		}
		sameImage(t, "APNG", decodePNG(t, p), red)
	}
	// An acTL chunk after the image data does not make an animation.
	late := bytes.Replace(ap, pngChunk("acTL", binary.BigEndian.AppendUint32(binary.BigEndian.AppendUint32(nil, 2), 2)), nil, 1)
	late = bytes.Replace(late, []byte("\x00\x00\x00\x00IEND"), append(pngChunk("acTL", make([]byte, 8)), "\x00\x00\x00\x00IEND"...), 1)
	if fr, err := FromBytes(late).Frames(); err != nil || fr.Count != 1 {
		t.Errorf("acTL after IDAT: %+v, %v", fr, err)
	}

	// Animated cannot go through their frames, whatever the output.
	for _, c := range []struct {
		what string
		m    *Image
	}{
		{"WebP to GIF", FromBytes(anim).Animated().GIF()},
		{"WebP to WebP", FromBytes(anim).Animated().WebP()},
		{"APNG to PNG", FromBytes(ap).Animated().PNG()},
		{"APNG read twice", FromReader(bytes.NewReader(ap)).Animated().PNG()},
	} {
		if _, err := c.m.ToBytes(); !errors.Is(err, errors.ErrUnsupported) || errors.Is(err, ErrFormat) {
			t.Errorf("%s: %v", c.what, err)
		}
	}
	// A still WebP or PNG passes Animated unchanged.
	for _, b := range [][]byte{lossless(red), still} {
		a, err := FromBytes(b).Animated().PNG().ToBytes()
		if err != nil {
			t.Fatal(err)
		}
		if p, _ := FromBytes(b).PNG().ToBytes(); !bytes.Equal(a, p) {
			t.Error("Animated changed a still image")
		}
		if fr, err := FromBytes(b).Frames(); err != nil || fr != (Frames{Count: 1}) {
			t.Errorf("still: %+v, %v", fr, err)
		}
	}
}
