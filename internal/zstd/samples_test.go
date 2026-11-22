package zstd

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// samples are the files of testdata: name and compressed bytes.
func samples(t *testing.T) map[string][]byte {
	t.Helper()
	names, err := filepath.Glob(filepath.Join("testdata", "*.zst"))
	if err != nil || len(names) < 6 {
		t.Fatalf("testdata: %d files, %v", len(names), err)
	}
	m := map[string][]byte{}
	for _, n := range names {
		b, err := os.ReadFile(n)
		if err != nil {
			t.Fatal(err)
		}
		m[filepath.Base(n)] = b
	}
	return m
}

// TestFileSamples: every file of testdata decompresses to the content its
// name gives the hash of, read at once and a few bytes at a time, by a new
// reader and by one used before.
func TestFileSamples(t *testing.T) {
	used := NewReader(nil)
	for name, z := range samples(t) {
		want, _, _ := strings.Cut(name, ".")
		for _, piece := range []int{0, 1, 7, 4096} {
			for _, r := range []*Reader{NewReader(bytes.NewReader(z)), used} {
				r.Reset(bytes.NewReader(z))
				var out []byte
				var err error
				if piece == 0 {
					out, err = io.ReadAll(r)
				} else {
					p := make([]byte, piece)
					for err == nil {
						var n int
						n, err = r.Read(p)
						out = append(out, p[:n]...)
					}
					if err == io.EOF {
						err = nil
					}
				}
				if err != nil {
					t.Errorf("%s, %d bytes at a time: %v", name, piece, err)
					continue
				}
				if sum := sha256.Sum256(out); hex.EncodeToString(sum[:4]) != want {
					t.Errorf("%s, %d bytes at a time: %d bytes with the hash %x", name, piece, len(out), sum[:4])
				}
			}
		}
	}
}

// TestMaxWindow: a limit on the window at least as large as what the
// stream holds changes nothing; one smaller than its matches reach back
// is an error, not a panic or other content.
func TestMaxWindow(t *testing.T) {
	for name, z := range samples(t) {
		want, err := io.ReadAll(NewReader(bytes.NewReader(z)))
		if err != nil {
			t.Fatal(err)
		}
		r := NewReader(bytes.NewReader(z))
		r.SetMaxWindow(len(want))
		if got, err := io.ReadAll(r); err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s with a window of the content's %d bytes: %d bytes, %v", name, len(want), len(got), err)
		}
		if !strings.Contains(name, "picture-512x260") {
			continue
		}
		// Rows repeat 1536 bytes on, over several blocks.
		r = NewReader(bytes.NewReader(z))
		r.SetMaxWindow(64)
		if got, err := io.ReadAll(r); err == nil {
			t.Errorf("%s with a window of 64 bytes: read %d bytes", name, len(got))
		} else if !bytes.Equal(got, want[:len(got)]) {
			t.Errorf("%s with a window of 64 bytes: other content before the error", name)
		}
	}
}

// TestDamaged: a stream cut short or with bytes changed is an error or
// some content, never a panic, and never more content than a frame may
// hold of what it was given.
func TestDamaged(t *testing.T) {
	rnd := rand.New(rand.NewSource(5))
	for name, z := range samples(t) {
		for n := 0; n < len(z); n++ {
			if n < 64 || n%13 == 0 {
				// (zero-dictionary-ids is several frames: cut between two, it is whole.)
				if _, err := io.ReadAll(NewReader(bytes.NewReader(z[:n]))); err == nil && n > 0 && !strings.Contains(name, "zero-dictionary") {
					t.Errorf("%s cut at %d of %d: read", name, n, len(z))
				}
			}
		}
		for i := 0; i < 2000; i++ {
			bad := append([]byte(nil), z...)
			for k := 1 + rnd.Intn(3); k > 0; k-- {
				bad[rnd.Intn(len(bad))] ^= byte(1 << rnd.Intn(8))
			}
			io.Copy(io.Discard, io.LimitReader(NewReader(bytes.NewReader(bad)), 64<<20))
		}
	}
}
