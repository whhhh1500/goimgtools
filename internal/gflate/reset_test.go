package gflate

import (
	"bytes"
	"testing"
)

// samples are data of the kinds a PNG's rows are: noise, a gradient,
// repeats, text-like runs.
func samples() [][]byte {
	n := 300 << 10
	noise, grad, rep := make([]byte, n), make([]byte, n), make([]byte, n)
	r := uint32(1)
	for i := range n {
		r = r*1664525 + 1013904223
		noise[i] = byte(r >> 24)
		grad[i] = byte(i/7 + int(r>>30))
		rep[i] = "the quick brown fox jumps over"[i%30] ^ byte(i/1000)
	}
	return [][]byte{noise, grad, rep}
}

// TestResetDict: a writer reset with each block's dictionary writes what a
// new writer with that dictionary writes, as zBlocks uses it.
func TestResetDict(t *testing.T) {
	for si, data := range samples() {
		for _, level := range []int{1, 3, 6, 9} {
			var want, got bytes.Buffer
			var w *Writer
			for start := 0; start < len(data); start += 64 << 10 {
				end := min(len(data), start+64<<10)
				dict := data[max(0, start-32<<10):start]
				nw, err := NewWriterDict(&want, level, dict)
				if err != nil {
					t.Fatal(err)
				}
				if w == nil {
					if w, err = NewWriter(&got, level); err != nil {
						t.Fatal(err)
					}
				} else {
					w.ResetDict(&got, dict)
				}
				for _, x := range []*Writer{nw, w} {
					x.Write(data[start:end])
					if end == len(data) {
						x.Close()
					} else {
						x.Flush()
					}
				}
			}
			if !bytes.Equal(want.Bytes(), got.Bytes()) {
				t.Errorf("sample %d level %d: reset writes %d bytes against %d", si, level, got.Len(), want.Len())
			}
		}
	}
}
