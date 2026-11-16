English | [中文](zh-CN/CHANGELOG.md)

# Changelog

## Unreleased

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
- `GIFOptions.Lossy` / `-gif-lossy`: lossy LZW compression for GIF, still and animated (the method of gifsicle's `--lossy`): a photographic animation 18% smaller at 16, 32% at 24.
- Animated WebP output: `Animated()` with WebP output writes an animated GIF as an animated WebP (lossy or lossless), storing only the rectangle that changed in each frame.
- Animated WebP input: read as its first frame (previously rejected as an unreadable image). `Frames` counts the frames of animated WebP and PNG (APNG) files; `info` shows them.
- `CompositeOptions.WidthRatio` / `HeightRatio`: watermark size as a fraction of the base image; `-watermark-width N%`.
- Lossy WebP: segments with individual quantizers (spatial noise shaping, ported from libwebp).
- `pipeline.Env.SetHelpers`, `Helpers`, `Parallel` and `Go`, allowing stages to distribute their work across cores.

### Changed

- From level 7, PNG output tries no filter against the filters the rows choose, every 32KB of rows, and keeps the smaller: screenshots, icons and drawings 10–19% smaller; photographs unchanged.
- The minimum Go version is 1.26 (0.1.0 required 1.27). PNG files written with Go 1.26 and Go 1.27 differ in their bytes but not in their pixels, as Go 1.27 rewrote `compress/flate`. With Go 1.26 the default PNG level is 6 (see below).
- Progressive JPEG chooses its scans as mozjpeg does (`optimize_scans`): files 0.4–14% smaller (2.4% on average over 18 images, 3.9% with `Trellis` and `QuantTable: QuantTableImageMagick`; with both, 0.7% smaller than mozjpeg's defaults at equal PSNR), with the same pixels. Single-threaded it takes 3–19% longer; the candidate scans are counted in parallel with `Threads`.
- `Animated()` no longer ignores an animation it cannot keep: a GIF to an output format other than GIF and WebP, or an animated WebP or APNG input, returns an error wrapping `errors.ErrUnsupported` instead of writing the first frame. A still image passes as before. So does `MaxFileSize` with `Animated()` on an animation, which was ignored.
- `Threads(n)` now extends to the number of CPUs (previously capped at 3). From 4 threads upwards, resizing, blurring, sharpening, modulation, rotation, and JPEG, PNG and lossy WebP encoding distribute their work across cores. Output is byte-for-byte identical for any number of threads.
- The default PNG compression level is now 7 with Go 1.27 (previously 6, the standard library's default; with Go 1.26 it stays 6, where level 7 gains only 1–1.5% and takes half as long again): Go 1.27's level 6 finds few repeated sequences, whereas level 7 produces files about 5 times smaller for gradients and icons and 4–25% smaller for photographs.
- PNG data is compressed in 256KB blocks (as in pigz), allowing multiple cores to share the work. When the image data fits within a single block, the file is identical to that of previous versions.
- Animated GIF output: a single palette is chosen for all frames (`GIFOptions.Colors` and `NoDither` now apply to animations), and frames after the first store only the changed rectangle. `GIFOptions{FixedPalette: true}` continues to write full frames with the fixed palette, frame by frame. The pixel limit is checked before any frame is processed. `NoDither` also applies with `FixedPalette`.
- Lossy WebP files are about 15% smaller at the same quality setting (segments); at equal SSIM they are 2.4% smaller than libwebp 1.6.
- Lossless WebP selects each band's predictors using the cost model as it stands at the start of that band, so that they can be selected in parallel; output files differ slightly.
- The command-line code has moved from `cmd/goimgtools` to package `cli`; `cmd/goimgtools` builds the same program.

### Fixed

- A panic in an operation could leave the chain's input open, retaining memory budget and temporary files.
- `Frames().LoopCount` of a GIF was its repeat count, one less than the number of times it plays, which is what the field documents.

## 0.1.0 (2026-11-14)

Initial public release: reading and writing PNG, JPEG, WebP, GIF (including animation), BMP and TIFF a few rows at a time within a memory limit; resizing (six kernels, five fit modes), cropping, rotation by multiples of 90°, flipping, flattening, compositing and watermarks, text; JPEG with optimized coding tables and progressive encoding, PNG colour reduction, `MaxFileSize`; batch processing; extension interfaces (`Pipe`, `Pixels`, `Apply`, `FromImage`, `FromSource`, `RegisterFormat`, `Stream`, `ToImage`, package `pipeline`); the `info`, `check`, `compare` and `formats` commands, and package `inspect`; input and output pixel limits, cancellation, and panics returned as errors.
