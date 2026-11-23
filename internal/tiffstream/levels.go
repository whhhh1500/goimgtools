package tiffstream

// levelInfo notes, from the entries p of the directory that ends at end,
// the place of the next directory and whether this one is a reduced image
// (bit 0 of NewSubfileType). Neither can make the decoding fail: a value
// that cannot be read is taken as none.
func (d *decoder) levelInfo(p []byte, end int64) {
	n := d.entryLen()
	for i := 0; i+n <= len(p); i += n {
		if d.byteOrder.Uint16(p[i:]) != tNewSubfileType {
			continue
		}
		if dt, count, val := d.field(p[i : i+n]); count == 1 && len(val) >= 4 {
			switch dt {
			case dtLong:
				d.reduced = d.byteOrder.Uint32(val)&1 != 0
			case dtShort:
				d.reduced = d.byteOrder.Uint16(val)&1 != 0
			}
		}
	}
	size := 4
	if d.big {
		size = 8
	}
	var b [8]byte
	if _, err := d.r.ReadAt(b[:size], end); err != nil {
		return
	}
	if d.big {
		d.next = int64(d.byteOrder.Uint64(b[:]))
	} else {
		d.next = int64(d.byteOrder.Uint32(b[:]))
	}
	if d.next < 0 {
		d.next = 0
	}
}

// UseLevel has the stream decode, instead of its image, the smallest of the
// reduced versions that follow it (the levels of a pyramid) of at least w x
// h pixels, of its proportions and kind of pixels, and reports whether it
// does; only before anything is decoded. The metadata stay those of the
// image.
func (s *stream) UseLevel(w, h int) bool {
	if s.y != 0 || s.done || s.err != nil || s.r == nil || s.d.mode == mPaletted {
		return false
	}
	full := s.d.config
	var best *stream
	seen := map[int64]bool{}
	for off, n := s.d.next, 0; off > 0 && n < 32 && !seen[off]; n++ {
		seen[off] = true
		d, err := newDecoderAt(s.r, off)
		if err != nil {
			break
		}
		off = d.next
		c := d.config
		if !d.reduced || c.Width < w || c.Height < h || c.Width >= full.Width || d.mode != s.d.mode || d.bpp != s.d.bpp ||
			d.grayAlpha != s.d.grayAlpha || d.premult != s.d.premult || (d.stored == nil) != (s.d.stored == nil) {
			continue
		}
		// Of the image's proportions, but for the rounding of each halving.
		if diff := int64(c.Width)*int64(full.Height) - int64(c.Height)*int64(full.Width); max(diff, -diff) > 2*int64(max(full.Width, full.Height)) {
			continue
		}
		if best != nil && best.d.config.Width <= c.Width {
			continue
		}
		ns, err := openDecoder(s.r, s.env, d)
		if err != nil {
			continue
		}
		if best != nil {
			best.Close()
		}
		best = ns
	}
	if best == nil {
		return false
	}
	nd, od := best.d, s.d
	nd.icc, nd.xmp, nd.iptc, nd.exif, nd.xres, nd.resUnit = od.icc, od.xmp, od.iptc, od.exif, od.xres, od.resUnit
	s.Close()
	*s = *best
	return true
}
