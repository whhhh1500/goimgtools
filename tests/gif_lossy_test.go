package tests

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/gif"
	"path/filepath"
	"testing"

	. "github.com/whhhh1500/goimgtools"
)

// TestGIFLossy: lossy LZW makes a photographic GIF smaller and keeps it
// close; transparency stays exactly where it was; 0 changes nothing.
func TestGIFLossy(t *testing.T) {
	photo := photoLike(240, 160)
	plain, err := FromImage(photo).GIF().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := FromImage(photo).GIF(GIFOptions{Lossy: 0}).ToBytes(); !bytes.Equal(b, plain) {
		t.Error("Lossy 0 changed the file")
	}
	ref, err := gif.Decode(bytes.NewReader(plain))
	if err != nil {
		t.Fatal(err)
	}
	prev := len(plain)
	for _, l := range []int{8, 24, 64} {
		b, err := FromImage(photo).GIF(GIFOptions{Lossy: l}).ToBytes()
		if err != nil {
			t.Fatal(err)
		}
		m, err := gif.Decode(bytes.NewReader(b))
		if err != nil {
			t.Fatalf("lossy %d: %v", l, err)
		}
		p, _, _ := psnrOf(m, ref)
		t.Logf("lossy %d: %d bytes (lossless %d), %.1f dB", l, len(b), len(plain), p)
		if len(b) > prev || p < 20 {
			t.Errorf("lossy %d: %d bytes after %d, %.1f dB", l, len(b), prev, p)
		}
		prev = len(b)
	}
	if prev >= len(plain)*9/10 {
		t.Errorf("lossy 64: %d bytes, lossless %d", prev, len(plain))
	}

	// An animation: the same frames, the same timing, transparency exact,
	// each frame close; with the palette chosen and with the fixed one.
	g := movingAnimation(160, 120, 5, true)
	in := encodeAll(t, g)
	for _, o := range []GIFOptions{{Lossy: 24}, {Lossy: 24, FixedPalette: true}} {
		lossless := o
		lossless.Lossy = 0
		a, err := FromBytes(in).Animated().GIF(lossless).ToBytes()
		if err != nil {
			t.Fatal(err)
		}
		b, err := FromBytes(in).Animated().GIF(o).ToBytes()
		if err != nil {
			t.Fatal(err)
		}
		if len(b) >= len(a) {
			t.Errorf("%+v: %d bytes, lossless %d", o, len(b), len(a))
		}
		fa, fb := coalesce(decodeGIF(t, a)), coalesce(decodeGIF(t, b))
		if len(fa) != len(fb) {
			t.Fatalf("%+v: %d frames, want %d", o, len(fb), len(fa))
		}
		for i := range fa {
			for k := 3; k < len(fa[i].Pix); k += 4 {
				if fa[i].Pix[k] != fb[i].Pix[k] {
					t.Fatalf("%+v frame %d: transparency changed", o, i)
				}
			}
			if p := framePSNR(fb[i], fa[i]); p < 25 {
				t.Errorf("%+v frame %d: %.1f dB", o, i, p)
			}
		}
	}

	// Transparent pixels in a still image stay transparent.
	icon := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			if (x-32)*(x-32)+(y-32)*(y-32) < 600 {
				icon.Pix[icon.PixOffset(x, y)+0] = uint8(4 * x)
				icon.Pix[icon.PixOffset(x, y)+1] = uint8(4 * y)
				icon.Pix[icon.PixOffset(x, y)+2] = 128
				icon.Pix[icon.PixOffset(x, y)+3] = 255
			}
		}
	}
	a, _ := FromImage(icon).GIF().ToBytes()
	b, err := FromImage(icon).GIF(GIFOptions{Lossy: 40}).ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	ma, _ := gif.Decode(bytes.NewReader(a))
	mb, _ := gif.Decode(bytes.NewReader(b))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			_, _, _, aa := ma.At(x, y).RGBA()
			_, _, _, ab := mb.At(x, y).RGBA()
			if aa != ab {
				t.Fatalf("pixel (%d,%d): alpha %d, want %d", x, y, ab, aa)
			}
		}
	}

	for _, l := range []int{-1, 256} {
		if _, err := FromImage(photo).GIF(GIFOptions{Lossy: l}).ToBytes(); err == nil || errors.Is(err, ErrFormat) {
			t.Errorf("lossy %d: %v", l, err)
		}
	}
}

// TestGIFLossyEverywhere: the same file whatever the memory limit and the
// number of threads.
func TestGIFLossyEverywhere(t *testing.T) {
	in := encodeAll(t, movingAnimation(300, 200, 5, true))
	ref, err := FromBytes(in).Animated().GIF(GIFOptions{Lossy: 20}).ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		limit   int64
		threads int
	}{{6 << 20, 1}, {0, 6}, {8 << 20, 4}} {
		dir := t.TempDir()
		got, err := FromReader(bytes.NewReader(in)).MemoryLimit(c.limit).TempDir(dir).Threads(c.threads).Animated().GIF(GIFOptions{Lossy: 20}).ToBytes()
		if err != nil {
			t.Fatalf("%+v: %v", c, err)
		}
		if !bytes.Equal(got, ref) {
			t.Errorf("%+v: another file", c)
		}
		if left, _ := filepath.Glob(filepath.Join(dir, "*")); len(left) != 0 {
			t.Errorf("%+v: temp files left: %v", c, left)
		}
	}
	_ = fmt.Sprint
}
