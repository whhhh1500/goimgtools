package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/internal/testutil"
)

// TestMark: -mark hides a text that readmark reads back with the key, as
// the library writes it; another key, an unmarked file and wrong settings
// are told apart.
func TestMark(t *testing.T) {
	dir := t.TempDir()
	in, out, plain := filepath.Join(dir, "in.png"), filepath.Join(dir, "out.jpg"), filepath.Join(dir, "plain.jpg")
	if err := goimgtools.FromImage(testutil.Natural(800, 600, 2)).PNG().ToFile(in); err != nil {
		t.Fatal(err)
	}
	if _, stderr, err := cli("-mark", "user 42", "-mark-key", "k", "-mark-kind", "screen", "-quality", "85", in, out); err != nil {
		t.Fatalf("%v: %s", err, stderr)
	}
	want, err := goimgtools.Open(in).InvisibleWatermark([]byte("user 42"), goimgtools.InvisibleWatermarkOptions{Key: "k", Kind: goimgtools.WatermarkScreen}).
		JPEG(goimgtools.JPEGOptions{Quality: 85}).ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(out); string(got) != string(want) {
		t.Error("-mark: another file than the library's")
	}
	if _, _, err := cli("-quality", "85", in, plain); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := cli("readmark", "-key", "k", out, plain)
	if err != nil || !strings.Contains(stdout, out+`: "user 42"`) || !strings.Contains(stdout, plain+": no mark") {
		t.Errorf("readmark: %v\n%s", err, stdout)
	}
	if stdout, _, _ := cli("readmark", "-key", "k", "-hex", out); !strings.Contains(stdout, out+": 75736572203432") {
		t.Errorf("readmark -hex: %s", stdout)
	}
	if stdout, _, _ := cli("readmark", "-key", "other", out); !strings.Contains(stdout, "no mark") {
		t.Errorf("readmark with another key: %s", stdout)
	}
	for _, args := range [][]string{
		{"-mark", "x", "-mark-kind", "loud", in, out},
		{"-mark", strings.Repeat("x", 33), in, out},
		{"-mark", "x", "-mark-strength", "-1", in, out},
		{"-mark", "x", "-mark-density", "40", in, out},
		{"readmark"},
		{"readmark", filepath.Join(dir, "missing.png")},
	} {
		if _, _, err := cli(args...); err == nil {
			t.Errorf("%q: no error", args)
		}
	}
}
