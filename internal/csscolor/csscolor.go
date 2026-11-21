// Package csscolor reads colours as CSS writes them: the 148 names of CSS
// Color 4, transparent, #rgb, #rgba, #rrggbb, #rrggbbaa, rgb() and rgba()
// with numbers or percentages, and hsl() and hsla(), in the comma or the
// space syntax.
package csscolor

import (
	"image/color"
	"math"
	"strconv"
	"strings"
)

// Parse reads a colour; ok is false when s is not one.
func Parse(s string) (c color.NRGBA, ok bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return c, false
	}
	if s[0] == '#' {
		return hex(s[1:])
	}
	if v, ok := names[s]; ok {
		return color.NRGBA{uint8(v >> 16), uint8(v >> 8), uint8(v), 255}, true
	}
	if s == "transparent" {
		return color.NRGBA{}, true
	}
	open := strings.IndexByte(s, '(')
	if open < 0 || !strings.HasSuffix(s, ")") {
		return c, false
	}
	fn, args := strings.TrimSpace(s[:open]), s[open+1:len(s)-1]
	var parts []string
	var alpha string
	if strings.Contains(args, ",") {
		parts = strings.Split(args, ",")
		if len(parts) == 4 {
			alpha, parts = parts[3], parts[:3]
		}
	} else {
		main := args
		if i := strings.IndexByte(args, '/'); i >= 0 {
			main, alpha = args[:i], args[i+1:]
		}
		parts = strings.Fields(main)
	}
	if len(parts) != 3 {
		return c, false
	}
	a := 1.0
	if alpha = strings.TrimSpace(alpha); alpha != "" {
		v, ok := number(alpha, 1)
		if !ok {
			return c, false
		}
		a = v
	}
	switch fn {
	case "rgb", "rgba":
		var v [3]float64
		for i, p := range parts {
			x, ok := number(strings.TrimSpace(p), 255)
			if !ok {
				return c, false
			}
			v[i] = x
		}
		return color.NRGBA{byteOf(v[0]), byteOf(v[1]), byteOf(v[2]), byteOf(a * 255)}, true
	case "hsl", "hsla":
		h, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(parts[0]), "deg"), 64)
		if err != nil {
			return c, false
		}
		// Saturation and lightness: percentages, or numbers of them.
		pct := func(p string) (float64, bool) {
			p = strings.TrimSpace(p)
			if strings.HasSuffix(p, "%") {
				return number(p, 1)
			}
			v, ok := number(p, 1)
			return v / 100, ok
		}
		sat, ok1 := pct(parts[1])
		l, ok2 := pct(parts[2])
		if !ok1 || !ok2 {
			return c, false
		}
		r, g, b := hslToRGB(h, clamp01(sat), clamp01(l))
		return color.NRGBA{byteOf(r * 255), byteOf(g * 255), byteOf(b * 255), byteOf(a * 255)}, true
	}
	return c, false
}

// number reads a number, a percentage of full.
func number(s string, full float64) (float64, bool) {
	if strings.HasSuffix(s, "%") {
		v, err := strconv.ParseFloat(strings.TrimSuffix(s, "%"), 64)
		return float64(v/100) * full, err == nil && !math.IsNaN(v)
	}
	v, err := strconv.ParseFloat(s, 64)
	return v, err == nil && !math.IsNaN(v)
}

func clamp01(v float64) float64 { return math.Max(0, math.Min(1, v)) }

func byteOf(v float64) uint8 { return uint8(math.Round(math.Max(0, math.Min(255, v)))) }

func hex(h string) (color.NRGBA, bool) {
	v, err := strconv.ParseUint(h, 16, 32)
	if err != nil {
		return color.NRGBA{}, false
	}
	switch len(h) {
	case 3:
		return color.NRGBA{uint8(v>>8&15) * 17, uint8(v>>4&15) * 17, uint8(v&15) * 17, 255}, true
	case 4:
		return color.NRGBA{uint8(v>>12&15) * 17, uint8(v>>8&15) * 17, uint8(v>>4&15) * 17, uint8(v&15) * 17}, true
	case 6:
		return color.NRGBA{uint8(v >> 16), uint8(v >> 8), uint8(v), 255}, true
	case 8:
		return color.NRGBA{uint8(v >> 24), uint8(v >> 16), uint8(v >> 8), uint8(v)}, true
	}
	return color.NRGBA{}, false
}

// hslToRGB is CSS Color 4's conversion, each on 0 to 1.
func hslToRGB(h, s, l float64) (r, g, b float64) {
	h = math.Mod(h, 360)
	if h < 0 {
		h += 360
	}
	f := func(n float64) float64 {
		k := math.Mod(n+h/30, 12)
		a := s * math.Min(l, 1-l)
		return l - float64(a*math.Max(-1, math.Min(k-3, math.Min(9-k, 1))))
	}
	return f(0), f(8), f(4)
}

// names are the named colours of CSS Color 4, as 0xRRGGBB.
var names = map[string]uint32{
	"aliceblue": 0xf0f8ff, "antiquewhite": 0xfaebd7, "aqua": 0x00ffff, "aquamarine": 0x7fffd4, "azure": 0xf0ffff,
	"beige": 0xf5f5dc, "bisque": 0xffe4c4, "black": 0x000000, "blanchedalmond": 0xffebcd, "blue": 0x0000ff,
	"blueviolet": 0x8a2be2, "brown": 0xa52a2a, "burlywood": 0xdeb887, "cadetblue": 0x5f9ea0, "chartreuse": 0x7fff00,
	"chocolate": 0xd2691e, "coral": 0xff7f50, "cornflowerblue": 0x6495ed, "cornsilk": 0xfff8dc, "crimson": 0xdc143c,
	"cyan": 0x00ffff, "darkblue": 0x00008b, "darkcyan": 0x008b8b, "darkgoldenrod": 0xb8860b, "darkgray": 0xa9a9a9,
	"darkgreen": 0x006400, "darkgrey": 0xa9a9a9, "darkkhaki": 0xbdb76b, "darkmagenta": 0x8b008b, "darkolivegreen": 0x556b2f,
	"darkorange": 0xff8c00, "darkorchid": 0x9932cc, "darkred": 0x8b0000, "darksalmon": 0xe9967a, "darkseagreen": 0x8fbc8f,
	"darkslateblue": 0x483d8b, "darkslategray": 0x2f4f4f, "darkslategrey": 0x2f4f4f, "darkturquoise": 0x00ced1,
	"darkviolet": 0x9400d3, "deeppink": 0xff1493, "deepskyblue": 0x00bfff, "dimgray": 0x696969, "dimgrey": 0x696969,
	"dodgerblue": 0x1e90ff, "firebrick": 0xb22222, "floralwhite": 0xfffaf0, "forestgreen": 0x228b22, "fuchsia": 0xff00ff,
	"gainsboro": 0xdcdcdc, "ghostwhite": 0xf8f8ff, "gold": 0xffd700, "goldenrod": 0xdaa520, "gray": 0x808080,
	"green": 0x008000, "greenyellow": 0xadff2f, "grey": 0x808080, "honeydew": 0xf0fff0, "hotpink": 0xff69b4,
	"indianred": 0xcd5c5c, "indigo": 0x4b0082, "ivory": 0xfffff0, "khaki": 0xf0e68c, "lavender": 0xe6e6fa,
	"lavenderblush": 0xfff0f5, "lawngreen": 0x7cfc00, "lemonchiffon": 0xfffacd, "lightblue": 0xadd8e6,
	"lightcoral": 0xf08080, "lightcyan": 0xe0ffff, "lightgoldenrodyellow": 0xfafad2, "lightgray": 0xd3d3d3,
	"lightgreen": 0x90ee90, "lightgrey": 0xd3d3d3, "lightpink": 0xffb6c1, "lightsalmon": 0xffa07a,
	"lightseagreen": 0x20b2aa, "lightskyblue": 0x87cefa, "lightslategray": 0x778899, "lightslategrey": 0x778899,
	"lightsteelblue": 0xb0c4de, "lightyellow": 0xffffe0, "lime": 0x00ff00, "limegreen": 0x32cd32, "linen": 0xfaf0e6,
	"magenta": 0xff00ff, "maroon": 0x800000, "mediumaquamarine": 0x66cdaa, "mediumblue": 0x0000cd,
	"mediumorchid": 0xba55d3, "mediumpurple": 0x9370db, "mediumseagreen": 0x3cb371, "mediumslateblue": 0x7b68ee,
	"mediumspringgreen": 0x00fa9a, "mediumturquoise": 0x48d1cc, "mediumvioletred": 0xc71585, "midnightblue": 0x191970,
	"mintcream": 0xf5fffa, "mistyrose": 0xffe4e1, "moccasin": 0xffe4b5, "navajowhite": 0xffdead, "navy": 0x000080,
	"oldlace": 0xfdf5e6, "olive": 0x808000, "olivedrab": 0x6b8e23, "orange": 0xffa500, "orangered": 0xff4500,
	"orchid": 0xda70d6, "palegoldenrod": 0xeee8aa, "palegreen": 0x98fb98, "paleturquoise": 0xafeeee,
	"palevioletred": 0xdb7093, "papayawhip": 0xffefd5, "peachpuff": 0xffdab9, "peru": 0xcd853f, "pink": 0xffc0cb,
	"plum": 0xdda0dd, "powderblue": 0xb0e0e6, "purple": 0x800080, "rebeccapurple": 0x663399, "red": 0xff0000,
	"rosybrown": 0xbc8f8f, "royalblue": 0x4169e1, "saddlebrown": 0x8b4513, "salmon": 0xfa8072, "sandybrown": 0xf4a460,
	"seagreen": 0x2e8b57, "seashell": 0xfff5ee, "sienna": 0xa0522d, "silver": 0xc0c0c0, "skyblue": 0x87ceeb,
	"slateblue": 0x6a5acd, "slategray": 0x708090, "slategrey": 0x708090, "snow": 0xfffafa, "springgreen": 0x00ff7f,
	"steelblue": 0x4682b4, "tan": 0xd2b48c, "teal": 0x008080, "thistle": 0xd8bfd8, "tomato": 0xff6347,
	"turquoise": 0x40e0d0, "violet": 0xee82ee, "wheat": 0xf5deb3, "white": 0xffffff, "whitesmoke": 0xf5f5f5,
	"yellow": 0xffff00, "yellowgreen": 0x9acd32,
}
