package watermark

import (
	"errors"
	"math"
)

// Kind is what the mark is made to survive.
type Kind int

const (
	// Blind survives what happens to a file: re-encoding, resizing,
	// cropping, turning, brightness and contrast, screenshots.
	Blind Kind = iota
	// Screen also survives a photograph of a screen showing the picture:
	// larger chips, which a camera's lens keeps, and a stronger mark.
	Screen
)

// The settings of each kind: the default number of tiles along the shorter
// side, the smallest chip in pixels, and the mark's amplitude in levels
// (0 to 255) where the picture is flat and where it is busy, at Strength 1.
// The amplitudes make each kind at Strength 1 about as visible by
// SSIMULACRA2 as it was before the sync pattern and the payload were tilted
// to the higher frequencies (0.84375 and 3.375, 1.665 and 6.66; see
// shapeWeight), which those were with the tile of 40% reference (0.75 and
// 3, 1.5 and 6; see wRef). The reference moved up to 12 to 21 cycles
// (refMin) left the blind mark about as visible and the screen mark less.
// The tile evened (evenEnergy) is less visible as strong: the amplitudes
// are 2.5% higher to be as visible by SSIMULACRA2 as before (0.9703125 and
// 3.88125, 1.8315 and 7.326). (Measured with 3 keys on the 24 Kodak
// photographs after the 18 attacks made with Pillow: 1236 readings of 1296
// against 1234, SSIMULACRA2 77.8 against 77.9, butteraugli 2.57 against
// 2.81; as strong as before, 1230, crops to 40% 51 against 55: the
// reference loses 0.4 dB of its share.) Evened at the coarser scale too
// (evenLow), they are 3% higher again (0.9945703125 and 3.97828125,
// 1.8772875 and 7.50915 before).
// (Measured for Blind: chips of at least 2.5 or 3 pixels, which JPEG and
// WebP keep better, leave small pictures too few tiles and read less
// over all. As visible by SSIMULACRA2, less where flat and more where busy
// reads about 5% more after JPEG and WebP but is more visible by
// butteraugli; more only where strongly textured reads less. The just
// noticeable difference of Wu et al., as Meta's PixelSeal weighs its mark
// by (more in dark places and along edges), read less on the 24 Kodak
// photographs after the 22 attacks, about as visible: 294 of 528 against
// 330 at SSIMULACRA2 86, 400 against 430 at 77.)
var kinds = [...]struct {
	density, minChip, flat, busy float64
}{
	Blind:  {density: 4, minChip: 2, flat: 1.024407421875, busy: 4.0976296875},
	Screen: {density: 4, minChip: 3, flat: 1.933606125, busy: 7.7344245},
}

// Options are the settings of a mark.
type Options struct {
	Kind     Kind
	Strength float64 // 0 means 1
	Density  float64 // tiles along the shorter side; 0 means the kind's
	// Texture is the picture's texture (TextureMeter), where NeedsTexture
	// says it is wanted; 0 when not measured.
	Texture float64
}

// MaxDensity is the most tiles along the shorter side.
const MaxDensity = 16

// smallSide is the shorter side below which the blind mark's chips go
// down to a pixel, where they would otherwise be the smallest of the kind
// and the picture is busy enough (fineTexture): chips of 2 pixels leave
// such a picture two tiles or fewer each way, too few to read even
// untouched. (Measured on the Kodak pictures made
// smaller, of 24, untouched / JPEG 80 / WebP 80: at 384 x 256 16, 16, 13
// against 24, 24, 24; at 256 x 171 7, 5, 0 against 24, 21, 22; at 448 x
// 299 21, 22, 17 against 24, 24, 24. Chips of 1.25 or 1.5 pixels, fewer
// tiles, read less; chips of a little over 1, as the density gives, read
// less after JPEG 50. From a shorter side of 300 up chips of a pixel read
// more after JPEG and WebP and crops but nothing once the picture is
// halved: 640 x 360 with 3 keys, 243 of 330 against 245.)
const smallSide = 300

// Marker adds a mark to the rows of an image of a given size.
type Marker struct {
	w, h       int
	chip       float64
	r          int // the radius of the window the mask is taken over
	flat, busy float64
	tile       []float64
	cx0        []int32 // per column: the chip to the left of the pixel's centre
	cfx        []float64
}

// NewMarker prepares the mark of payload (1 to MaxPayload bytes) with the
// key for an image of w x h pixels.
func NewMarker(payload []byte, k Key, o Options, w, h int) (*Marker, error) {
	if len(payload) < 1 || len(payload) > MaxPayload {
		return nil, errors.New("the mark must be 1 to 32 bytes")
	}
	if int(o.Kind) < 0 || int(o.Kind) >= len(kinds) {
		return nil, errors.New("unknown kind of mark")
	}
	set := kinds[o.Kind]
	strength := o.Strength
	if strength == 0 {
		strength = 1
	}
	if !(strength > 0 && strength <= 16) {
		return nil, errors.New("strength must be above 0 and at most 16")
	}
	density := o.Density
	if density == 0 {
		density = set.density
	}
	if !(density >= 1 && density <= MaxDensity) {
		return nil, errors.New("density must be 1 to 16")
	}
	m := &Marker{w: w, h: h, flat: float64(strength * set.flat), busy: float64(strength * set.busy),
		tile: tile(newPatterns(k), payload, k)}
	m.chip = float64(min(w, h)) / float64(density*N)
	if m.chip < set.minChip {
		m.chip = set.minChip
		if o.Kind == Blind && min(w, h) < smallSide && o.Texture >= fineTexture {
			m.chip = 1
		}
	}
	m.r = max(1, int(math.Round(m.chip)))
	m.cx0 = make([]int32, w)
	m.cfx = make([]float64, w)
	for x := range w {
		c := (float64(x)+0.5)/m.chip - 0.5
		f := math.Floor(c)
		m.cx0[x] = int32(mod(int(f), N))
		m.cfx[x] = c - f
	}
	return m, nil
}

// Radius is the number of rows above and below a row that Row needs.
func (m *Marker) Radius() int { return m.r }

// rowScratch is a row's working storage: the window's column sums.
type rowScratch struct {
	sum, sum2, lo, hi []float64
}

// NewRow returns a function that marks row y of NRGBA pixels: win holds
// the rows y-Radius to y+Radius (repeated past the edges), dst receives
// the marked row. One function for each goroutine.
func (m *Marker) NewRow() func(y int, win [][]uint8, dst []uint8) {
	s := &rowScratch{sum: make([]float64, m.w), sum2: make([]float64, m.w), lo: make([]float64, m.w), hi: make([]float64, m.w)}
	return func(y int, win [][]uint8, dst []uint8) { m.row(s, y, win, dst) }
}

const nvfTheta = 75.0 / 400.0 // the noise visibility function's theta

// shiftTexture is the variance of the picture around a place (levels
// squared) at which a place near white or black is moved away from the end
// by three quarters of what the mark needs; flat, by all of it, busy, by
// half. Moved alike, the bright parts of a photograph darkened with their
// texture, which butteraugli found first in 5 of the 24 Kodak photographs.
// (Measured with 3 keys on the 24 Kodak photographs after the 18 attacks
// made with Pillow, at Strength 0.75: butteraugli 1.78 against 1.86,
// SSIMULACRA2 84.1 against 84.0, 1217 readings of 1296 against 1218; on the
// 5 pictures of the first set after the 22 attacks made with this package,
// 281 of 330 against 280, the screen mark 295 against 296 and less visible,
// SSIMULACRA2 52.8 against 51.4. Every place moved by half lost the mostly
// white screenshot after JPEG 70: 275 against 280.)
const shiftTexture = 4.0

func (m *Marker) row(s *rowScratch, y int, win [][]uint8, dst []uint8) {
	w, r := m.w, m.r
	// Down each column of the window: luma, its square, the darkest and
	// the brightest channel.
	for x := range w {
		var sum, sum2, lo, hi float64
		for _, row := range win {
			p := row[4*x : 4*x+3 : 4*x+3]
			cr, cg, cb := float64(p[0]), float64(p[1]), float64(p[2])
			l := float64(0.299*cr) + float64(0.587*cg) + float64(0.114*cb)
			sum += l
			sum2 += float64(l * l)
			lo += min(cr, cg, cb)
			hi += max(cr, cg, cb)
		}
		s.sum[x], s.sum2[x], s.lo[x], s.hi[x] = sum, sum2, lo, hi
	}
	// The field's row: the chips above and below the row's centre.
	c := (float64(y)+0.5)/m.chip - 0.5
	f := math.Floor(c)
	fy := c - f
	y0 := mod(int(f), N)
	t0, t1 := m.tile[y0*N:y0*N+N], m.tile[mod(y0+1, N)*N:][:N]
	n := float64(len(win) * (2*r + 1))
	// Across: a running sum over the window's columns, edges repeated.
	var sum, sum2, lo, hi float64
	for i := -r; i <= r; i++ {
		x := max(0, min(i, w-1))
		sum, sum2, lo, hi = sum+s.sum[x], sum2+s.sum2[x], lo+s.lo[x], hi+s.hi[x]
	}
	for x := range w {
		mean := sum / n
		v := max(0, sum2/n-float64(mean*mean))
		nvf := 1 / (1 + float64(nvfTheta*v))
		amp := float64(m.busy*(1-nvf)) + float64(m.flat*nvf)
		// Near white or black the mark cannot go both ways: the place is
		// moved away from the end by what it needs, at most amp; where the
		// picture has texture (a sky's clouds), by about half of that (see
		// shiftTexture).
		mlo, mhi := lo/n, hi/n
		share := 0.5 + 0.5/(1+float64(v/shiftTexture))
		shift := float64(share * (max(0, amp-mlo) - max(0, amp-(255-mhi))))
		room := min(mlo+shift, 255-(mhi+shift))
		amp = min(amp, max(room, 0)+0.3)
		// The field: bilinear between the chips' centres.
		x0 := m.cx0[x]
		x1 := x0 + 1
		if x1 == N {
			x1 = 0
		}
		fx := m.cfx[x]
		a := float64(t0[x0]*(1-fx)) + float64(t0[x1]*fx)
		b := float64(t1[x0]*(1-fx)) + float64(t1[x1]*fx)
		d := shift + float64(amp*(float64(a*(1-fy))+float64(b*fy)))
		src, out := win[len(win)/2][4*x:4*x+4:4*x+4], dst[4*x:4*x+4:4*x+4]
		for ch := range 3 {
			out[ch] = uint8(max(0, min(255, math.Round(float64(src[ch])+d))))
		}
		out[3] = src[3]
		// Slide the window one column right.
		xo, xi := max(0, x-r), min(w-1, x+r+1)
		sum += s.sum[xi] - s.sum[xo]
		sum2 += s.sum2[xi] - s.sum2[xo]
		lo += s.lo[xi] - s.lo[xo]
		hi += s.hi[xi] - s.hi[xo]
	}
}
