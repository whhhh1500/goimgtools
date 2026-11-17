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
	runs, nearRuns := 0, 0
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
		n := 200 + rng.IntN(len(data)-200)
		p.init(bytes.NewReader(data[:n]), 0, n)
		for range 1 + rng.IntN(800) {
			p.readBit(uint8(rng.IntN(256)))
		}
		if p.unexpectedEOF {
			continue
		}
		near := len(p.buf)-p.r < asmMargin
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
		a := p
		nz := parseBlockAsm(&a, &d.tokenProb[plane], (*[16]int16)(d.coeff[:16]), first, ctx, uint32(quant[0])|uint32(quant[1])<<16)
		got := [16]int16(d.coeff[:16])
		if near {
			// Too near the end of the window: nothing read.
			if nz != asmTooNear || got != [16]int16{} || a.value != p.value || a.bits != p.bits || a.rangeM1 != p.rangeM1 || a.r != p.r {
				t.Fatalf("near the end of the window: %d, state %x %d %d %d", nz, a.value, a.bits, a.rangeM1, a.r)
			}
			nearRuns++
			continue
		}
		if nz != nzGo || got != want || a.value != q.value || a.bits != q.bits || a.rangeM1 != q.rangeM1 || a.r != q.r {
			t.Fatalf("plane %d ctx %d first %d quant %v: nz %d %d\ngot  %v\nwant %v\nstate %x %d %d %d, want %x %d %d %d",
				plane, ctx, first, quant, nz, nzGo, got, want, a.value, a.bits, a.rangeM1, a.r, q.value, q.bits, q.rangeM1, q.r)
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
	t.Logf("%d blocks, %d near the end of the window; values by category %v", runs, nearRuns, cats)
	if nearRuns == 0 {
		t.Error("no block near the end of the window")
	}
	for i, n := range cats {
		if n < 100 {
			t.Errorf("category %d only %d times", i, n)
		}
	}
}
