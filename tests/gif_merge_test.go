package tests

import (
	"bytes"
	"image"
	"image/gif"
	"testing"

	. "github.com/whhhh1500/goimgtools"
)

// TestGIFMergesFramesAlike: in GIF output a frame that shows what the frame
// before shows lengthens it instead (up to the longest delay a GIF holds),
// with one palette for all frames and with a palette per frame; what is
// shown, and for how long, stays the same.
func TestGIFMergesFramesAlike(t *testing.T) {
	for _, local := range []bool{false, true} {
		g := movingAnimation(60, 30, 4, local)
		// Frame 1 twice more, frame 3 once more with a delay that cannot be
		// added to its own.
		g.Image = append(g.Image[:2], append([]*image.Paletted{g.Image[1], g.Image[1]}, g.Image[2:]...)...)
		g.Delay = []int{10, 20, 30, 40, 50, 65500}
		g.Disposal = make([]byte, len(g.Image))
		g.Image = append(g.Image, g.Image[len(g.Image)-1])
		g.Delay = append(g.Delay, 100)
		g.Disposal = append(g.Disposal, 0)
		in := encodeAll(t, g)
		out := must(FromBytes(in).Animated().GIF().ToBytes())
		got := decodeGIF(t, out)
		if want := []int{10, 90, 50, 65500, 100}; len(got.Delay) != len(want) || !equalInts(got.Delay, want) {
			t.Errorf("local %v: delays %v, want %v", local, got.Delay, want)
		}
		// The pictures, each as long as it shows.
		expand := func(g *gif.GIF) []string {
			var s []string
			for i, p := range coalesce(g) {
				for d := 0; d < g.Delay[i]; d += 10 {
					s = append(s, string(p.Pix))
				}
			}
			return s
		}
		a, b := expand(decodeGIF(t, in)), expand(got)
		if len(a) != len(b) {
			t.Fatalf("local %v: %d and %d tenths of pictures", local, len(a), len(b))
		}
		for i := range a {
			if a[i] != b[i] {
				t.Fatalf("local %v: the picture at %d0ms differs", local, i)
			}
		}
		if !bytes.HasPrefix(out, []byte("GIF89a")) {
			t.Error("not a GIF")
		}
	}
}

func equalInts(a, b []int) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return len(a) == len(b)
}
