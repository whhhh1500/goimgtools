package textsrc

import (
	"os"
	"testing"
)

// TestEnglishNames: a font is named in English though its table has the
// names of another language first (as Segoe UI and Microsoft YaHei do:
// "Negreta" before "Bold"), from a file and from bytes.
func TestEnglishNames(t *testing.T) {
	const path = "../../tests/testdata/fonts/NameTest.ttf"
	f, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if f.Info.Family != "NameTest" || f.Info.Style != "Bold" || f.Info.Full != "NameTest Bold" {
		t.Errorf("from a file: %+v", f.Info)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	g, err := OpenData(data)
	if err != nil {
		t.Fatal(err)
	}
	if g.Info.Full != "NameTest Bold" {
		t.Errorf("from bytes: %+v", g.Info)
	}
}
