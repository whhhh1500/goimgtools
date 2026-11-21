English | [中文](zh-CN/cli.md)

# Command line

The subcommands for inspecting and checking images (`info`, `check`, `compare`, `stats`, `hash`, `exif`, `formats`) are described in [Usage](usage.md#inspecting-and-checking-images).

```
goimgtools [options] input output
goimgtools batch [options] in_dir out_dir
goimgtools -text "text" [options] output
goimgtools info [-json] file...
goimgtools check file...
goimgtools compare a b
goimgtools stats [-json] file...
goimgtools hash [-similar bits] file...
goimgtools exif [-json] file...
goimgtools formats
goimgtools fonts [-all]
goimgtools ops
```

Installation:

```bash
go install github.com/whhhh1500/goimgtools/cmd/goimgtools@latest
```

Alternatively, build from source:

```bash
go build -o goimgtools ./cmd/goimgtools
```

```bash
goimgtools -rotate auto -resize 1536x1536 -no-enlarge -flatten white -watermark logo.png -quality 85 in.jpg out.jpg
```

```bash
goimgtools -watermark-text "© Sample" -font-size 40 -text-color white -stroke-width 2 -watermark-tile -watermark-gap 80,60 -watermark-angle -30 -watermark-opacity 0.3 in.jpg out.webp
```

```bash
goimgtools -trim 10 -resize 1200x -extend 20,20,20,20 -background white -sharpen mild in.jpg out.jpg
```

```bash
goimgtools -op median:3 -op normalize -op "tint:#704214" scan.jpg out.jpg
```

Operations run in a fixed order: rotate → flip → trim → crop → resize → extend → grayscale → modulate → blur → sharpen → `-op` steps (in the order specified) → flatten → watermark.

| Option | Description |
|---|---|
| `-rotate auto\|degrees` | `auto` corrects the orientation from EXIF; a number specifies clockwise rotation in degrees. For angles that are not multiples of 90, the canvas is enlarged and the corners are filled with `-background` |
| `-flip` / `-flop` | Mirror top to bottom / left to right |
| `-trim difference` | Remove borders of the same colour as the top-left corner; difference 0–255 (10 tolerates JPEG noise) |
| `-trim-background color`, `-trim-margin N`, `-trim-line-art` | The colour of the borders to remove (instead of the top-left corner's), pixels to keep around what is found, and comparing the pixels as they are instead of after the median filter that passes over noise (for drawings of thin lines) |
| `-crop X,Y,W,H` | Crop |
| `-resize WxH` | Target size; either side may be omitted (`1536x`, `x1024`) |
| `-fit` | `inside` (default), `cover`, `contain`, `outside`, `fill` |
| `-linear-light` | Resize in linear light: fine light detail keeps its brightness when the image is reduced |
| `-position` | The part `-fit cover` keeps and where `-fit contain` places the image: `center` (default), `northwest` … `southeast`; for `cover` also `entropy` (the most detail) or `attention` (what draws the eye) |
| `-background color` | Colour of the padding for `-fit contain`, of the `-extend` borders, and of the corners after `-rotate` by an oblique angle: `white`, `black`, `transparent`, `#rrggbb`, `#rrggbbaa` |
| `-kernel` | `nearest`, `linear`, `cubic` (default), `mitchell`, `lanczos2`, `lanczos3`, `mks2013`, `mks2021`, `box`, `hamming` |
| `-reducing-gap` | With `-resize-method pillow`: Pillow's `reducing_gap` (2 with `-no-enlarge -fast-shrink` is Pillow's `thumbnail`) |
| `-resize-method` | `draw` (default), `libvips` (sharp's resize, to the byte; sharp's default kernel is `lanczos3`) or `pillow` (Pillow's resize, to the byte) |
| `-no-enlarge` | Do not enlarge the image |
| `-fast-shrink` | When a JPEG is reduced to less than half its size, or a lossy WebP without alpha to a third, shrink it first during decoding (with `-resize-method libvips`: sharp's `fastShrinkOnLoad`, every WebP) |
| `-extend T,R,B,L` | Add borders on all four sides (in pixels) in the `-background` colour |
| `-extend-with copy\|repeat\|mirror` | Make the `-extend` borders of the image itself: its edge pixels repeated, the image repeated, or the image mirrored |
| `-grayscale` | Convert to grayscale |
| `-grayscale-linear` | Convert to grayscale by the luminance of linear light, as sharp's `greyscale` |
| `-modulate B,S,H[,L]` | Brightness factor, saturation factor, hue in degrees and lightness added (L\*, 0 to 100), e.g. `1.2,0.8,0` |
| `-blur sigma` | Gaussian blur |
| `-blur-method pillow\|libvips` | `pillow` (default): as Pillow's `GaussianBlur`; `libvips`: as sharp's `blur` |
| `-blur-min-amplitude F` | Where `-blur-method libvips` cuts the Gaussian off, 0.001 to 1 (default 0.2) |
| `-sharpen mild\|SIGMA[,AMOUNT[,THRESHOLD]]` | `mild` applies mild sharpening; otherwise an unsharp mask (`AMOUNT` defaults to 1.5, `THRESHOLD` to 0) |
| `-sharpen SIGMA,libvips[,FLAT,JAGGED,JAGGED_FROM,MAX_BRIGHTEN,MAX_DARKEN]` | sharp's sharpen with a sigma: the lightness alone, by its curve (sharp's m1, m2, x1, y2, y3; 1,2,2,10,20 by default) |
| `-flatten color` | Flatten onto a background colour |
| `-watermark file` / `-watermark-text text` | Image watermark / text watermark (`\n` inserts a line break) |
| `-watermark-width N\|N%` | Scale the watermark to a width of N pixels, or to N% of the main image's width (so that the watermark is the same size when batch-processing images of different sizes) |
| `-gravity`, `-offset X,Y` | Nine-point position and pixel offset; when only these two are used, the default margin is 24 pixels |
| `-watermark-ratio X,Y` | Offset as a proportion of the main image's width and height |
| `-watermark-anchor`, `-watermark-pivot` | Anchor point and rotation pivot: a nine-point position name or `X,Y` |
| `-watermark-angle degrees` | Clockwise rotation |
| `-watermark-opacity 0–1` | Overall opacity |
| `-watermark-blend MODE` | How the watermark's colours mix with the image's: over (default), multiply, screen, overlay, darken, lighten, color-dodge, color-burn, hard-light, soft-light, difference, exclusion |
| `-watermark-tile`, `-watermark-gap X,Y` | Tiling and spacing |
| `-mark text`, `-mark-key K`, `-mark-kind blind\|screen`, `-mark-strength S`, `-mark-density D` | Invisible watermark of 1–32 bytes, applied last; `goimgtools readmark -key K file...` reads it back (see [Invisible watermarks](watermark.md)) |
| `-text text` | Render the text directly as an image, without an input file |
| `-font`, `-font-size`, `-text-color`, `-text-background`, `-text-width` | Font (comma-separated files or font names), font size, colour, background, wrap width |
| `-stroke-width`, `-stroke-color` | Stroke |
| `-shadow-color`, `-shadow-offset X,Y`, `-shadow-blur` | Shadow |
| `-format` | `jpeg`, `png`, `webp`, `gif`, `bmp`, `tiff`; determined by the extension by default |
| `-quality N` | Quality for JPEG or lossy WebP |
| `-jpeg-optimize`, `-jpeg-progressive` | JPEG: optimised coding tables; progressive encoding (smaller files, pixels unchanged) |
| `-jpeg-trellis` | JPEG: trellis quantization (smaller files at the same perceived quality; pixels change; can be combined with the two options above) |
| `-jpeg-overshoot-deringing` | JPEG: less ringing beside dark text and lines on white, as mozjpeg does it (sharp's `overshootDeringing`, its bytes): screenshots and documents look much better at the same quality (a screenshot at 50 as good as at 75 without it, in a file 25% smaller), for files a few percent larger; photographs about unchanged |
| `-jpeg-chroma 420\|422\|444` | JPEG: how finely colour is kept: 420 (the default, half across and down), 422 (half across) or 444 (all of it: crisp coloured text and edges, a larger file) |
| `-jpeg-quant-table N` | JPEG: quantization tables 0–8, numbered as mozjpeg's `-quant-table` (0, the default, the standard's; 3 mozjpeg's default), or 9, made as jpegli makes them: the smallest files (see [Compression](compression.md)) |
| `-png-colors N`, `-no-dither` | PNG: reduce to N colours (2–256); disable dithering |
| `-max-size size` | Limit the file to this size (`200KB`, `1.5MB` or a number of bytes) by lowering the quality automatically (JPEG, lossy WebP) or the number of colours (PNG, first without loss; GIF); with `-animated`, an animation is an error |
| `-tile-size N`, `-tile-overlap N`, `-tile-layout NAME` | For an output named `.dzi`, `.dz` or `.zip` (in a ZIP archive), which writes a pyramid of tiles (as sharp's `tile`): the side of a tile (256), the overlap (0), and the layout: `dz` (Deep Zoom, the default), `google`, `iiif`, `iiif3` or `zoomify` (the tiles then go in a directory named as the output without `.dz`) |
| `-skip-larger` | Keep the original file if the result is not smaller than it (same format); intended for compression-only jobs |
| `-webp-lossless`, `-webp-effort N` | Lossless WebP; effort 1 (fastest) to 9 (smallest file), 5 by default. The effort also applies to lossy WebP, where 5 already does all there is (above 5, only the alpha plane gets more), 2 takes about half the time and 1 about a third |
| `-webp-near-lossless` | Near-lossless WebP, as libwebp's: lossless, but where a pixel differs from a neighbour anyway its channels may change a little, `-quality` saying how much (100 none, 80–99 at most 1 level, 60–79 2, 40–59 4, 20–39 8, below 16; 75 by default); photographs about 37% smaller than lossless |
| `-webp-exact` | Keep the colour of fully transparent pixels exactly (by default it is dropped, as libwebp, sharp and Pillow drop it, which makes images with transparency smaller) |
| `-webp-alpha-quality N` | Lossy WebP: the quality of the transparency, 1–99: its values reduced to fewer levels (2–16 up to 70), 0 and 255 kept; by default (100) it is kept exactly |
| `-png-level N`, `-gif-colors N`, `-gif-fixed-palette`, `-tiff-compression none\|deflate\|lzw`, `-tiff-predictor` | Format-specific options |
| `-gif-lossy N` | GIF: lossy compression, the colour difference 1–255 it may accept (16–32 for photographic animations, 8–16 for dithered stills; see [Compression](compression.md)) |
| `-animated` | Process every frame of an animated GIF, WebP or PNG (APNG) and write an animated GIF, WebP or PNG (APNG, without loss) by the output; `-gif-colors` and `-no-dither` also apply to GIF, the WebP options to WebP, `-png-level` to PNG. Another output format is an error |
| `-op NAME[:ARG,ARG...]` | Run an operation by name (`goimgtools ops` lists all of them, both built-in and registered by the program); may be specified more than once, and the operations run after sharpening in the order specified |
| `-keep-exif`, `-keep-xmp`, `-strip-icc` | Metadata |
| `-density DPI` | Record a resolution of DPI pixels per inch in the output (JPEG, PNG, TIFF, BMP); by default the input's is kept |
| `-srgb` | Convert colours to sRGB using the colour profile; the output contains no profile (applied automatically when converting to GIF or BMP) |
| `-mem MB`, `-threads N`, `-tmp directory` | Memory limit (default 64), number of threads (default 1, at most the number of CPU cores; see [CPU and threads](performance.md#cpu-and-threads)), temporary directory |
| `-max-pixels N` | Maximum size of accepted and produced images, in megapixels (default 268, i.e. 16383×16383); with `-animated`, the total over all frames |

## Batch processing

```bash
goimgtools batch -resize 1920x1920 -no-enlarge -quality 80 -format jpeg in_dir out_dir
```

Applies the same processing to every image in the input directory (`.jpg .jpeg .png .webp .gif .bmp .tif .tiff`) and writes each result to a file of the same name in the output directory. All single-file options are supported, together with the following:

| Option | Description |
|---|---|
| `-jobs N` | Number of files processed concurrently, default 3; any value is accepted, and `0` means unlimited (all at once) |
| `-recursive` | Include subdirectories and recreate the directory structure in the output directory |
| `-overwrite` | Overwrite existing output files (skipped by default); when the input and output directories are the same, files are replaced in place only if this option is set |
| `-quiet` | Print only failures and the summary |

- When `-format` is given, the extension is changed to match that format; otherwise each file retains its original format.
- A failure on one file is reported and processing continues with the remaining files; the exit code is non-zero if any file fails.
- Each file uses at most `-mem` of working memory and `-threads` cores; the total is this multiplied by the concurrency. With unlimited concurrency and many files, memory and CPU usage multiply accordingly.

Measurements on 42 real photographs (250 million pixels in total, 20 cores), resized to 1920 and converted to JPEG:

| Concurrency | Time | Peak memory |
|---|---|---|
| 1 | 4.6s | 13MB |
| 3 (default) | 1.5s | 19.5MB |
| 8 | 0.87s | 32MB |
| Unlimited (all 42 at once) | 0.83s | 46MB |

Colours are specified as `white`, `black`, `#rrggbb` or `#rrggbbaa`.
