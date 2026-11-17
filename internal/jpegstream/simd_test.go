package jpegstream

import (
	"bytes"
	"image"
	"math/rand/v2"
	"testing"
)

// TestIslowAVX2: the AVX2 inverse DCT gives what islowGo gives, byte for
// byte, for blocks of every kind: a DC alone, a few coefficients, half the
// columns or rows, all of them; values of real files, and any int32 at all
// (whose products and shifts wrap the same way).
func TestIslowAVX2(t *testing.T) {
	if !useAVX2 {
		t.Skip("no AVX2")
	}
	rng := rand.New(rand.NewPCG(1, 2))
	for i := range 200000 {
		var b block
		switch i % 6 {
		case 0: // DC alone
			b[0] = rng.Int32N(16384) - 8192
		case 1: // a few coefficients
			for range 1 + rng.IntN(6) {
				b[rng.IntN(64)] = rng.Int32N(2048) - 1024
			}
		case 2: // the top left quarter
			for r := range 4 {
				for c := range 4 {
					b[8*r+c] = rng.Int32N(4096) - 2048
				}
			}
		case 3: // a column or a row alone
			k := rng.IntN(8)
			for j := range 8 {
				if rng.IntN(2) == 0 {
					b[8*j+k] = rng.Int32N(4096) - 2048
				} else {
					b[8*k+j] = rng.Int32N(4096) - 2048
				}
			}
		case 4: // every coefficient
			for j := range b {
				b[j] = rng.Int32N(8192) - 4096
			}
		case 5: // anything, wrapping
			for j := range b {
				if rng.IntN(3) == 0 {
					b[j] = int32(rng.Uint32())
				}
			}
		}
		for _, stride := range []int{8, 13} {
			want, got := make([]byte, 8*stride), make([]byte, 8*stride)
			for j := range want {
				want[j], got[j] = byte(j), byte(j)
			}
			wb, gb := b, b
			islowGo(&wb, want, stride)
			islowAVX2(&gb, &got[0], stride)
			if !bytes.Equal(got, want) {
				t.Fatalf("block %d %v, stride %d:\n got %v\nwant %v", i, b, stride, got, want)
			}
		}
	}
}

func BenchmarkIslow(b *testing.B) {
	rng := rand.New(rand.NewPCG(1, 2))
	blocks := make([]block, 256)
	for i := range blocks {
		for range 1 + rng.IntN(10) {
			blocks[i][rng.IntN(20)] = rng.Int32N(512) - 256
		}
	}
	dst := make([]byte, 64)
	for _, c := range []struct {
		name string
		f    func(*block)
	}{
		{"go", func(k *block) { islowGo(k, dst, 8) }},
		{"avx2", func(k *block) { islowAVX2(k, &dst[0], 8) }},
	} {
		if c.name == "avx2" && !useAVX2 {
			continue
		}
		b.Run(c.name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				k := blocks[i&255]
				c.f(&k)
			}
		})
	}
}

func BenchmarkIslowNoCopy(b *testing.B) {
	rng := rand.New(rand.NewPCG(1, 2))
	var k block
	for range 8 {
		k[rng.IntN(20)] = rng.Int32N(512) - 256
	}
	dst := make([]byte, 64)
	b.Run("avx2", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			islowAVX2(&k, &dst[0], 8)
		}
	})
	b.Run("go", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			kk := k
			islowGo(&kk, dst, 8)
		}
	})
}

func sameRow(t *testing.T, what string, w int, got, want []byte) {
	t.Helper()
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("%s, width %d: byte %d (pixel %d) is %d, want %d", what, w, i, i/4, got[i], want[i])
		}
	}
}

// TestYccH2V2AVX2: rows of every width from 1 to 300 and random samples
// (all values, and runs of 0 and 255 that the sums take past the ends of
// the range) convert as the Go code converts them: chroma halved both
// ways, across, and at full size.
func TestYccH2V2AVX2(t *testing.T) {
	if !useAVX2 {
		t.Skip("no AVX2")
	}
	rng := rand.New(rand.NewPCG(3, 4))
	fill := func(b []byte) {
		for i := range b {
			switch rng.IntN(4) {
			case 0:
				b[i] = 0
			case 1:
				b[i] = 255
			default:
				b[i] = byte(rng.IntN(256))
			}
		}
	}
	for w := 1; w <= 300; w++ {
		n := (w + 1) / 2
		for range 20 {
			y, cbN, cbF, crN, crF := make([]byte, w), make([]byte, n), make([]byte, n), make([]byte, n), make([]byte, n)
			for _, b := range [][]byte{y, cbN, cbF, crN, crF} {
				fill(b)
			}
			want, got := make([]byte, 4*w), make([]byte, 4*w)
			yccH2V2Go(want, y, cbN, cbF, crN, crF, 0, n)
			yccH2V2(got, y, cbN, cbF, crN, crF)
			sameRow(t, "h2v2", w, got, want)
			yccH2V1Go(want, y, cbN, crN, 0, n)
			yccH2V1(got, y, cbN, crN)
			sameRow(t, "h2v1", w, got, want)
			// Full chroma: cbN and cbF, crN and crF together, w samples.
			cb, cr := append(cbN[:n:n], cbF...)[:w], append(crN[:n:n], crF...)[:w]
			for x := range y {
				yccPair((*[4]byte)(want[4*x:]), int32(y[x]), cb[x], cr[x])
			}
			yccRow(got, y, cb, cr)
			sameRow(t, "full", w, got, want)
		}
	}
}

func BenchmarkYccH2V2(b *testing.B) {
	const w = 6000
	y, c := make([]byte, w), make([]byte, w/2)
	for i := range y {
		y[i] = byte(i * 7)
	}
	for i := range c {
		c[i] = byte(i * 13)
	}
	dst := make([]byte, 4*w)
	b.Run("go", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			yccH2V2Go(dst, y, c, c, c, c, 0, w/2)
		}
	})
	b.Run("dispatch", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			yccH2V2(dst, y, c, c, c, c)
		}
	})
}

// TestFdctQuantAVX2: blocks of samples (flat, smooth, noisy, extreme) and
// quantization tables of every value transform and quantize with AVX2 as
// fdct and quantizeCoefs do.
func TestFdctQuantAVX2(t *testing.T) {
	if !useAVX2 {
		t.Skip("no AVX2")
	}
	rng := rand.New(rand.NewPCG(9, 10))
	for i := range 50000 {
		var e encoder
		for q := range nQuantIndex {
			for j := range blockSize {
				x := 1 + rng.IntN(255)
				if i%3 == 0 {
					x = 1 + rng.IntN(8)
				}
				e.quant[q][j] = uint8(x)
				e.recip[q][j] = recip(8 * int32(x))
				e.natHalf[q][unzig[j]] = 4 * int32(x)
				e.natRecip[q][unzig[j]] = uint32(e.recip[q][j])
			}
		}
		var b block
		base := rng.Int32N(256)
		for j := range b {
			switch i % 4 {
			case 0:
				b[j] = base
			case 1:
				b[j] = min(255, max(0, base+rng.Int32N(9)-4))
			case 2:
				b[j] = rng.Int32N(256)
			case 3:
				b[j] = 255 * rng.Int32N(2)
			}
		}
		q := quantIndex(i & 1)
		var want, got qblock
		wb, gb := b, b
		fdct(&wb)
		e.quantizeCoefs(&wb, q, &want)
		e.quantize(&gb, q, &got)
		if got != want {
			t.Fatalf("block %d %v:\n got %v\nwant %v", i, b, got, want)
		}
	}
}

// TestRGBAToYCbCrAVX2: whole blocks of pixels convert with AVX2 as ycc
// converts them, and 4 blocks of chroma shrink as scale shrinks them.
func TestRGBAToYCbCrAVX2(t *testing.T) {
	if !useAVX2 {
		t.Skip("no AVX2")
	}
	rng := rand.New(rand.NewPCG(11, 12))
	m := image.NewRGBA(image.Rect(3, 5, 3+37, 5+29))
	for range 2000 {
		for i := range m.Pix {
			m.Pix[i] = byte(rng.IntN(256))
			if rng.IntN(5) == 0 {
				m.Pix[i] = 255 * byte(rng.IntN(2))
			}
		}
		p := image.Pt(3+rng.IntN(30), 5+rng.IntN(22))
		var y, cb, cr, wy, wcb, wcr block
		rgbaToYCbCr(m, p, &y, &cb, &cr)
		for j := range 8 {
			for i := range 8 {
				o := m.PixOffset(p.X+i, p.Y+j)
				wy[8*j+i], wcb[8*j+i], wcr[8*j+i] = ycc(int32(m.Pix[o]), int32(m.Pix[o+1]), int32(m.Pix[o+2]))
			}
		}
		if y != wy || cb != wcb || cr != wcr {
			t.Fatalf("block at %v differs", p)
		}
		var src [4]block
		for k := range src {
			for j := range src[k] {
				src[k][j] = rng.Int32N(256)
			}
		}
		var got, want block
		scale(&got, &src)
		for i := 0; i < 4; i++ {
			off := (i&2)<<4 | (i&1)<<2
			for yy := 0; yy < 4; yy++ {
				for x := 0; x < 4; x++ {
					j := 16*yy + 2*x
					want[8*yy+x+off] = (src[i][j] + src[i][j+1] + src[i][j+8] + src[i][j+9] + 1 + int32(x&1)) >> 2
				}
			}
		}
		if got != want {
			t.Fatalf("scale: %v, want %v", got, want)
		}
	}
}
