package qoistream

import (
	"bytes"
	"testing"

	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// FuzzOpen: any file is read or refused, without a panic, within the
// memory it reserves.
func FuzzOpen(f *testing.F) {
	for _, m := range testutil.Images(9, 4) {
		var b bytes.Buffer
		if err := Encode(&b, testutil.Chopped(m, 3)); err == nil {
			f.Add(b.Bytes())
		}
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		env := pipeline.NewEnv(4<<20, "")
		src, err := Open(bytes.NewReader(data), env)
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
