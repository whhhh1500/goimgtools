package ops

import (
	"image"

	"github.com/whhhh1500/goimgtools/internal/iccprof"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// ToSRGB converts the colours of src from the colour profile t was made
// for to sRGB; transparency is kept. With helpers in the pipeline the rows
// of each strip are shared out, as for Modulate.
func ToSRGB(src pipeline.Source, t *iccprof.Transform) pipeline.Source {
	env := pipeline.EnvOf(src)
	ways := env.Helpers() + 1
	convert := func(pix []uint8) {
		var lastIn, lastOut [3]uint8
		have := false
		for i := 0; i+3 < len(pix); i += 4 {
			p := pix[i : i+4 : i+4]
			if p[3] == 0 {
				continue
			}
			in := [3]uint8{p[0], p[1], p[2]}
			if have && in == lastIn {
				p[0], p[1], p[2] = lastOut[0], lastOut[1], lastOut[2]
				continue
			}
			p[0], p[1], p[2] = t.Convert(p[0], p[1], p[2])
			lastIn, lastOut, have = in, [3]uint8{p[0], p[1], p[2]}, true
		}
	}
	return pipeline.Map(src, func(strip *image.NRGBA) error {
		rows := strip.Rect.Dy()
		parts := max(1, min(ways, rows, rows*strip.Rect.Dx()/(8<<10)))
		row := 4 * strip.Rect.Dx()
		env.Parallel(parts, func(k int) {
			for y := k * rows / parts; y < (k+1)*rows/parts; y++ {
				convert(strip.Pix[y*strip.Stride:][:row])
			}
		})
		return nil
	})
}
