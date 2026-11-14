package pngstream

import (
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// benchRows are rows of a photograph as the encoder sees them: a filter
// byte and then RGB.
func benchRows(b *testing.B) (rows [][]byte, bpp int) {
	f, err := os.Open(filepath.Join(runtime.GOROOT(), "src", "image", "testdata", "video-001.jpeg"))
	if err != nil {
		b.Skip(err)
	}
	defer f.Close()
	m, err := jpeg.Decode(f)
	if err != nil {
		b.Fatal(err)
	}
	r := m.Bounds()
	for y := r.Min.Y; y < r.Max.Y; y++ {
		row := make([]byte, 1+3*r.Dx()*8)
		for rep := 0; rep < 8; rep++ {
			for x := r.Min.X; x < r.Max.X; x++ {
				c := m.(*image.YCbCr).YCbCrAt(x, y)
				R, G, B, _ := c.RGBA()
				o := 1 + 3*(rep*r.Dx()+x-r.Min.X)
				row[o], row[o+1], row[o+2] = uint8(R>>8), uint8(G>>8), uint8(B>>8)
			}
		}
		rows = append(rows, row)
	}
	return rows, 3
}

func BenchmarkFilter(b *testing.B) {
	rows, bpp := benchRows(b)
	var cr [nFilter][]byte
	for i := range cr {
		cr[i] = make([]byte, len(rows[0]))
	}
	n := 0
	for _, r := range rows {
		n += len(r) - 1
	}
	b.SetBytes(int64(n))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for y := 1; y < len(rows); y++ {
			copy(cr[0], rows[y])
			filter(&cr, rows[y-1], bpp)
		}
	}
}
