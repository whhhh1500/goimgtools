package vp8lenc

// Pixels are packed as 0xAARRGGBB, the order the format defines them in.

// subPixels subtracts b from a channel by channel, modulo 256.
func subPixels(a, b uint32) uint32 {
	ag := 0x00ff00ff + (a & 0xff00ff00) - (b & 0xff00ff00)
	rb := 0xff00ff00 + (a & 0x00ff00ff) - (b & 0x00ff00ff)
	return ag&0xff00ff00 | rb&0x00ff00ff
}

// subGreen applies the subtract-green transform to one pixel.
func subGreen(p uint32) uint32 {
	g := p >> 8 & 0xff
	rb := (p&0x00ff00ff + 0x01000100 - g*0x00010001) & 0x00ff00ff
	return p&0xff00ff00 | rb
}

// avg2 is the per-channel average, rounded down.
func avg2(a, b uint32) uint32 {
	return a&b + (a^b)&0xfefefefe>>1
}

func absDiff(a, b uint32) uint32 {
	if a > b {
		return a - b
	}
	return b - a
}

// sad is the sum of the absolute channel differences of two pixels.
func sad(a, b uint32) uint32 {
	return absDiff(a>>24, b>>24) + absDiff(a>>16&0xff, b>>16&0xff) +
		absDiff(a>>8&0xff, b>>8&0xff) + absDiff(a&0xff, b&0xff)
}

func clamp255(v int32) uint32 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint32(v)
}

// clampAddSub is predictor 12: clamp(l + t - tl) per channel.
func clampAddSub(l, t, tl uint32) uint32 {
	a := clamp255(int32(l>>24) + int32(t>>24) - int32(tl>>24))
	r := clamp255(int32(l>>16&0xff) + int32(t>>16&0xff) - int32(tl>>16&0xff))
	g := clamp255(int32(l>>8&0xff) + int32(t>>8&0xff) - int32(tl>>8&0xff))
	b := clamp255(int32(l&0xff) + int32(t&0xff) - int32(tl&0xff))
	return a<<24 | r<<16 | g<<8 | b
}

// clampAddSubHalf is predictor 13: clamp(a + (a-b)/2) per channel, the
// division truncating toward zero.
func clampAddSubHalf(a, b uint32) uint32 {
	f := func(x, y int32) uint32 { return clamp255(x + (x-y)/2) }
	return f(int32(a>>24), int32(b>>24))<<24 |
		f(int32(a>>16&0xff), int32(b>>16&0xff))<<16 |
		f(int32(a>>8&0xff), int32(b>>8&0xff))<<8 |
		f(int32(a&0xff), int32(b&0xff))
}

// residuals writes cur[x] minus predictor `mode` to out[x] for x in
// [x0, x1), x0 >= 1. up is the row above and must hold x1+1 pixels (its
// element x1 is the top-right neighbour of the last pixel, which for the
// rightmost column is the first pixel of the current row).
func residuals(mode uint8, cur, up []uint32, x0, x1 int, out []uint32) {
	if x0 >= x1 {
		return
	}
	cur = cur[:x1]
	out = out[:x1]
	if mode >= 2 {
		up = up[:x1+1]
	}
	switch mode {
	case 0:
		for x := x0; x < x1; x++ {
			out[x] = subPixels(cur[x], 0xff000000)
		}
	case 1:
		for x := x0; x < x1; x++ {
			out[x] = subPixels(cur[x], cur[x-1])
		}
	case 2:
		for x := x0; x < x1; x++ {
			out[x] = subPixels(cur[x], up[x])
		}
	case 3:
		for x := x0; x < x1; x++ {
			out[x] = subPixels(cur[x], up[x+1])
		}
	case 4:
		for x := x0; x < x1; x++ {
			out[x] = subPixels(cur[x], up[x-1])
		}
	case 5:
		for x := x0; x < x1; x++ {
			out[x] = subPixels(cur[x], avg2(avg2(cur[x-1], up[x+1]), up[x]))
		}
	case 6:
		for x := x0; x < x1; x++ {
			out[x] = subPixels(cur[x], avg2(cur[x-1], up[x-1]))
		}
	case 7:
		for x := x0; x < x1; x++ {
			out[x] = subPixels(cur[x], avg2(cur[x-1], up[x]))
		}
	case 8:
		for x := x0; x < x1; x++ {
			out[x] = subPixels(cur[x], avg2(up[x-1], up[x]))
		}
	case 9:
		for x := x0; x < x1; x++ {
			out[x] = subPixels(cur[x], avg2(up[x], up[x+1]))
		}
	case 10:
		for x := x0; x < x1; x++ {
			out[x] = subPixels(cur[x], avg2(avg2(cur[x-1], up[x-1]), avg2(up[x], up[x+1])))
		}
	case 11:
		for x := x0; x < x1; x++ {
			l, t, tl := cur[x-1], up[x], up[x-1]
			p := t
			if sad(tl, t) < sad(tl, l) {
				p = l
			}
			out[x] = subPixels(cur[x], p)
		}
	case 12:
		for x := x0; x < x1; x++ {
			out[x] = subPixels(cur[x], clampAddSub(cur[x-1], up[x], up[x-1]))
		}
	case 13:
		for x := x0; x < x1; x++ {
			out[x] = subPixels(cur[x], clampAddSubHalf(avg2(cur[x-1], up[x]), up[x-1]))
		}
	}
}
