# goimgtools

| Fit into 1536 + white background + watermark (1 thread) | goimgtools | Go standard library, whole image |
|---|---|---|
| JPEG 6000×4000 (24 MP): peak process memory | **8.3MB** | 242MB |
| PNG 6900×9200 (63 MP, with alpha): peak process memory | **10.4MB** | 588MB |
| JPEG 6000×4000: time | **0.38s** | 0.87s |
| PNG 6900×9200: time | **1.63s** | 3.10s |

**An image processing library and command-line tool in pure Go, with ultra-low memory use.** Images flow through the processing chain a few rows at a time and are never decoded whole, so memory barely grows with the size of the image, and it is faster than decoding the whole image too. (About 5.4MB of each peak is the Go runtime itself; see [Memory, speed and benchmarks](docs/performance.md) for the setup and more numbers.)

[![Go Reference](https://pkg.go.dev/badge/github.com/whhhh1500/goimgtools.svg)](https://pkg.go.dev/github.com/whhhh1500/goimgtools)
[![test](https://github.com/whhhh1500/goimgtools/actions/workflows/test.yml/badge.svg)](https://github.com/whhhh1500/goimgtools/actions/workflows/test.yml)

English | [中文](README.zh-CN.md)

## Features

- **A hard memory limit**: 64MB by default, set with `MemoryLimit`. Steps that must see the whole image before they can go on (rotations, progressive JPEG, colour reduction and so on) move their staged data to temporary files beyond their share; every buffer that grows with the image counts against the limit, and running out returns an error instead of quietly using more. Made for small servers and containers, and for services that process many images at once.
- **The usual operations**: resize (6 kernels, 5 fits), crop, rotate by any angle, flip, trim and extend borders, flatten, grayscale, modulate, blur, sharpen; image or text watermarks (nine-point placement, scaling to the image, rotation, tiling, opacity); text rendering (per-character font fallback, stroke, shadow).
- **Six formats, read and write**: JPEG, PNG, WebP (lossy and lossless), GIF (including animated), BMP, TIFF. ICC colour profiles are kept by default or converted to sRGB; EXIF is kept on request.
- **Compression**: JPEG with optimized tables, progressive and trellis quantization; PNG colour reduction with a palette chosen for the image; lossy WebP 2.4% smaller than libwebp's default at equal SSIM on 7 photographs; a file size limit that picks the quality; animated GIFs that store only what changes.
- **Multi-core, optionally**: `Threads(n)`, with output byte-for-byte identical to one thread.
- **No crashes**: any panic is returned as an error; input and output pixel counts are limited; runs can be cancelled; fuzz tests guard all of it.
- **Extensible**: your own operations, inputs, file formats and resize filters; operations registered by name; hooks that change built-in behaviour; a command-line program that carries your operations.
- **Pure Go, no cgo**. The only dependencies are the Go project's own extensions (`golang.org/x/image`, plus `x/text` and `x/sys` for fonts) and `github.com/klauspost/compress` (its zlib decompressor only).

## Install

```bash
go get github.com/whhhh1500/goimgtools
```

The command-line program:

```bash
go install github.com/whhhh1500/goimgtools/cmd/goimgtools@latest
```

Requires Go 1.27 or newer.

## Quick start

The interface follows [sharp](https://sharp.pixelplumbing.com/): open an input, chain operations, pick an output format, write. Operations run in the order they are chained; any error is returned by the last call.

```go
err := goimgtools.Open("in.jpg").
    Rotate().                              // upright, by the EXIF orientation
    Resize(1536, 0).                       // 1536 wide, height in proportion
    Flatten(color.White).                  // transparent areas become white
    Composite(goimgtools.Open("logo.png"), goimgtools.CompositeOptions{
        Gravity: goimgtools.SouthEast, Left: -24, Top: -24, // bottom right, 24 pixels in
    }).
    JPEG(85).
    ToFile("out.jpg")
```

On the command line:

```bash
goimgtools -rotate auto -resize 1536x1536 -no-enlarge -flatten white -watermark logo.png -quality 85 in.jpg out.jpg
```

```bash
goimgtools batch -resize 1920x1920 -no-enlarge -quality 80 -format jpeg in_dir out_dir
```

## Documentation

| Document | Contents |
|---|---|
| [Usage](docs/usage.md) | Inputs, operations, output, metadata and resource control; using it in a service (error kinds, cancellation, concurrency); watermarks; text; inspecting and checking images |
| [Compression](docs/compression.md) | Ways to make JPEG, PNG, WebP and animated GIF files smaller, with measurements |
| [Command line](docs/cli.md) | Every option; batch processing of directories |
| [Extending](docs/extending.md) | Your own operations, inputs and formats; operations by name; hooks; your own command-line program |
| [Memory, speed and benchmarks](docs/performance.md) | How memory is kept low, multi-core, measurements and how to repeat them |
| [Compatibility and comparisons](docs/compatibility.md) | What is byte-for-byte identical to the standard library, what differs on purpose, comparisons with libwebp |
| [Testing](docs/testing.md) | How it is tested, platforms it runs on, fuzz tests |
| [Changelog](docs/CHANGELOG.md) | Changes by version |
| [API reference](https://pkg.go.dev/github.com/whhhh1500/goimgtools) | Every type and method |

## Supported formats

| Format | Read | Write | How it is read |
|---|---|---|---|
| PNG | yes | yes | Streamed by rows; interlaced (Adam7) images keep the passes in working storage and hand out rows from there |
| JPEG | yes | yes | Baseline streamed by rows; progressive keeps the DCT coefficients in working storage and reconstructs rows after the last scan |
| WebP | yes | yes (lossy, lossless) | Lossy streamed by macroblock rows; lossless keeps only the back-reference window the format allows (about 4.2MB) |
| GIF | yes (animated too) | yes (animated too) | Streamed by rows; interlaced as PNG |
| BMP | yes | yes | Rows read by file offset |
| TIFF | yes | yes | Streamed decompression by rows |

Working storage stays in memory while it is within 1/8 of the memory limit (8MB by default) and moves to a temporary file beyond that, deleted when done.

## Not supported

- AVIF and HEIC.
- EXIF in TIFF files (ICC profiles are read and written).
- Converting lookup-table (A2B0 and similar) and grayscale colour profiles to sRGB; they are written out unchanged (also to GIF and BMP).
- JPEG decoding is sequential in its entropy decoding, and its IDCT and colour conversion are not shared out between cores yet: with many threads it is the slowest part of shrinking a JPEG.
- Animation is GIF only. Identical consecutive frames are not merged (each still takes a frame, a few dozen bytes); gifsicle's lossy LZW (`--lossy`) is not implemented.
- No built-in contrast, gamma, levels or invert; each is a few lines with `Pixels` (see the grayscale example in [Extending](docs/extending.md)). Filters that look at neighbouring pixels (median, emboss, edge detection and so on) are written as a `Pipe` stage.
- GIF, BMP and TIFF encoding use one core; lossless WebP's LZ77 matching and second pass are sequential, so more threads make it only 10–20% faster.
- JPEG trellis quantization estimates bit costs from the standard Huffman tables (mozjpeg first gathers statistics over the whole image): this library streams strips and does not keep a second copy of the coefficients for it.
- `MaxFileSize` works for JPEG and lossy WebP only (and registered formats that provide `Quality`).

## Versioning

Until 1.0 the API may still change. Changes are listed in the [changelog](docs/CHANGELOG.md).

## Contributing

Questions, bug reports and pull requests are welcome; see [CONTRIBUTING](.github/CONTRIBUTING.md).

## License

MIT ([LICENSE](LICENSE)). Code adapted from the Go standard library and `golang.org/x/image` keeps their BSD licence ([licenses/LICENSE-GO](licenses/LICENSE-GO)); the two parts ported from libjpeg and the trellis quantization ported from mozjpeg keep their licences ([licenses/LICENSE-LIBJPEG-TURBO](licenses/LICENSE-LIBJPEG-TURBO)); the blur and unsharp mask ported from Pillow keep its MIT-CMU licence ([licenses/LICENSE-PILLOW](licenses/LICENSE-PILLOW)); the lossy WebP segmentation ported from libwebp keeps its BSD licence ([licenses/LICENSE-LIBWEBP](licenses/LICENSE-LIBWEBP)). [licenses/THIRD-PARTY.md](licenses/THIRD-PARTY.md) lists which files come from where.
