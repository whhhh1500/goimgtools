package icnsstream

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// FuzzOpen: any file is read or refused, without a panic, within the
// memory it reserves.
func FuzzOpen(f *testing.F) {
	if b, err := os.ReadFile(filepath.Join("..", "..", "tests", "testdata", "pillow", "fmt-legacy.icns")); err == nil {
		f.Add(b)
	}
	// A legacy image stored without compression.
	plane := bytes.Repeat([]byte{7}, 3*16*16)
	e := append([]byte("is32"), binary.BigEndian.AppendUint32(nil, uint32(8+len(plane)))...)
	e = append(e, plane...)
	f.Add(append(append([]byte("icns"), binary.BigEndian.AppendUint32(nil, uint32(8+len(e)))...), e...))
	f.Fuzz(func(t *testing.T, data []byte) {
		env := pipeline.NewEnv(4<<20, t.TempDir())
		src, err := Open(bytes.NewReader(data), int64(len(data)), env)
		if err != nil {
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
