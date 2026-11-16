// Copyright 2012 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the licenses/LICENSE-GO file.

package jpegstream

import (
	"image"
	mbits "math/bits"
)

// setup is the standard library's makeImg without the allocation: it decides
// the subsampling ratio and flex mode. The strip buffers are allocated by the
// stream.
func (d *decoder) setup(mxx, myy int) {
	d.ready, d.mxx, d.myy = true, mxx, myy
	if d.nComp == 1 {
		return
	}

	// Determine if we need flex mode for non-standard subsampling.
	// Flex mode is needed when:
	// - Cb and Cr have different sampling factors, or
	// - The Y component doesn't have the maximum sampling factors, or
	// - The ratio doesn't match any standard YCbCrSubsampleRatio.
	subsampleRatio := image.YCbCrSubsampleRatio444
	if d.comp[1].h != d.comp[2].h || d.comp[1].v != d.comp[2].v ||
		d.maxH != d.comp[0].h || d.maxV != d.comp[0].v {
		d.flex = true
	} else {
		hRatio := d.maxH / d.comp[1].h
		vRatio := d.maxV / d.comp[1].v
		switch hRatio<<4 | vRatio {
		case 0x11:
			subsampleRatio = image.YCbCrSubsampleRatio444
		case 0x12:
			subsampleRatio = image.YCbCrSubsampleRatio440
		case 0x21:
			subsampleRatio = image.YCbCrSubsampleRatio422
		case 0x22:
			subsampleRatio = image.YCbCrSubsampleRatio420
		case 0x41:
			subsampleRatio = image.YCbCrSubsampleRatio411
		case 0x42:
			subsampleRatio = image.YCbCrSubsampleRatio410
		default:
			d.flex = true
		}
	}

	d.ratio = subsampleRatio
}

// Specified in section B.2.3.
// beginScan is the first half of the standard library's processSOS: it parses
// and checks the scan header and resets the per-scan state.
func (d *decoder) beginScan(n int) error {
	if d.nComp == 0 {
		return FormatError("missing SOF marker")
	}
	if n < 6 || 4+2*d.nComp < n || n%2 != 0 {
		return FormatError("SOS has wrong length")
	}
	if err := d.readFull(d.tmp[:n]); err != nil {
		return err
	}
	nComp := int(d.tmp[0])
	if n != 4+2*nComp {
		return FormatError("SOS length inconsistent with number of components")
	}
	scan := &d.scan.comps
	*scan = [maxComponents]scanComp{}
	totalHV := 0
	for i := 0; i < nComp; i++ {
		cs := d.tmp[1+2*i] // Component selector.
		compIndex := -1
		for j, comp := range d.comp[:d.nComp] {
			if cs == comp.c {
				compIndex = j
			}
		}
		if compIndex < 0 {
			return FormatError("unknown component selector")
		}
		scan[i].compIndex = uint8(compIndex)
		// Section B.2.3 states that "the value of Cs_j shall be different from
		// the values of Cs_1 through Cs_(j-1)". Since we have previously
		// verified that a frame's component identifiers (C_i values in section
		// B.2.2) are unique, it suffices to check that the implicit indexes
		// into d.comp are unique.
		for j := 0; j < i; j++ {
			if scan[i].compIndex == scan[j].compIndex {
				return FormatError("repeated component selector")
			}
		}
		totalHV += d.comp[compIndex].h * d.comp[compIndex].v

		// The baseline t <= 1 restriction is specified in table B.3.
		scan[i].td = d.tmp[2+2*i] >> 4
		if t := scan[i].td; t > maxTh || (d.baseline && t > 1) {
			return FormatError("bad Td value")
		}
		scan[i].ta = d.tmp[2+2*i] & 0x0f
		if t := scan[i].ta; t > maxTh || (d.baseline && t > 1) {
			return FormatError("bad Ta value")
		}
	}
	// Section B.2.3 states that if there is more than one component then the
	// total H*V values in a scan must be <= 10.
	if d.nComp > 1 && totalHV > 10 {
		return FormatError("total sampling factors too large")
	}

	// zigStart and zigEnd are the spectral selection bounds.
	// ah and al are the successive approximation high and low values.
	// The spec calls these values Ss, Se, Ah and Al.
	//
	// For progressive JPEGs, these are the two more-or-less independent
	// aspects of progression. Spectral selection progression is when not
	// all of a block's 64 DCT coefficients are transmitted in one pass.
	// For example, three passes could transmit coefficient 0 (the DC
	// component), coefficients 1-5, and coefficients 6-63, in zig-zag
	// order. Successive approximation is when not all of the bits of a
	// band of coefficients are transmitted in one pass. For example,
	// three passes could transmit the 6 most significant bits, followed
	// by the second-least significant bit, followed by the least
	// significant bit.
	//
	// For sequential JPEGs, these parameters are hard-coded to 0/63/0/0, as
	// per table B.3.
	zigStart, zigEnd, ah, al := int32(0), int32(blockSize-1), uint32(0), uint32(0)
	if d.progressive {
		zigStart = int32(d.tmp[1+2*nComp])
		zigEnd = int32(d.tmp[2+2*nComp])
		ah = uint32(d.tmp[3+2*nComp] >> 4)
		al = uint32(d.tmp[3+2*nComp] & 0x0f)
		if (zigStart == 0 && zigEnd != 0) || zigStart > zigEnd || blockSize <= zigEnd {
			return FormatError("bad spectral selection bounds")
		}
		if zigStart != 0 && nComp != 1 {
			return FormatError("progressive AC coefficients for more than one component")
		}
		if ah != 0 && ah != al+1 {
			return FormatError("bad successive approximation values")
		}
	}

	// mxx and myy are the number of MCUs (Minimum Coded Units) in the image.
	// The MCU dimensions are based on the maximum sampling factors.
	// For standard subsampling, maxH/maxV equals h0/v0 (Y's factors).
	// For flex mode, Y may not have the maximum factors.
	mxx := (d.width + 8*d.maxH - 1) / (8 * d.maxH)
	myy := (d.height + 8*d.maxV - 1) / (8 * d.maxV)
	if !d.ready {
		d.setup(mxx, myy)
	}
	for i := 0; i < nComp; i++ {
		d.touched[scan[i].compIndex] = true
	}

	d.bits = bits{}
	// An end-of-band run ends with its scan, as in libjpeg; the standard
	// library would carry one that a corrupt scan leaves over into the
	// next scan.
	d.eobRun = 0
	d.scan.nComp = nComp
	d.scan.zigStart, d.scan.zigEnd, d.scan.ah, d.scan.al = zigStart, zigEnd, ah, al
	d.scan.mcu, d.scan.expectedRST = 0, uint8(rst0Marker)
	d.scan.dc = [maxComponents]int32{}
	d.scan.blockCount = 0
	return nil
}

type scanComp struct {
	compIndex uint8
	td        uint8 // DC table selector.
	ta        uint8 // AC table selector.
}

// scanState is what processSOS keeps in local variables across MCUs.
type scanState struct {
	comps            [maxComponents]scanComp
	nComp            int
	zigStart, zigEnd int32
	ah, al           uint32
	mcu              int
	expectedRST      uint8
	dc               [maxComponents]int32
	blockCount       int
}

// scanRow is the body of processSOS's MCU loop for one MCU row.
func (d *decoder) scanRow(my int) error {
	st := &d.scan
	nComp, scan := st.nComp, &st.comps
	zigStart, zigEnd, ah, al := st.zigStart, st.zigEnd, st.ah, st.al
	mxx, myy := d.mxx, d.myy
	mcu, expectedRST := st.mcu, st.expectedRST
	var (
		// b is the decoded coefficients, in natural (not zig-zag) order.
		b  block
		dc = st.dc
		// bx and by are the location of the current block, in units of 8x8
		// blocks: the third block in the first row has (bx, by) = (2, 0).
		bx, by     int
		blockCount = st.blockCount
		// In a scan of one component, nbx and nby are the block that
		// blockCount counts to, of q in a row; they move on without
		// divisions.
		nbx, nby, q int
	)
	if nComp == 1 {
		q = mxx * d.comp[scan[0].compIndex].h
		nbx, nby = blockCount%q, blockCount/q
	}
	for mx := 0; mx < mxx; mx++ {
		for i := 0; i < nComp; i++ {
			compIndex := scan[i].compIndex
			hi := d.comp[compIndex].h
			vi := d.comp[compIndex].v
			jx, jy := 0, 0 // j%hi and j/hi
			for j := 0; j < hi*vi; j++ {
				// The blocks are traversed one MCU at a time. For 4:2:0 chroma
				// subsampling, there are four Y 8x8 blocks in every 16x16 MCU.
				//
				// For a sequential 32x16 pixel image, the Y blocks visiting order is:
				//	0 1 4 5
				//	2 3 6 7
				//
				// For progressive images, the interleaved scans (those with nComp > 1)
				// are traversed as above, but non-interleaved scans are traversed left
				// to right, top to bottom:
				//	0 1 2 3
				//	4 5 6 7
				// Only DC scans (zigStart == 0) can be interleaved. AC scans must have
				// only one component.
				//
				// To further complicate matters, for non-interleaved scans, there is no
				// data for any blocks that are inside the image at the MCU level but
				// outside the image at the pixel level. For example, a 24x16 pixel 4:2:0
				// progressive image consists of two 16x16 MCUs. The interleaved scans
				// will process 8 Y blocks:
				//	0 1 4 5
				//	2 3 6 7
				// The non-interleaved scans will process only 6 Y blocks:
				//	0 1 2
				//	3 4 5
				if nComp != 1 {
					bx = hi*mx + jx
					by = vi*my + jy
					if jx++; jx == hi {
						jx, jy = 0, jy+1
					}
				} else {
					bx, by = nbx, nby
					if nbx++; nbx == q {
						nbx, nby = 0, nby+1
					}
					blockCount++
					if bx*8 >= d.width || by*8 >= d.height {
						continue
					}
				}

				// Load the previous partially decoded coefficients, if
				// applicable. bp is the block being decoded: b, or the
				// block of the MCU row when the scans are decoded in step.
				// nzm marks bp's non-zero AC coefficients, for refine.
				bp, nzm := &b, (*uint64)(nil)
				switch {
				case d.rowCoef[0] != nil:
					stride := mxx * hi
					k := (by-vi*my)*stride + bx
					if bx < 0 || bx >= stride || k < 0 || k >= len(d.rowCoef[compIndex]) {
						return FormatError("block outside the image")
					}
					bp, nzm = &d.rowCoef[compIndex][k], &d.rowNZ[compIndex][k]
					if !d.progressive {
						*bp, *nzm = block{}, 0
					}
				case d.progressive:
					if err := d.coef.load(int(compIndex), bx, by, &b, zigStart, zigEnd); err != nil {
						return err
					}
				default:
					b = block{}
				}

				if ah != 0 {
					if err := d.refine(bp, nzm, d.huff[acTable][scan[i].ta], zigStart, zigEnd, 1<<al); err != nil {
						return err
					}
				} else if !d.full {
					if err := d.decodeBlock(bp, d.huff[dcTable][scan[i].td], d.huff[acTable][scan[i].ta],
						&d.quant[d.comp[compIndex].tq], &dc[compIndex]); err != nil {
						return err
					}
				} else {
					b := bp
					zig := zigStart
					if zig == 0 {
						zig++
						// Decode the DC coefficient, as specified in section
						// F.2.2.1 (with the fast table when it can).
						dcH := d.huff[dcTable][scan[i].td]
						if d.bits.n < 32 {
							d.fillBits()
						}
						if e := dcH.fast[d.bits.a>>(64-lutBits)]; e != 0 && d.bits.n >= 32 {
							n := uint32(e) & 0xff
							d.bits.a <<= n
							d.bits.n -= int32(n)
							dc[compIndex] += e >> 16
						} else {
							value, err := d.decodeHuffman(dcH)
							if err != nil {
								return err
							}
							if value > 16 {
								return UnsupportedError("excessive DC component")
							}
							dcDelta, err := d.receiveExtend(value)
							if err != nil {
								return err
							}
							dc[compIndex] += dcDelta
						}
						b[0] = dc[compIndex] << al
					}

					if zig <= zigEnd && d.eobRun > 0 {
						d.eobRun--
					} else {
						// Decode the AC coefficients, as specified in section F.2.2.2.
						huff := d.huff[acTable][scan[i].ta]
						for ; zig <= zigEnd; zig++ {
							if d.bits.n < 16 {
								d.fillBits()
							}
							if d.bits.n >= lutBits {
								if e := huff.fast[d.bits.a>>(64-lutBits)]; e != 0 {
									if z := zig + e>>8&0x0f; z <= zigEnd {
										n := uint32(e) & 0xff
										d.bits.a <<= n
										d.bits.n -= int32(n)
										zig = z
										b[unzig[zig]] = e >> 16 << al
										if nzm != nil {
											*nzm |= 1 << zig
										}
										continue
									}
								}
							}
							value, err := d.decodeHuffman(huff)
							if err != nil {
								return err
							}
							val0 := value >> 4
							val1 := value & 0x0f
							if val1 != 0 {
								zig += int32(val0)
								if zig > zigEnd {
									break
								}
								ac, err := d.receiveExtend(val1)
								if err != nil {
									return err
								}
								b[unzig[zig]] = ac << al
								if nzm != nil {
									*nzm |= 1 << zig
								}
							} else {
								if val0 != 0x0f {
									d.eobRun = uint16(1 << val0)
									if val0 != 0 {
										bits, err := d.decodeBits(int32(val0))
										if err != nil {
											return err
										}
										d.eobRun |= uint16(bits)
									}
									d.eobRun--
									break
								}
								zig += 0x0f
							}
						}
					}
				}

				if d.full {
					// Save the coefficients; the pixels are made after the
					// last scan (reconstructBand), or after the MCU row's
					// last scan (multiscan.go).
					if bp == &b {
						if err := d.coef.save(int(compIndex), bx, by, &b, zigStart, zigEnd); err != nil {
							return err
						}
					}
					continue
				}
				if err := d.reconstructBlock(&b, bx, by-d.bandMy*vi, int(compIndex)); err != nil {
					return err
				}
			} // for j
		} // for i
		mcu++
		if d.ri > 0 && mcu%d.ri == 0 && mcu < mxx*myy {
			// For well-formed input, the RST[0-7] restart marker follows
			// immediately. For corrupt input, call findRST to try to
			// resynchronize.
			if err := d.readFull(d.tmp[:2]); err != nil {
				return err
			} else if d.tmp[0] != 0xff || d.tmp[1] != expectedRST {
				if err := d.findRST(expectedRST); err != nil {
					return err
				}
			}
			expectedRST++
			if expectedRST == rst7Marker+1 {
				expectedRST = rst0Marker
			}
			// Reset the Huffman decoder.
			d.bits = bits{}
			// Reset the DC components, as per section F.2.1.3.1.
			dc = [maxComponents]int32{}
			// Reset the progressive decoder state, as per section G.1.2.2.
			d.eobRun = 0
		}
	} // for mx
	st.mcu, st.expectedRST, st.dc, st.blockCount = mcu, expectedRST, dc, blockCount
	return nil
}

// decodeBlock decodes a block of a sequential scan whose pixels are made
// straight away (the AC loop of the standard library's processSOS for zigStart
// 0, zigEnd 63 and al 0, with the fast table). Each coefficient is multiplied
// by its quantizer as it is decoded, where the standard library multiplies in
// reconstructBlock; the products are the same. b must be zero. It sets
// d.dcOnly when the block has no AC coefficient, for reconstructBlock.
func (d *decoder) decodeBlock(b *block, dcH, acH *huffman, qt *block, dc *int32) error {
	stored := false // an AC coefficient
	if d.bits.n < 32 {
		d.fillBits()
	}
	if e := dcH.fast[d.bits.a>>(64-lutBits)]; e != 0 && d.bits.n >= 32 {
		n := uint32(e) & 0xff
		d.bits.a <<= n
		d.bits.n -= int32(n)
		*dc += e >> 16
	} else {
		value, err := d.decodeHuffman(dcH)
		if err != nil {
			return err
		}
		if value > 16 {
			return UnsupportedError("excessive DC component")
		}
		dcDelta, err := d.receiveExtend(value)
		if err != nil {
			return err
		}
		*dc += dcDelta
	}
	b[0] = *dc * qt[0]
	if d.eobRun > 0 {
		d.eobRun--
		d.dcOnly = !stored
		return nil
	}
	// The accumulator is kept in a and n, and stored back in d.bits for
	// the calls.
	a, n := d.bits.a, d.bits.n
	for zig := int32(1); zig < blockSize; zig++ {
		// With 32 bits at hand a code of up to lutBits bits and its extra
		// bits are in the accumulator; the look-ups here decode them the
		// way decodeHuffman and receiveExtend would.
		if n < 32 {
			d.bits.a, d.bits.n = a, n
			d.fillBits()
			a, n = d.bits.a, d.bits.n
		}
		if n >= 32 {
			peek := a >> (64 - lutBits)
			if e := acH.fast[peek]; e != 0 {
				if z := zig + e>>8&0x0f; z < blockSize {
					k := uint32(e) & 0xff
					a <<= k
					n -= int32(k)
					zig = z
					b[unzig[zig&63]&63] = e >> 16 * qt[zig&63]
					stored = true
					continue
				}
			} else if v := acH.lut[peek]; v != 0 {
				l := uint32(v&0xff) - 1
				run, size := int32(v>>12), uint32(v>>8)&0x0f
				switch {
				case size != 0:
					zig += run
					if zig >= blockSize {
						d.bits.a, d.bits.n = a<<l, n-int32(l)
						d.dcOnly = !stored
						return nil
					}
					x := int32(a << l >> (64 - size))
					if x < 1<<(size-1) {
						x += (-1 << size) + 1
					}
					a <<= l + size
					n -= int32(l + size)
					b[unzig[zig&63]&63] = x * qt[zig&63]
					stored = true
					continue
				case run == 0: // end of block
					d.bits.a, d.bits.n = a<<l, n-int32(l)
					d.dcOnly = !stored
					return nil
				case run == 0x0f: // sixteen zeros
					a <<= l
					n -= int32(l)
					zig += 0x0f
					continue
				}
				// An end-of-band run, which only progressive scans have:
				// the general path below.
			}
		}
		d.bits.a, d.bits.n = a, n
		value, err := d.decodeHuffman(acH)
		if err != nil {
			return err
		}
		val0 := value >> 4
		val1 := value & 0x0f
		if val1 != 0 {
			zig += int32(val0)
			if zig >= blockSize {
				d.dcOnly = !stored
				return nil
			}
			ac, err := d.receiveExtend(val1)
			if err != nil {
				return err
			}
			b[unzig[zig]] = ac * qt[zig]
			stored = true
		} else {
			if val0 != 0x0f {
				d.eobRun = uint16(1 << val0)
				if val0 != 0 {
					bits, err := d.decodeBits(int32(val0))
					if err != nil {
						return err
					}
					d.eobRun |= uint16(bits)
				}
				d.eobRun--
				d.dcOnly = !stored
				return nil
			}
			zig += 0x0f
		}
		a, n = d.bits.a, d.bits.n
	}
	d.bits.a, d.bits.n = a, n
	d.dcOnly = !stored
	return nil
}

// refine decodes a successive approximation refinement block, as specified in
// section G.1.2. nzm, if not nil, has a bit set for each non-zero coefficient
// of b (by its position in zig-zag order), which refine keeps up to date;
// otherwise refine finds them.
func (d *decoder) refine(b *block, nzm *uint64, h *huffman, zigStart, zigEnd, delta int32) error {
	// Refining a DC component is trivial.
	if zigStart == 0 {
		if zigEnd != 0 {
			panic("unreachable")
		}
		bit, err := d.decodeBit()
		if err != nil {
			return err
		}
		if bit {
			b[0] |= delta
		}
		return nil
	}

	var m uint64
	if nzm != nil {
		m = *nzm
	} else {
		for k := zigStart; k <= zigEnd; k++ {
			if b[unzig[k]] != 0 {
				m |= 1 << k
			}
		}
	}

	// Refining AC components is more complicated; see sections G.1.2.2 and
	// G.1.2.3. The accumulator is kept in a and n, as in decodeBlock; the
	// look-up and the bits read here are what decodeHuffman, decodeBits and
	// decodeBit would do.
	zig := zigStart
	a, n := d.bits.a, d.bits.n
	if d.eobRun == 0 {
	loop:
		for ; zig <= zigEnd; zig++ {
			if n < 32 {
				a, n = d.refill(a, n)
			}
			var value uint8
			if v := h.lut[a>>(64-lutBits)]; v != 0 && n >= lutBits {
				l := uint32(v&0xff) - 1
				a <<= l
				n -= int32(l)
				value = uint8(v >> 8)
			} else {
				d.bits.a, d.bits.n = a, n
				var err error
				if value, err = d.decodeHuffman(h); err != nil {
					return err
				}
				a, n = d.bits.a, d.bits.n
			}
			val0 := int32(value >> 4)
			z := int32(0)
			switch value & 0x0f {
			case 0:
				if val0 != 0x0f {
					d.eobRun = uint16(1 << val0)
					if val0 != 0 {
						if n < val0 {
							if a, n = d.refill(a, n); n < val0 {
								return d.bits.end
							}
						}
						d.eobRun |= uint16(a >> (64 - val0))
						a <<= val0
						n -= val0
					}
					break loop
				}
			case 1:
				if n == 0 {
					if a, n = d.refill(a, n); n == 0 {
						return d.bits.end
					}
				}
				z = delta
				if a>>63 == 0 {
					z = -z
				}
				a <<= 1
				n--
			default:
				d.bits.a, d.bits.n = a, n
				return FormatError("unexpected Huffman code")
			}

			// The standard library's refineNonZeroes, here for a walk that
			// stops at the zero entry after val0 others: it refines the
			// non-zero entries before that one.
			in := ^uint64(0) >> (63 - zigEnd) &^ (uint64(1)<<zig - 1)
			end, nz := zigEnd+1, val0
			for zr := ^m & in; zr != 0; zr &= zr - 1 {
				if nz == 0 {
					end = int32(mbits.TrailingZeros64(zr))
					break
				}
				nz--
			}
			x := m & in
			if end < 64 {
				x &= uint64(1)<<end - 1
			}
			for ; x != 0; x &= x - 1 {
				if n == 0 {
					if a, n = d.refill(a, n); n == 0 {
						return d.bits.end
					}
				}
				bit := a >> 63
				a <<= 1
				n--
				if bit != 0 {
					u := unzig[mbits.TrailingZeros64(x)]
					if b[u] >= 0 {
						b[u] += delta
					} else {
						b[u] -= delta
					}
				}
			}
			zig = end
			if zig > zigEnd {
				d.bits.a, d.bits.n = a, n
				return FormatError("too many coefficients")
			}
			if z != 0 {
				b[unzig[zig]] = z
				m |= 1 << zig
			}
		}
	}
	if d.eobRun > 0 {
		d.eobRun--
		var err error
		if a, n, err = d.refineNonZeroes(b, m, a, n, zig, zigEnd, delta); err != nil {
			return err
		}
	}
	d.bits.a, d.bits.n = a, n
	if nzm != nil {
		*nzm = m
	}
	return nil
}

// refill stores the accumulator a, n in d.bits, tops it up and returns it.
func (d *decoder) refill(a uint64, n int32) (uint64, int32) {
	d.bits.a, d.bits.n = a, n
	d.fillBits()
	return d.bits.a, d.bits.n
}

// refineNonZeroes refines every non-zero entry of b from zig to zigEnd, in
// zig-zag order (a block in an end-of-band run), reading the correction bits
// from the accumulator a, n, which it returns. m has a bit set for each
// non-zero entry; the standard library looks at the entries one by one,
// which finds the same ones.
func (d *decoder) refineNonZeroes(b *block, m, a uint64, n, zig, zigEnd, delta int32) (uint64, int32, error) {
	if zig > zigEnd {
		return a, n, nil
	}
	for x := m & (^uint64(0) >> (63 - zigEnd) &^ (uint64(1)<<zig - 1)); x != 0; x &= x - 1 {
		if n == 0 {
			if a, n = d.refill(a, n); n == 0 {
				return a, n, d.bits.end
			}
		}
		bit := a >> 63
		a <<= 1
		n--
		if bit != 0 {
			u := unzig[mbits.TrailingZeros64(x)]
			if b[u] >= 0 {
				b[u] += delta
			} else {
				b[u] -= delta
			}
		}
	}
	return a, n, nil
}

// dequantize multiplies the coefficients of b by their quantizers.
func (d *decoder) dequantize(b *block, compIndex int) {
	qt := &d.quant[d.comp[compIndex].tq]
	for zig := 0; zig < blockSize; zig++ {
		b[unzig[zig]] *= qt[zig]
	}
}

// dequantizeNZ is dequantize for a block whose non-zero AC coefficients
// are marked in nz (by their positions in zig-zag order): the others stay
// zero.
func (d *decoder) dequantizeNZ(b *block, nz uint64, compIndex int) {
	qt := &d.quant[d.comp[compIndex].tq]
	b[0] *= qt[0]
	for x := nz &^ 1; x != 0; x &= x - 1 {
		k := mbits.TrailingZeros64(x)
		b[unzig[k]] *= qt[k]
	}
}

// reconstructBlock performs the inverse DCT of the dequantized b and stores
// the block to the image. Here the image is the current band, and by is
// relative to the band's first block row.
func (d *decoder) reconstructBlock(b *block, bx, by, compIndex int) error {
	var h, v int
	if d.flex {
		// Flex mode: scale bx and by according to the component's sampling factors.
		h = d.comp[compIndex].expandH
		v = d.comp[compIndex].expandV
		bx, by = bx*h, by*v
	}

	// bs is the side of the block in pixels.
	bs := 8 >> d.scale
	dst, stride := []byte(nil), 0
	if d.nComp == 1 {
		dst, stride = d.img1.Pix[bs*(by*d.img1.Stride+bx):], d.img1.Stride
	} else {
		switch compIndex {
		case 0:
			dst, stride = d.img3.Y[bs*(by*d.img3.YStride+bx):], d.img3.YStride
		case 1:
			dst, stride = d.img3.Cb[bs*(by*d.img3.CStride+bx):], d.img3.CStride
		case 2:
			dst, stride = d.img3.Cr[bs*(by*d.img3.CStride+bx):], d.img3.CStride
		case 3:
			dst, stride = d.blackPix[bs*(by*d.blackStride+bx):], d.blackStride
		default:
			return UnsupportedError("too many components")
		}
	}

	if d.flex {
		// Flex mode: expand each source pixel to h×v destination pixels.
		idct(b)
		for y := 0; y < 8; y++ {
			y8 := y * 8
			yv := y * v
			for x := 0; x < 8; x++ {
				val := uint8(max(0, min(255, b[y8+x]+128)))
				xh := x * h
				for yy := 0; yy < v; yy++ {
					for xx := 0; xx < h; xx++ {
						dst[(yv+yy)*stride+xh+xx] = val
					}
				}
			}
		}
		return nil
	}

	if d.dcOnly {
		dcStore(b[0], dst, stride, d.scale)
		return nil
	}
	switch d.scale {
	case 0:
		idctStore(b, dst, stride)
	case 1:
		idct4Store(b, dst, stride)
	case 2:
		idct2Store(b, dst, stride)
	default:
		idct1Store(b, dst)
	}
	return nil
}

// findRST advances past the next RST restart marker that matches expectedRST.
// Other than I/O errors, it is also an error if we encounter an {0xFF, M}
// two-byte marker sequence where M is not 0x00, 0xFF or the expectedRST.
//
// This is similar to libjpeg's jdmarker.c's next_marker function.
// https://github.com/libjpeg-turbo/libjpeg-turbo/blob/2dfe6c0fe9e18671105e94f7cbf044d4a1d157e6/jdmarker.c#L892-L935
//
// Precondition: d.tmp[:2] holds the next two bytes of JPEG-encoded input
// (input in the d.readFull sense).
func (d *decoder) findRST(expectedRST uint8) error {
	for {
		// i is the index such that, at the bottom of the loop, we read 2-i
		// bytes into d.tmp[i:2], maintaining the invariant that d.tmp[:2]
		// holds the next two bytes of JPEG-encoded input. It is either 0 or 1,
		// so that each iteration advances by 1 or 2 bytes (or returns).
		i := 0

		if d.tmp[0] == 0xff {
			if d.tmp[1] == expectedRST {
				return nil
			} else if d.tmp[1] == 0xff {
				i = 1
			} else if d.tmp[1] != 0x00 {
				// libjpeg's jdmarker.c's jpeg_resync_to_restart does something
				// fancy here, treating RST markers within two (modulo 8) of
				// expectedRST differently from RST markers that are 'more
				// distant'. Until we see evidence that recovering from such
				// cases is frequent enough to be worth the complexity, we take
				// a simpler approach for now. Any marker that's not 0x00, 0xff
				// or expectedRST is a fatal FormatError.
				return FormatError("bad RST marker")
			}

		} else if d.tmp[1] == 0xff {
			d.tmp[0] = 0xff
			i = 1
		}

		if err := d.readFull(d.tmp[i:2]); err != nil {
			return err
		}
	}
}
