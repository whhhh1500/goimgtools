package svg

import "math"

// kappa places the control points of a cubic curve that draws a quarter
// circle.
const kappa = 0.5522847498307936

// shapeSegs is the outline of a basic shape or path, in user space; nil
// for one that draws nothing.
func shapeSegs(n *node, st style, vp viewport) []seg {
	num := func(name string, axis int) (float64, bool) {
		v, ok := n.attr(name)
		if !ok {
			return 0, false
		}
		return parseLength(v, st.fontSize, vp, axis)
	}
	get := func(name string, axis int) float64 { v, _ := num(name, axis); return v }
	switch n.name {
	case "path":
		return parsePath(n.attrs["d"])
	case "rect":
		w, h := get("width", axisX), get("height", axisY)
		if !(w > 0) || !(h > 0) {
			return nil
		}
		rx, okX := num("rx", axisX)
		ry, okY := num("ry", axisY)
		if !okX || rx < 0 {
			okX, rx = false, 0
		}
		if !okY || ry < 0 {
			okY, ry = false, 0
		}
		switch {
		case okX && !okY:
			ry = rx
		case okY && !okX:
			rx = ry
		}
		return rectSegs(get("x", axisX), get("y", axisY), w, h, math.Min(rx, w/2), math.Min(ry, h/2))
	case "circle":
		r := get("r", axisDiag)
		if !(r > 0) {
			return nil
		}
		return ellipseSegs(get("cx", axisX), get("cy", axisY), r, r)
	case "ellipse":
		rx, okX := num("rx", axisX)
		ry, okY := num("ry", axisY)
		// An auto radius takes the other's value (SVG 2); so does a
		// negative one, as Chrome takes it.
		okX, okY = okX && rx >= 0, okY && ry >= 0
		if !okX {
			rx = ry
		}
		if !okY {
			ry = rx
		}
		if !(rx > 0) || !(ry > 0) {
			return nil
		}
		return ellipseSegs(get("cx", axisX), get("cy", axisY), rx, ry)
	case "line":
		return []seg{{cmd: cmdMove, pts: [3][2]float64{{get("x1", axisX), get("y1", axisY)}}},
			{cmd: cmdLine, pts: [3][2]float64{{get("x2", axisX), get("y2", axisY)}}}}
	case "polyline", "polygon":
		v, _ := numbers(n.attrs["points"])
		if len(v)%2 == 1 {
			v = v[:len(v)-1] // an odd number: the last is dropped
		}
		if len(v) < 4 {
			return nil
		}
		out := []seg{{cmd: cmdMove, pts: [3][2]float64{{v[0], v[1]}}}}
		for i := 2; i+1 < len(v) && len(out) < maxSegs; i += 2 {
			out = append(out, seg{cmd: cmdLine, pts: [3][2]float64{{v[i], v[i+1]}}})
		}
		if n.name == "polygon" {
			out = append(out, seg{cmd: cmdClose})
		}
		return out
	}
	return nil
}

// rectSegs is a rectangle with corners rounded by rx and ry.
func rectSegs(x, y, w, h, rx, ry float64) []seg {
	mv := func(px, py float64) seg { return seg{cmd: cmdMove, pts: [3][2]float64{{px, py}}} }
	ln := func(px, py float64) seg { return seg{cmd: cmdLine, pts: [3][2]float64{{px, py}}} }
	if rx <= 0 || ry <= 0 {
		return []seg{mv(x, y), ln(x+w, y), ln(x+w, y+h), ln(x, y+h), {cmd: cmdClose}}
	}
	kx, ky := float64(rx*kappa), float64(ry*kappa)
	cu := func(x1, y1, x2, y2, x3, y3 float64) seg {
		return seg{cmd: cmdCubic, pts: [3][2]float64{{x1, y1}, {x2, y2}, {x3, y3}}}
	}
	r, b := x+w, y+h
	return []seg{
		mv(x+rx, y), ln(r-rx, y),
		cu(r-rx+kx, y, r, y+ry-ky, r, y+ry), ln(r, b-ry),
		cu(r, b-ry+ky, r-rx+kx, b, r-rx, b), ln(x+rx, b),
		cu(x+rx-kx, b, x, b-ry+ky, x, b-ry), ln(x, y+ry),
		cu(x, y+ry-ky, x+rx-kx, y, x+rx, y),
		{cmd: cmdClose},
	}
}

// ellipseSegs is an ellipse as four cubic curves, from its rightmost point
// round clockwise on the screen, as the specification starts it.
func ellipseSegs(cx, cy, rx, ry float64) []seg {
	kx, ky := float64(rx*kappa), float64(ry*kappa)
	cu := func(x1, y1, x2, y2, x3, y3 float64) seg {
		return seg{cmd: cmdCubic, pts: [3][2]float64{{x1, y1}, {x2, y2}, {x3, y3}}}
	}
	return []seg{
		{cmd: cmdMove, pts: [3][2]float64{{cx + rx, cy}}},
		cu(cx+rx, cy+ky, cx+kx, cy+ry, cx, cy+ry),
		cu(cx-kx, cy+ry, cx-rx, cy+ky, cx-rx, cy),
		cu(cx-rx, cy-ky, cx-kx, cy-ry, cx, cy-ry),
		cu(cx+kx, cy-ry, cx+rx, cy-ky, cx+rx, cy),
		{cmd: cmdClose},
	}
}
