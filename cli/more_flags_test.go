package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/inspect"
	"github.com/whhhh1500/goimgtools/internal/testutil"
)

// TestLutAndIptcFlags: -lut applies the cube of a .cube file as ColorLUT
// does, and -keep-iptc keeps the IPTC record as KeepIptc does; info names
// the record.
func TestLutAndIptcFlags(t *testing.T) {
	dir := t.TempDir()
	in, cube, out := filepath.Join(dir, "in.png"), filepath.Join(dir, "look.cube"), filepath.Join(dir, "out.png")
	writePNG(t, in, testutil.Images(60, 40)["nrgba"])
	text := "LUT_3D_SIZE 2\n0 0 0\n0 1 0\n1 0 0\n1 1 0\n0 0 1\n0 1 1\n1 0 1\n1 1 1\n" // red and green change places
	if err := os.WriteFile(cube, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := cli("-lut", cube, in, out); err != nil {
		t.Fatal(err)
	}
	l, err := goimgtools.ParseCube(strings.NewReader(text))
	if err != nil {
		t.Fatal(err)
	}
	want, err := goimgtools.Open(in).ColorLUT(l).PNG().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(out); !bytes.Equal(got, want) {
		t.Error("-lut: the file differs from ColorLUT's")
	}
	if plain, _ := goimgtools.Open(in).PNG().ToBytes(); bytes.Equal(want, plain) {
		t.Error("the cube changed nothing")
	}
	for _, bad := range []string{filepath.Join(dir, "none.cube"), in} {
		if _, _, err := cli("-lut", bad, in, out); err == nil || !strings.Contains(err.Error(), "-lut") {
			t.Errorf("-lut %s: %v", filepath.Base(bad), err)
		}
	}

	// -tiff-tile and -tiff-pyramid are TIFFOptions' Tile and Pyramid.
	tif := filepath.Join(dir, "out.tif")
	if _, _, err := cli("-tiff-tile", "32", "-tiff-pyramid", "-tiff-compression", "deflate", in, tif); err != nil {
		t.Fatal(err)
	}
	wantTIFF, err := goimgtools.Open(in).TIFF(goimgtools.TIFFOptions{Tile: 32, Pyramid: true, Compression: goimgtools.TIFFDeflate}).ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(tif); !bytes.Equal(got, wantTIFF) {
		t.Error("-tiff-tile -tiff-pyramid: the file differs from TIFFOptions'")
	}
	if strips, _ := goimgtools.Open(in).TIFF(goimgtools.TIFFOptions{Compression: goimgtools.TIFFDeflate}).ToBytes(); bytes.Equal(strips, wantTIFF) {
		t.Error("tiles and a pyramid changed nothing")
	}

	// -gif-effort is GIFOptions' Effort.
	gifOut := filepath.Join(dir, "out.gif")
	if _, _, err := cli("-gif-effort", "1", in, gifOut); err != nil {
		t.Fatal(err)
	}
	wantGIF, err := goimgtools.Open(in).GIF(goimgtools.GIFOptions{Effort: 1}).ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(gifOut); !bytes.Equal(got, wantGIF) {
		t.Error("-gif-effort 1: the file differs from GIFOptions{Effort: 1}'s")
	}
	if _, _, err := cli("-gif-effort", "12", in, gifOut); err == nil {
		t.Error("-gif-effort 12: written")
	}

	// A JPEG with an IPTC record in an APP13 segment.
	rec := []byte("\x1c\x02\x78\x00\x09A caption")
	res := append([]byte("Photoshop 3.0\x008BIM\x04\x04\x00\x00\x00\x00\x00"), byte(len(rec)))
	res = append(append(res, rec...), 0)
	jpg, err := goimgtools.Open(in).JPEG().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	seg := append([]byte{0xff, 0xed, byte((len(res) + 2) >> 8), byte(len(res) + 2)}, res...)
	withIptc := filepath.Join(dir, "iptc.jpg")
	if err := os.WriteFile(withIptc, append(append(append([]byte(nil), jpg[:2]...), seg...), jpg[2:]...), 0o644); err != nil {
		t.Fatal(err)
	}
	kept, dropped := filepath.Join(dir, "kept.jpg"), filepath.Join(dir, "dropped.jpg")
	if _, _, err := cli("-keep-iptc", withIptc, kept); err != nil {
		t.Fatal(err)
	}
	if _, _, err := cli(withIptc, dropped); err != nil {
		t.Fatal(err)
	}
	if got, ok, err := inspect.ReadIptc(goimgtools.Open(kept)); err != nil || !ok || !bytes.Equal(got, rec) {
		t.Errorf("-keep-iptc: %q, %v, %v", got, ok, err)
	}
	if _, ok, _ := inspect.ReadIptc(goimgtools.Open(dropped)); ok {
		t.Error("the record was kept without -keep-iptc")
	}
	text, _, err = cli("info", withIptc, dropped)
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Split(strings.TrimSpace(text), "\n"); len(lines) != 2 || !strings.Contains(lines[0], ", IPTC") || strings.Contains(lines[1], "IPTC") {
		t.Errorf("info:\n%s", text)
	}
}
