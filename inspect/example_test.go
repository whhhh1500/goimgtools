package inspect_test

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"

	"github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/inspect"
)

// picture is a small test picture as a PNG file: a colour gradient.
func picture(w, h int) []byte {
	m := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			m.SetNRGBA(x, y, color.NRGBA{uint8(x * 255 / w), uint8(y * 255 / h), 128, 255})
		}
	}
	var buf bytes.Buffer
	png.Encode(&buf, m)
	return buf.Bytes()
}

// Is an upload a whole, readable image?
func ExampleVerify() {
	file := picture(64, 48)
	fmt.Println(inspect.Verify(goimgtools.FromBytes(file)))

	cut := file[:len(file)/2] // the header is still there
	_, headerErr := goimgtools.FromBytes(cut).Metadata()
	err := inspect.Verify(goimgtools.FromBytes(cut))
	fmt.Println(headerErr, errors.Is(err, goimgtools.ErrFormat))
	// Output:
	// <nil>
	// <nil> true
}

// What does a compression setting cost?
func ExampleCompare() {
	original := picture(320, 240)
	small, _ := goimgtools.FromBytes(original).JPEG(goimgtools.JPEGOptions{Quality: 30}).ToBytes()
	exact, _ := goimgtools.FromBytes(original).WebP(goimgtools.WebPOptions{Lossless: true}).ToBytes()

	d, err := inspect.Compare(goimgtools.FromBytes(original), goimgtools.FromBytes(small))
	fmt.Println(d.Identical, d.PSNR > 30, d.PSNR < 60, err)

	d, err = inspect.Compare(goimgtools.FromBytes(original), goimgtools.FromBytes(exact))
	fmt.Println(d, err)
	// Output:
	// false true true <nil>
	// identical <nil>
}

// Placeholders a page shows while the image loads.
func ExampleBlurHash() {
	img := goimgtools.FromBytes(picture(640, 480))
	b, err := inspect.BlurHash(img, 4, 3)
	if err != nil {
		fmt.Println(err)
		return
	}
	th, _ := inspect.ThumbHash(img)
	fmt.Println(len(b), len(th) >= 5)
	// Output: 28 true
}

// How alike a JPEG looks to its original.
func ExampleSSIM() {
	orig := picture(160, 120)
	jpeg, _ := goimgtools.FromBytes(orig).JPEG(goimgtools.JPEGOptions{Quality: 90}).ToBytes()
	s, err := inspect.SSIM(goimgtools.FromBytes(orig), goimgtools.FromBytes(jpeg))
	fmt.Println(s > 0.98, err)
	// Output: true <nil>
}
