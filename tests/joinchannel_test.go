package tests

import (
	"image"
	"image/color"
	"testing"

	. "github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// TestJoinChannel: a grey mask becomes the alpha, multiplied with the
// image's own; ExtractChannel(ChannelAlpha) gives it back; the size must
// match; both are read in strips that need not line up.
func TestJoinChannel(t *testing.T) {
	photo := noisyPhoto(97, 61, false)
	mask := image.NewGray(photo.Rect)
	for i := range mask.Pix {
		mask.Pix[i] = uint8(i * 7)
	}
	got := drawn(t, FromImage(photo).JoinChannel(FromImage(mask)))
	for i := 0; i < len(photo.Pix); i += 4 {
		if got.Pix[i+3] != mask.Pix[i/4] || got.Pix[i] != photo.Pix[i] {
			t.Fatalf("pixel %d: %v, want colour %v alpha %d", i/4, got.Pix[i:i+4], photo.Pix[i:i+3], mask.Pix[i/4])
		}
	}
	back := drawn(t, FromImage(photo).JoinChannel(FromImage(mask)).ExtractChannel(ChannelAlpha))
	for i := range mask.Pix {
		if back.Pix[4*i] != mask.Pix[i] {
			t.Fatalf("extracted alpha %d at %d, want %d", back.Pix[4*i], i, mask.Pix[i])
		}
	}
	// With transparency of its own: multiplied. Strips of 7 rows against
	// the mask's 11.
	cut := noisyPhoto(97, 61, true)
	m := drawn(t, FromSource(func(env *pipeline.Env) (pipeline.Source, error) {
		return testutil.ChoppedEnv(cut, 7, env), nil
	}).JoinChannel(FromSource(func(env *pipeline.Env) (pipeline.Source, error) {
		return testutil.ChoppedEnv(mask, 11, env), nil
	})))
	for i := 0; i < len(cut.Pix); i += 4 {
		want := uint8((uint32(cut.Pix[i+3])*uint32(mask.Pix[i/4]) + 127) / 255)
		if m.Pix[i+3] != want {
			t.Fatalf("pixel %d: alpha %d, want %d", i/4, m.Pix[i+3], want)
		}
	}
	// A colour mask counts by its grey.
	white := drawn(t, FromImage(photo).JoinChannel(Blank(97, 61, color.White)))
	if white.Pix[3] != 255 {
		t.Errorf("white mask: alpha %d", white.Pix[3])
	}
	if _, err := FromImage(photo).JoinChannel(Blank(96, 61, color.White)).PNG().ToBytes(); err == nil {
		t.Error("another size: no error")
	}
	if _, err := FromImage(photo).JoinChannel(nil).PNG().ToBytes(); err == nil {
		t.Error("nil: no error")
	}
}
