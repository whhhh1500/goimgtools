# goimgtools

| Fit within 1536 + white background + watermark (single thread) | goimgtools | Standard library (whole-image decode) |
|---|---|---|
| JPEG 6000×4000 (24 MP), peak process memory | **8.3MB** | 242MB |
| PNG 6900×9200 (63 MP, with alpha), peak process memory | **10.4MB** | 588MB |
| JPEG 6000×4000, time | **0.38s** | 0.87s |
| PNG 6900×9200, time | **1.63s** | 3.10s |

goimgtools is a streaming image processing library and command-line tool written in pure Go. Images pass through the processing chain in strips of a few rows and are never decoded in full, so memory use is essentially independent of image size, and processing is faster than with whole-image decoding.

Note: peak memory includes about 5.4MB used by the Go runtime itself. See [Performance and benchmarks](docs/performance.md) for the test setup and full results.

[![Go Reference](https://pkg.go.dev/badge/github.com/whhhh1500/goimgtools.svg)](https://pkg.go.dev/github.com/whhhh1500/goimgtools)
[![test](https://github.com/whhhh1500/goimgtools/actions/workflows/test.yml/badge.svg)](https://github.com/whhhh1500/goimgtools/actions/workflows/test.yml)

English | [中文](README.zh-CN.md)

## Features

- **Bounded memory**: a 64MB limit by default, configurable with `MemoryLimit`. Steps that depend on the whole image (rotation, progressive JPEG, colour reduction and others) move staged data to temporary files when it exceeds its share; every buffer that grows with image size counts against the limit, and exceeding it returns `ErrMemoryLimit`. Suited to memory-constrained servers and containers and to high-concurrency image services.
- **Operations**: resize (6 interpolation kernels, 5 fit modes), crop, rotation by any angle, flip, trim and extend, flatten, grayscale, colour modulation, Gaussian blur, sharpening (including unsharp masking); image and text watermarks (nine-point positioning, proportional scaling, rotation, tiling, opacity); text rendering (per-character font fallback, stroke, shadow).
- **Formats**: reading and writing JPEG, PNG, WebP (lossy and lossless), GIF, BMP and TIFF; animated GIF to animated GIF or WebP, and the first frame of an animated WebP or PNG (APNG); ICC colour profiles are preserved by default or converted to sRGB; EXIF is optionally preserved.
- **Compression**: JPEG Huffman table optimization, progressive encoding with mozjpeg's scan search, trellis quantization, and the quantization tables of mozjpeg or of jpegli (with jpegli's, 15% smaller than mozjpeg's defaults at equal SSIMULACRA2 on 14 images); PNG colour reduction with an adaptive palette, and lossless PNG levels 8–9 that try every row filter (oxipng's Brute) and compress with a port of libdeflate's near-optimal parsing, split into blocks by cost as zopfli does (on 17 images 0.4% smaller than oxipng `-o max`, in a sixth of its time); lossy WebP with spatial noise shaping (2.4% smaller than libwebp's default settings at equal SSIM on 7 photographs), and lossless WebP whose matches follow the cheapest path through the pixels (4.7% smaller than libwebp's slowest setting on 17 images, in an eighth of its time); quality selection for a target file size; inter-frame differencing for animated GIF and WebP, lossy LZW for GIF (a photographic animated GIF as lossy WebP: 33% smaller than with libwebp's animation encoder at equal PSNR).
- **Parallelism**: `Threads(n)` enables multi-core processing, with output byte-for-byte identical to a single thread.
- **Robustness**: panics during a run are returned as errors; input and output pixel counts are bounded; `context` cancellation is supported; continuously fuzz-tested.
- **Extensibility**: custom operations, input sources, file formats and interpolation filters; operations registered by name; hooks that modify built-in behaviour; command-line programs that include custom operations.
- **Pure Go**: no cgo. Dependencies are limited to `golang.org/x/image` (with `x/text` and `x/sys` for font parsing) and `github.com/klauspost/compress` (zlib decompression only).

## Installation

```bash
go get github.com/whhhh1500/goimgtools
```

Command-line tool:

```bash
go install github.com/whhhh1500/goimgtools/cmd/goimgtools@latest
```

Requires Go 1.26 or later. Go 1.27 rewrote the standard library's `compress/flate`, so PNG files (not their pixels) differ between the two; the measurements in these documents were made with Go 1.27.

## Quick start

The API uses method chaining similar to [sharp](https://sharp.pixelplumbing.com/): open an input, append operations, select an output format, write. Operations run in the order they are called; errors are returned by the final write method.

```go
err := goimgtools.Open("in.jpg").
    Rotate().                              // correct orientation from EXIF
    Resize(1536, 0).                       // width 1536, height proportional
    Flatten(color.White).                  // white background for transparent areas
    Composite(goimgtools.Open("logo.png"), goimgtools.CompositeOptions{
        Gravity: goimgtools.SouthEast, Left: -24, Top: -24, // bottom right, 24-pixel margin
    }).
    JPEG(85).
    ToFile("out.jpg")
```

Command line:

```bash
goimgtools -rotate auto -resize 1536x1536 -no-enlarge -flatten white -watermark logo.png -quality 85 in.jpg out.jpg
```

```bash
goimgtools batch -resize 1920x1920 -no-enlarge -quality 80 -format jpeg in_dir out_dir
```

## Documentation

| Document | Contents |
|---|---|
| [Usage](docs/usage.md) | Inputs, operations, output, metadata and resource control; server-side use (error classes, cancellation, concurrency); watermarks; text; image inspection tools |
| [Compression](docs/compression.md) | Compression methods for JPEG, PNG, WebP and animated GIF, with measurements |
| [Command line](docs/cli.md) | All options; batch processing of directories |
| [Extending](docs/extending.md) | Custom operations, inputs and formats; operations by name; hooks; custom command-line programs |
| [Performance and benchmarks](docs/performance.md) | Memory control, multi-core processing, measurements and how to reproduce them |
| [Compatibility and comparisons](docs/compatibility.md) | Where output matches the standard library byte for byte, intentional differences, comparison with libwebp |
| [Testing](docs/testing.md) | Test methods, verified platforms, fuzz testing |
| [Changelog](docs/CHANGELOG.md) | Changes by version |
| [API reference](https://pkg.go.dev/github.com/whhhh1500/goimgtools) | All types and methods |

## Supported formats

| Format | Read | Write | Reading method |
|---|---|---|---|
| PNG | ✓ (APNG: the default image) | ✓ | Row-by-row streaming; interlaced (Adam7) images are staged pass by pass, then emitted row by row |
| JPEG | ✓ | ✓ | Baseline: row-by-row streaming; progressive: DCT coefficients are staged and rows reconstructed after the final scan |
| WebP | ✓ (animation: the first frame) | ✓ (lossy, lossless, animation) | Lossy: streaming by macroblock row; lossless: only the maximum back-reference window defined by the format (about 4.2MB) is retained |
| GIF | ✓ (including animation) | ✓ (including animation) | Row-by-row streaming; interlaced images handled as for PNG |
| BMP | ✓ | ✓ | Rows read by file offset |
| TIFF | ✓ | ✓ | Row-by-row streaming decompression |

Staged data is kept in memory up to 1/8 of the memory limit (8MB by default); beyond that it is written to temporary files, which are deleted after use.

## Limitations

- AVIF and HEIC are not supported.
- EXIF in TIFF files is neither read nor written (ICC profiles are).
- Lookup-table (A2B0 and similar) and grayscale colour profiles are not converted to sRGB; they are written unchanged, including to GIF and BMP.
- Entropy decoding in JPEG is sequential, and the IDCT and colour conversion are not yet parallelized; with many threads this is the slowest stage when shrinking JPEGs.
- Animations are processed from animated GIF input only (written as GIF or WebP); the frames of an animated WebP or APNG are not processed one by one (`Animated()` reports `errors.ErrUnsupported`; without it the first frame is read). In GIF output, identical consecutive frames are not merged (each is still stored, at a few dozen bytes).
- Contrast, gamma, levels and inversion are not built in; each takes a few lines with `Pixels` (see the grayscale example in [Extending](docs/extending.md)). Filters that depend on neighbouring pixels (median, emboss, edge detection and others) are implemented as `Pipe` stages.
- GIF, BMP and TIFF encoding is single-threaded; LZ77 matching and the second pass of lossless WebP are sequential, so multiple threads speed it up by only 10–20%.
- JPEG trellis quantization estimates code lengths from the standard Huffman tables (mozjpeg first collects statistics for the whole image): this library processes strips as a stream and does not retain the coefficients of the whole image for this purpose.
- `MaxFileSize` supports JPEG and lossy WebP only (and registered formats that provide `Quality`), and not animations.

## Versioning

Before 1.0, the API may change in incompatible ways. Changes are recorded in the [changelog](docs/CHANGELOG.md).

## Contributing

Issues and pull requests are welcome; see the [contributing guide](.github/CONTRIBUTING.md).

## License

Released under the MIT License ([LICENSE](LICENSE)). Code adapted from the Go standard library and `golang.org/x/image` retains their BSD license ([licenses/LICENSE-GO](licenses/LICENSE-GO)); the two components ported from libjpeg and the trellis quantization ported from mozjpeg retain their licenses ([licenses/LICENSE-LIBJPEG-TURBO](licenses/LICENSE-LIBJPEG-TURBO)); the blur and unsharp mask ported from Pillow retain its MIT-CMU license ([licenses/LICENSE-PILLOW](licenses/LICENSE-PILLOW)); the lossy WebP segmentation ported from libwebp retains its BSD license ([licenses/LICENSE-LIBWEBP](licenses/LICENSE-LIBWEBP)); the JPEG quantization tables after jpegli retain its BSD license ([licenses/LICENSE-JPEGLI](licenses/LICENSE-JPEGLI)); the DEFLATE compressor ported from libdeflate retains its MIT license ([licenses/LICENSE-LIBDEFLATE](licenses/LICENSE-LIBDEFLATE)), its block splitting after zopfli the Apache License 2.0 ([licenses/LICENSE-ZOPFLI](licenses/LICENSE-ZOPFLI)), and the PNG row filter search after oxipng its MIT license ([licenses/LICENSE-OXIPNG](licenses/LICENSE-OXIPNG)). [licenses/THIRD-PARTY.md](licenses/THIRD-PARTY.md) lists the origin of each file.
