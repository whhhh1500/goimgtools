package gifstream

import (
	"bufio"
	"bytes"
	"compress/lzw"
	"fmt"
	"io"
	"math/rand"
	"testing"
)

// inBlocks is data as the sub-blocks of a GIF (of random sizes), with the
// block that ends them and what follows it.
func inBlocks(r *rand.Rand, data, after []byte) []byte {
	var out []byte
	for len(data) > 0 {
		n := min(len(data), 1+r.Intn(255))
		if r.Intn(3) == 0 {
			n = min(len(data), 255)
		}
		out = append(out, byte(n))
		out = append(out, data[:n]...)
		data = data[n:]
	}
	return append(append(out, 0), after...)
}

// lzwSample is pixels of lit bits and their LZW data, as compress/lzw
// writes it: random, in runs, or in repeats, long enough at times to fill
// the table more than once.
func lzwSample(r *rand.Rand, lit int) (pix, coded []byte) {
	n := r.Intn(40)
	switch r.Intn(4) {
	case 0:
		n = 2000 + r.Intn(60000)
	case 1:
		n = 200 + r.Intn(3000)
	}
	pix = make([]byte, n)
	kind := r.Intn(3)
	for i := range pix {
		switch {
		case kind == 0 || i == 0:
			pix[i] = byte(r.Intn(1 << lit))
		case kind == 1: // runs
			pix[i] = pix[i-1]
			if r.Intn(20) == 0 {
				pix[i] = byte(r.Intn(1 << lit))
			}
		default: // repeats of what came before
			pix[i] = pix[i-1-r.Intn(min(i, 7))]
			if r.Intn(50) == 0 {
				pix[i] = byte(r.Intn(1 << lit))
			}
		}
	}
	if r.Intn(2) == 0 && len(pix) > 0 {
		return pix, lzwDeferred(r, pix, lit)
	}
	var buf bytes.Buffer
	w := lzw.NewWriter(&buf, lzw.LSB, lit)
	w.Write(pix)
	w.Close()
	return pix, buf.Bytes()
}

// lzwDeferred is the LZW data of pix as an encoder writes it that does not
// clear the table when it is full (as gifsicle, giflib and this package's
// deferLZW do), but goes on with the codes it has, and clears now and then
// at a place of its own choosing.
func lzwDeferred(r *rand.Rand, pix []byte, lit int) []byte {
	var out []byte
	var acc uint64
	var nacc uint
	clr := 1 << lit
	eof := clr + 1
	width, hi, overflow := uint(lit+1), eof, 1<<(lit+1)
	emit := func(code int) {
		acc |= uint64(code) << nacc
		for nacc += width; nacc >= 8; nacc -= 8 {
			out = append(out, byte(acc))
			acc >>= 8
		}
	}
	table := map[uint32]int{}
	emit(clr)
	cur := int(pix[0])
	for _, b := range pix[1:] {
		key := uint32(cur)<<8 | uint32(b)
		if c, ok := table[key]; ok {
			cur = c
			continue
		}
		emit(cur)
		cur = int(b)
		if hi < 4095 {
			hi++
			table[key] = hi
			if hi == overflow {
				width++
				overflow <<= 1
			}
		} else if r.Intn(3000) == 0 {
			emit(clr)
			clear(table)
			width, hi, overflow = uint(lit+1), eof, 1<<(lit+1)
		}
	}
	emit(cur)
	// (The last code moved hi on, as every code does.)
	if hi < 4095 {
		if hi++; hi == overflow {
			width++
		}
	}
	emit(eof)
	if nacc > 0 {
		out = append(out, byte(acc))
	}
	return out
}

// TestLZWReader: lzwReader gives the bytes and the error compress/lzw's
// reader gives, for data as it is written, cut short, with bits changed,
// and with bytes or blocks after its end; read in pieces of any size; and
// leaves the blocks as that reader leaves them, so that what follows the
// data is judged alike (blockReader.close) and read from the same place.
func TestLZWReader(t *testing.T) {
	r := rand.New(rand.NewSource(53))
	for trial := 0; trial < 6000; trial++ {
		lit := 2 + r.Intn(7)
		pix, coded := lzwSample(r, lit)
		what := "whole"
		switch r.Intn(6) {
		case 0:
			if len(coded) > 1 {
				coded = coded[:r.Intn(len(coded))]
				what = "cut"
			}
		case 1:
			coded = append([]byte(nil), coded...)
			for k := 1 + r.Intn(3); k > 0 && len(coded) > 0; k-- {
				coded[r.Intn(len(coded))] ^= byte(1 << r.Intn(8))
			}
			what = "damaged"
		case 2:
			extra := make([]byte, 1+r.Intn(300))
			r.Read(extra)
			coded = append(append([]byte(nil), coded...), extra...)
			what = "with bytes after"
		}
		after := []byte{0x3b, 1, 2, 3}
		if r.Intn(4) == 0 {
			after = append([]byte{byte(1 + r.Intn(3)), 9, 9, 9, 0}, after...) // one more block, or the start of one
			what += ", a block after"
		}
		seed := r.Int63()
		file := inBlocks(rand.New(rand.NewSource(seed)), coded, after)
		if r.Intn(8) == 0 && len(file) > 2 {
			file = file[:r.Intn(len(file))]
			what += ", the file cut"
		}
		name := fmt.Sprintf("trial %d (%d bits, %d pixels, %s)", trial, lit, len(pix), what)

		// stop: read everything (-1), or as a frame is read: so many
		// pixels, then one byte more.
		stops := []int{-1, len(pix)}
		if len(pix) > 1 {
			stops = append(stops, r.Intn(len(pix)))
		}
		for _, stop := range stops {
			type result struct {
				data          []byte
				err           error
				more          int
				moreErr       error
				closeErr      error
				i, j          uint8
				brErr         error
				rest          []byte
				closeConsults bool
			}
			run := func(fast bool) (res result) {
				d := &decoder{r: bufio.NewReader(bytes.NewReader(file))}
				br := &blockReader{d: d}
				var rd io.Reader
				if fast {
					f := new(lzwReader)
					f.reset(br, uint(lit))
					rd = f
				} else {
					rd = lzw.NewReader(br, lzw.LSB, lit)
				}
				sizes := rand.New(rand.NewSource(seed))
				if stop < 0 {
					for res.err == nil {
						p := make([]byte, 1+sizes.Intn(5000))
						var n int
						n, res.err = rd.Read(p)
						res.data = append(res.data, p[:n]...)
						if n == 0 && res.err == nil {
							t.Fatalf("%s: a read of nothing without an error", name)
						}
					}
					res.closeConsults = true
				} else {
					res.data = make([]byte, stop)
					var n int
					n, res.err = io.ReadFull(rd, res.data)
					res.data = res.data[:n]
					if res.err == nil {
						var one [1]byte
						res.more, res.moreErr = rd.Read(one[:])
						// A frame is closed when nothing more came.
						res.closeConsults = res.more == 0 && (res.moreErr == io.EOF || res.moreErr == io.ErrUnexpectedEOF)
					}
				}
				if res.closeConsults {
					res.i, res.j, res.brErr = br.i, br.j, br.err
					res.closeErr = br.close()
					res.rest, _ = io.ReadAll(d.r)
				}
				return res
			}
			want, got := run(false), run(true)
			if what == "whole" && stop < 0 && (want.err != io.EOF || !bytes.Equal(want.data, pix)) {
				t.Fatalf("%s: the standard reader gives %d bytes and %v", name, len(want.data), want.err)
			}
			same := func(a, b error) bool { return (a == nil) == (b == nil) && (a == nil || a.Error() == b.Error()) }
			switch {
			case !bytes.Equal(want.data, got.data):
				t.Fatalf("%s, stop %d: %d bytes, the standard reader gives %d", name, stop, len(got.data), len(want.data))
			case !same(want.err, got.err):
				t.Fatalf("%s, stop %d: error %v, the standard reader's is %v", name, stop, got.err, want.err)
			case want.more != got.more || !same(want.moreErr, got.moreErr):
				t.Fatalf("%s, stop %d: one byte more: %d, %v; the standard reader %d, %v", name, stop, got.more, got.moreErr, want.more, want.moreErr)
			case want.closeConsults != got.closeConsults:
				t.Fatalf("%s, stop %d: closed %v, want %v", name, stop, got.closeConsults, want.closeConsults)
			}
			// What is left of the blocks matters once everything of the
			// data was read: the standard reader decodes ahead of what it
			// hands out, so before that the two may stand at different
			// places, as any two of its own runs may.
			if stop < 0 {
				if want.i != got.i || want.j != got.j || !same(want.brErr, got.brErr) || !same(want.closeErr, got.closeErr) || !bytes.Equal(want.rest, got.rest) {
					t.Fatalf("%s: the blocks are left at %d of %d (%v), closed with %v, %d bytes after; the standard reader %d of %d (%v), %v, %d",
						name, got.i, got.j, got.brErr, got.closeErr, len(got.rest), want.i, want.j, want.brErr, want.closeErr, len(want.rest))
				}
			} else if want.closeConsults && (!same(want.closeErr, got.closeErr) || !bytes.Equal(want.rest, got.rest)) {
				t.Fatalf("%s, stop %d: closed with %v, %d bytes after; the standard reader %v, %d", name, stop, got.closeErr, len(got.rest), want.closeErr, len(want.rest))
			}
		}
	}
}

// BenchmarkLZWReader: the two readers on the pixels of a picture-like
// frame.
func BenchmarkLZWReader(b *testing.B) {
	r := rand.New(rand.NewSource(1))
	pix := make([]byte, 4<<20)
	for i := range pix {
		v := 128 + int(40*float64(i%3000)/3000) + r.Intn(24) + (i/3000)%64
		pix[i] = byte(v)
	}
	var buf bytes.Buffer
	w := lzw.NewWriter(&buf, lzw.LSB, 8)
	w.Write(pix)
	w.Close()
	file := inBlocks(rand.New(rand.NewSource(2)), buf.Bytes(), nil)
	out := make([]byte, 6000)
	for _, fast := range []bool{false, true} {
		name := "standard"
		if fast {
			name = "lzwReader"
		}
		b.Run(name, func(b *testing.B) {
			b.SetBytes(int64(len(pix)))
			f := new(lzwReader)
			for i := 0; i < b.N; i++ {
				d := &decoder{r: bufio.NewReaderSize(bytes.NewReader(file), 64<<10)}
				br := &blockReader{d: d}
				var rd io.Reader
				if fast {
					f.reset(br, 8)
					rd = f
				} else {
					rd = lzw.NewReader(br, lzw.LSB, 8)
				}
				total := 0
				for {
					n, err := io.ReadFull(rd, out)
					total += n
					if err != nil {
						break
					}
				}
				if total != len(pix) {
					b.Fatalf("%d bytes", total)
				}
			}
		})
	}
}
