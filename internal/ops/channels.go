package ops

import (
	"image"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// ---- recomb, unflatten, channels ----

// Recomb mixes the channels of each pixel of src, as libvips' recomb
// under sharp's recomb: channel v of the result is the sum over u of
// m[v][u] times channel u (red, green, blue and, with alpha, alpha), in
// double precision, then single precision, then a level: kept within 0 to
// 255, its fraction dropped. Without alpha the alpha of each pixel is
// kept; with it, the fourth row makes the alpha.
func Recomb(src pipeline.Source, m [4][4]float64, alpha bool) pipeline.Source {
	n := 3
	if alpha {
		n = 4
		src = pipeline.WithAlpha(src)
	}
	// The products, each rounded as libvips rounds it, by level: the sums
	// are made of them in libvips' order.
	var prod [4][4][256]float64
	for v := 0; v < n; v++ {
		for u := 0; u < n; u++ {
			for l := range prod[v][u] {
				prod[v][u][l] = m[v][u] * float64(l)
			}
		}
	}
	return pipeline.Map(src, func(strip *image.NRGBA) error {
		for i := 0; i+3 < len(strip.Pix); i += 4 {
			p := strip.Pix[i : i+4 : i+4]
			var out [4]uint8
			for v := 0; v < n; v++ {
				t := prod[v][0][p[0]] + prod[v][1][p[1]] + prod[v][2][p[2]]
				if alpha {
					t += prod[v][3][p[3]]
				}
				out[v] = truncLevel(float64(float32(t)))
			}
			p[0], p[1], p[2] = out[0], out[1], out[2]
			if alpha {
				p[3] = out[3]
			}
		}
		return nil
	})
}

// Unflatten makes the white pixels of src transparent, as sharp's
// unflatten: those whose grey (libvips' B_W, see grey) is 255. The others
// keep their alpha.
func Unflatten(src pipeline.Source) pipeline.Source {
	t := labTables()
	return pipeline.Map(pipeline.WithAlpha(src), func(strip *image.NRGBA) error {
		for i := 0; i+3 < len(strip.Pix); i += 4 {
			p := strip.Pix[i : i+4 : i+4]
			if nearWhite(p[0], p[1], p[2]) && t.grey(p[0], p[1], p[2]) == 255 {
				p[3] = 0
			}
		}
		return nil
	})
}

// nearWhite reports whether a colour is near enough white that its grey
// may be 255 (checked on every colour by a test): only these are worked
// out.
func nearWhite(r, g, b uint8) bool { return g >= 254 && r >= 248 && b >= 220 }

// Channel makes an opaque greyscale image of channel c (0 red, 1 green,
// 2 blue, 3 alpha) of src, as sharp's extractChannel.
func Channel(src pipeline.Source, c int) pipeline.Source {
	return &grayOf{src: pipeline.Map(src, func(*image.NRGBA) error { return nil }), env: pipeline.EnvOf(src), ch: c}
}

// RemoveAlpha makes every pixel of src opaque, showing the colour it
// stores, as sharp's removeAlpha.
func RemoveAlpha(src pipeline.Source) pipeline.Source {
	if pipeline.IsOpaque(src) {
		return src
	}
	return opaqueSource{pipeline.Map(src, func(strip *image.NRGBA) error {
		for i := 3; i < len(strip.Pix); i += 4 {
			strip.Pix[i] = 255
		}
		return nil
	})}
}

// opaqueSource is a source known to be opaque.
type opaqueSource struct{ pipeline.Source }

func (opaqueSource) Opaque() bool { return true }

// EnsureAlpha gives the pixels of an opaque src the alpha level; a source
// that may have transparency is left as it is (sharp's ensureAlpha).
func EnsureAlpha(src pipeline.Source, level uint8) pipeline.Source {
	if !pipeline.IsOpaque(src) || level == 255 {
		return src
	}
	return pipeline.Map(pipeline.WithAlpha(src), func(strip *image.NRGBA) error {
		for i := 3; i < len(strip.Pix); i += 4 {
			strip.Pix[i] = level
		}
		return nil
	})
}
