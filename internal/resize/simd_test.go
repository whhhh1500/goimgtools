package resize

import (
	"bytes"
	"image"
	"math/rand/v2"
	"reflect"
	"testing"
	"unsafe"

	"golang.org/x/image/draw"

	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// TestVipsSIMD: libvips' reduce with AVX2 gives what the Go code gives,
// for every kernel, down and across, by ratios from barely below 1 to 20
// (box shrinks before), with transparency and without, widths below a
// chunk and odd ones.
func TestVipsSIMD(t *testing.T) {
	if !useSIMD {
		t.Skip("no SIMD")
	}
	rng := rand.New(rand.NewPCG(13, 14))
	imgs := testutil.Images(203, 151)
	run := func(m image.Image, hs, vs float64, k VipsKernel, simd bool) []byte {
		t.Helper()
		useSIMD = simd
		defer func() { useSIMD = true }()
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

// TestScaleSIMDLayout: the assembly passes read source and contrib at these
// offsets.
func TestScaleSIMDLayout(t *testing.T) {
	var s source
	var c contrib
	if unsafe.Sizeof(s) != 24 || unsafe.Offsetof(s.j) != 4 || unsafe.Offsetof(s.invTotalWeightFFFF) != 16 ||
		unsafe.Sizeof(c) != 16 || unsafe.Offsetof(c.weight) != 8 {
		t.Fatal("source or contrib laid out differently")
	}
}

// TestScaleSIMD: the default scaler with the SIMD passes gives what it
// gives with the Go code, to the bit, for every kind of strip, shrinking
// and enlarging, with and without helpers, in linear light too.
func TestScaleSIMD(t *testing.T) {
	if !useSIMD {
		t.Skip("no SIMD")
	}
	defer func() { useSIMD = true }()
	for name, m := range testutil.Images(97, 61) {
		for _, sz := range [][2]int{{40, 25}, {150, 90}, {97, 30}, {13, 200}} {
			for _, helpers := range []int{0, 3} {
				for _, linear := range []bool{false, true} {
					var outs [2]image.Image
					for k, simd := range []bool{false, true} {
						useSIMD = simd
						env := pipeline.NewEnv(0, "")
						env.SetHelpers(helpers)
						src := testutil.ChoppedEnv(m, 7, env)
						var sc pipeline.Source
						var err error
						if linear {
							sc, err = ScaleLinear(src, sz[0], sz[1], draw.CatmullRom, float64(m.Bounds().Dx()), float64(m.Bounds().Dy()))
						} else {
							sc, err = Scale(src, sz[0], sz[1], draw.CatmullRom)
						}
						if err != nil {
							t.Fatal(err)
						}
						if outs[k], err = pipeline.Collect(sc); err != nil {
							t.Fatal(err)
						}
					}
					if !reflect.DeepEqual(outs[0], outs[1]) {
						t.Errorf("%s to %v, %d helpers, linear %v: SIMD differs", name, sz, helpers, linear)
					}
				}
			}
		}
	}
}
