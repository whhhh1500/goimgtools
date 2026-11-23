package tests

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	. "github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/internal/testutil"
)

// pdfObjects reads a PDF's cross-reference table and returns each object's
// text, checking that every offset points at its object.
func pdfObjects(t *testing.T, pdf []byte) map[int][]byte {
	t.Helper()
	m := regexp.MustCompile(`startxref\n(\d+)\n%%EOF\n$`).FindSubmatch(pdf)
	if m == nil {
		t.Fatal("no startxref")
	}
	xref, _ := strconv.Atoi(string(m[1]))
	var n int
	if _, err := fmt.Sscanf(string(pdf[xref:]), "xref\n0 %d\n", &n); err != nil {
		t.Fatalf("xref: %v", err)
	}
	lines := bytes.Split(pdf[xref:], []byte("\n"))[2:]
	objs := map[int][]byte{}
	for i := 1; i < n; i++ {
		var off, gen int
		var kind string
		fmt.Sscanf(string(lines[i]), "%d %d %s", &off, &gen, &kind)
		if kind != "n" {
			continue
		}
		head := fmt.Sprintf("%d 0 obj\n", i)
		if !bytes.HasPrefix(pdf[off:], []byte(head)) {
			t.Fatalf("object %d is not at %d", i, off)
		}
		end := bytes.Index(pdf[off:], []byte("\nendobj\n"))
		objs[i] = pdf[off+len(head) : off+end]
	}
	return objs
}

// pdfStream is the data of a stream object, its length from the object
// the dictionary names.
func pdfStream(t *testing.T, objs map[int][]byte, n int) (dict string, data []byte) {
	t.Helper()
	o := objs[n]
	i := bytes.Index(o, []byte(">>\nstream\n"))
	dict = string(o[:i])
	var length int
	if m := regexp.MustCompile(`/Length (\d+) 0 R`).FindStringSubmatch(dict); m != nil {
		ref, _ := strconv.Atoi(m[1])
		length, _ = strconv.Atoi(string(bytes.TrimSpace(objs[ref])))
	} else if m := regexp.MustCompile(`/Length (\d+)`).FindStringSubmatch(dict); m != nil {
		length, _ = strconv.Atoi(m[1])
	}
	return dict, o[i+len(">>\nstream\n") : i+len(">>\nstream\n")+length]
}

// unpredict inflates rows filtered by PNG's predictors.
func unpredict(t *testing.T, data []byte, w, ch, h int) []byte {
	t.Helper()
	zr, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(zr)
	if err != nil || len(raw) != h*(1+w*ch) {
		t.Fatalf("%d bytes, %v", len(raw), err)
	}
	out := make([]byte, 0, h*w*ch)
	prev := make([]byte, w*ch)
	for y := 0; y < h; y++ {
		f, row := raw[y*(1+w*ch)], raw[y*(1+w*ch)+1:(y+1)*(1+w*ch)]
		cur := make([]byte, w*ch)
		for i := range row {
			var a, b, c int
			if i >= ch {
				a, c = int(cur[i-ch]), int(prev[i-ch])
			}
			b = int(prev[i])
			var p int
			switch f {
			case 1:
				p = a
			case 2:
				p = b
			case 3:
				p = (a + b) / 2
			case 4:
				pa, pb, pc := abs(b-c), abs(a-c), abs(a+b-2*c)
				switch {
				case pa <= pb && pa <= pc:
					p = a
				case pb <= pc:
					p = b
				default:
					p = c
				}
			}
			cur[i] = row[i] + uint8(p)
		}
		out = append(out, cur...)
		prev = cur
	}
	return out
}

// TestPDF: a document of one page as large as the image at its
// resolution, the picture without loss (colours, grey, the soft mask of
// transparency, the profile) or as a JPEG, every object where the
// cross-reference table says.
func TestPDF(t *testing.T) {
	src := testutil.Images(37, 23)["nrgba"].(*image.NRGBA)
	pdf, err := FromImage(src).Density(144).PDF().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	objs := pdfObjects(t, pdf)
	if !bytes.Contains(objs[3], []byte("/MediaBox [0 0 18.5 11.5]")) {
		t.Errorf("page: %s", objs[3])
	}
	dict, data := pdfStream(t, objs, 5)
	if !regexp.MustCompile(`/Width 37 /Height 23 /ColorSpace /DeviceRGB .*/SMask 7 0 R`).MatchString(dict) {
		t.Errorf("picture: %s", dict)
	}
	rgb := unpredict(t, data, 37, 3, 23)
	_, mdata := pdfStream(t, objs, 7)
	alpha := unpredict(t, mdata, 37, 1, 23)
	for y := 0; y < 23; y++ {
		for x := 0; x < 37; x++ {
			c := src.NRGBAAt(x, y)
			i := y*37 + x
			if got := (color.NRGBA{rgb[3*i], rgb[3*i+1], rgb[3*i+2], alpha[i]}); got != c && c.A != 0 {
				t.Fatalf("pixel %d,%d: %v, want %v", x, y, got, c)
			}
		}
	}
	// Grey, and a JPEG.
	grey := testutil.Images(30, 20)["gray"].(*image.Gray)
	pdf, err = FromImage(grey).PDF().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	objs = pdfObjects(t, pdf)
	dict, data = pdfStream(t, objs, 5)
	if !bytes.Contains([]byte(dict), []byte("/DeviceGray")) || !bytes.Equal(unpredict(t, data, 30, 1, 20), grey.Pix) {
		t.Errorf("grey: %s", dict)
	}
	photo := photoLike(120, 90)
	pdf, err = FromImage(photo).PDF(PDFOptions{Quality: 90}).ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	objs = pdfObjects(t, pdf)
	dict, data = pdfStream(t, objs, 5)
	j, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil || !bytes.Contains([]byte(dict), []byte("/DCTDecode")) || j.Bounds().Dx() != 120 {
		t.Fatalf("JPEG picture: %s %v", dict, err)
	}
	if _, err := FromImage(photo).PDF(PDFOptions{Quality: 101}).ToBytes(); err == nil {
		t.Error("quality 101 accepted")
	}
}

// refPDF reads the picture of a PDF this package wrote without loss,
// apart from its writer: through the cross-reference table to the image,
// its rows unfiltered, the soft mask applied.
func refPDF(pdf []byte) (image.Image, error) {
	t := &testing.T{}
	var out image.Image
	var err error
	func() {
		defer func() {
			if v := recover(); v != nil {
				err = fmt.Errorf("pdf: %v", v)
			}
		}()
		objs := pdfObjectsErr(pdf)
		if objs == nil {
			err = fmt.Errorf("pdf: no cross-reference table")
			return
		}
		dict, data := pdfStream(t, objs, 5)
		var w, h int
		if m := regexp.MustCompile(`/Width (\d+) /Height (\d+)`).FindStringSubmatch(dict); m != nil {
			w, _ = strconv.Atoi(m[1])
			h, _ = strconv.Atoi(m[2])
		}
		ch := 3
		if bytes.Contains([]byte(dict), []byte("/DeviceGray")) {
			ch = 1
		}
		px := unpredict(t, data, w, ch, h)
		img := image.NewNRGBA(image.Rect(0, 0, w, h))
		for i := 0; i < w*h; i++ {
			c := px[ch*i : ch*i+ch]
			if ch == 1 {
				c = []byte{c[0], c[0], c[0]}
			}
			copy(img.Pix[4*i:], c)
			img.Pix[4*i+3] = 255
		}
		if bytes.Contains([]byte(dict), []byte("/SMask")) {
			_, md := pdfStream(t, objs, 7)
			a := unpredict(t, md, w, 1, h)
			for i := range a {
				img.Pix[4*i+3] = a[i]
			}
		}
		out = img
	}()
	return out, err
}

// pdfObjectsErr is pdfObjects that reports nothing on a malformed file.
func pdfObjectsErr(pdf []byte) map[int][]byte {
	m := regexp.MustCompile(`startxref\n(\d+)\n%%EOF\n$`).FindSubmatch(pdf)
	if m == nil {
		return nil
	}
	xref, _ := strconv.Atoi(string(m[1]))
	var n int
	if _, err := fmt.Sscanf(string(pdf[xref:]), "xref\n0 %d\n", &n); err != nil {
		return nil
	}
	lines := bytes.Split(pdf[xref:], []byte("\n"))[2:]
	objs := map[int][]byte{}
	for i := 1; i < n; i++ {
		var off, gen int
		var kind string
		fmt.Sscanf(string(lines[i]), "%d %d %s", &off, &gen, &kind)
		if kind != "n" {
			continue
		}
		head := fmt.Sprintf("%d 0 obj\n", i)
		if !bytes.HasPrefix(pdf[off:], []byte(head)) {
			return nil
		}
		end := bytes.Index(pdf[off:], []byte("\nendobj\n"))
		objs[i] = pdf[off+len(head) : off+end]
	}
	return objs
}

// TestPDFJPEGAsIs: a JPEG stored without loss is put in the document as it
// is (its bytes, DCTDecode), with its profile and resolution, on a page of
// one image and in Pages; grey too; but not after an operation, at a
// quality, nor a CMYK JPEG: those are decoded and written as before.
func TestPDFJPEGAsIs(t *testing.T) {
	photo, err := FromImage(photoLike(160, 100)).Density(150).ToProfile(ProfileDisplayP3()).JPEG(JPEGOptions{Quality: 85}).ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	asIs := func(what string, pdf []byte, jpg []byte, space string, mediaBox string) {
		t.Helper()
		objs := pdfObjects(t, pdf)
		if !bytes.Contains(objs[3], []byte(mediaBox)) {
			t.Errorf("%s: page %s", what, objs[3])
		}
		dict, data := pdfStream(t, objs, 5)
		if !strings.Contains(dict, "/DCTDecode") || !strings.Contains(dict, space) || !bytes.Equal(data, jpg) {
			t.Errorf("%s: not the JPEG as it is: %s (%d bytes of %d)", what, dict, len(data), len(jpg))
		}
	}
	pdf, err := FromBytes(photo).PDF().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	asIs("one page", pdf, photo, "/ICCBased", "/MediaBox [0 0 76.8 48]")
	pdf, err = Pages([]*Image{FromBytes(photo), FromBytes(photo)}).ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	asIs("Pages", pdf, photo, "/ICCBased", "/MediaBox [0 0 76.8 48]")
	grey, err := FromImage(testutil.Images(40, 30)["gray"]).JPEG().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	pdf, err = FromBytes(grey).PDF().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	asIs("grey", pdf, grey, "/DeviceGray", "/MediaBox [0 0 40 30]")
	// Decoded: after an operation, at a quality, a CMYK file.
	cmyk, err := os.ReadFile(filepath.Join("testdata", "icc", "cmyk-plain.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	for what, m := range map[string]*Image{
		"resized":    FromBytes(photo).Resize(80, 0).PDF(),
		"quality 80": FromBytes(photo).PDF(PDFOptions{Quality: 80}),
		"CMYK":       FromBytes(cmyk).PDF(),
	} {
		pdf, err := m.ToBytes()
		if err != nil {
			t.Fatal(err)
		}
		_, data := pdfStream(t, pdfObjects(t, pdf), 5)
		if bytes.Equal(data, photo) || bytes.Equal(data, cmyk) {
			t.Errorf("%s: the JPEG as it is", what)
		}
	}
}
