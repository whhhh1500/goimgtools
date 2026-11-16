package vp8lenc

// The optimal parse: instead of deciding match by match, the LZ77 stage
// takes a stretch of pixels, finds the best match at each, and chooses the
// cheapest way through the stretch (from its end back to its start, the
// least cost from each pixel on: a literal, or the match at any length
// that can pay). This is the idea of libwebp's TraceBackwards
// (src/enc/backward_references_cost_enc.c; Copyright 2012 Google Inc., BSD
// licence, see licenses/LICENSE-LIBWEBP), and so are the weights given to
// literals (82%) and colour cache hits (68%) against what the statistics
// make them cost: the prefix codes made for each region of the image
// later code them for less than the statistics of the whole stream say.
// The rest is written for this package: a backward pass over a stretch at
// a time, as the pixels stream through the window.
//
// It is used for the variants with the predictor and with the colour
// table (photographs, palette images: 2 to 8% smaller); on the raw pixels
// of screenshots the greedy parse comes out smaller.

// optimalStretch is the most pixels parsed at once.
const optimalStretch = 8 << 10

// optimalBytes is the memory of the optimal parse, per tokenizer.
const optimalBytes = (optimalStretch+2)*(4+4+4+4+2+4) + 4<<maxCacheBits

// maxCacheBits is the largest colour cache tried.
const maxCacheBits = 10

type optimal struct {
	ml, md    []int32  // the match at each pixel of the stretch: length (0: none), distance
	lit       []uint32 // the cost of each pixel as a literal
	cte       []uint32 // the least cost from each pixel to the end of the stretch
	choice    []uint16 // the length taken at each pixel (0: a literal)
	nextBreak []int32  // the next pixel whose match does not just continue the one before
	sim       []uint32 // the colour cache, simulated
}

func newOptimal() *optimal {
	n := optimalStretch + 1
	return &optimal{
		ml: make([]int32, n), md: make([]int32, n),
		lit: make([]uint32, n), cte: make([]uint32, n),
		choice: make([]uint16, n), nextBreak: make([]int32, n+1),
		sim: make([]uint32, 0, 1<<maxCacheBits),
	}
}

// inheritAbove: inside a match longer than this, the match at the next
// pixel is taken to be the same one, a pixel shorter, without a search.
const inheritAbove = 64

// processOptimal is process with the optimal parse.
func (t *tokenizer) processOptimal(final bool) {
	end := t.base + t.n
	stop := end
	if !final {
		stop -= maxMatch + 2
	}
	o := t.opt
	if !final && t.n < len(t.buf) && stop-t.cur < optimalStretch {
		// Wait for a whole stretch, unless the buffer is full: matches end
		// at the end of a stretch, and rows come one at a time.
		return
	}
	for t.cur < stop {
		if n := t.st.nLit + t.st.nCopy; n >= t.nextCM {
			t.cm.update(t.st)
			if t.nextCM < 1<<18 {
				t.nextCM *= 4
			} else {
				t.nextCM += 1 << 18
			}
		}
		n := min(stop, t.cur+optimalStretch) - t.cur
		ml, md, lit, cte, choice, nb := o.ml[:n+1], o.md[:n+1], o.lit[:n+1], o.cte[:n+1], o.choice[:n+1], o.nextBreak[:n+2]
		for i := 0; i < n; i++ {
			t.insertTo(t.cur + i)
			if i > 0 && ml[i-1] > inheritAbove {
				ml[i], md[i] = ml[i-1]-1, md[i-1]
				continue
			}
			l, dist, _ := t.find(t.cur + i)
			if l < minMatch {
				l = 0
			}
			ml[i], md[i] = int32(min(l, n-i)), int32(dist)
		}
		t.insertTo(t.cur + n)
		// Where a match stops just continuing the one before: the places
		// where ending a long match can pay.
		nb[n], nb[n+1] = int32(n), int32(n)
		for j := n - 1; j >= 0; j-- {
			if j == 0 || ml[j] == 0 || md[j] != md[j-1] || ml[j] != ml[j-1]-1 {
				nb[j] = int32(j)
			} else {
				nb[j] = nb[j+1]
			}
		}
		// Literal costs, with the largest colour cache simulated over the
		// stretch as if every pixel went through it.
		hit := (t.cm.hit*68 + 50) / 100
		var shift uint
		if t.cm.hit != 0 {
			cc := &t.st.cands[len(t.st.cands)-1]
			o.sim = append(o.sim[:0], cc.tab...)
			shift = cc.shift
		}
		for i := 0; i < n; i++ {
			px := t.buf[t.cur+i-t.base]
			c := (t.cm.pixel(px)*82 + 50) / 100
			if t.cm.hit != 0 {
				k := px * cacheMult >> shift
				if o.sim[k] == px {
					c = min(c, hit)
				} else {
					o.sim[k] = px
				}
			}
			// A prefix code takes a bit at least, however common the
			// literal (the statistics alone say almost nothing for the
			// one colour of a flat area).
			lit[i] = max(c, 16)
		}
		cte[n] = 0
		for i := n - 1; i >= 0; i-- {
			best := lit[i] + cte[i+1]
			ch := uint16(0)
			if L := int(ml[i]); L >= minMatch {
				ds, dn, _ := prefixCode(distCode(int(md[i]), t.width))
				dc := uint32(t.cm.dist[ds]) + 16*uint32(dn)
				try := func(k int) {
					ls, ln, _ := prefixCode(uint32(k))
					if c := dc + uint32(t.cm.length[ls]) + 16*uint32(ln) + cte[i+k]; c < best {
						best, ch = c, uint16(k)
					}
				}
				for k := minMatch; k <= min(L, 32); k++ {
					try(k)
				}
				if L > 32 {
					for j := int(nb[i+33]); j-i < L; j = int(nb[j+1]) {
						try(j - i)
					}
					try(L)
				}
			}
			cte[i], choice[i] = best, ch
		}
		for i := 0; i < n; {
			if k := int(choice[i]); k > 0 {
				t.copyRef(t.cur+i, k, int(md[i]))
				i += k
			} else {
				t.literal(t.cur + i)
				i++
			}
		}
		t.cur += n
	}
}
