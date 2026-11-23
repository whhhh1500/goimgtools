package tests

import (
	"os/exec"
	"strings"
	"testing"
)

// TestNoCrypto: nothing of the library or its command brings in a crypto
// package. crypto/sha256 alone brings in the FIPS 140 module, whose 32MB
// entropy buffer Windows counts as committed memory in every process from
// the start (internal/sha256 is used instead).
func TestNoCrypto(t *testing.T) {
	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go command here:", err)
	}
	out, err := exec.Command(gobin, "list", "-deps",
		"github.com/whhhh1500/goimgtools/...").CombinedOutput()
	if err != nil {
		t.Skipf("go list: %v\n%s", err, out)
	}
	for _, p := range strings.Fields(string(out)) {
		if p == "crypto" || strings.HasPrefix(p, "crypto/") {
			t.Errorf("%s is a dependency", p)
		}
	}
}
