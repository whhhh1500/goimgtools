package vp8enc

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
	"math"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"golang.org/x/image/webp"

	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// decoded is a WebP file decoded by golang.org/x/image.
type decoded struct {
	w, h  int
	yc    *image.YCbCr
	alpha []uint8 // nil without an ALPH chunk
}

func decodeX(t testing.TB, data []byte) decoded {
	t.Helper()
	m, err := webp.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("x/image/webp cannot decode the output: %v", err)
	}
	d := decoded{w: m.Bounds().Dx(), h: m.Bounds().Dy()}
	switch p := m.(type) {
	case *image.YCbCr:
		d.yc = p
	case *image.NYCbCrA:
		d.yc = &p.YCbCr
		d.alpha = make([]uint8, d.w*d.h)
		for y := 0; y < d.h; y++ {
			copy(d.alpha[y*d.w:(y+1)*d.w], p.A[y*p.AStride:])
		}
	default:
		t.Fatalf("decoded to %T", m)
	}
	if d.yc.SubsampleRatio != image.YCbCrSubsampleRatio420 {
		t.Fatalf("decoded subsampling %v", d.yc.SubsampleRatio)
	}
	return d
}

func mustEncode(t testing.TB, src pipeline.Source, q int, dbg *debug) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := encode(&buf, src, &Options{Quality: q}, dbg); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	return buf.Bytes()
}

// srcPixel is pixel (x, y) of m as non-premultiplied 8-bit RGBA.
func srcPixel(m image.Image, x, y int) (r, g, b, a float64) {
	c := color.NRGBAModel.Convert(m.At(x, y)).(color.NRGBA)
	return float64(c.R), float64(c.G), float64(c.B), float64(c.A)
}

// srcLuma is the BT.601 limited-range luma the encoder should aim for.
func srcLuma(m image.Image) []float64 {
	b := m.Bounds()
	out := make([]float64, 0, b.Dx()*b.Dy())
	yc, _ := m.(*image.YCbCr)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if yc != nil {
				out = append(out, 16+float64(yc.Y[yc.YOffset(x, y)])*219/255)
				continue
			}
			r, g, bl, _ := srcPixel(m, x, y)
			out = append(out, 16+(65.738*r+129.057*g+25.064*bl)/256)
		}
	}
	return out
}

func lumaPSNR(d decoded, want []float64) float64 {
	var sum float64
	for y := 0; y < d.h; y++ {
		for x := 0; x < d.w; x++ {
			e := float64(d.yc.Y[y*d.yc.YStride+x]) - want[y*d.w+x]
			sum += e * e
		}
	}
	if sum == 0 {
		return 99
	}
	return 10 * math.Log10(255*255*float64(d.w*d.h)/sum)
}

// The decoder side of the colour conversion, as libwebp does it (yuv.h).
func multHi(v, coeff int) int { return (v * coeff) >> 8 }

func yuvClip(v int) uint8 {
	if v&^(256<<6-1) == 0 {
		return uint8(v >> 6)
	}
	if v < 0 {
		return 0
	}
	return 255
}

func yuvToRGB(y, u, v int, dst []uint8) {
	dst[0] = yuvClip(multHi(y, 19077) + multHi(v, 26149) - 14234)
	dst[1] = yuvClip(multHi(y, 19077) - multHi(u, 6419) - multHi(v, 13320) + 8708)
	dst[2] = yuvClip(multHi(y, 19077) + multHi(u, 33050) - 17685)
}

// upsampleRow is libwebp's "fancy" chroma upsampler for one output row:
// near is the chroma row closer to it and far the other one.
func upsampleRow(yRow, nearU, nearV, farU, farV []uint8, dst []uint8) {
	w := len(yRow)
	mix := func(n, f []uint8, x int) (int, int) { return int(n[x]), int(f[x]) }
	{
		nu, fu := mix(nearU, farU, 0)
		nv, fv := mix(nearV, farV, 0)
		yuvToRGB(int(yRow[0]), (3*nu+fu+2)>>2, (3*nv+fv+2)>>2, dst)
	}
	last := (w - 1) >> 1
	for x := 1; x <= last; x++ {
		var uv [2][2]int
		for c, pl := range [2][2][]uint8{{nearU, farU}, {nearV, farV}} {
			tl, t := int(pl[0][x-1]), int(pl[0][x]) // near row: left, right
			l, cur := int(pl[1][x-1]), int(pl[1][x])
			avg := tl + t + l + cur + 8
			diag12 := (avg + 2*(t+l)) >> 3
			diag03 := (avg + 2*(tl+cur)) >> 3
			uv[c][0] = (diag12 + tl) >> 1
			uv[c][1] = (diag03 + t) >> 1
		}
		yuvToRGB(int(yRow[2*x-1]), uv[0][0], uv[1][0], dst[3*(2*x-1):])
		yuvToRGB(int(yRow[2*x]), uv[0][1], uv[1][1], dst[3*2*x:])
	}
	if w&1 == 0 {
		nu, fu := mix(nearU, farU, last)
		nv, fv := mix(nearV, farV, last)
		yuvToRGB(int(yRow[w-1]), (3*nu+fu+2)>>2, (3*nv+fv+2)>>2, dst[3*(w-1):])
	}
}

// toRGB converts decoded planes to RGB exactly as libwebp does by default.
func toRGB(d decoded) []uint8 {
	out := make([]uint8, d.w*d.h*3)
	cw, ch := (d.w+1)/2, (d.h+1)/2
	crow := func(pl []uint8, y int) []uint8 { return pl[y*d.yc.CStride : y*d.yc.CStride+cw] }
	for y := 0; y < d.h; y++ {
		near := y / 2
		far := near + 1
		if y&1 == 0 {
			far = near - 1
		}
		far = max(0, min(far, ch-1))
		upsampleRow(d.yc.Y[y*d.yc.YStride:y*d.yc.YStride+d.w],
			crow(d.yc.Cb, near), crow(d.yc.Cr, near), crow(d.yc.Cb, far), crow(d.yc.Cr, far),
			out[y*d.w*3:(y+1)*d.w*3])
	}
	return out
}

func rgbPSNR(m image.Image, rgb []uint8) float64 {
	b := m.Bounds()
	var sum float64
	i := 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := srcPixel(m, x, y)
			for c, v := range [3]float64{r, g, bl} {
				e := v - float64(rgb[i+c])
				sum += e * e
			}
			i += 3
		}
	}
	if sum == 0 {
		return 99
	}
	return 10 * math.Log10(255*255*float64(len(rgb))/sum)
}

// photoLike is a smooth picture with a few edges, in the given pixel type.
func photoLike(w, h int) *image.NRGBA {
	m := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			fx, fy := float64(x)/float64(w), float64(y)/float64(h)
			r := 128 + 100*math.Sin(3*fx+fy)
			g := 128 + 90*math.Cos(2*fy-fx)
			b := 40 + 170*fx*fy
			if (x-w/2)*(x-w/2)+(y-h/2)*(y-h/2) < w*h/30 {
				r, g, b = 230-80*fx, 60+50*fy, 70
			}
			m.SetNRGBA(x, y, color.NRGBA{uint8(r), uint8(g), uint8(b), 255})
		}
	}
	return m
}

func sortedNames(m map[string]image.Image) []string {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

var testSizes = [][2]int{{67, 131}, {16, 16}, {1, 1}, {17, 33}, {640, 480}}

// minLumaPSNR is the luma PSNR the noisy test images must reach.
func minLumaPSNR(q int) float64 {
	return map[int]float64{1: 19, 30: 28.5, 75: 34, 100: 48}[q]
}

func TestRoundTrip(t *testing.T) {
	for _, sz := range testSizes {
		imgs := testutil.Images(sz[0], sz[1])
		for _, name := range sortedNames(imgs) {
			m := imgs[name]
			want := srcLuma(m)
			prev := 0.0
			for _, q := range []int{1, 30, 75, 100} {
				var ref []byte
				for _, n := range []int{1, 7, 16, 500} {
					if sz[0] == 640 && n != 16 && (q == 30 || q == 100) {
						continue // keep the test fast
					}
					env := pipeline.NewEnv(0, t.TempDir())
					data := mustEncode(t, testutil.ChoppedEnv(m, n, env), q, nil)
					if env.Budget.Used() != 0 {
						t.Fatalf("%s %v q%d n%d: %d bytes of budget still reserved", name, sz, q, n, env.Budget.Used())
					}
					if ref == nil {
						ref = data
					} else if !bytes.Equal(ref, data) {
						t.Fatalf("%s %v q%d: strips of %d rows give different bytes", name, sz, q, n)
					}
				}
				d := decodeX(t, ref)
				if d.w != sz[0] || d.h != sz[1] {
					t.Fatalf("%s %v q%d: decoded size %dx%d", name, sz, q, d.w, d.h)
				}
				p := lumaPSNR(d, want)
				if testing.Verbose() && sz[0] == 640 {
					t.Logf("%s %v q%d: %d bytes, luma %.2f dB", name, sz, q, len(ref), p)
				}
				if sz[0] >= 64 {
					if p < minLumaPSNR(q) {
						t.Errorf("%s %v q%d: luma PSNR %.2f dB, want at least %.0f", name, sz, q, p, minLumaPSNR(q))
					}
					if p < prev-0.01 {
						t.Errorf("%s %v q%d: luma PSNR %.2f dB is below the %.2f dB of a lower quality", name, sz, q, p, prev)
					}
					prev = p
				}
			}
		}
	}
}

// TestColour checks the whole colour path on a clean picture: the decoded
// file, converted to RGB the way libwebp does, must be close to the source.
func TestColour(t *testing.T) {
	m := photoLike(320, 240)
	min := map[int]float64{1: 31, 30: 37, 75: 39, 100: 41}
	prev := 0
	for _, q := range []int{1, 30, 75, 100} {
		data := mustEncode(t, pipeline.FromImage(m, nil), q, nil)
		p := rgbPSNR(m, toRGB(decodeX(t, data)))
		t.Logf("q%d: %d bytes, RGB %.2f dB", q, len(data), p)
		if p < min[q] {
			t.Errorf("q%d: RGB PSNR %.2f dB, want at least %.0f", q, p, min[q])
		}
		if len(data) <= prev {
			t.Errorf("q%d: %d bytes, not more than the %d of a lower quality", q, len(data), prev)
		}
		prev = len(data)
	}
	// Quality 0 and a nil Options mean the default.
	var a, b, c bytes.Buffer
	if err := Encode(&a, pipeline.FromImage(m, nil), nil); err != nil {
		t.Fatal(err)
	}
	if err := Encode(&b, pipeline.FromImage(m, nil), &Options{}); err != nil {
		t.Fatal(err)
	}
	if err := Encode(&c, pipeline.FromImage(m, nil), &Options{Quality: DefaultQuality}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.Bytes(), c.Bytes()) || !bytes.Equal(b.Bytes(), c.Bytes()) {
		t.Error("default quality is not 75")
	}
}

// compareRecon reports how many decoded samples differ from the encoder's
// own reconstruction.
func compareRecon(d decoded, dbg *debug) int {
	w16 := (d.w + 15) &^ 15
	bad := 0
	for y := 0; y < d.h; y++ {
		for x := 0; x < d.w; x++ {
			if d.yc.Y[y*d.yc.YStride+x] != dbg.y[y*w16+x] {
				bad++
			}
		}
	}
	for y := 0; y < (d.h+1)/2; y++ {
		for x := 0; x < (d.w+1)/2; x++ {
			if d.yc.Cb[y*d.yc.CStride+x] != dbg.u[y*(w16/2)+x] || d.yc.Cr[y*d.yc.CStride+x] != dbg.v[y*(w16/2)+x] {
				bad++
			}
		}
	}
	return bad
}

// TestReconstruction: the encoder predicts from its own reconstruction, so
// that must be exactly what a decoder produces (with the loop filter off,
// which only changes the decoder's final output).
func TestReconstruction(t *testing.T) {
	for _, sz := range testSizes {
		imgs := testutil.Images(sz[0], sz[1])
		imgs["photo"] = photoLike(sz[0], sz[1])
		for _, name := range sortedNames(imgs) {
			for _, q := range []int{1, 30, 75, 100} {
				variants := []debug{
					{},
					{forceParts: 4},
					{modeBudget: int64((sz[0]+15)/16*((sz[1]+15)/16)) * 5 * 256}, // 5 bits a macroblock
				}
				if sz[0] == 640 {
					variants = variants[:1] // keep the test fast
				}
				for _, variant := range variants {
					dbg := variant
					dbg.noFilter, dbg.rec = true, true
					data := mustEncode(t, testutil.Chopped(imgs[name], 5), q, &dbg)
					d := decodeX(t, data)
					if bad := compareRecon(d, &dbg); bad != 0 {
						t.Fatalf("%s %v q%d %+v: %d samples differ from the encoder's reconstruction",
							name, sz, q, variant, bad)
					}
					if variant.modeBudget != 0 && dbg.modeBits > variant.modeBudget {
						t.Fatalf("%s %v q%d: modes take %d, budget %d", name, sz, q, dbg.modeBits, variant.modeBudget)
					}
				}
			}
		}
	}
}

// TestRandomImages runs many small images of different character through
// the encoder, with the overrides that reach the rarely used paths.
func TestRandomImages(t *testing.T) {
	r := rand.New(rand.NewSource(99))
	for iter := 0; iter < 500; iter++ {
		w, h := 1+r.Intn(90), 1+r.Intn(90)
		m := image.NewNRGBA(image.Rect(0, 0, w, h))
		kind := r.Intn(5)
		amp := 1 + r.Intn(255)
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				var c color.NRGBA
				switch kind {
				case 0: // noise of some amplitude
					c = color.NRGBA{uint8(r.Intn(amp)), uint8(r.Intn(amp)), uint8(r.Intn(amp)), 255}
				case 1: // gradients
					c = color.NRGBA{uint8(x * 255 / w), uint8(y * 255 / h), uint8(r.Intn(amp)), 255}
				case 2: // hard edges
					v := uint8(255 * ((x/3 + y/5) & 1))
					c = color.NRGBA{v, v, v, 255}
				case 3: // flat colour, noisy alpha
					c = color.NRGBA{uint8(amp), uint8(amp), uint8(amp), uint8(r.Intn(256))}
				default:
					c = color.NRGBA{uint8(x*y + r.Intn(amp)), uint8(x ^ y), uint8(x + y), 255}
				}
				m.SetNRGBA(x, y, c)
			}
		}
		q := 1 + r.Intn(100)
		dbg := &debug{noFilter: true, rec: true}
		if r.Intn(4) == 0 {
			dbg.forceParts = 1 << r.Intn(4)
		}
		if r.Intn(4) == 0 {
			dbg.modeBudget = int64((w+15)/16*((h+15)/16)) * int64(900+r.Intn(3000))
		}
		data := mustEncode(t, pipeline.FromImage(m, nil), q, dbg)
		riffChunks(t, data)
		d := decodeX(t, data)
		if bad := compareRecon(d, dbg); bad != 0 {
			t.Fatalf("iteration %d (%dx%d q%d kind %d): %d samples differ from the encoder's reconstruction",
				iter, w, h, q, kind, bad)
		}
		// And as normally written, with the loop filter.
		if d := decodeX(t, mustEncode(t, pipeline.FromImage(m, nil), q, nil)); d.w != w || d.h != h {
			t.Fatalf("iteration %d: decoded %dx%d, want %dx%d", iter, d.w, d.h, w, h)
		}
	}
}

// TestModeBudget: the estimate that keeps the first partition under its
// format limit must not be below what the arithmetic coder really produces.
func TestModeBudget(t *testing.T) {
	m := testutil.Images(640, 480)["rgba-opaque"]
	for _, q := range []int{30, 75, 100} {
		dbg := &debug{}
		mustEncode(t, pipeline.FromImage(m, nil), q, dbg)
		est := (dbg.modeBits + 7) / 8 / 256
		t.Logf("q%d: modes estimated at %d bytes, first partition %d bytes, %d%% 4x4", q, est, dbg.p0Len, dbg.usedI4*100/(40*30))
		// The partition also holds the header (at most 2KB of probabilities).
		if dbg.p0Len > est*32/31+2200 {
			t.Errorf("q%d: first partition %d bytes, estimate %d", q, dbg.p0Len, est)
		}
	}
}

const pilScript = `
import sys
from PIL import Image
bad = 0
for line in open(sys.argv[1]):
    webp, raw, w, h, ch = line.split()
    w, h, ch = int(w), int(h), int(ch)
    try:
        im = Image.open(webp)
        im.load()
    except Exception as e:
        print("FAIL", webp, "libwebp cannot decode:", e); bad += 1; continue
    if im.size != (w, h):
        print("FAIL", webp, "size", im.size); bad += 1; continue
    if (im.mode == "RGBA") != (ch == 4):
        print("FAIL", webp, "mode", im.mode); bad += 1; continue
    got = im.tobytes()
    want = open(raw, "rb").read()
    if got != want:
        n = sum(1 for a, b in zip(got, want) if a != b)
        mx = max(abs(a - b) for a, b in zip(got, want))
        print("FAIL", webp, "differs from x/image in", n, "bytes, max", mx); bad += 1
print("checked", bad)
`

// TestLibwebp decodes the encoder's files with libwebp (through Python's
// PIL) and requires the pixels to be identical to what golang.org/x/image
// decodes, converted to RGB with libwebp's arithmetic. A bitstream both
// decoders read the same way is a valid one.
func TestLibwebp(t *testing.T) {
	py, err := exec.LookPath("python")
	if err != nil {
		t.Skip("python not found")
	}
	if out, err := exec.Command(py, "-c", "from PIL import features; assert features.check('webp')").CombinedOutput(); err != nil {
		t.Skipf("PIL with WebP support not available: %v %s", err, out)
	}
	dir := t.TempDir()
	var list strings.Builder
	n := 0
	add := func(label string, data []byte) {
		d := decodeX(t, data)
		rgb := toRGB(d)
		ch := 3
		if d.alpha != nil {
			ch = 4
			rgba := make([]uint8, d.w*d.h*4)
			for i := 0; i < d.w*d.h; i++ {
				copy(rgba[4*i:], rgb[3*i:3*i+3])
				rgba[4*i+3] = d.alpha[i]
			}
			rgb = rgba
		}
		base := filepath.Join(dir, fmt.Sprintf("%03d_%s", n, label))
		n++
		if err := os.WriteFile(base+".webp", data, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(base+".raw", rgb, 0o644); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&list, "%s.webp %s.raw %d %d %d\n", base, base, d.w, d.h, ch)
	}
	for _, sz := range [][2]int{{67, 131}, {1, 1}, {16, 16}, {17, 33}, {200, 150}} {
		imgs := testutil.Images(sz[0], sz[1])
		imgs["photo"] = photoLike(sz[0], sz[1])
		for _, name := range sortedNames(imgs) {
			for _, q := range []int{1, 30, 75, 100} {
				label := fmt.Sprintf("%s_%dx%d_q%d", name, sz[0], sz[1], q)
				add(label, mustEncode(t, testutil.Chopped(imgs[name], 16), q, nil))
				if sz[0] == 67 {
					add(label+"_parts8", mustEncode(t, testutil.Chopped(imgs[name], 16), q, &debug{forceParts: 8}))
					add(label+"_tight", mustEncode(t, testutil.Chopped(imgs[name], 16), q, &debug{modeBudget: 5 * 9 * 4 * 256}))
				}
			}
		}
	}
	// Flat pictures: every macroblock skipped, with and without the skip flag.
	flat := image.NewGray(image.Rect(0, 0, 100, 60))
	add("flat", mustEncode(t, pipeline.FromImage(flat, nil), 75, nil))
	add("flat_noskip", mustEncode(t, pipeline.FromImage(flat, nil), 75, &debug{modeBudget: 1}))
	// Alpha planes of every kind, compressed or not.
	for _, name := range alphaKinds {
		for _, sz := range [][2]int{{150, 100}, {1, 40}, {300, 3}, {5000, 4}} {
			add(fmt.Sprintf("alpha_%s_%dx%d", name, sz[0], sz[1]),
				mustEncode(t, pipeline.FromImage(alphaImage(name, sz[0], sz[1]), nil), 60, nil))
		}
	}

	listFile := filepath.Join(dir, "list.txt")
	if err := os.WriteFile(listFile, []byte(list.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(py, "-c", pilScript, listFile).CombinedOutput()
	if err != nil {
		t.Fatalf("python: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "checked 0") {
		t.Fatalf("libwebp disagrees (%d files):\n%s", n, out)
	}
	t.Logf("libwebp and x/image agree on %d files", n)
}

var alphaKinds = []string{"disc", "ramp", "vramp", "noise", "zero", "blocks", "dots"}

// alphaImage is a picture whose alpha plane has the given character.
func alphaImage(kind string, w, h int) *image.NRGBA {
	m := image.NewNRGBA(image.Rect(0, 0, w, h))
	r := rand.New(rand.NewSource(11))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var a int
			switch kind {
			case "disc": // opaque shape with a soft edge on a transparent ground
				d := math.Hypot(float64(x)-float64(w)/2, float64(y)-float64(h)/2)
				a = int(255 * math.Max(0, math.Min(1, (float64(min(w, h))*0.4-d)/3)))
			case "ramp":
				a = x * 255 / w
			case "vramp":
				a = y * 255 / h
			case "noise":
				a = r.Intn(256)
			case "zero":
				a = 0
			case "blocks":
				a = 255 * ((x/13 + y/7) & 1)
			case "dots": // mostly opaque
				a = 255
				if r.Intn(50) == 0 {
					a = r.Intn(256)
				}
			}
			m.SetNRGBA(x, y, color.NRGBA{uint8(x), uint8(y), uint8(x + y), uint8(a)})
		}
	}
	return m
}

// riffChunks lists the chunks of a WebP file.
func riffChunks(t testing.TB, data []byte) map[string][]byte {
	t.Helper()
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		t.Fatal("not a WebP file")
	}
	if n := int(data[4]) | int(data[5])<<8 | int(data[6])<<16 | int(data[7])<<24; n != len(data)-8 {
		t.Fatalf("RIFF size %d in a file of %d bytes", n, len(data))
	}
	chunks := map[string][]byte{}
	for p := 12; p < len(data); {
		if p+8 > len(data) {
			t.Fatal("truncated chunk header")
		}
		n := int(data[p+4]) | int(data[p+5])<<8 | int(data[p+6])<<16 | int(data[p+7])<<24
		if p+8+n > len(data) {
			t.Fatal("chunk runs past the end of the file")
		}
		chunks[string(data[p:p+4])] = data[p+8 : p+8+n]
		p += 8 + n + n&1
	}
	return chunks
}

// TestAlphaPlanes: the alpha plane comes back exactly, and planes with
// structure are stored compressed.
func TestAlphaPlanes(t *testing.T) {
	for _, name := range alphaKinds {
		for _, sz := range [][2]int{{150, 100}, {1, 40}, {300, 3}, {5000, 4}, {129, 257}} {
			w, h := sz[0], sz[1]
			m := alphaImage(name, w, h)
			env := pipeline.NewEnv(0, t.TempDir())
			data := mustEncode(t, testutil.ChoppedEnv(m, 11, env), 60, nil)
			d := decodeX(t, data)
			if d.alpha == nil {
				t.Fatalf("%s %v: no alpha", name, sz)
			}
			for i := 0; i < w*h; i++ {
				if d.alpha[i] != m.Pix[4*i+3] {
					t.Fatalf("%s %v: alpha differs at %d,%d", name, sz, i%w, i/w)
				}
			}
			alph := riffChunks(t, data)["ALPH"]
			raw := 1 + w*h
			t.Logf("%s %v: ALPH %d bytes (raw %d), header %#x", name, sz, len(alph), raw, alph[0])
			switch {
			case len(alph) > raw:
				t.Errorf("%s %v: ALPH chunk of %d bytes is larger than the plane", name, sz, len(alph))
			case name == "noise" && w*h > 1000 && alph[0] != 0:
				t.Errorf("%s %v: noise stored with header %#x", name, sz, alph[0])
			case name != "noise" && w*h > 10000 && len(alph) > raw/5:
				t.Errorf("%s %v: ALPH chunk of %d bytes for a plane of %d", name, sz, len(alph), raw)
			}
			if env.Budget.Used() != 0 {
				t.Errorf("%s %v: budget still reserved", name, sz)
			}
		}
	}
}

// TestAlphaSpool checks the run-length packing of the spooled alpha rows.
func TestAlphaSpool(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	for _, w := range []int{1, 2, 3, 127, 128, 129, 130, 300, 70000} {
		env := pipeline.NewEnv(0, t.TempDir())
		st := newStore(env)
		sp := newAlphaSpool(st, w)
		var rows [][]uint8
		for i := 0; i < 40; i++ {
			row := make([]uint8, w)
			switch i % 4 {
			case 0: // flat
				for x := range row {
					row[x] = uint8(i)
				}
			case 1: // noise
				r.Read(row)
			case 2: // runs of random length
				for x := 0; x < w; {
					n, v := 1+r.Intn(1+r.Intn(400)), uint8(r.Intn(4))
					for ; n > 0 && x < w; n-- {
						row[x] = v
						x++
					}
				}
			default: // pairs, which are not worth a run
				for x := range row {
					row[x] = uint8(x / 2)
				}
			}
			if err := sp.add(row); err != nil {
				t.Fatal(err)
			}
			if len(sp.pack) > int(alphaSpoolBytes(w)) {
				t.Fatalf("w %d: packed row of %d bytes exceeds the bound %d", w, len(sp.pack), alphaSpoolBytes(w))
			}
			rows = append(rows, row)
		}
		for pass := 0; pass < 2; pass++ {
			sp.rewind(make([]byte, 100))
			got := make([]uint8, w)
			for i, row := range rows {
				if err := sp.next(got); err != nil {
					t.Fatalf("w %d row %d: %v", w, i, err)
				}
				if !bytes.Equal(got, row) {
					t.Fatalf("w %d row %d differs", w, i)
				}
			}
		}
		st.Close()
		if env.Budget.Used() != 0 {
			t.Errorf("budget still reserved")
		}
	}
}

func TestAlpha(t *testing.T) {
	for _, sz := range [][2]int{{67, 131}, {1, 1}, {16, 16}, {33, 17}} {
		imgs := testutil.Images(sz[0], sz[1])
		for _, name := range sortedNames(imgs) {
			m := imgs[name]
			transparent := false
			want := make([]uint8, 0, sz[0]*sz[1])
			for y := 0; y < sz[1]; y++ {
				for x := 0; x < sz[0]; x++ {
					_, _, _, a := m.At(x, y).RGBA()
					want = append(want, uint8(a>>8))
					transparent = transparent || a>>8 != 0xff
				}
			}
			env := pipeline.NewEnv(0, t.TempDir())
			data := mustEncode(t, testutil.ChoppedEnv(m, 3, env), 75, nil)
			d := decodeX(t, data)
			if transparent != (d.alpha != nil) {
				t.Fatalf("%s %v: transparent %v, ALPH chunk %v", name, sz, transparent, d.alpha != nil)
			}
			if got := string(data[12:16]); (got == "VP8X") != transparent {
				t.Fatalf("%s %v: first chunk %q", name, sz, got)
			}
			if transparent && !bytes.Equal(d.alpha, want) {
				t.Fatalf("%s %v: alpha plane changed", name, sz)
			}
			if env.Budget.Used() != 0 {
				t.Fatalf("%s %v: budget still reserved", name, sz)
			}
		}
	}

	// A source that says it is opaque gets no alpha handling at all.
	m := testutil.Images(40, 40)["nrgba"]
	data := mustEncode(t, opaque{testutil.Chopped(m, 9)}, 75, nil)
	if d := decodeX(t, data); d.alpha != nil {
		t.Error("ALPH chunk for a source that declared itself opaque")
	}

	// Colours under partial transparency are stored non-premultiplied.
	pre := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			pre.Set(x, y, color.NRGBA{200, 100, 50, uint8(64 + 2*x)})
		}
	}
	d := decodeX(t, mustEncode(t, pipeline.FromImage(pre, nil), 90, nil))
	rgb := toRGB(d)
	for i, want := range [3]int{200, 100, 50} {
		if got := int(rgb[(32*64+10)*3+i]); got < want-6 || got > want+6 {
			t.Errorf("channel %d under alpha: got %d, want about %d", i, got, want)
		}
	}
}

type opaque struct{ pipeline.Source }

func (opaque) Opaque() bool { return true }

func tempFiles(t *testing.T, dir string) int {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	return len(ents)
}

// TestSmallBudget encodes a 2000x2000 image with transparency under a 2MB
// memory limit: the spooled data has to go to temporary files.
func TestSmallBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("large image")
	}
	const w, h = 2000, 2000
	m := image.NewNRGBA(image.Rect(0, 0, w, h))
	r := rand.New(rand.NewSource(3))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			n := uint8(r.Intn(24))
			m.SetNRGBA(x, y, color.NRGBA{uint8(x/8) + n, uint8(y/8) + n, uint8((x+y)/16) + n, uint8(255 - (x*y)%97)})
		}
	}
	const limit = 2 << 20
	dir := t.TempDir()
	env := pipeline.NewEnv(limit, dir)
	data := mustEncode(t, testutil.ChoppedEnv(m, 37, env), 75, nil)
	if env.Budget.Peak() > limit {
		t.Errorf("budget peak %d over the limit %d", env.Budget.Peak(), limit)
	}
	if env.Budget.Used() != 0 {
		t.Errorf("%d bytes of budget still reserved", env.Budget.Used())
	}
	if n := tempFiles(t, dir); n != 0 {
		t.Errorf("%d temporary files left behind", n)
	}
	t.Logf("%d bytes, budget peak %d", len(data), env.Budget.Peak())

	// The same bytes as with plenty of memory.
	big := pipeline.NewEnv(0, dir)
	if !bytes.Equal(data, mustEncode(t, testutil.ChoppedEnv(m, 16, big), 75, nil)) {
		t.Error("output depends on the memory limit")
	}
	t.Logf("budget peak without a limit: %d", big.Budget.Peak())
	d := decodeX(t, data)
	if d.w != w || d.h != h || d.alpha == nil {
		t.Fatalf("decoded %dx%d alpha %v", d.w, d.h, d.alpha != nil)
	}
	for y := 0; y < h; y += 7 {
		for x := 0; x < w; x++ {
			if d.alpha[y*w+x] != m.Pix[m.PixOffset(x, y)+3] {
				t.Fatalf("alpha differs at %d,%d", x, y)
			}
		}
	}
	if p := lumaPSNR(d, srcLuma(m)); p < 33 {
		t.Errorf("luma PSNR %.2f dB", p)
	}
}

// synth is a large procedural image that exists one strip at a time.
type synth struct {
	w, h, y int
	env     *pipeline.Env
	strip   *image.NRGBA
}

func synthPixel(x, y int) color.NRGBA {
	n := uint8((x*7919 ^ y*104729) >> 3 & 15) // fine texture
	a := 255
	if d := (x-3000)*(x-3000) + (y-2000)*(y-2000); d > 1900*1900 {
		a = max(0, 255-(d-1900*1900)/20000)
	}
	return color.NRGBA{uint8(x/24) + n, uint8(y/16) + n, uint8((x+y)/40) + n, uint8(a)}
}

func (s *synth) Bounds() image.Rectangle { return image.Rect(0, 0, s.w, s.h) }
func (s *synth) ColorModel() color.Model { return color.NRGBAModel }
func (s *synth) Env() *pipeline.Env      { return s.env }
func (s *synth) Close() error            { return nil }
func (s *synth) Next() (image.Image, error) {
	if s.y >= s.h {
		return nil, io.EOF
	}
	r := image.Rect(0, s.y, s.w, min(s.y+24, s.h))
	if s.strip == nil {
		s.strip = image.NewNRGBA(image.Rect(0, 0, s.w, 24))
	}
	m := *s.strip
	m.Rect = r
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := 0; x < s.w; x++ {
			m.SetNRGBA(x, y, synthPixel(x, y))
		}
	}
	s.y = r.Max.Y
	return &m, nil
}

// TestLargeImage encodes 6000x4000 pixels with transparency, with the
// default memory limit and with only 2MB.
func TestLargeImage(t *testing.T) {
	if testing.Short() {
		t.Skip("large image")
	}
	const w, h = 6000, 4000
	dir := t.TempDir()
	var ref []byte
	for _, limit := range []int64{0, 2 << 20} {
		env := pipeline.NewEnv(limit, dir)
		data := mustEncode(t, &synth{w: w, h: h, env: env}, 75, nil)
		t.Logf("limit %d: %d bytes, budget peak %d", env.Budget.Limit(), len(data), env.Budget.Peak())
		if env.Budget.Peak() > env.Budget.Limit() {
			t.Errorf("budget peak %d over the limit %d", env.Budget.Peak(), env.Budget.Limit())
		}
		if limit == 0 && env.Budget.Peak() > 4<<20+3*int64(len(data)) {
			t.Errorf("budget peak %d for a file of %d bytes", env.Budget.Peak(), len(data))
		}
		if env.Budget.Used() != 0 {
			t.Errorf("%d bytes of budget still reserved", env.Budget.Used())
		}
		if n := tempFiles(t, dir); n != 0 {
			t.Errorf("%d temporary files left behind", n)
		}
		if ref == nil {
			ref = data
		} else if !bytes.Equal(ref, data) {
			t.Error("output depends on the memory limit")
		}
	}
	d := decodeX(t, ref)
	if d.w != w || d.h != h || d.alpha == nil {
		t.Fatalf("decoded %dx%d alpha %v", d.w, d.h, d.alpha != nil)
	}
	var sum float64
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := synthPixel(x, y)
			if d.alpha[y*w+x] != c.A {
				t.Fatalf("alpha differs at %d,%d", x, y)
			}
			want := 16 + (65.738*float64(c.R)+129.057*float64(c.G)+25.064*float64(c.B))/256
			e := float64(d.yc.Y[y*d.yc.YStride+x]) - want
			sum += e * e
		}
	}
	if p := 10 * math.Log10(255*255*w*h/sum); p < 36 {
		t.Errorf("luma PSNR %.2f dB", p)
	} else {
		t.Logf("luma PSNR %.2f dB", p)
	}
}

// failing yields some strips and then fails or ends.
type failing struct {
	pipeline.Source
	left int
	err  error
}

func (f *failing) Next() (image.Image, error) {
	if f.left == 0 {
		return nil, f.err
	}
	f.left--
	return f.Source.Next()
}

type closeCounter struct {
	pipeline.Source
	closed int
}

func (c *closeCounter) Close() error { c.closed++; return c.Source.Close() }

func TestErrors(t *testing.T) {
	m := testutil.Images(40, 50)["nrgba"]
	dir := t.TempDir()
	check := func(name string, src pipeline.Source, o *Options, want error) {
		t.Helper()
		env := pipeline.EnvOf(src)
		err := Encode(io.Discard, src, o)
		if err == nil {
			t.Errorf("%s: no error", name)
		} else if want != nil && !errors.Is(err, want) {
			t.Errorf("%s: error %v, want %v", name, err, want)
		}
		if env.Budget.Used() != 0 {
			t.Errorf("%s: budget still reserved", name)
		}
	}
	env := func() *pipeline.Env { return pipeline.NewEnv(0, dir) }
	check("empty", testutil.ChoppedEnv(image.NewNRGBA(image.Rect(0, 0, 0, 0)), 4, env()), nil, nil)
	check("no rows", testutil.ChoppedEnv(image.NewNRGBA(image.Rect(0, 0, 5, 0)), 4, env()), nil, nil)
	check("too wide", testutil.ChoppedEnv(image.NewGray(image.Rect(0, 0, 16384, 1)), 4, env()), nil, nil)
	check("too high", testutil.ChoppedEnv(image.NewGray(image.Rect(0, 0, 1, 16384)), 4, env()), nil, nil)
	check("quality 101", testutil.ChoppedEnv(m, 4, env()), &Options{Quality: 101}, nil)
	check("quality -1", testutil.ChoppedEnv(m, 4, env()), &Options{Quality: -1}, nil)
	boom := errors.New("boom")
	for _, left := range []int{0, 1, 5} {
		check("failing source", &failing{testutil.ChoppedEnv(m, 4, env()), left, boom}, nil, boom)
		check("short source", &failing{testutil.ChoppedEnv(m, 4, env()), left, io.EOF}, nil, io.ErrUnexpectedEOF)
	}
	if n := tempFiles(t, dir); n != 0 {
		t.Errorf("%d temporary files left behind", n)
	}

	// A writer that fails.
	if err := Encode(failWriter{}, testutil.Chopped(m, 4), nil); err == nil {
		t.Error("failing writer: no error")
	}
	// The largest dimensions are accepted (flat images keep this quick).
	for _, sz := range [][2]int{{16383, 20}, {20, 16383}} {
		big := image.NewGray(image.Rect(0, 0, sz[0], sz[1]))
		for i := range big.Pix {
			big.Pix[i] = uint8(i % 251)
		}
		d := decodeX(t, mustEncode(t, pipeline.FromImage(big, nil), 50, nil))
		if d.w != sz[0] || d.h != sz[1] {
			t.Errorf("%v: decoded %dx%d", sz, d.w, d.h)
		}
	}
	// The source is not closed.
	cc := &closeCounter{Source: testutil.Chopped(m, 4)}
	if err := Encode(io.Discard, cc, nil); err != nil || cc.closed != 0 {
		t.Errorf("err %v, source closed %d times", err, cc.closed)
	}
}

type failWriter struct{}

func (failWriter) Write(p []byte) (int, error) { return 0, errors.New("disk full") }

// TestBounds: the image need not start at (0, 0).
func TestBounds(t *testing.T) {
	for _, name := range []string{"nrgba", "ycbcr420", "ycbcr444", "gray", "paletted", "rgba"} {
		big := testutil.Images(90, 70)[name]
		r := image.Rect(7, 5, 60, 44)
		sub := big.(interface {
			SubImage(image.Rectangle) image.Image
		}).SubImage(r)
		data := mustEncode(t, testutil.Chopped(sub, 6), 90, nil)
		d := decodeX(t, data)
		if d.w != r.Dx() || d.h != r.Dy() {
			t.Fatalf("%s: decoded %dx%d", name, d.w, d.h)
		}
		if p := lumaPSNR(d, srcLuma(sub)); p < 32 {
			t.Errorf("%s: luma PSNR %.2f dB", name, p)
		}
	}
}

func TestTransforms(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	var src, ref, out [16]uint8
	var coef [16]int16
	for i := 0; i < 2000; i++ {
		for j := range src {
			src[j], ref[j] = uint8(r.Intn(256)), uint8(r.Intn(256))
		}
		fdct(src[:], 4, ref[:], 4, &coef)
		idctAdd(&coef, ref[:], 4, out[:], 4)
		for j := range src {
			if d := int(src[j]) - int(out[j]); d < -1 || d > 1 {
				t.Fatalf("DCT round trip off by %d", d)
			}
		}
		var dc, y2, back [16]int16
		for j := range dc {
			dc[j] = int16(r.Intn(4081) - 2040)
		}
		fwht(&dc, &y2)
		iwht(&y2, &back)
		for j := range dc {
			if d := int(dc[j]) - int(back[j]); d < -1 || d > 1 {
				t.Fatalf("WHT round trip off by %d", d)
			}
		}
	}
}

// TestBoolEncoder checks the arithmetic coder against a straightforward
// decoder (RFC 6386 section 7.3), including long runs that carry.
func TestBoolEncoder(t *testing.T) {
	r := rand.New(rand.NewSource(5))
	for iter := 0; iter < 300; iter++ {
		n := 1 + r.Intn(3000)
		bits := make([]bool, n)
		probs := make([]uint8, n)
		mode := iter % 4
		for i := range bits {
			switch mode {
			case 0:
				probs[i] = uint8(1 + r.Intn(255))
				bits[i] = r.Intn(256) >= int(probs[i])
			case 1: // improbable bits: maximal output, many carries
				probs[i] = uint8(250 + r.Intn(6))
				bits[i] = r.Intn(4) != 0
			case 2:
				probs[i] = 1
				bits[i] = true
			default:
				probs[i] = 128
				bits[i] = r.Intn(2) == 0
			}
		}
		var buf bytes.Buffer
		var bw boolEnc
		bw.init(&buf, make([]byte, 7))
		for i := range bits {
			bw.putBit(bits[i], probs[i])
		}
		size, err := bw.finish()
		if err != nil || size != int64(buf.Len()) {
			t.Fatalf("finish: %d %v, wrote %d", size, err, buf.Len())
		}
		data := buf.Bytes()
		// Reference decoder.
		pos := 0
		next := func() uint32 {
			if pos < len(data) {
				pos++
				return uint32(data[pos-1])
			}
			pos++
			return 0
		}
		value := next()<<8 | next()
		rng, bitCount := uint32(255), 0
		for i := range bits {
			split := 1 + ((rng-1)*uint32(probs[i]))>>8
			big := split << 8
			var bit bool
			if value >= big {
				bit = true
				rng -= split
				value -= big
			} else {
				rng = split
			}
			for rng < 128 {
				value <<= 1
				rng <<= 1
				bitCount++
				if bitCount == 8 {
					bitCount = 0
					value |= next()
				}
			}
			if bit != bits[i] {
				t.Fatalf("iteration %d: bit %d of %d decodes wrong", iter, i, n)
			}
		}
		if pos > len(data) {
			t.Fatalf("iteration %d: decoder read %d bytes past the %d written", iter, pos-len(data), len(data))
		}
	}
}

// TestThreads: the file is the same whatever the number of helpers coding
// rows and partitions at once, and with a budget too small for them to
// keep their partitions apart; nothing is left reserved or on disk.
func TestThreads(t *testing.T) {
	r := rand.New(rand.NewSource(9))
	imgs := []image.Image{photoLike(1000, 700), photoLike(37, 900), photoLike(900, 23)}
	noisy := image.NewNRGBA(image.Rect(0, 0, 640, 480))
	for i := range noisy.Pix {
		noisy.Pix[i] = uint8(r.Intn(256))
	}
	imgs = append(imgs, noisy, testutil.Images(333, 211)["nrgba"])
	for n, m := range imgs {
		for _, q := range []int{20, 75, 100} {
			ref := mustEncode(t, testutil.Chopped(m, 16), q, nil)
			for _, c := range []struct {
				helpers int
				limit   int64
			}{{1, 0}, {3, 0}, {7, 0}, {7, 3 << 20}} {
				dir := t.TempDir()
				env := pipeline.NewEnv(c.limit, dir)
				env.SetHelpers(c.helpers)
				got := mustEncode(t, testutil.ChoppedEnv(m, 13, env), q, nil)
				if !bytes.Equal(got, ref) {
					t.Errorf("image %d q%d with %d helpers, limit %d: another file", n, q, c.helpers, c.limit)
				}
				if env.Budget.Used() != 0 || tempFiles(t, dir) != 0 {
					t.Errorf("image %d q%d with %d helpers: %d bytes reserved, %d temporary files", n, q, c.helpers, env.Budget.Used(), tempFiles(t, dir))
				}
			}
		}
	}
}
