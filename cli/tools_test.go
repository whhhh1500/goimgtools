package cli

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/inspect"
	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// animation writes a GIF of n small frames, each shown for delay
// hundredths of a second, and returns its path.
func animation(t *testing.T, dir string, n, delay int) string {
	t.Helper()
	pal := color.Palette{color.Black, color.White}
	g := &gif.GIF{LoopCount: 0}
	for i := 0; i < n; i++ {
		m := image.NewPaletted(image.Rect(0, 0, 24, 16), pal)
		m.SetColorIndex(i%24, i%16, 1)
		g.Image = append(g.Image, m)
		g.Delay = append(g.Delay, delay)
	}
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, g); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "anim.gif")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestInfo: one line per file with what the file is; JSON on request.
func TestInfo(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.png"), filepath.Join(dir, "b.png")
	writePNG(t, a, testutil.Images(61, 45)["nrgba"])
	writePNG(t, b, testutil.Images(30, 20)["gray"])
	anim := animation(t, dir, 5, 20)
	big := filepath.Join(dir, "big.jpg")
	if err := goimgtools.FromImage(testutil.Images(800, 500)["rgba-opaque"]).JPEG(goimgtools.JPEGOptions{Quality: 80}).ToFile(big); err != nil {
		t.Fatal(err)
	}
	out, _, err := cli("info", a, b, anim, big)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"a.png: png 61x45 alpha, rgb 8-bit, ",
		"b.png: png 30x20, gray 8-bit, ",
		"anim.gif: gif 24x16, palette 8-bit, ",
		", 5 frames in 1.00s, plays for ever\n",
		"big.jpg: jpeg 800x500, ycbcr 8-bit, 0.4 MP, ",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("info output lacks %q:\n%s", want, out)
		}
	}
	if n := strings.Count(out, "\n"); n != 4 {
		t.Errorf("info printed %d lines for 4 files:\n%s", n, out)
	}

	// A file that cannot be read gets its line too, and the command fails.
	out, _, err = cli("info", a, filepath.Join(dir, "missing.png"))
	if err == nil || !strings.Contains(out, "a.png: png 61x45") || !strings.Contains(out, "missing.png: ") {
		t.Errorf("info with a missing file: err %v, output:\n%s", err, out)
	}

	// As JSON: one object to a line.
	out, _, err = cli("info", "-json", a, anim, filepath.Join(dir, "missing.png"))
	if err == nil {
		t.Error("info -json with a missing file succeeded")
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 {
		t.Fatalf("info -json printed %d lines:\n%s", len(lines), out)
	}
	var got []map[string]any
	for _, line := range lines {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("not JSON: %q: %v", line, err)
		}
		got = append(got, m)
	}
	if m := got[0]; m["format"] != "png" || m["width"] != 61.0 || m["height"] != 45.0 || m["alpha"] != true ||
		m["color"] != "rgb" || m["bits"] != 8.0 || m["frames"] != 1.0 || m["bytes"].(float64) <= 0 || m["error"] != nil {
		t.Errorf("JSON of the PNG: %v", m)
	}
	if m := got[1]; m["format"] != "gif" || m["frames"] != 5.0 || m["duration_ms"] != 1000.0 || m["loop_count"] != 0.0 {
		t.Errorf("JSON of the animation: %v", m)
	}
	if m := got[2]; m["error"] == nil || m["format"] != nil {
		t.Errorf("JSON of the missing file: %v", m)
	}
	if _, _, err := cli("info"); err == nil {
		t.Error("info without files accepted")
	}
}

// TestCheck: a whole file is ok; a damaged one is named and fails the
// command, though its header reads.
func TestCheck(t *testing.T) {
	dir := t.TempDir()
	good, bad, text := filepath.Join(dir, "good.png"), filepath.Join(dir, "bad.png"), filepath.Join(dir, "notes.txt")
	writePNG(t, good, testutil.Images(120, 90)["nrgba"])
	data, _ := os.ReadFile(good)
	os.WriteFile(bad, data[:len(data)*2/3], 0o644)
	os.WriteFile(text, []byte("not an image"), 0o644)

	out, _, err := cli("check", good)
	if err != nil || out != good+": ok\n" {
		t.Errorf("check of a good file: %v\n%s", err, out)
	}
	// info is content with the damaged file; check is not.
	if out, _, err := cli("info", bad); err != nil || !strings.Contains(out, "png 120x90") {
		t.Errorf("info of the damaged file: %v\n%s", err, out)
	}
	out, _, err = cli("check", good, bad, text)
	if err == nil || !strings.Contains(err.Error(), "2 of 3") {
		t.Errorf("check with two bad files: %v", err)
	}
	if !strings.Contains(out, "good.png: ok\n") || !strings.Contains(out, "bad.png: goimgtools: not a readable image") || !strings.Contains(out, "notes.txt: goimgtools: not a readable image") {
		t.Errorf("check output:\n%s", out)
	}
	out, _, _ = cli("check", "-quiet", good, bad)
	if strings.Contains(out, "good.png") || !strings.Contains(out, "bad.png") {
		t.Errorf("check -quiet output:\n%s", out)
	}
	if _, _, err := cli("check"); err == nil {
		t.Error("check without files accepted")
	}
}

// TestCompare: identical across formats, a figure for a lossy copy, sizes
// that differ, and the threshold.
func TestCompare(t *testing.T) {
	dir := t.TempDir()
	src := testutil.Images(200, 150)["rgba-opaque"]
	a, same, lossy, small := filepath.Join(dir, "a.png"), filepath.Join(dir, "a.bmp"), filepath.Join(dir, "a.jpg"), filepath.Join(dir, "small.png")
	writePNG(t, a, src)
	for path, m := range map[string]*goimgtools.Image{
		same:  goimgtools.FromImage(src),
		lossy: goimgtools.FromImage(src).JPEG(goimgtools.JPEGOptions{Quality: 40}),
		small: goimgtools.FromImage(src).Resize(100, 0),
	} {
		if err := m.ToFile(path); err != nil {
			t.Fatal(err)
		}
	}
	if out, _, err := cli("compare", a, same); err != nil || out != "identical\n" {
		t.Errorf("png against bmp: %v\n%s", err, out)
	}
	out, _, err := cli("compare", a, lossy)
	if err != nil || !strings.HasPrefix(out, "PSNR ") || !strings.Contains(out, " dB, largest difference ") {
		t.Errorf("png against jpeg: %v\n%s", err, out)
	}
	if out, _, err := cli("compare", a, small); err != nil || out != "different sizes: 200x150 and 100x75\n" {
		t.Errorf("different sizes: %v\n%s", err, out)
	}
	// The threshold: met by the identical copy, not by the lossy one, nor
	// by another size.
	if _, _, err := cli("compare", "-min-psnr", "60", a, same); err != nil {
		t.Errorf("-min-psnr with identical images: %v", err)
	}
	if _, _, err := cli("compare", "-min-psnr", "60", a, lossy); err == nil {
		t.Error("-min-psnr 60 accepted a JPEG of quality 40")
	}
	if _, _, err := cli("compare", "-min-psnr", "5", a, lossy); err != nil {
		t.Errorf("-min-psnr 5: %v", err)
	}
	if _, _, err := cli("compare", "-min-psnr", "5", a, small); err == nil {
		t.Error("-min-psnr accepted images of different sizes")
	}
	for _, args := range [][]string{{"compare"}, {"compare", a}, {"compare", a, same, lossy}, {"compare", a, filepath.Join(dir, "none.png")}} {
		if _, _, err := cli(args...); err == nil {
			t.Errorf("%v accepted", args)
		}
	}
}

// TestFormats: the list of formats.
func TestFormats(t *testing.T) {
	out, _, err := cli("formats")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 11 {
		t.Fatalf("formats printed %d lines:\n%s", len(lines), out)
	}
	if !strings.Contains(out, "psd    .psd .psb      read\n") {
		t.Errorf("no line for psd, read only:\n%s", out)
	}
	for _, name := range []string{"jpeg", "png", "webp", "gif", "bmp", "tiff", "ico", "qoi", "pnm", "tga"} {
		found := false
		for _, l := range lines {
			f := strings.Fields(l)
			if f[0] == name && strings.HasSuffix(l, "read, write") && strings.HasPrefix(f[1], ".") {
				found = true
			}
		}
		if !found {
			t.Errorf("no line for %s:\n%s", name, out)
		}
	}
	if !strings.Contains(out, ".jpg .jpeg") || !strings.Contains(out, ".tif .tiff") {
		t.Errorf("extensions missing:\n%s", out)
	}
	if _, _, err := cli("formats", "x"); err == nil {
		t.Error("formats with an argument accepted")
	}
}

// TestStatsHashExif: the three tools that look into the pixels or the EXIF
// block, each on files it has something to say about.
func TestStatsHashExif(t *testing.T) {
	dir := t.TempDir()
	// A red picture, the same picture smaller and as a JPEG, and another.
	red := image.NewNRGBA(image.Rect(0, 0, 64, 48))
	for y := 0; y < 48; y++ {
		for x := 0; x < 64; x++ {
			c := color.NRGBA{200, 30, 30, 255}
			if (x/16+y/16)%2 == 0 && x < 32 {
				c = color.NRGBA{20, 20, 60, 255}
			}
			red.SetNRGBA(x, y, c)
		}
	}
	a, small, other, notImage := filepath.Join(dir, "a.png"), filepath.Join(dir, "small.jpg"), filepath.Join(dir, "other.png"), filepath.Join(dir, "notes.txt")
	writePNG(t, a, red)
	if err := goimgtools.FromImage(red).Resize(32, 0).JPEG(goimgtools.JPEGOptions{Quality: 90}).ToFile(small); err != nil {
		t.Fatal(err)
	}
	writePNG(t, other, testutil.Images(64, 48)["gray"])
	os.WriteFile(notImage, []byte("not an image"), 0o644)

	// stats
	out, _, err := cli("stats", a, other)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "a.png: average #") || !strings.Contains(out, ", 2 colours; mostly #c81e1e ") || !strings.Contains(out, "other.png: average #") || !strings.Contains(out, ", greyscale,") {
		t.Errorf("stats output:\n%s", out)
	}
	out, _, err = cli("stats", "-json", a, notImage)
	if err == nil {
		t.Error("stats of a text file succeeded")
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	var st map[string]any
	if len(lines) != 2 || json.Unmarshal([]byte(lines[0]), &st) != nil || st["colors"] != 2.0 || st["gray"] != false || st["opaque"] != true ||
		st["width"] != 64.0 || len(st["dominant"].([]any)) != 2 || !strings.Contains(lines[1], `"error"`) {
		t.Errorf("stats -json output:\n%s", out)
	}

	// hash: a real picture (a video frame that comes with Go), the same
	// picture smaller and recompressed, and another picture.
	frame := filepath.Join(runtime.GOROOT(), "src", "image", "testdata", "video-001.png")
	if _, err := os.Stat(frame); err != nil {
		t.Skip("the test images of Go are not here: ", err)
	}
	if err := goimgtools.Open(frame).Resize(75, 0).JPEG(goimgtools.JPEGOptions{Quality: 85}).ToFile(small); err != nil {
		t.Fatal(err)
	}
	out, _, err = cli("hash", "-similar", "8", frame, small, other)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "video-001.png ~ ") || !strings.Contains(out, "small.jpg (") || strings.Contains(out, "other.png (") || strings.Contains(out, "~ "+other) {
		t.Errorf("hash -similar output:\n%s", out)
	}
	first := strings.Fields(strings.Split(out, "\n")[0])
	if len(first) != 2 || len(first[0]) != 16 || first[1] != frame {
		t.Errorf("hash line: %q", strings.Split(out, "\n")[0])
	}
	if out, _, err := cli("hash", "-similar", "0", a, other); err != nil || !strings.Contains(out, "no two files within 0 bits") {
		t.Errorf("hash with nothing alike: %v\n%s", err, out)
	}
	if out, _, err := cli("hash", a, notImage); err == nil || !strings.Contains(out, "notes.txt: ") || !strings.Contains(out, "  "+a) {
		t.Errorf("hash with a text file: %v\n%s", err, out)
	}

	// exif: a file written by another program, one without EXIF.
	withExif := filepath.Join("..", "tests", "testdata", "exif-pillow.jpg")
	out, _, err = cli("exif", withExif, a)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "exif-pillow.jpg: TestCam Corp TestCam X100, ") || !strings.Contains(out, ", ISO 400, ") || !strings.Contains(out, "at 31.230033, 121.474967") || !strings.Contains(out, "a.png: no EXIF\n") {
		t.Errorf("exif output:\n%s", out)
	}
	out, _, err = cli("exif", "-json", withExif, a)
	if err != nil {
		t.Fatal(err)
	}
	lines = strings.Split(strings.TrimSpace(out), "\n")
	var e, none map[string]any
	if len(lines) != 2 || json.Unmarshal([]byte(lines[0]), &e) != nil || json.Unmarshal([]byte(lines[1]), &none) != nil ||
		e["model"] != "TestCam X100" || e["iso"] != 400.0 || e["taken"] != "2026-05-17T09:30:15+08:00" || e["orientation"] != 6.0 || e["exif"] != true ||
		none["exif"] != false || none["model"] != nil {
		t.Errorf("exif -json output:\n%s", out)
	}
	for _, args := range [][]string{{"stats"}, {"hash"}, {"exif"}, {"hash", "-similar", "99", a}, {"exif", notImage}} {
		if _, _, err := cli(args...); err == nil {
			t.Errorf("%v accepted", args)
		}
	}
}

// TestAdjustOptions: the options of the new operations give the file the
// library gives for the same steps.
func TestAdjustOptions(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "in.png")
	writePNG(t, in, testutil.Images(120, 80)["nrgba"])
	cases := []struct {
		args []string
		lib  func(*goimgtools.Image) *goimgtools.Image
	}{
		{[]string{"-rotate", "30", "-background", "transparent"}, func(m *goimgtools.Image) *goimgtools.Image {
			return m.Rotate(30, goimgtools.RotateOptions{Background: color.Transparent})
		}},
		{[]string{"-rotate", "-90"}, func(m *goimgtools.Image) *goimgtools.Image { return m.Rotate(270) }},
		{[]string{"-trim", "10", "-extend", "1,2,3,4", "-background", "#ff000080"}, func(m *goimgtools.Image) *goimgtools.Image {
			return m.Trim(10).Extend(1, 2, 3, 4, goimgtools.ExtendOptions{Background: color.NRGBA{255, 0, 0, 128}})
		}},
		{[]string{"-grayscale", "-modulate", "1.2,0.5,30"}, func(m *goimgtools.Image) *goimgtools.Image { return m.Grayscale().Modulate(1.2, 0.5, 30) }},
		{[]string{"-sharpen", "1.5,libvips,0.5,3,1.5,15,25"}, func(m *goimgtools.Image) *goimgtools.Image {
			return m.Sharpen(goimgtools.SharpenOptions{Sigma: 1.5, Method: goimgtools.MethodLibvips,
				Curve: &goimgtools.SharpenCurve{Flat: 0.5, Jagged: 3, JaggedFrom: 1.5, MaxBrighten: 15, MaxDarken: 25}})
		}},
		{[]string{"-resize", "37x37", "-kernel", "lanczos3", "-resize-method", "pillow", "-reducing-gap", "2", "-no-enlarge"}, func(m *goimgtools.Image) *goimgtools.Image {
			return m.Resize(37, 37, goimgtools.ResizeOptions{Kernel: goimgtools.KernelLanczos3, Method: goimgtools.MethodPillow, ReducingGap: 2, WithoutEnlargement: true})
		}},
		{[]string{"-resize", "37x21", "-fit", "fill", "-kernel", "hamming", "-resize-method", "pillow"}, func(m *goimgtools.Image) *goimgtools.Image {
			return m.Resize(37, 21, goimgtools.ResizeOptions{Fit: goimgtools.FitFill, Kernel: goimgtools.KernelHamming, Method: goimgtools.MethodPillow})
		}},
		{[]string{"-resize", "37x21", "-fit", "fill", "-kernel", "lanczos3", "-resize-method", "libvips"}, func(m *goimgtools.Image) *goimgtools.Image {
			return m.Resize(37, 21, goimgtools.ResizeOptions{Fit: goimgtools.FitFill, Kernel: goimgtools.KernelLanczos3, Method: goimgtools.MethodLibvips})
		}},
		{[]string{"-blur", "3", "-blur-method", "libvips", "-blur-min-amplitude", "0.05"}, func(m *goimgtools.Image) *goimgtools.Image {
			return m.Blur(3, goimgtools.BlurOptions{Method: goimgtools.MethodLibvips, MinAmplitude: 0.05})
		}},
		{[]string{"-grayscale-linear", "-modulate", "1,1.3,0,-8"}, func(m *goimgtools.Image) *goimgtools.Image {
			return m.Grayscale(goimgtools.GrayscaleOptions{Linear: true}).Modulate(1, 1.3, 0, goimgtools.ModulateOptions{Lightness: -8})
		}},
		{[]string{"-blur", "2.5", "-sharpen", "mild"}, func(m *goimgtools.Image) *goimgtools.Image { return m.Blur(2.5).Sharpen() }},
		{[]string{"-sharpen", "2"}, func(m *goimgtools.Image) *goimgtools.Image {
			return m.Sharpen(goimgtools.SharpenOptions{Sigma: 2})
		}},
		{[]string{"-sharpen", "1.5,2,3", "-resize", "60x"}, func(m *goimgtools.Image) *goimgtools.Image {
			return m.Resize(60, 0).Sharpen(goimgtools.SharpenOptions{Sigma: 1.5, Amount: 2, Threshold: 3})
		}},
		{[]string{"-watermark", in, "-watermark-width", "25%", "-gravity", "center", "-offset", "0,0"}, func(m *goimgtools.Image) *goimgtools.Image {
			return m.Composite(goimgtools.Open(in), goimgtools.CompositeOptions{Gravity: goimgtools.Center, WidthRatio: 0.25})
		}},
	}
	for _, c := range cases {
		out := filepath.Join(dir, "out.png")
		if _, _, err := cli(append(c.args, in, out)...); err != nil {
			t.Fatalf("%v: %v", c.args, err)
		}
		got, _ := os.ReadFile(out)
		want, err := c.lib(goimgtools.Open(in)).PNG().ToBytes()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%v: another file than the library's", c.args)
		}
	}
}

// TestOp: -op applies operations by name, built-in or registered by the
// program, in the order given; ops lists them; batch takes -op too.
func TestOp(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "in.png")
	writePNG(t, in, testutil.Images(120, 80)["nrgba"])
	out := filepath.Join(dir, "out.png")
	if _, _, err := cli("-op", "blur:2", "-op", "resize:60,0", in, out); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(out)
	want, err := goimgtools.Open(in).Blur(2).Resize(60, 0).PNG().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Error("-op blur:2 -op resize:60,0: another file than the library's")
	}
	if _, _, err := cli("-op", "no-such:1", in, out); err == nil || !strings.Contains(err.Error(), "goimgtools ops") {
		t.Errorf("unknown -op: %v", err)
	}
	if _, _, err := cli("-op", "blur:x", in, out); err == nil {
		t.Error("-op blur:x accepted")
	}

	// A program that registers an operation of its own has it on its
	// command line.
	err = goimgtools.RegisterOperation(goimgtools.OperationDef{Name: "test-swap", Usage: "", Help: "swap red and blue",
		New: func(args ...string) (goimgtools.Operation, error) {
			return goimgtools.OperationFunc(func(src pipeline.Source) (pipeline.Source, error) {
				return pipeline.Map(src, func(s *image.NRGBA) error {
					for i := 0; i < len(s.Pix); i += 4 {
						s.Pix[i], s.Pix[i+2] = s.Pix[i+2], s.Pix[i]
					}
					return nil
				}), nil
			}), nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	defer goimgtools.UnregisterOperation("test-swap")
	list, _, err := cli("ops")
	if err != nil || !strings.Contains(list, "test-swap") || !strings.Contains(list, "blur:SIGMA") {
		t.Errorf("ops: %v\n%s", err, list)
	}
	if _, _, err := cli("-op", "test-swap", "-op", "test-swap", in, out); err != nil {
		t.Fatal(err)
	}
	twice, _ := os.ReadFile(out)
	plain, _ := goimgtools.Open(in).PNG().ToBytes()
	if !bytes.Equal(twice, plain) {
		t.Error("swapping twice changed the image")
	}
	// Batch.
	src, dst := filepath.Join(dir, "src"), filepath.Join(dir, "dst")
	os.Mkdir(src, 0o755)
	writePNG(t, filepath.Join(src, "a.png"), testutil.Images(40, 30)["nrgba"])
	if _, _, err := cli("batch", "-quiet", "-op", "flip", src, dst); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dst, "a.png"))
	w, _ := goimgtools.Open(filepath.Join(src, "a.png")).Flip().PNG().ToBytes()
	if !bytes.Equal(b, w) {
		t.Error("batch -op flip: another file than the library's")
	}
}

func TestPlaceholderCommand(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.png")
	writePNG(t, a, testutil.Images(120, 80)["rgba-opaque"])
	out, _, err := cli("placeholder", "-components", "3x2", a)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := inspect.BlurHash(goimgtools.Open(a), 3, 2)
	th, _ := inspect.ThumbHash(goimgtools.Open(a))
	if want := b + "  " + base64.StdEncoding.EncodeToString(th) + "  " + a + "\n"; out != want {
		t.Errorf("printed %q, want %q", out, want)
	}
	for _, args := range [][]string{{"placeholder"}, {"placeholder", "-components", "10x1", a}, {"placeholder", filepath.Join(dir, "none.png")}} {
		if _, _, err := cli(args...); err == nil {
			t.Errorf("%v: no error", args)
		}
	}
}

func TestCompareSSIM(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.png"), filepath.Join(dir, "b.png")
	writePNG(t, a, testutil.Images(60, 40)["rgba-opaque"])
	writePNG(t, b, testutil.Images(60, 40)["gray"])
	out, _, err := cli("compare", "-ssim", a, b)
	s, _ := inspect.SSIM(goimgtools.Open(a), goimgtools.Open(b))
	if err != nil || !strings.Contains(out, fmt.Sprintf("SSIM %.5f\n", s)) {
		t.Errorf("printed %q, %v; SSIM %.5f", out, err, s)
	}
}
