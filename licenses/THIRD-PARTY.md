# Third-party code and acknowledgements

goimgtools is released under the MIT License (see `LICENSE` in the top
directory). Parts of it
come from, or follow, other projects. This file says which parts, and under
what terms.

## Go standard library and golang.org/x/image

BSD 3-Clause License, copyright The Go Authors. The full text is in
`LICENSE-GO`, next to this file. The files concerned carry the Go Authors' copyright line.

The decoders and encoders below are the standard ones, changed to work a few
rows at a time instead of on a whole image; the pixel arithmetic is theirs.

| Here | From |
|---|---|
| `internal/pngstream` | `image/png` |
| `internal/jpegstream` (reader, baseline writer, DCT, Huffman tables) | `image/jpeg` |
| `internal/gifstream` (reader, writer) | `image/gif` |
| `internal/bmpstream` | `golang.org/x/image/bmp` |
| `internal/tiffstream`, `internal/tiffstream/lzw` | `golang.org/x/image/tiff` |
| `internal/webpstream`, `internal/webpstream/vp8`, `internal/webpstream/vp8l` | `golang.org/x/image/webp`, `vp8`, `vp8l` |
| `internal/resize/resize.go` (weights and per-pixel arithmetic) | `golang.org/x/image/draw` |
| `internal/vp8enc/tables.go` (tables fixed by the VP8 format) | `golang.org/x/image/vp8` |

## Independent JPEG Group's library and libjpeg-turbo

This software is based in part on the work of the Independent JPEG Group.

Two files contain Go ports of routines of the Independent JPEG Group's JPEG
library as it is kept in libjpeg-turbo:

| Here | Ported from |
|---|---|
| `internal/jpegstream/writer_opt.go`: `optimalSpec` | `jchuff.c`: `jpeg_gen_optimal_table` |
| `internal/jpegstream/writer_prog.go`: the progressive scan script and the coding of a progressive scan | `jcparam.c`: `jpeg_simple_progression`; `jcphuff.c` |

Those sources are copyright (C) 1991-1997 Thomas G. Lane (and, for the
library as a whole, Thomas G. Lane and Guido Vollbeding), with libjpeg-turbo
modifications copyright D. R. Commander and others. They are distributed
under the IJG licence and, for the libjpeg-turbo modifications, a BSD-style
licence. The ports are translations into Go: the routines were rewritten to
work on this package's data structures, and nothing of the original files is
included unchanged.

Both licences ask that their notices accompany redistributed source. The
file `LICENSE-LIBJPEG-TURBO`, next to this one, holds them as libjpeg-turbo 3.1.3 itself
distributes them: the legal section of its `README.ijg` (the IJG licence)
and its `LICENSE.md` (which includes the Modified BSD licence), unchanged.

The IJG licence asks, for source derived from the library, that the whole
`README.ijg` file be included, not only its legal section. **That file is
not here yet**: it is to be copied unchanged from libjpeg-turbo
(https://github.com/libjpeg-turbo/libjpeg-turbo) into this directory.

## mozjpeg

`internal/jpegstream/writer_trellis.go` is a Go port of the trellis
quantization of mozjpeg (`jcdctmgr.c`: `quantize_trellis`, the AC and the
DC trellis, with mozjpeg's default parameters from `jcparam.c`), copyright
(C) 2014 Mozilla Corporation, in a file that is otherwise libjpeg-turbo's
and the Independent JPEG Group's. mozjpeg is distributed under the same
licences as libjpeg-turbo (the IJG licence and the Modified BSD licence;
see the section above and `LICENSE-LIBJPEG-TURBO`). The port works on this
package's blocks and reads its bit costs from the standard Huffman tables;
nothing of the original file is included unchanged.

## Pillow

`internal/ops/blur.go` contains Go ports of two routines of Pillow 12.1.1,
so that the results are Pillow's to the byte:

| Here | Ported from |
|---|---|
| `boxRadius`, `newBox`, `hbox`, `vbox`: the Gaussian blur as three box blurs of a fractional radius, in Pillow's fixed-point arithmetic | `src/libImaging/BoxBlur.c` |
| `Sharpen` with a sigma: the unsharp mask on top of that blur | `src/libImaging/UnsharpMask.c` (by Kevin Cazabon, donated to PIL in 2009) |

The PIL is copyright Secret Labs AB and Fredrik Lundh and contributors,
Pillow copyright Jeffrey A. Clark and contributors, under the MIT-CMU
licence. `LICENSE-PILLOW`, next to this file, holds the notice as Pillow
12.1.1 distributes it, unchanged. The routines were rewritten in Go to work
on this package's rows (a few at a time instead of a whole image); nothing
of the original files is included unchanged.

The reference images in `tests/testdata/pillow` were made with Pillow from
`video-001.png` of the Go image test data.

## Acknowledgements (no code taken)

- **libwebp** (Google): the WebP encoders in `internal/vp8enc` and
  `internal/vp8lenc` are written from the format specifications (RFC 6386,
  RFC 9649). They use libwebp's colour conversion constants and its mapping
  from quality to quantizer, so that files look the same in either decoder,
  and were checked against libwebp's output.
- **pngquant / libimagequant**: the colour quantizer in `internal/quant`
  uses ideas described for pngquant (working on premultiplied colours,
  weighting by how visible an error would be, a dither map). No code of
  either was used.
- **libvips / sharp**: the chained interface of the package follows sharp;
  so do the mild sharpen kernel, `Modulate` working in LCh, the limits of
  sigma and the default threshold of `Trim`.
