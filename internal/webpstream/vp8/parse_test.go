package vp8

import (
	"bytes"
	"math/rand/v2"
	"testing"
)

// TestParseAsm: parseBlockAsm reads the same bits as parseResiduals4Go and
// gives the same coefficients, result and reader state, for random input,
// probabilities (zeros likely and unlikely, so that every category of
// value comes), planes, contexts, factors, first positions and places in
// the window.
func TestParseAsm(t *testing.T) {
	if !parseAsm {
		t.Skip("no assembly")
	}
	rng := rand.New(rand.NewPCG(3, 4))
	data := make([]byte, 1200)
	var d Decoder
	var cats [8]int
	runs := 0
	for range 300000 {
		for j := range data {
			data[j] = byte(rng.Uint32())
		}
		bias := rng.IntN(4)
		for pl := range d.tokenProb {
			for b := range d.tokenProb[pl] {
				for c := range d.tokenProb[pl][b] {
					for k := range d.tokenProb[pl][b][c] {
						v := rng.IntN(256)
						switch bias {
						case 1:
							v = rng.IntN(48)
						case 2:
							v = 192 + rng.IntN(64)
						}
						d.tokenProb[pl][b][c][k] = uint8(v)
					}
				}
			}
		}
		var p partition
		p.init(bytes.NewReader(data), 0, len(data))
		for range 1 + rng.IntN(800) {
			p.readBit(uint8(rng.IntN(256)))
		}
		if p.unexpectedEOF || len(p.buf)-p.r < asmMargin {
			continue
		}
		plane, ctx, first := rng.IntN(nPlane), rng.IntN(3), rng.IntN(2)
		quant := [2]uint16{uint16(1 + rng.IntN(300)), uint16(1 + rng.IntN(300))}
		if rng.IntN(3) == 0 {
			quant = [2]uint16{1, 1}
		}

		q := p
		clear(d.coeff[:16])
		nzGo := d.parseResiduals4Go(&q, plane, uint8(ctx), quant, first == 1, 0)
		want := [16]int16(d.coeff[:16])
		if q.unexpectedEOF {
			t.Fatal("ran out of input")
		}

		clear(d.coeff[:16])
		st := asmState{p.value, int64(p.bits), uint64(p.rangeM1), int64(p.r)}
		nz := parseBlockAsm(&st, &p.buf[0], &d.tokenProb[plane], (*[16]int16)(d.coeff[:16]), first, ctx, uint32(quant[0])|uint32(quant[1])<<16)
		got := [16]int16(d.coeff[:16])
		if nz != nzGo || got != want || st.value != q.value || int(st.bits) != q.bits || uint32(st.rng) != q.rangeM1 || st.rng>>32 != 0 || int(st.r) != q.r {
			t.Fatalf("plane %d ctx %d first %d quant %v: nz %d %d\ngot  %v\nwant %v\nstate %x %d %d %d, want %x %d %d %d",
				plane, ctx, first, quant, nz, nzGo, got, want, st.value, st.bits, st.rng, st.r, q.value, q.bits, q.rangeM1, q.r)
		}
		if quant == [2]uint16{1, 1} {
			for _, c := range want {
				v := int(c)
				if v < 0 {
					v = -v
				}
				switch {
				case v == 0:
				case v == 1:
					cats[0]++
				case v <= 4:
					cats[1]++
				case v <= 6:
					cats[2]++
				case v <= 10:
					cats[3]++
				case v <= 18:
					cats[4]++
				case v <= 34:
					cats[5]++
				case v <= 66:
					cats[6]++
				default:
					cats[7]++
				}
			}
		}
		runs++
	}
	t.Logf("%d blocks; values by category %v", runs, cats)
	for i, n := range cats {
		if n < 100 {
			t.Errorf("category %d only %d times", i, n)
		}
	}
}
