package lzw

import "io"

// goimgtools addition: the encoder of TIFF's LZW, as libtiff's (tif_lzw.c)
// writes it: codes most significant bit first, 256 the clear code and 257
// the end, the code width growing from 9 to 12 bits as the table fills,
// one code before the table outgrows it ("early change", which the
// decoder's lag of one code makes the same moment), and the table cleared
// when it is full.

const (
	codeClear = 256
	codeEOI   = 257
	codeFirst = 258
	codeMax   = 4095
	// The hash table: open addressing, keys of 20 bits (a code and a
	// byte), as in compress/lzw.
	tableSize = 4 * 4096
	tableMask = tableSize - 1
	invalid   = 0xffffffff
)

// Writer is an LZW encoder of TIFF's kind.
type Writer struct {
	w     io.Writer
	bits  uint32
	nBits uint
	width uint
	next  uint32 // the next code to assign
	prev  uint32 // the code of the bytes so far, or invalid
	table [tableSize]uint32
	out   []byte
	err   error
}

// NewWriter returns an encoder writing to w. Close writes the end.
func NewWriter(w io.Writer) *Writer {
	e := &Writer{out: make([]byte, 0, 4096)}
	e.Reset(w)
	return e
}

// Reset starts a new stream to w, as NewWriter, reusing e's memory.
func (e *Writer) Reset(w io.Writer) {
	e.w, e.prev, e.err = w, invalid, nil
	e.bits, e.nBits, e.out = 0, 0, e.out[:0]
	e.reset()
	e.emit(codeClear)
}

func (e *Writer) reset() {
	for i := range e.table {
		e.table[i] = invalid
	}
	e.width, e.next = 9, codeFirst
}

// emit writes a code of the current width.
func (e *Writer) emit(c uint32) {
	e.bits = e.bits<<e.width | c
	e.nBits += e.width
	for e.nBits >= 8 {
		e.nBits -= 8
		e.out = append(e.out, byte(e.bits>>e.nBits))
	}
	e.bits &= 1<<e.nBits - 1
	if len(e.out) >= 4096 && e.err == nil {
		_, e.err = e.w.Write(e.out)
		e.out = e.out[:0]
	}
}

// grow takes the code just assigned into account: the table cleared when
// it is full, the width widened when the next code needs it.
func (e *Writer) grow() {
	e.next++
	if e.next == codeMax-1 {
		e.emit(codeClear)
		e.reset()
		return
	}
	if e.next > 1<<e.width-1 {
		e.width++
	}
}

func (e *Writer) Write(p []byte) (int, error) {
	if e.err != nil {
		return 0, e.err
	}
	if len(p) == 0 {
		return 0, nil
	}
	prev := e.prev
	i := 0
	if prev == invalid {
		prev, i = uint32(p[0]), 1
	}
loop:
	for _, b := range p[i:] {
		key := prev<<8 | uint32(b)
		h := (key>>12 ^ key) & tableMask
		for t := e.table[h]; t != invalid; t = e.table[h] {
			if t>>12 == key {
				prev = t & 0xfff
				continue loop
			}
			h = (h + 1) & tableMask
		}
		e.emit(prev)
		e.table[h] = key<<12 | e.next
		e.grow()
		prev = uint32(b)
	}
	e.prev = prev
	return len(p), e.err
}

// Close writes the last code, the end code and the last bits. It does not
// close the underlying writer.
func (e *Writer) Close() error {
	if e.err != nil {
		return e.err
	}
	if e.prev != invalid {
		e.emit(e.prev)
		e.prev = invalid
		// As libtiff: the table grows by the code it would have added.
		e.next++
		if e.next == codeMax-1 {
			e.emit(codeClear)
			e.width = 9
		} else if e.next > 1<<e.width-1 {
			e.width++
		}
	}
	e.emit(codeEOI)
	if e.nBits > 0 {
		e.out = append(e.out, byte(e.bits<<(8-e.nBits)))
		e.nBits, e.bits = 0, 0
	}
	if e.err == nil && len(e.out) > 0 {
		_, e.err = e.w.Write(e.out)
		e.out = e.out[:0]
	}
	return e.err
}
