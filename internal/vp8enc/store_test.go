package vp8enc

import (
	"bytes"
	"io"
	"testing"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// TestStoreBeyondSlice checks that a store keeps a small amount in its
// slice and moves a larger one to a temporary file at once, leaving none of
// it in memory the budget has to spare, and that both read back what was
// written.
func TestStoreBeyondSlice(t *testing.T) {
	dir := t.TempDir()
	env := pipeline.NewEnv(0, dir)
	chunk := make([]byte, 100<<10)
	for i := range chunk {
		chunk[i] = byte(i*7 + i>>9)
	}
	for _, total := range []int{300 << 10, 3 << 20} {
		st := newStore(env)
		var want []byte
		for len(want) < total {
			if _, err := st.Write(chunk[:min(len(chunk), total-len(want))]); err != nil {
				t.Fatal(err)
			}
			want = append(want, chunk[:min(len(chunk), total-len(want))]...)
		}
		if st.Size() != int64(total) {
			t.Fatalf("%d bytes written, size %d", total, st.Size())
		}
		onDisk := st.sp != nil && st.sp.OnDisk()
		if onDisk != (total > st.memMax) {
			t.Errorf("%d bytes with a slice of up to %d: on disk %v", total, st.memMax, onDisk)
		}
		// In memory: the slice, or the buffer the temporary file is written
		// through.
		if used, most := env.Budget.Used(), int64(max(st.memMax, spillBuf)); used > most {
			t.Errorf("%d bytes written: %d bytes of the budget in use, more than %d", total, used, most)
		}
		got := make([]byte, total)
		for off := 0; off < total; {
			n, err := st.ReadAt(got[off:min(off+70000, total)], int64(off))
			if err != nil && err != io.EOF {
				t.Fatal(err)
			}
			off += n
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%d bytes read back differ from those written", total)
		}
		st.Close()
		if env.Budget.Used() != 0 {
			t.Errorf("%d bytes of budget still reserved", env.Budget.Used())
		}
		if n := tempFiles(t, dir); n != 0 {
			t.Errorf("%d temporary files left behind", n)
		}
	}
}
