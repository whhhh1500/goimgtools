package tests

import (
	"fmt"
	"image"
	"image/gif"
	"testing"

	. "github.com/whhhh1500/goimgtools"
)

// TestAnimatedWebPBlendOpaque: the frames of an animated WebP that are
// blended onto the picture put their opaque pixels there as they are, as
// libwebp's animation decoder does (BlendPixelRowNonPremult passes them
// over; its arithmetic, meant for the partly transparent, would darken
// each channel of them by one level). An animation written without loss,
// whose later frames are blended, with opaque pixels where the picture
// changes and transparent ones where it does not, comes back exactly.
func TestAnimatedWebPBlendOpaque(t *testing.T) {
	for name, g := range map[string]*gif.GIF{
		"a square that moves": movingAnimation(200, 150, 5, false),
		"a dithered picture":  ditheredAnimation(240, 160, 6),
		"palettes of its own": movingAnimation(120, 90, 4, true),
	} {
		want := coalesce(g)
		lossless, err := FromBytes(encodeAll(t, g)).Animated().WebP(WebPOptions{Lossless: true}).ToBytes()
		if err != nil {
			t.Fatal(err)
		}
		_, _, _, written := readWebPAnimation(t, lossless)
		blended := 0
		for _, f := range written {
			if f.blend {
				blended++
			}
		}
		if blended == 0 {
			t.Fatalf("%s: no frame is blended", name)
		}
		// The frames as this package reads them.
		var got []*image.NRGBA
		_, err = FromBytes(lossless).Animated().Pixels(func(strip *image.NRGBA) error {
			if strip.Rect.Min.Y == 0 {
				got = append(got, image.NewNRGBA(want[0].Rect))
			}
			m := got[len(got)-1]
			for y := strip.Rect.Min.Y; y < strip.Rect.Max.Y; y++ {
				copy(m.Pix[m.PixOffset(0, y):], strip.Pix[strip.PixOffset(strip.Rect.Min.X, y):][:4*strip.Rect.Dx()])
			}
			return nil
		}).WebP(WebPOptions{Lossless: true}).ToBytes()
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != len(want) {
			t.Fatalf("%s: %d frames read, want %d", name, len(got), len(want))
		}
		for i := range got {
			sameImage(t, fmt.Sprintf("%s, frame %d", name, i), got[i], want[i])
		}
	}
}
