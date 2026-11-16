English | [中文](zh-CN/CHANGELOG.md)

# Changelog

## Unreleased

### Upgrading from 0.1.0

Nothing exported by 0.1.0 was removed or changed in its signature (checked with `apidiff`, which every push now runs against the last release). What can still surprise code written for 0.1.0:

- `Animated()` returns an error wrapping `errors.ErrUnsupported` for an animation it cannot keep (a GIF to JPEG, PNG, BMP or TIFF; an animated WebP or APNG input), where 0.1.0 silently wrote the first frame. Leave out `Animated()` to get the first frame.
- A lossy WebP is decoded to `*image.NRGBA` strips, no longer to `*image.YCbCr`, so that its colours are libwebp's; `Metadata()` still reports `ycbcr`. Code that type-asserts the decoded image should accept any `image.Image`.
- `Frames().LoopCount` of a GIF is the number of times it plays, as documented, one more than 0.1.0 returned.
- The default PNG level is 7 with Go 1.27 (6 before): smaller files, written more slowly. `PNGOptions{CompressionLevel: 6}` keeps the old files.
- `Threads(n)` is no longer capped at 3: a larger n uses that many cores.

### Added

- Image adjustments: `Grayscale`, `Modulate`, `Blur`, `Sharpen` (mild sharpening or unsharp masking), `Extend`, `Trim` and `RotateBy` (arbitrary angle).
- `inspect.Stats`, `inspect.Hash` (perceptual hash) and `inspect.ReadExif` / `ParseExif`; the `stats`, `hash` and `exif` commands.
- Operations by name: `RegisterOperation`, `UnregisterOperation`, `LookupOperation`, `Operations` and `Image.Do`. Hooks around every step: `Image.Around`. `ParseColor`.
- Package `cli`: the command line as a library (`cli.Main`, `cli.Run`), allowing programs to add their own operations; the `-op NAME[:ARGS]` flag and the `ops` command.
- `ToSRGB()` / `-srgb`: converts colours to sRGB using the colour profile. GIF and BMP output, which cannot carry a profile, is converted automatically.
- Colour profiles in WebP and TIFF files (read and write); EXIF in WebP files.
- `JPEGOptions.Trellis` / `-jpeg-trellis`: trellis quantization, following mozjpeg.
- `JPEGOptions.QuantTable` (type `QuantTable`, with a named constant for each choice) / `-jpeg-quant-table`: mozjpeg's nine quantization tables, numbered as its `-quant-table`; `QuantTableStandard` (0), the default, is the JPEG standard's.
- `QuantTableJpegli` (`-jpeg-quant-table 9`): JPEG quantization tables made as jpegli makes them, with its dead zone without `Trellis`; with `Progressive` and `Trellis`, 15% smaller than mozjpeg's defaults at equal SSIMULACRA2.
- `-webp-effort`: the effort of WebP on the command line (`WebPOptions.Effort` in the library).
- `GIFOptions.Lossy` / `-gif-lossy`: lossy LZW compression for GIF, still and animated (the method of gifsicle's `--lossy`): a photographic animation 18% smaller at 16, 32% at 24.
- Animated GIF: frames with more than 256 colours together but at most 256 each are written with a palette per frame, every pixel unchanged (they were reduced to one palette, which changed pixels and made the file larger); a frame of changes is written whichever of three ways codes smallest, and its LZW dictionary kept when full while that pays: 0.2–0.45% smaller than gifsicle `-O3` on three animations.
- Animated WebP output: `Animated()` with WebP output writes an animated GIF as an animated WebP (lossy or lossless), storing only the rectangle that changed in each frame.
- Animated WebP input: read as its first frame (previously rejected as an unreadable image). `Frames` counts the frames of animated WebP and PNG (APNG) files; `info` shows them.
- `CompositeOptions.WidthRatio` / `HeightRatio`: watermark size as a fraction of the base image; `-watermark-width N%`.
- Lossy WebP: segments with individual quantizers (spatial noise shaping, ported from libwebp).
- `pipeline.Env.SetHelpers`, `Helpers`, `Parallel` and `Go`, allowing stages to distribute their work across cores.

### Changed

- From level 7, PNG output tries no filter against the filters the rows choose, every 32KB of rows, and keeps the smaller: screenshots, icons and drawings 10–19% smaller; photographs unchanged.
- PNG levels 8 and 9 search for the smallest file: rows are filtered as oxipng's Brute strategy filters them (every filter tried), and the data is compressed with a Go port of libdeflate's near-optimal parsing instead of `compress/flate`, its blocks split by cost and its symbols priced at their entropy, as zopfli does. On 17 screenshots, photographs and game artwork, 14.2% (8) and 15.5% (9) smaller than level 7, against 14.8% for oxipng `-o max` (in a sixth of its time) and 15.8% for ECT `-9`; the same file with every Go version. They take about 4MB (8) and 6.5MB (9) more memory per thread.
- The minimum Go version is 1.26 (0.1.0 required 1.27). PNG files written with Go 1.26 and Go 1.27 differ in their bytes but not in their pixels, as Go 1.27 rewrote `compress/flate`. With Go 1.26 the default PNG level is 6 (see below).
- Progressive JPEG chooses its scans as mozjpeg does (`optimize_scans`): files 0.4–14% smaller (2.4% on average over 18 images, 3.9% with `Trellis` and `QuantTable: QuantTableImageMagick`; with both, 0.7% smaller than mozjpeg's defaults at equal PSNR), with the same pixels. Single-threaded it takes 3–19% longer; the candidate scans are counted in parallel with `Threads`.
- `Animated()` no longer ignores an animation it cannot keep: a GIF to an output format other than GIF and WebP, or an animated WebP or APNG input, returns an error wrapping `errors.ErrUnsupported` instead of writing the first frame. A still image passes as before. So does `MaxFileSize` with `Animated()` on an animation, which was ignored.
- `Threads(n)` now extends to the number of CPUs (previously capped at 3). From 4 threads upwards, resizing, blurring, sharpening, modulation, rotation, and JPEG, PNG and lossy WebP encoding distribute their work across cores. Output is byte-for-byte identical for any number of threads.
- The default PNG compression level is now 7 with Go 1.27 (previously 6, the standard library's default; with Go 1.26 it stays 6, where level 7 gains only 1–1.5% and takes half as long again): Go 1.27's level 6 finds few repeated sequences, whereas level 7 produces files about 5 times smaller for gradients and icons and 4–25% smaller for photographs.
- PNG data is compressed in 256KB blocks (as in pigz), allowing multiple cores to share the work. When the image data fits within a single block, the file is identical to that of previous versions.
- Animated GIF output: a single palette is chosen for all frames (`GIFOptions.Colors` and `NoDither` now apply to animations), and frames after the first store only the changed rectangle. `GIFOptions{FixedPalette: true}` continues to write full frames with the fixed palette, frame by frame. The pixel limit is checked before any frame is processed. `NoDither` also applies with `FixedPalette`.
- Lossy WebP: segment-based quantization, following libwebp's spatial noise shaping.
- Lossy WebP: `WebPOptions.Effort` applies to lossy output too (it was for lossless only). The default, 5, converts colours with sharp YUV, quantizes 4×4 blocks by trellis and chooses the loop filter levels by measuring, as libwebp does at its best settings; chroma DC errors are diffused at every effort; quantizers and rate-distortion weights were tuned on SSIMULACRA2 and butteraugli. On 14 images: 6.6% / 9.5% smaller than libwebp's default and 1.3% / 2.2% smaller than its best settings (`-m 6 -af -sharp_yuv`), at equal SSIMULACRA2 / butteraugli. Encoding takes 2 to 2.6 times as long; `Effort: 1` keeps the earlier speed and is still 6.1% / 4.8% smaller than libwebp's default.
- Lossy WebP with transparency: the alpha plane is coded by the lossless encoder at its default effort, through the better of no filter and the horizontal one (all four filters from effort 7), with the optimal parse for planes of up to 4 megapixels (all from effort 7), and the colours of transparent areas are made cheap, as libwebp does: a photograph in a soft round window 50% smaller (146KB to 73KB at quality 75; libwebp 88KB), alpha chunks up to 6 times smaller (Tux's 2% smaller than libwebp's). Up to 8MB more memory while the plane is coded; under a low memory limit the alpha chunk (not the pixels) can differ, as lossless files can.
- Lossy WebP is decoded to the pixels libwebp, and so browsers, show, to the byte: chroma interpolated between samples and libwebp's integer conversion (before, each chroma sample covered its 2×2 block and the colours differed by about half a level on average, up to 20–40 at colour edges). Decoded lossy WebP is now RGB; `Metadata()` still reports it as `ycbcr`.
- Lossless WebP chooses its LZ77 matches by the cheapest path through the pixels (the idea of libwebp’s TraceBackwards), searches further for repeats in screenshots, and tries several block sizes and numbers of Huffman groups: 5.5% smaller over 17 images at the default effort (screenshots 6–9%), in 3.8 times the time (42s against 11s on one core; libwebp's default takes 24s); 5.3% smaller in total than libwebp’s slowest setting (5.7% at effort 9, in 106s against its 308s). At every pixel the longest match at the nearest distances above and to the left is offered besides the hash chain's (a gradient mask 16% smaller), and prefix codes are evened out for run-length coding of their lengths when that pays, as libwebp does. Images of up to 4 megapixels also race a variant with libwebp's cross-color transform (icons and interface pictures with colour gradients 2–7% smaller), their split into Huffman groups is also tried without the colour cache (an icon 4% smaller), and their variants without the predictor also try matches only at distance 1 and a row up, as libwebp's "RLE" references (icons of few colours 3–4% smaller). A 6000×4000 photograph from a JPEG, whose noise leaves the parse little to find, comes out the same size, in 5.1s instead of 4.1s and at most 25.6MB instead of 22.2MB.
- Lossless WebP selects each band's predictors using the cost model as it stands at the start of that band, so that they can be selected in parallel; output files differ slightly.
- The command-line code has moved from `cmd/goimgtools` to package `cli`; `cmd/goimgtools` builds the same program.

### Fixed

- A panic in an operation could leave the chain's input open, retaining memory budget and temporary files.
- `Frames().LoopCount` of a GIF was its repeat count, one less than the number of times it plays, which is what the field documents.

## 0.1.0 (2026-11-14)

Initial public release: reading and writing PNG, JPEG, WebP, GIF (including animation), BMP and TIFF a few rows at a time within a memory limit; resizing (six kernels, five fit modes), cropping, rotation by multiples of 90°, flipping, flattening, compositing and watermarks, text; JPEG with optimized coding tables and progressive encoding, PNG colour reduction, `MaxFileSize`; batch processing; extension interfaces (`Pipe`, `Pixels`, `Apply`, `FromImage`, `FromSource`, `RegisterFormat`, `Stream`, `ToImage`, package `pipeline`); the `info`, `check`, `compare` and `formats` commands, and package `inspect`; input and output pixel limits, cancellation, and panics returned as errors.
