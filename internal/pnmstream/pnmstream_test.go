package pnmstream

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"io"
	"testing"

	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// TestBinaryBytes: the binary files of maximum 255, the usual ones, are
// read with their bytes as the values: grey (P5, and a PAM of depth 1) as
// grey, grey with alpha, RGB (P6, and a PAM of depth 3) and RGBA as NRGBA;
// at sizes of several bands, of one row and of one column. A file cut
// short is an error at the band that lacks it.
func TestBinaryBytes(t *testing.T) {
	photo := testutil.Natural(301, 260, 7)
	for _, sz := range [][2]int{{301, 260}, {1, 1}, {1, 77}, {77, 1}, {16, 16}} {
		w, h := sz[0], sz[1]
		for depth := 1; depth <= 4; depth++ {
			// The values: the photograph's, with every byte value among the
			// first of them, and alpha that changes.
			data := make([]byte, w*h*depth)
			for i := 0; i < w*h; i++ {
				p := photo.Pix[photo.PixOffset(i%w, i/w):][:4]
				v := [4]byte{p[0], p[1], p[2], byte(i*7 + i/w)}
				if depth <= 2 {
					v[1] = v[3]
				}
				copy(data[i*depth:(i+1)*depth], v[:depth])
			}
			for i := 0; i < min(256, len(data)); i++ {
				data[i] = byte(255 - i)
			}
			want := image.NewNRGBA(image.Rect(0, 0, w, h))
			for i := 0; i < w*h; i++ {
				v, d := data[i*depth:(i+1)*depth], want.Pix[4*i:4*i+4]
				switch depth {
				case 1:
					d[0], d[1], d[2], d[3] = v[0], v[0], v[0], 255
				case 2:
					d[0], d[1], d[2], d[3] = v[0], v[0], v[0], v[1]
				case 3:
					d[0], d[1], d[2], d[3] = v[0], v[1], v[2], 255
				case 4:
					copy(d, v)
				}
			}
			heads := []string{fmt.Sprintf("P7\nWIDTH %d\nHEIGHT %d\nDEPTH %d\nMAXVAL 255\nENDHDR\n", w, h, depth)}
			switch depth {
			case 1:
				heads = append(heads, fmt.Sprintf("P5\n%d %d\n255\n", w, h))
			case 3:
				heads = append(heads, fmt.Sprintf("P6\n# a comment\n%d %d 255\n", w, h))
			}
			for _, head := range heads {
				file := append([]byte(head), data...)
				env := pipeline.NewEnv(0, t.TempDir())
				src, err := Open(bytes.NewReader(file), int64(len(file)), env)
				if err != nil {
					t.Fatalf("%q: %v", head, err)
				}
				if grey := src.ColorModel() == color.GrayModel; grey != (depth == 1) || pipeline.IsOpaque(src) != (depth%2 == 1) {
					t.Errorf("%q: grey %v, opaque %v", head, grey, pipeline.IsOpaque(src))
				}
				rows := pipeline.Map(src, func(*image.NRGBA) error { return nil })
				got, err := pipeline.Collect(rows)
				rows.Close() // and src
				if err != nil {
					t.Fatalf("%q: %v", head, err)
				}
				if g := got.(*image.NRGBA); !bytes.Equal(g.Pix, want.Pix) {
					t.Errorf("%q: not the picture of its bytes", head)
				}
				if env.Budget.Used() != 0 {
					t.Errorf("%q: %d bytes left reserved", head, env.Budget.Used())
				}
				// Cut short by a byte: refused at once (the size says so).
				if _, err := Open(bytes.NewReader(file[:len(file)-1]), int64(len(file)-1), nil); err != io.ErrUnexpectedEOF {
					t.Errorf("%q less a byte: %v", head, err)
				}
			}
		}
	}
}

// FuzzOpen: any file is read or refused, without a panic, within the
// memory it reserves.
func FuzzOpen(f *testing.F) {
	for _, s := range []string{
		"P1\n3 2\n1 0 1\n0 1 0\n", "P2\n# c\n2 2\n100\n0 50 100 7\n", "P3\n1 1\n255\n1 2 3\n",
		"P4\n9 1\n\xa5\x80", "P5 2 1 1000\n\x00\x01\x03\xe8", "P6\n1 1\n7\n\x01\x02\x03",
		"P7\nWIDTH 1\nHEIGHT 1\nDEPTH 2\nMAXVAL 255\nTUPLTYPE GRAYSCALE_ALPHA\nENDHDR\n\x80\x40",
		"PF\n1 1\n1.0\n\x3f\x00\x00\x00\x00\x00\x00\x00\x3f\x80\x00\x00",
	} {
		f.Add([]byte(s))
	}
	dir := f.TempDir() // one for every input: making one each is slow
	f.Fuzz(func(t *testing.T, data []byte) {
		env := pipeline.NewEnv(4<<20, dir)
		src, err := Open(bytes.NewReader(data), int64(len(data)), env)
		if err != nil {
			return
		}
		if b := src.Bounds(); int64(b.Dx())*int64(b.Dy()) > 1<<22 {
			src.Close()
			return
		}
		for {
			if _, err := src.Next(); err != nil {
				break
			}
		}
		src.Close()
		if env.Budget.Used() != 0 {
			t.Fatalf("%d bytes reserved", env.Budget.Used())
		}
	})
}
