// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE-GO file.

package tiffstream

import (
	"bufio"
	"io"
)

type byteReader interface {
	io.Reader
	io.ByteReader
}

// packBitsReader is the streaming form of x/image's unpackBits: it decodes
// PackBits-compressed data from r one packet at a time. Like unpackBits it
// fails once more than lim bytes have been produced, to prevent
// decompression bombs, and reports a packet cut short as an error.
//
// The PackBits compression format is described in section 9 (p. 42)
// of the TIFF spec.
type packBitsReader struct {
	br      byteReader
	buf     [128]byte
	pending []byte // decoded bytes of the current packet not yet handed out
	total   int64  // bytes decoded so far
	lim     int64
	err     error
}

func newPackBitsReader(r io.Reader, lim int64) *packBitsReader {
	br, ok := r.(byteReader)
	if !ok {
		br = bufio.NewReader(r)
	}
	return &packBitsReader{br: br, lim: lim}
}

// next decodes one packet into z.pending. It returns io.EOF only at a packet
// boundary.
func (z *packBitsReader) next() error {
	b, err := z.br.ReadByte()
	if err != nil {
		return err
	}
	code := int(int8(b))
	switch {
	case code >= 0:
		n, err := io.ReadFull(z.br, z.buf[:code+1])
		if err != nil {
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return err
		}
		z.pending = z.buf[:n]
	case code == -128:
		// No-op.
	default:
		if b, err = z.br.ReadByte(); err != nil {
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return err
		}
		for j := 0; j < 1-code; j++ {
			z.buf[j] = b
		}
		z.pending = z.buf[:1-code]
	}
	z.total += int64(len(z.pending))
	if z.total > z.lim {
		z.pending = nil
		return FormatError("PackBits: decompressed data too large")
	}
	return nil
}

func (z *packBitsReader) Read(p []byte) (int, error) {
	n := 0
	for n < len(p) {
		if len(z.pending) == 0 {
			if z.err == nil {
				z.err = z.next()
			}
			if z.err != nil {
				if n > 0 {
					return n, nil
				}
				return 0, z.err
			}
			continue
		}
		m := copy(p[n:], z.pending)
		z.pending = z.pending[m:]
		n += m
	}
	return n, nil
}
