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
| `internal/gflate` (the compressor, as it is; `ResetDict` and the loads of `load_store_unsafe.go` added) | `compress/flate` of Go 1.27 |

## Independent JPEG Group's library and libjpeg-turbo

This software is based in part on the work of the Independent JPEG Group.

These files contain Go ports of routines of the Independent JPEG Group's
JPEG library as it is kept in libjpeg-turbo:

| Here | Ported from |
|---|---|
| `internal/jpegstream/islow.go` | `jidctint.c`: `jpeg_idct_islow` |
| `internal/jpegstream/idctred.go` | `jidctred.c`: `jpeg_idct_4x4`, `jpeg_idct_2x2`, `jpeg_idct_1x1` |
| `internal/jpegstream/ycc.go` | `jdsample.c` (fancy upsampling), `jdcolor.c` (YCbCr to RGB) |
| `internal/jpegstream/idct_amd64.s`, `idct4_amd64.s`, `ycc_amd64.s`, `fdct_amd64.s`, `rgb_amd64.s` (AVX2), `idct_arm64.s`, `jpeg_arm64.s` (NEON) | the same routines, as `islow.go`, `idctred.go`, `ycc.go`, `fdct.go` and `writer.go` compute them, after the approach of libjpeg-turbo's SIMD code (`simd/x86_64`) |
| `internal/jpegstream/fdct.go` | `jfdctint.c`: `jpeg_fdct_islow` |
| `internal/jpegstream/writer.go`: `ycc`, `scale`, `scaleH`, `padRows`, the dummy blocks past the edges, the markers | `jccolor.c`: `rgb_ycc_convert`; `jcsample.c`: `h2v2_downsample`, `h2v1_downsample`; `jcprepct.c`: `expand_bottom_edge`; `jccoefct.c`: `compress_data`; `jcmarker.c` |
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

`internal/jpegstream/writer_progsearch.go` follows mozjpeg's search for
the progressive scan script (`jcparam.c`: `jpeg_search_progression`, the
candidate scans; `jcmaster.c`: `select_scans`, how the bit depth and the
band split are chosen and the order of the chosen scans), under the same
licences. It was rewritten for this package, which prices a candidate by
counting its symbols rather than encoding it; nothing of the original
files is included unchanged.

`internal/jpegstream/qtables.go` holds the nine quantization tables of
mozjpeg's `jcparam.c` (`std_luminance_quant_tbl`,
`std_chrominance_quant_tbl`), generated from that file, under the same
licences.

## jpegli

BSD 3-Clause License, copyright (c) the JPEG XL Project Authors, with an
additional patent grant by Google. Both texts, as github.com/google/jpegli
distributes them (commit 031a007), are in `LICENSE-JPEGLI`, next to this
file.

`internal/jpegstream/qtables_jpegli.go` makes the quantization tables of
`QuantTableJpegli` as jpegli makes them (`lib/jpegli/quant.cc`:
`SetQuantMatrices`, `DistanceToScale`, the base tables
`kBaseQuantMatrixYCbCr`, `k420Rescale` and `kZeroBiasOffsetYCbCrAC`;
`lib/jpegli/encode.cc`: `jpegli_quality_to_distance`), and applies the
dead zone of `ReQuantizeBlock` (`lib/jpegli/encode_finish.cc`) without the
adaptive part. The tables are copied from those files; the code was
rewritten in Go, and the tables it makes were checked against the ones
cjpegli writes.

## libdeflate

MIT License, copyright 2016 Eric Biggers and 2024 Google LLC. The full
text, as libdeflate distributes it (commit c7e5386), is in
`LICENSE-LIBDEFLATE`, next to this file.

`internal/deflate` is a Go port of libdeflate's near-optimal parsing, the
compressor of its levels 10 to 12 (`lib/deflate_compress.c`:
`deflate_compress_near_optimal` and what it calls, the block splitting,
the Huffman code construction and the block output;
`lib/find_min_cost_path_template.h`; `lib/bt_matchfinder.h`;
`lib/matchfinder_common.h`), with the tables of `deflate_compress.c`
generated from that file. The code was rewritten in Go; what was added is
a dictionary (the data before) and ending with a sync flush instead of a
final block, so that a stream can be compressed in pieces. Without a
dictionary, with libdeflate's settings, its output is byte for byte
libdeflate's (checked at levels 10, 11 and 12).

`internal/deflate` also ports libdeflate's lazy parsing, the compressor of
its levels 5 to 9 (`lib/deflate_compress.c`:
`deflate_compress_lazy_generic`, the minimum match length heuristics;
`lib/hc_matchfinder.h`, with its multi-hop links), rewritten in Go the same
way. Without a dictionary its output is byte for byte libdeflate's at
levels 5 to 9 (checked on photographs, screenshots, paletted images, text,
random data and runs, from 18 bytes to 72MB; libdeflate stores inputs of
up to 55 bytes, which this port compresses).

`internal/inflate` decompresses with the method of libdeflate's
decompressor (`lib/deflate_decompress.c`, `lib/decompress_template.h`): a
64-bit bit buffer refilled without a branch, decode tables whose entries
hold a symbol's extra bits count and base value, subtables for long codes,
and a fast loop that looks up the next code before refilling and copies
matches eight bytes at a time. It was written afresh in Go, with an entry
layout of its own; no code of libdeflate is included. The table sizes are
those of zlib's `enough` program, as libdeflate's.

## zopfli

Apache License 2.0, copyright 2011 Google Inc. The full text, as
github.com/google/zopfli distributes it (commit df1517d), is in
`LICENSE-ZOPFLI`, next to this file; zopfli has no NOTICE file.

`internal/deflate/entropy.go` prices the symbols of a parse at their
entropy under the last pass's frequencies, as zopfli's cost model does
(`src/zopfli/squeeze.c`: `GetCostStat`, `CalculateStatistics`), in fixed
point.

`internal/deflate/split.go` splits the blocks of deflate as zopfli does
(`src/zopfli/blocksplitter.c`: `ZopfliBlockSplitLZ77`, `FindMinimum`,
`FindLargestSplittableBlock`; the size of a part estimated as
`ZopfliCalculateBlockSizeAutoType` does, the least of a dynamic, a static
and a stored block): the largest part that may still gain is split where
its two halves cost least, found by sampling nine points and narrowing
down, up to fifteen parts. It works on this package's parse instead of
zopfli's LZ77 store, by byte position instead of by symbol; the code was
rewritten in Go and nothing of the original files is included unchanged.
`internal/deflate/block.go` (`evenForRLE`) is a port of zopfli's
`OptimizeHuffmanForRle` (`src/zopfli/deflate.c`), used as zopfli's
`TryOptimizeHuffmanForRle` uses it, under the same licence.

`internal/zopfli` is a port of zopfli's compressor, under the same
licence: `src/zopfli/katajainen.c`, `tree.c`, `hash.c`, `cache.c`,
`lz77.c`, `squeeze.c`, `blocksplitter.c` and `deflate.c`, translated into
Go. Given the same data and options it writes the bytes zopfli writes (the
tests check this against zopfli's own output). It makes its memory once,
for the longest data it is given, rather than as it goes, and keeps less
of it for each byte: the parse of each run of the optimal parse is
counted rather than kept, the best run made again at the end; the
symbols of a parse are found again from its lengths and distances; the
match cache keeps all the lengths of a position, one position after the
other, rather than 8 in a slot of each, so that a position is searched
only once (in the first run of the optimal parse) where Zopfli searches
again at the positions with more each time; and the costs of the lengths
and distances are looked up once a run rather than for each length of
each match, and added in Zopfli's order. It adds
compressing a block after a dictionary with its bits left open, so that
blocks compressed on several cores join into zopfli's stream.

## ECT (Efficient Compression Tool)

Apache License 2.0, copyright 2011 Google Inc. (the zopfli it builds on),
with modifications by Felix Hanau. The full text, as
github.com/fhanau/Efficient-Compression-Tool distributes it
(`License.txt`), is in `LICENSE-ECT`, next to this file.

`internal/zopfli` adds, as options (`Ultra`, `MoreCodes`), what ECT's fork
of zopfli does on top of it (`src/zopfli/squeeze.c`: the "ultra" runs of
the optimal parse priced by the code lengths of the best parse;
`src/zopfli/deflate.cpp`: `GetAdvancedLengths`, `EncodeTree` with its
fused repeats of 7 and 8, `ReplaceBadCodes`), rewritten in Go on the
port's own structures; nothing of the original files is included
unchanged. The order of the first appearance of the colours that
`internal/pngstream` gives a palette at level 10 is the one ECT's lodepng
gives it.

## Brotli

MIT License, copyright (c) 2009, 2010, 2013-2016 by the Brotli Authors. The
full text is in `LICENSE-BROTLI`, next to this file.

`internal/zopfli/blocksize.go` (`brotliEvenForRle`) is a port of Brotli's
`OptimizeHuffmanCountsForRle`, as ECT carries it (`src/zopfli/deflate.cpp`);
its tests check it against that C code.

## oxipng

MIT License, copyright (c) 2016 Joshua Holmer. The full text is in
`LICENSE-OXIPNG`, next to this file.

`internal/pngstream/brute.go` chooses the filter of each row of a PNG as
oxipng's Brute strategy does (`src/filters/strategies.rs`:
`BruteEvaluator`, with `src/png/mod.rs`: `filter_image`): every filter is
tried, and the one with which the last rows compress smallest wins. oxipng
compresses the rows with libdeflate for every filter; this package
estimates the size of the row alone after the rows before it instead (see
the comment of `bruteFilter`). No code of oxipng is included.

## Pillow

`internal/ops` contains Go ports of routines of Pillow 12.1.1, so that the
results are Pillow's to the byte:

| Here | Ported from |
|---|---|
| `blur.go`: `boxRadius`, `newBox`, `hbox`, `vbox`: the Gaussian blur (and `BoxBlur`) as box blurs of a fractional radius, in Pillow's fixed-point arithmetic | `src/libImaging/BoxBlur.c` |
| `blur.go`: `Sharpen` with a sigma: the unsharp mask on top of that blur | `src/libImaging/UnsharpMask.c` (by Kevin Cazabon, donated to PIL in 2009) |
| `pillowrotate.go`: `TransformPillow`, `AffinePillow`: the affine, perspective and quad transforms behind `Image.transform` and `Image.rotate`, their nearest, bilinear and bicubic filters | `src/libImaging/Geometry.c` |
| `pillowfilter.go`: `PillowKernel`: the 3 x 3 and 5 x 5 kernels of `ImageFilter` | `src/libImaging/Filter.c` |

The PIL is copyright Secret Labs AB and Fredrik Lundh and contributors,
Pillow copyright Jeffrey A. Clark and contributors, under the MIT-CMU
licence. `LICENSE-PILLOW`, next to this file, holds the notice as Pillow
12.1.1 distributes it, unchanged. The routines were rewritten in Go to work
on this package's rows (a few at a time instead of a whole image); nothing
of the original files is included unchanged.

The reference images in `tests/testdata/pillow` were made with Pillow from
`video-001.png` of the Go image test data.

## libwebp

BSD 3-Clause License, copyright (c) 2010 Google Inc., with an additional
patent grant by Google. Both texts, as libwebp 1.6.0 distributes them
(`COPYING` and `PATENTS`), are in `LICENSE-LIBWEBP`, next to this file.

`internal/webpstream/yuv_amd64.s` and `internal/webpstream/vp8/filter_amd64.s`
compute libwebp's upsampling, conversion of colour and loop filter (as the Go
code of those packages computes them) after the approach of its SSE2 code
(`src/dsp/upsampling_sse2.c`, `yuv_sse2.h`, `dec_sse2.c`);
`internal/webpstream/vp8l/pred_amd64.s` undoes the lossless predictors as
`lossless_sse2.c` does (`PredictorAdd0_SSE2` to `PredictorAdd12_SSE2`);
`internal/webpstream/vp8/lf_amd64.s` is the decoder's loop filter as
`dec_sse2.c` has it (`VFilter16`, `HFilter16`, `VFilter16i`, `HFilter16i` and
the 8-pixel ones, with their helpers), translated to assembly;
`internal/vp8enc/ssim_amd64.s` the window sums of its SSIM (`ssim_sse2.c`);
`internal/vp8enc/dct_amd64.s` the encoder's 4x4 forward transform after its
`FTransform_SSE2` (`enc_sse2.c`), with the quantization, squared error and
coefficient histogram as the Go code computes them, and
`internal/webpstream/vp8/idct_amd64.s` the inverse transform of decoder and
encoder, and `dc_amd64.s` that of a block with only its DC coefficient; the
files of the same names ending in `_arm64.s` do the same with NEON.
`internal/webpstream/vp8/parse_amd64.s` and `parse_arm64.s` read the
coefficients of a block as libwebp's `GetCoeffsFast` (`src/dec/vp8_dec.c`)
does, with its `VP8GetBit` and `VP8GetSigned` (`src/utils/bit_reader_inl_utils.h`),
returning, as it does, the position after the last coefficient.

`internal/vp8enc/analysis.go` follows the segmentation of libwebp's lossy
encoder (`src/enc/analysis_enc.c`, and `VP8SetSegmentParams` and
`SimplifySegments` in `src/enc/quant_enc.c`): how a macroblock's
susceptibility is measured from the histogram of its DCT coefficients
under the DC and TrueMotion predictions, the clustering of the
susceptibilities into four segments, and the mapping of a segment to its
quantizer ("spatial noise shaping", at libwebp's default strength), its
filter level following the segment's beta, and the merging of segments that
come out alike. It was rewritten in Go for this encoder (which converts and
measures the image a macroblock row at a time, on several goroutines);
nothing of the original files is included unchanged.

`internal/vp8enc/sharp.go` is a Go port of libwebp's sharp YUV conversion
(`sharpyuv/sharpyuv.c` and `sharpyuv_dsp.c`: the luma and chroma corrected,
pass after pass, by what the decoder's chroma interpolation gives back,
with libwebp's conversion matrix), and `sharp_tables.go` holds the gamma
tables of `sharpyuv/sharpyuv_gamma.c`, generated by printing them. The
passes are made over pairs of rows as they arrive, instead of over the
whole image one after the other, and always four of them; with four
passes every sample is libwebp's.

`internal/vp8enc/autofilter.go` chooses the loop filter level of each
segment as libwebp's autofilter does (`src/enc/filter_enc.c`:
`VP8StoreFilterStats`, `VP8AdjustFilterStrength`), with the SSIM of
`src/dsp/ssim.c` (`SSIMCalculation`) and the inner-edge loop filter of
`src/dsp/dec.c`; the sums of the SSIM windows are computed differently
(separably), to the same values. `internal/vp8enc/trellis.go` is a port
of `TrellisQuantizeBlock` (`src/enc/quant_enc.c`), used for 4x4 blocks
only, and `internal/vp8enc/dcdiffuse.go` of its chroma DC error diffusion
(`CorrectDCValues`, `QuantizeSingle`, `StoreDiffusionErrors`). All were
rewritten in Go for this encoder; nothing of the original files is
included unchanged.

`internal/vp8enc/alphall.go` codes the alpha plane of a lossy file as
libwebp's `src/enc/alpha_enc.c` does: through the filters of
`src/dsp/filters.c`, with the estimate of the best one of
`src/utils/filters_utils.c` (`WebPEstimateBestFilter`), as the green
channel of a lossless image. `cleanTransparent` in
`internal/vp8enc/convert.go` makes the samples of transparent pixels cheap
as `WebPCleanupTransparentArea` does (`src/enc/picture_tools_enc.c`). Both
were rewritten in Go; nothing of the original files is included unchanged.

`internal/iccprof/lut.go` interpolates the lookup tables of colour
profiles as LittleCMS (MIT licence) does: its tetrahedral interpolation
(`cmsintrp.c`, `TetrahedralInterpFloat`), in four dimensions between two
of them (`Eval4Inputs`), on grids of the sizes it picks
(`_cmsReasonableGridpointsByColorspace`), and compensates the black point
as it does (`cmssamp.c`, `cmsDetectBlackPoint`; `cmscnvrt.c`,
`ComputeBlackPointCompensation`). The routines were written anew; no code
of LittleCMS is included.

`internal/webpstream/fancy.go` turns decoded Y'CbCr into RGB as libwebp
does (`src/dsp/upsampling.c`: the "fancy" chroma upsampler;
`src/dsp/yuv.h`: the integer conversion), so that decoded pixels are
libwebp's to the byte; it was rewritten in Go to work on strips.
`internal/webpstream/rescale.go` is a Go port of libwebp's rescaler
(`src/utils/rescaler_utils.c` and the plain C of `src/dsp/rescaler.c`) and
of its premultiplication by alpha (`WebPMultARGBRow`,
`src/dsp/alpha_processing.c`); `scaled.go` decodes scaled as
`src/dec/io_dec.c` and `src/dec/vp8l_dec.c` do.

`internal/vp8lenc/optimal.go` follows the idea of libwebp's lossless
`TraceBackwards` (`src/enc/backward_references_cost_enc.c`): the LZ77
matches are chosen by the cheapest path through the pixels under a cost
model made from the statistics, with literals weighted at 82% and colour
cache hits at 68% of what the statistics price them at, as libwebp weighs
them. The path is worked out differently (backward, over a stretch of the
streamed pixels at a time, without libwebp's interval cost manager); no
code of libwebp is included. Offering, besides the hash chain's match, the
longest one at the nearest distances with the cheapest codes is the idea
of libwebp's `BackwardReferencesLz77Box` (the same file's
`backward_references_enc.c`), done here at every pixel of the parse.

`internal/vp8lenc/crosscolor.go` is libwebp's cross-color transform
(`src/enc/predictor_enc.c`: `VP8LColorSpaceTransform`,
`GetBestColorTransformForTile`, `GetBestGreenToRed`,
`GetBestGreenRedToBlue`, `PredictionCostCrossColor`; `src/dsp/lossless_enc.c`:
`VP8LTransformColor`), rewritten in Go with its costs in fixed point.

`processRLE` in `internal/vp8lenc/lz77.go` takes matches only at distance 1
and a row up, greedily, as libwebp's `BackwardReferencesRle`
(`src/enc/backward_references_enc.c`) does.

`internal/vp8lenc/rle.go` evens out the counts of a prefix code before
its lengths are made, as libwebp's `OptimizeHuffmanForRle`
(`src/utils/huffman_encode_utils.c`) does, and `writeTree` in
`internal/vp8lenc/huffman.go` leaves out the zero lengths at the end of a
code as libwebp's `StoreFullHuffmanCode` (`src/enc/vp8l_enc.c`) does.
Both were rewritten in Go; here each is used only when it makes the code
smaller.

The rest of the WebP encoders in `internal/vp8enc` and `internal/vp8lenc`
is written from the format specifications (RFC 6386, RFC 9649). It uses
libwebp's colour conversion constants and its mapping from quality to
quantizer, so that files look the same in either decoder, and was checked
against libwebp's output.

`internal/webpanim` writes animated WebP files from the container
specification. How a GIF's frame delays and loop count become a WebP's
(10ms or less shown as 100ms; a finite loop count plus one; no looping
extension as one play) follows libwebp's `examples/gif2webp.c`; no code
of it is included.

`blendNonPremult` in `animate.go` blends a frame of an animated WebP onto
the picture as libwebp's animation decoder does (`src/demux/anim_decode.c`:
`BlendPixelNonPremult`, `BlendChannelNonPremult`), with its integer
arithmetic, so that the frames come out as libwebp draws them; it was
rewritten in Go.

## libvips

`CLAHE` (`internal/ops/window.go`) gives the results of libvips' local
histogram equalization (`libvips/histogram/hist_local.c`, LGPL 2.1), whose
method it follows: a window's histogram moved a column at a time, counts
cut at the slope and what is cut spread over the levels, the image
mirrored past its edges. It was written afresh in Go (the cut counts are
kept in blocks, and what is cut off follows from them); no code of libvips
is included. The smart crops of `Resize` (`internal/ops/smartcrop.go`)
follow libvips' smartcrop (`libvips/conversion/smartcrop.c`): its entropy
slicing, and its attention scores (whose skin tone vector comes from
smartcrop.js by Jonas Wagner, MIT licence), written afresh in Go. The blend modes of `Composite` (`internal/ops/blend.go`) and
`Normalize`'s percentiles (`internal/ops/levels.go`) reproduce libvips'
arithmetic as observed through sharp and the vips command, without its
code.

`internal/iccprof/chemical-proof.icc` is the CMYK profile libvips ships
(`libvips/colour/profiles/cmyk.icm`) and takes a CMYK image without a
profile to be in, cut down to its description, copyright, white point and
perceptual table (AToB0). Its copyright tag reads: "Created by Graeme W.
Gill. Released into the public domain. No Warranty, Use at your own risk."

## apngdis

`blendOver` in `animate.go` composites a frame of an APNG over the picture
with the integer arithmetic of apngdis (by Max Stepin, the author of APNG;
zlib licence), rewritten in Go; nothing of its files is included.

## The HEIC module (`heic/`)

The module `github.com/whhhh1500/goimgtools/heic` carries the notices of
what it includes in its own `licenses/` directory, which goes with it:

- **Kvazaar** (BSD 3-clause licence, copyright Tampere University,
  ITU/ISO/IEC and the project's contributors; `LICENSE-KVAZAAR`): the HEVC
  encoder, at commit c997f08 of github.com/ultravideo/kvazaar with the
  changes of `internal/kvazaar/build/kvazaar.patch` (no threads, and an
  option for a QP offset of Cb and Cr and one for the weight of their
  distortion), compiled to WebAssembly by wasi-sdk with the entry points
  of `internal/kvazaar/build/shim.c` and translated to Go by wasm2go
  (github.com/ncruces/wasm2go): `internal/kvazaar/module.go` is that
  translation, which `internal/kvazaar/build/build.sh` makes again. It
  includes parts of the C library of wasi-sdk (wasi-libc, with musl and
  cloudlibc; MIT and BSD 2-clause licences, `LICENSE-WASI-LIBC`).
- **gen2brain/h265** (MIT licence, copyright roticv and Karpeles Lab Inc.;
  `LICENSE-H265`): the module decodes HEIC with it (a dependency), and the
  writing of the HEIF boxes in `container.go` is taken from its
  `heic/encode.go` and `heic/gridenc.go`, changed.

## The AVIF module (`avif/`)

The module `github.com/whhhh1500/goimgtools/avif` carries the notices of
what it includes in its own `licenses/` directory, which goes with it:
libavif 1.4.2 (BSD 2-clause, `LICENSE-LIBAVIF`, which also lists the notices
of code libavif itself includes), aom 3.14.1 (BSD 2-clause and the Alliance
for Open Media patent licence, `LICENSE-AOM`, `PATENTS-AOM`), dav1d 1.5.3
(BSD 2-clause, `LICENSE-DAV1D`), libyuv (BSD 3-clause and its patent grant,
`LICENSE-LIBYUV`, `PATENTS-LIBYUV`) and parts of wasi-libc
(`LICENSE-WASI-LIBC`), compiled to WebAssembly with the entry points of
`internal/libavif/build/shim.c` and translated to Go by wasm2go:
`internal/libavif/module.go` is that translation, which
`internal/libavif/build/build.sh` makes again. `orientation.go` maps irot
and imir to an EXIF orientation as libavif's `avifexif.c` does (as
gen2brain/avif, MIT licence, wrote it in Go).

## Acknowledgements (no code taken)

- **gifsicle**: `internal/gifstream/optimize.go` and `localpal.go` try,
  for a frame of changes, the ways gifsicle's optimizer writes one
  (transparency only over stretches of unchanged pixels of more than one
  colour, or none) and keep the smallest, and `lzwdefer.go` keeps the LZW
  dictionary when it is full as gifsicle's encoder may; the code was
  written from these ideas. `internal/gifstream/lossy.go` implements the
  idea of the lossy LZW of gifsicle (by Kornel Lesiński, described at
  kornel.ski/lossygif): lengthening a dictionary string with a pixel of a
  close enough colour. It was written from that description; no code of
  gifsicle (GPL) was used.
- **pngquant / libimagequant**: the colour quantizer in `internal/quant`
  uses ideas described for pngquant (working on premultiplied colours,
  weighting by how visible an error would be, a dither map). No code of
  either was used.
- **libjpeg, stb_image**: decoding a JPEG straight to 1/2, 1/4 or 1/8 of
  its size, the averages of the pixels worked out from the coefficients,
  is libjpeg's scaled decoding (`jidctred.c`); `dct.go`'s reduced IDCTs were
  derived afresh. Decoding a code's extra bits together with the code in
  one table look-up is an idea of stb_image's JPEG decoder.
- **libvips / sharp**: the chained interface of the package follows sharp;
  so do the mild sharpen kernel, `Modulate` working in LCh, the limits of
  sigma and the default threshold of `Trim`.
