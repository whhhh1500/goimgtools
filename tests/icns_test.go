package tests

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"testing"

	. "github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/internal/testutil"
)

// TestICNS: an icon of the standard sizes up to the image's, each square,
// the image fitted in its proportions; its largest image read back; the
// older run-length images read as Pillow reads them.
func TestICNS(t *testing.T) {
	src := testutil.Images(300, 200)["nrgba"]
	data, err := FromImage(src).ICNS().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	if string(data[:4]) != "icns" || int(binary.BigEndian.Uint32(data[4:])) != len(data) || string(data[8:12]) != "TOC " {
		t.Fatalf("header % x", data[:16])
	}
	// The table of contents: icp4, icp5, ic11, icp6, ic12, ic07, ic08, ic13.
	n := (int(binary.BigEndian.Uint32(data[12:])) - 8) / 8
	var types []string
	for i := 0; i < n; i++ {
		types = append(types, string(data[16+8*i:20+8*i]))
	}
	if got := fmtTypes(types); got != "icp4 icp5 ic11 icp6 ic12 ic07 ic08 ic13" {
		t.Errorf("types %s", got)
	}
	m, err := FromBytes(data).ToImage()
	if err != nil {
		t.Fatal(err)
	}
	if b := m.Bounds(); b.Dx() != 256 || b.Dy() != 256 {
		t.Fatalf("read back %v", b)
	}
	// Fitted: transparent above and below the picture.
	if c := color.NRGBAModel.Convert(m.At(128, 5)).(color.NRGBA); c.A != 0 {
		t.Errorf("padding %v", c)
	}
	if _, err := FromImage(src).ICNS(ICNSOptions{Sizes: []int{48}}).ToBytes(); err == nil {
		t.Error("size 48 accepted")
	}
	// Another PNG level: other bytes, the same pixels; 9 is the default.
	fast := must(FromImage(src).ICNS(ICNSOptions{CompressionLevel: 1}).ToBytes())
	nine := must(FromImage(src).ICNS(ICNSOptions{CompressionLevel: 9}).ToBytes())
	if bytes.Equal(fast, data) || !bytes.Equal(nine, data) || len(fast) <= len(data) {
		t.Errorf("level 1: %d bytes, level 9: %d, default %d", len(fast), len(nine), len(data))
	}
	if d := worstDiff(t, decodeNRGBA(t, fast), decodeNRGBA(t, data)); d != 0 {
		t.Errorf("level 1 read back %d off", d)
	}
	if _, err := FromImage(src).ICNS(ICNSOptions{CompressionLevel: 11}).ToBytes(); err == nil {
		t.Error("level 11 accepted")
	}
	small, err := FromImage(image.NewNRGBA(image.Rect(0, 0, 5, 5))).ICNS().ToBytes()
	if err != nil || !bytes.Contains(small, []byte("icp4")) {
		t.Errorf("a 5x5 image: %v", err)
	}
	a, err1 := FromBytes(pillowFile(t, "fmt-legacy.icns")).ToImage()
	b, err2 := FromBytes(pillowFile(t, "fmt-legacy.icns.png")).ToImage()
	if err1 != nil || err2 != nil || !psdSame(a, b) {
		t.Errorf("legacy icon: %v %v", err1, err2)
	}
}

func fmtTypes(ts []string) string {
	var b bytes.Buffer
	for i, t := range ts {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(t)
	}
	return b.String()
}
