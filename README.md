# goimgtools

| Fit within 1536 + white background + watermark (single thread) | goimgtools | Standard library (whole-image decode) |
|---|---|---|
| JPEG 6000×4000 (24 MP), peak process memory | **9.9MB** | 243MB |
| PNG 6900×9200 (63 MP, with alpha), peak process memory | **10.4MB** | 588MB |
| JPEG 6000×4000, time | **0.17s** | 0.85s |
| PNG 6900×9200, time | **1.63s** | 3.10s |

goimgtools is a streaming image processing library and command-line tool written in pure Go. Images pass through the processing chain in strips of a few rows and are never decoded in full, so memory use is essentially independent of image size, and processing is faster than with whole-image decoding.

Note: peak memory includes about 5.4MB used by the Go runtime itself. See [Performance and benchmarks](docs/performance.md) for the test setup and full results.

[![Go Reference](https://pkg.go.dev/badge/github.com/whhhh1500/goimgtools.svg)](https://pkg.go.dev/github.com/whhhh1500/goimgtools)
[![test](https://github.com/whhhh1500/goimgtools/actions/workflows/test.yml/badge.svg)](https://github.com/whhhh1500/goimgtools/actions/workflows/test.yml)

English | [中文](README.zh-CN.md)

## Features

- **Bounded memory**: a 64MB limit by default, configurable with `MemoryLimit`. Steps that depend on the whole image (rotation, colour reduction, a progressive JPEG from a stream and others) move staged data to temporary files when it exceeds its share; every buffer that grows with image size counts against the limit, and exceeding it returns `ErrMemoryLimit`. Suited to memory-constrained servers and containers and to high-concurrency image services.
- **Operations**: resize (8 interpolation kernels, 5 fit modes), crop, rotation by any angle, affine, perspective and quadrilateral transforms, flip, trim and extend, flatten, grayscale, colour modulation, negate, linear levels, gamma, threshold, tint, normalize, auto contrast, equalize, posterize, solarize, Pillow's enhancements (colour, contrast, brightness, sharpness) and built-in filters (emboss, contour, find edges, edge enhance…), local contrast (CLAHE), Gaussian and box blur, sharpening (including unsharp masking), median and rank filters, dilate and erode, custom convolution kernels, channel mixing (recomb), unflatten, channel extraction, smart crops (entropy, attention); image and text watermarks (nine-point positioning, proportional scaling, rotation, tiling, opacity); text rendering (per-character font fallback, stroke, shadow); drawing (rectangles with round corners, ellipses and circles, polygons, lines; fills and strokes with smooth edges, as Pillow's ImageDraw draws them but anti-aliased); blank canvases; grids of images (sharp's join).
- **Invisible watermarks**: `InvisibleWatermark` hides up to 32 bytes (a user ID, an order number), read back with `inspect.ReadWatermark` and the key, without the original: a blind mark that survives saving again, resizing, cropping, turning, brightness and contrast and screenshots, and a mark against leaks that also survives a photograph of a screen; strength and density can be set (see [Invisible watermarks](docs/watermark.md)).
- **Formats**: reading and writing JPEG, PNG, WebP (lossy and lossless), GIF, BMP and TIFF; animated GIF, WebP and PNG (APNG) to animated GIF, WebP or PNG; ICC colour profiles (matrix, lookup-table and grey; CMYK images are converted to sRGB through theirs) are preserved by default or converted to sRGB; the resolution (DPI) is preserved or set; EXIF and XMP are optionally preserved.
- **Compression**: JPEG Huffman table optimization, progressive encoding with mozjpeg's scan search, trellis quantization, and the quantization tables of mozjpeg or of jpegli (with jpegli's, 16% smaller than mozjpeg's defaults at equal SSIMULACRA2 on 14 images), and mozjpeg's overshoot deringing (sharp's bytes; a screenshot at quality 50 as good as at 75 without it); PNG colour reduction with an adaptive palette, and lossless PNG levels 8–9 that try every row filter (oxipng's Brute) and compress with a port of libdeflate's near-optimal parsing, split into blocks by cost as zopfli does (on 17 images 0.9% smaller than oxipng `-o max` in less than half its time), and level 10, which compresses the rows with each filter to choose and compresses with a port of Zopfli on several cores (0.23% smaller than ECT `-9` on the same images); lossy WebP with spatial noise shaping, sharp YUV, trellis quantization, measured loop filter levels and chroma error diffusion (on 14 images 6.6% smaller than libwebp's default settings at equal SSIMULACRA2, and 1.3% smaller than its best settings), and lossless WebP whose matches follow the cheapest path through the pixels (5.1% smaller than libwebp's slowest setting on 17 images, in a quarter of its time), and near-lossless WebP as libwebp's (photographs 37% smaller than lossless; 7.3% smaller than cwebp's on 23 images); quality selection for a target file size or a target SSIM (the smallest file that still looks the same); inter-frame differencing for animated GIF and WebP, lossy LZW for GIF (a photographic animated GIF as lossy WebP: 33% smaller than with libwebp's animation encoder at equal PSNR).
- **Pillow's and sharp's pixels**: JPEG files decode to libjpeg-turbo's pixels and are written as libjpeg-turbo writes them (sharp's and Pillow's bytes), and many operations give Pillow's or sharp's results to the byte: resize (`MethodLibvips` as sharp, JPEGs and WebPs shrunk on load included; `MethodPillow` as Pillow, `thumbnail` included), Pillow's ImageOps, ImageEnhance and ImageFilter (`BoxBlur` included), blur (either way), tile pyramids (sharp's `tile`: Deep Zoom, Google Maps, IIIF, Zoomify), rotation by any angle (as sharp or as Pillow), affine transforms (as sharp, or Pillow's `transform`, perspective included), trim, grayscale, threshold, negate, linear, recomb, unflatten, median, rank filters, dilate and erode, convolution, CLAHE, ensure-alpha, among others (see [Compatibility](docs/compatibility.md)).
- **Parallelism**: `Threads(n)` enables multi-core processing, with output byte-for-byte identical to a single thread.
- **Robustness**: panics during a run are returned as errors; input and output pixel counts are bounded; `context` cancellation is supported; continuously fuzz-tested.
- **Extensibility**: custom operations, input sources, file formats and interpolation filters; operations registered by name; hooks that modify built-in behaviour; command-line programs that include custom operations.
- **Pure Go**: no cgo. Dependencies are limited to `golang.org/x/image`, with `x/text` (font parsing) and `x/sys` (processor features).

## Installation

```bash
go get github.com/whhhh1500/goimgtools
```

Command-line tool:

```bash
go install github.com/whhhh1500/goimgtools/cmd/goimgtools@latest
```

Requires Go 1.26 or later. Go 1.27 rewrote the standard library's `compress/flate`, so PNG files written at levels 1 to 6 (not their pixels) differ between the two (the default level, 7, and 8 and 9 do not use it); the measurements in these documents were made with Go 1.27.

## Quick start

The API uses method chaining similar to [sharp](https://sharp.pixelplumbing.com/): open an input, append operations, select an output format, write. Operations run in the order they are called; errors are returned by the final write method.

```go
err := goimgtools.Open("in.jpg").
    AutoOrient().                          // correct orientation from EXIF
    Resize(1536, 0).                       // width 1536, height proportional
    Flatten(color.White).                  // white background for transparent areas
    Composite(goimgtools.Open("logo.png"), goimgtools.CompositeOptions{
        Gravity: goimgtools.SouthEast, Left: -24, Top: -24, // bottom right, 24-pixel margin
    }).
    JPEG(goimgtools.JPEGOptions{Quality: 85}).
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
| [Invisible watermarks](docs/watermark.md) | Hiding a mark in a picture and reading it back: the two kinds, settings, how it works, measurements |
| [Compression](docs/compression.md) | Compression methods for JPEG, PNG, WebP and animated GIF, with measurements |
| [Command line](docs/cli.md) | All options; batch processing of directories |
| [Extending](docs/extending.md) | Custom operations, inputs and formats; operations by name; hooks; custom command-line programs |
| [Performance and benchmarks](docs/performance.md) | Memory control, multi-core processing, measurements and how to reproduce them |
| [Compatibility and comparisons](docs/compatibility.md) | Where output matches the standard library, Pillow or sharp byte for byte, intentional differences, comparison with libwebp |
| [Testing](docs/testing.md) | Test methods, verified platforms, fuzz testing |
| [Changelog](docs/CHANGELOG.md) | Changes by version |
| [API reference](https://pkg.go.dev/github.com/whhhh1500/goimgtools) | All types and methods |

## Supported formats

| Format | Read | Write | Reading method |
|---|---|---|---|
| PNG | ✓ (APNG: the default image, every frame with `Animated()`) | ✓ (APNG with `Animated()`) | Row-by-row streaming; interlaced (Adam7) images are staged pass by pass, then emitted row by row |
| JPEG | ✓ | ✓ | Baseline: row-by-row streaming; progressive: DCT coefficients are staged and rows reconstructed after the final scan |
| WebP | ✓ (animation: the first frame) | ✓ (lossy, lossless, animation) | Lossy: streaming by macroblock row; lossless: only the maximum back-reference window defined by the format (about 4.2MB) is retained |
| GIF | ✓ (including animation) | ✓ (including animation) | Row-by-row streaming; interlaced images handled as for PNG |
| BMP | ✓ | ✓ | Rows read by file offset |
| TIFF | ✓ | ✓ | Row-by-row streaming decompression; JPEG-compressed TIFFs (as libtiff, libvips and sharp write them) are read and written |
| ICO | ✓ (and CUR: the largest image, the one with the most colours among those as large) | ✓ (one or several sizes, as PNG) | The largest image: a PNG one streamed, a bitmap one (at most 256×256) whole; several sizes are each resized from the image kept in working storage |

Staged data is kept in memory up to 1/8 of the memory limit (8MB by default); beyond that it is written to temporary files, which are deleted after use.

## Limitations

- HEIC, AVIF and JPEG XL are not supported. Their codecs (Kvazaar and gen2brain/h265, aom and dav1d, libjxl) were tried as separate modules, translated to Go, and take far more memory than this library allows itself: hundreds of megabytes to encode a 12-megapixel photograph (cjxl itself, streaming, 196 to 268MB) and several gigabytes to compile. The `heic` and `avif` modules of 0.2.0 are no longer developed. A format can be added from outside with `RegisterFormat` (see [Extending](docs/extending.md)).
- Entropy decoding in JPEG is sequential (from `Threads(5)` the inverse DCT and the conversion of colour go to the other cores); with many threads it is the slowest stage of decoding a JPEG.
- A per-pixel adjustment of your own (beyond `Curves` and the other level operations) takes a few lines with `Pixels` (see the grayscale example in [Extending](docs/extending.md)), and a filter that needs neighbouring pixels can be a `Pipe` stage or a `Convolve` kernel.
- GIF and BMP encoding is single-threaded (the strips of a compressed TIFF are compressed on the other cores); the optimal parse of lossless WebP (once the matches are found) and its second pass are sequential, so eight threads make it about twice as fast on screenshots and drawings, and 1.5 times on photographs.
- JPEG trellis quantization estimates code lengths from the standard Huffman tables (mozjpeg first collects statistics for the whole image): this library processes strips as a stream and does not retain the coefficients of the whole image for this purpose.
- `MaxFileSize` supports JPEG, lossy and near-lossless WebP, PNG, GIF and JPEG-compressed TIFF (and registered formats that provide `Quality`), not lossless WebP, BMP, other TIFF or animations.

## Versioning

The API was settled once, after 0.1.0 (a few names changed then: see "Upgrading from 0.1.0" in the [changelog](docs/CHANGELOG.md)). From then on, releases only add to it: nothing exported is removed, renamed or changed in its signature, which every push checks with `apidiff`; the basic API's signatures, the fields of its options and results and the values of its constants are also fixed at compile time by `tests/api_lock_test.go`. Output may change where it becomes more faithful (as JPEG files becoming libjpeg-turbo's); such changes are listed under "Upgrading" in the [changelog](docs/CHANGELOG.md). An interface that code of your own implements (an `Operation`, a `pipeline.Source`) never gains a method: new capabilities come as optional interfaces. Invisible watermarks made by a release are read by every later one (`tests/watermark_compat_test.go` reads marks made by 0.2.0). A release that has to break the API would be a new major version.

## Contributing

Issues and pull requests are welcome; see the [contributing guide](.github/CONTRIBUTING.md).

## License

Released under the MIT License ([LICENSE](LICENSE)). Code adapted from the Go standard library and `golang.org/x/image` retains their BSD license ([licenses/LICENSE-GO](licenses/LICENSE-GO)); the parts ported from libjpeg-turbo (the JPEG decoder's inverse DCTs and upsampling, the encoder's forward DCT, colour conversion and downsampling) and the trellis quantization ported from mozjpeg retain their licenses ([licenses/LICENSE-LIBJPEG-TURBO](licenses/LICENSE-LIBJPEG-TURBO)); the blur and unsharp mask ported from Pillow retain its MIT-CMU license ([licenses/LICENSE-PILLOW](licenses/LICENSE-PILLOW)); the parts of the lossy WebP encoder and of the WebP decoder's colour conversion ported from libwebp retain its BSD license and patent grant ([licenses/LICENSE-LIBWEBP](licenses/LICENSE-LIBWEBP)); the JPEG quantization tables after jpegli retain its BSD license ([licenses/LICENSE-JPEGLI](licenses/LICENSE-JPEGLI)); the DEFLATE compressor ported from libdeflate retains its MIT license ([licenses/LICENSE-LIBDEFLATE](licenses/LICENSE-LIBDEFLATE)), its block splitting after zopfli the Apache License 2.0 ([licenses/LICENSE-ZOPFLI](licenses/LICENSE-ZOPFLI)), and the PNG row filter search after oxipng its MIT license ([licenses/LICENSE-OXIPNG](licenses/LICENSE-OXIPNG)). [licenses/THIRD-PARTY.md](licenses/THIRD-PARTY.md) lists the origin of each file.
