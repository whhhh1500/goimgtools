package goimgtools_test

// The examples are written against the public packages only, the way a
// program that imports the library is.

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"

	"github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/pipeline"
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

// Open an image, chain operations, pick the output, write it.
func Example() {
	out, err := goimgtools.FromBytes(picture(640, 480)).
		Resize(320, 0).
		JPEG(85).
		ToBytes()
	if err != nil {
		fmt.Println(err)
		return
	}
	info, _ := goimgtools.FromBytes(out).Metadata()
	fmt.Println(info.Format, info.Width, info.Height)
	// Output: jpeg 320 240
}

// An operation that only changes pixel values is a function over strips.
func ExampleImage_Pixels() {
	grey := func(strip *image.NRGBA) error {
		for i := 0; i < len(strip.Pix); i += 4 {
			p := strip.Pix[i : i+4]
			y := uint8((299*int(p[0]) + 587*int(p[1]) + 114*int(p[2])) / 1000)
			p[0], p[1], p[2] = y, y, y
		}
		return nil
	}
	img, err := goimgtools.FromBytes(picture(64, 48)).Pixels(grey).ToImage()
	if err != nil {
		fmt.Println(err)
		return
	}
	r, g, b, _ := img.At(10, 10).RGBA()
	fmt.Println(r == g && g == b)
	// Output: true
}

// rowCounter reports how many rows went through it. It is a stage that
// hands on what it reads: everything but Next and Close it has from the
// Source it embeds, the way a subclass has the methods of its base class.
type rowCounter struct {
	pipeline.Source
	rows int
}

func (c *rowCounter) Next() (image.Image, error) {
	strip, err := c.Source.Next()
	if err == nil {
		c.rows += strip.Bounds().Dy()
	}
	return strip, err
}

func (c *rowCounter) Close() error {
	fmt.Println(c.rows, "rows of", c.Bounds().Dx(), "pixels went through")
	return c.Source.Close()
}

// Any other operation is a stage of the pixel stream, put into the chain
// with Pipe.
func ExampleImage_Pipe() {
	count := goimgtools.OperationFunc(func(src pipeline.Source) (pipeline.Source, error) {
		return &rowCounter{Source: src}, nil
	})
	_, err := goimgtools.FromBytes(picture(600, 1200)).
		Resize(300, 0).
		Pipe(count).
		JPEG(80).
		ToBytes()
	fmt.Println(err)
	// Output:
	// 600 rows of 300 pixels went through
	// <nil>
}

// Thumbnail is a group of steps with a name.
func Thumbnail(size int) func(*goimgtools.Image) *goimgtools.Image {
	return func(m *goimgtools.Image) *goimgtools.Image {
		return m.Rotate().Resize(size, size, goimgtools.ResizeOptions{Fit: goimgtools.FitCover})
	}
}

func ExampleImage_Apply() {
	w, h, err := goimgtools.FromBytes(picture(640, 480)).Apply(Thumbnail(100)).OutputSize()
	fmt.Println(w, h, err)
	// Output: 100 100 <nil>
}

// Product is a type of your own that has every method of Image, and more:
// embedding *Image is how a Go program "extends the class".
type Product struct {
	*goimgtools.Image
	SKU string
}

func OpenProduct(data []byte, sku string) Product {
	return Product{goimgtools.FromBytes(data).Rotate(), sku}
}

// Listing adds the shop's steps to the recipe.
func (p Product) Listing() Product {
	p.Resize(300, 300, goimgtools.ResizeOptions{Fit: goimgtools.FitContain, Background: color.White}).
		Composite(goimgtools.Text(p.SKU, goimgtools.TextOptions{Size: 14}),
			goimgtools.CompositeOptions{Gravity: goimgtools.SouthEast, Left: -6, Top: -6})
	return p
}

func Example_embedding() {
	p := OpenProduct(picture(640, 480), "SKU-0042")
	p.Flatten(color.White) // a method of Image
	w, h, err := p.Listing().OutputSize()
	fmt.Println(w, h, err)
	// Output: 300 300 <nil>
}

// checker is an image that is computed instead of read: a chessboard.
type checker struct {
	size, cell, y int
	env           *pipeline.Env
	strip         *image.Gray
}

func (c *checker) Bounds() image.Rectangle { return image.Rect(0, 0, c.size, c.size) }
func (c *checker) ColorModel() color.Model { return color.GrayModel }
func (c *checker) Env() *pipeline.Env      { return c.env }
func (c *checker) Close() error            { return nil }

func (c *checker) Next() (image.Image, error) {
	if c.y >= c.size {
		return nil, io.EOF
	}
	if c.strip == nil {
		c.strip = image.NewGray(image.Rect(0, 0, c.size, 1))
	}
	// One row at a time; the same buffer every time, under a new row number.
	row := &image.Gray{Pix: c.strip.Pix, Stride: c.strip.Stride, Rect: image.Rect(0, c.y, c.size, c.y+1)}
	for x := range row.Pix {
		row.Pix[x] = uint8(255 * ((x/c.cell + c.y/c.cell) % 2))
	}
	c.y++
	return row, nil
}

// An input of your own is a function that makes a pixel stream.
func ExampleFromSource() {
	board := goimgtools.FromSource(func(env *pipeline.Env) (pipeline.Source, error) {
		return &checker{size: 800, cell: 100, env: env}, nil
	})
	out, err := board.Resize(80, 0).PNG().ToBytes()
	if err != nil {
		fmt.Println(err)
		return
	}
	img, _ := png.Decode(bytes.NewReader(out))
	fmt.Println(img.Bounds().Size(), img.At(5, 5), img.At(15, 5))
	// Output: (80,80) {0} {255}
}

// ---- a file format of your own: binary PPM ("P6") ----

type ppmReader struct {
	r        *bufio.Reader
	env      *pipeline.Env
	w, h, y  int
	row      []byte
	strip    *image.RGBA
	reserved int64
}

func ppmDecode(in goimgtools.Input, env *pipeline.Env) (pipeline.Source, error) {
	r := bufio.NewReader(in)
	var magic string
	var w, h, depth int
	if _, err := fmt.Fscan(r, &magic, &w, &h, &depth); err != nil || magic != "P6" || depth != 255 || w < 1 || h < 1 {
		return nil, errors.New("ppm: not a binary PPM with 8 bits a channel")
	}
	r.ReadByte() // the one white space after the header
	return &ppmReader{r: r, env: env, w: w, h: h}, nil
}

func (p *ppmReader) Bounds() image.Rectangle { return image.Rect(0, 0, p.w, p.h) }
func (p *ppmReader) ColorModel() color.Model { return color.RGBAModel }
func (p *ppmReader) Env() *pipeline.Env      { return p.env }
func (p *ppmReader) Opaque() bool            { return true }

func (p *ppmReader) Close() error {
	p.env.Budget.Release(p.reserved)
	p.reserved = 0
	return nil
}

func (p *ppmReader) Next() (image.Image, error) {
	if p.y >= p.h {
		return nil, io.EOF
	}
	if p.strip == nil {
		// The two row buffers grow with the width of the image, so they
		// are taken from the budget of the run.
		need := int64(p.w) * 7
		if err := p.env.Budget.Reserve(need, "ppm row"); err != nil {
			return nil, err
		}
		p.reserved = need
		p.row, p.strip = make([]byte, p.w*3), image.NewRGBA(image.Rect(0, 0, p.w, 1))
	}
	if _, err := io.ReadFull(p.r, p.row); err != nil {
		return nil, fmt.Errorf("ppm: row %d: %w", p.y, err)
	}
	out := &image.RGBA{Pix: p.strip.Pix, Stride: p.strip.Stride, Rect: image.Rect(0, p.y, p.w, p.y+1)}
	for x := 0; x < p.w; x++ {
		copy(out.Pix[4*x:4*x+3], p.row[3*x:3*x+3])
		out.Pix[4*x+3] = 255
	}
	p.y++
	return out, nil
}

// lent keeps a stage from closing a source that is not its own.
type lent struct{ pipeline.Source }

func (lent) Close() error { return nil }

func ppmEncode(w io.Writer, src pipeline.Source, _ goimgtools.EncodeOptions) error {
	b := src.Bounds()
	if _, err := fmt.Fprintf(w, "P6\n%d %d\n255\n", b.Dx(), b.Dy()); err != nil {
		return err
	}
	// Map hands over every strip as 8-bit NRGBA, whatever the image is.
	rows := pipeline.Map(lent{src}, func(strip *image.NRGBA) error {
		rgb := make([]byte, 0, strip.Rect.Dx()*3)
		for y := strip.Rect.Min.Y; y < strip.Rect.Max.Y; y++ {
			rgb = rgb[:0]
			row := strip.Pix[strip.PixOffset(strip.Rect.Min.X, y):][:strip.Rect.Dx()*4]
			for i := 0; i < len(row); i += 4 {
				rgb = append(rgb, row[i], row[i+1], row[i+2])
			}
			if _, err := w.Write(rgb); err != nil {
				return err
			}
		}
		return nil
	})
	defer rows.Close()
	for {
		if _, err := rows.Next(); err == io.EOF {
			return nil
		} else if err != nil {
			return err
		}
	}
}

// A format of your own is recognised, read and written like the built-in
// ones once it is registered.
func ExampleRegisterFormat() {
	goimgtools.RegisterFormat(goimgtools.Codec{
		Format: goimgtools.Format{Name: "ppm", Extensions: []string{".ppm"}},
		Match:  func(head []byte) bool { return bytes.HasPrefix(head, []byte("P6")) },
		Decode: ppmDecode,
		Encode: ppmEncode,
	})

	ppm, err := goimgtools.FromBytes(picture(320, 200)).Resize(160, 0).ToFormat("ppm").ToBytes()
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Printf("%q %d bytes\n", ppm[:11], len(ppm))

	// And back: the file is recognised by its content.
	info, err := goimgtools.FromBytes(ppm).Metadata()
	fmt.Println(info.Format, info.Width, info.Height, info.HasAlpha, err)
	_, err = goimgtools.FromBytes(ppm).Rotate(90).JPEG(80).ToBytes()
	fmt.Println(err)
	// Output:
	// "P6\n160 100\n" 48015 bytes
	// ppm 160 100 false <nil>
	// <nil>
}

// One of the package's own formats can be read or written differently:
// take its Codec, wrap what you want to change, register it again.
func ExampleCodecByName() {
	original, _ := goimgtools.CodecByName("jpeg")
	defer goimgtools.UnregisterFormat("jpeg") // the package's own again

	logged := original
	logged.Encode = func(w io.Writer, src pipeline.Source, o goimgtools.EncodeOptions) error {
		fmt.Printf("writing a %v jpeg at quality %d\n", src.Bounds().Size(), o.Quality)
		return original.Encode(w, src, o)
	}
	goimgtools.RegisterFormat(logged)

	_, err := goimgtools.FromBytes(picture(640, 480)).Resize(100, 0).JPEG(70).ToBytes()
	fmt.Println(err)
	// Output:
	// writing a (100,75) jpeg at quality 70
	// <nil>
}

// A resampling filter of your own.
func ExampleFilter() {
	// A box filter: every source pixel under a result pixel counts the same.
	box := &goimgtools.Filter{Support: 0.5, At: func(t float64) float64 { return 1 }}
	w, h, err := goimgtools.FromBytes(picture(640, 480)).
		Resize(64, 0, goimgtools.ResizeOptions{Filter: box}).
		OutputSize()
	fmt.Println(w, h, err)
	// Output: 64 48 <nil>
}

// An operation of your own, made available by name: to Do, and to the
// command line of a program built with package cli (-op sepia:0.8), next
// to the built-in ones.
func ExampleRegisterOperation() {
	err := goimgtools.RegisterOperation(goimgtools.OperationDef{
		Name:  "sepia",
		Usage: "[STRENGTH]",
		Help:  "warm brown tones, 0 to 1 (default 1)",
		New: func(args ...string) (goimgtools.Operation, error) {
			k := 1.0
			if len(args) == 1 {
				if _, err := fmt.Sscan(args[0], &k); err != nil || k < 0 || k > 1 {
					return nil, fmt.Errorf("strength 0 to 1, got %q", args[0])
				}
			}
			return goimgtools.OperationFunc(func(src pipeline.Source) (pipeline.Source, error) {
				return pipeline.Map(src, func(s *image.NRGBA) error {
					for i := 0; i+3 < len(s.Pix); i += 4 {
						r, g, b := float64(s.Pix[i]), float64(s.Pix[i+1]), float64(s.Pix[i+2])
						y := 0.299*r + 0.587*g + 0.114*b
						s.Pix[i] = uint8(min(255, r+k*(y*1.07-r)))
						s.Pix[i+1] = uint8(min(255, g+k*(y*0.74-g)))
						s.Pix[i+2] = uint8(min(255, b+k*(y*0.43-b)))
					}
					return nil
				}), nil
			}), nil
		},
	})
	if err != nil {
		fmt.Println(err)
		return
	}
	defer goimgtools.UnregisterOperation("sepia")

	out, err := goimgtools.FromBytes(picture(64, 48)).Do("resize", "32", "0").Do("sepia", "0.8").PNG().ToBytes()
	if err != nil {
		fmt.Println(err)
		return
	}
	m, _ := png.Decode(bytes.NewReader(out))
	fmt.Println(m.Bounds().Size())
	// Output: (32,24)
}

// A hook goes around every step of a recipe. This one replaces the
// built-in blur with a box blur of its own and lets the other steps be.
func ExampleImage_Around() {
	ownBlur := func(step goimgtools.Step, in pipeline.Source, run func(pipeline.Source) (pipeline.Source, error)) (pipeline.Source, error) {
		fmt.Println("step", step)
		if step.Name != "blur" {
			return run(in)
		}
		return boxBlur(in), nil // in is ours now: boxBlur closes it with its result
	}
	_, err := goimgtools.FromBytes(picture(64, 48)).
		Resize(32, 0).Blur(2).Flip().
		Around(ownBlur).
		PNG().ToBytes()
	if err != nil {
		fmt.Println(err)
	}
	// Output:
	// step resize:32,0,inside,cubic
	// step blur:2
	// step flip
}

// boxBlur averages each pixel with its left and right neighbours.
func boxBlur(src pipeline.Source) pipeline.Source {
	return pipeline.Map(src, func(s *image.NRGBA) error {
		w := s.Rect.Dx()
		for y := 0; y < s.Rect.Dy(); y++ {
			row := s.Pix[y*s.Stride : y*s.Stride+4*w]
			prev := [4]int{int(row[0]), int(row[1]), int(row[2]), int(row[3])}
			for x := 0; x < w; x++ {
				r := min(x+1, w-1)
				cur := [4]int{int(row[4*x]), int(row[4*x+1]), int(row[4*x+2]), int(row[4*x+3])}
				for c := 0; c < 4; c++ {
					row[4*x+c] = uint8((prev[c] + cur[c] + int(row[4*r+c])) / 3)
				}
				prev = cur
			}
		}
		return nil
	})
}

// Behaviour changed the way a subclass overrides a method: a type of your
// own embeds *Image (so it has all its methods) and puts a hook around the
// recipes it makes. Here every Resize also sharpens a little, which
// shrunk photographs usually want.
type Photo struct{ *goimgtools.Image }

func OpenPhoto(data []byte) Photo {
	sharpenAfterResize := func(step goimgtools.Step, in pipeline.Source, run func(pipeline.Source) (pipeline.Source, error)) (pipeline.Source, error) {
		out, err := run(in)
		if err != nil || step.Name != "resize" {
			return out, err
		}
		// The built-in sharpen, as an operation of its own: a recipe on the
		// stream out.
		return goimgtools.FromSource(func(*pipeline.Env) (pipeline.Source, error) { return out, nil }).
			Sharpen().Stream(out.Env())
	}
	return Photo{goimgtools.FromBytes(data).Around(sharpenAfterResize)}
}

func Example_overriding() {
	out, err := OpenPhoto(picture(640, 480)).Resize(320, 0).JPEG(85).ToBytes()
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(len(out) > 0)
	// Output: true
}
