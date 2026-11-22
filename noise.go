package goimgtools

import (
	"errors"
	"image"
	"image/color"
	"io"
	"math"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// NoiseOptions are the settings of Noise.
type NoiseOptions struct {
	// Mean is the grey the values lie around, 0 to 255, and Sigma their
	// standard deviation. With both 0 they are 128 and 30, as libvips'
	// gaussnoise and sharp's create have them.
	Mean, Sigma float64
	// Seed chooses the noise: the same seed gives the same image, on
	// every processor.
	Seed int64
}

// Noise is a grey image of width x height pixels of Gaussian noise, as
// Pillow's Image.effect_noise and libvips' gaussnoise (sharp's create with
// noise) make one: film grain to Composite onto a picture, a texture, a
// test image. Each value is the mean plus sigma times the sum of twelve
// random numbers less its average (as libvips makes it), which is as good
// as a normal one, held to 0..255. Its rows are made as they are asked
// for, so it takes no memory of its size. Without an output format the
// result is written as PNG.
func Noise(width, height int, o NoiseOptions) *Image {
	if width < 1 || height < 1 {
		return &Image{err: errors.New("goimgtools: Noise needs a width and a height of at least 1")}
	}
	if math.IsNaN(o.Mean) || math.IsNaN(o.Sigma) || math.IsInf(o.Mean, 0) || math.IsInf(o.Sigma, 0) || o.Sigma < 0 {
		return &Image{err: errors.New("goimgtools: Noise needs a mean and a sigma that are numbers, the sigma not negative")}
	}
	if o.Mean == 0 && o.Sigma == 0 {
		o.Mean, o.Sigma = 128, 30
	}
	return &Image{open: func() (*input, error) {
		return &input{kind: "noise", gen: func(env *pipeline.Env) (pipeline.Source, error) {
			return newNoise(width, height, o, env)
		}}, nil
	}}
}

// noiseSource gives strips of noise. Every row has random numbers of its
// own, from the seed and its number, so the image does not depend on how
// it is cut into strips.
type noiseSource struct {
	env    *pipeline.Env
	b      image.Rectangle
	o      NoiseOptions
	rows   int
	y      int
	strip  *image.Gray
	held   int64
	closed bool
}

func newNoise(w, h int, o NoiseOptions, env *pipeline.Env) (*noiseSource, error) {
	s := &noiseSource{env: env, b: image.Rect(0, 0, w, h), o: o}
	s.rows = pipeline.StripRows(int64(w), h)
	s.held = int64(w) * int64(s.rows)
	if err := env.Budget.Reserve(s.held, "noise strip"); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *noiseSource) Bounds() image.Rectangle { return s.b }
func (s *noiseSource) ColorModel() color.Model { return color.GrayModel }
func (s *noiseSource) Env() *pipeline.Env      { return s.env }
func (s *noiseSource) Opaque() bool            { return true }

// splitmix is the next state and number of the SplitMix64 generator.
func splitmix(x uint64) (uint64, uint64) {
	x += 0x9e3779b97f4a7c15
	z := x
	z = (z ^ z>>30) * 0xbf58476d1ce4e5b9
	z = (z ^ z>>27) * 0x94d049bb133111eb
	return x, z ^ z>>31
}

func (s *noiseSource) Next() (image.Image, error) {
	if s.y >= s.b.Max.Y {
		return nil, io.EOF
	}
	n := min(s.rows, s.b.Max.Y-s.y)
	w := s.b.Dx()
	if s.strip == nil {
		s.strip = image.NewGray(image.Rect(0, 0, w, s.rows))
	}
	for j := 0; j < n; j++ {
		row := s.strip.Pix[j*s.strip.Stride:][:w]
		// The row's own numbers.
		_, state := splitmix(uint64(s.o.Seed) ^ uint64(s.y+j)*0xd1342543de82ef95)
		for x := range row {
			// Twelve numbers of 16 bits: their sum less 6 x 65535 has a
			// standard deviation of 65536 (near enough).
			sum := int64(-6 * 65535)
			for k := 0; k < 3; k++ {
				var r uint64
				state, r = splitmix(state)
				sum += int64(r&0xffff) + int64(r>>16&0xffff) + int64(r>>32&0xffff) + int64(r>>48)
			}
			v := s.o.Mean + float64(float64(s.o.Sigma*float64(sum))/65536)
			switch {
			case v <= 0:
				row[x] = 0
			case v >= 255:
				row[x] = 255
			default:
				row[x] = uint8(v + 0.5)
			}
		}
	}
	st := &image.Gray{Pix: s.strip.Pix[:n*s.strip.Stride], Stride: s.strip.Stride, Rect: image.Rect(0, s.y, w, s.y+n)}
	s.y += n
	return st, nil
}

func (s *noiseSource) Close() error {
	if !s.closed {
		s.closed = true
		s.env.Budget.Release(s.held)
	}
	return nil
}
