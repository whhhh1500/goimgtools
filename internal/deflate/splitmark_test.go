package deflate

import (
	"bytes"
	"testing"
)

// TestSplitMarksSame: the marks of the parse change nothing in the output.
func TestSplitMarksSame(t *testing.T) {
	defer func(v bool) { splitMarks = v }(splitMarks)
	p := Level12
	p.NoSkip, p.SplitByCost, p.SplitPasses, p.BlockLen, p.CachePerBlock = true, true, 2, 1<<20, 3
	fine := p
	fine.FineSplit, fine.MoreCodes = true, true
	s := samples()
	big := bytes.Join([][]byte{s["rows"], s["text"], s["random"][:50000], s["rows"][:100000], s["mixed"]}, nil)
	for _, q := range []Params{p, fine} {
		for name, data := range map[string][]byte{"mixed": s["mixed"], "rows": s["rows"], "big": big} {
			var out [2][]byte
			for k, on := range []bool{false, true} {
				splitMarks = on
				out[k] = New(q).Compress(nil, data, 0, true)
			}
			if !bytes.Equal(out[0], out[1]) {
				t.Fatalf("%s (fine %v): %d bytes with the marks, %d without", name, q.FineSplit, len(out[1]), len(out[0]))
			}
			if got := inflate(t, nil, out[1]); !bytes.Equal(got, data) {
				t.Fatalf("%s: round trip differs", name)
			}
		}
	}
}
