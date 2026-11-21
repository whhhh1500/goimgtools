package pnmstream

import (
	"bytes"
	"testing"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// FuzzOpen: any file is read or refused, without a panic, within the
// memory it reserves.
func FuzzOpen(f *testing.F) {
	for _, s := range []string{
		"P1\n3 2\n1 0 1\n0 1 0\n", "P2\n# c\n2 2\n100\n0 50 100 7\n", "P3\n1 1\n255\n1 2 3\n",
		"P4\n9 1\n\xa5\x80", "P5 2 1 1000\n\x00\x01\x03\xe8", "P6\n1 1\n7\n\x01\x02\x03",
		"P7\nWIDTH 1\nHEIGHT 1\nDEPTH 2\nMAXVAL 255\nTUPLTYPE GRAYSCALE_ALPHA\nENDHDR\n\x80\x40",
		"PF\n1 1\n1.0\n\x3f\x00\x00\x00\x00\x00\x00\x00\x3f\x80\x00\x00",
	} {
		f.Add([]byte(s))
	}
	dir := f.TempDir() // one for every input: making one each is slow
	f.Fuzz(func(t *testing.T, data []byte) {
		env := pipeline.NewEnv(4<<20, dir)
		src, err := Open(bytes.NewReader(data), int64(len(data)), env)
		if err != nil {
			return
		}
		if b := src.Bounds(); int64(b.Dx())*int64(b.Dy()) > 1<<22 {
			src.Close()
			return
		}
		for {
			if _, err := src.Next(); err != nil {
				break
			}
		}
		src.Close()
		if env.Budget.Used() != 0 {
			t.Fatalf("%d bytes reserved", env.Budget.Used())
		}
	})
}
