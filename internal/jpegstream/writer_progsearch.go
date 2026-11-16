package jpegstream

// The scan script of a progressive file is searched for, as mozjpeg does it
// by default ("optimize_scans": jcparam.c, jpeg_search_progression;
// jcmaster.c, select_scans; Copyright (C) 2014, Mozilla Corporation, under
// the IJG and BSD licences of libjpeg-turbo, see licenses/THIRD-PARTY.md).
// The pixels a decoder gets are the same whatever the script; only how
// well the coefficients compress differs, by up to 15% on photographs.
//
// The DC coefficients of all components go in one scan, whole. The AC
// coefficients of luminance, and those of the two chrominance components
// together, are a group each, for which two things are chosen:
//
//   - how many low bits (Al, 0 to 3 for luminance, 0 to 2 for chrominance)
//     the first scans leave to refinement scans, one per bit;
//   - where the first scans split the coefficients in two bands: after
//     coefficient 2, 5, 8, 12 or 18 of the zig-zag order, or not at all.
//
// Every choice is priced by the bits its scans take with Huffman tables
// made for them, plus their tables and headers. mozjpeg encodes each
// candidate scan; here a candidate is a scan run in counting mode, which
// gives the same statistics. The candidates that do not depend on Al are
// counted while the image is quantized; once Al is chosen, the band splits
// are counted in one more pass over the stored coefficients. The scans of
// the chosen script keep their statistics for writing.

// freqSplits are where the first scans of a group may split their band.
var freqSplits = [...]int{2, 8, 5, 12, 18}

// progGroup is the AC coefficients of one or two components.
type progGroup struct {
	comps []int
	alMax int
	// first[al] are the scans of coefficients 1..8 and 9..63 without their
	// low al bits, two per component; refine[al] the refinement scans of
	// bit al, one per component.
	first  [][]*progScan
	refine [][]*progScan
	al     int
	// splits[0] codes 1..63 at al, splits[i+1] the bands of freqSplits[i].
	splits [][]*progScan
	split  int
}

func newProgGroup(e *encoder, comps []int, alMax int) *progGroup {
	g := &progGroup{comps: comps, alMax: alMax}
	for al := 0; al <= alMax; al++ {
		var first []*progScan
		for _, ci := range comps {
			first = append(first,
				&progScan{e: e, gather: true, comps: []int{ci}, ss: 1, se: 8, al: al},
				&progScan{e: e, gather: true, comps: []int{ci}, ss: 9, se: 63, al: al})
		}
		g.first = append(g.first, first)
		if al < alMax {
			var refine []*progScan
			for _, ci := range comps {
				refine = append(refine, &progScan{e: e, gather: true, comps: []int{ci}, ss: 1, se: 63, ah: al + 1, al: al})
			}
			g.refine = append(g.refine, refine)
		}
	}
	return g
}

// chooseAl picks the number of bits left to refinement scans, and makes
// the band split candidates for it.
func (g *progGroup) chooseAl(e *encoder) {
	best := int64(-1)
	for al := 0; al <= g.alMax; al++ {
		c := cost(g.first[al])
		for i := 0; i < al; i++ {
			c += cost(g.refine[i])
		}
		if best < 0 || c < best {
			best, g.al = c, al
		}
	}
	whole := []*progScan{}
	for _, ci := range g.comps {
		whole = append(whole, &progScan{e: e, gather: true, comps: []int{ci}, ss: 1, se: 63, al: g.al})
	}
	g.splits = [][]*progScan{whole}
	for _, s := range freqSplits {
		var c []*progScan
		for _, ci := range g.comps {
			c = append(c,
				&progScan{e: e, gather: true, comps: []int{ci}, ss: 1, se: s, al: g.al},
				&progScan{e: e, gather: true, comps: []int{ci}, ss: s + 1, se: 63, al: g.al})
		}
		g.splits = append(g.splits, c)
	}
}

func (g *progGroup) chooseSplit() {
	best := int64(-1)
	for i, c := range g.splits {
		if v := cost(c); best < 0 || v < best {
			best, g.split = v, i
		}
	}
}

func cost(scans []*progScan) int64 {
	var n int64
	for _, s := range scans {
		n += s.cost()
	}
	return n
}

// progSearch is the search for the script.
type progSearch struct {
	dc     *progScan
	groups []*progGroup // luminance, then chrominance
}

func newProgSearch(e *encoder, nComponent int) *progSearch {
	ps := &progSearch{}
	if nComponent == 3 {
		ps.dc = &progScan{e: e, gather: true, comps: []int{0, 1, 2}}
		ps.groups = []*progGroup{newProgGroup(e, []int{0}, 3), newProgGroup(e, []int{1, 2}, 2)}
	} else {
		ps.dc = &progScan{e: e, gather: true, comps: []int{0}}
		ps.groups = []*progGroup{newProgGroup(e, []int{0}, 3)}
	}
	return ps
}

// counted are the scans counted while the image is quantized.
func (ps *progSearch) counted() []*progScan {
	out := []*progScan{ps.dc}
	for _, g := range ps.groups {
		for _, c := range g.first {
			out = append(out, c...)
		}
		for _, c := range g.refine {
			out = append(out, c...)
		}
	}
	return out
}

// splitScans are the band split candidates, once Al is chosen.
func (ps *progSearch) splitScans() []*progScan {
	var out []*progScan
	for _, g := range ps.groups {
		g.chooseAl(ps.dc.e)
		for _, c := range g.splits {
			out = append(out, c...)
		}
	}
	return out
}

// script is the chosen script, in mozjpeg's order: DC; each group's first
// scans and its refinements down to the lower of the two groups' Al; then
// the remaining refinements of both, bit by bit.
func (ps *progSearch) script() []*progScan {
	out := []*progScan{ps.dc}
	minAl := ps.groups[0].al
	for _, g := range ps.groups {
		g.chooseSplit()
		minAl = min(minAl, g.al)
	}
	for _, g := range ps.groups {
		out = append(out, g.splits[g.split]...)
		for al := g.al - 1; al >= minAl; al-- {
			out = append(out, g.refine[al]...)
		}
	}
	for al := minAl - 1; al >= 0; al-- {
		for _, g := range ps.groups {
			out = append(out, g.refine[al]...)
		}
	}
	return out
}

// cost is the size of the scan in bits, written with Huffman tables made
// for it: its codes and extra bits, its tables and its header.
func (s *progScan) cost() int64 {
	bits := s.extra + 7 // the extra bits; at most 7 to pad the last byte
	header := int64(2 + 2 + 1 + 2*len(s.comps) + 3)
	var tables []int
	switch {
	case s.ss == 0 && s.ah == 0:
		tables = []int{0, 1}[:min(len(s.comps), 2)]
	case s.ss != 0:
		tables = []int{min(s.comps[0], 1)}
	}
	if len(tables) > 0 {
		header += 4
	}
	for _, t := range tables {
		freq := s.freq[t] // optimalSpec destroys it; the scan may be written
		spec := optimalSpec(&freq)
		header += 1 + 16 + int64(len(spec.value))
		var lut codeLUT
		lut.init(&spec)
		for v, f := range s.freq[t][:256] {
			bits += f * int64(lut[v]>>24)
		}
	}
	return bits + 8*header
}
