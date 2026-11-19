// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build (amd64 || arm64 || ppc64le || riscv64 || loong64 || 386) && !purego

package gflate

import "unsafe"

// The loads and the store of load_store.go, by unsafe on little-endian
// processors that load unaligned words (as klauspost/compress does): with
// one bounds check each, they took a third of the time of level 6. (An
// addition to compress/flate; the bytes written are the same.)

type indexer interface {
	int | int8 | int16 | int32 | int64 | uint | uint8 | uint16 | uint32 | uint64
}

// loadLE8 will load from b at index i.
func loadLE8[I indexer](b []byte, i I) byte {
	return b[i]
}

// loadLE32 will load from b at index i.
func loadLE32[I indexer](b []byte, i I) uint32 {
	_ = b[i+3]
	return *(*uint32)(unsafe.Pointer(&b[i]))
}

// loadLE64 will load from b at index i.
func loadLE64[I indexer](b []byte, i I) uint64 {
	_ = b[i+7]
	return *(*uint64)(unsafe.Pointer(&b[i]))
}

// storeLE64 will store v at start of b.
func storeLE64(b []byte, v uint64) {
	_ = b[7]
	*(*uint64)(unsafe.Pointer(&b[0])) = v
}
