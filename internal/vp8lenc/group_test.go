package vp8lenc

import (
	"bytes"
	"fmt"
	"image"
	"math/rand/v2"
	"testing"

	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

func encodeWith(t *testing.T, m image.Image, effort int, replay bool) []byte {
	t.Helper()
	src := testutil.Chopped(m, 16)
	var buf bytes.Buffer
	b := src.Bounds()
	e := &encoder{env: pipeline.EnvOf(src), out: &buf, w: b.Dx(), h: b.Dy(), p: paramsFor(effort), replay: replay}
	defer e.close()
	if err := e.encode(src); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestGroupReplay: the replays of the grouper from the symbols the first
// one kept give the file that replaying the tokens every time gives.
func TestGroupReplay(t *testing.T) {
	imgs := map[string]image.Image{
		"natural":   testutil.Natural(300, 200, 3),
		"natural 2": testutil.Natural(257, 190, 8),
	}
	for name, m := range testutil.Images(160, 120) {
		imgs[name] = m
	}
	for _, name := range sorted(imgs) {
		for _, effort := range []int{2, 5, 9} {
			kept := encodeWith(t, imgs[name], effort, false)
			replayed := encodeWith(t, imgs[name], effort, true)
			if !bytes.Equal(kept, replayed) {
				t.Errorf("%s effort %d: %d bytes from the kept symbols, %d replaying the tokens", name, effort, len(kept), len(replayed))
			}
			checkFile(t, fmt.Sprintf("%s effort %d", name, effort), kept, imgs[name])
		}
	}
}

// TestKeepMem: what a grouper keeping its blocks' symbols allocates for
// them stays within keepMem of the symbols it was given.
func TestKeepMem(t *testing.T) {
	r := rand.New(rand.NewPCG(5, 6))
	for _, c := range []struct {
		w, h      int
		hbits     uint
		cacheBits uint
		spread    int // distinct values per channel
	}{
		{300, 200, 2, 0, 256}, {300, 200, 3, 10, 256}, {1000, 700, 5, 10, 256},
		{640, 480, 4, 0, 3}, {97, 31, 2, 4, 40}, {2000, 300, 6, 10, 256},
	} {
		// The symbols are counted as they are made; the grouper is told
		// their number, as the encoder tells it the bound from its stats.
		type sym struct {
			x, y, kind int
			a, b       uint32
		}
		var syms []sym
		var words int64
		for y := 0; y < c.h; y++ {
			for x := 0; x < c.w; x++ {
				switch r.IntN(8) {
				case 0:
					syms = append(syms, sym{x, y, 0, uint32(1 + r.IntN(50)), uint32(1 + r.IntN(1000))})
					words += 2
				case 1:
					if c.cacheBits != 0 {
						syms = append(syms, sym{x, y, 1, uint32(r.IntN(1 << c.cacheBits)), 0})
						words++
						continue
					}
					fallthrough
				default:
					v := func() uint32 { return uint32(r.IntN(c.spread)) }
					syms = append(syms, sym{x, y, 2, v()<<24 | v()<<16 | v()<<8 | v(), 0})
					words += 4
				}
			}
		}
		g := newGrouper(c.w, c.h, c.hbits, c.cacheBits, 8, false, &grouperScratch{}, true, words)
		for _, s := range syms {
			switch s.kind {
			case 0:
				g.copyRef(s.x, s.y, int(s.a), s.b)
			case 1:
				g.cacheHit(s.x, s.y, s.a)
			default:
				g.literal(s.x, s.y, s.a)
			}
		}
		g.endReplay()
		if !g.keep {
			t.Fatalf("%+v: symbols not kept", c)
		}
		used := int64(len(g.blkAt))*4 + int64(len(g.blkLen))*2 + int64(cap(g.dec))*4
		for _, ch := range g.blks {
			used += int64(cap(ch)) * 2
		}
		if most := keepMem(c.w, c.h, c.hbits, words, 1); used > most {
			t.Errorf("%+v: %d bytes, keepMem %d", c, used, most)
		}
	}
}
