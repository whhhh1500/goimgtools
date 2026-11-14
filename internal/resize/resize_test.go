package resize

import (
	"bytes"
	"image"
	"testing"

	"golang.org/x/image/draw"

	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// TestMatchesXImage requires byte-identical output to x/image/draw for every
// source pixel type, for downscaling, upscaling, mixed and 1:1, with source
// strips of several heights.
func TestMatchesXImage(t *testing.T) {
	sizes := [][2]int{{31, 47}, {1, 1}, {150, 20}, {190, 141}, {500, 333}, {97, 400}, {191, 143}}
	kernels := map[string]*draw.Kernel{"catmullrom": draw.CatmullRom, "bilinear": draw.BiLinear}
	for name, m := range testutil.Images(191, 143) {
		for kn, q := range kernels {
			for i, sz := range sizes {
				want := image.NewRGBA(image.Rect(0, 0, sz[0], sz[1]))
				q.Scale(want, want.Bounds(), m, m.Bounds(), draw.Src, nil)
				src := testutil.Chopped(m, []int{1, 7, 16, 300}[i%4])
				sc, err := Scale(src, sz[0], sz[1], q)
				if err != nil {
					t.Fatal(err)
				}
				got, err := pipeline.Collect(sc)
				if err != nil {
					t.Fatalf("%s/%s/%v: %v", name, kn, sz, err)
				}
				sc.Close()
				if !bytes.Equal(rgbaPix(t, got), want.Pix) {
					t.Errorf("%s/%s/%v: pixels differ from x/image", name, kn, sz)
				}
				// A greyscale image stays one; nothing else becomes one.
				if _, isGray := got.(*image.Gray); isGray != (name == "gray") {
					t.Errorf("%s/%s/%v: scaled to %T", name, kn, sz, got)
				}
			}
		}
	}
}

// rgbaPix returns the pixels of a scaled image as RGBA bytes. A greyscale
// result stands for its value in red, green and blue, fully opaque: what
// x/image writes for a greyscale source.
func rgbaPix(t *testing.T, m image.Image) []uint8 {
	t.Helper()
	switch p := m.(type) {
	case *image.RGBA:
		return p.Pix
	case *image.Gray:
		out := make([]uint8, 0, len(p.Pix)*4)
		for _, v := range p.Pix {
			out = append(out, v, v, v, 255)
		}
		return out
	}
	t.Fatalf("scaled to %T", m)
	return nil
}

// TestSubImageSource checks a source whose bounds do not start at the origin.
func TestSubImageSource(t *testing.T) {
	full := testutil.Images(200, 160)["nrgba"].(*image.NRGBA)
	m := full.SubImage(image.Rect(13, 21, 180, 150))
	want := image.NewRGBA(image.Rect(0, 0, 60, 50))
	draw.CatmullRom.Scale(want, want.Bounds(), m, m.Bounds(), draw.Src, nil)
	sc, err := Scale(testutil.Chopped(m, 5), 60, 50, draw.CatmullRom)
	if err != nil {
		t.Fatal(err)
	}
	got, err := pipeline.Collect(sc)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(rgbaPix(t, got), want.Pix) {
		t.Error("pixels differ from x/image")
	}
}

// TestOpaqueStaysOpaque backs the scaler's Opaque claim: scaling an opaque
// source never produces an alpha below 255.
func TestOpaqueStaysOpaque(t *testing.T) {
	imgs := testutil.Images(191, 143)
	for _, name := range []string{"rgba-opaque", "gray", "gray16", "ycbcr420", "ycbcr411"} {
		for _, q := range []*draw.Kernel{draw.CatmullRom, draw.BiLinear} {
			for _, sz := range [][2]int{{31, 47}, {500, 333}, {190, 141}, {7, 400}} {
				sc, err := Scale(testutil.Chopped(imgs[name], 16), sz[0], sz[1], q)
				if err != nil {
					t.Fatal(err)
				}
				got, err := pipeline.Collect(sc)
				if err != nil {
					t.Fatal(err)
				}
				pix := rgbaPix(t, got)
				for i := 3; i < len(pix); i += 4 {
					if pix[i] != 0xff {
						t.Fatalf("%s %v: alpha %d at pixel %d", name, sz, pix[i], i/4)
					}
				}
			}
		}
	}
}
