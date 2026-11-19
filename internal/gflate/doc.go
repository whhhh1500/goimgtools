// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package gflate is the compressor of Go 1.27's compress/flate (its
// writer only, the files as they are but for the package's name), with
// one addition, Writer.ResetDict: a writer reset with another dictionary,
// so that the 256KB blocks a PNG is compressed in (see pngstream's
// zBlocks) do not each make a new one (about 0.9MB). It writes the bytes
// Go 1.27's compress/flate writes, with any Go version.
package gflate

// An InternalError reports an error in the flate code itself.
type InternalError string

func (e InternalError) Error() string { return "flate: internal error: " + string(e) }
