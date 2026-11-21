package jpegstream

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"image"
	"image/color"
	"math/rand/v2"
	"testing"

	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// deringImages are pictures with pixels at 255 beside others: dark text and
// lines on white, colours with a channel clipped at 255, noise with many
// white pixels; odd sizes.
func deringImages() map[string]*image.NRGBA {
	text := image.NewNRGBA(image.Rect(0, 0, 197, 131))
	for y := 0; y < 131; y++ {
		for x := 0; x < 197; x++ {
			v := uint8(255)
			switch {
			case (x/3+y/7)%5 == 0 && y%13 < 9: // strokes
				v = uint8(20 + (x*7+y)%40)
			case x%29 < 2 || y%17 == 0: // rules
				v = 60
			case (x+y)%41 == 0:
				v = 180
			}
			text.SetNRGBA(x, y, color.NRGBA{v, v, v, 255})
		}
	}
	sat := image.NewNRGBA(image.Rect(0, 0, 120, 90))
	for y := 0; y < 90; y++ {
		for x := 0; x < 120; x++ {
			c := func(v int) uint8 { return uint8(min(255, max(0, v))) }
			sat.SetNRGBA(x, y, color.NRGBA{c(x*4 - 40), c(330 - y*5), c((x+y)*3 - 100), 255})
		}
	}
	r := rand.New(rand.NewPCG(3, 4))
	noise := image.NewNRGBA(image.Rect(0, 0, 67, 45))
	for i := 0; i < len(noise.Pix); i += 4 {
		for k := 0; k < 3; k++ {
			v := uint8(r.IntN(256))
			if r.IntN(10) < 3 {
				v = 255
			}
			noise.Pix[i+k] = v
		}
		noise.Pix[i+3] = 255
	}
	return map[string]*image.NRGBA{"text": text, "sat": sat, "noise": noise}
}

// TestDeringingMatchesSharp: with OvershootDeringing the file is sharp's
// (0.35.5, libvips with mozjpeg: jpeg({overshootDeringing: true}) and
// optimized tables, its default; the sums are of its files from the first
// DQT on, its JFIF segment aside), for 4:2:0 and 4:4:4 at three
// qualities, whatever the strips and the threads.
func TestDeringingMatchesSharp(t *testing.T) {
	want := map[string][6]string{
		"text":  {"21d52df4eb028e6a", "8ec83d3e7270a486", "39bb960d499dabd0", "1aec055dc640290a", "fbfc1d1b8f68b541", "885bf1c856870bec"},
		"sat":   {"f9f5d57ebfa60fe3", "a1f416b56c62fb4d", "b29e47ae82a1c3d9", "9c996731f5f3776f", "b8077e7cd14a34a9", "f323b2854de4d89c"},
		"noise": {"5cf77e8dfefe0d4c", "c411b09d7b46185e", "7b1c678764cc40f6", "3087c1bf490b35f4", "c66f9e13cfe642b7", "4d269b2621d07513"},
	}
	imgs := deringImages()
	for name, sums := range want {
		for i, c := range []struct{ q, sub int }{{30, Subsample420}, {75, Subsample420}, {95, Subsample420}, {30, Subsample444}, {75, Subsample444}, {95, Subsample444}} {
			for _, helpers := range []int{0, 3} {
				env := pipeline.NewEnv(0, t.TempDir())
				env.SetHelpers(helpers)
				var buf bytes.Buffer
				o := &Options{Quality: c.q, Optimize: true, Subsampling: c.sub, OvershootDeringing: true}
				if err := Encode(&buf, testutil.ChoppedEnv(imgs[name], 5+3*i, env), o); err != nil {
					t.Fatal(err)
				}
				b := buf.Bytes()
				b = b[bytes.Index(b, []byte{0xff, 0xdb}):]
				if got := fmt.Sprintf("%x", sha256.Sum256(b))[:16]; got != sums[i] {
					t.Errorf("%s q%d sub %d helpers %d: sum %s, sharp's %s", name, c.q, c.sub, helpers, got, sums[i])
				}
			}
		}
	}
}

// TestDering: blocks without white, or all white, are left alone; in the
// others only the white pixels change, to at least 255 and at most 31 or
// twice the DC quantizer above it, and the mean stays at most 255.
func TestDering(t *testing.T) {
	var b block
	for i := range b {
		b[i] = int32(i * 3)
	}
	if c := b; dering(&c, 8) || c != b {
		t.Error("a block without white changed")
	}
	for i := range b {
		b[i] = 255
	}
	if c := b; dering(&c, 8) || c != b {
		t.Error("a white block changed")
	}
	r := rand.New(rand.NewPCG(1, 2))
	for range 20000 {
		for i := range b {
			b[i] = int32(r.IntN(256))
			if r.IntN(3) == 0 {
				b[i] = 255
			}
		}
		q0 := int32(1 + r.IntN(40))
		c := b
		if !dering(&c, q0) {
			continue
		}
		var sum int32
		for i := range c {
			sum += c[i]
			if b[i] != 255 {
				if c[i] != b[i] {
					t.Fatalf("pixel %d: %d changed to %d", i, b[i], c[i])
				}
				continue
			}
			if c[i] < 255 || c[i] > 255+min(31, 2*q0) {
				t.Fatalf("white pixel %d became %d (q0 %d)", i, c[i], q0)
			}
		}
		if sum > 255*blockSize {
			t.Fatalf("block sum %d above white's", sum)
		}
	}
}
