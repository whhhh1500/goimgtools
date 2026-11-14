# Third-party code and acknowledgements

goimgtools is released under the MIT License (see `LICENSE`). Parts of it
come from, or follow, other projects. This file says which parts, and under
what terms.

## Go standard library and golang.org/x/image

BSD 3-Clause License, copyright The Go Authors. The full text is in
`LICENSE-GO`. The files concerned carry the Go Authors' copyright line.

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

Both licences ask that their notices accompany redistributed source. Their
full texts are the files `README.ijg` and `LICENSE.md` of libjpeg-turbo
(https://github.com/libjpeg-turbo/libjpeg-turbo). **Before this repository
is published, copy those two files here unchanged**; they are not yet
included.

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
- **libvips / sharp**: the chained interface of the package follows sharp.
