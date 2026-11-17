package lzw

import (
	"bytes"
	"io"
	"math/rand"
	"testing"
)

// TestWriterRoundTrip: what the writer codes, the reader (x/image's, of
// TIFF's kind) reads back, for data that fills the table again and again
// (noise), compresses well (runs, patterns) and is small; written in one
// piece and in small pieces.
func TestWriterRoundTrip(t *testing.T) {
	rnd := rand.New(rand.NewSource(1))
	noise := make([]byte, 300000)
	rnd.Read(noise)
	runs := make([]byte, 200000)
	for i := range runs {
		runs[i] = byte(i / 700)
	}
	pattern := bytes.Repeat([]byte("goimgtools lzw "), 20000)
	mixed := make([]byte, 0, 400000)
	for len(mixed) < 400000 {
		n := rnd.Intn(5000)
		if rnd.Intn(2) == 0 {
			mixed = append(mixed, noise[:n]...)
		} else {
			mixed = append(mixed, bytes.Repeat([]byte{byte(n)}, n)...)
		}
	}
	for name, data := range map[string][]byte{"noise": noise, "runs": runs, "pattern": pattern, "mixed": mixed,
		"one": {7}, "two": {1, 2}, "empty": {}, "low": bytes.Repeat([]byte{0, 1, 2, 3}, 5000)} {
		for _, piece := range []int{len(data) + 1, 1, 13, 4096} {
			var buf bytes.Buffer
			w := NewWriter(&buf)
			for i := 0; i < len(data); i += piece {
				if _, err := w.Write(data[i:min(i+piece, len(data))]); err != nil {
					t.Fatal(err)
				}
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			got, err := io.ReadAll(NewReader(bytes.NewReader(buf.Bytes()), MSB, 8))
			if err != nil {
				t.Fatalf("%s in pieces of %d: %v", name, piece, err)
			}
			if !bytes.Equal(got, data) {
				t.Fatalf("%s in pieces of %d: %d bytes back, %d written", name, piece, len(got), len(data))
			}
			if name == "runs" && piece == 1 && buf.Len() > len(data)/10 { // a run of n bytes takes about sqrt(2n) codes
				t.Errorf("runs: %d bytes for %d", buf.Len(), len(data))
			}
		}
	}
}

// FuzzWriterRoundTrip: whatever is written, the reader gives back.
func FuzzWriterRoundTrip(f *testing.F) {
	f.Add([]byte("abababababababababab"), uint8(3))
	f.Add(bytes.Repeat([]byte{0}, 5000), uint8(1))
	f.Fuzz(func(t *testing.T, data []byte, piece uint8) {
		var buf bytes.Buffer
		w := NewWriter(&buf)
		step := int(piece) + 1
		for i := 0; i < len(data); i += step {
			w.Write(data[i:min(i+step, len(data))])
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(NewReader(bytes.NewReader(buf.Bytes()), MSB, 8))
		if err != nil || !bytes.Equal(got, data) {
			t.Fatalf("%d bytes back (%v), %d written", len(got), err, len(data))
		}
	})
}
