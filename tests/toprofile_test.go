package tests

import (
	"image"
	"image/color"
	"testing"

	. "github.com/whhhh1500/goimgtools"
)

// TestToProfile: sRGB red into Display P3 is the red LittleCMS makes of
// it, the profile written with the file; a P3 image into Adobe RGB goes
// directly (P3's reds, which sRGB cannot show, are kept); formats without
// profiles and profiles that are not RGB ones of primaries refuse.
func TestToProfile(t *testing.T) {
	red := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	for i := 0; i < len(red.Pix); i += 4 {
		copy(red.Pix[i:], []byte{255, 0, 0, 255})
	}
	p3 := ProfileDisplayP3()
	png, err := FromImage(red).ToProfile(p3).PNG().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	info, err := FromBytes(png).Metadata()
	if err != nil || !info.HasICC {
		t.Fatalf("profile not written: %+v %v", info, err)
	}
	m, _ := FromBytes(png).StripICC().ToImage()
	// LittleCMS (Pillow's ImageCms, relative colorimetric): (234, 51, 35).
	if c := color.NRGBAModel.Convert(m.At(1, 1)).(color.NRGBA); abs(int(c.R)-234) > 1 || abs(int(c.G)-51) > 1 || abs(int(c.B)-35) > 1 {
		t.Errorf("sRGB red in P3: %v", c)
	}
	// Back to sRGB through the profile written: the red again.
	back, _ := FromBytes(png).ToSRGB().ToImage()
	if c := color.NRGBAModel.Convert(back.At(1, 1)).(color.NRGBA); c.R < 254 || c.G > 1 || c.B > 1 {
		t.Errorf("back to sRGB: %v", c)
	}
	// P3's red, which sRGB cannot show, into Adobe RGB, directly: as
	// LittleCMS makes it (239, 0, 0), where through sRGB it would be
	// clipped first (to about 219).
	adobe, err := FromBytes(pillowFile(t, "fmt-p3red.png")).ToProfile(ProfileAdobeRGB()).PNG().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	a, _ := FromBytes(adobe).StripICC().ToImage()
	if c := color.NRGBAModel.Convert(a.At(1, 1)).(color.NRGBA); abs(int(c.R)-239) > 1 || c.G > 1 || c.B > 1 {
		t.Errorf("P3 red in Adobe RGB: %v", c)
	}
	if _, err := FromImage(red).ToProfile(p3).GIF().ToBytes(); err == nil {
		t.Error("GIF with a profile accepted")
	}
	if _, err := FromImage(red).ToProfile([]byte("not a profile")).PNG().ToBytes(); err == nil {
		t.Error("a broken profile accepted")
	}
}
