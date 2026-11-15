package tests

import (
	"bytes"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/internal/testutil"
)

// TestWebPMetaLibwebp: libwebp (through Python's PIL) finds the colour
// profile and the EXIF block of the WebP files written here, unchanged,
// and decodes their pixels. Skipped without Python and PIL.
func TestWebPMetaLibwebp(t *testing.T) {
	py, err := exec.LookPath("python")
	if err != nil {
		t.Skip("python not found")
	}
	if out, err := exec.Command(py, "-c", "from PIL import features; assert features.check('webp')").CombinedOutput(); err != nil {
		t.Skipf("PIL with WebP support not available: %v %s", err, out)
	}
	icc := make([]byte, 3001) // odd: the chunk gets a padding byte
	for i := range icc {
		icc[i] = byte(i * 13)
	}
	exif := exifWithOrientation(binary.BigEndian, 3)
	jp := withAPP(jpegBytes(testutil.Images(70, 50)["rgba-opaque"], 90),
		app(0xe1, append([]byte("Exif\x00\x00"), exif...)),
		app(0xe2, append([]byte("ICC_PROFILE\x00\x01\x01"), icc...)))
	clear := pngBytes(t, testutil.Images(70, 50)["nrgba"])
	dir := t.TempDir()
	cases := map[string]*Image{
		"lossy":           FromBytes(jp).KeepExif().WebP(),
		"lossless":        FromBytes(jp).KeepExif().WebP(WebPOptions{Lossless: true}),
		"lossy, alpha":    FromBytes(withPNGProfile(t, clear, icc)).WebP(),
		"profile only":    FromBytes(jp).WebP(),
		"lossless, alpha": FromBytes(withPNGProfile(t, clear, icc)).WebP(WebPOptions{Lossless: true}),
	}
	var script strings.Builder
	script.WriteString("import sys\nfrom PIL import Image\n")
	for name, m := range cases {
		b, err := m.ToBytes()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		path := filepath.Join(dir, strings.NewReplacer(" ", "_", ",", "").Replace(name)+".webp")
		if err := os.WriteFile(path, b, 0o644); err != nil {
			t.Fatal(err)
		}
		script.WriteString("im = Image.open(r'" + path + "'); im.load()\n")
		script.WriteString("print(r'" + name + "|%dx%d|%d|%s' % (im.size + (len(im.info.get('icc_profile') or b''), (im.info.get('exif') or b'').hex())))\n")
	}
	out, err := exec.Command(py, "-c", script.String()).CombinedOutput()
	if err != nil {
		t.Fatalf("PIL could not read the files: %v\n%s", err, out)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Split(strings.TrimSpace(line), "|")
		if len(f) != 4 {
			t.Fatalf("PIL printed %q", line)
		}
		name, size, iccLen, gotExif := f[0], f[1], f[2], f[3]
		if size != "70x50" || iccLen != "3001" {
			t.Errorf("%s: PIL read %s with a %s-byte profile, want 70x50 and 3001", name, size, iccLen)
		}
		wantExif := ""
		if name == "lossy" || name == "lossless" {
			wantExif = bytesHex(exif)
		}
		if (wantExif == "") != (gotExif == "") || !strings.HasSuffix(gotExif, wantExif) {
			t.Errorf("%s: PIL found EXIF %q, want %q", name, gotExif, wantExif)
		}
	}
	t.Logf("%s", out)
}

func bytesHex(b []byte) string {
	const digits = "0123456789abcdef"
	var s strings.Builder
	for _, c := range b {
		s.WriteByte(digits[c>>4])
		s.WriteByte(digits[c&15])
	}
	return s.String()
}

// withPNGProfile is a PNG with an iCCP chunk holding icc, made by the
// library from one without.
func withPNGProfile(t *testing.T, png, icc []byte) []byte {
	t.Helper()
	jp := withAPP(jpegBytes(testutil.Images(2, 2)["rgba-opaque"], 90), app(0xe2, append([]byte("ICC_PROFILE\x00\x01\x01"), icc...)))
	carrier, err := FromBytes(jp).PNG().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	// Copy the iCCP chunk of the carrier into png, after IHDR.
	i := bytes.Index(carrier, []byte("iCCP")) - 4
	n := int(binary.BigEndian.Uint32(carrier[i:]))
	chunk := carrier[i : i+12+n]
	ihdrEnd := 8 + 8 + 13 + 4
	return append(append(append([]byte(nil), png[:ihdrEnd]...), chunk...), png[ihdrEnd:]...)
}
