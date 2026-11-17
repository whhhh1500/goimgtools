package tests

import (
	"crypto/sha256"
	"fmt"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	. "github.com/whhhh1500/goimgtools"
)

// tileSums is the sha256 (first 16 hex digits) of every file under dir,
// by its path from dir.
func tileSums(t *testing.T, dir string) map[string]string {
	t.Helper()
	sums := map[string]string{}
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		sums[filepath.ToSlash(rel)] = fmt.Sprintf("%x", sha256.Sum256(b))[:16]
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return sums
}

// TestTiles: ToTiles writes the Deep Zoom pyramid sharp's tile writes
// (libvips' dzsave), every tile and the .dzi file to the byte (64-pixel
// tiles overlapping by 1, of a 150x103 photograph: levels of odd sizes),
// with 1 and 4 threads, and by ToFile for a name ending in .dzi; a grey
// image gives grey tiles; the tiles are in the format asked for. The sums
// are of sharp 0.35.5's files.
func TestTiles(t *testing.T) {
	sharp := map[string]string{
		"0/0_0.jpeg": "91f065114d5fc842",
		"1/0_0.jpeg": "b0277a966b1631b8",
		"2/0_0.jpeg": "90c1724b3521aa9a",
		"3/0_0.jpeg": "9ecad96d8a24ea6e",
		"4/0_0.jpeg": "99eb81baff1acfc5",
		"5/0_0.jpeg": "01b3a0e1b2e54a4e",
		"6/0_0.jpeg": "573d68c4fc785253",
		"7/0_0.jpeg": "b6c2e89150069a39",
		"7/1_0.jpeg": "2967f1ed06fdc10b",
		"8/0_0.jpeg": "752a4ae8fe3e4f92",
		"8/0_1.jpeg": "80d56ede841d11de",
		"8/1_0.jpeg": "f7879ecfc5a50082",
		"8/1_1.jpeg": "38733f1b4a224338",
		"8/2_0.jpeg": "eb9d59e727bf0601",
		"8/2_1.jpeg": "1aa4f65344bfc75b",
	}
	in := sourcePNG(t, "pillow/source.png")
	for _, threads := range []int{1, 4} {
		dir := t.TempDir()
		if err := FromBytes(in).Threads(threads).ToTiles(filepath.Join(dir, "s.dz"), TileOptions{Size: 64, Overlap: 1}); err != nil {
			t.Fatal(err)
		}
		got := tileSums(t, filepath.Join(dir, "s_files"))
		if len(got) != len(sharp) {
			t.Errorf("%d threads: %d tiles, sharp writes %d", threads, len(got), len(sharp))
		}
		for name, sum := range sharp {
			if got[name] != sum {
				t.Errorf("%d threads: %s is %q, sharp's %s", threads, name, got[name], sum)
			}
		}
		if dzi := tileSums(t, dir)["s.dzi"]; dzi != "9a78f87fcde24454" {
			t.Errorf("s.dzi: %s", dzi)
		}
	}
	// By the file name, with the defaults (256-pixel tiles): one per level.
	dir := t.TempDir()
	if err := FromBytes(in).ToFile(filepath.Join(dir, "x.dzi")); err != nil {
		t.Fatal(err)
	}
	if got := tileSums(t, filepath.Join(dir, "x_files")); len(got) != 9 || got["8/0_0.jpeg"] == "" {
		t.Errorf("ToFile x.dzi: %v", got)
	}
	// Grey tiles of a grey image; PNG tiles when PNG is asked for.
	gray := sourcePNG(t, "pillow/gray.png")
	if err := FromBytes(gray).ToTiles(filepath.Join(dir, "g"), TileOptions{Size: 64}); err != nil {
		t.Fatal(err)
	}
	if info, err := Open(filepath.Join(dir, "g_files", "8", "0_0.jpeg")).Metadata(); err != nil || info.Color != "gray" {
		t.Errorf("grey tile: %+v, %v", info, err)
	}
	if err := FromBytes(in).PNG().ToTiles(filepath.Join(dir, "p"), TileOptions{Size: 100}); err != nil {
		t.Fatal(err)
	}
	m := decodePNG(t, must(os.ReadFile(filepath.Join(dir, "p_files", "8", "1_1.png"))))
	if m.Bounds() != image.Rect(0, 0, 50, 3) {
		t.Errorf("an edge tile: %v", m.Bounds())
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "p.dzi")); !strings.Contains(string(b), `Format="png"`) {
		t.Errorf("p.dzi: %s", b)
	}
	for _, o := range []TileOptions{{Size: 9000}, {Size: 64, Overlap: 65}, {Size: -1}, {Layout: 5}} {
		if err := FromBytes(in).ToTiles(filepath.Join(dir, "bad"), o); err == nil {
			t.Errorf("%+v accepted", o)
		}
	}
}

// treeSum is a digest of every file under dir (by its path and the sum of
// its bytes) but blank.png.
func treeSum(t *testing.T, dir string) (int, string) {
	t.Helper()
	var lines []string
	for name, sum := range tileSums(t, dir) {
		if filepath.Base(name) != "blank.png" {
			lines = append(lines, name+" "+sum+"\n")
		}
	}
	slices.Sort(lines)
	return len(lines), fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join(lines, ""))))[:16]
}

// TestTileLayouts: the other layouts, depths, blank tiles and backgrounds,
// as sharp 0.35.5 writes them (libvips' dzsave): every tile and the files
// that describe them (info.json, ImageProperties.xml, .dzi) to the byte,
// with 1 and 4 threads; the digests are of sharp's trees. An image with
// transparency: its levels shrunk with the colours weighted by alpha, its
// JPEG tiles flattened over black, as libvips does both. Google's
// blank.png is a tile of the background.
func TestTileLayouts(t *testing.T) {
	src := sourcePNG(t, "pillow/source.png")
	rgba := sourcePNG(t, "pillow/median-rgba-source.png")
	half := color.NRGBA{255, 0, 0, 128}
	for _, c := range []struct {
		name  string
		in    []byte
		o     TileOptions
		files int
		sum   string
		blank color.NRGBA
	}{
		{"google", src, TileOptions{Size: 64, Layout: TileGoogle}, 9, "3fb3452d455ac037", color.NRGBA{255, 255, 255, 255}},
		{"google-rgba", rgba, TileOptions{Size: 32, Layout: TileGoogle, Center: true, Background: half, SkipBlanks: 40}, 37, "b62a4d9a4e17d759", half},
		{"google-overlap", src, TileOptions{Size: 64, Overlap: 3, Layout: TileGoogle, Center: true, Background: color.RGBA{10, 200, 30, 255}}, 13, "bc43e93b55e12893", color.NRGBA{10, 200, 30, 255}},
		{"iiif", src, TileOptions{Size: 40, Layout: TileIIIF, Depth: TileDepthOnePixel}, 24, "974753686776dd46", color.NRGBA{}},
		{"iiif3", src, TileOptions{Size: 64, Layout: TileIIIF3, ID: "https://images.example.org/iiif/x"}, 10, "a4e8fdf0ae377238", color.NRGBA{}},
		{"zoomify", src, TileOptions{Size: 20, Overlap: 2, Layout: TileZoomify}, 78, "6c5719e7bd036671", color.NRGBA{}},
		{"dz-rgba", rgba, TileOptions{Size: 64, Overlap: 1}, 16, "55d83fe99ba51ea1", color.NRGBA{}},
		{"dz-depth", src, TileOptions{Size: 64, Overlap: 1, Depth: TileDepthOneTile}, 10, "e1acc1855db7ab54", color.NRGBA{}},
		{"dz-skip", src, TileOptions{Size: 32, Overlap: 1, SkipBlanks: 60, Background: color.RGBA{120, 100, 60, 255}}, 33, "f5620b1395443660", color.NRGBA{}},
	} {
		for _, threads := range []int{1, 4} {
			dir := t.TempDir()
			if err := FromBytes(c.in).Threads(threads).ToTiles(filepath.Join(dir, "t"), c.o); err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			if n, sum := treeSum(t, dir); n != c.files || sum != c.sum {
				t.Errorf("%s, %d threads: %d files, digest %s; sharp's %d, %s", c.name, threads, n, sum, c.files, c.sum)
			}
			b, err := os.ReadFile(filepath.Join(dir, "t", "blank.png"))
			if c.o.Layout != TileGoogle {
				if err == nil {
					t.Errorf("%s: a blank.png", c.name)
				}
				continue
			}
			m := decodePNG(t, b)
			if m.Bounds().Dx() != c.o.Size || color.NRGBAModel.Convert(m.At(3, 5)) != c.blank {
				t.Errorf("%s: blank.png %v %v", c.name, m.Bounds(), m.At(3, 5))
			}
		}
	}
	// The overlap of a layout but Deep Zoom is less than the size; unknown
	// depths and layouts; SkipBlanks is at most 255.
	dir := t.TempDir()
	for _, o := range []TileOptions{{Size: 64, Overlap: 64, Layout: TileGoogle}, {Depth: 4}, {Layout: -1}, {SkipBlanks: 256}} {
		if err := FromBytes(src).ToTiles(filepath.Join(dir, "bad"), o); err == nil {
			t.Errorf("%+v accepted", o)
		}
	}
	if err := FromBytes(src).ToTiles(filepath.Join(dir, "ok"), TileOptions{Size: 64, Overlap: 64}); err != nil {
		t.Errorf("Deep Zoom overlap of the size: %v", err)
	}
}
