package svg

import (
	"math"
	"strconv"
	"strings"

	"github.com/whhhh1500/goimgtools/internal/shape"
)

// instance is the instance a font with variations is drawn at for a style,
// as Chrome sets it: font-weight on the wght axis, font-stretch on wdth,
// an oblique font-style on slnt (its angle the other way), italic on ital,
// then the axes font-variation-settings names over them. The axes a font
// has not are passed over.
func instance(st style) []shape.Variation {
	w := float32(st.weight)
	if w == 0 {
		w = 400
	}
	sw := float32(st.stretch)
	if sw == 0 {
		sw = 100
	}
	vs := []shape.Variation{{Tag: "wght", Value: w}, {Tag: "wdth", Value: sw}, {Tag: "opsz", Value: float32(st.fontSize)}}
	switch {
	case st.oblique != 0:
		vs = append(vs, shape.Variation{Tag: "slnt", Value: float32(-st.oblique)})
	case st.italic:
		vs = append(vs, shape.Variation{Tag: "ital", Value: 1})
	}
	for _, v := range variationSettings(st.fontVar) {
		found := false
		for i := range vs {
			if vs[i].Tag == v.Tag {
				vs[i].Value, found = v.Value, true
			}
		}
		if !found {
			vs = append(vs, v)
		}
	}
	return vs
}

// instanceKey is what of a style its instance depends on.
type instanceKey struct {
	weight           int
	stretch, oblique float64
	italic           bool
	fontVar          string
}

func instanceOf(st style) instanceKey {
	return instanceKey{st.weight, st.stretch, st.oblique, st.italic, st.fontVar}
}

// variationSettings reads font-variation-settings: a list of a tag of four
// letters in quotes and a number, by commas; nothing when it is not one.
func variationSettings(s string) []shape.Variation {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []shape.Variation
	for _, part := range strings.Split(s, ",") {
		f := strings.Fields(part)
		if len(f) != 2 || len(f[0]) != 6 || f[0][0] != f[0][5] || f[0][0] != '"' && f[0][0] != '\'' {
			return nil
		}
		v, err := strconv.ParseFloat(f[1], 32)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
			return nil
		}
		out = append(out, shape.Variation{Tag: f[0][1:5], Value: float32(v)})
	}
	return out
}
