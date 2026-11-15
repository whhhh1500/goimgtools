English | [中文](zh-CN/cli.md)

# Command line

For the subcommands that inspect and check images (`info`, `check`, `compare`, `stats`, `hash`, `exif`, `formats`), see [Usage](usage.md#inspecting-and-checking-images).

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

Install:

```bash
go install github.com/whhhh1500/goimgtools/cmd/goimgtools@latest
```

Or build from source:

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

The order of execution is fixed: rotate → flip → trim → crop → resize → extend → grayscale → modulate → blur → sharpen → each `-op` step (in the order given) → flatten → watermark.

| Option | Description |
|---|---|
| `-rotate auto\|degrees` | `auto` straightens the image by EXIF; a number is degrees clockwise. For angles that are not multiples of 90 the canvas grows, and the corners use `-background` |
| `-flip` / `-flop` | Mirror top-bottom / left-right |
| `-trim difference` | Remove borders of the same colour as the top-left corner; difference 0–255 (10 absorbs JPEG noise) |
| `-crop X,Y,W,H` | Crop |
| `-resize WxH` | Target size; you can give just one side (`1536x`, `x1024`) |
| `-fit` | `inside` (default), `cover`, `contain`, `outside`, `fill` |
| `-background color` | Colour of the padding for `-fit contain`, of the `-extend` borders, and of the corners after an angled `-rotate`: `white`, `black`, `transparent`, `#rrggbb`, `#rrggbbaa` |
| `-kernel` | `nearest`, `linear`, `cubic` (default), `mitchell`, `lanczos2`, `lanczos3` |
| `-no-enlarge` | Do not enlarge |
| `-fast-shrink` | When a JPEG shrinks to less than half, shrink it first during decoding |
| `-extend T,R,B,L` | Add borders on all four sides (in pixels), coloured with `-background` |
| `-grayscale` | Convert to grayscale |
| `-modulate B,S,H` | Brightness factor, saturation factor, hue in degrees, e.g. `1.2,0.8,0` |
| `-blur sigma` | Gaussian blur |
| `-sharpen mild\|SIGMA[,AMOUNT[,THRESHOLD]]` | `mild` is a mild sharpen; or an unsharp mask (`AMOUNT` defaults to 1.5, `THRESHOLD` to 0) |
| `-flatten color` | Flatten onto a background colour |
| `-watermark file` / `-watermark-text text` | Image watermark / text watermark (`\n` for a line break) |
| `-watermark-width N\|N%` | Scale the watermark to N pixels wide, or to N% of the main image's width (so the watermark is the same size when batch-processing images of different sizes) |
| `-gravity`, `-offset X,Y` | Nine-point grid position and pixel offset; when only these two are used, the default is 24 pixels from the edge |
| `-watermark-ratio X,Y` | Offset as a proportion of the main image's width and height |
| `-watermark-anchor`, `-watermark-pivot` | Anchor, pivot: a nine-point grid name or `X,Y` |
| `-watermark-angle degrees` | Rotate clockwise |
| `-watermark-opacity 0–1` | Overall opacity |
| `-watermark-tile`, `-watermark-gap X,Y` | Tile and spacing |
| `-text text` | No input file; render the text directly as an image |
| `-font`, `-font-size`, `-text-color`, `-text-background`, `-text-width` | Font (comma-separated, files or font names), font size, colour, background, wrap width |
| `-stroke-width`, `-stroke-color` | Stroke |
| `-shadow-color`, `-shadow-offset X,Y`, `-shadow-blur` | Shadow |
| `-format` | `jpeg`, `png`, `webp`, `gif`, `bmp`, `tiff`; by default follows the extension |
| `-quality N` | Quality of JPEG or lossy WebP |
| `-jpeg-optimize`, `-jpeg-progressive` | JPEG: optimised coding tables, progressive (smaller, pixels unchanged) |
| `-jpeg-trellis` | JPEG: trellis quantization (smaller at the same visual quality, pixels change; can be combined with the two above) |
| `-png-colors N`, `-no-dither` | PNG: reduce to N colours (2–256); no dithering |
| `-max-size size` | Keep the file no larger than this size (`200KB`, `1.5MB` or a number of bytes), lowering the quality automatically |
| `-skip-larger` | Keep the original file when the result is not smaller than it (same format); for jobs that only compress |
| `-webp-lossless` | Lossless WebP |
| `-png-level N`, `-gif-colors N`, `-gif-fixed-palette`, `-tiff-compression none\|deflate` | Options for each format |
| `-animated` | Process every frame of an animated GIF; `-gif-colors` and `-no-dither` apply as well |
| `-op NAME[:ARG,ARG...]` | Run an operation by name (`goimgtools ops` lists them all: built-in ones and those the program registers); can be given more than once, and they run after sharpen in the order given |
| `-keep-exif`, `-strip-icc` | Metadata |
| `-srgb` | Convert colours to sRGB using the colour profile; the output carries no profile (automatic when converting to GIF or BMP) |
| `-mem MB`, `-threads N`, `-tmp directory` | Memory limit (default 64), number of threads (default 1, up to the number of CPU cores; see [CPU and threads](performance.md#cpu-and-threads)), temporary directory |
| `-max-pixels N` | Largest image accepted and produced, in megapixels (default 268, i.e. 16383×16383); with `-animated`, the total over all frames |

## Batch processing

```bash
goimgtools batch -resize 1920x1920 -no-enlarge -quality 80 -format jpeg in_dir out_dir
```

Applies the same processing to every image in the input directory (`.jpg .jpeg .png .webp .gif .bmp .tif .tiff`) and writes each one to a file of the same name in the output directory. All options of single-file mode work, plus:

| Option | Description |
|---|---|
| `-jobs N` | How many files to process at once, default 3; any number works, `0` means no limit (all at once) |
| `-recursive` | Include subdirectories, rebuilding the directory structure in the output directory |
| `-overwrite` | Overwrite existing output files (skipped by default); when input and output are the same directory, files are replaced in place only with this option |
| `-quiet` | Print only failures and the summary |

- With `-format`, the extension changes to match that format; otherwise each file keeps its original format.
- A file that fails is reported and the rest continue; the exit code is non-zero if any file fails.
- Each file uses at most `-mem` of working memory and `-threads` cores; the total is that times the concurrency. With unlimited concurrency and many files, memory and CPU use multiply.

Measured on 42 real photos (250 million pixels in total, 20 cores), resized to 1920 and converted to JPEG:

| Concurrency | Time | Peak memory |
|---|---|---|
| 1 | 4.6s | 13MB |
| 3 (default) | 1.5s | 19.5MB |
| 8 | 0.87s | 32MB |
| Unlimited (42 at once) | 0.83s | 46MB |

Write colours as `white`, `black`, `#rrggbb` or `#rrggbbaa`.
