package tests

import (
	"bytes"
	"image"
	"strings"
	"testing"

	. "github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/inspect"
)

// TestXmp: the XMP packet sharp 0.35 writes into JPEG, PNG (zTXt), WebP
// and TIFF is read, kept in every one of these formats with KeepXmp (where
// this library writes PNG's as iTXt) and dropped without it.
func TestXmp(t *testing.T) {
	const mark = `xmp:Rating="4"`
	read := func(name string, data []byte) []byte {
		t.Helper()
		x, ok, err := inspect.ReadXmp(FromBytes(data))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if info, err := FromBytes(data).Metadata(); err != nil || info.HasXmp != ok {
			t.Errorf("%s: HasXmp %v, ReadXmp found %v (%v)", name, info.HasXmp, ok, err)
		}
		return x
	}
	for _, in := range []string{"jpeg", "png", "webp", "tiff"} {
		src := sourcePNG(t, "sharp/xmp."+in)
		x := read(in, src)
		if !bytes.Contains(x, []byte(mark)) || !strings.HasPrefix(string(x), "<x:xmpmeta") {
			t.Fatalf("%s: XMP %q", in, x)
		}
		for _, out := range []string{"jpeg", "png", "webp", "tiff"} {
			kept := must(FromBytes(src).KeepXmp().ToFormat(out).ToBytes())
			if got := read(in+" to "+out, kept); !bytes.Equal(got, x) {
				t.Errorf("%s to %s: XMP %q, want %q", in, out, got, x)
			}
			if got := read(in+" to "+out+" dropped", must(FromBytes(src).ToFormat(out).ToBytes())); got != nil {
				t.Errorf("%s to %s without KeepXmp: XMP kept", in, out)
			}
		}
	}
	// A PNG with the packet in a tEXt chunk, as some programs write it.
	plain := must(FromImage(image.NewGray(image.Rect(0, 0, 4, 4))).PNG().ToBytes())
	i := bytes.Index(plain, []byte("IDAT")) - 4
	chunk := pngChunk("tEXt", []byte("XML:com.adobe.xmp\x00<x:xmpmeta>t</x:xmpmeta>"))
	withText := append(append(append([]byte(nil), plain[:i]...), chunk...), plain[i:]...)
	if got := read("tEXt", withText); string(got) != "<x:xmpmeta>t</x:xmpmeta>" {
		t.Errorf("tEXt: %q", got)
	}
}
