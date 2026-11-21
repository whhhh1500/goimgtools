package kvazaar

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"testing"

	"github.com/gen2brain/h265/hevc"
)

// fill gives a picture planes of a gradient with a little texture, the same
// on every run.
func fill(p *Picture) [3][]byte {
	var planes [3][]byte
	seed := uint32(1)
	for i := range 3 {
		plane, stride := p.Plane(i)
		if plane == nil {
			continue
		}
		w, h := p.width, p.height
		if i > 0 && p.chroma == Chroma420 {
			w, h = w/2, h/2
		}
		for y := range h {
			for x := range w {
				seed = seed*1664525 + 1013904223
				v := byte(x*3+y*2+i*60) + byte(seed>>29)
				plane[y*stride+x] = v
				planes[i] = append(planes[i], v)
			}
		}
	}
	return planes
}

// decode decodes a stream with gen2brain/h265 and returns its planes, rows
// cut to the size the stream says.
func decode(t *testing.T, s []byte) (*hevc.Picture, [3][]byte) {
	t.Helper()
	var d hevc.Decoder
	var pics []*hevc.Picture
	for _, nal := range hevc.SplitAnnexB(s) {
		out, err := d.DecodeNAL(nal)
		if err != nil {
			t.Fatal(err)
		}
		pics = append(pics, out...)
	}
	pics = append(pics, d.Flush()...)
	if len(pics) != 1 {
		t.Fatalf("%d pictures", len(pics))
	}
	p := pics[0]
	var planes [3][]byte
	for i, plane := range [3][]byte{p.Y, p.Cb, p.Cr} {
		if plane == nil {
			continue
		}
		w, h, stride := p.CropW, p.CropH, p.StrideY
		if i > 0 {
			stride = p.StrideC
			switch p.ChromaFormat {
			case 1:
				w, h = w/2, h/2
			case 0:
				continue
			}
		}
		for y := range h {
			planes[i] = append(planes[i], plane[(p.CropY+y)*stride+p.CropX:][:w]...)
		}
	}
	return p, planes
}

// TestLossless: pictures of each chroma format, of a size Kvazaar pads, come
// back as they went in, as gen2brain/h265 decodes them.
func TestLossless(t *testing.T) {
	e, err := NewEncoder()
	if err != nil {
		t.Fatal(err)
	}
	// One encoder codes the three one after another.
	for _, c := range []Chroma{Chroma400, Chroma420, Chroma444} {
		p, err := e.NewPicture(70, 46, c)
		if err != nil {
			t.Fatal(err)
		}
		want := fill(p)
		s, err := p.Encode(30, "preset=fast\nlossless=1\n")
		if err != nil {
			t.Fatal(err)
		}
		pic, got := decode(t, s)
		if pic.CropW != 70 || pic.CropH != 46 || pic.ChromaFormat != int(c) {
			t.Errorf("chroma %d: %dx%d, chroma %d", c, pic.CropW, pic.CropH, pic.ChromaFormat)
		}
		for i := range 3 {
			if string(got[i]) != string(want[i]) {
				t.Errorf("chroma %d: plane %d differs", c, i)
			}
		}
	}
}

// TestSameStream: the stream of a picture is the same on every processor
// (the sum is the one amd64 gives) and close to the picture.
func TestSameStream(t *testing.T) {
	e, err := NewEncoder()
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.NewPicture(96, 64, Chroma420)
	if err != nil {
		t.Fatal(err)
	}
	want := fill(p)
	s, err := p.Encode(30, "preset=medium\nintra-chroma-search=1\nsignhide=1\nvaq=8\ndeblock=-1:-1\nwpp=0\n")
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(s))[:16]; got != "1f1b74dcbd3c4e59" {
		t.Errorf("stream %s, %d bytes", got, len(s))
	}
	_, got := decode(t, s)
	var se, n float64
	for i := range 3 {
		for j := range want[i] {
			d := float64(want[i][j]) - float64(got[i][j])
			se, n = se+d*d, n+1
		}
	}
	if se/n > 40 {
		t.Errorf("mean squared error %.1f", se/n)
	}
}

// TestErrors: sizes Kvazaar cannot code, an option it does not know, and a
// picture coded twice are errors, after which the encoder codes on; a
// fault of the module is an error, after which it does not.
func TestErrors(t *testing.T) {
	e, err := NewEncoder()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range [][2]int{{0, 8}, {8, 0}, {7, 8}, {8, 7}, {1 << 16, 8}} {
		if _, err := e.NewPicture(s[0], s[1], Chroma420); err == nil {
			t.Errorf("%v accepted", s)
		}
	}
	p, err := e.NewPicture(16, 16, Chroma420)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Encode(30, "no-such-option=1\n"); !errors.Is(err, ErrOption) {
		t.Errorf("unknown option: %v", err)
	}
	if _, err := p.Encode(30, ""); err == nil {
		t.Error("coded twice")
	}
	p, err = e.NewPicture(16, 16, Chroma420)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Encode(30, ""); err != nil {
		t.Errorf("after the errors: %v", err)
	}
	if err := e.call(func() error { panic("trap") }); err == nil || !e.broken {
		t.Errorf("a trap: %v", err)
	}
	if _, err := e.NewPicture(16, 16, Chroma420); err == nil {
		t.Error("a broken encoder made a picture")
	}
}
