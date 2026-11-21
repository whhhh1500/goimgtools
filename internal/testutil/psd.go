package testutil

import (
	"bytes"
	"encoding/binary"
)

// PSDFile describes a Photoshop file for PSD.
type PSDFile struct {
	PSB         bool
	Mode, Depth int // colour mode (1 grey, 2 indexed, 3 RGB, 4 CMYK, 9 Lab, ...) and bits
	W, H        int
	// Channels are the planes of the composite, each its H rows as stored.
	Channels [][]byte
	RLE      bool
	Palette  []byte          // the 768 bytes of an indexed image
	Res      map[int][]byte  // image resources by id
	Layers   int             // the layer count, written when not 0
	Tagged   map[string]bool // global tagged blocks to write (empty), such as "Mtrn"
}

// PSD writes a Photoshop file of the composite image f describes.
func PSD(f PSDFile) []byte {
	var b bytes.Buffer
	be := binary.BigEndian
	u16 := func(v int) { b.Write(be.AppendUint16(nil, uint16(v))) }
	u32 := func(v int) { b.Write(be.AppendUint32(nil, uint32(v))) }
	length := func(v int) {
		if f.PSB {
			b.Write(be.AppendUint64(nil, uint64(v)))
		} else {
			u32(v)
		}
	}
	b.WriteString("8BPS")
	if f.PSB {
		u16(2)
	} else {
		u16(1)
	}
	b.Write(make([]byte, 6))
	u16(len(f.Channels))
	u32(f.H)
	u32(f.W)
	u16(f.Depth)
	u16(f.Mode)
	u32(len(f.Palette))
	b.Write(f.Palette)
	var res bytes.Buffer
	for _, id := range sortedKeys(f.Res) {
		d := f.Res[id]
		res.WriteString("8BIM")
		res.Write(be.AppendUint16(nil, uint16(id)))
		res.Write([]byte{0, 0}) // an empty name, padded
		res.Write(be.AppendUint32(nil, uint32(len(d))))
		res.Write(d)
		if len(d)%2 == 1 {
			res.WriteByte(0)
		}
	}
	u32(res.Len())
	b.Write(res.Bytes())
	var lm bytes.Buffer
	if f.Layers != 0 || len(f.Tagged) > 0 {
		// A layer info section holding only the count (no layer records
		// follow it: enough for a reader that skips them).
		li := be.AppendUint16(nil, uint16(int16(f.Layers)))
		li = append(li, 0, 0)
		if f.PSB {
			lm.Write(be.AppendUint64(nil, uint64(len(li))))
		} else {
			lm.Write(be.AppendUint32(nil, uint32(len(li))))
		}
		lm.Write(li)
		lm.Write(be.AppendUint32(nil, 0)) // no global layer mask
		for _, k := range []string{"Mtrn", "Mt16", "Mt32"} {
			if f.Tagged[k] {
				lm.WriteString("8BIM" + k)
				if f.PSB {
					lm.Write(be.AppendUint64(nil, 4))
				} else {
					lm.Write(be.AppendUint32(nil, 4))
				}
				lm.Write([]byte{0, 0, 0, 1})
			}
		}
	}
	length(lm.Len())
	b.Write(lm.Bytes())
	if !f.RLE {
		u16(0)
		for _, c := range f.Channels {
			b.Write(c)
		}
		return b.Bytes()
	}
	u16(1)
	rb := len(f.Channels[0]) / f.H
	var data bytes.Buffer
	for _, c := range f.Channels {
		for y := 0; y < f.H; y++ {
			p := PackBits(c[y*rb : (y+1)*rb])
			if f.PSB {
				u32(len(p))
			} else {
				u16(len(p))
			}
			data.Write(p)
		}
	}
	b.Write(data.Bytes())
	return b.Bytes()
}

// PackBits compresses a row as Photoshop does: runs of three or more of a
// byte, the rest as literals, 128 at most each.
func PackBits(row []byte) []byte {
	var out []byte
	for i := 0; i < len(row); {
		j := i + 1
		for j < len(row) && j-i < 128 && row[j] == row[i] {
			j++
		}
		if j-i >= 3 {
			out = append(out, byte(int8(1-(j-i))), row[i])
			i = j
			continue
		}
		// A literal up to the next run of three.
		j = i
		for j < len(row) && j-i < 128 && !(j+2 < len(row) && row[j] == row[j+1] && row[j] == row[j+2]) {
			j++
		}
		out = append(out, byte(j-i-1))
		out = append(out, row[i:j]...)
		i = j
	}
	return out
}

func sortedKeys(m map[int][]byte) []int {
	var k []int
	for id := range m {
		k = append(k, id)
	}
	for i := 1; i < len(k); i++ {
		for j := i; j > 0 && k[j] < k[j-1]; j-- {
			k[j], k[j-1] = k[j-1], k[j]
		}
	}
	return k
}
