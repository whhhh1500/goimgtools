package heic_test

import (
	"testing"

	"github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/heic"
)

// The API of the module, held to its signatures: changing any of these
// breaks the build here, before the api job of the CI (apidiff against
// heic/<the tag in .github/api-base>) fails on it too. The exported API only
// grows; a new need gets a new name, option or field.
var (
	_ func() goimgtools.Codec = heic.Codec
	_ func([]byte) bool       = heic.Match

	// The default quality, which a saved setting may rely on.
	_ [60]struct{} = [heic.DefaultQuality]struct{}{}
)

// TestAPILock holds the fields of Options to their names and types.
func TestAPILock(t *testing.T) {
	var o heic.Options
	var (
		_ int  = o.Quality
		_ int  = o.Effort
		_ bool = o.Lossless
		_ bool = o.Chroma444
	)
	if c := heic.Codec(); c.Format.Name != "heic" || c.Decode == nil || c.Encode == nil || c.Quality == nil {
		t.Errorf("codec: %+v", c.Format)
	}
}
