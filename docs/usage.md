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
    AutoOrient().                          // correct orientation from EXIF
    Resize(1536, 0).                       // width 1536, height proportional
    Flatten(color.White).                  // white background for transparent areas
    Composite(goimgtools.Open("logo.png"), goimgtools.CompositeOptions{
        Gravity: goimgtools.SouthEast, Left: -24, Top: -24, // bottom right, 24-pixel margin
    }).
    JPEG(goimgtools.JPEGOptions{Quality: 85}).
    ToFile("out.jpg")
```

### Inputs

| Method | Description |
|---|---|
| `Open(path)` / `FromBytes(b)` / `FromReader(r)` | A file, bytes in memory, a stream |
| `Text(text, TextOptions{...})` | Renders text as an image, for use as a watermark or as output in its own right (see [Text](#text)) |
| `Blank(width, height, colour)` | An image of one colour (sharp's `create`, Pillow's `Image.new`): a canvas to `Composite` pictures and text onto; a translucent colour gives transparency. Its rows are made as they are read: no memory of its size |
| `FromImage(img)` | An `image.Image` already in memory |
| `FromSource(open)` | A caller-supplied pixel stream, such as a computed image or pixels read from another source (see [Extending](extending.md)) |

### Operations

Where this library can work an operation out the way libvips (and so sharp) or Pillow does, to the byte, its options have a `Method`: `MethodDefault` (the operation's own default, given below), `MethodLibvips` or `MethodPillow`. `Resize`, `Blur`, `Sharpen`, `Rotate` and `Convolve` take one.

| Method | Description |
|---|---|
| `Resize(w, h, ResizeOptions{...})` | Resizes. A `w` or `h` of 0 is calculated proportionally. With the default method an image of 16-bit samples (PNG, TIFF) stays 16-bit, the values `x/image`'s `Scale` gives a 16-bit destination (not with `LinearLight`, nor the Pillow and libvips methods, which work in 8 bits) |
| `Crop(left, top, w, h)` | Crops |
| `AutoOrient()` | Corrects the orientation automatically from EXIF (no effect when no orientation information is present) |
| `Rotate(degrees, RotateOptions{Background, Method, Interpolator, Expand})` | Rotates clockwise by any angle (negative values rotate anticlockwise). A multiple of 90 is an exact quarter or half turn, without interpolation. For other angles the canvas is the box around the turned image and the corners are filled with `Background` (`nil` for black, `color.Transparent` for transparent); bilinear interpolation by default (`Interpolator`: nearest or bicubic). Byte-for-byte identical to sharp's `rotate`, with and without transparency. With `Method: MethodPillow` it is Pillow's `Image.rotate(-degrees)`, to the byte: the canvas keeps the image's size unless `Expand`, the corners are `Background` (`nil`: transparent, or black without transparency); Pillow's own default interpolator is nearest. By name `rotate DEGREES[,BACKGROUND][,pillow][,nearest\|bilinear\|bicubic][,expand]` |
| `Affine(a, b, c, d, AffineOptions{Background, InputX, InputY, OutputX, OutputY, Interpolator})` | Moves each point (x, y) to (a·x + b·y, c·x + d·y): turns, mirrors, slants and stretches in one. The canvas is the box around the moved image; the offsets are sharp's `idx`, `idy`, `odx`, `ody`. Byte-for-byte identical to sharp's `affine` with `InterpolateBilinear` (its default), `InterpolateNearest` and `InterpolateBicubic`, with and without transparency |
| `Transform(width, height, t, TransformOptions{Background, Interpolator})` | Pillow's `Image.transform((width, height), method, data)`, byte-for-byte: each output pixel takes the colour at the point of the image that `t` maps its centre to. `TransformAffine(a, b, c, d, e, f)`: (x, y) to (a·x + b·y + c, d·x + e·y + f), the matrix from the output to the image; `TransformExtent(x0, y0, x1, y1)`: that rectangle of the image stretched over the output; `TransformPerspective(a, …, h)`: a perspective, to straighten a photographed page; `TransformQuad(x0, y0, …, y3)`: the quadrilateral with those corners (output's top left, bottom left, bottom right, top right) warped onto the output, bilinearly; `TransformPerspectiveQuad(x0, y0, …, y3)`: the same corners by a true perspective, worked out from them, to straighten a photographed page given its corners (the pixels of Pillow with the cookbook's coefficients). Points outside the image take `Background` (`nil`: transparent, or black without transparency). Bilinear by default (Pillow's own default is nearest). By name `transform WIDTH,HEIGHT,affine\|extent\|perspective\|quad\|perspective-quad,NUMBERS…[,nearest\|bilinear\|bicubic][,BACKGROUND]` |
| `Flip()` / `Flop()` | Mirrors top to bottom / mirrors left to right |
| `Trim(threshold, TrimOptions{Background, LineArt, Margin})` | Removes edges on all four sides that match the colour of the top-left pixel or `Background` (a pixel is considered different only if its red, green, blue or alpha channel differs by more than `threshold`; 10, sharp's default, absorbs JPEG noise). As sharp's `trim` (libvips' `find_trim`), the edges are found on a 3×3 median of the image, so that specks of dust and JPEG artefacts in them do not stop the trim (the pixels kept are not filtered); `LineArt` compares the pixels as they are, for drawings of lines one pixel thin, which the median would remove. `Margin` keeps that many pixels around what is found. The same boxes as sharp's on the same pixels |
| `Extend(top, right, bottom, left, ExtendOptions{Background, Mode})` | Adds borders on all four sides; widths are given in CSS order: top, right, bottom, left. By default they are of the colour `Background` (`nil` for black, `color.Transparent` for transparent); `Mode` makes them of the image itself instead: `ExtendCopy` repeats the edge pixels, `ExtendRepeat` repeats the image as tiles, `ExtendMirror` mirrors it (the edge pixel twice), also for borders wider than the image. Pixel for pixel sharp's `extend` with `extendWith`. A top or bottom border that repeats or mirrors keeps the image in working storage |
| `Grayscale(GrayscaleOptions{Linear})` | Converts to grayscale: Rec. 601 luma rounded to the nearest level, byte-for-byte identical to Pillow's `convert("L")` (of a JPEG too, which is decoded as Pillow decodes it). `Linear` takes the luminance of linear light (Rec. 709 weights) instead, byte-for-byte identical to sharp's `greyscale`: reds and blues come out darker, greens lighter. Opaque images are written as single-channel grayscale files |
| `Modulate(brightness, saturation, hue, ModulateOptions{Lightness})` | Multiplies brightness and saturation by the given factors and rotates the hue by the given number of degrees, in the LCh colour space (as sharp does); `Lightness` is then added to the lightness (L\*, 0 to 100), as sharp's `lightness`. Every pixel is within 1 of sharp's output. `Modulate(1.2, 0.8, 0)` increases brightness by 20% and makes colours slightly paler |
| `Negate(NegateOptions{Alpha})` | Every colour its opposite (255 − level); transparency is kept, or with `Alpha` negated too. Byte-for-byte identical to sharp's `negate({alpha: false})` and `negate()` |
| `Linear(a, b)` | Every red, green and blue level v becomes a·v + b, the fraction dropped and kept within 0–255, as sharp's `linear` (byte-for-byte identical): `Linear(1.2, -25)` raises the contrast |
| `Curves(points, CurvesOptions{Red, Green, Blue})` | Maps each red, green and blue level through a tone curve, as image editors' Curves: the smooth curve through `points` (`{input, output}`, 0–255), a monotone cubic (Fritsch–Carlson: no overshoot), flat beyond the first and last point; `Red`, `Green`, `Blue` are curves of one channel each, after it. Transparency is kept. By name `curves X:Y...[,red=X:Y/X:Y...]` |
| `Gamma(g)` | Every level v becomes 255·(v/255)^(1/g), as ImageMagick's `-gamma`: above 1 brightens the middle levels, below 1 darkens them; black and white stay |
| `Threshold(level, ThresholdOptions{PerChannel})` | Black and white: pixels whose grey (of their linear light, as sharp's) is `level` or more become white. Byte-for-byte identical to sharp's `threshold` for images without transparency; opaque images are written as single-channel files. `PerChannel` makes each of red, green and blue 0 or 255 on its own (sharp's `threshold(level, {greyscale: false})`, to the byte) |
| `Tint(color)` | The colour at the brightness of each pixel, less of it towards black and white (sharp's `tint`; within 1 level of its output): `Tint(color.RGBA{112, 66, 20, 255})` gives sepia |
| `Normalize(NormalizeOptions{Lower, Upper})` | Stretches the lightness (L* in L*a*b*) so that the darkest `Lower` percent become black and the lightest `100-Upper` percent white (1 and 99 by default), keeping the colours' hue and saturation; sharp's `normalise`, within 1 level (its percentiles reproduced exactly). Reads the whole image first and keeps it in working storage |
| `Filter(f)` | One of Pillow's built-in filters, as `im.filter(f)`, to the byte: `FilterBlur`, `FilterContour`, `FilterDetail`, `FilterEdgeEnhance`, `FilterEdgeEnhanceMore`, `FilterEmboss`, `FilterFindEdges`, `FilterSharpen`, `FilterSmooth`, `FilterSmoothMore`. `Convolve` with `Method: MethodPillow` is `ImageFilter.Kernel` (3×3 or 5×5): Pillow's arithmetic, its kernel turned upside down, the pixels at the edges kept, and with transparency alpha filtered too, as Pillow does |
| `Enhance(e, factor)` | Pillow's `ImageEnhance`, to the byte (as torchvision's `ColorJitter` and `adjust_*` use it on images): `EnhanceColor` (saturation), `EnhanceContrast`, `EnhanceBrightness` or `EnhanceSharpness` by `factor`, any number, 1 leaving the image as it is: each level `factor` of the way from a degenerate image (grey; a grey of the mean lightness; black; the image smoothed by Pillow's `SMOOTH`) to the image's. With `EnhanceContrast` the whole image is read first, to measure its mean lightness |
| `AutoContrast(AutoContrastOptions{CutoffLow, CutoffHigh, Ignore, PreserveTone})` | Stretches each of red, green and blue so that its darkest level becomes black and its lightest white, leaving out `CutoffLow` and `CutoffHigh` percent of the pixels at the ends and the levels in `Ignore`; `PreserveTone` stretches the lightness with one table for the three, so that colours do not shift. Pillow's `ImageOps.autocontrast`, to the byte. Reads the whole image first |
| `Equalize()` | Spreads the levels of each channel so that each is about as frequent: Pillow's `ImageOps.equalize`, to the byte. Reads the whole image first |
| `Posterize(bits)` / `Solarize(threshold)` | Keeps the `bits` highest bits of each level (1–8) / inverts the levels at or above `threshold` (0–256; Pillow's default 128): Pillow's `ImageOps.posterize` and `solarize`, to the byte |
| `CLAHE(CLAHEOptions{Width, Height, MaxSlope})` | Local contrast (contrast-limited adaptive histogram equalization), bringing out detail in dark and light areas: each level becomes its rank within the `Width` × `Height` window around the pixel, each level of the window counting at most `MaxSlope` pixels (3 by default). Byte-for-byte identical to sharp's `clahe` for images without transparency; a 64×64 window on 24 MP takes 18s on one core, 7s on four (sharp: 7.6s, 8.7s) |
| `Median(size)` | Median filter: each channel of each pixel the middle value of the size × size square around it (odd, 3–99), removing specks while keeping edges. Byte-for-byte identical to Pillow's `MedianFilter` and sharp's `median` |
| `Rank(size, rank)` | Rank filter: each channel of each pixel the value of the given rank (0 the smallest) among the size × size square (odd, 1–99). Rank 0 is Pillow's `MinFilter` (dark areas grow), size×size−1 its `MaxFilter` (light areas grow); byte-for-byte identical to Pillow's `RankFilter` |
| `Dilate(width)`, `Erode(width)` | sharp's `dilate` and `erode`, byte for byte: each channel the bitwise AND (dilate) or OR (erode) of the square of 2·width+1 pixels (1–65536). On black-and-white images, such as `Threshold` produces, dilate grows black shapes (text on white) by width pixels and erode shrinks them; on other images use `Rank`. The time hardly depends on width |
| `Convolve(Convolution{Width, Height, Weights, Scale, Offset})` | A custom kernel of odd sides up to 99: the weighted sum of the window, divided by `Scale` (the sum of the weights by default), plus `Offset`. Byte-for-byte identical to sharp's `convolve`; e.g. weights `0,-1,0,-1,5,-1,0,-1,0` sharpen, `-1,-1,-1,-1,8,-1,-1,-1,-1` with `Scale: 1` find edges |
| `Blur(sigma, BlurOptions{Method, MinAmplitude})` | Gaussian blur; sigma must be greater than 0 and at most 1000 pixels. By default (`MethodPillow`) Pillow's `GaussianBlur`: three box blurs, byte-for-byte identical to Pillow for images without transparency. `MethodLibvips` is sharp's `blur` byte for byte, transparency included: the Gaussian itself with libvips' integer weights, cut off where it falls below `MinAmplitude` of its peak (0.2 by default, as sharp's `minAmplitude`, so it blurs less than Pillow at the same sigma). Either way the running time hardly depends on sigma |
| `BoxBlur(radius)` | Box blur: each pixel the mean of the square of side 2×radius+1 around it (a fractional radius counts the pixels at its edge in part), 0 to 1000; Pillow's `ImageFilter.BoxBlur`, to the byte for an image without transparency. By name `boxblur RADIUS` |
| `Sharpen()` | Mild sharpening (sharp's 3×3 convolution kernel), suited to use after downscaling |
| `Sharpen(SharpenOptions{Sigma, Amount, Threshold})` | Unsharp mask (Pillow's `UnsharpMask`); `Amount` defaults to 1.5 |
| `Sharpen(SharpenOptions{Sigma, Method: MethodLibvips, Curve})` | sharp's `sharpen` with a sigma: the lightness alone (L\* of L\*a\*b\*) is sharpened, so colours do not shift; `Curve` (`SharpenCurve{Flat, Jagged, JaggedFrom, MaxBrighten, MaxDarken}`, sharp's m1, m2, x1, y2, y3; nil for sharp's defaults) maps the difference to the blur. Within a level of sharp's result on a few pixels |
| `Flatten(color)` | Removes transparency by flattening onto a background colour, as sharp's `flatten` (libvips) does, to the byte; JPEG output of an image with transparency is flattened over black the same way, as sharp writes it |
| `Recomb(matrix)` | Mixes the channels by a matrix of 3 rows of 3 numbers (or 4 of 4, the fourth row making the alpha): each row gives one channel of the result as the sum of the levels times its numbers, the fraction dropped. Byte-for-byte identical to sharp's `recomb`; `{{0.393, 0.769, 0.189}, {0.349, 0.686, 0.168}, {0.272, 0.534, 0.131}}` gives sepia |
| `Unflatten()` | Makes white pixels transparent (those whose grey, as sharp's, is 255); after `Threshold`, every pixel at or above the threshold. Byte-for-byte identical to sharp's `unflatten` |
| `ExtractChannel(c)` | A greyscale image of one channel: `ChannelRed`, `ChannelGreen`, `ChannelBlue` or `ChannelAlpha` (sharp's `extractChannel`) |
| `EnsureAlpha(alpha)` | An image without transparency gets an alpha channel of the given opacity, 0–1 (trunc(alpha·255), as sharp's `ensureAlpha`, byte for byte); an image with transparency keeps it |
| `RemoveAlpha()` | Drops the transparency: every pixel shows the colour it stores (sharp's `removeAlpha`); unlike `Flatten`, nothing is blended with a background |
| `Composite(overlay, CompositeOptions{...})` | Composites an overlay or watermark (see [Watermarks](#watermarks)) |
| `InvisibleWatermark(mark, InvisibleWatermarkOptions{Kind, Key, Strength, Density})` | Hides 1–32 bytes invisibly, to be read back with `inspect.ReadWatermark` and the key, without the original; `WatermarkBlind` survives what is done to a file, `WatermarkScreen` also a photograph of a screen (see [Invisible watermarks](watermark.md)) |
| `Animated()` | Processes every frame of an animated GIF, WebP or PNG (APNG) and writes an animated GIF, WebP or PNG (APNG). A WebP's frames are drawn as libwebp's animation decoder draws them (blended or not, cleared or not), pixel for pixel; an APNG's as its specification says. A still image passes unchanged; an animation that cannot be kept (one to another format) is an error wrapping `errors.ErrUnsupported`. A WebP stores the first frame whole and later frames as the changed rectangle. In a GIF, all frames share a single palette chosen from the image content (`Colors` and `NoDither` in `GIFOptions` still apply; if the image has no more colours than that, the colours are kept unchanged), and from the second frame onwards only the changed region is stored (see [Compression](compression.md)). An APNG is lossless: one palette for all frames when they have 256 colours or fewer, else RGB or RGBA; later frames as the rectangle that differs from what they are drawn on (the frame before kept, cleared or undone, as apngopt chooses), as it is or with the unchanged pixels transparent and blended over, whichever is smallest; `CompressionLevel` applies, `Colors` does not (an error) |
| `Pixels(fn)` | A custom per-pixel operation: `fn` receives 8-bit NRGBA data strip by strip and modifies it in place (see [Extending](extending.md)) |
| `Pipe(op)` | Inserts an arbitrary custom operation at this point in the chain (see [Extending](extending.md)) |
| `Apply(steps...)` | Calls a group of steps, defined as a function, in the middle of the chain |

`ResizeOptions`:

- `Fit`: `FitInside` (default; scales proportionally to fit within the box, without cropping or distortion), `FitCover` (fills the box, then crops the excess around the centre; sharp's default), `FitContain` (scales proportionally to fit within the box, then pads with `Background` to exactly w×h; black by default), `FitOutside`, `FitFill` (stretches).
- `WithoutEnlargement`: disables enlargement.
- `Position`: the part `FitCover` keeps, and where `FitContain` places the image: the centre by default, or one of the nine positions `NorthWest` … `SouthEast` (sharp's `position`; a centred crop rounds as sharp's does).
- `LinearLight`: resizes in linear light (as libvips' `thumbnail` with `linear`): the levels are made linear before they are averaged and encoded again after, so that fine light detail (thin lines, text, a starry sky) keeps its brightness when the image is reduced instead of darkening, and colours mix as light does. A pattern of black and white pixels halved becomes level 188 (half the light of white) rather than 128. A 24-megapixel PNG reduced to 1500×1000 takes 0.74 s instead of 0.64 s. Not available with `MethodLibvips`.
- `Interesting`: for `FitCover`, chooses the part to keep by what the image shows, as sharp's `strategy.entropy` / `strategy.attention` (libvips' smartcrop): `InterestingEntropy` keeps the part with the most detail (on the same pixels, the same crop as libvips), `InterestingAttention` the part that draws the eye (edges, skin tones and saturated reds, scored on the image shrunk to 32×32 as libvips shrinks it). The resized image is held in memory while the part is chosen.
- `Kernel`: `KernelCubic` (default, Catmull-Rom), `KernelNearest`, `KernelLinear`, `KernelMitchell`, `KernelLanczos2`, `KernelLanczos3`, `KernelMKS2013`, `KernelMKS2021` (Magic Kernel Sharp, as sharp's `mks2013` and `mks2021`), `KernelBox`, `KernelHamming` (Pillow's `BOX` and `HAMMING`).
- `CustomKernel`: a custom resize filter (`&goimgtools.CustomKernel{Support, At}`); if set, it replaces `Kernel`.
- `FastShrinkOnLoad`: performs an initial shrink of JPEGs, and of lossy WebPs without alpha, during decoding (see [Shrinking JPEGs while decoding](performance.md#shrinking-jpegs-while-decoding-optional)); with `MethodLibvips`, of every WebP, as sharp.
- `Method`: how the pixels are worked out. `MethodDefault` (default) weighs the source for each pixel at its own place, in double precision, as `golang.org/x/image/draw`'s `Scale` does. `MethodLibvips` is sharp's `resize`, to the byte: libvips' `vips_resize` on the colours multiplied by alpha in 8 bits, as sharp runs it, with every `Kernel` (sharp's default is `KernelLanczos3`), `Fit`, `Position`, `Background` and `WithoutEnlargement`. As sharp does, turns asked for before the `Resize` (`Rotate`, `Flip`, `Flop`) are done before it and the EXIF turn (`AutoOrient()`) after it, and a JPEG is shrunk while it is decoded by the factor sharp picks, with libjpeg's scaled decoding: `FastShrinkOnLoad` is sharp's `fastShrinkOnLoad`, which sharp has on by default (without it at least a factor of 2 is left to the resize). With it a WebP is scaled while it is decoded too, as sharp has libwebp do it: by the smaller of the two reductions, each side rounded to the nearest, the planes of a lossy image each scaled from its own size (the loop filter skipped when both sides go below three quarters), a lossless one with its colours multiplied by alpha; the resize does what remains (nothing when the WebP comes out at the size asked for). A lossy WebP to a third of its size takes less than half as long. Not with `LinearLight`. It takes about as long as `MethodDefault` (a 24-megapixel PNG to 1500×1000: 0.62 s against 0.65 s on one core). `MethodPillow` is Pillow's `Image.resize`, to the byte: across and then down, the weights in 22-bit fixed point, an image with transparency resized with its colours multiplied by alpha (Pillow's `RGBa`); `KernelCubic` (the default, as Pillow's) is `BICUBIC`, `KernelLanczos3` `LANCZOS`, `KernelLinear` `BILINEAR`, `KernelBox` `BOX`, `KernelHamming` `HAMMING`, `KernelNearest` `NEAREST`. Turns are done before it, and with `FastShrinkOnLoad` a JPEG is decoded reduced as Pillow's `draft(mode, size)` does before `resize(size)` (libjpeg's scaled decoding). Pillow resizes palette images with `NEAREST` whatever the filter; this library resizes their colours with the kernel asked for. With `MethodPillow` the fits are Pillow's `ImageOps`: `FitInside` is `contain`, `FitOutside` `cover`, `FitContain` `pad` (its centering from `Position`, its colour `Background`) and `FitCover` `fit` (the part of the image of the box's aspect ratio, placed by `Position`, resized straight to the box); with `ReducingGap`, `FitInside` sizes as `Image.thumbnail` does (each side the floor or the ceiling, whichever keeps the aspect ratio closer).
- `ReducingGap` (with `MethodPillow`): Pillow's `reducing_gap`. An image without transparency reduced by more than that many times is first reduced by whole factors, averaging boxes of pixels (`Image.reduce`), leaving at least that much to the resize; with `FastShrinkOnLoad` a JPEG is decoded reduced for the box times the gap. `Resize(w, h, ResizeOptions{Method: MethodPillow, ReducingGap: 2, WithoutEnlargement: true, FastShrinkOnLoad: true})` is `im.thumbnail((w, h))`, to the byte.

A grayscale image remains grayscale (single-channel) after resizing and is not converted to colour.

Blur, sharpening, modulation and extension are streamed row by row and require only a few additional rows of memory (about 6×sigma additional rows for blur, or 3.6×sigma for `MethodLibvips`, and a further 3×sigma rows for unsharp masking; data beyond the memory share is written to a temporary file). `Trim` and `Rotate` require the whole image: the pixels are first staged in working storage (a temporary file when they exceed the memory share) and then read again; consequently, when the chain contains `Trim`, `OutputSize` must also read the pixels once. Images with an alpha channel are blurred and sharpened using premultiplied alpha, so that colour from transparent areas does not bleed into neighbouring pixels.

### Output

| Method | Description |
|---|---|
| `JPEG(JPEGOptions{Quality, Optimize, Progressive, Trellis, QuantTable, ChromaSubsampling})` | `Quality` 1–100; 0 means 80. `Optimize` and `Progressive` reduce file size without changing pixels; `Trellis` and `QuantTable` (0–8, as mozjpeg's `-quant-table`, or `QuantTableJpegli`) change pixels (see [Compression](compression.md)). `ChromaSubsampling`: `Chroma420` (the default: colour at half the width and height, as the standard library and libjpeg), `Chroma422` (half the width) or `Chroma444` (all of the colour: crisp coloured text and edges, about 30% larger), as sharp's `chromaSubsampling` |
| `PNG(PNGOptions{CompressionLevel, Colors, NoDither})` | Compression level from 1 (fastest) to 10 (smallest; 8 and 9 search for the smallest file, in 5 to 20 and 18 to 65 times the time of level 7 on one core; 10, which compresses with Zopfli, about 14 times as long as 9 with 8 threads); `Colors` reduces the image to at most 256 colours (see [Compression](compression.md)) |
| `WebP(WebPOptions{Quality, Lossless, Effort})` | Lossy (quality 1–100, default 75) or lossless; `Effort` from 1, fastest, to 9, smallest, default 5 (for lossy, 5 already does all there is but for transparency, 2 takes about half the time and 1 about a third) |
| `GIF(GIFOptions{Colors, NoDither, FixedPalette})` | Up to 256 colours, selected from the image content; `FixedPalette` uses the standard library's fixed palette instead |
| `BMP()` | |
| `TIFF(TIFFOptions{Compression, Predictor})` | Uncompressed, `TIFFDeflate` or `TIFFLZW` (as libtiff writes it); `Predictor` stores each sample as its difference from the pixel to its left (horizontal differencing), which makes photographs compress much better (sharp's `predictor: "horizontal"`; a 24-megapixel photograph: LZW 82 → 52MB, Deflate 67 → 45MB). Compressed images of more than 1MB of pixel data are written in strips of about 1MB |
| `ToFormat(name, options)` | Selects the output format by name (`"jpeg"`, `"png"`, `".tif"`…, and registered formats); `options` is the format's options struct and may be omitted |
| `MaxFileSize(n)` | Limits the output to n bytes, automatically selecting the highest quality that fits (JPEG, lossy WebP, and registered formats that provide `Quality`), or for PNG and GIF the most colours (a PNG first without loss). With `Animated()` on an animation it is an error wrapping `errors.ErrUnsupported`; a GIF of one frame is handled as a still image |
| `ToFile(path)` / `ToWriter(w)` / `ToBytes()` | Runs the chain and writes the result. Without an explicit format, `ToFile` uses the file extension; the output file is produced only if the entire run succeeds |
| `ToTiles(path, TileOptions{...})` | Writes the image as a pyramid of tiles for a zooming viewer, as sharp's `tile` (libvips' `dzsave`) writes it, every JPEG tile and the files that describe them to the byte (each level the one above shrunk by the mean of four pixels, an odd last row or column taken twice; with transparency the colours weighted by alpha). `Layout`: `TileDeepZoom` (the default; OpenSeadragon: `path.dzi` and `path_files/LEVEL/COLUMN_ROW.jpeg`), `TileGoogle` (Leaflet, OpenLayers: `path/LEVEL/ROW/COLUMN.jpg` on a canvas of `Background`, white, of whole tiles, the image at its corner or with `Center` in the middle, tiles of the background alone left out and `path/blank.png` in their place), `TileIIIF` and `TileIIIF3` (IIIF Image API level 0, `path/info.json` with the address `ID`), `TileZoomify` (`path/ImageProperties.xml`, `path/TileGroupN/LEVEL-COLUMN-ROW.jpg`). Tiles of `Size` pixels (256); `Overlap` pixels shared with the neighbours (0); `Depth` down to a pixel, a tile or one level (`TileDepthOnePixel`, `TileDepthOneTile`, `TileDepthOne`; the layout's by default); `SkipBlanks` leaves out tiles within that many levels of the background (Google's default 5). In the recipe's output format, by default JPEG at quality 80 with optimized coding tables (sharp's). Streams: each level holds a row of tiles; the tiles of a row are encoded on the helpers. `ToFile("x.dzi")` (or `.dz`) is `ToTiles` with the defaults. A path ending in `.zip` writes them in a ZIP archive, as sharp does (stored, as libvips stores them; each file sharp's, their order not libvips' thread-dependent one); so does `ToFile("x.zip")`. A grey image gives grey tiles (sharp's are RGB) |
| `ToTilesWriter(w, name, TileOptions{...})` | The pyramid of `ToTiles` as a ZIP archive written to `w` (sharp's `tile` to a buffer), the image named `name` in it: for a web server's response, a cloud storage upload. A row of tiles is held at a time |
| `Metadata()` | Reads only the file header and describes the **input**: format, stored width and height, colour type (grayscale / palette / RGB / YCbCr / CMYK) and bit depth, presence of transparency, EXIF orientation, presence of ICC / EXIF data, and the resolution (`Density`, pixels per inch: that of the EXIF block when it has one, else the format's own; 25.4 or less, which libvips writes when it knows none, counts as none, as in sharp). Unaffected by the operations on the chain |
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
| `SetExif(ExifFields{...})` | Writes text fields into the output's EXIF block (`Copyright`, `Artist`, `ImageDescription`, `Software`, `DateTime`), as sharp's `withExif` / `withExifMerge`: into the input's block with `KeepExif()`, everything else in it left where it is, else into a block of their own. JPEG, PNG, WebP and TIFF |
| `KeepXmp()` | Copies the input's XMP packet (Adobe's metadata: keywords, rating, rights, edit history) to JPEG, PNG, WebP and TIFF output, as sharp's `keepXmp`. Discarded by default |
| `StripICC()` | Discards the ICC colour profile. Preserved by default, since wide-gamut photographs would otherwise show a colour shift |
| `Density(dpi)` | Records a resolution of `dpi` pixels per inch (1–65535) in the output: in the JFIF segment of a JPEG, the pHYs chunk of a PNG, the resolution tags of a TIFF, the header of a BMP, and the resolution fields of a kept EXIF block (WebP and GIF have no place of their own for it). By default the input's resolution is kept, also when the image is resized, as libvips and sharp keep it; `Density(0)` records none |
| `ToSRGB()` | Converts colours to sRGB using the input's colour profile (primaries and tone curves, or lookup tables); the output carries no profile. Colours are then correct even in programs that ignore profiles, and the file is slightly smaller. Applied automatically when converting to GIF or BMP, which cannot carry a profile. A CMYK image (JPEG, TIFF) is always turned into sRGB through its profile when it is decoded, as sharp does; one without a profile through libvips's CMYK profile, as sharp |
| `MemoryLimit(bytes)` | Working memory limit; 64MB by default. PNG output at the default level takes about 2MB of it for its compressor (less for small images), levels 8 and 9 3MB and 5.6MB, level 10 about 50MB for each thread compressing (with the default limit it compresses on one thread), level 6 and below little |
| `Threads(n)` | Maximum number of cores used by a single run; default 1, up to the number of CPU cores (see [CPU and threads](performance.md#cpu-and-threads)) |
| `SetDefaultThreads(n)` | Program-wide default thread count (used by runs that do not call `Threads`), so that it need not be specified at every call site |
| `TempDir(dir)` | Directory for temporary files; the system temporary directory by default |
| `LimitInputPixels(n)` | Limit on the number of input pixels; default 16383×16383 (as in sharp). For an animated GIF or WebP with `Animated()`, the limit applies to the total pixels of all frames |
| `LimitOutputPixels(n)` | Limit on the number of pixels in the image produced by any step of the chain (and therefore in the result); same default as above. Prevents a small file from being enlarged into a result that fills the disk and takes hours to compute |
| `WithContext(ctx)` | Stops processing when `ctx` is cancelled or times out, and returns `ctx.Err()` |
| `Clone()` | Copies the recipe, so that several results can be derived from the same starting point |

ICC colour profiles are read and written in JPEG, PNG, WebP and TIFF; EXIF is read and written in JPEG, PNG, WebP and TIFF (a TIFF holds it in its first directory, with the EXIF and GPS directories its tags point to: what describes the picture is read from there, the orientation included, and written there); XMP in JPEG (an APP1 segment of up to 64KB; the extended XMP of larger packets is not kept), PNG (read from iTXt, zTXt or tEXt, written as iTXt), WebP and TIFF. GIF and BMP cannot carry a colour profile, so when converting to these formats, colours are converted to sRGB using the input's profile (measured: without conversion, a Display P3 image shows a colour difference of up to 17.6 and appears visibly washed out). After `AutoOrient()` corrects the orientation, the orientation in the preserved EXIF is set to "upright".

Conversion supports profiles described by three primaries and tone curves (cameras, phones, Display P3, Adobe RGB and ProPhoto all belong to this category); every value is within 1 of LittleCMS (relative colorimetric, colours outside sRGB clipped). Lookup-table profiles (uncommon, mostly used for printers) are preserved unchanged and not converted.

## Server-side use

**One recipe, several results.** Methods on the chain modify the same `Image` and return it; they do not copy it. To derive several results from the same starting point, call `Clone()` first:

```go
base := goimgtools.Open("in.jpg").AutoOrient()
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
| `ErrFileSize` | The `MaxFileSize` target cannot be met even at the lowest quality (two colours for PNG and GIF) | Reduce the dimensions first |
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
    return goimgtools.FromReader(in).WithContext(ctx).Resize(1600, 1600).JPEG().ToWriter(out)
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
- `Blend`: how the overlay's colours mix with the image's: `BlendOver` (the default, as `image/draw`'s `Over`), `BlendMultiply`, `BlendScreen`, `BlendOverlay`, `BlendDarken`, `BlendLighten`, `BlendColorDodge`, `BlendColorBurn`, `BlendHardLight`, `BlendSoftLight`, `BlendDifference`, `BlendExclusion`; computed as sharp's `blend` (libvips' composite), within 1 level on an opaque image. And the Porter-Duff operators, as sharp's of the same names: `BlendClear`, `BlendSource`, `BlendIn`, `BlendOut`, `BlendAtop`, `BlendDest`, `BlendDestOver`, `BlendDestIn` (a mask: the image kept where the overlay is opaque), `BlendDestOut` (a cut-out), `BlendDestAtop`, `BlendXor`, and `BlendAdd`, `BlendSaturate`; clear, source, in, out, dest-in and dest-atop also clear the image where the overlay is not. With transparency what shows of a colour is within 2 levels of sharp's (the pixels are kept multiplied by alpha in 8 bits, libvips' in floating point). On the command line: `-watermark-blend multiply`, `-watermark-blend dest-in`.
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
| `goimgtools info file...` | `img.Metadata()`, `img.Frames()` | One line per file: format, width and height, colour type and bit depth, pixel count, file size, frame count and duration of animations (GIF, WebP, APNG), EXIF orientation, resolution in dpi, presence of ICC / EXIF data. With `-json`, one line of JSON per file |
| `goimgtools check file...` | `inspect.Verify(img)` | Fully decodes the image once and reports files that are corrupt, truncated or not images. `info` reads only the file header and therefore cannot detect damage in the latter part of a file |
| `goimgtools compare a b` | `inspect.Compare(a, b)` | Compares two images pixel by pixel, including images in different formats: reports whether they are identical and, if not, the PSNR (higher means closer; above 40, differences are barely perceptible), the largest difference, and the number of differing pixels. `-min-psnr 35` serves as a quality gate: if the threshold is not met, the command exits with a non-zero status |
| `goimgtools stats file...` | `inspect.Stats(img)` | Examines every pixel: average colour, brightness, the few colours covering the largest area, whether the image is in fact grayscale, whether it actually contains transparent pixels, and the number of colours (when there are no more than 256). Used to choose a placeholder colour, to decide between dark and light text, or to determine whether the image can be stored as grayscale or with a palette. Also what sharp's `stats()` reports, to the last digits: `Channels` (lowest and highest level of red, green, blue and alpha and where each first occurs, sums, mean, standard deviation), `Entropy` and `Sharpness` |
| `goimgtools hash file...` | `inspect.Hash(img)` | A 64-bit perceptual hash: the same image at a different size, format or compression rate differs by only a few bits, while different images differ in about half of the bits. `-similar 10` lists files that are close to each other, for finding duplicate images |
| `goimgtools exif file...` | `inspect.ReadExif(img)` | Camera, lens, capture time, exposure settings, GPS location and other EXIF fields |
| `goimgtools readmark -key K file...` | `inspect.ReadWatermark(img, key)` | The mark hidden with `InvisibleWatermark` and the same key, without the original, or none (see [Invisible watermarks](watermark.md)) |
| | `inspect.ReadXmp(img)` | The XMP packet (XML) of the file, as sharp's `metadata().xmp`; `info` reports whether there is one |
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

Measured (single thread): `info` and `exif` read only the file header and take about 0.015 seconds; on a 24-megapixel JPEG, `check` and `hash` take about 0.2 seconds, `stats` about 0.5 seconds (sharp's `stats()` 0.43 seconds), and `compare` on two such images about 0.5 seconds.

Notes:

- The hash reflects the light-and-dark layout of the whole picture, so it does not recognise an image after cropping, rotation or mirroring; images without such a layout (solid colours, smooth gradients from one side to the other) are indistinguishable to it.
- EXIF is read from JPEG, PNG, WebP and TIFF. When writing files, the library discards EXIF (including location) by default; to preserve it, call `KeepExif()` explicitly.
- All these tools read images row by row within bounded memory; `stats` retains only a colour table of about 100KB, regardless of image size.

`Compare` compares the pixels of the two recipes after processing, so two processing approaches can be compared directly without first writing files; for example, the same image resized with two algorithms: `inspect.Compare(a.Resize(800, 0), b.Resize(800, 0, goimgtools.ResizeOptions{Kernel: goimgtools.KernelLinear}))`. The output format (`JPEG()`, `WebP()` and so on) is not part of the comparison; to measure the loss caused by encoding, call `ToBytes()` first, then read the result back with `FromBytes` and compare.
