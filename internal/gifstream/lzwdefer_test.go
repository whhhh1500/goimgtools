package gifstream

import (
	"bytes"
	"compress/lzw"
	"io"
	"math/rand/v2"
	"testing"
)

// deferEncode codes pix with deferLZW and returns the LZW data, without
// the GIF sub-block framing.
func deferEncode(pix []byte, lit uint) []byte {
	var out bytes.Buffer
	e := newEncoder(&out, 1, 1, nil)
	bw := blockWriter{e: e}
	bw.setup()
	z := new(deferLZW)
	z.reset(bw, lit, 0)
	z.write(pix)
	z.close()
	bw.close()
	// Undo the sub-blocks.
	var data []byte
	b := out.Bytes()
	for len(b) > 0 && b[0] != 0 {
		n := int(b[0])
		data = append(data, b[1:1+n]...)
		b = b[1+n:]
	}
	return data
}

func TestDeferLZW(t *testing.T) {
	r := rand.New(rand.NewPCG(4, 5))
	for _, c := range []struct {
		name string
		n    int
		lit  uint
		gen  func(i int) byte
	}{
		{"short", 300, 8, func(i int) byte { return byte(r.IntN(7)) }},
		{"two colours", 5000, 2, func(i int) byte { return byte(i / 13 % 2) }},
		{"long random", 200000, 8, func(i int) byte { return byte(r.IntN(256)) }},
		{"long patterned", 300000, 4, func(i int) byte { return byte((i*i/7 + i/300) % 16) }},
		{"one pixel", 1, 2, func(i int) byte { return 1 }},
		{"none", 0, 2, func(i int) byte { return 0 }},
	} {
		pix := make([]byte, c.n)
		for i := range pix {
			pix[i] = c.gen(i)
		}
		data := deferEncode(pix, c.lit)
		got, err := io.ReadAll(lzw.NewReader(bytes.NewReader(data), lzw.LSB, int(c.lit)))
		if err != nil || !bytes.Equal(got, pix) {
			t.Errorf("%s: decoded %d bytes, %v; want %d", c.name, len(got), err, len(pix))
		}
		var std bytes.Buffer
		w := lzw.NewWriter(&std, lzw.LSB, int(c.lit))
		w.Write(pix)
		w.Close()
		// Until the dictionary fills the codes are the standard library's.
		if c.n < 3000 && !bytes.Equal(data, std.Bytes()) {
			t.Errorf("%s: not the bytes of compress/lzw", c.name)
		}
		t.Logf("%s: deferred %d bytes, compress/lzw %d", c.name, len(data), std.Len())
	}
}
