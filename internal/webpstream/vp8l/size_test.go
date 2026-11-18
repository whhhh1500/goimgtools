package vp8l

import (
	"testing"
	"unsafe"
)

// TestHGroupBytes: the memory counted for each Huffman group is its size
// (on 64-bit platforms, which hGroupBytes is written for; 32-bit ones take
// less).
func TestHGroupBytes(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) != 8 {
		t.Skip("32-bit")
	}
	if got := int64(unsafe.Sizeof(hGroup{})); got != hGroupBytes {
		t.Fatalf("an hGroup takes %d bytes, hGroupBytes is %d", got, hGroupBytes)
	}
}
