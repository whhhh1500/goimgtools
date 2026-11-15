English | [中文](zh-CN/CHANGELOG.md)

# Changelog

## Unreleased

### Added

- Image adjustments: `Grayscale`, `Modulate`, `Blur`, `Sharpen` (mild, or an unsharp mask), `Extend`, `Trim` and `RotateBy` (any angle).
- `inspect.Stats`, `inspect.Hash` (a perceptual hash) and `inspect.ReadExif` / `ParseExif`; the commands `stats`, `hash` and `exif`.
- Operations by name: `RegisterOperation`, `UnregisterOperation`, `LookupOperation`, `Operations` and `Image.Do`. Hooks around every step: `Image.Around`. `ParseColor`.
- Package `cli`: the command line as a library (`cli.Main`, `cli.Run`), so that a program can add its own operations to it; the flag `-op NAME[:ARGS]` and the command `ops`.
- `ToSRGB()` / `-srgb`: converts the colours to sRGB from the colour profile. GIF and BMP output, which cannot hold a profile, is converted automatically.
- Colour profiles in WebP and TIFF files (read and written); EXIF in WebP files.
- `JPEGOptions.Trellis` / `-jpeg-trellis`: trellis quantization, as mozjpeg does it.
- `CompositeOptions.WidthRatio` / `HeightRatio`: a watermark sized as a fraction of the image; `-watermark-width N%`.
- Lossy WebP: segments with quantizers of their own (spatial noise shaping, ported from libwebp).
- `pipeline.Env.SetHelpers`, `Helpers`, `Parallel` and `Go`, for stages that share their work between cores.

### Changed

- `Threads(n)` goes up to the number of CPUs (it was capped at 3). From 4 threads on, resizing, blurring, sharpening, modulating, rotating, and JPEG, PNG and lossy WebP encoding share their work between cores. The output is byte-for-byte the same for any number of threads.
- PNG's default compression level is 7 (it was 6, the standard library's): Go's level 6 finds few repeats, and level 7 makes gradients and icons about 5 times smaller and photographs 4–25% smaller.
- PNG data is compressed in blocks of 256KB (as pigz does), so that several cores can share the work. While the image data fits in one block the file is the same as before.
- Animated GIF output: one palette chosen for all frames (`GIFOptions.Colors` and `NoDither` now apply to animations), and the frames after the first hold only the rectangle that changed. `GIFOptions{FixedPalette: true}` keeps writing whole frames with the fixed palette, frame by frame. The pixel limit is checked before any frame is processed. `NoDither` also applies with `FixedPalette`.
- Lossy WebP files are about 15% smaller at the same quality setting (segments); against libwebp 1.6 at equal SSIM they are 2.4% smaller.
- Lossless WebP chooses each band's predictors with the cost model as the band starts, so that they can be chosen in parallel; files differ slightly.
- The command-line code moved from `cmd/goimgtools` to package `cli`; `cmd/goimgtools` builds the same program.

### Fixed

- A panic in an operation could leave the input of the chain open, holding memory budget and temporary files.

## 0.1.0 (2026-11-14)

The first public release: reading and writing PNG, JPEG, WebP, GIF (animated too), BMP and TIFF a few rows at a time within a memory limit; resizing (six kernels, five fits), cropping, rotating by multiples of 90°, flipping, flattening, compositing and watermarks, text; JPEG with optimized tables and progressive coding, PNG colour reduction, `MaxFileSize`; batch processing; extension interfaces (`Pipe`, `Pixels`, `Apply`, `FromImage`, `FromSource`, `RegisterFormat`, `Stream`, `ToImage`, package `pipeline`); the commands `info`, `check`, `compare` and `formats`, and package `inspect`; limits on input and output pixels, cancellation, and panics returned as errors.
