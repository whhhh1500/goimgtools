//go:build go1.27 && amd64

// Go 1.27's compress/flate, which fuses the multiply-adds of its estimates
// on some processors (this copy does not), writes the same bytes where it
// does not: on amd64.

package gflate

import (
	"bytes"
	"compress/flate"
	"io"
	"testing"
)

// TestSameAsStdlib: the levels zBlocks uses (to 6) write what compress/flate
// writes, with and without a dictionary, closed and flushed. (Copied from
// Go 1.27.1; later releases of Go 1.27 changed levels 7 to 9 only.)
func TestSameAsStdlib(t *testing.T) {
	for si, data := range samples() {
		dict := data[:32<<10]
		body := data[32<<10:]
		for level := -2; level <= 6; level++ {
			for _, d := range [][]byte{nil, dict} {
				var a, b bytes.Buffer
				wa, err := flate.NewWriterDict(&a, level, d)
				if err != nil {
					t.Fatal(err)
				}
				wb, err := NewWriterDict(&b, level, d)
				if err != nil {
					t.Fatal(err)
				}
				for _, w := range []io.Writer{wa, wb} {
					w.Write(body[:100<<10])
					w.(interface{ Flush() error }).Flush()
					w.Write(body[100<<10:])
					w.(io.Closer).Close()
				}
				if !bytes.Equal(a.Bytes(), b.Bytes()) {
					t.Errorf("sample %d level %d dict %v: %d bytes against compress/flate's %d", si, level, d != nil, b.Len(), a.Len())
				}
			}
		}
	}
}
