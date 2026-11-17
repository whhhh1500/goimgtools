package tests

import (
	"crypto/sha256"
	"fmt"
	"image"
	"os"
	"path/filepath"
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
