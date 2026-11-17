package lzw

import (
	"bytes"
	"io"
	"testing"

	xlzw "golang.org/x/image/tiff/lzw"
)

// FuzzSameAsXImage: on any input, valid or not, the reader gives the bytes
// and the error x/image's gives, read in pieces of any size.
func FuzzSameAsXImage(f *testing.F) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	w.Write(bytes.Repeat([]byte("abcabcabdabcab"), 900))
	w.Close()
	f.Add(buf.Bytes(), uint16(7))
	f.Add([]byte{0x80, 0x00, 0x00}, uint16(1))
	f.Fuzz(func(t *testing.T, data []byte, piece uint16) {
		read := func(r io.Reader) ([]byte, error) {
			var out []byte
			p := make([]byte, int(piece%5000)+1)
			for {
				n, err := r.Read(p)
				out = append(out, p[:n]...)
				if err != nil || len(out) > 1<<22 {
					return out, err
				}
			}
		}
		got, gerr := read(NewReader(bytes.NewReader(data), MSB, 8))
		want, werr := read(xlzw.NewReader(bytes.NewReader(data), xlzw.MSB, 8))
		if !bytes.Equal(got, want) || (gerr == nil) != (werr == nil) || (gerr != nil && gerr.Error() != werr.Error()) {
			t.Fatalf("%d bytes (%v), x/image %d bytes (%v)", len(got), gerr, len(want), werr)
		}
	})
}
