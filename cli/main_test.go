package cli

import (
	"archive/zip"
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/internal/testutil"
)

func writePNG(t *testing.T, path string, m image.Image) {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, m); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func cli(args ...string) (string, string, error) {
	var out, errOut bytes.Buffer
	err := run(args, &out, &errOut)
	return out.String(), errOut.String(), err
}

// TestConvert runs the full set of options and compares the file with what
// the library writes for the same chain.
func TestConvert(t *testing.T) {
	dir := t.TempDir()
	in, logo, out := filepath.Join(dir, "in.png"), filepath.Join(dir, "logo.png"), filepath.Join(dir, "out.jpg")
	writePNG(t, in, testutil.Images(613, 457)["nrgba"])
	writePNG(t, logo, testutil.Images(90, 40)["nrgba"])

	_, _, err := cli("-crop", "10,20,500,400", "-resize", "200x200", "-no-enlarge", "-flatten", "#fff",
		"-watermark", logo, "-watermark-width", "45", "-gravity", "southeast", "-offset", "-7,-5",
		"-quality", "85", "-mem", "32", "-tmp", dir, in, out)
	if err != nil {
		t.Fatal(err)
	}
	want, err := goimgtools.Open(in).Crop(10, 20, 500, 400).
		Resize(200, 200, goimgtools.ResizeOptions{Fit: goimgtools.FitInside, WithoutEnlargement: true}).
		Flatten(color.White).
		Composite(goimgtools.Open(logo).Resize(45, 0), goimgtools.CompositeOptions{Gravity: goimgtools.SouthEast, Left: -7, Top: -5}).
		JPEG(goimgtools.JPEGOptions{Quality: 85}).ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(out)
	if !bytes.Equal(got, want) {
		t.Error("command output differs from the library chain")
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(got))
	if err != nil || cfg.Width != 200 || cfg.Height != 160 {
		t.Errorf("output is %dx%d (err %v), want 200x160", cfg.Width, cfg.Height, err)
	}

	// Width only, PNG by extension, default watermark margin, -format.
	outPNG := filepath.Join(dir, "out.png")
	if _, _, err := cli("-resize", "100x", "-png-level", "9", "-watermark", logo, in, outPNG); err != nil {
		t.Fatal(err)
	}
	pc, err := png.DecodeConfig(bytes.NewReader(mustRead(t, outPNG)))
	if err != nil || pc.Width != 100 || pc.Height != 75 {
		t.Errorf("out.png is %dx%d (err %v), want 100x75", pc.Width, pc.Height, err)
	}
	odd := filepath.Join(dir, "out.dat")
	if _, _, err := cli("-format", "jpeg", in, odd); err != nil {
		t.Fatal(err)
	}
	if _, err := jpeg.Decode(bytes.NewReader(mustRead(t, odd))); err != nil {
		t.Errorf("-format jpeg did not write a JPEG: %v", err)
	}
	// Tiles: Deep Zoom by the name, the other layouts by -tile-layout.
	if _, _, err := cli("-tile-size", "128", in, filepath.Join(dir, "deep.dzi")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := cli("-tile-layout", "google", "-tile-size", "128", in, filepath.Join(dir, "map.dz")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := cli("-tile-layout", "iiif", in, filepath.Join(dir, "pack.zip")); err != nil {
		t.Fatal(err)
	}
	if z, err := zip.OpenReader(filepath.Join(dir, "pack.zip")); err != nil || z.File[len(z.File)-1].Name != "pack/info.json" {
		t.Errorf("pack.zip: %v", err)
	} else {
		z.Close()
	}
	for _, name := range []string{"deep.dzi", "deep_files/10/4_3.jpeg", "map/blank.png", "map/3/3/4.jpg"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("tiles: %v", err)
		}
	}
	if _, _, err := cli("-tile-layout", "mosaic", in, filepath.Join(dir, "x.dz")); err == nil || !strings.Contains(err.Error(), "-tile-layout") {
		t.Errorf("-tile-layout mosaic: %v", err)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestBadArguments(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "in.png")
	writePNG(t, in, testutil.Images(20, 20)["gray"])
	out := filepath.Join(dir, "out.jpg")
	for _, args := range [][]string{
		{},
		{in},
		{"-resize", "abc", in, out},
		{"-resize", "x", in, out},
		{"-resize", "0x10", in, out},
		{"-fit", "squash", "-resize", "10x10", in, out},
		{"-crop", "1,2,3", in, out},
		{"-crop", "0,0,50,50", in, out},
		{"-flatten", "pink", in, out},
		{"-watermark", in, "-gravity", "up", in, out},
		{"-watermark", in, "-offset", "1", in, out},
		{"-watermark", filepath.Join(dir, "nope.png"), in, out},
		{"-format", "heic", in, out},
		{"-max-size", "big", in, out},
		{"-lossless", in, out},
		{"-progressive", in, out},
		{"-max-size", "1B", in, out},
		{"-rotate", "abc", in, out},
		{"-rotate", "NaN", in, out},
		{"-trim", "300", in, out},
		{"-trim", "x", in, out},
		{"-extend", "1,2", in, out},
		{"-extend", "1,2,3,-4", in, out},
		{"-modulate", "1,2", in, out},
		{"-modulate", "-1,1,0", in, out},
		{"-blur", "-1", in, out},
		{"-watermark", in, "-watermark-width", "0%", in, out},
		{"-watermark", in, "-watermark-width", "abc", in, out},
		{"-watermark", in, "-watermark-width", "-5", in, out},
		{"-watermark", in, "-watermark-width", "5000%", in, out},
		{"-blur", "2000", in, out},
		{"-sharpen", "0", in, out},
		{"-sharpen", "1,2,3,4", in, out},
		{"-sharpen", "1,1.5,2.5", in, out},
		{"-sharpen", "strong", in, out},
		{"-rotate", "30", "-background", "pink", in, out},
		{"-kernel", "sinc", "-resize", "10x10", in, out},
		{"-fit", "contain", "-background", "pink", "-resize", "10x10", in, out},
		{"-tiff-compression", "zip", "-format", "tiff", in, out},
		{"-watermark", in, "-watermark-text", "x", in, out},
		{"-watermark", in, "-watermark-anchor", "middle", in, out},
		{"-watermark", in, "-watermark-ratio", "0.5", in, out},
		{"-watermark", in, "-watermark-opacity", "3", in, out},
		{"-watermark-text", "x", "-font", "no such font at all", in, out},
		{"-text", "hello", in, out},
		{"-text", "hello", "-text-color", "pink", out},
		{"fonts", "-everything"},
		{"-mem", "0", in, out},
		{"-quality", "x", in, out},
		{"info"},
		{in, filepath.Join(dir, "out.xyz")},
	} {
		if _, _, err := cli(args...); err == nil {
			t.Errorf("%v: no error", args)
		}
		if _, err := os.Stat(out); err == nil {
			t.Fatalf("%v: left an output file", args)
		}
	}
	if _, usage, err := cli("-h"); err != nil || !strings.Contains(usage, "-resize") {
		t.Errorf("-h: err %v, usage %q", err, usage)
	}
}

// TestNewOptions runs the options added after the first version and
// compares each file with what the library writes for the same chain.
func TestNewOptions(t *testing.T) {
	dir := t.TempDir()
	in, logo := filepath.Join(dir, "in.png"), filepath.Join(dir, "logo.png")
	writePNG(t, in, testutil.Images(200, 120)["rgba-opaque"])
	writePNG(t, logo, testutil.Images(30, 20)["nrgba"])
	same := func(what, out string, want *goimgtools.Image) {
		t.Helper()
		got, err := os.ReadFile(out)
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		ref, err := want.ToBytes()
		if err != nil {
			t.Fatalf("%s: reference: %v", what, err)
		}
		if !bytes.Equal(got, ref) {
			t.Errorf("%s: the file differs from the library's output", what)
		}
	}
	must := func(args ...string) {
		t.Helper()
		if _, _, err := cli(args...); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	white := color.RGBA{255, 255, 255, 255}

	out := filepath.Join(dir, "a.png")
	must("-rotate", "90", "-flop", "-resize", "100x100", "-fit", "contain", "-background", "white", "-kernel", "lanczos3", in, out)
	same("rotate, flop, contain", out, goimgtools.Open(in).Rotate(90).Flop().
		Resize(100, 100, goimgtools.ResizeOptions{Fit: goimgtools.FitContain, Background: white, Kernel: goimgtools.KernelLanczos3}).PNG())

	out = filepath.Join(dir, "b.png")
	must("-watermark", logo, "-gravity", "northwest", "-watermark-ratio", "0.5,0.5", "-watermark-anchor", "center",
		"-watermark-angle", "30", "-watermark-pivot", "5,5", "-watermark-opacity", "0.5", "-watermark-tile", "-watermark-gap", "10,6", in, out)
	same("watermark placement", out, goimgtools.Open(in).Composite(goimgtools.Open(logo), goimgtools.CompositeOptions{
		LeftRatio: 0.5, TopRatio: 0.5, Anchor: goimgtools.Center, Angle: 30,
		Pivot: goimgtools.CustomPosition, PivotX: 5, PivotY: 5, Opacity: 0.5, Tile: true, TileGapX: 10, TileGapY: 6}).PNG())

	// Text: the built-in font by name, so the test does not depend on the system.
	text := goimgtools.TextOptions{Fonts: []string{"Go Regular"}, Size: 20, Color: white,
		StrokeWidth: 2, StrokeColor: color.RGBA{0, 0, 0, 255},
		ShadowColor: color.NRGBA{0x10, 0x20, 0x30, 0x80}, ShadowX: 3, ShadowY: 1, ShadowBlur: 2}
	textArgs := []string{"-font", "Go Regular", "-font-size", "20", "-text-color", "white", "-stroke-width", "2",
		"-shadow-color", "#10203080", "-shadow-offset", "3,1", "-shadow-blur", "2"}
	out = filepath.Join(dir, "c.png")
	must(append(textArgs, "-watermark-text", `two\nlines`, "-gravity", "center", in, out)...)
	same("text watermark", out, goimgtools.Open(in).Composite(goimgtools.Text("two\nlines", text),
		goimgtools.CompositeOptions{Gravity: goimgtools.Center}).PNG())
	out = filepath.Join(dir, "d.png")
	must(append(textArgs, "-text", "Hello", "-text-background", "#000", "-text-width", "150", out)...)
	text.Background, text.Width = color.RGBA{0, 0, 0, 255}, 150
	same("text image", out, goimgtools.Text("Hello", text).PNG())

	// Every output format, by extension and by -format.
	for ext, want := range map[string]func(*goimgtools.Image) *goimgtools.Image{
		"webp": func(m *goimgtools.Image) *goimgtools.Image { return m.WebP(goimgtools.WebPOptions{Quality: 60}) },
		"gif":  func(m *goimgtools.Image) *goimgtools.Image { return m.GIF(goimgtools.GIFOptions{Colors: 16}) },
		"bmp":  func(m *goimgtools.Image) *goimgtools.Image { return m.BMP() },
		"tif": func(m *goimgtools.Image) *goimgtools.Image {
			return m.TIFF(goimgtools.TIFFOptions{Compression: goimgtools.TIFFDeflate})
		},
	} {
		out = filepath.Join(dir, "e."+ext)
		must("-quality", "60", "-gif-colors", "16", "-tiff-compression", "deflate", in, out)
		same(ext, out, want(goimgtools.Open(in)))
		info, err := goimgtools.Open(out).Metadata()
		if err != nil || info.Width != 200 {
			t.Errorf("%s: reading it back: %+v, %v", ext, info, err)
		}
	}
	out = filepath.Join(dir, "p.jpg")
	must("-quality", "70", "-jpeg-progressive", in, out)
	same("progressive jpeg", out, goimgtools.Open(in).JPEG(goimgtools.JPEGOptions{Quality: 70, Progressive: true}))
	must("-quality", "70", "-jpeg-optimize", in, out)
	same("optimized jpeg", out, goimgtools.Open(in).JPEG(goimgtools.JPEGOptions{Quality: 70, Optimize: true}))
	out = filepath.Join(dir, "c16.png")
	must("-png-colors", "16", "-no-dither", in, out)
	same("png colours", out, goimgtools.Open(in).PNG(goimgtools.PNGOptions{Colors: 16, NoDither: true}))
	// The command line and the library fit a box the same way by default.
	out = filepath.Join(dir, "fit.png")
	must("-resize", "100x100", in, out)
	same("default fit", out, goimgtools.Open(in).Resize(100, 100).PNG())
	out = filepath.Join(dir, "e.webp")
	must("-webp-lossless", "-webp-effort", "2", in, out)
	same("lossless webp effort 2", out, goimgtools.Open(in).WebP(goimgtools.WebPOptions{Lossless: true, Effort: 2}))
	out = filepath.Join(dir, "f.bin")
	must("-format", "webp", "-webp-lossless", in, out)
	same("lossless webp", out, goimgtools.Open(in).WebP(goimgtools.WebPOptions{Lossless: true}))
	back, err := goimgtools.Open(out).PNG().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	ref, _ := goimgtools.Open(in).PNG().ToBytes()
	if !bytes.Equal(back, ref) {
		t.Error("lossless WebP did not keep the pixels")
	}

	// The font list always ends with the built-in font.
	list, _, err := cli("fonts")
	if err != nil || !strings.Contains(list, "Go Regular") || !strings.Contains(list, "(built in)") {
		t.Errorf("fonts: %v\n%s", err, list)
	}
	if _, _, err := cli("fonts", "-all"); err != nil {
		t.Errorf("fonts -all: %v", err)
	}
}

// TestMaxSize: -max-size gives a file within the size, equal to the
// library's.
func TestMaxSize(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "in.png")
	writePNG(t, in, testutil.Images(400, 300)["rgba-opaque"])
	for arg, limit := range map[string]int64{"20KB": 20 << 10, "0.03MB": 31457, "9000": 9000, "12k": 12 << 10} {
		for _, ext := range []string{"jpg", "webp"} {
			out := filepath.Join(dir, "o."+ext)
			if _, _, err := cli("-max-size", arg, in, out); err != nil {
				t.Fatalf("-max-size %s %s: %v", arg, ext, err)
			}
			got, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			if int64(len(got)) > limit {
				t.Errorf("-max-size %s %s: file is %d bytes", arg, ext, len(got))
			}
			want, err := goimgtools.Open(in).MaxFileSize(limit).ToFile(filepath.Join(dir, "w."+ext)), error(nil)
			if want != nil || err != nil {
				t.Fatal(want, err)
			}
			ref, _ := os.ReadFile(filepath.Join(dir, "w."+ext))
			if !bytes.Equal(got, ref) {
				t.Errorf("-max-size %s %s: differs from the library", arg, ext)
			}
		}
	}
}

// TestBatch processes a directory tree and checks which files are written,
// that each equals what the single-file mode writes, and how existing
// outputs, failures and name clashes are handled.
func TestBatch(t *testing.T) {
	dir := t.TempDir()
	in, out := filepath.Join(dir, "in"), filepath.Join(dir, "out")
	imgs := testutil.Images(120, 80)
	if err := os.MkdirAll(filepath.Join(in, "sub", "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := []string{"a.png", "b.png", "c.png", "d.png", filepath.Join("sub", "e.png"), filepath.Join("sub", "deep", "f.png")}
	for i, f := range files {
		writePNG(t, filepath.Join(in, f), imgs[[]string{"nrgba", "gray", "rgba-opaque", "paletted", "nrgba64", "gray16"}[i]])
	}
	os.WriteFile(filepath.Join(in, "notes.txt"), []byte("not an image"), 0o644)
	exists := func(p string) bool { _, err := os.Stat(p); return err == nil }
	count := func(d string) int {
		n := 0
		filepath.WalkDir(d, func(p string, e os.DirEntry, err error) error {
			if err == nil && !e.IsDir() {
				n++
			}
			return nil
		})
		return n
	}

	// The top directory only, same format.
	stdout, _, err := cli("batch", "-resize", "40x40", in, out)
	if err != nil {
		t.Fatalf("batch: %v\n%s", err, stdout)
	}
	if !strings.Contains(stdout, "4 processed") || !strings.Contains(stdout, "1 other files ignored") {
		t.Errorf("summary:\n%s", stdout)
	}
	if count(out) != 4 || exists(filepath.Join(out, "sub")) || exists(filepath.Join(out, "notes.txt")) {
		t.Errorf("%d files written; want the 4 of the top directory", count(out))
	}
	for _, f := range files[:4] {
		single := filepath.Join(dir, "single.png")
		if _, _, err := cli("-resize", "40x40", filepath.Join(in, f), single); err != nil {
			t.Fatal(err)
		}
		a, _ := os.ReadFile(single)
		b, err := os.ReadFile(filepath.Join(out, f))
		if err != nil || !bytes.Equal(a, b) {
			t.Errorf("%s: differs from the single-file result (%v)", f, err)
		}
	}
	os.Remove(filepath.Join(dir, "single.png"))

	// Again: everything exists, nothing is redone. With -overwrite it is.
	stdout, _, err = cli("batch", "-resize", "40x40", in, out)
	if err != nil || !strings.Contains(stdout, "0 processed, 4 skipped") {
		t.Errorf("second run: %v\n%s", err, stdout)
	}
	stdout, _, err = cli("batch", "-quiet", "-overwrite", "-resize", "20x20", in, out)
	if err != nil || !strings.Contains(stdout, "4 processed") || strings.Contains(stdout, "a.png ->") {
		t.Errorf("-overwrite -quiet: %v\n%s", err, stdout)
	}
	if info, err := goimgtools.Open(filepath.Join(out, "a.png")).Metadata(); err != nil || info.Width != 20 {
		t.Errorf("after -overwrite: %+v, %v", info, err)
	}

	// The whole tree, another format, every way of setting -jobs.
	for _, jobs := range []string{"1", "3", "0", "50"} {
		out2 := filepath.Join(dir, "out-"+jobs)
		stdout, _, err = cli("batch", "-jobs", jobs, "-recursive", "-format", "webp", "-quality", "60", in, out2)
		if err != nil || !strings.Contains(stdout, "6 processed") {
			t.Fatalf("-jobs %s: %v\n%s", jobs, err, stdout)
		}
		for _, f := range files {
			p := filepath.Join(out2, strings.TrimSuffix(f, ".png")+".webp")
			if info, err := goimgtools.Open(p).Metadata(); err != nil || info.Format != "webp" || info.Width != 120 {
				t.Errorf("-jobs %s: %s: %+v, %v", jobs, p, info, err)
			}
		}
		if count(out2) != 6 {
			t.Errorf("-jobs %s: %d files written", jobs, count(out2))
		}
	}

	// A broken image and two inputs that would become the same file: both
	// are reported, the rest is done, and the run counts as failed.
	bad := filepath.Join(dir, "bad")
	os.MkdirAll(bad, 0o755)
	writePNG(t, filepath.Join(bad, "ok.png"), imgs["gray"])
	writePNG(t, filepath.Join(bad, "twin.png"), imgs["gray"])
	var twin bytes.Buffer
	if err := gif.Encode(&twin, imgs["paletted"], nil); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(bad, "twin.gif"), twin.Bytes(), 0o644) // twin.gif and twin.png both want twin.jpg
	os.WriteFile(filepath.Join(bad, "broken.jpg"), []byte("\xff\xd8 not a jpeg"), 0o644)
	badOut := filepath.Join(dir, "bad-out")
	stdout, stderr, err := cli("batch", "-format", "jpeg", bad, badOut)
	if err == nil || !strings.Contains(err.Error(), "2 of 4 files failed") {
		t.Errorf("failures: err = %v\n%s", err, stdout)
	}
	if !strings.Contains(stderr, "FAILED broken.jpg") || !strings.Contains(stderr, "FAILED twin.png") ||
		!strings.Contains(stdout, "2 processed, 2 failed") {
		t.Errorf("failure report:\nstdout: %s\nstderr: %s", stdout, stderr)
	}
	if !exists(filepath.Join(badOut, "ok.jpg")) || !exists(filepath.Join(badOut, "twin.jpg")) || count(badOut) != 2 {
		t.Errorf("after failures: %d files in the output", count(badOut))
	}

	// In place: nothing is touched without -overwrite; with it the files
	// are replaced, and nothing else is left in the directory.
	place := filepath.Join(dir, "place")
	os.MkdirAll(place, 0o755)
	writePNG(t, filepath.Join(place, "x.png"), imgs["rgba-opaque"])
	before, _ := os.ReadFile(filepath.Join(place, "x.png"))
	stdout, _, err = cli("batch", "-resize", "30x30", place, place)
	after, _ := os.ReadFile(filepath.Join(place, "x.png"))
	if err != nil || !bytes.Equal(before, after) || !strings.Contains(stdout, "1 skipped") {
		t.Errorf("in place without -overwrite: %v\n%s", err, stdout)
	}
	if _, _, err = cli("batch", "-overwrite", "-resize", "30x30", place, place); err != nil {
		t.Fatal(err)
	}
	if info, err := goimgtools.Open(filepath.Join(place, "x.png")).Metadata(); err != nil || info.Width != 30 || count(place) != 1 {
		t.Errorf("in place with -overwrite: %+v, %v, %d files", info, err, count(place))
	}

	// The output directory inside the input directory is not read back in.
	nested := filepath.Join(in, "out")
	if _, _, err = cli("batch", "-recursive", in, nested); err != nil {
		t.Fatal(err)
	}
	stdout, _, err = cli("batch", "-recursive", "-overwrite", in, nested)
	if err != nil || !strings.Contains(stdout, "6 processed") || exists(filepath.Join(nested, "out")) {
		t.Errorf("output inside input: %v\n%s", err, stdout)
	}

	// What cannot work is refused before anything is written.
	empty := filepath.Join(dir, "empty")
	os.MkdirAll(empty, 0o755)
	none := filepath.Join(dir, "none")
	for _, args := range [][]string{
		{"batch"},
		{"batch", in},
		{"batch", filepath.Join(dir, "missing"), none},
		{"batch", filepath.Join(in, "a.png"), none},
		{"batch", empty, none},
		{"batch", "-jobs", "-1", in, none},
		{"batch", "-format", "heic", in, none},
		{"batch", "-resize", "abc", in, none},
		{"batch", "-text", "hello", in, none},
	} {
		if _, _, err := cli(args...); err == nil {
			t.Errorf("%v: no error", args)
		}
		if exists(none) {
			t.Fatalf("%v: created the output directory", args)
		}
	}
	if _, usage, err := cli("batch", "-h"); err != nil || !strings.Contains(usage, "-jobs") || !strings.Contains(usage, "-resize") {
		t.Errorf("batch -h: %v\n%s", err, usage)
	}
}

// TestSkipLarger: with -skip-larger a result that is not smaller than the
// input file leaves the input as it is; a smaller one is written.
func TestSkipLarger(t *testing.T) {
	dir := t.TempDir()
	// A smooth gradient compresses so well in full colour that reducing it
	// to a dithered palette makes the file larger; noise does the opposite.
	smooth := image.NewRGBA(image.Rect(0, 0, 400, 300))
	for y := 0; y < 300; y++ {
		for x := 0; x < 400; x++ {
			smooth.SetRGBA(x, y, color.RGBA{uint8(x * 255 / 400), uint8(y * 255 / 300), 128, 255})
		}
	}
	grad, noisy := filepath.Join(dir, "grad.png"), filepath.Join(dir, "noisy.png")
	writePNG(t, grad, smooth)
	writePNG(t, noisy, testutil.Images(400, 300)["rgba-opaque"])
	size := func(p string) int64 {
		st, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		return st.Size()
	}
	read := func(p string) []byte {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	out := filepath.Join(dir, "out.png")

	// Without the option the gradient grows.
	if _, _, err := cli("-png-colors", "256", grad, out); err != nil {
		t.Fatal(err)
	}
	if size(out) <= size(grad) {
		t.Fatalf("the test needs a picture that grows: %d -> %d bytes", size(grad), size(out))
	}
	// With it the input is copied instead, and said so.
	stdout, _, err := cli("-skip-larger", "-png-colors", "256", grad, out)
	if err != nil || !bytes.Equal(read(out), read(grad)) || !strings.Contains(stdout, "kept") {
		t.Errorf("gradient with -skip-larger: err %v, same as the input %v, output %q", err, bytes.Equal(read(out), read(grad)), stdout)
	}
	// A picture that does shrink is written as usual.
	stdout, _, err = cli("-skip-larger", "-png-colors", "256", noisy, out)
	if err != nil || size(out) >= size(noisy) || stdout != "" {
		t.Errorf("noise with -skip-larger: err %v, %d -> %d bytes, output %q", err, size(noisy), size(out), stdout)
	}
	// In place: the gradient is left untouched, the noise replaced; no
	// other file appears.
	for _, f := range []string{grad, noisy} {
		before := read(f)
		if _, _, err := cli("-skip-larger", "-png-colors", "256", f, f); err != nil {
			t.Fatal(err)
		}
		if same := bytes.Equal(read(f), before); same != (f == grad) {
			t.Errorf("%s in place: unchanged = %v", filepath.Base(f), same)
		}
	}
	os.Remove(out)
	if names, _ := filepath.Glob(filepath.Join(dir, "*")); len(names) != 2 {
		t.Errorf("files in the directory: %v", names)
	}
	// A JPEG saved again at a higher quality would grow too.
	lowQ := filepath.Join(dir, "low.jpg")
	if _, _, err := cli("-quality", "30", grad, lowQ); err != nil {
		t.Fatal(err)
	}
	high := filepath.Join(dir, "high.jpeg")
	if _, _, err := cli("-skip-larger", "-quality", "95", lowQ, high); err != nil || !bytes.Equal(read(high), read(lowQ)) {
		t.Errorf("jpeg at a higher quality with -skip-larger: err %v", err)
	}
	// It only compares like with like: another format is always written.
	webp := filepath.Join(dir, "g.webp")
	if _, _, err := cli("-skip-larger", "-webp-lossless", grad, webp); err != nil {
		t.Fatal(err)
	}
	if info, err := goimgtools.Open(webp).Metadata(); err != nil || info.Format != "webp" {
		t.Errorf("another format with -skip-larger: %+v, %v", info, err)
	}
	// In a batch the kept files are counted.
	in2, out2 := filepath.Join(dir, "b-in"), filepath.Join(dir, "b-out")
	os.MkdirAll(in2, 0o755)
	writePNG(t, filepath.Join(in2, "grad.png"), smooth)
	writePNG(t, filepath.Join(in2, "noisy.png"), testutil.Images(400, 300)["rgba-opaque"])
	stdout, _, err = cli("batch", "-skip-larger", "-png-colors", "256", in2, out2)
	if err != nil || !strings.Contains(stdout, "2 processed (1 kept as they were") {
		t.Errorf("batch with -skip-larger: %v\n%s", err, stdout)
	}
	if !bytes.Equal(read(filepath.Join(out2, "grad.png")), read(filepath.Join(in2, "grad.png"))) ||
		size(filepath.Join(out2, "noisy.png")) >= size(filepath.Join(in2, "noisy.png")) {
		t.Error("batch with -skip-larger: wrong files written")
	}
}

// TestAnimatedColours: -animated takes -gif-colors and -no-dither, and the
// file is the library's.
func TestAnimatedColours(t *testing.T) {
	dir := t.TempDir()
	in, out := filepath.Join(dir, "in.gif"), filepath.Join(dir, "out.gif")
	g := &gif.GIF{}
	for k := 0; k < 4; k++ {
		pal := color.Palette{}
		for i := 0; i < 200; i++ {
			pal = append(pal, color.RGBA{uint8(i), uint8(40 * k), uint8(255 - i), 255})
		}
		m := image.NewPaletted(image.Rect(0, 0, 60, 40), pal)
		for i := range m.Pix {
			m.Pix[i] = uint8((i + 7*k) % 200)
		}
		g.Image, g.Delay = append(g.Image, m), append(g.Delay, 3)
	}
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, g); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(in, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := cli("-animated", "-gif-colors", "24", "-no-dither", in, out); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(out)
	want, err := goimgtools.FromBytes(buf.Bytes()).Animated().GIF(goimgtools.GIFOptions{Colors: 24, NoDither: true}).ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Error("command output differs from the library")
	}
	o, err := gif.DecodeAll(bytes.NewReader(got))
	if err != nil {
		t.Fatal(err)
	}
	// The colour table is padded to a power of two; the pixels use at most
	// 24 entries.
	used := map[uint8]bool{}
	for _, f := range o.Image {
		for _, v := range f.Pix {
			used[v] = true
		}
	}
	if len(o.Image) != 4 || len(used) > 24 {
		t.Errorf("%d frames, %d colours", len(o.Image), len(used))
	}
}
