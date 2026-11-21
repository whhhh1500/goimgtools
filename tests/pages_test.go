package tests

import (
	"image/color"
	"regexp"
	"strconv"
	"strings"
	"testing"

	. "github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/internal/testutil"
)

// TestPages: one page of each image, in order, each its own size and kind
// (grey, with transparency), read through its own recipe; other formats
// refuse; read as an image it is its first page.
func TestPages(t *testing.T) {
	a := FromImage(testutil.Images(40, 30)["nrgba"])
	b := FromImage(testutil.Images(20, 50)["gray"]).Resize(10, 0)
	c := Blank(7, 9, color.NRGBA{200, 10, 10, 255})
	pdf, err := Pages([]*Image{a, b, c}).PDF().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	objs := pdfObjects(t, pdf)
	if !regexp.MustCompile(`/Kids \[\d+ 0 R \d+ 0 R \d+ 0 R\] /Count 3`).Match(objs[2]) {
		t.Fatalf("pages: %s", objs[2])
	}
	// Each page's picture, through the tree: the sizes in order.
	var sizes []string
	for _, k := range regexp.MustCompile(`(\d+) 0 R`).FindAllSubmatch(regexp.MustCompile(`/Kids \[([^\]]*)\]`).FindSubmatch(objs[2])[1], -1) {
		n, _ := strconv.Atoi(string(k[1]))
		im, _ := strconv.Atoi(string(regexp.MustCompile(`/Im0 (\d+) 0 R`).FindSubmatch(objs[n])[1]))
		m := regexp.MustCompile(`/Width (\d+) /Height (\d+)`).FindSubmatch(objs[im])
		sizes = append(sizes, string(m[1])+"x"+string(m[2]))
	}
	if got := strings.Join(sizes, " "); got != "40x30 10x25 7x9" {
		t.Errorf("pages %s", got)
	}
	if _, err := Pages([]*Image{a, c}).PNG().ToBytes(); err == nil {
		t.Error("Pages as PNG accepted")
	}
	info, err := Pages([]*Image{c, a}).Metadata()
	if err != nil || info.Width != 7 {
		t.Errorf("first page: %+v %v", info, err)
	}
	if _, err := Pages(nil).PDF().ToBytes(); err == nil {
		t.Error("no pages accepted")
	}
}
