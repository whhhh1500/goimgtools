package vp8enc

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"math"
	"math/rand"
	"testing"

	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

var maskKinds = []string{"noise", "sparse", "dense", "disc", "cols", "rows", "check", "zero", "one"}

// maskImage is a picture whose alpha plane is a mask of blocks of cell x
// cell pixels, of the given character: which blocks an animation's frame
// leaves out (disc and sparse are the likeliest), and what tries the coder.
func maskImage(kind string, w, h, cell int) *image.NRGBA {
	m := image.NewNRGBA(image.Rect(0, 0, w, h))
	r := rand.New(rand.NewSource(int64(w*131 + h*17 + cell)))
	bw, bh := (w+cell-1)/cell, (h+cell-1)/cell
	opaque := make([]bool, bw*bh)
	for by := 0; by < bh; by++ {
		for bx := 0; bx < bw; bx++ {
			var o bool
			switch kind {
			case "noise":
				o = r.Intn(2) == 0
			case "sparse": // a few blocks left out
				o = r.Intn(10) != 0
			case "dense": // a few blocks coded
				o = r.Intn(10) == 0
			case "disc": // what moves is in the middle
				o = math.Hypot(float64(bx)-float64(bw)/2, float64(by)-float64(bh)/2) < float64(min(bw, bh))*0.35
			case "cols":
				o = bx%3 == 0
			case "rows":
				o = by%3 != 0
			case "check":
				o = (bx+by)&1 == 0
			case "one":
				o = bx != bw/2 || by != bh/2
			}
			opaque[by*bw+bx] = o
		}
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			a := uint8(0)
			if opaque[y/cell*bw+x/cell] {
				a = 0xff
			}
			m.SetNRGBA(x, y, color.NRGBA{uint8(3 * x), uint8(5 * y), uint8(x + y), a})
		}
	}
	return m
}

// TestBlockMask: an alpha plane that is a mask of blocks, coded by the mask
// coder, comes back exactly, at every size and block size, and takes about
// what the lossless encoder makes of it; a plane that is no such mask is
// coded as without the hint.
func TestBlockMask(t *testing.T) {
	sizes := [][2]int{{150, 150}, {720, 90}, {1, 1}, {7, 3}, {8, 8}, {9, 17}, {16, 16}, {33, 65}, {300, 7}, {5, 300}, {257, 129}, {1134, 464}}
	for _, cell := range []int{4, 8} {
		for _, sz := range sizes {
			w, h := sz[0], sz[1]
			for _, kind := range maskKinds {
				m := maskImage(kind, w, h, cell)
				transparent := false
				for i := 3; i < len(m.Pix); i += 4 {
					transparent = transparent || m.Pix[i] == 0
				}
				what := fmt.Sprintf("%s %dx%d blocks of %d", kind, w, h, cell)
				env := pipeline.NewEnv(0, t.TempDir())
				dbg := &debug{}
				var buf bytes.Buffer
				if err := encode(&buf, testutil.ChoppedEnv(m, 11, env), &Options{Quality: 60, AlphaBlocks: cell}, dbg); err != nil {
					t.Fatalf("%s: %v", what, err)
				}
				if env.Budget.Used() != 0 {
					t.Errorf("%s: budget still reserved", what)
				}
				d := decodeX(t, buf.Bytes())
				if !transparent {
					if d.alpha != nil || dbg.maskBits != 0 {
						t.Errorf("%s: an opaque picture with an alpha plane", what)
					}
					continue
				}
				if d.alpha == nil {
					t.Fatalf("%s: no alpha", what)
				}
				for i := 0; i < w*h; i++ {
					if d.alpha[i] != m.Pix[4*i+3] {
						t.Fatalf("%s: alpha differs at %d,%d", what, i%w, i/w)
					}
				}
				// The mask is what the file holds, unless the plane as it
				// is takes less (a few pixels).
				alph := riffChunks(t, buf.Bytes())["ALPH"]
				coded := 1 + int(dbg.maskBits+7)/8
				switch {
				case dbg.maskBits == 0:
					t.Fatalf("%s: not coded as a mask", what)
				case coded < 1+w*h && (len(alph) != coded || alph[0] != 1):
					t.Fatalf("%s: a mask of %d bytes, an ALPH chunk of %d with header %#x", what, coded, len(alph), alph[0])
				case len(alph) > 1+w*h:
					t.Fatalf("%s: an ALPH chunk of %d bytes for %d pixels", what, len(alph), w*h)
				}
				// Against the lossless encoder, on planes large enough for
				// the difference to be more than a few bytes of header. (A
				// pattern that repeats exactly, which no frame of an
				// animation leaves, it codes in half: it copies from any
				// distance, the mask coder from next door.)
				if w*h < 10000 || kind == "cols" || kind == "rows" || kind == "check" {
					continue
				}
				ref := riffChunks(t, mustEncode(t, pipeline.FromImage(m, nil), 60, nil))["ALPH"]
				if len(alph) > len(ref)*11/10+8 {
					t.Errorf("%s: %d bytes, the lossless encoder %d", what, len(alph), len(ref))
				}
				if kind == "disc" || kind == "sparse" {
					t.Logf("%s: %d bytes, the lossless encoder %d", what, len(alph), len(ref))
				}
			}
		}
	}

	// Not a mask of blocks: the hint is passed over, and the file is the
	// one written without it.
	for _, name := range alphaKinds {
		if name == "zero" {
			continue // wholly transparent: a mask, of blocks of any size
		}
		m := alphaImage(name, 150, 100)
		dbg := &debug{}
		var buf bytes.Buffer
		if err := encode(&buf, pipeline.FromImage(m, nil), &Options{Quality: 60, AlphaBlocks: 8}, dbg); err != nil {
			t.Fatal(err)
		}
		if dbg.maskBits != 0 {
			t.Errorf("%s: coded as a mask of blocks", name)
		}
		if !bytes.Equal(buf.Bytes(), mustEncode(t, pipeline.FromImage(m, nil), 60, nil)) {
			t.Errorf("%s: the hint changed the file", name)
		}
	}
	// A mask of blocks of 8 is one of blocks of 4 too, not the other way.
	m := maskImage("noise", 96, 64, 4)
	dbg := &debug{}
	if err := encode(&bytes.Buffer{}, pipeline.FromImage(m, nil), &Options{AlphaBlocks: 8}, dbg); err != nil || dbg.maskBits != 0 {
		t.Errorf("blocks of 4 taken for blocks of 8: %d bits, %v", dbg.maskBits, err)
	}
	m = maskImage("noise", 96, 64, 8)
	if err := encode(&bytes.Buffer{}, pipeline.FromImage(m, nil), &Options{AlphaBlocks: 4}, dbg); err != nil || dbg.maskBits == 0 {
		t.Errorf("blocks of 8 not taken for blocks of 4: %v", err)
	}
}

// TestBlockMaskParse: the parse of a mask covers every packed pixel once,
// with copies that are what they say, whatever places they may come from.
func TestBlockMaskParse(t *testing.T) {
	for _, cell := range []int{4, 8, 16} {
		for _, sz := range [][2]int{{1, 1}, {8, 5}, {9, 40}, {70, 33}, {300, 300}, {40000, 9}} {
			w, h := sz[0], sz[1]
			r := rand.New(rand.NewSource(int64(w + h + cell)))
			m := &blockMask{w: w, h: h, cell: cell, pw: (w + 7) >> 3, bh: (h + cell - 1) / cell}
			m.rows = make([]uint8, m.pw*m.bh)
			for i := range m.rows {
				// Runs, as masks have them.
				if i == 0 || r.Intn(4) == 0 {
					m.rows[i] = [4]uint8{0, 0xff, 0x0f, 0xf0}[r.Intn(4)]
				} else {
					m.rows[i] = m.rows[i-1]
				}
			}
			total := m.pw * h
			for _, nd := range []int{1, 2, 4} {
				for _, minMatch := range []int{2, 3} {
					dist := [4]int{m.pw, 1, m.pw + 1, max(m.pw-1, 1)}
					p := 0
					m.parse(nd, minMatch, func(lit uint8, sym, n int) {
						if n == 0 {
							if lit != m.at(p) {
								t.Fatalf("%dx%d cell %d: literal %#x at %d, the plane has %#x", w, h, cell, lit, p, m.at(p))
							}
							p++
							return
						}
						if sym >= nd || n < minMatch || n > maskMaxMatch || dist[sym] > p || p+n > total {
							t.Fatalf("%dx%d cell %d: copy of %d from place %d at %d of %d", w, h, cell, n, sym, p, total)
						}
						for i := 0; i < n; i++ {
							if m.at(p+i) != m.at(p+i-dist[sym]) {
								t.Fatalf("%dx%d cell %d: copy of %d from place %d at %d is wrong at %d", w, h, cell, n, sym, p, i)
							}
						}
						p += n
					})
					if p != total {
						t.Fatalf("%dx%d cell %d: the parse covers %d of %d", w, h, cell, p, total)
					}
				}
			}
		}
	}
}
