English | [中文](zh-CN/usage.md)

# Usage

## Installation and basics

```bash
go get github.com/whhhh1500/goimgtools
```

```go
import "github.com/whhhh1500/goimgtools"
```

Requires Go 1.27 (the `golang.org/x/image` dependency needs 1.26 or later). To write extensions, also import `github.com/whhhh1500/goimgtools/pipeline`.

The API follows the chained style of [sharp](https://sharp.pixelplumbing.com/): input → chain of operations → choose an output format → write. Operations run in the order they are called; errors are returned at the last step.

```go
err := goimgtools.Open("in.jpg").
    Rotate().                              // upright according to EXIF orientation
    Resize(1536, 0).                       // width 1536, height in proportion
    Flatten(color.White).                  // white background under transparent areas
    Composite(goimgtools.Open("logo.png"), goimgtools.CompositeOptions{
        Gravity: goimgtools.SouthEast, Left: -24, Top: -24, // bottom-right corner, 24-pixel margin
    }).
    JPEG(85).
    ToFile("out.jpg")
```

### Inputs

| Method | What it does |
|---|---|
| `Open(path)` / `FromBytes(b)` / `FromReader(r)` | File, memory, stream |
| `Text(text, TextOptions{...})` | Draws text as an image; use it as a watermark or output it on its own (see [Text](#text)) |
| `FromImage(img)` | An `image.Image` already in memory |
| `FromSource(open)` | A pixel stream you supply: a computed image, pixels read from elsewhere (see [Extending](extending.md)) |

### Operations

| Method | What it does |
|---|---|
| `Resize(w, h, ResizeOptions{...})` | Resize. 0 for `w` or `h` means it is calculated in proportion |
| `Extract(left, top, w, h)` | Crop |
| `Rotate()` | Rotates upright automatically according to EXIF orientation (does nothing when there is no orientation information) |
| `Rotate(angle)` | Rotates clockwise, by a multiple of 90 |
| `RotateBy(degrees, background)` | Rotates clockwise by any angle (negative is anticlockwise); the canvas grows to hold the whole image and the corners are filled with `background` (`nil` black, `color.Transparent` transparent); bilinear interpolation. For multiples of 90 it is `Rotate`, without interpolation |
| `Flip()` / `Flop()` | Mirror top to bottom / mirror left to right |
| `Trim(threshold)` | Removes the edges on all four sides that are the same colour as the top-left pixel (a pixel counts as different only when its red, green, blue or alpha channel differs by more than `threshold`; 10 swallows JPEG noise, same as sharp) |
| `Extend(top, right, bottom, left, background)` | Adds borders on all four sides; widths in CSS top, right, bottom, left order |
| `Grayscale()` | Converts to grayscale: Rec. 601 luma rounded to the nearest level, byte-for-byte identical to Pillow's `convert("L")`; JPEGs use the luma stored in the file directly. Opaque images are output as single-channel grayscale files |
| `Modulate(brightness, saturation, hue)` | Multiplies brightness and saturation by a factor and turns the hue by a number of degrees, in the LCh colour space (sharp's approach); every pixel is within 1 of sharp: `Modulate(1.2, 0.8, 0)` is 20% brighter and a little paler |
| `Blur(sigma)` | Gaussian blur, sigma greater than 0 and at most 1000 pixels; Pillow's `GaussianBlur` algorithm, running time does not change with sigma |
| `Sharpen()` | Light sharpening (sharp's 3×3 convolution kernel), suited to use after downscaling |
| `Sharpen(SharpenOptions{Sigma, Amount, Threshold})` | Unsharp mask (Pillow's `UnsharpMask`); `Amount` defaults to 1.5 |
| `Flatten(color)` | Removes transparency by flattening onto a background colour |
| `Composite(overlay, CompositeOptions{...})` | Composite / watermark (see [Watermarks](#watermarks)) |
| `Animated()` | Processes every frame of an animated GIF and outputs an animated GIF (takes effect for GIF in, GIF out). All frames share one palette chosen from the picture (`Colors` and `NoDither` in `GIFOptions` still apply; when the image has no more colours than that, they are kept unchanged), and from the second frame on only the changed part is stored (see [Compression](compression.md)) |
| `Pixels(fn)` | Your own per-pixel operation: `fn` receives 8-bit NRGBA strip by strip and modifies it in place (see [Extending](extending.md)) |
| `Pipe(op)` | Any operation of your own, inserted at this point in the chain (see [Extending](extending.md)) |
| `Apply(steps...)` | Write a group of steps as a function and call it in the middle of the chain |

`ResizeOptions`:

- `Fit`: `FitInside` (default; scales proportionally to fit inside the box, no cropping or distortion), `FitCover` (fills the box, then crops the excess around the centre; sharp's default), `FitContain` (scales proportionally to fit inside the box, then pads with `Background` to exactly w×h; black by default), `FitOutside`, `FitFill` (stretch).
- `WithoutEnlargement`: never enlarge.
- `Kernel`: `KernelCubic` (default, Catmull-Rom), `KernelNearest`, `KernelLinear`, `KernelMitchell`, `KernelLanczos2`, `KernelLanczos3`.
- `Filter`: your own resize filter (`&goimgtools.Filter{Support, At}`); when given, it replaces `Kernel`.
- `FastShrinkOnLoad`: shrink JPEGs first while decoding (see [Shrinking JPEGs while decoding](performance.md#shrinking-jpegs-while-decoding-optional)).

A grayscale image stays grayscale (single-channel) after resizing; it does not become colour.

Blur, sharpen, modulate and extend all stream through row by row and use only a few extra rows of memory (blur about 6×sigma rows more, unsharp mask another 3×sigma rows; beyond the memory share they go to a temporary file). `Trim` and `RotateBy` must see the whole image first: the pixels go into working storage (a temporary file when they exceed the memory share) and are read again; so when the chain contains `Trim`, `OutputSize` also has to read the pixels once. Images with an alpha channel are blurred and sharpened with premultiplied alpha, so colour from transparent areas does not bleed into neighbouring pixels.

### Output

| Method | What it does |
|---|---|
| `JPEG(quality, JPEGOptions{Optimize, Progressive})` | Quality 1–100; 0 means 80. The two options make the file smaller without changing pixels (see [Compression](compression.md)) |
| `PNG(PNGOptions{CompressionLevel, Colors, NoDither})` | Compression level 1 (fastest) to 9 (smallest); `Colors` reduces to at most 256 colours (see [Compression](compression.md)) |
| `WebP(WebPOptions{Quality, Lossless, Effort})` | Lossy (quality 1–100, default 75) or lossless (`Effort` 1 fastest to 9 smallest, default 5) |
| `GIF(GIFOptions{Colors, NoDither, FixedPalette})` | Up to 256 colours, chosen from the image content; `FixedPalette` uses the standard library's fixed palette instead |
| `BMP()` | |
| `TIFF(TIFFOptions{Compression})` | Uncompressed or `TIFFDeflate` |
| `ToFormat(name, options)` | Chooses the output format by name (`"jpeg"`, `"png"`, `".tif"`…, and registered formats); the options are that format's options struct and can be left out |
| `MaxFileSize(n)` | Output no larger than n bytes: automatically uses the highest quality that fits (JPEG, lossy WebP, and registered formats with a `Quality`) |
| `ToFile(path)` / `ToWriter(w)` / `ToBytes()` | Runs and writes. When no format is given, `ToFile` goes by the extension; the file is left behind only if the whole run succeeds |
| `Metadata()` | Reads only the file header and describes the **input**: format, stored width and height, colour type (grayscale / palette / RGB / YCbCr / CMYK) and bit depth, whether it has transparency, EXIF orientation, whether it has ICC / EXIF. Not affected by the operations on the chain |
| `Frames()` | How many frames the input has, how long one playthrough takes, how many times it loops (only GIF can have more than one frame). Reads the whole file but does not decode pixels |
| `OutputSize()` | How big the **result** will be: the width and height after all operations on the chain, without decoding pixels |
| `ToImage()` | Runs and returns the whole `image.Image` (the whole image is in memory and counts towards the memory limit) |
| `Stream(env)` | Runs and returns the result's pixel stream without writing a file (see [Extending](extending.md)) |
| `Formats()` / `FormatByName(s)` | The supported formats and their extensions; look up by name or extension (`"jpeg"`, `"JPG"` and `".tif"` are all recognised) |
| `RegisterFormat(codec)` / `CodecByName(s)` / `UnregisterFormat(name)` | Add a format, or get a format's read and write functions to modify them; after unregistering, the built-in implementation comes back (see [Extending](extending.md)) |

When no output format is given and there is no extension to go by, the output format is the same as the input; text input outputs PNG by default.

### Metadata and resources

| Method | What it does |
|---|---|
| `KeepExif()` | Carries the input's EXIF (camera, time, GPS) to the output. Dropped by default |
| `StripICC()` | Drops the ICC colour profile. Kept by default, because otherwise wide-gamut photos shift in colour |
| `ToSRGB()` | Converts colours to sRGB according to the input's colour profile, and the output no longer carries a profile: colours are right even in programs that ignore profiles, and the file is a little smaller. Done automatically when converting to GIF or BMP (which cannot carry a profile) |
| `MemoryLimit(bytes)` | Working memory limit, 64MB by default |
| `Threads(n)` | The maximum number of cores one run uses; default 1, up to the number of CPU cores (see [CPU and threads](performance.md#cpu-and-threads)) |
| `SetDefaultThreads(n)` | The default thread count for the whole program (used by runs that do not call `Threads`), so you do not have to write it at every call site |
| `TempDir(dir)` | Directory for temporary files; the system temporary directory by default |
| `LimitInputPixels(n)` | Limit on the number of input pixels; default 16383×16383 (same as sharp). For an animated GIF with `Animated()`, it is the total pixels of all frames |
| `LimitOutputPixels(n)` | Limit on the number of pixels of the image produced by any step on the chain (and therefore also of the result); same default as above. Stops a small file from being enlarged into a result that fills the disk and takes hours to compute |
| `WithContext(ctx)` | Stops processing when `ctx` is cancelled or times out, and returns `ctx.Err()` |
| `Clone()` | Copies the recipe, to derive several results from the same starting point |

Colour profiles (ICC) are read and written in JPEG, PNG, WebP and TIFF; EXIF is read and written in JPEG, PNG and WebP (TIFF keeps EXIF in its own directory, which is neither read nor written). GIF and BMP cannot carry a colour profile, so when converting to these two formats the colours are converted to sRGB according to the input's profile (measured: a Display P3 image without conversion has a colour difference of up to 17.6 and looks visibly washed out). After `Rotate()` makes the image upright, the kept EXIF has its orientation changed to "upright".

Conversion supports profiles described by three primaries plus tone curves (cameras, phones, Display P3, Adobe RGB and ProPhoto are all of this kind), and every value is within 1 of LittleCMS (relative colorimetric, colours outside sRGB clipped); lookup-table profiles (rare, mostly for printers) are kept as they are and not converted.

## Using it in a service

**One recipe, several results.** The methods on the chain modify the same `Image` and return it; they do not copy it. To make several results from the same starting point, call `Clone()` first:

```go
base := goimgtools.Open("in.jpg").Rotate()
thumb := base.Clone().Resize(200, 0)
large := base.Clone().Resize(1600, 0)
```

A finished recipe can be run repeatedly, and by several goroutines at the same time (input from `FromReader` can only be read once).

**Error classes.** Check with `errors.Is`:

| Error | Meaning | What a service usually does |
|---|---|---|
| `ErrFormat` | The input is not a readable image: another kind of file, an unsupported format or variant, corrupt or truncated data | The uploaded file has a problem |
| `ErrPixelLimit` | The input's pixel count exceeds `LimitInputPixels` (animated GIFs count all frames together), or the result of a step on the chain exceeds `LimitOutputPixels` | The image is too big, or the requested size is too big |
| `ErrMemoryLimit` | Cannot be processed within the memory limit | Raise `MemoryLimit` or reject |
| `ErrFileSize` | The `MaxFileSize` target cannot be reached even at the lowest quality | Reduce the dimensions first |
| `context.Canceled` / `DeadlineExceeded` | The `WithContext` context has ended | The request was cancelled |
| `*PanicError` (use `errors.As`) | A bug (panic) in the library or in extension code while processing this input; carries the cause and stack trace | Log and report it; this request fails, the program keeps running |
| Others | Reading the input failed, disk full, bad argument and so on; the original error is kept | System fault or the caller's mistake |

How it decides: an error reported by a decoder counts as `ErrFormat` only when "reading the input itself did not fail, and it is not a memory or temporary-file problem". The tests check this case by case on real images that were truncated or corrupted: where the standard decoders report an error, this library reports `ErrFormat`; a connection dropped midway reports the connection's own error.

**Cancellation.** `WithContext(ctx)` checks once between strips during decoding and encoding (a few rows of pixels), returns soon after cancellation, and deletes a half-written file.

**Who can read the written file.** `ToFile` first writes to a hidden file in the same directory and renames it once everything has succeeded, so no half-written result is left behind. This file is created with the permissions of an ordinary new file (usually `0644` on Linux, decided by the process's umask), and when it replaces an existing file it keeps that file's permissions. It is not `0600`, readable only by the owner, as temporary files are — otherwise a web server running as another user could not read the processed image.

**Concurrency and total memory.** Each run has its own memory budget, independent of the others; the library itself does not limit how many run at once. Processing N images at once uses roughly N times the memory and CPU of one image (one image usually takes 6–15MB, at most `MemoryLimit`). To limit the total, control concurrency with a semaphore:

```go
var slots = make(chan struct{}, 4) // at most 4 images at a time

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

Measured (batch mode, 42 photos): peak 19.5MB with concurrency 3, 32MB with concurrency 8, 46MB with all 42 at once.

**It does not crash, and one file cannot drag it down.**

- `Composite(nil)`, `Flatten(nil)`, passing two sets of options to the same method, out-of-range quality or colour counts and so on all return an error when run.
- Any panic during a run is turned into a returned `*PanicError`, including panics in worker goroutines when multithreaded; temporary files and half-written output are cleaned up as usual.
- Input pixels, the pixels of every step's result, and the total pixels of all frames of an animated GIF all have limits (see the table above). The first two are rejected before any computation starts; for an animated GIF the frames are counted first, and if the total is over the limit it is rejected without processing a single frame (with `FixedPalette`, processing stops at the frame that goes over).
- These are guarded by fuzz tests (see [Testing](testing.md)): arbitrary bytes are read as images and arbitrary parameter combinations are run through the whole chain, requiring no panic, no exceeding the memory budget, the whole budget returned, and no temporary files left behind.

## Watermarks

`Composite` positions the overlay in three steps, each of them optional:

1. **Choose a position on the base image**: a nine-point grid point `Gravity` (top-left by default) + pixel offsets `Left`, `Top` + ratio offsets `LeftRatio`, `TopRatio` (as a fraction of the base image's width and height).
2. **Put the watermark's anchor at this position**: `Anchor` uses the same nine names as `Gravity` (`NorthWest` … `Center` … `SouthEast`) to select one of the watermark's corners, edge midpoints or its centre; or use `CustomPosition` with `AnchorX`, `AnchorY` (pixels from the watermark's top-left corner) to pick any point on the watermark. When not set, the anchor has the same name as `Gravity`, so `Gravity: SouthEast, Left: -24, Top: -24` means "bottom-right corner with a 24-pixel margin".
3. **Rotate by an angle around the pivot**: `Angle` is in degrees clockwise, any value; `Pivot` is the pivot, again one of the nine names or `CustomPosition` with `PivotX`, `PivotY`; by default it is the anchor.

Also:

- `Opacity`: 0–1, overall opacity, multiplied by the watermark's own transparency; when not set, the watermark is used as it is.
- `Tile`, `TileGapX`, `TileGapY`: tile across the whole image, with adjustable gaps. With `Angle`, each watermark is rotated on its own, and the rows and columns stay horizontal and vertical.
- `WidthRatio`, `HeightRatio`: first scale the watermark to this fraction of the base image's width (height), keeping its aspect ratio; when both are given, it fits within both. When batch-processing images of different sizes, the watermark looks the same size on every image. On the command line, write `-watermark-width 20%`.

```go
// Tile semi-transparent diagonal text watermarks across the whole image
goimgtools.Open("in.jpg").
    Composite(
        goimgtools.Text("© Sample watermark", goimgtools.TextOptions{Size: 26, Color: color.White}),
        goimgtools.CompositeOptions{Tile: true, TileGapX: 60, TileGapY: 50, Angle: -30, Opacity: 0.25},
    ).ToFile("out.jpg")

// Watermark centre at 1/3 of the base image's width and 1/3 of its height
Composite(logo, goimgtools.CompositeOptions{LeftRatio: 1.0 / 3, TopRatio: 1.0 / 3, Anchor: goimgtools.Center})

// Bottom-right corner, width 15% of the base image, whatever the base image's size
Composite(logo, goimgtools.CompositeOptions{Gravity: goimgtools.SouthEast, Left: -24, Top: -24, WidthRatio: 0.15})
```

The whole watermark is held in memory (counting towards the memory limit), so it should be a small image. See [sample-effects.png](sample-effects.png) for samples.

## Text

`Text` is an input, like `Open`, and any operation can follow it.

```go
goimgtools.Text("First line\nSecond line Mixed", goimgtools.TextOptions{
    Fonts: []string{"Microsoft YaHei"},           // or a font file path
    Size:  48, Color: color.White,
    StrokeWidth: 3, StrokeColor: color.Black,     // outline
    ShadowColor: color.RGBA{0, 0, 0, 180}, ShadowX: 4, ShadowY: 4, ShadowBlur: 3, // shadow
}).ToFile("text.png")
```

- **Fonts**: `Fonts` is a list, tried in order for each character; when an earlier font lacks a character, a later one is used. Each entry is a font file (`.ttf`, `.otf`, `.ttc`; for a collection file, `filename#1` selects the second font) or the name of a font installed on the system. `FontData` passes font data held in memory.
- **Default fonts**: when none are given, common Chinese fonts on the system are used first, and the built-in Go Regular (no Chinese) last. `DefaultFonts()` lists the default order on the current system; `SystemFonts()` lists all fonts installed on the system.
- **Layout**: font size, colour (with alpha for semi-transparent text), background colour (transparent by default), automatic line wrapping at `Width`, left / centre / right alignment, line spacing, padding.
- **Outline / shadow**: `StrokeWidth`, `StrokeColor`; `ShadowColor`, `ShadowX`, `ShadowY`, `ShadowBlur` (zero offset plus blur gives a glow). The image is enlarged automatically to fit these effects.
- **Limitations**: right-to-left text, text that needs ligature shaping, and vertical text are not supported. A character that no font has is drawn as a missing-glyph box.

Memory rules for fonts:

- Font files are not read in whole; only the glyphs the text uses are read. Measured: Microsoft YaHei is 18.8MB; reading it whole takes 18.8MB, reading it this way takes only 0.13MB.
- Fallback fonts are opened only when an earlier font lacks a character.
- Each open font counts 256KB, and the drawing buffer counts "6 × font size²" bytes, towards the memory limit (about 6MB for 1000-pixel text); going over reports a memory-limit error.
- Text is rendered line by line, so memory holds only one line.

## Inspecting and checking images

A few tools that only look at images and do not change them. On the command line they are subcommands; in the library, besides `Metadata()` and `Frames()` above, there is a public subpackage `inspect` (written using only the library's public API; it likewise processes row by row with bounded memory).

| Command line | Library | What it does |
|---|---|---|
| `goimgtools info file...` | `img.Metadata()`, `img.Frames()` | One line per file: format, width and height, colour type and bit depth, pixel count, file size, frame count and duration of animated GIFs, EXIF orientation, whether it has ICC / EXIF. With `-json`, one line of JSON per file |
| `goimgtools check file...` | `inspect.Verify(img)` | Fully decodes the image once and reports files that are corrupt, truncated or not images at all. `info` reads only the file header, so it cannot tell when the second half of a file is broken |
| `goimgtools compare a b` | `inspect.Compare(a, b)` | Compares two images pixel by pixel, even in different formats: whether they are identical; if not, the PSNR (higher is closer; above 40 the eye can hardly tell), the largest difference, and how many pixels differ. `-min-psnr 35` works as a quality gate: if it is not reached, the command exits with a non-zero status |
| `goimgtools stats file...` | `inspect.Stats(img)` | Looks at every pixel: average colour, brightness, the few colours covering the largest area, whether the image is actually grayscale, whether it actually has transparent pixels, number of colours (when there are no more than 256). Use it to pick a placeholder colour, to choose dark or light text, or to judge whether the image can be stored as grayscale or palette |
| `goimgtools hash file...` | `inspect.Hash(img)` | A 64-bit "perceptual hash": the same image at a different size, format or compression rate differs by only a few bits; different images differ by about half. `-similar 10` lists files that are close to each other, to find duplicate images |
| `goimgtools exif file...` | `inspect.ReadExif(img)` | Camera, lens, time taken, exposure settings, GPS location and so on from EXIF |
| `goimgtools formats` | `Formats()`, `CodecByName()` | Which formats are supported, their extensions, and whether they can be read or written |

`info`, `stats` and `exif` all accept `-json`, which outputs one line of JSON per file.

```bash
goimgtools info photo.jpg anim.gif
```

```
photo.jpg: jpeg 6000x4000, ycbcr 8-bit, 24.0 MP, 5.6MB, EXIF orientation 6 (upright it is 4000x6000), ICC profile, EXIF
anim.gif: gif 400x300, palette 8-bit, 0.1 MP, 1.2MB, 24 frames in 2.40s, plays for ever
```

```go
// Is the uploaded file an intact image?
if err := inspect.Verify(goimgtools.FromBytes(upload)); err != nil {
    // errors.Is(err, goimgtools.ErrFormat): the file has a problem
}

// How far is the compressed image from the original?
d, err := inspect.Compare(goimgtools.Open("orig.png"), goimgtools.Open("small.jpg"))
fmt.Println(d) // PSNR 41.87 dB, largest difference 23 of 255, 96.4% of the pixels differ
```

```go
// Find duplicates: hashes within 10 bits of each other are most likely the same image
a, _ := inspect.Hash(goimgtools.Open("a.jpg"))
b, _ := inspect.Hash(goimgtools.Open("b.webp"))
if a.Distance(b) <= 10 { /* different versions of the same image */ }

// Take an image's main colour as a placeholder colour
st, _ := inspect.Stats(goimgtools.Open("photo.jpg"))
fmt.Println(inspect.Hex(st.Average), st.Brightness) // #2e2f29 0.18

// Camera and shooting information
if e, ok, _ := inspect.ReadExif(goimgtools.Open("photo.jpg")); ok {
    fmt.Println(e.Model, e.Taken, e.HasGPS)
}
```

Measured (single thread): `info` and `exif` read only the file header, about 0.015 seconds; on a 24-megapixel JPEG, `check` and `hash` take about 0.2 seconds, `stats` about 0.34 seconds, and `compare` of two such images about 0.5 seconds.

Notes:

- The hash looks at the light-and-dark layout of the whole picture, so it no longer recognises an image after cropping, rotation or mirroring; images with no light-and-dark layout (solid colours, smooth gradients from one side to the other) all look the same to it.
- EXIF is read from JPEG, PNG and WebP. When the library writes a file, it drops EXIF (including location) by default; to keep it, call `KeepExif()` explicitly.
- All these tools read images row by row with bounded memory; `stats` keeps only a colour table of about 100KB, regardless of image size.

`Compare` compares the pixels after the two recipes have been processed, so two ways of processing can be compared directly without writing files first, for example the same image with two resize algorithms: `inspect.Compare(a.Resize(800, 0), b.Resize(800, 0, goimgtools.ResizeOptions{Kernel: goimgtools.KernelLinear}))`. The output format (`JPEG()`, `WebP()` and so on) takes no part in the comparison; to compare the loss caused by encoding, first call `ToBytes()`, then read the result back with `FromBytes` and compare.
