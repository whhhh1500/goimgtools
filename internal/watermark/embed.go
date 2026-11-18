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
var kinds = [...]struct {
	density, minChip, flat, busy float64
}{
	Blind:  {density: 4, minChip: 2, flat: 0.75, busy: 3},
	Screen: {density: 4, minChip: 3, flat: 1.5, busy: 6},
}

// Options are the settings of a mark.
type Options struct {
	Kind     Kind
	Strength float64 // 0 means 1
	Density  float64 // tiles along the shorter side; 0 means the kind's
}

// MaxDensity is the most tiles along the shorter side.
const MaxDensity = 16

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
	m.chip = max(set.minChip, float64(min(w, h))/float64(density*N))
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
		// moved away from the end by what it needs, at most amp.
		mlo, mhi := lo/n, hi/n
		shift := max(0, amp-mlo) - max(0, amp-(255-mhi))
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
