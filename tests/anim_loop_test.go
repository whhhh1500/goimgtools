package tests

import (
	"fmt"
	"testing"
	"time"

	. "github.com/whhhh1500/goimgtools"
)

// TestLoopAndFrameDelay: Loop and FrameDelay replace how often an animation
// plays and how long its frames show, in GIF, WebP and APNG output; bad
// values are errors.
func TestLoopAndFrameDelay(t *testing.T) {
	in := encodeAll(t, testAnimation(64, 48))
	// GIF: plays 3 times (repeats twice), every frame 50ms.
	b := must(FromBytes(in).Animated().Loop(3).FrameDelay(50 * time.Millisecond).GIF().ToBytes())
	g := decodeGIF(t, b)
	if g.LoopCount != 2 || fmt.Sprint(g.Delay) != "[5 5 5 5 5 5]" {
		t.Errorf("GIF: loop %d, delays %v", g.LoopCount, g.Delay)
	}
	// Once: no looping extension.
	if g := decodeGIF(t, must(FromBytes(in).Animated().Loop(1).GIF().ToBytes())); g.LoopCount != -1 {
		t.Errorf("GIF once: loop %d", g.LoopCount)
	}
	// Each frame its own, the last for the rest.
	g = decodeGIF(t, must(FromBytes(in).Animated().FrameDelay(20*time.Millisecond, 70*time.Millisecond).GIF().ToBytes()))
	if fmt.Sprint(g.Delay) != "[2 7 7 7 7 7]" || g.LoopCount != 3 {
		t.Errorf("GIF: delays %v, loop %d (the input's)", g.Delay, g.LoopCount)
	}
	// WebP: for ever, 250ms.
	w := must(FromBytes(in).Animated().Loop(0).FrameDelay(250 * time.Millisecond).WebP(WebPOptions{Lossless: true}).ToBytes())
	shown, ms, loop := playWebP(t, w)
	if loop != 0 || len(shown) != 6 || fmt.Sprint(ms) != "[250 250 250 250 250 250]" {
		t.Errorf("WebP: loop %d, %v", loop, ms)
	}
	// APNG: 5 plays, 1.5s in all.
	p := must(FromBytes(in).Animated().Loop(5).FrameDelay(250 * time.Millisecond).PNG().ToBytes())
	if fr, err := FromBytes(p).Frames(); err != nil || fr.LoopCount != 5 || fr.Duration != 1500*time.Millisecond {
		t.Errorf("APNG: %+v, %v", fr, err)
	}
	for _, bad := range []*Image{FromBytes(in).Animated().Loop(-1), FromBytes(in).Animated().FrameDelay(), FromBytes(in).Animated().FrameDelay(-time.Second)} {
		if _, err := bad.GIF().ToBytes(); err == nil {
			t.Error("no error")
		}
	}
}
