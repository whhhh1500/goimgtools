package gifstream

import (
	"encoding/binary"
	"errors"
	"io"
)

// lzwReader decodes the LZW data of a GIF frame as compress/lzw's Reader
// does (least significant bits first), to the same bytes and the same
// errors for any input, and has taken the same bytes from the blocks by
// the time each Read returns, so that blockReader.close finds what it
// would (goimgtools addition). It is faster in three ways: the bits come
// from the buffered block eight bytes at a time; what a code stands for
// is written where the caller wants it, its length known beforehand,
// instead of to a buffer of its own from the end and copied twice; and
// it is written eight bytes at a time, each code keeping the last bytes
// of its string as one word (as Wuffs' decoder does).
type lzwReader struct {
	br    *blockReader
	bits  uint64
	nBits uint
	width uint
	lit   uint // the width of a literal
	// As in compress/lzw: the codes below clear are literals; hi is the
	// code the next one read defines; overflow the code at which the
	// width grows; last the code before, or lzwNone.
	clear, eof, hi, overflow, last uint16
	err                            error

	// What a code stands for: its length, its first byte, its last bytes
	// (one to eight, the earliest lowest: those past the last multiple of
	// eight before its end) and the code of what comes before them (a
	// string of a multiple of eight bytes, so its word is whole).
	length [1 << lzwMaxWidth]uint16
	first  [1 << lzwMaxWidth]uint8
	chunk  [1 << lzwMaxWidth]uint64
	before [1 << lzwMaxWidth]uint16

	// pend is what was decoded and did not fit in the caller's buffer.
	pend []byte
	buf  [1<<lzwMaxWidth + 8]byte
}

const (
	lzwMaxWidth = 12
	lzwNone     = 0xffff
)

var (
	errLZWInvalid = errors.New("lzw: invalid code")
	errLZWClosed  = errors.New("lzw: reader/writer is closed")
)

// reset starts the reader on the data br gives, of literals litWidth bits
// wide (2 to 8).
func (r *lzwReader) reset(br *blockReader, litWidth uint) {
	r.br, r.bits, r.nBits = br, 0, 0
	r.lit, r.width = litWidth, litWidth+1
	r.clear = 1 << litWidth
	r.eof, r.hi = r.clear+1, r.clear+1
	r.overflow = 1 << r.width
	r.last = lzwNone
	r.err, r.pend = nil, nil
	for i := uint16(0); i < r.clear; i++ {
		r.length[i], r.first[i], r.chunk[i] = 1, uint8(i), uint64(i)
	}
}

// Close makes every later Read fail, as compress/lzw's.
func (r *lzwReader) Close() error {
	r.pend, r.err = nil, errLZWClosed
	return nil
}

func (r *lzwReader) Read(p []byte) (int, error) {
	n := copy(p, r.pend)
	r.pend = r.pend[n:]
	if n == len(p) {
		return n, nil
	}
	br := r.br
	tmp := &br.d.tmp
	bits, nBits, width := r.bits, r.nBits, r.width
	hi, last, overflow := r.hi, r.last, r.overflow
	clear, eof := r.clear, r.eof
	err := r.err
	for n < len(p) && err == nil {
		// The next code. Bytes are taken from the buffered block while
		// there is room for them, the next block only when the code needs
		// it (as compress/lzw asks for a byte only then).
		if nBits < width {
			for {
				if int(br.j)-int(br.i) >= 8 {
					k := (64 - nBits) >> 3
					bits |= binary.LittleEndian.Uint64(tmp[br.i:]) << nBits
					br.i += uint8(k)
					// (Above nBits are bits of the bytes that follow in
					// this block, which the next bytes taken put there
					// again.)
					nBits += 8 * k
					break
				}
				if br.i == br.j {
					if nBits >= width {
						break
					}
					if br.fill(); br.err != nil {
						break
					}
					continue
				}
				bits |= uint64(tmp[br.i]) << nBits
				br.i++
				if nBits += 8; nBits > 56 {
					break
				}
			}
			if nBits < width {
				if err = br.err; err == io.EOF {
					err = io.ErrUnexpectedEOF
				}
				break
			}
		}
		code := uint16(bits) & (1<<width - 1)
		bits >>= width
		nBits -= width

		if code >= clear && code <= eof {
			if code == eof {
				err = io.EOF
				break
			}
			width = r.lit + 1
			hi, overflow, last = eof, 1<<width, lzwNone
			continue
		}
		defined := false
		if code >= hi {
			if code > hi {
				err = errLZWInvalid
				break
			}
			// The code being defined: what the code before stands for,
			// and its first byte again. (With no code before, the table
			// is full and hi its last code, which stands as it is.)
			if last != lzwNone {
				r.define(hi, last, r.first[last])
				defined = true
			}
		}
		size := int(r.length[code])
		dst := p[n:]
		whole := size+8 <= len(dst)
		if !whole {
			dst = r.buf[:]
		}
		if size <= 8 {
			binary.LittleEndian.PutUint64(dst, r.chunk[code])
		} else {
			at := (size - 1) &^ 7
			for c := code; ; c = r.before[c] {
				binary.LittleEndian.PutUint64(dst[at:], r.chunk[c])
				if at == 0 {
					break
				}
				at -= 8
			}
		}
		if whole {
			n += size
		} else {
			k := copy(p[n:], dst[:size])
			n += k
			r.pend = dst[k:size]
		}
		if last != lzwNone && !defined {
			r.define(hi, last, r.first[code])
		}
		last, hi = code, hi+1
		if hi >= overflow {
			if width == lzwMaxWidth {
				last = lzwNone
				hi--
			} else {
				width++
				overflow = 1 << width
			}
		}
		if len(r.pend) > 0 {
			break
		}
	}
	// The whole bytes taken beyond those the codes read so far needed go
	// back to the block: all of them are of the block that is buffered.
	if k := nBits >> 3; k > 0 && br.err == nil {
		br.i -= uint8(k)
		nBits -= 8 * k
		bits &= 1<<nBits - 1
	}
	r.bits, r.nBits, r.width = bits, nBits, width
	r.hi, r.last, r.overflow = hi, last, overflow
	r.err = err
	if n == 0 {
		return 0, err
	}
	return n, nil
}

// define makes the code hi stand for what the code before stands for,
// followed by the byte b.
func (r *lzwReader) define(hi, before uint16, b uint8) {
	size := r.length[before]
	if k := size & 7; k != 0 {
		r.chunk[hi] = r.chunk[before] | uint64(b)<<(8*k)
		r.before[hi] = r.before[before]
	} else {
		r.chunk[hi] = uint64(b)
		r.before[hi] = before
	}
	r.first[hi], r.length[hi] = r.first[before], size+1
}
