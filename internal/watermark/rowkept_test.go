package watermark

import (
	"bytes"
	"image"
	"math"
	"testing"

	"github.com/whhhh1500/goimgtools/internal/testutil"
)

// columnsLong is the window's column sums the long way: at every pixel
// the window's rows are added up anew, the darkest and brightest channel
// as the luma is.
func columnsLong(win [][]uint8, w int) [][4]float64 {
	col := make([][4]float64, w)
	for x := range w {
		var sum, sum2, lo, hi float64
		for _, row := range win {
			p := row[4*x : 4*x+3]
			cr, cg, cb := float64(p[0]), float64(p[1]), float64(p[2])
			l := float64(0.299*cr) + float64(0.587*cg) + float64(0.114*cb)
			sum += l
			sum2 += float64(l * l)
			lo += min(cr, cg, cb)
			hi += max(cr, cg, cb)
		}
		col[x] = [4]float64{sum, sum2, lo, hi}
	}
	return col
}

// rowLong marks a row from the column sums made the long way.
func (m *Marker) rowLong(y int, win [][]uint8, dst []uint8) {
	w, r := m.w, m.r
	col := columnsLong(win, w)
	c := (float64(y)+0.5)/m.chip - 0.5
	f := math.Floor(c)
	fy := c - f
	y0 := mod(int(f), N)
	t0, t1 := m.tile[y0*N:y0*N+N], m.tile[mod(y0+1, N)*N:][:N]
	n := float64(len(win) * (2*r + 1))
	var sum, sum2, lo, hi float64
	for i := -r; i <= r; i++ {
		c := col[max(0, min(i, w-1))]
		sum, sum2, lo, hi = sum+c[0], sum2+c[1], lo+c[2], hi+c[3]
	}
	for x := range w {
		mean := sum / n
		v := max(0, sum2/n-float64(mean*mean))
		nvf := 1 / (1 + float64(nvfTheta*v))
		amp := float64(m.busy*(1-nvf)) + float64(m.flat*nvf)
		mlo, mhi := lo/n, hi/n
		share := 0.5 + 0.5/(1+float64(v/shiftTexture))
		shift := float64(share * (max(0, amp-mlo) - max(0, amp-(255-mhi))))
		room := min(mlo+shift, 255-(mhi+shift))
		amp = min(amp, max(room, 0)+0.3)
		x0 := m.cx0[x]
		x1 := x0 + 1
		if x1 == N {
			x1 = 0
		}
		fx := m.cfx[x]
		a := float64(t0[x0]*(1-fx)) + float64(t0[x1]*fx)
		b := float64(t1[x0]*(1-fx)) + float64(t1[x1]*fx)
		d := shift + float64(amp*(float64(a*(1-fy))+float64(b*fy)))
		for ch := range 3 {
			dst[4*x+ch] = uint8(max(0, min(255, math.Round(float64(win[len(win)/2][4*x+ch])+d))))
		}
		dst[4*x+3] = win[len(win)/2][4*x+3]
		ci, co := col[min(w-1, x+r+1)], col[max(0, x-r)]
		sum, sum2, lo, hi = sum+(ci[0]-co[0]), sum2+(ci[1]-co[1]), lo+(ci[2]-co[2]), hi+(ci[3]-co[3])
	}
}

// TestRowKept: the rows of NewRow and of NewRowKept are, to the byte, the
// rows made the long way, and the column sums they are made from the same
// to the bit; on pictures like photographs with parts at white and at
// black, for windows of 5 to 23 rows, taller than the picture too; with
// the rows asked for in order, as the goroutines of a pipeline share out
// its strips (each its own function, a few rows of every strip), and out
// of order.
func TestRowKept(t *testing.T) {
	for i, c := range []struct {
		w, h int
		o    Options
	}{
		{640, 480, Options{Density: 1}},               // 17 rows
		{700, 720, Options{Density: 1, Kind: Screen}}, // 23 rows
		{301, 97, Options{}},                          // 5 rows
		{37, 200, Options{Strength: 3}},
		{200, 4, Options{}},                         // the window taller than the picture
		{90, 12, Options{Density: 1, Kind: Screen}}, // 7 rows of 12
		{3, 3, Options{}},
	} {
		w, h := c.w, c.h
		m, err := NewMarker([]byte("user-12345"), NewKey("k"), c.o, w, h)
		if err != nil {
			t.Fatal(err)
		}
		whole := testutil.Natural(720, 720, uint64(i)+3)
		src := image.NewNRGBA(image.Rect(0, 0, w, h))
		for y := range h {
			copy(src.Pix[y*src.Stride:][:4*w], whole.Pix[whole.PixOffset(0, y):])
			for x := range w {
				p := src.Pix[y*src.Stride+4*x:][:4]
				switch (x/23 + y/9) % 7 {
				case 2:
					p[0], p[1], p[2] = 255, 255, 255
				case 5:
					p[0], p[1], p[2] = 0, 0, 0
				}
				p[3] = uint8(x + 3*y)
			}
		}
		r := m.Radius()
		window := func(y int) [][]uint8 {
			win := make([][]uint8, 2*r+1)
			for i := range win {
				sy := max(0, min(h-1, y-r+i))
				win[i] = src.Pix[sy*src.Stride:][:4*w]
			}
			return win
		}
		want := make([][]uint8, h)
		sums := make([][][4]float64, h)
		for y := range want {
			want[y] = make([]uint8, 4*w)
			m.rowLong(y, window(y), want[y])
			sums[y] = columnsLong(window(y), w)
		}
		got := make([]uint8, 4*w)
		// A row function and the working storage of another like it (nil:
		// none): the row, and the sums.
		type maker struct {
			row func(int, [][]uint8, []uint8)
			s   *rowScratch
		}
		newMaker := func(kept bool) maker {
			if kept {
				return maker{m.NewRowKept(), m.newScratch(true)}
			}
			return maker{m.NewRow(), m.newScratch(false)}
		}
		check := func(how string, mk maker, y int) {
			t.Helper()
			win := window(y)
			clear(got)
			mk.row(y, win, got)
			if !bytes.Equal(got, want[y]) {
				t.Fatalf("%dx%d, window of %d rows, %s: row %d is not the row made the long way", w, h, 2*r+1, how, y)
			}
			if mk.s.kept != nil {
				m.columnsKept(mk.s, y, win)
			} else {
				m.columns(mk.s, win)
			}
			for x, c := range sums[y] {
				if s := [4]float64{mk.s.sum[x], mk.s.sum2[x], mk.s.lo[x], mk.s.hi[x]}; s != c {
					t.Fatalf("%dx%d, window of %d rows, %s: row %d column %d: sums %v, the long way %v", w, h, 2*r+1, how, y, x, s, c)
				}
			}
		}
		plain, kept := newMaker(false), newMaker(true)
		for y := range h {
			check("NewRow", plain, y)
			check("NewRowKept in order", kept, y)
		}
		// Strips of n rows shared out among parts, as ops.windowStage does.
		for _, sh := range [][2]int{{10, 4}, {16, 3}, {7, 7}, {64, 2}} {
			n, parts := sh[0], sh[1]
			rows := make([]maker, parts)
			for k := range rows {
				rows[k] = newMaker(true)
			}
			for y0 := 0; y0 < h; y0 += n {
				y1 := min(y0+n, h)
				for k := parts - 1; k >= 0; k-- {
					for y := y0 + k*(y1-y0)/parts; y < y0+(k+1)*(y1-y0)/parts; y++ {
						check("NewRowKept in parts", rows[k], y)
					}
				}
			}
		}
		// Out of order: back, the same row again, far, and near again.
		kept = newMaker(true)
		seed := uint32(i) + 1
		y := 0
		for range 6 * h {
			seed = seed*1664525 + 1013904223
			switch seed >> 29 {
			case 0:
				y = int(seed>>8) % h
			case 1:
				y = max(0, y-int(seed>>8)%(r+2))
			case 2:
			default:
				y = min(h-1, y+1+int(seed>>8)%(2*r+3)*int(seed>>28&1))
			}
			check("NewRowKept out of order", kept, y)
		}
		// A window that is not the one NewRowKept keeps rows for is worked
		// out as NewRow does, and what is kept stays good.
		short, long := window(h / 2)[1:], make([]uint8, 4*w)
		m.rowLong(h/2, short, long)
		kept.row(h/2, short, got)
		if !bytes.Equal(got, long) {
			t.Errorf("%dx%d: a window of %d rows through NewRowKept is not the row made the long way", w, h, len(short))
		}
		check("NewRowKept after another window", kept, min(h-1, h/2+1))
		if m.KeptBytes() < int64(2*r+1)*int64(w)*10 {
			t.Errorf("%dx%d: KeptBytes %d", w, h, m.KeptBytes())
		}
	}
}
