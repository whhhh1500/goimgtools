English | [中文](zh-CN/comparison.md)

# Comparison with other tools

Every capability of this package set against the strongest tool there is for it: the same task, the same input, both numbers measured on the same machine. A row is **ahead**, **level** or **behind**; what is behind is listed at the end, with what is being done about it or why it is left.

Measured on 22 November 2026: Windows 10, Intel Core i7-12700K, Go 1.27.1. The others: libvips 8.18.7 (its `vips` and `vipsthumbnail` programs, built with mozjpeg, libpng, libwebp, cgif and librsvg), sharp 0.35.5 on Node 24.14.1, Pillow 12.1.1 on Python 3.14.3, libwebp 1.6.0 (`cwebp`, `dwebp`), jpegli at commit 031a007, gifsicle 1.98, LittleCMS (through Pillow's `ImageCms`), and SSIMULACRA2 and butteraugli from libjxl.

- **Time** is the best wall time of three runs of a whole process (start, read, work, write); **memory** is the peak working set of that process as the operating system records it. A process that only starts takes 7.3MB here, 7.8MB for `vips`, 56.6MB for Node with sharp loaded, 18.2MB for Python with Pillow loaded.
- Memory is ranked before time: a row whose memory is lower and whose time is within a tenth is ahead.
- One thread each, unless a row says otherwise: `-threads 1` here, `VIPS_CONCURRENCY=1` for libvips and `sharp.concurrency(1)` (libvips still writes on a thread of its own, so its CPU time is given where it is more than its wall time).
- Inputs of 6000×4000 pixels: a baseline JPEG (4:2:0, 7.6MB), the same picture as a PNG (42MB), a lossy WebP, a GIF and an uncompressed TIFF.

## Speed and memory

| Task | goimgtools | libvips | sharp | Pillow | Others | Verdict |
|---|---|---|---|---|---|---|
| JPEG to a JPEG 1536 wide | 0.10s, 9.2MB with `FastShrinkOnLoad` (as the others shrink while decoding); 0.21s, 11.7MB without | `vipsthumbnail` 0.14s, 48.7MB | 0.17s, 86.6MB | 0.21s, 61.6MB with `draft`; 0.34s, 142MB without | | ahead |
| JPEG to JPEG, same size | 0.17s, 7.9MB | 0.11s (0.19s of CPU), 32.9MB | 0.24s, 217MB | 0.25s, 204MB | | memory ahead, CPU time level; with 4 threads 0.10s, 12.7MB against 0.11s, 40.5MB |
| PNG to a PNG 1536 wide | 0.53s, 14.0MB, a file of 2.1MB | `vipsthumbnail` 0.36s, 40.6MB, 4.0MB | 0.43s, 86MB, 4.0MB | 0.62s, 142MB, 2.4MB | | memory and file ahead, time **behind** (PNG encoding) |
| PNG to PNG, same size | 1.86s, 12.5MB, 42.2MB (level 7, the default); 1.40s, 9.5MB, 43.9MB (level 6) | 1.38s, 26.0MB, 42.6MB (`filter=all`); 1.16s, 25.9MB, 66.2MB (its default, unfiltered) | 1.45s, 74MB, 42.6MB (`adaptiveFiltering`); 1.21s, 74MB, 66.2MB (default) | 1.82s, 113MB, 42.6MB | | memory ahead; at the same file size the time is **behind** by a third (with 4 threads: 0.87s, 18.1MB against 1.11s for the unfiltered file) |
| PNG to JPEG, same size (PNG decoding) | 0.37s, 8.7MB | 0.33s (0.39s of CPU), 25.3MB | 0.44s, 210MB | 0.46s, 204MB | | memory ahead, time level |
| JPEG to lossy WebP, quality 75 | 4.02s, 21.6MB, 3.49MB (effort 5, the default); 2.46s, 21.4MB (effort 3); 1.59s, 20.2MB, 2.79MB (effort 1) | 1.86s, 198MB, 3.60MB | 1.95s, 246MB; 6.2s, 425MB (effort 6, `smartSubsample`) | 3.80s, 314MB | `cwebp` 1.85s, 207MB, 3.60MB; 14.9s, 294MB, 3.51MB (`-m 6 -af -sharp_yuv`, its best) | ahead: the default makes files as small as cwebp's best settings (see Quality) in 0.27 of its time and 0.07 of its memory; effort 1 is faster than cwebp's default, its files 5% smaller at equal SSIMULACRA2 and the same at equal butteraugli |
| PNG to lossless WebP | 4.91s, 27.7MB, 28.9MB | 8.89s, 752MB, 29.8MB | 8.98s, 799MB | 10.2s, 784MB | `cwebp -lossless` 8.68s, 677MB, 29.8MB | ahead in all three |
| Lossy WebP to a JPEG 1536 wide | 0.36s, 11.8MB with `FastShrinkOnLoad`; 0.41s, 11.9MB without | `vipsthumbnail` 0.23s (0.31s of CPU), 29.2MB | 0.29s, 83.9MB | 0.77s, 394MB | | memory ahead, CPU time level (0.33s) |
| Lossy WebP decoded (to BMP) | 0.32s, 9.0MB | 0.29s, 200MB (to TIFF) | | | `dwebp` 0.30s, 79.0MB | memory ahead, time level (8% more) |
| GIF to a JPEG 1536 wide | 0.17s, 9.6MB | 0.19s, 155MB | 0.25s, 197MB | 0.20s, 45MB | | ahead |
| TIFF to a JPEG 1536 wide | 0.14s, 11.6MB | 0.30s, 80.6MB | 0.36s, 133MB | 0.37s, 236MB | | ahead |
| JPEG to a GIF 1536 wide | 0.49s, 15.9MB, 442KB | | 1.99s, 127MB, 500KB | 0.58s, 142MB, 409KB | | ahead |
| Blur, sigma 2 (JPEG in and out) | 0.27s, 12.1MB | 0.15s (0.22s of CPU), 31.8MB | 0.27s, 268MB | 0.59s, 295MB | | memory ahead, time **behind** libvips (0.28s of CPU against its 0.22s, which it spreads over more than one core; its kernel is cut off at a fifth of its peak), level with sharp |
| Sharpen, sigma 2 | 0.35s, 12.5MB | 0.98s, 34.9MB | 1.07s, 219MB | 0.72s, 295MB | | ahead |
| Rotate 90° | 0.31s, 14.2MB | 0.17s, 97.1MB | 0.33s, 267MB | 0.28s, 204MB | | memory ahead (a seventh), time **behind** libvips (it holds the whole image) |
| Rotate 30° | 0.64s, 14.1MB | 0.42s (0.53s of CPU), 98.5MB | 0.54s, 398MB | 1.42s, 376MB | | memory ahead, time **behind** libvips |
| Greyscale | 0.21s, 8.7MB | 0.18s (0.22s of CPU), 30.1MB | 0.28s, 220MB | 0.27s, 135MB | | memory ahead, CPU time level |
| Brightness, saturation and hue | 0.46s, 9.4MB | | 1.14s, 230MB | | | ahead |
| A logo laid on | 0.18s, 9.7MB | | 0.30s, 217MB | 0.27s, 204MB | | ahead |
| Animated GIF (18 frames, 768×512) to 384 wide | 0.70s, 23.1MB, 942KB; 0.30s, 22.9MB, 947KB (effort 1) | | 2.03s, 75.1MB, 1405KB | | gifsicle 0.31s, 17.9MB, 1416KB (its default sampling); 0.46s, 21.1MB, 1428KB (`--resize-method lanczos3 -O3`) | file ahead (a third smaller); time level with gifsicle at effort 1, memory **behind** it by 5MB |
| Animated GIF to animated WebP | 0.51s, 26.7MB, 854KB (effort 1); 1.28s, 25.8MB, 940KB (the default) | 0.54s, 29.9MB, 946KB | 0.60s, 78.0MB, 946KB | 0.45s, 35.2MB, 1339KB | | ahead at effort 1 |

With four threads (`-threads 4`, `VIPS_CONCURRENCY=4`, `sharp.concurrency(4)`):

| Task | goimgtools | libvips | sharp |
|---|---|---|---|
| JPEG to a JPEG 1536 wide | 0.09s, 12.3MB (`FastShrinkOnLoad`); 0.12s, 16.6MB | 0.13s, 86.3MB | 0.16s, 95.7MB |
| JPEG to JPEG, same size | 0.10s, 12.7MB | 0.11s, 40.5MB | 0.24s, 223MB |
| PNG to a PNG 1536 wide | 0.30s, 20.0MB | 0.35s, 75.4MB | 0.42s, 114MB |
| PNG to PNG, same size | 0.87s, 18.1MB, 42.2MB | 1.11s, 34.3MB, 66.2MB | 1.19s, 82.2MB, 66.2MB |
| JPEG to lossy WebP | 2.08s, 20.8MB; 0.83s, 20.0MB (effort 1) | 1.83s, 198MB | 1.89s, 246MB |
| PNG to lossless WebP | 3.94s, 33.0MB | 8.11s, 752MB | 8.58s, 800MB |
| Blur, sigma 2 | 0.12s, 20.1MB | 0.13s, 35.3MB | 0.28s, 270MB |
| Sharpen, sigma 2 | 0.15s, 21.3MB | 0.31s, 45.4MB | 0.43s, 231MB |
| Rotate 90° | 0.22s, 18.0MB | 0.17s, 99.5MB | 0.33s, 267MB |
| Rotate 30° | 0.41s, 19.9MB | 0.21s, 104MB | 0.38s, 399MB |

## Quality

| Capability | Test | Metric | goimgtools | The others | Verdict |
|---|---|---|---|---|---|
| Downscaling (Lanczos 3) | The 24 Kodak photographs to 300 wide, and the 6000×4000 PNG to 1536 wide, against the exact Lanczos 3 convolution (each channel resized in 32-bit floats, rounded once) | Mean PSNR; the largest difference of a channel | Default method: 55.7dB, 1 (Kodak); 54.7dB, 1 (24 megapixels). `ResizePillow` and `ResizeLibvips` give those tools' pixels to the byte | sharp and `vips resize`: 53.6dB, 15; 54.0dB, 2. Pillow: 56.2dB, 19; 57.7dB, 1 | level: all are beyond what can be seen; the default has the smallest largest error, Pillow the best mean (its pixels are an option here) |
| JPEG encoding | The 24 Kodak photographs at 7 qualities; the size needed for the same score, against mozjpeg with all its options (trellis, optimized scans, table 3, overshoot deringing) | File size at equal SSIMULACRA2 / equal butteraugli (3-norm) | `Progressive`, `Trellis`, `QuantTableJpegli`: 10.7% / 9.8% smaller. With mozjpeg's table 3: 1.8% / 1.8% smaller. The default (libjpeg-turbo's bytes): 11.5% / 6.5% larger | jpegli: 6.7% / 13.0% smaller. libvips' default: 10.1% / 8.0% larger | ahead of mozjpeg; against jpegli ahead by SSIMULACRA2 (4.0 points), **behind** by butteraugli (3.2 points) |
| Lossy WebP encoding | The same 24 photographs at 6 qualities, against `cwebp -m 6 -af -sharp_yuv` | The same two | Default: 0.7% larger / 0.2% smaller. Effort 1: 4.2% / 6.6% larger | `cwebp` default: 9.9% / 6.5% larger | level with libwebp's best, in a quarter of its time |
| Colour management | A grid over the whole cube of colours tagged with Display P3, Adobe RGB and two CMYK profiles of lookup tables (RSWOP, Chemical proof), converted to sRGB | Difference from LittleCMS (perceptual intent) | Largest difference 1; 0.7% to 1.7% of the values differ, none by more than 1 | LittleCMS is the reference | level |
| Lossless sizes (PNG, GIF, WebP) | See [Compression](compression.md) | File size | 17 PNG images: level 9 is 13.35% below level 7 where oxipng 10.2 `-o max` is 12.5%, level 10 is 0.23% smaller than ECT 0.9.5 `-9`. Three animated GIFs: 0.2–0.45% smaller than gifsicle `-O3`. Lossless WebP: 2.8% smaller than `cwebp -lossless` (the table above) | oxipng, ECT, gifsicle, cwebp | ahead |

## Features

What each reads (R) and writes (W). libvips is the build above (its other loaders are optional and absent from it); Pillow's list is what `Image.OPEN` and `Image.SAVE` hold.

| Format | goimgtools | libvips / sharp | Pillow |
|---|---|---|---|
| JPEG, PNG, WebP, GIF, TIFF | R W | R W | R W |
| Animation | GIF, WebP and PNG (APNG), R W | GIF and WebP, R W (no APNG) | GIF, WebP, APNG, R W |
| BMP, ICO, ICNS, TGA, QOI | R W | — | R W |
| PNM (PBM, PGM, PPM, PAM, PFM) | R W | — | R W |
| PSD, PSB | R | — | R (PSD) |
| CUR, MPO (the first picture) | R | — | R; MPO W |
| SVG | R (its own renderer) | R (librsvg) | — |
| PDF | W | — (optional) | W |
| Tiles for a zooming viewer (Deep Zoom, Zoomify, Google) | W | W | — |
| AVIF, HEIC | — | R W | AVIF R W |
| JPEG XL | — | — (optional) | — |
| JPEG 2000 | — | — (optional) | R W |
| TIFF: every kind read; tiles and pyramids written | R W (read: classic and BigTIFF, strips and tiles, pyramids' first level; greys and palettes of 1, 2, 4, 8 and 16 bits, grey with alpha, RGB with and without alpha, CMYK, Lab, floats; LZW, Deflate, PackBits, CCITT, JPEG, zstd, WebP) | R W (the Windows build without zstd) | R W (no tiles written; the Windows build without zstd; float RGB and float Lab not read) |
| DDS, PCX, SGI, XBM | R | — | R W |
| XPM, FITS | R (XPM with `None` and colour names, FITS with its byte order and BZERO, which Pillow 12.1.1 gets wrong) | — | R |
| DIB, FLI, IM, MSP, SPIDER, BLP, DCX, FTEX, GBR, IMT, MCIDAS, PCD, PIXAR, SUN, WMF, EPS | — (a `.dib` file that is a whole BMP is read as one) | — | R (some W) |
| UltraHDR (a JPEG with a gain map) | R (the picture alone) | R W | R (the picture alone) |

| Capability | goimgtools | libvips / sharp | Pillow |
|---|---|---|---|
| Operations | All of sharp's (resize with its kernels and fits, extract, extend, trim, composite with every blend mode, rotate, affine, flip, flop, sharpen, median, blur, dilate, erode, flatten, unflatten, gamma, negate, normalise, CLAHE, convolve, threshold, boolean, linear, recomb, modulate, tint, greyscale, channels), to its pixels where `…Libvips` options say so; Pillow's `ImageOps`, `ImageEnhance`, `ImageFilter` kernels and `transform`, to its pixels | Its own | Its own |
| Missing operations | Expressions over pixels (`ImageMath`); output in CMYK or Lab; 16 bits kept through every step (through the default resize alone). Colour lookup cubes (`ColorLUT`) and flood fill (`FloodFill`), both to Pillow's bytes, arcs, pie slices and chords, and pictures of noise are there now | | |
| Metadata | EXIF read and parsed, kept, or text fields set; XMP read and kept; IPTC read and kept (JPEG, TIFF); ICC kept, removed, converted to sRGB or to an RGB profile; resolution | EXIF, XMP, ICC and IPTC kept; EXIF fields, XMP and ICC set | EXIF read and written tag by tag, XMP read, IPTC read, ICC |
| Text | Fonts tried in order, shaping by the font's tables (HarfBuzz's port), right-to-left, wrapping, alignment, outline, shadow, colour emoji (COLR version 0, bitmaps). No vertical lines, no choice of font features, no markup | Pango: fonts by fallback, shaping, right-to-left, wrapping, alignment, justification, markup; no outline or shadow | FreeType and Raqm: one font at a time, shaping, font features, vertical lines, variable fonts, outline; lines broken by the caller |
| Watermarks | Pictures and text, tiled, turned, by opacity and blend; an invisible mark that survives recompression, read back with a key | Composite | Paste |
| SVG | 1,327 of resvg's 1,801 tests (1,303 of the 1,414 without text) | librsvg: 1,099 (1,073) | — |
| Memory limit, streaming | A limit for one image (64MB by default), rows streamed, what does not fit in temporary files | Streams where the operations allow, no limit | Whole images in memory |
| Fingerprints, placeholders, checks | Perceptual hash, BlurHash, ThumbHash, SSIM, statistics, damaged-file check | Statistics | Statistics |

## Behind

| What | The gap, measured | Standing |
|---|---|---|
| PNG encoding time | On one thread a third more time than libpng's (libvips, `filter=all`) for a file 0.8% smaller: 1.86s against 1.38s (1.83s of CPU against 1.52s). On four threads ahead: 0.87s against 1.11s | Left: the time is the Deflate coder's, a Go port of libdeflate's lazy parser that takes this picture at about 60MB a second whatever the depth of its search (depths of 16, 35, 50 and 100 measured: 1.77s to 1.81s, the files within 0.02% of each other). Level 6 takes libpng's time (1.40s) for a file 3% larger |
$1 | Left: decoding and encoding the JPEG alone take 0.16s on one core here, so one thread cannot reach 0.15s; libvips does that part on other cores. With the same cores (four threads) the times are level, and the blur here is the whole Gaussian |
| Rotation by 90° | 0.31s against 0.17s (0.68s before the rows of a round were written as one block) | Left, by design: libvips holds the image in memory (97MB); here it is turned through a store that may be a file, in 14MB |
| Rotation by other angles | 0.64s against 0.42s on one thread, 0.41s against 0.21s on four | Left, by design: the source is kept in working storage in tiles of 32 pixels and read back through a small cache, so the rotation takes 14MB where libvips holds the decoded image (98MB). Reading the tiles back is a quarter of the time (0.155s of 0.60s measured); larger tiles would buy time with memory |
| GIF decoding | Was 0.24s against 0.19s for a 24 megapixel GIF | Closed: 0.17s, with a decoder of its own for the LZW data (the same pixels and errors as `compress/lzw`) |
$1 At the default effort gifsicle is twice as fast (0.31s against 0.70s; 0.46s with its Lanczos method and `-O3`); at `Effort: 1` the times are level (0.30s) and the file is still a third smaller than gifsicle's (947KB against 1416KB). Memory is 5MB more than gifsicle's (17.9MB against 22.9MB): 7MB is the program itself and 6MB what is in use (the picture so far, the colour counts, a frame in work); the rest is what Go's collector lets the heap grow by before it collects | Time closed with `GIFOptions.Effort`. Memory left: collecting sooner (GOGC=25) brings this task to 18.6MB but costs the tasks that stream 1 to 2MB each (measured), so it is not set; gifsicle keeps frames as 8-bit indexes and does no true-colour work. Cutting the allocations from 48MB to 18MB in all did not move the peak |
| JPEG against jpegli by butteraugli | 3.2 points more file at equal butteraugli (4.0 points less at equal SSIMULACRA2) | Left: the two measures pull apart, and this encoder is tuned by SSIMULACRA2, by which it is ahead; the gap by butteraugli is jpegli's own method (quantization adapted block by block to butteraugli's model) |
| TIFF variants | BigTIFF, CMYK, Lab, floats, 2 and 4 bits, grey with alpha, zstd and WebP compression were not read; tiles and pyramids were not written | Closed: all read; tiles and pyramids written (`TIFFOptions.Tile`, `Pyramid`), the levels libvips' to the pixel. A pyramid of a 24 megapixel image: 1.41s and 21.7MB with Deflate against libvips' 1.31s and 54.9MB; with JPEG tiles 0.30s and 30.6MB against 0.16s (0.25s of CPU) and 39.6MB |
| DDS, PCX, SGI, XBM, XPM, FITS | Were not read | Closed for reading. Not written, on purpose: nothing new is saved in PCX, SGI, XBM or XPM, and a DDS for a game is made by a texture tool, with mip levels and a block coder of its own. DIB (a BMP without its file header) is left: it has no signature to know it by |
| JPEG 2000 | Not read | Left: a codec of its own (wavelets and the EBCOT coder). Of the others only Pillow has it (through OpenJPEG); libvips' Windows build and sharp do not |
| IPTC | Was not read or kept | Closed: read from JPEG, TIFF and PSD, kept in JPEG and TIFF (`KeepIptc`) |
| Text: vertical lines, font features, variable fonts, COLR version 1 in `Text()` | Not in `Text()`; the SVG renderer draws all three (vertical lines, variable fonts, COLR version 1), so an SVG `<text>` gives them today. Font features (`font-feature-settings`) are in neither | Left: `Text()` lays out and draws through one path whose pixels are pinned (wrapping, alignment, outline, shadow), which would have to be rebuilt on the SVG text engine |
| `ImageMath`; CMYK and Lab output; 16 bits through every step | Not there. `Color3DLUT` (`ColorLUT`), flood fill (`FloodFill`), arcs (`Arc`, `Pie`, `Chord`) and noise (`Noise`) were added | Left. `ImageMath`: in Go the expression is a function, which `Pipe` and `pipeline.Map` run over every strip within the same memory (`Linear`, `Recomb` and `Boolean` are the usual arithmetic). CMYK and Lab output need the output tables of a profile and a rendering intent, and the colour management here reads profiles in the input direction only. 16 bits through every step: the operations work on 8-bit strips, and widening them doubles what each holds for input that is nearly always 8-bit; the default resize and conversion between formats keep 16 bits |
| AVIF, HEIC, JPEG XL | Not read or written | Left out on purpose: their codecs, translated to Go, took far more memory to build and to run than this package allows itself; they were removed on 21 November 2026 |
| FLI, IM, MSP, SPIDER, BLP, DCX, FTEX, GBR, IMT, MCIDAS, PCD, PIXAR, SUN, WMF, EPS | Not read | Left: formats of the 1980s and 1990s that are met next to never; WMF and EPS need an interpreter of their own |
| PDF read | Not there | Left: it takes a PDF interpreter; libvips and Pillow do not have one either without Poppler or PDFium |

The scripts that made these numbers are in [`tests/compare`](../tests/compare).
