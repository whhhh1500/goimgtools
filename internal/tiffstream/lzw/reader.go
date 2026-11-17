// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the licenses/LICENSE-GO file.

// Package lzw is golang.org/x/image/tiff/lzw (v0.47.0) with two changes: a
// reader can be reused for the next stream (Reset) instead of allocating its
// 20KB of tables per TIFF strip, and it always reads through its own
// bufio.Reader, called directly instead of through an interface. The decoded
// bytes are the same.
package lzw // import "golang.org/x/image/tiff/lzw"

/*
This file was branched from src/pkg/compress/lzw/reader.go in the
standard library. Differences from the original are marked with "NOTE".

The tif_lzw.c file in the libtiff C library has this comment:

----
The 5.0 spec describes a different algorithm than Aldus
implements. Specifically, Aldus does code length transitions
one code earlier than should be done (for real LZW).
Earlier versions of this library implemented the correct
LZW algorithm, but emitted codes in a bit order opposite
to the TIFF spec. Thus, to maintain compatibility w/ Aldus
we interpret MSB-LSB ordered codes to be images written w/
old versions of this library, but otherwise adhere to the
Aldus "off by one" algorithm.
----

The Go code doesn't read (invalid) TIFF files written by old versions of
libtiff, but the LZW algorithm in this package still differs from the one in
Go's standard package library to accommodate this "off by one" in valid TIFFs.
*/

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// Order specifies the bit ordering in an LZW data stream.
type Order int

const (
	// LSB means Least Significant Bits first, as used in the GIF file format.
	LSB Order = iota
	// MSB means Most Significant Bits first, as used in the TIFF and PDF
	// file formats.
	MSB
)

const (
	maxWidth           = 12
	decoderInvalidCode = 0xffff
	flushBuffer        = 1 << maxWidth
)

// decoder is the state from which the readXxx method converts a byte
// stream into a code stream.
type decoder struct {
	// goimgtools: the input is buffered here, its bytes taken one at a
	// time without a call each (bufio's ReadByte was most of the time).
	r          io.Reader
	in         []byte
	inPos, inN int
	inErr      error
	bits       uint32 // LSB: the bits read and not used, the first lowest
	bits64     uint64 // MSB: the bits read and not used, the first highest
	nBits      uint
	width      uint
	msb        bool // codes most significant bit first, else least
	valid      bool // made with valid arguments
	litWidth   int  // width in bits of literal codes
	err        error

	// The first 1<<litWidth codes are literal codes.
	// The next two codes mean clear and EOF.
	// Other valid codes are in the range [lo, hi] where lo := clear + 2,
	// with the upper bound incrementing on each code seen.
	// overflow is the code at which hi overflows the code width. NOTE: TIFF's LZW is "off by one".
	// last is the most recently seen code, or decoderInvalidCode.
	clear, eof, hi, overflow, last uint16

	// Each code c in [lo, hi] expands to two or more bytes. For c != hi:
	//   suffix[c] is the last of these bytes.
	//   prefix[c] is the code for all but the last byte.
	//   This code can either be a literal code or another code in [lo, c).
	// The c == hi case is a special case.
	suffix [1 << maxWidth]uint8
	prefix [1 << maxWidth]uint16
	// goimgtools: length[c] is how many bytes code c expands to (1 for a
	// literal), and top the end of the entries saved since the table was
	// last cleared: a code below top has all its chain in this table, so
	// its bytes go straight to their place. (A code from top to hi, which
	// only a damaged stream has, keeps the standard library's path.)
	length [1 << maxWidth]uint16
	top    uint16

	// output is the temporary output buffer.
	// Literal codes are accumulated from the start of the buffer.
	// Non-literal codes decode to a sequence of suffixes that are first
	// written right-to-left from the end of the buffer before being copied
	// to the start of the buffer.
	// It is flushed when it contains >= 1<<maxWidth bytes,
	// so that there is always room to decode an entire code.
	output [2 * 1 << maxWidth]byte
	o      int    // write index into output
	toRead []byte // bytes to return from Read
}

// Size is about how much memory a Reader holds: its tables, its output
// and its input buffer.
const Size = 3*(1<<maxWidth) + 2*(1<<maxWidth) + inBuf

const inBuf = 32 << 10

// readByte returns the next input byte, as bufio.Reader's ReadByte does:
// an error only when no byte is left, the reader tried again on empty
// reads up to 100 times.
func (d *decoder) readByte() (byte, error) {
	if d.inPos < d.inN {
		b := d.in[d.inPos]
		d.inPos++
		return b, nil
	}
	if d.inErr != nil {
		err := d.inErr
		d.inErr = nil
		return 0, err
	}
	for i := 0; i < 100; i++ {
		n, err := d.r.Read(d.in)
		if n < 0 || n > len(d.in) {
			panic("lzw: reader returned an invalid count")
		}
		d.inPos, d.inN = 0, n
		if err != nil {
			d.inErr = err
		}
		if n > 0 {
			d.inPos = 1
			return d.in[0], nil
		}
		if err != nil {
			d.inErr = nil
			return 0, err
		}
	}
	return 0, io.ErrNoProgress
}

// readLSB returns the next code for "Least Significant Bits first" data.
func (d *decoder) readLSB() (uint16, error) {
	for d.nBits < d.width {
		x, err := d.readByte()
		if err != nil {
			return 0, err
		}
		d.bits |= uint32(x) << d.nBits
		d.nBits += 8
	}
	code := uint16(d.bits & (1<<d.width - 1))
	d.bits >>= d.width
	d.nBits -= d.width
	return code, nil
}

// topUpMSB reads bytes one at a time until a code's bits are there, for
// "Most Significant Bits first" data.
func (d *decoder) topUpMSB() error {
	for d.nBits < d.width {
		x, err := d.readByte()
		if err != nil {
			return err
		}
		d.bits64 |= uint64(x) << (56 - d.nBits)
		d.nBits += 8
	}
	return nil
}

func (d *decoder) Read(b []byte) (int, error) {
	for {
		if len(d.toRead) > 0 {
			n := copy(b, d.toRead)
			d.toRead = d.toRead[n:]
			return n, nil
		}
		if d.err != nil {
			return 0, d.err
		}
		d.decode()
	}
}

// decode decompresses bytes from r and leaves them in d.toRead.
// read specifies how to decode bytes into codes.
// litWidth is the width in bits of literal codes.
func (d *decoder) decode() {
	// Loop over the code stream, converting codes into decompressed bytes.
loop:
	for {
		var code uint16
		var err error
		if d.msb {
			// readMSB, inline: eight bytes at a time while eight are
			// buffered (the same bits in the same order), else one by one.
			if d.nBits < d.width {
				if d.inN-d.inPos >= 8 {
					v := binary.BigEndian.Uint64(d.in[d.inPos:])
					k := (64 - d.nBits) >> 3
					d.bits64 |= v >> d.nBits
					d.nBits += 8 * k
					d.bits64 &^= 1<<(64-d.nBits) - 1
					d.inPos += int(k)
				} else {
					err = d.topUpMSB()
				}
			}
			if err == nil {
				code = uint16(d.bits64 >> (64 - d.width))
				d.bits64 <<= d.width
				d.nBits -= d.width
			}
		} else {
			code, err = d.readLSB()
		}
		if err != nil {
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			d.err = err
			break
		}
		switch {
		case code < d.clear:
			// We have a literal code.
			d.output[d.o] = uint8(code)
			d.o++
			if d.last != decoderInvalidCode {
				// Save what the hi code expands to.
				d.suffix[d.hi] = uint8(code)
				d.prefix[d.hi] = d.last
				d.length[d.hi] = d.length[d.last] + 1
				d.top = d.hi + 1
			}
		case code == d.clear:
			d.width = 1 + uint(d.litWidth)
			d.hi = d.eof
			d.top = d.eof + 1
			d.overflow = 1 << d.width
			d.last = decoderInvalidCode
			continue
		case code == d.eof:
			d.err = io.EOF
			break loop
		case code >= d.eof+1 && code < d.top:
			n := int(d.length[code])
			c, i := code, d.o+n-1
			for c >= d.clear {
				d.output[i] = d.suffix[c]
				i--
				c = d.prefix[c]
			}
			d.output[i] = uint8(c)
			d.o += n
			if d.last != decoderInvalidCode {
				// Save what the hi code expands to.
				d.suffix[d.hi] = uint8(c)
				d.prefix[d.hi] = d.last
				d.length[d.hi] = d.length[d.last] + 1
				d.top = d.hi + 1
			}
		case code <= d.hi:
			c, i := code, len(d.output)-1
			if code == d.hi && d.last != decoderInvalidCode {
				// code == hi is a special case which expands to the last expansion
				// followed by the head of the last expansion. To find the head, we walk
				// the prefix chain until we find a literal code.
				c = d.last
				for c >= d.clear {
					c = d.prefix[c]
				}
				d.output[i] = uint8(c)
				i--
				c = d.last
			}
			// Copy the suffix chain into output and then write that to w.
			for c >= d.clear {
				d.output[i] = d.suffix[c]
				i--
				c = d.prefix[c]
			}
			d.output[i] = uint8(c)
			d.o += copy(d.output[d.o:], d.output[i:])
			if d.last != decoderInvalidCode {
				// Save what the hi code expands to.
				d.suffix[d.hi] = uint8(c)
				d.prefix[d.hi] = d.last
				d.length[d.hi] = d.length[d.last] + 1
				d.top = d.hi + 1
			}
		default:
			d.err = errors.New("lzw: invalid code")
			break loop
		}
		d.last, d.hi = code, d.hi+1
		if d.hi+1 >= d.overflow { // NOTE: the "+1" is where TIFF's LZW differs from the standard algorithm.
			if d.width == maxWidth {
				d.last = decoderInvalidCode
			} else {
				d.width++
				d.overflow <<= 1
			}
		}
		if d.o >= flushBuffer {
			break
		}
	}
	// Flush pending output.
	d.toRead = d.output[:d.o]
	d.o = 0
}

var errClosed = errors.New("lzw: reader/writer is closed")

func (d *decoder) Close() error {
	d.err = errClosed // in case any Reads come along
	return nil
}

// Reader decompresses TIFF-flavoured LZW data.
type Reader = decoder

// NewReader returns a Reader of the data in r. The number of bits to use for
// literal codes, litWidth, must be in the range [2,8] and is typically 8.
// The reader buffers its input, so it may read more from r than it needs.
func NewReader(r io.Reader, order Order, litWidth int) *Reader {
	d := new(decoder)
	switch order {
	case LSB:
	case MSB:
		d.msb = true
	default:
		d.err = errors.New("lzw: unknown order")
		return d
	}
	if litWidth < 2 || 8 < litWidth {
		d.err = fmt.Errorf("lzw: litWidth %d out of range", litWidth)
		return d
	}
	d.r, d.in, d.valid = r, make([]byte, inBuf), true
	d.litWidth = litWidth
	d.init()
	return d
}

func (d *decoder) init() {
	for i := 0; i < 1<<d.litWidth; i++ {
		d.length[i] = 1
	}
	d.width = 1 + uint(d.litWidth)
	d.clear = uint16(1) << uint(d.litWidth)
	d.eof, d.hi = d.clear+1, d.clear+1
	d.top = d.eof + 1
	d.overflow = uint16(1) << d.width
	d.last = decoderInvalidCode
}

// Reset makes d read a new stream from r, as a Reader newly made with the
// same order and litWidth would. A Reader whose NewReader arguments were
// invalid stays invalid.
func (d *decoder) Reset(r io.Reader) {
	if !d.valid {
		return
	}
	d.r, d.inPos, d.inN, d.inErr = r, 0, 0, nil
	d.bits, d.bits64, d.nBits, d.err = 0, 0, 0, nil
	d.o, d.toRead = 0, nil
	d.init()
}
