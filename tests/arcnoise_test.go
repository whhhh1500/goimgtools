package tests

import (
	"bytes"
	"image"
	"image/color"
	"math"
	"testing"

	. "github.com/whhhh1500/goimgtools"
)

// redness is how much of each pixel of m is red over white (0 to 255),
// summed, and a function that tells of one pixel.
func redness(t *testing.T, m *Image) (float64, func(x, y int) float64) {
	t.Helper()
	img, err := m.ToImage()
	if err != nil {
		t.Fatal(err)
	}
	at := func(x, y int) float64 {
		c := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
		return 255 - float64(c.G) // white is 255, red 0
	}
	var sum float64
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			sum += at(x, y)
		}
	}
	return sum / 255, at
}

// TestArcPieChord: Arc, Pie and Chord draw the part of an ellipse between
// two angles, measured clockwise from three o'clock as Pillow's ImageDraw
// measures them.
func TestArcPieChord(t *testing.T) {
	red := color.NRGBA{255, 0, 0, 255}
	canvas := func() *Image { return Blank(100, 100, color.White) }
	quarter := math.Pi * 40 * 40 / 4
	near := func(what string, got, want float64) {
		t.Helper()
		if math.Abs(got-want) > want/100 {
			t.Errorf("%s: an area of %.1f pixels, want %.1f", what, got, want)
		}
	}

	// A quarter from three o'clock clockwise is the bottom right one.
	area, at := redness(t, canvas().Draw(Pie(50, 50, 40, 40, 0, 90).Fill(red)))
	near("Pie 0 to 90", area, quarter)
	if at(70, 70) != 255 || at(30, 70) != 0 || at(70, 30) != 0 || at(30, 30) != 0 {
		t.Errorf("Pie 0 to 90: not the bottom right quarter (%v %v %v %v)", at(70, 70), at(30, 70), at(70, 30), at(30, 30))
	}
	// An end before the start goes on round: 270 to 90 is the right half.
	area, at = redness(t, canvas().Draw(Pie(50, 50, 40, 40, 270, 90).Fill(red)))
	near("Pie 270 to 90", area, 2*quarter)
	if at(70, 50) != 255 || at(30, 50) != 0 {
		t.Errorf("Pie 270 to 90: not the right half")
	}
	// A whole turn (and more) is the ellipse.
	whole, _ := redness(t, canvas().Draw(Ellipse(50, 50, 40, 25).Fill(red)))
	for _, end := range []float64{360, 1000} {
		area, _ = redness(t, canvas().Draw(Pie(50, 50, 40, 25, 0, end).Fill(red)))
		near("Pie of a whole turn", area, whole)
	}
	// A chord from 0 to 180 is the lower half, one from 0 to 90 a quarter
	// less the triangle to the centre.
	area, at = redness(t, canvas().Draw(Chord(50, 50, 40, 40, 0, 180).Fill(red)))
	near("Chord 0 to 180", area, 2*quarter)
	if at(50, 70) != 255 || at(50, 30) != 0 {
		t.Errorf("Chord 0 to 180: not the lower half")
	}
	area, _ = redness(t, canvas().Draw(Chord(50, 50, 40, 40, 0, 90).Fill(red)))
	near("Chord 0 to 90", area, quarter-40*40/2)
	// An arc from 270 to 360 is the top right quarter of the outline: its
	// middle is painted, the centre and the other quarters are not; its
	// area is its length times its width.
	area, at = redness(t, canvas().Draw(Arc(50, 50, 40, 40, 270, 360).Stroke(red, 4)))
	near("Arc 270 to 360", area, math.Pi*40/2*4)
	if at(78, 21) != 255 || at(50, 50) != 0 || at(78, 78) != 0 || at(21, 21) != 0 {
		t.Errorf("Arc 270 to 360: not the top right quarter of the outline")
	}
	// A pie's outline: the arc and the two lines to the centre.
	_, at = redness(t, canvas().Draw(Pie(50, 50, 40, 40, 0, 90).Stroke(red, 3)))
	if at(70, 50) != 255 || at(50, 70) != 255 || at(65, 65) != 0 || at(78, 78) != 255 {
		t.Errorf("Pie outlined: the lines to the centre or the arc are missing")
	}

	// By name, as Do and the command line take them.
	byName := must(canvas().Do("pie", "50", "50", "40", "40", "0", "90", "fill=red").PNG().ToBytes())
	if want := must(canvas().Draw(Pie(50, 50, 40, 40, 0, 90).Fill(red)).PNG().ToBytes()); !bytes.Equal(byName, want) {
		t.Error("pie by name differs from Pie")
	}
	for _, name := range []string{"arc", "chord"} {
		if _, err := canvas().Do(name, "50", "50", "40", "30", "10", "200", "stroke=blue", "width=2").PNG().ToBytes(); err != nil {
			t.Errorf("%s by name: %v", name, err)
		}
	}

	for name, s := range map[string]Shape{
		"an arc without a stroke":  Arc(50, 50, 40, 40, 0, 90).Fill(red),
		"a pie with nothing":       Pie(50, 50, 40, 40, 0, 90),
		"a radius below 0":         Chord(50, 50, -1, 40, 0, 90).Fill(red),
		"an angle not a number":    Pie(50, 50, 40, 40, math.NaN(), 90).Fill(red),
		"an angle without an end":  Arc(50, 50, 40, 40, 0, math.Inf(1)).Stroke(red, 1),
		"a centre not a number":    Arc(math.Inf(-1), 50, 40, 40, 0, 90).Stroke(red, 1),
		"a stroke of no width":     Arc(50, 50, 40, 40, 0, 90).Stroke(red, 0),
		"an ellipse of no numbers": Pie(50, 50, math.NaN(), 40, 0, 90).Fill(red),
	} {
		if _, err := canvas().Draw(s).PNG().ToBytes(); err == nil {
			t.Errorf("%s: drawn", name)
		}
	}
}

// TestNoise: Noise is grey values around the mean with the standard
// deviation asked for, the same for the same seed however it is cut into
// strips, another for another seed.
func TestNoise(t *testing.T) {
	stats := func(m *Image) (mean, sigma float64, pix []uint8) {
		t.Helper()
		img, err := m.ToImage()
		if err != nil {
			t.Fatal(err)
		}
		g, ok := img.(*image.Gray)
		if !ok {
			t.Fatalf("an image of %T", img)
		}
		var s, s2 float64
		for _, v := range g.Pix {
			s += float64(v)
			s2 += float64(v) * float64(v)
		}
		n := float64(len(g.Pix))
		mean = s / n
		return mean, math.Sqrt(s2/n - mean*mean), g.Pix
	}
	mean, sigma, pix := stats(Noise(700, 500, NoiseOptions{}))
	if math.Abs(mean-128) > 0.3 || math.Abs(sigma-30) > 0.3 {
		t.Errorf("Noise by default: mean %.2f, sigma %.2f, want 128 and 30", mean, sigma)
	}
	// Normal enough: about 68.3%% within one sigma, 95.4%% within two.
	var in1, in2 float64
	for _, v := range pix {
		d := math.Abs(float64(v) - 128)
		if d <= 30 {
			in1++
		}
		if d <= 60 {
			in2++
		}
	}
	if p1, p2 := in1/float64(len(pix)), in2/float64(len(pix)); math.Abs(p1-0.689) > 0.01 || math.Abs(p2-0.956) > 0.01 {
		// (Whole greys: within 30.5 and 60.5 of the mean.)
		t.Errorf("Noise: %.3f within one sigma, %.3f within two", p1, p2)
	}
	mean, sigma, _ = stats(Noise(400, 300, NoiseOptions{Mean: 60, Sigma: 10, Seed: 7}))
	if math.Abs(mean-60) > 0.2 || math.Abs(sigma-10) > 0.2 {
		t.Errorf("Noise of mean 60 and sigma 10: mean %.2f, sigma %.2f", mean, sigma)
	}
	if mean, sigma, _ = stats(Noise(50, 50, NoiseOptions{Mean: 200})); mean != 200 || sigma != 0 {
		t.Errorf("Noise of sigma 0: mean %.2f, sigma %.2f", mean, sigma)
	}
	// The same whatever the strips (a small memory limit makes them
	// shorter), and by the seed.
	a := must(Noise(3000, 900, NoiseOptions{Seed: 5}).PNG().ToBytes())
	b := must(Noise(3000, 900, NoiseOptions{Seed: 5}).MemoryLimit(16 << 20).PNG().ToBytes())
	c := must(Noise(3000, 900, NoiseOptions{Seed: 6}).PNG().ToBytes())
	if !bytes.Equal(a, b) {
		t.Error("Noise differs with the memory limit")
	}
	if bytes.Equal(a, c) {
		t.Error("Noise is the same for another seed")
	}
	// A row is its own: the first of a tall image is that of an image of
	// one row.
	_, _, tall := stats(Noise(3000, 900, NoiseOptions{Seed: 5}))
	_, _, one := stats(Noise(3000, 1, NoiseOptions{Seed: 5}))
	rows := map[string]bool{}
	for y := 0; y < 900; y++ {
		rows[string(tall[3000*y:3000*(y+1)])] = true
	}
	if !bytes.Equal(tall[:3000], one) || len(rows) != 900 {
		t.Errorf("Noise: a row depends on the rows around it, or rows repeat (%d different of 900)", len(rows))
	}
	if info, err := Noise(30, 20, NoiseOptions{}).Metadata(); err != nil || info.Format != "noise" || info.Width != 30 || info.Height != 20 {
		t.Errorf("Metadata of Noise: %+v, %v", info, err)
	}
	// Through the chain: grain over a picture.
	if _, err := FromImage(photoLike(64, 48)).Composite(Noise(64, 48, NoiseOptions{Sigma: 12, Mean: 128}), CompositeOptions{Blend: BlendOverlay}).JPEG().ToBytes(); err != nil {
		t.Errorf("Noise composited: %v", err)
	}
	for name, m := range map[string]*Image{
		"no width":         Noise(0, 5, NoiseOptions{}),
		"a sigma below 0":  Noise(5, 5, NoiseOptions{Sigma: -1}),
		"a mean not there": Noise(5, 5, NoiseOptions{Mean: math.NaN()}),
	} {
		if _, err := m.PNG().ToBytes(); err == nil {
			t.Errorf("Noise with %s: made", name)
		}
	}
}
