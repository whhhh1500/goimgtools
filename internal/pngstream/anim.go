package pngstream

import (
	"encoding/binary"
	"errors"
	"io"
	"time"
)

// Animation is what the chunks of an animated PNG (APNG) file say about it.
type Animation struct {
	// Frames is the number of frames (fcTL chunks): 0 for a still image.
	Frames int
	// Duration is how long the frames show, once through.
	Duration time.Duration
	// LoopCount is how often the animation plays: 0 for ever.
	LoopCount int
}

// ReadAnimation reads the chunk headers of the PNG file r (size bytes) and
// the acTL and fcTL chunks, nothing else. A file without an acTL chunk
// before its image data is a still image: an Animation of no frames.
func ReadAnimation(r io.ReaderAt, size int64) (Animation, error) {
	var a Animation
	var b [26]byte
	n, err := r.ReadAt(b[:8], 0)
	if n < 8 {
		if err == nil || err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return a, err
	}
	if string(b[:8]) != pngHeader {
		return a, errors.New("png: invalid format: not a PNG file")
	}
	anim, seenIDAT := false, false
	for pos := int64(8); size-pos >= 8; {
		if _, err := r.ReadAt(b[:8], pos); err != nil {
			return a, err
		}
		length := int64(binary.BigEndian.Uint32(b[:4]))
		name := string(b[4:8])
		pos += 8
		switch name {
		case "acTL":
			if !seenIDAT && length >= 8 && size-pos >= 8 {
				if _, err := r.ReadAt(b[:8], pos); err != nil {
					return a, err
				}
				anim = true
				a.LoopCount = int(binary.BigEndian.Uint32(b[4:8]))
			}
		case "fcTL":
			if length >= 26 && size-pos >= 26 {
				if _, err := r.ReadAt(b[:26], pos); err != nil {
					return a, err
				}
				a.Frames++
				num, den := int64(binary.BigEndian.Uint16(b[20:])), int64(binary.BigEndian.Uint16(b[22:]))
				if den == 0 {
					den = 100
				}
				a.Duration += time.Duration(num) * time.Second / time.Duration(den)
			}
		case "IDAT":
			seenIDAT = true
		case "IEND":
			pos = size
		}
		pos += length + 4 // the data and the CRC
	}
	if !anim {
		return Animation{}, nil
	}
	return a, nil
}
