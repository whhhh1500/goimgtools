English | [中文](zh-CN/usage.md)

# Usage

## Installation and basic usage

```bash
go get github.com/whhhh1500/goimgtools
```

```go
import "github.com/whhhh1500/goimgtools"
```

Requires Go 1.26 or later (the minimum of the `golang.org/x/image` dependency). PNG files written with Go 1.26 and Go 1.27 differ in their bytes but not in their pixels, as Go 1.27 rewrote `compress/flate` (but for levels 8 and 9, which have a compressor of their own). Extensions additionally import `github.com/whhhh1500/goimgtools/pipeline`.

The API uses method chaining similar to [sharp](https://sharp.pixelplumbing.com/): open an input, append operations, select an output format, write. Operations run in the order they are called; errors are returned by the final step.

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

### Inputs

| Method | Description |
|---|---|
| `Open(path)` / `FromBytes(b)` / `FromReader(r)` | A file, bytes in memory, a stream |
| `Text(text, TextOptions{...})` | Renders text as an image, for use as a watermark or as output in its own right (see [Text](#text)) |
| `FromImage(img)` | An `image.Image` already in memory |
| `FromSource(open)` | A caller-supplied pixel stream, such as a computed image or pixels read from another source (see [Extending](extending.md)) |

### Operations

| Method | Description |
|---|---|
| `Resize(w, h, ResizeOptions{...})` | Resizes. A `w` or `h` of 0 is calculated proportionally |
| `Extract(left, top, w, h)` | Crops |
| `Rotate()` | Corrects the orientation automatically from EXIF (no effect when no orientation information is present) |
| `Rotate(angle)` | Rotates clockwise by a multiple of 90 |
| `RotateBy(degrees, background)` | Rotates clockwise by any angle (negative values rotate anticlockwise); the canvas is enlarged to contain the whole image and the corners are filled with `background` (`nil` for black, `color.Transparent` for transparent); uses bilinear interpolation. Multiples of 90 are handled as `Rotate`, without interpolation |
| `Flip()` / `Flop()` | Mirrors top to bottom / mirrors left to right |
| `Trim(threshold)` | Removes edges on all four sides that match the colour of the top-left pixel (a pixel is considered different only if its red, green, blue or alpha channel differs by more than `threshold`; a value of 10 absorbs JPEG noise, as in sharp) |
| `Extend(top, right, bottom, left, background)` | Adds borders on all four sides; widths are given in CSS order: top, right, bottom, left |
| `Grayscale()` | Converts to grayscale: Rec. 601 luma rounded to the nearest level, byte-for-byte identical to Pillow's `convert("L")`; for JPEG, the luma stored in the file is used directly. Opaque images are written as single-channel grayscale files |
| `Modulate(brightness, saturation, hue)` | Multiplies brightness and saturation by the given factors and rotates the hue by the given number of degrees, in the LCh colour space (as sharp does); every pixel is within 1 of sharp's output. `Modulate(1.2, 0.8, 0)` increases brightness by 20% and makes colours slightly paler |
| `Negate()` | Every colour its opposite (255 − level); transparency is kept. Byte-for-byte identical to sharp's `negate({alpha: false})` |
| `Linear(a, b)` | Every red, green and blue level v becomes a·v + b, the fraction dropped and kept within 0–255, as sharp's `linear` (byte-for-byte identical): `Linear(1.2, -25)` raises the contrast |
| `Gamma(g)` | Every level v becomes 255·(v/255)^(1/g), as ImageMagick's `-gamma`: above 1 brightens the middle levels, below 1 darkens them; black and white stay |
| `Threshold(level)` | Black and white: pixels whose grey (of their linear light, as sharp's) is `level` or more become white. Byte-for-byte identical to sharp's `threshold` for images without transparency; opaque images are written as single-channel files |
| `Tint(color)` | The colour at the brightness of each pixel, less of it towards black and white (sharp's `tint`; within 2 levels of its output): `Tint(color.RGBA{112, 66, 20, 255})` gives sepia |
| `Normalize(NormalizeOptions{Lower, Upper})` | Stretches the lightness (L* in L*a*b*) so that the darkest `Lower` percent become black and the lightest `100-Upper` percent white (1 and 99 by default), keeping the colours' hue and saturation; sharp's `normalise`, within 1 level (its percentiles reproduced exactly). Reads the whole image first and keeps it in working storage |
| `CLAHE(CLAHEOptions{Width, Height, MaxSlope})` | Local contrast (contrast-limited adaptive histogram equalization), bringing out detail in dark and light areas: each level becomes its rank within the `Width` × `Height` window around the pixel, each level of the window counting at most `MaxSlope` pixels (3 by default). Byte-for-byte identical to sharp's `clahe` for images without transparency; a 64×64 window on 24 MP takes 18s on one core, 7s on four (sharp: 7.6s, 8.7s) |
| `Median(size)` | Median filter: each channel of each pixel the middle value of the size × size square around it (odd, 3–99), removing specks while keeping edges. Byte-for-byte identical to Pillow's `MedianFilter` and sharp's `median` |
| `Convolve(Convolution{Width, Height, Weights, Scale, Offset})` | A custom kernel of odd sides up to 99: the weighted sum of the window, divided by `Scale` (the sum of the weights by default), plus `Offset`. Byte-for-byte identical to sharp's `convolve`; e.g. weights `0,-1,0,-1,5,-1,0,-1,0` sharpen, `-1,-1,-1,-1,8,-1,-1,-1,-1` with `Scale: 1` find edges |
| `Blur(sigma)` | Gaussian blur; sigma must be greater than 0 and at most 1000 pixels. Uses Pillow's `GaussianBlur` algorithm; running time is independent of sigma |
| `Sharpen()` | Mild sharpening (sharp's 3×3 convolution kernel), suited to use after downscaling |
| `Sharpen(SharpenOptions{Sigma, Amount, Threshold})` | Unsharp mask (Pillow's `UnsharpMask`); `Amount` defaults to 1.5 |
| `Flatten(color)` | Removes transparency by flattening onto a background colour |
| `Composite(overlay, CompositeOptions{...})` | Composites an overlay or watermark (see [Watermarks](#watermarks)) |
| `Animated()` | Processes every frame of an animated GIF, WebP or PNG (APNG) and writes an animated GIF or WebP. A WebP's frames are drawn as libwebp's animation decoder draws them (blended or not, cleared or not), pixel for pixel; an APNG's as its specification says. A still image passes unchanged; an animation that cannot be kept (one to another format) is an error wrapping `errors.ErrUnsupported`. A WebP stores the first frame whole and later frames as the changed rectangle. In a GIF, all frames share a single palette chosen from the image content (`Colors` and `NoDither` in `GIFOptions` still apply; if the image has no more colours than that, the colours are kept unchanged), and from the second frame onwards only the changed region is stored (see [Compression](compression.md)) |
| `Pixels(fn)` | A custom per-pixel operation: `fn` receives 8-bit NRGBA data strip by strip and modifies it in place (see [Extending](extending.md)) |
| `Pipe(op)` | Inserts an arbitrary custom operation at this point in the chain (see [Extending](extending.md)) |
| `Apply(steps...)` | Calls a group of steps, defined as a function, in the middle of the chain |

`ResizeOptions`:

- `Fit`: `FitInside` (default; scales proportionally to fit within the box, without cropping or distortion), `FitCover` (fills the box, then crops the excess around the centre; sharp's default), `FitContain` (scales proportionally to fit within the box, then pads with `Background` to exactly w×h; black by default), `FitOutside`, `FitFill` (stretches).
- `WithoutEnlargement`: disables enlargement.
- `Kernel`: `KernelCubic` (default, Catmull-Rom), `KernelNearest`, `KernelLinear`, `KernelMitchell`, `KernelLanczos2`, `KernelLanczos3`.
- `Filter`: a custom resize filter (`&goimgtools.Filter{Support, At}`); if set, it replaces `Kernel`.
- `FastShrinkOnLoad`: performs an initial shrink of JPEGs during decoding (see [Shrinking JPEGs while decoding](performance.md#shrinking-jpegs-while-decoding-optional)).

A grayscale image remains grayscale (single-channel) after resizing and is not converted to colour.

Blur, sharpening, modulation and extension are streamed row by row and require only a few additional rows of memory (about 6×sigma additional rows for blur, and a further 3×sigma rows for unsharp masking; data beyond the memory share is written to a temporary file). `Trim` and `RotateBy` require the whole image: the pixels are first staged in working storage (a temporary file when they exceed the memory share) and then read again; consequently, when the chain contains `Trim`, `OutputSize` must also read the pixels once. Images with an alpha channel are blurred and sharpened using premultiplied alpha, so that colour from transparent areas does not bleed into neighbouring pixels.

### Output

| Method | Description |
|---|---|
| `JPEG(quality, JPEGOptions{Optimize, Progressive, Trellis, QuantTable})` | Quality 1–100; 0 means 80. `Optimize` and `Progressive` reduce file size without changing pixels; `Trellis` and `QuantTable` (0–8, as mozjpeg's `-quant-table`, or `QuantTableJpegli`) change pixels (see [Compression](compression.md)) |
| `PNG(PNGOptions{CompressionLevel, Colors, NoDither})` | Compression level from 1 (fastest) to 9 (smallest; 8 and 9 search for the smallest file, at about 10 and 20 times the time of level 7); `Colors` reduces the image to at most 256 colours (see [Compression](compression.md)) |
| `WebP(WebPOptions{Quality, Lossless, Effort})` | Lossy (quality 1–100, default 75) or lossless; `Effort` from 1, fastest, to 9, smallest, default 5 (for lossy, 5 already does all there is but for transparency, 1 takes about half the time) |
| `GIF(GIFOptions{Colors, NoDither, FixedPalette})` | Up to 256 colours, selected from the image content; `FixedPalette` uses the standard library's fixed palette instead |
| `BMP()` | |
| `TIFF(TIFFOptions{Compression})` | Uncompressed or `TIFFDeflate` |
| `ToFormat(name, options)` | Selects the output format by name (`"jpeg"`, `"png"`, `".tif"`…, and registered formats); `options` is the format's options struct and may be omitted |
| `MaxFileSize(n)` | Limits the output to n bytes, automatically selecting the highest quality that fits (JPEG, lossy WebP, and registered formats that provide `Quality`). With `Animated()` on an animation it is an error wrapping `errors.ErrUnsupported`; a GIF of one frame is handled as a still image |
| `ToFile(path)` / `ToWriter(w)` / `ToBytes()` | Runs the chain and writes the result. Without an explicit format, `ToFile` uses the file extension; the output file is produced only if the entire run succeeds |
| `Metadata()` | Reads only the file header and describes the **input**: format, stored width and height, colour type (grayscale / palette / RGB / YCbCr / CMYK) and bit depth, presence of transparency, EXIF orientation, presence of ICC / EXIF data. Unaffected by the operations on the chain |
| `Frames()` | The number of frames in the input, the duration of one playthrough, and the loop count (only GIF can have more than one frame). Reads the whole file without decoding pixels |
| `OutputSize()` | The dimensions of the **result**: width and height after all operations on the chain, computed without decoding pixels |
| `ToImage()` | Runs the chain and returns the whole `image.Image` (the full image is held in memory and counts towards the memory limit) |
| `Stream(env)` | Runs the chain and returns the result's pixel stream without writing a file (see [Extending](extending.md)) |
| `Formats()` / `FormatByName(s)` | Lists the supported formats and their extensions; looks up a format by name or extension (`"jpeg"`, `"JPG"` and `".tif"` are all recognised) |
| `RegisterFormat(codec)` / `CodecByName(s)` / `UnregisterFormat(name)` | Adds a format, or retrieves a format's read and write functions for modification; unregistering restores the built-in implementation (see [Extending](extending.md)) |

When no output format is specified and no extension is available, the output format is the same as the input; text input is written as PNG by default.

### Metadata and resource control

| Method | Description |
|---|---|
| `KeepExif()` | Copies the input's EXIF data (camera, time, GPS) to the output. Discarded by default |
| `StripICC()` | Discards the ICC colour profile. Preserved by default, since wide-gamut photographs would otherwise show a colour shift |
| `ToSRGB()` | Converts colours to sRGB using the input's colour profile; the output carries no profile. Colours are then correct even in programs that ignore profiles, and the file is slightly smaller. Applied automatically when converting to GIF or BMP, which cannot carry a profile |
| `MemoryLimit(bytes)` | Working memory limit; 64MB by default |
| `Threads(n)` | Maximum number of cores used by a single run; default 1, up to the number of CPU cores (see [CPU and threads](performance.md#cpu-and-threads)) |
| `SetDefaultThreads(n)` | Program-wide default thread count (used by runs that do not call `Threads`), so that it need not be specified at every call site |
| `TempDir(dir)` | Directory for temporary files; the system temporary directory by default |
| `LimitInputPixels(n)` | Limit on the number of input pixels; default 16383×16383 (as in sharp). For an animated GIF or WebP with `Animated()`, the limit applies to the total pixels of all frames |
| `LimitOutputPixels(n)` | Limit on the number of pixels in the image produced by any step of the chain (and therefore in the result); same default as above. Prevents a small file from being enlarged into a result that fills the disk and takes hours to compute |
| `WithContext(ctx)` | Stops processing when `ctx` is cancelled or times out, and returns `ctx.Err()` |
| `Clone()` | Copies the recipe, so that several results can be derived from the same starting point |

ICC colour profiles are read and written in JPEG, PNG, WebP and TIFF; EXIF is read and written in JPEG, PNG and WebP (TIFF stores EXIF in a separate directory, which is neither read nor written). GIF and BMP cannot carry a colour profile, so when converting to these formats, colours are converted to sRGB using the input's profile (measured: without conversion, a Display P3 image shows a colour difference of up to 17.6 and appears visibly washed out). After `Rotate()` corrects the orientation, the orientation in the preserved EXIF is set to "upright".

Conversion supports profiles described by three primaries and tone curves (cameras, phones, Display P3, Adobe RGB and ProPhoto all belong to this category); every value is within 1 of LittleCMS (relative colorimetric, colours outside sRGB clipped). Lookup-table profiles (uncommon, mostly used for printers) are preserved unchanged and not converted.

## Server-side use

**One recipe, several results.** Methods on the chain modify the same `Image` and return it; they do not copy it. To derive several results from the same starting point, call `Clone()` first:

```go
base := goimgtools.Open("in.jpg").Rotate()
thumb := base.Clone().Resize(200, 0)
large := base.Clone().Resize(1600, 0)
```

A completed recipe can be run repeatedly, including concurrently by multiple goroutines (input from `FromReader` can be read only once).

**Error classes.** Test with `errors.Is`:

| Error | Meaning | Typical handling in a service |
|---|---|---|
| `ErrFormat` | The input is not a readable image: a file of another type, an unsupported format or variant, or corrupt or truncated data | The uploaded file is invalid |
| `ErrPixelLimit` | The input pixel count exceeds `LimitInputPixels` (for animations, all frames combined), or the result of a step on the chain exceeds `LimitOutputPixels` | The image, or the requested size, is too large |
| `ErrMemoryLimit` | The image cannot be processed within the memory limit | Increase `MemoryLimit` or reject the request |
| `ErrFileSize` | The `MaxFileSize` target cannot be met even at the lowest quality | Reduce the dimensions first |
| `context.Canceled` / `DeadlineExceeded` | The `WithContext` context has ended | The request was cancelled |
| `*PanicError` (use `errors.As`) | A bug (panic) in the library or in extension code while processing this input; carries the cause and stack trace | Log and report it; the request fails and the program continues running |
| Others | Failure to read the input, a full disk, an invalid argument and so on; the original error is preserved | A system fault or a caller error |

Classification rule: an error reported by a decoder is classified as `ErrFormat` only if reading the input itself did not fail and the error is not caused by memory or temporary files. The tests verify this case by case with real images that have been truncated or corrupted: wherever the standard decoders report an error, this library reports `ErrFormat`; a connection dropped midway yields the connection's own error.

**Cancellation.** `WithContext(ctx)` is checked once between strips (a few rows of pixels) during decoding and encoding; processing returns shortly after cancellation, and a partially written file is deleted.

**Permissions of the written file.** `ToFile` first writes to a hidden file in the same directory and renames it once all steps have succeeded, so no partially written result is left behind. This file is created with the permissions of an ordinary new file (typically `0644` on Linux, determined by the process umask); when it replaces an existing file, it retains that file's permissions. It is not created with the owner-only `0600` permissions used for temporary files, since a web server running as another user would otherwise be unable to read the processed image.

**Concurrency and total memory.** Each run has its own memory budget, independent of other runs; the library itself does not limit the number of concurrent runs. Processing N images concurrently uses approximately N times the memory and CPU of a single image (typically 6–15MB per image, at most `MemoryLimit`). To bound the total, limit concurrency with a semaphore:

```go
var slots = make(chan struct{}, 4) // at most 4 images concurrently

func handle(ctx context.Context, in io.Reader, out io.Writer) error {
    select {
    case slots <- struct{}{}:
        defer func() { <-slots }()
    case <-ctx.Done():
        return ctx.Err()
    }
    return goimgtools.FromReader(in).WithContext(ctx).Resize(1600, 1600).JPEG(80).ToWriter(out)
}
```

Measured (batch mode, 42 photographs): peak memory 19.5MB at concurrency 3, 32MB at concurrency 8, and 46MB with all 42 processed at once.

**Robustness: no crashes, and no single file can bring down the service.**

- `Composite(nil)`, `Flatten(nil)`, two sets of options passed to the same method, out-of-range quality or colour counts and similar cases all return an error at run time.
- Any panic during a run is returned as a `*PanicError`, including panics in worker goroutines when multithreaded; temporary files and partially written output are cleaned up as usual.
- Input pixels, the pixels of each step's result, and the total pixels of all frames of an animated GIF are all bounded (see the table above). The first two are rejected before any computation begins; for an animated GIF, the frames are counted first, and if the total exceeds the limit, the input is rejected before any frame is processed (with `FixedPalette`, processing stops at the frame that exceeds the limit).
- These guarantees are enforced by fuzz tests (see [Testing](testing.md)): arbitrary bytes are read as images and arbitrary parameter combinations are run through the entire chain, with the requirements that no panic occurs, the memory budget is never exceeded, the entire budget is returned, and no temporary files are left behind.

## Watermarks

`Composite` positions the overlay in three steps, each of which is optional:

1. **Select a position on the base image**: a nine-point grid position `Gravity` (top left by default), plus pixel offsets `Left`, `Top`, plus ratio offsets `LeftRatio`, `TopRatio` (as fractions of the base image's width and height).
2. **Place the watermark's anchor at this position**: `Anchor` uses the same nine names as `Gravity` (`NorthWest` … `Center` … `SouthEast`) to select one of the watermark's corners, edge midpoints or its centre; alternatively, `CustomPosition` with `AnchorX`, `AnchorY` (in pixels from the watermark's top-left corner) specifies any point on the watermark. If unset, the anchor takes the same name as `Gravity`, so `Gravity: SouthEast, Left: -24, Top: -24` means "bottom-right corner with a 24-pixel margin".
3. **Rotate about the pivot**: `Angle` is in degrees clockwise and may take any value; `Pivot` is the pivot point, likewise one of the nine names or `CustomPosition` with `PivotX`, `PivotY`; it defaults to the anchor.

Additional options:

- `Opacity`: 0–1, overall opacity, multiplied by the watermark's own alpha; if unset, the watermark is used unchanged.
- `Blend`: how the overlay's colours mix with the image's: `BlendOver` (the default, as `image/draw`'s `Over`), `BlendMultiply`, `BlendScreen`, `BlendOverlay`, `BlendDarken`, `BlendLighten`, `BlendColorDodge`, `BlendColorBurn`, `BlendHardLight`, `BlendSoftLight`, `BlendDifference`, `BlendExclusion`; computed as sharp's `blend` (libvips' composite), within 1 level on an opaque image. On the command line: `-watermark-blend multiply`.
- `Tile`, `TileGapX`, `TileGapY`: tiles the watermark across the whole image with configurable gaps. With `Angle`, each watermark is rotated individually, while rows and columns remain horizontal and vertical.
- `WidthRatio`, `HeightRatio`: first scales the watermark to this fraction of the base image's width (height), preserving its aspect ratio; when both are given, it is fitted within both. When batch-processing images of varying sizes, the watermark therefore appears the same size on every image. On the command line: `-watermark-width 20%`.

```go
// Semi-transparent diagonal text watermark tiled across the whole image
goimgtools.Open("in.jpg").
    Composite(
        goimgtools.Text("© Sample watermark", goimgtools.TextOptions{Size: 26, Color: color.White}),
        goimgtools.CompositeOptions{Tile: true, TileGapX: 60, TileGapY: 50, Angle: -30, Opacity: 0.25},
    ).ToFile("out.jpg")

// Watermark centre at 1/3 of the base image's width and 1/3 of its height
Composite(logo, goimgtools.CompositeOptions{LeftRatio: 1.0 / 3, TopRatio: 1.0 / 3, Anchor: goimgtools.Center})

// Bottom-right corner, 15% of the base image's width regardless of its size
Composite(logo, goimgtools.CompositeOptions{Gravity: goimgtools.SouthEast, Left: -24, Top: -24, WidthRatio: 0.15})
```

The entire watermark is held in memory (counting towards the memory limit), so it should be a small image. See [sample-effects.png](sample-effects.png) for examples.

## Text

`Text` is an input, like `Open`, and may be followed by any operation.

```go
goimgtools.Text("First line\nSecond line Mixed", goimgtools.TextOptions{
    Fonts: []string{"Microsoft YaHei"},           // or a font file path
    Size:  48, Color: color.White,
    StrokeWidth: 3, StrokeColor: color.Black,     // stroke
    ShadowColor: color.RGBA{0, 0, 0, 180}, ShadowX: 4, ShadowY: 4, ShadowBlur: 3, // shadow
}).ToFile("text.png")
```

- **Fonts**: `Fonts` is a list tried in order for each character; if a font lacks a character, the next font is used. Each entry is a font file (`.ttf`, `.otf`, `.ttc`; for a collection file, `filename#1` selects the second font) or the name of an installed system font. `FontData` supplies font data held in memory.
- **Default fonts**: if none are specified, common Chinese fonts installed on the system are used first, with the built-in Go Regular (which has no Chinese glyphs) last. `DefaultFonts()` lists the default order on the current system; `SystemFonts()` lists all fonts installed on the system.
- **Layout**: font size, colour (an alpha component gives semi-transparent text), background colour (transparent by default), automatic line wrapping at `Width`, left / centre / right alignment, line spacing, padding.
- **Stroke / shadow**: `StrokeWidth`, `StrokeColor`; `ShadowColor`, `ShadowX`, `ShadowY`, `ShadowBlur` (a zero offset with blur produces a glow). The image is enlarged automatically to accommodate these effects.
- **Limitations**: right-to-left text, text requiring ligature shaping, and vertical text are not supported. A character absent from all fonts is drawn as a missing-glyph box.

Font memory usage:

- Font files are not read in full; only the glyphs used by the text are read. Measured: Microsoft YaHei is 18.8MB; reading it in full takes 18.8MB, whereas this approach takes only 0.13MB.
- Fallback fonts are opened only when a preceding font lacks a character.
- Each open font counts as 256KB, and the rendering buffer as "6 × font size²" bytes, towards the memory limit (about 6MB for 1000-pixel text); exceeding the limit returns a memory-limit error.
- Text is rendered line by line, so only one line is held in memory.

## Inspecting and checking images

Tools that examine images without modifying them. On the command line they are subcommands; in the library, in addition to `Metadata()` and `Frames()` above, they are provided by the public subpackage `inspect` (implemented solely on the library's public API, and likewise processing row by row within bounded memory).

| Command line | Library | Description |
|---|---|---|
| `goimgtools info file...` | `img.Metadata()`, `img.Frames()` | One line per file: format, width and height, colour type and bit depth, pixel count, file size, frame count and duration of animations (GIF, WebP, APNG), EXIF orientation, presence of ICC / EXIF data. With `-json`, one line of JSON per file |
| `goimgtools check file...` | `inspect.Verify(img)` | Fully decodes the image once and reports files that are corrupt, truncated or not images. `info` reads only the file header and therefore cannot detect damage in the latter part of a file |
| `goimgtools compare a b` | `inspect.Compare(a, b)` | Compares two images pixel by pixel, including images in different formats: reports whether they are identical and, if not, the PSNR (higher means closer; above 40, differences are barely perceptible), the largest difference, and the number of differing pixels. `-min-psnr 35` serves as a quality gate: if the threshold is not met, the command exits with a non-zero status |
| `goimgtools stats file...` | `inspect.Stats(img)` | Examines every pixel: average colour, brightness, the few colours covering the largest area, whether the image is in fact grayscale, whether it actually contains transparent pixels, and the number of colours (when there are no more than 256). Used to choose a placeholder colour, to decide between dark and light text, or to determine whether the image can be stored as grayscale or with a palette |
| `goimgtools hash file...` | `inspect.Hash(img)` | A 64-bit perceptual hash: the same image at a different size, format or compression rate differs by only a few bits, while different images differ in about half of the bits. `-similar 10` lists files that are close to each other, for finding duplicate images |
| `goimgtools exif file...` | `inspect.ReadExif(img)` | Camera, lens, capture time, exposure settings, GPS location and other EXIF fields |
| `goimgtools formats` | `Formats()`, `CodecByName()` | The supported formats, their extensions, and whether each can be read or written |

`info`, `stats` and `exif` all accept `-json`, which writes one line of JSON per file.

```bash
goimgtools info photo.jpg anim.gif
```

```
photo.jpg: jpeg 6000x4000, ycbcr 8-bit, 24.0 MP, 5.6MB, EXIF orientation 6 (upright it is 4000x6000), ICC profile, EXIF
anim.gif: gif 400x300, palette 8-bit, 0.1 MP, 1.2MB, 24 frames in 2.40s, plays for ever
```

```go
// Check that an uploaded file is an intact image
if err := inspect.Verify(goimgtools.FromBytes(upload)); err != nil {
    // errors.Is(err, goimgtools.ErrFormat): the file is invalid
}

// Difference between the compressed image and the original
d, err := inspect.Compare(goimgtools.Open("orig.png"), goimgtools.Open("small.jpg"))
fmt.Println(d) // PSNR 41.87 dB, largest difference 23 of 255, 96.4% of the pixels differ
```

```go
// Duplicate detection: hashes within 10 bits of each other most likely belong to the same image
a, _ := inspect.Hash(goimgtools.Open("a.jpg"))
b, _ := inspect.Hash(goimgtools.Open("b.webp"))
if a.Distance(b) <= 10 { /* different versions of the same image */ }

// Main colour of an image, for use as a placeholder colour
st, _ := inspect.Stats(goimgtools.Open("photo.jpg"))
fmt.Println(inspect.Hex(st.Average), st.Brightness) // #2e2f29 0.18

// Camera and capture information
if e, ok, _ := inspect.ReadExif(goimgtools.Open("photo.jpg")); ok {
    fmt.Println(e.Model, e.Taken, e.HasGPS)
}
```

Measured (single thread): `info` and `exif` read only the file header and take about 0.015 seconds; on a 24-megapixel JPEG, `check` and `hash` take about 0.2 seconds, `stats` about 0.34 seconds, and `compare` on two such images about 0.5 seconds.

Notes:

- The hash reflects the light-and-dark layout of the whole picture, so it does not recognise an image after cropping, rotation or mirroring; images without such a layout (solid colours, smooth gradients from one side to the other) are indistinguishable to it.
- EXIF is read from JPEG, PNG and WebP. When writing files, the library discards EXIF (including location) by default; to preserve it, call `KeepExif()` explicitly.
- All these tools read images row by row within bounded memory; `stats` retains only a colour table of about 100KB, regardless of image size.

`Compare` compares the pixels of the two recipes after processing, so two processing approaches can be compared directly without first writing files; for example, the same image resized with two algorithms: `inspect.Compare(a.Resize(800, 0), b.Resize(800, 0, goimgtools.ResizeOptions{Kernel: goimgtools.KernelLinear}))`. The output format (`JPEG()`, `WebP()` and so on) is not part of the comparison; to measure the loss caused by encoding, call `ToBytes()` first, then read the result back with `FromBytes` and compare.
