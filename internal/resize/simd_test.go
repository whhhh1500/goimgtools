package resize

import (
	"bytes"
	"image"
	"math/rand/v2"
	"testing"

	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// TestVipsAVX2: libvips' reduce with AVX2 gives what the Go code gives,
// for every kernel, down and across, by ratios from barely below 1 to 20
// (box shrinks before), with transparency and without, widths below a
// chunk and odd ones.
func TestVipsAVX2(t *testing.T) {
	if !useAVX2 {
		t.Skip("no AVX2")
	}
	rng := rand.New(rand.NewPCG(13, 14))
	imgs := testutil.Images(203, 151)
	run := func(m image.Image, hs, vs float64, k VipsKernel, simd bool) []byte {
		t.Helper()
		useAVX2 = simd
		defer func() { useAVX2 = true }()
		src, err := Vips(testutil.Chopped(m, 7), hs, vs, k)
		if err != nil {
			t.Fatal(err)
		}
		got, err := pipeline.Collect(src)
		if err != nil {
			t.Fatal(err)
		}
		switch g := got.(type) {
		case *image.NRGBA:
			return g.Pix
		case *image.Gray:
			return g.Pix
		}
		t.Fatalf("%T", got)
		return nil
	}
	for i := range 300 {
		names := []string{"rgba", "nrgba", "gray", "rgba-opaque"}
		m := imgs[names[i%len(names)]]
		hs := 1 + rng.Float64()*[]float64{0.2, 2, 8, 19}[rng.IntN(4)]
		vs := 1 + rng.Float64()*[]float64{0.2, 2, 8, 19}[rng.IntN(4)]
		if i%7 == 0 {
			hs = 1
		}
		k := VipsKernel{Kind: []int{VipsLanczos3, VipsCubic, VipsLinear, VipsMitchell, VipsLanczos2, VipsMKS2013, VipsMKS2021}[rng.IntN(7)]}
		want, got := run(m, hs, vs, k, false), run(m, hs, vs, k, true)
		if !bytes.Equal(got, want) {
			t.Fatalf("%d: %s by %v, %v with %+v differs", i, names[i%len(names)], hs, vs, k)
		}
	}
}
