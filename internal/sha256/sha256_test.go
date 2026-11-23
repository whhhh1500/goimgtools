package sha256

import (
	std "crypto/sha256"
	"encoding/hex"
	"testing"
)

// TestSum256: the vectors of FIPS 180-4, and every length from 0 to 300
// bytes (each padding case, one block and two) and a few large ones, the
// same as crypto/sha256.
func TestSum256(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"", "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
		{"abc", "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"},
		{"abcdbcdecdefdefgefghfghighijhijkijkljklmklmnlmnomnopnopq", "248d6a61d20638b8e5c026930c3e6039a33ce45964ff2167f6ecedd419db06c1"},
	} {
		got := Sum256([]byte(c.in))
		if hex.EncodeToString(got[:]) != c.want {
			t.Errorf("Sum256(%q) = %x, want %s", c.in, got, c.want)
		}
	}
	data := make([]byte, 1<<20+77)
	x := uint32(1)
	for i := range data {
		x = x*1664525 + 1013904223
		data[i] = byte(x >> 24)
	}
	lengths := []int{1000, 4096, 65536, 1 << 20, len(data)}
	for n := 0; n <= 300; n++ {
		lengths = append(lengths, n)
	}
	for _, n := range lengths {
		if got, want := Sum256(data[:n]), std.Sum256(data[:n]); got != want {
			t.Errorf("%d bytes: %x, crypto/sha256 gives %x", n, got, want)
		}
	}
}

func BenchmarkSum256(b *testing.B) {
	data := make([]byte, 1<<20)
	b.SetBytes(int64(len(data)))
	for b.Loop() {
		Sum256(data)
	}
}
