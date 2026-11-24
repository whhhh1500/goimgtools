English | [中文](zh-CN/comparison.md)

# Comparison with other tools

Every capability of this package set against the strongest tool there is for it: the same task, the same input, both numbers measured on the same machine. A row is **ahead**, **level** or **behind**; what is behind is listed at the end, with how it was closed or why it is left.

Measured on 22 and 23 November 2026: Windows 10, Intel Core i7-12700K, Go 1.27.1. The others: libvips 8.18.7 (its `vips` and `vipsthumbnail` programs, built with mozjpeg, libpng, libwebp, cgif and librsvg), sharp 0.35.5 on Node 24.14.1, Pillow 12.1.1 on Python 3.14.3, libwebp 1.6.0 (`cwebp`, `dwebp`), jpegli at commit 031a007, gifsicle 1.98, LittleCMS (through Pillow's `ImageCms`), and SSIMULACRA2 and butteraugli from libjxl.

- **Time** is the best wall time of three runs of a whole process (start, read, work, write). **Memory** is the largest of the three runs' peaks of the working set (the memory the process held resident) as the operating system records it; where a second figure follows a slash, it is the peak of private (committed) memory, what the process had asked the system for whether resident or not, decoders' and encoders' buffers included. **Temporary files** are what the process wrote less its output (the system's count of bytes written); they are given where there are any. A process that only starts takes 6.8MB here (13.5MB private), 7.8MB for `vips` (4.0MB), 57MB for Node with sharp loaded (31MB), 18.2MB for Python with Pillow loaded (13.1MB). A MB is 2^20 bytes, in file sizes too.
- Memory is ranked before time. In the first table a row whose memory is lower and whose time is within a tenth is ahead; in the tables of the other features memory within 5% of the smallest of the others is level, and the time is noted when it is more than twice the fastest's (and more than 0.05s apart).
- Runs vary: times by up to a tenth from one series to the next, memory by a few MB where a collection falls (the SVG row has measured 38–49MB). Rows where the tools do not do the same work say so.
- One thread each, unless a row says otherwise: `-threads 1` here, `VIPS_CONCURRENCY=1` for libvips and `sharp.concurrency(1)` (libvips still writes on a thread of its own, so its CPU time is given where it is more than its wall time).
- Inputs of 6000×4000 pixels: a baseline JPEG (4:2:0, 7.6MB), the same picture as a PNG (42MB), a lossy WebP, a GIF and an uncompressed TIFF.

## Speed and memory

Each cell: time, peak working set / peak private memory, and for the writers the file; temporary files where there are any.

| Task | goimgtools | libvips | sharp | Pillow | Others | Verdict |
|---|---|---|---|---|---|---|
| JPEG to a JPEG 1536 wide | 0.10s, 8.7 / 14.9MB with `FastShrinkOnLoad` (as the others shrink while decoding); 0.21s, 9.8 / 16.2MB without | `vipsthumbnail` 0.14s, 48.6 / 37.0MB | 0.19s, 87.3 / 85.1MB | 0.25s, 61.6 / 56.3MB with `draft`; 0.32s, 142 / 137MB without | | ahead |
| JPEG to JPEG, same size | 0.16s, 7.7 / 14.0MB | 0.16s, 32.8 / 21.2MB | 0.24s (0.28s of CPU), 217 / 216MB | 0.24s, 204 / 199MB | | memory ahead, time level; with 4 threads 0.10s, 12.1 / 19.1MB against 0.11s, 40.7 / 29.5MB |
| PNG to a PNG 1536 wide | 0.52s, 13.5 / 20.5MB, a file of 2.05MB | `vipsthumbnail` 0.37s (0.42s of CPU), 40.5 / 36.0MB, 3.79MB | 0.41s (0.50s of CPU), 86.4 / 91.6MB, 3.79MB | 0.61s, 142 / 137MB, 2.28MB | | memory and file ahead, time **behind** (PNG encoding); with 4 threads ahead (0.30s against 0.37s) |
| PNG to PNG, same size | 1.83s, 12.1 / 18.6MB, 40.3MB (level 7, the default); 1.38s, 9.1 / 15.5MB, 41.9MB (level 6); 1.16s, 9.0 / 15.4MB, 42.0MB (level 4) | 1.65s, 25.9 / 21.4MB, 40.6MB (`filter=all`); 1.28s, 63.1MB (its default, unfiltered) | 1.63s, 74.1 / 79.3MB, 40.6MB (`adaptiveFiltering`); 1.27s, 63.1MB (default) | 1.77s, 113 / 108MB, 40.6MB | | memory ahead; against libpng's filtered file 0.8% smaller in 11% more time, at level 4 faster for a file 3% larger (with 4 threads: 0.91s, 17.7 / 25.1MB against 1.19s for the unfiltered file) |
| PNG to JPEG, same size (PNG decoding) | 0.35s, 8.3 / 14.7MB | 0.32s (0.39s of CPU), 25.2 / 20.7MB | 0.46s, 211 / 216MB | 0.45s, 204 / 199MB | | memory ahead, time level |
| JPEG to lossy WebP, quality 75 | 4.00s, 21.0 / 27.2MB, 3.32MB, 37.2MB to temporary files (effort 5, the default); 2.53s, 21.2 / 27.7MB, 3.24MB (effort 3); 1.55s, 19.7 / 27.1MB, 2.66MB (effort 1) | 1.84s, 198 / 191MB, 3.43MB | 1.91s, 246 / 250MB; 6.27s, 425 / 434MB, 3.40MB (effort 6, `smartSubsample`) | 3.77s, 314 / 315MB | `cwebp` 1.81s, 207 / 206MB, 3.44MB; 15.0s, 294 / 288MB, 3.35MB (`-m 6 -af -sharp_yuv`, its best) | ahead: the default makes files as small as cwebp's best settings (see Quality) in 0.27 of its time and 0.07 of its memory; effort 1 is faster than cwebp's default, its files 5% smaller at equal SSIMULACRA2 and the same at equal butteraugli |
| PNG to lossless WebP | 4.73s, 27.1 / 37.7MB, 27.6MB, 93.1MB to temporary files | 8.25s, 752 / 795MB, 28.4MB | 8.40s, 800 / 850MB | 10.1s, 784 / 827MB | `cwebp -lossless` 7.94s, 677 / 719MB, 28.4MB | ahead in all three |
| Lossy WebP to a JPEG 1536 wide | 0.36s, 9.7 / 16.0MB with `FastShrinkOnLoad`; 0.41s, 10.1 / 16.4MB without | `vipsthumbnail` 0.24s (0.34s of CPU), 29.1 / 110MB | 0.31s (0.36s of CPU), 84.3 / 161MB | 0.77s, 394 / 391MB | | memory ahead, CPU time level (0.36s against 0.34s) |
| Lossy WebP decoded (to BMP) | 0.33s, 8.5 / 14.9MB | 0.33s, 200 / 354MB (to TIFF) | | | `dwebp` 0.31s, 78.9 / 75.1MB | memory ahead, time level (6% more) |
| GIF to a JPEG 1536 wide | 0.17s, 9.6 / 15.8MB | 0.19s, 155 / 127MB | 0.26s, 198 / 192MB | 0.20s, 45.2 / 40.0MB | | ahead |
| TIFF to a JPEG 1536 wide | 0.14s, 10.0 / 16.3MB | 0.31s, 80.7 / 76.1MB | 0.37s, 135 / 140MB | 0.36s, 236 / 232MB | | ahead |
| JPEG to a GIF 1536 wide | 0.49s, 15.4 / 21.7MB, 431KB, 6.0MB to temporary files | | 2.02s, 128 / 136MB, 488KB | 0.58s, 142 / 137MB, 399KB | | ahead |
| Blur, sigma 2 (JPEG in and out) | 0.28s, 11.8 / 18.4MB; `BlurLibvips` (libvips' kernel to the byte) 0.24s, 11.7 / 18.2MB | 0.15s (0.19s of CPU), 31.7 / 20.0MB | 0.30s, 269 / 276MB | 0.60s, 295 / 291MB | | memory ahead, time **behind** libvips (see Behind), level with sharp |
| Sharpen, sigma 2 | 0.35s, 12.0 / 18.6MB | 1.00s, 34.8 / 22.7MB | 1.10s, 220 / 218MB | 0.72s, 295 / 291MB | | ahead |
| Rotate 90° | 0.32s, 13.8 / 20.4MB, 91.6MB to temporary files | 0.17s (0.25s of CPU), 97.1 / 85.7MB | 0.34s, 267 / 275MB | 0.29s, 204 / 199MB | | memory ahead (a seventh), time **behind** libvips (it holds the whole image) |
| Rotate 30° | 0.64s, 13.9 / 20.3MB, 91.8MB to temporary files | 0.50s, 98.5 / 87.2MB | 0.60s (0.72s of CPU), 399 / 406MB | 1.41s, 376 / 372MB | | memory ahead, time **behind** libvips |
| Greyscale | 0.20s, 8.6 / 14.8MB | 0.18s (0.30s of CPU), 30.1 / 18.5MB | 0.30s (0.44s of CPU), 221 / 219MB | 0.26s, 135 / 130MB | | memory ahead, CPU time ahead |
| Brightness, saturation and hue | 0.45s, 9.2 / 15.3MB | | 1.13s, 230 / 228MB | | | ahead |
| A logo laid on | 0.18s, 9.6 / 16.0MB | | 0.29s (0.42s of CPU), 217 / 215MB | 0.27s, 204 / 199MB | | ahead |
| Animated GIF (18 frames, 768×512) to 384 wide | 0.69s, 22.2 / 28.3MB, 919KB, 6.8MB to temporary files; 0.31s, 23.0 / 29.2MB, 925KB (effort 1) | | 2.09s, 75.3 / 75.8MB, 1.34MB | | gifsicle 0.32s, 17.9 / 17.0MB, 1.35MB (its default sampling); 0.47s, 21.1 / 20.6MB, 1.36MB (`--resize-method lanczos3 -O3`) | file ahead (a third smaller); time level with gifsicle at effort 1, memory **behind** it by 4–5MB |
| Animated GIF to animated WebP | 0.53s, 25.9 / 33.0MB, 834KB, 10.1MB to temporary files (effort 1); 1.26s, 26.2 / 32.7MB, 918KB (the default) | 0.57s, 29.9 / 20.6MB, 924KB | 0.64s, 78.4 / 78.4MB, 923KB | 0.47s, 35.0 / 31.4MB, 1.28MB | | ahead at effort 1 by working set and file; private memory 12MB above libvips' |

With four threads (`-threads 4`, `VIPS_CONCURRENCY=4`, `sharp.concurrency(4)`):

| Task | goimgtools | libvips | sharp |
|---|---|---|---|
| JPEG to a JPEG 1536 wide | 0.09s, 12.0 / 18.6MB (`FastShrinkOnLoad`); 0.12s, 16.5 / 23.3MB | 0.14s, 86.4 / 75.0MB | 0.17s, 95.8 / 94.7MB |
| JPEG to JPEG, same size | 0.10s, 12.1 / 19.1MB | 0.11s, 40.7 / 29.5MB | 0.25s, 225 / 224MB |
| PNG to a PNG 1536 wide | 0.30s, 20.0 / 28.9MB | 0.37s, 75.7 / 71.4MB | 0.47s, 115 / 120MB |
| PNG to PNG, same size | 0.91s, 17.7 / 25.1MB, 40.3MB | 1.19s, 34.3 / 29.9MB, 63.1MB | 1.22s, 82.4 / 87.8MB, 63.1MB |
| JPEG to lossy WebP | 2.22s, 20.4 / 27.1MB; 0.93s, 20.0 / 27.0MB (effort 1) | 1.87s, 198 / 191MB | 1.93s, 246 / 249MB |
| PNG to lossless WebP | 4.43s, 32.3 / 43.6MB | 8.46s, 752 / 796MB | 9.06s, 799 / 851MB |
| Blur, sigma 2 | 0.15s, 19.1 / 25.6MB; `BlurLibvips` 0.11s, 17.6 / 24.4MB | 0.14s, 34.5 / 22.8MB | 0.29s, 270 / 278MB |
| Sharpen, sigma 2 | 0.19s, 21.0 / 27.4MB | 0.32s, 45.0 / 33.0MB | 0.43s, 230 / 229MB |
| Rotate 90° | 0.26s, 18.1 / 24.8MB | 0.17s, 99.5 / 88.8MB | 0.33s, 267 / 275MB |
| Rotate 30° | 0.56s, 19.2 / 25.9MB | 0.31s, 101 / 90.7MB | 0.46s, 400 / 409MB |

## Quality

| Capability | Test | Metric | goimgtools | The others | Verdict |
|---|---|---|---|---|---|
| Downscaling (Lanczos 3) | The 24 Kodak photographs to 300 wide, and the 6000×4000 PNG to 1536 wide, against the exact Lanczos 3 convolution (each channel resized in 32-bit floats, rounded once) | Mean PSNR; the largest difference of a channel | Default method: 55.7dB, 1 (Kodak); 54.7dB, 1 (24 megapixels). `ResizePillow` and `ResizeLibvips` give those tools' pixels to the byte | sharp and `vips resize`: 53.6dB, 15; 54.0dB, 2. Pillow: 56.2dB, 19; 57.7dB, 1 | level: all are beyond what can be seen; the default has the smallest largest error, Pillow the best mean (its pixels are an option here) |
| JPEG encoding | The 24 Kodak photographs at 7 qualities; the size needed for the same score, against mozjpeg with all its options (trellis, optimized scans, table 3, overshoot deringing) | File size at equal SSIMULACRA2 / equal butteraugli (3-norm) | `Progressive`, `Trellis`, `QuantTableJpegli`: 10.7% / 9.8% smaller. With mozjpeg's table 3: 1.8% / 1.8% smaller. The default (libjpeg-turbo's bytes): 11.5% / 6.5% larger | jpegli: 6.7% / 13.0% smaller. libvips' default: 10.1% / 8.0% larger | ahead of mozjpeg; against jpegli ahead by SSIMULACRA2 (4.0 points), **behind** by butteraugli (3.2 points) |
| Lossy WebP encoding | The same 24 photographs at 6 qualities, against `cwebp -m 6 -af -sharp_yuv` | The same two | Default: 0.7% larger / 0.2% smaller. Effort 1: 4.2% / 6.6% larger | `cwebp` default: 9.9% / 6.5% larger | level with libwebp's best, in a quarter of its time |
| Colour management | A grid over the whole cube of colours tagged with Display P3, Adobe RGB and two CMYK profiles of lookup tables (RSWOP, Chemical proof), converted to sRGB | Difference from LittleCMS (perceptual intent) | Largest difference 1; 0.7% to 1.7% of the values differ, none by more than 1 | LittleCMS is the reference | level |
| Lossless sizes (PNG, GIF, WebP) | See [Compression](compression.md) | File size | 17 PNG images: level 9 is 13.35% below level 7 where oxipng 10.2 `-o max` is 12.5%, level 10 is 0.23% smaller than ECT 0.9.5 `-9`. Three animated GIFs: 0.2–0.45% smaller than gifsicle `-O3`. Lossless WebP: 2.8% smaller than `cwebp -lossless` (the table above) | oxipng, ECT, gifsicle, cwebp | ahead |
| Colours reduced to 256 (PNG with a palette, GIF) | The 24 Kodak photographs, each tool's own reduction and dithering | Total size; mean SSIMULACRA2 / butteraugli | PNG 5.35MB, GIF 6.17MB; 83.19 / 0.875 | libvips and sharp (libimagequant for PNG, cgif for GIF): 5.58MB at 80.01 / 1.119, 6.30MB at 78.25 / 1.439. Pillow: 5.90MB and 6.89MB at 61.17 / 3.818 | ahead: smaller files and better scores |
| Lossy GIF | The 24 photographs, `GIFOptions.Lossy` against gifsicle's `--lossy`, each at several settings (the same number is not the same loss: 24 here scores 60.25, gifsicle's 81.46) | File size at equal SSIMULACRA2 | 13% smaller than gifsicle at its scores around 80, 20% around 76, 24% around 71 (`Lossy` 8, 12, 16 against gifsicle's 24–60, 80–120, 120–200) | gifsicle 1.98 `-O3` | ahead |
| Near-lossless WebP | The 24 photographs at 60 | Total size; mean scores | 6.61MB, 92.80 / 0.343 | cwebp, libvips and sharp: 6.79MB, 92.79 / 0.337 | ahead: 2.8% smaller for the same scores |
| JPEG options at quality 85 | The 24 photographs: the default, trellis quantization, 4:4:4, overshoot deringing, and JPEG inside TIFF | Total size at the same scores | 2.09MB (80.25 / 0.954); trellis 1.88MB; 4:4:4 2.48MB; deringing 2.09MB; in TIFF 2.10MB | libvips within 0.2% of each; sharp and Pillow 1.1–1.6% smaller, as they write optimized Huffman tables by default (with `Optimize`, `-jpeg-optimize`, the file here is sharp's to the byte; trellis with it 0.15% smaller than sharp's, 0.03 lower by SSIMULACRA2). Pillow's JPEG in TIFF: 5.74MB at 85.62 (a different encoder set-up) | level |

## The other features

Every other feature, one thread, on the same photograph unless a heading says otherwise, against each tool that does the same (made by `bench_more.ps1`, the tables by `mktable.py`). Each cell: time, peak working set / peak private memory, what went to temporary files when anything did, and for writers and animations the file. The verdict weighs the working set against the smallest of the others' (within 5% is level), and notes the time when it is more than twice the fastest's.

#### Reading: each format to a JPEG of 1536 wide

| Feature | goimgtools | libvips | sharp | Pillow | Other | Verdict |
|---|---|---|---|---|---|---|
| in.bmp | 0.12s, 9.3MB / 15.7MB |  |  | 0.28s, 144MB / 139MB |  | less memory |
| in.qoi | 0.33s, 9.4MB / 16.0MB |  |  | 26.48s, 186MB / 254MB |  | less memory |
| in.ppm | 0.16s, 9.2MB / 15.4MB |  |  | 0.27s, 144MB / 139MB |  | less memory |
| in.tga | 0.36s, 9.2MB / 15.5MB |  |  | 0.30s, 146MB / 141MB |  | less memory |
| in.psd | 0.18s, 9.2MB / 15.8MB |  |  | 0.30s, 162MB / 157MB |  | less memory |
| in.dds | 0.21s, 9.2MB / 15.4MB |  |  | 20.76s, 185MB / 252MB |  | less memory |
| in.fits | 0.08s, 8.8MB / 15.1MB |  |  | 0.16s, 55.2MB / 27.8MB |  | less memory |
| in.pcx | 0.37s, 9.2MB / 15.5MB |  |  | 0.40s, 146MB / 141MB |  | less memory |
| in.sgi | 0.16s, 9.1MB / 15.4MB |  |  | 0.31s, 162MB / 157MB |  | less memory |
| in.xbm | 0.18s, 8.8MB / 15.1MB |  |  | 0.31s, 169MB / 164MB |  | less memory |
| in.xpm | 0.22s, 9.1MB / 15.3MB |  |  | 18.59s, 169MB / 165MB |  | less memory |
| in_deflate.tif | 0.40s, 9.7MB / 15.9MB | vipsthumbnail: 0.51s, 81.2MB / 76.6MB | 0.58s, 135MB / 140MB | 0.54s, 237MB / 233MB |  | less memory |
| in_lzw.tif | 0.57s, 9.7MB / 16.0MB | vipsthumbnail: 0.57s, 81.2MB / 76.6MB | 0.63s, 136MB / 141MB | 0.58s, 238MB / 234MB |  | less memory |
| in_jpeg.tif | 0.25s, 27.6MB / 33.9MB | vipsthumbnail: 0.13s, 40.9MB / 36.1MB | 0.20s, 95.7MB / 100MB | 0.34s, 146MB / 142MB |  | less memory |
| in_pyr.tif | 0.30s, 16.4MB / 22.5MB | vipsthumbnail: 0.07s, 23.5MB / 18.8MB | 0.28s, 94.5MB / 99.2MB | 0.45s, 169MB / 141MB |  | less memory, 4.3 times the fastest's time |
| in_pyr.tif, -fast-shrink | 0.09s, 13.2MB / 25.0MB | vipsthumbnail: 0.08s, 23.4MB / 18.7MB |  |  |  | less memory |
| in_ll.webp | 0.43s, 18.2MB / 24.5MB | vipsthumbnail: 0.36s, 138MB / 123MB | 0.44s, 186MB / 189MB | 0.69s, 419MB / 415MB |  | less memory |
| in.ico | 0.03s, 9.6MB / 15.9MB |  |  | 0.13s, 43.2MB / 38.1MB |  | less memory |
| in.icns | 0.04s, 8.8MB / 15.1MB |  |  | 0.14s, 47.6MB / 42.8MB |  | less memory |
| in.svg | 0.28s, 42.9MB / 48.3MB | vipsthumbnail: 1.71s, 40.1MB / 34.1MB | 1.77s, 90.5MB / 96.2MB |  |  | **more memory** |
| in.svg to PNG, same size | 1.67s, 49.8MB / 55.5MB, 91.6MB to temporary files | 62.39s, 148MB / 205MB |  |  |  | less memory |

#### Writing: the photograph in each format, the same size

| Feature | goimgtools | libvips | sharp | Pillow | Other | Verdict |
|---|---|---|---|---|---|---|
| bmp | 0.17s, 8.3MB / 14.8MB, file 68.7MB |  |  | 0.21s, 114MB / 110MB, file 68.7MB |  | less memory |
| qoi | 0.33s, 8.2MB / 14.6MB, file 44.4MB |  |  | 23.14s, 198MB / 199MB, file 44.4MB |  | less memory |
| ppm | 0.17s, 8.2MB / 14.6MB, file 68.7MB |  |  | 0.21s, 114MB / 110MB, file 68.7MB |  | less memory |
| tga | 0.24s, 8.2MB / 14.6MB, file 68.5MB |  |  | 0.23s, 116MB / 111MB, file 68.7MB |  | less memory |
| pdf | 0.01s, 7.6MB / 14.0MB, file 7.2MB |  |  |  |  | this package only |
| pdf quality 85 | 0.17s, 8.6MB / 14.8MB, file 5.7MB |  |  | 0.27s, 127MB / 123MB, file 5.7MB |  | less memory |
| tiff none | 0.14s, 8.2MB / 14.6MB, file 68.7MB | 0.11s, 36.2MB / 24.9MB, file 68.7MB | 0.19s, 88.9MB / 87.0MB, file 68.7MB | 0.23s, 116MB / 112MB, file 68.7MB |  | less memory |
| tiff deflate predictor | 1.26s, 9.3MB / 15.8MB, file 45.8MB | 1.05s, 35.9MB / 25.1MB, file 44.9MB | 1.13s, 87.9MB / 87.4MB, file 44.9MB | 1.49s, 117MB / 112MB, file 63.1MB |  | less memory |
| tiff lzw predictor | 0.72s, 8.3MB / 14.7MB, file 50.8MB | 0.79s, 35.7MB / 24.8MB, file 50.8MB | 0.91s, 88.1MB / 87.4MB, file 50.8MB | 0.84s, 117MB / 112MB, file 74.0MB |  | less memory |
| tiff jpeg | 0.20s, 23.8MB / 29.8MB, file 5.7MB | 0.17s, 35.4MB / 25.8MB, file 5.7MB | 0.22s, 86.0MB / 88.0MB, file 5.7MB | 0.34s, 117MB / 112MB, file 16.0MB |  | less memory |
| tiff tiles 256 | 1.06s, 14.9MB / 21.3MB, file 52.8MB | 1.08s, 37.2MB / 25.6MB, file 47.3MB | 1.17s, 85.4MB / 83.1MB, file 47.3MB |  |  | less memory |
| tiff pyramid | 1.41s, 20.8MB / 27.4MB, 17.4MB to temporary files, file 70.3MB | 1.43s, 59.6MB / 58.5MB, file 61.2MB | 1.52s, 108MB / 116MB, file 61.2MB |  |  | less memory |
| ico 256 | 0.28s, 17.0MB / 24.3MB, file 46KB |  |  |  |  | this package only |
| ico 16,32,48,256 | 0.58s, 24.9MB / 35.6MB, 91.6MB to temporary files, file 28KB |  |  | 0.65s, 212MB / 208MB, file 37KB |  | less memory |
| icns standard sizes | 1.07s, 39.5MB / 48.7MB, file 1.9MB |  |  | 0.43s, 195MB / 190MB, file 2.3MB |  | less memory, 2.5 times the fastest's time |
| png level 1 | 0.27s, 9.2MB / 15.4MB, file 60.7MB | 0.69s, 33.6MB / 21.9MB, file 71.2MB | 0.70s, 82.0MB / 80.0MB, file 71.2MB | 1.07s, 114MB / 110MB, file 63.2MB |  | less memory |
| png level 4 | 0.96s, 9.3MB / 15.5MB, file 44.4MB | 1.15s, 33.6MB / 21.9MB, file 63.6MB | 1.21s, 81.8MB / 79.5MB, file 63.6MB | 1.49s, 114MB / 110MB, file 44.7MB |  | less memory |
| png level 9 | 12.75s, 24.7MB / 31.3MB, file 42.0MB | 1.39s, 33.7MB / 22.0MB, file 58.0MB; libvips filter=all: 3.63s, 33.7MB / 21.9MB, file 45.1MB | 1.45s, 82.0MB / 79.7MB, file 58.0MB | 3.33s, 114MB / 110MB, file 45.8MB |  | less memory, 9.2 times the fastest's time |
| png 256 colours | 2.25s, 16.1MB / 22.8MB, 91.6MB to temporary files, file 9.7MB | 5.56s, 132MB / 124MB, file 10.2MB | 5.61s, 180MB / 182MB, file 10.2MB | 1.84s, 314MB / 310MB, file 9.5MB |  | less memory |
| png level 10, 1536 wide | 24.55s, 59.8MB / 76.8MB, file 1.9MB |  |  |  |  | this package only |
| gif same size | 1.80s, 13.8MB / 20.2MB, 91.6MB to temporary files, file 11.7MB | 7.33s, 290MB / 309MB, file 12.5MB | 7.40s, 338MB / 366MB, file 12.5MB | 1.73s, 314MB / 310MB, file 11.6MB |  | less memory |
| gif lossy 24, 1536 wide | 0.46s, 15.1MB / 21.6MB, 6.0MB to temporary files, file 241KB |  |  |  |  | this package only |
| webp near-lossless 60 | 16.07s, 29.9MB / 41.1MB, 93.6MB to temporary files, file 15.2MB | 19.48s, 693MB / 752MB, file 15.1MB | 19.68s, 740MB / 808MB, file 15.1MB |  | cwebp: 19.68s, 618MB / 674MB, file 15.1MB | less memory |
| webp lossless effort 9 | 6.09s, 28.0MB / 43.8MB, 93.1MB to temporary files, file 27.6MB |  | 9.64s, 800MB / 852MB, file 28.3MB |  | cwebp: 44.31s, 1316MB / 1326MB, file 28.3MB | less memory |
| jpeg progressive | 0.88s, 8.6MB / 14.9MB, 68.7MB to temporary files, file 5.2MB | 0.53s, 170MB / 159MB, file 5.0MB | 0.56s, 218MB / 216MB, file 5.0MB | 0.52s, 280MB / 293MB, file 5.0MB |  | less memory |
| jpeg optimize | 0.20s, 8.1MB / 14.4MB, 19.1MB to temporary files, file 5.4MB | 0.18s, 170MB / 159MB, file 5.2MB | 0.32s, 218MB / 216MB, file 5.2MB | 0.36s, 280MB / 293MB, file 5.2MB |  | less memory |
| jpeg trellis | 1.26s, 9.2MB / 15.6MB, file 5.4MB | 1.35s, 170MB / 159MB, file 4.8MB | 1.36s, 218MB / 217MB, file 4.8MB |  |  | less memory |
| jpeg progressive trellis jpegli tables | 2.11s, 10.0MB / 16.3MB, 68.7MB to temporary files, file 4.7MB | 2.67s, 186MB / 187MB, file 3.9MB | 2.81s, 234MB / 245MB, file 3.9MB |  |  | less memory |
| jpeg 4:4:4 | 0.22s, 7.7MB / 14.0MB, file 7.0MB | 0.19s, 32.8MB / 21.2MB, file 6.7MB | 0.39s, 354MB / 354MB, file 6.1MB | 0.28s, 206MB / 201MB, file 6.7MB |  | less memory |
| jpeg overshoot deringing | 0.18s, 7.7MB / 14.1MB, file 6.0MB | 0.20s, 32.8MB / 21.2MB, file 5.7MB | 0.30s, 218MB / 216MB, file 5.2MB |  |  | less memory |
| jpeg -max-size 1MB | 0.35s, 20.6MB / 29.8MB, 34.3MB to temporary files, file 1012KB |  |  |  |  | this package only |
| jpeg -min-ssim 0.98 | 6.60s, 31.1MB / 37.7MB, 78.1MB to temporary files, file 7.0MB |  |  |  |  | this package only |
| deep zoom tiles | 0.34s, 34.1MB / 40.5MB, file 5.1MB | 0.33s, 41.7MB / 30.3MB, file 5.0MB | 0.71s, 94.3MB / 93.0MB, file 5.1MB |  |  | less memory |
| google tiles | 0.36s, 39.6MB / 46.1MB, file 5.1MB | 0.36s, 46.2MB / 34.6MB, file 4.9MB | 0.77s, 99.8MB / 97.6MB, file 5.1MB |  |  | less memory |
| zoomify tiles | 0.34s, 35.8MB / 42.2MB, file 5.1MB | 0.31s, 41.4MB / 29.9MB, file 4.9MB | 0.71s, 93.8MB / 91.5MB, file 5.1MB |  |  | less memory |

#### Options, JPEG in and out

| Feature | goimgtools | libvips | sharp | Pillow | Other | Verdict |
|---|---|---|---|---|---|---|
| crop 3000x2000 | 0.12s, 8.3MB / 14.6MB | 0.10s, 27.5MB / 17.7MB | 0.20s, 110MB / 110MB | 0.21s, 160MB / 155MB |  | less memory |
| trim | 0.25s, 9.4MB / 15.6MB, 91.6MB to temporary files |  | 4.58s, 269MB / 278MB | 0.39s, 412MB / 408MB |  | less memory |
| extend 100 mirror | 0.24s, 9.3MB / 15.7MB, 4.6MB to temporary files | 0.18s, 98.2MB / 86.8MB | 0.36s, 281MB / 287MB |  |  | less memory |
| flip | 0.40s, 14.3MB / 20.6MB, 91.6MB to temporary files | 0.17s, 98.9MB / 87.6MB | 0.35s, 269MB / 276MB | 0.29s, 298MB / 293MB |  | less memory, 2.3 times the fastest's time |
| flop | 0.20s, 8.8MB / 15.0MB | 0.17s, 98.9MB / 87.6MB | 0.32s, 219MB / 217MB | 0.29s, 297MB / 293MB |  | less memory |
| rotate 180 | 0.42s, 14.2MB / 20.5MB, 91.6MB to temporary files | 0.20s, 98.9MB / 87.6MB | 0.36s, 269MB / 276MB | 0.29s, 298MB / 293MB |  | less memory, 2.2 times the fastest's time |
| resize 1536 nearest | 0.14s, 8.8MB / 15.1MB | 0.12s, 42.9MB / 31.3MB | 0.16s, 86.8MB / 84.4MB | 0.19s, 126MB / 121MB |  | less memory |
| resize 1536 libvips method | 0.13s, 9.5MB / 15.7MB |  |  |  |  | this package only |
| resize 1536 pillow method | 0.34s, 9.6MB / 15.8MB |  |  |  |  | this package only |
| resize 1536 linear light | 0.29s, 9.7MB / 16.0MB | vipsthumbnail: 0.37s, 95.4MB / 83.9MB |  |  |  | less memory |
| resize 1536x1536 cover attention | 0.22s, 24.7MB / 31.2MB | vipsthumbnail: 0.16s, 53.4MB / 42.0MB | 0.23s, 100MB / 97.9MB |  |  | less memory |
| resize 1536x1536 cover entropy | 0.22s, 24.5MB / 30.6MB | vipsthumbnail: 0.16s, 53.4MB / 42.0MB | 0.24s, 100MB / 98.2MB |  |  | less memory |
| resize 1536x1536 contain | 0.19s, 9.7MB / 15.9MB |  | 0.18s, 92.2MB / 89.9MB | 0.34s, 144MB / 139MB |  | less memory |
| enlarge to 12000 wide | 1.31s, 16.5MB / 23.3MB | 2.55s, 40.1MB / 28.4MB | 2.85s, 638MB / 637MB | 1.29s, 847MB / 844MB |  | less memory |
| grayscale linear | 0.29s, 8.6MB / 14.8MB |  | 0.33s, 221MB / 219MB |  |  | less memory |
| sharpen mild | 0.35s, 9.2MB / 15.8MB |  |  |  |  | this package only |
| sharpen libvips | 1.87s, 12.7MB / 18.7MB | 0.96s, 35.0MB / 22.9MB | 1.06s, 220MB / 218MB |  |  | less memory |
| blur 10 | 0.27s, 13.0MB / 19.3MB | 0.27s, 32.0MB / 20.3MB | 0.36s, 269MB / 275MB | 0.59s, 297MB / 293MB |  | less memory |
| lut warm 9 | 0.83s, 8.9MB / 15.1MB |  |  |  |  | this package only |
| lut 33 | 0.83s, 12.2MB / 18.2MB |  |  | 0.62s, 299MB / 294MB |  | less memory |
| srgb from Display P3 | 0.31s, 8.9MB / 15.1MB | 0.25s, 30.7MB / 21.4MB | 0.42s, 217MB / 217MB | 0.45s, 298MB / 293MB |  | less memory |
| profile adobe-rgb | 0.38s, 8.4MB / 14.6MB |  |  |  |  | this package only |
| keep exif xmp iptc | 0.13s, 7.8MB / 14.1MB | 0.14s, 30.4MB / 21.2MB | 0.27s, 216MB / 217MB | 0.23s, 206MB / 201MB |  | less memory |
| exif fields | 0.16s, 7.8MB / 14.0MB |  |  |  |  | this package only |

#### Named operations (`Do`, `-op`), JPEG in and out

| Feature | goimgtools | libvips | sharp | Pillow | Other | Verdict |
|---|---|---|---|---|---|---|
| `affine:0.966,-0.259,0.259,0.966,white` | 0.59s, 13.3MB / 19.4MB, 91.8MB to temporary files | 0.44s, 98.2MB / 86.9MB | 0.60s, 344MB / 351MB | 0.78s, 397MB / 392MB |  | less memory |
| `arc:3000,2000,1500,1000,30,300,stroke=red,width=12` | 0.22s, 12.8MB / 19.2MB |  |  | 0.28s, 297MB / 293MB |  | less memory |
| `autocontrast:2` | 0.27s, 9.7MB / 15.9MB, 91.6MB to temporary files |  |  | 0.32s, 298MB / 293MB |  | less memory |
| `bandbool:and` | 0.21s, 8.8MB / 15.0MB | 0.17s, 29.1MB / 17.5MB | 0.25s, 220MB / 218MB |  |  | less memory |
| `boxblur:5` | 0.19s, 11.4MB / 18.0MB |  |  | 0.40s, 297MB / 293MB |  | less memory |
| `chord:3000,2000,1500,1000,30,300,fill=red` | 0.18s, 9.0MB / 15.2MB |  |  | 0.27s, 298MB / 293MB |  | less memory |
| `circle:3000,2000,1500,fill=#ff000080,stroke=blue,width=20` | 0.23s, 9.1MB / 15.3MB |  |  | 0.29s, 298MB / 293MB |  | less memory |
| `clahe:64,64` | 10.84s, 12.2MB / 18.7MB |  | 7.87s, 270MB / 276MB |  |  | less memory |
| `convolve:3,3,-1,-1,-1,-1,9,-1,-1,-1,-1` | 0.64s, 9.5MB / 15.6MB | 0.45s, 31.8MB / 20.2MB | 0.60s, 218MB / 216MB | 0.60s, 297MB / 293MB |  | less memory |
| `crop:1000,1000,4000,2000` | 0.13s, 8.4MB / 14.6MB |  |  |  |  | this package only |
| `curves:64:40,192:220` | 0.19s, 8.8MB / 15.0MB |  |  |  |  | this package only |
| `dilate:2` | 0.26s, 9.5MB / 15.6MB | 0.20s, 31.3MB / 19.6MB | 0.31s, 217MB / 215MB | 9.77s, 297MB / 293MB |  | less memory |
| `ellipse:3000,2000,2500,1500,fill=yellow,stroke=black,width=30` | 0.21s, 9.1MB / 15.4MB |  |  | 0.28s, 297MB / 293MB |  | less memory |
| `enhance:color,1.5` | 0.32s, 8.8MB / 15.0MB |  |  | 0.41s, 298MB / 293MB |  | less memory |
| `enhance:contrast,1.3` | 0.27s, 9.8MB / 16.0MB, 91.6MB to temporary files |  |  | 0.44s, 297MB / 293MB |  | less memory |
| `enhance:brightness,1.2` | 0.20s, 8.8MB / 15.0MB |  |  | 0.38s, 298MB / 293MB |  | less memory |
| `enhance:sharpness,2` | 0.59s, 9.2MB / 15.4MB |  |  | 0.59s, 298MB / 293MB |  | less memory |
| `equalize` | 0.27s, 9.8MB / 16.0MB, 91.6MB to temporary files | 0.22s, 96.1MB / 84.9MB |  | 0.32s, 297MB / 293MB |  | less memory |
| `erode:2` | 0.26s, 9.4MB / 15.6MB | 0.15s, 31.3MB / 19.7MB | 0.32s, 217MB / 215MB | 9.78s, 297MB / 293MB |  | less memory |
| `extend:200,200,200,200,mirror` | 0.26s, 9.4MB / 15.7MB, 9.2MB to temporary files |  |  |  |  | this package only |
| `extract-channel:green` | 0.18s, 8.8MB / 15.0MB | 0.13s, 29.1MB / 17.5MB | 0.29s, 169MB / 167MB | 0.27s, 229MB / 224MB |  | less memory |
| `filter:blur` | 0.78s, 9.2MB / 15.4MB |  |  | 0.82s, 297MB / 293MB |  | less memory |
| `filter:contour` | 0.67s, 9.2MB / 15.4MB |  |  | 0.60s, 298MB / 293MB |  | less memory |
| `filter:detail` | 0.50s, 9.2MB / 15.4MB |  |  | 0.49s, 298MB / 293MB |  | less memory |
| `filter:edge-enhance` | 0.58s, 9.2MB / 15.4MB |  |  | 0.55s, 298MB / 293MB |  | less memory |
| `filter:edge-enhance-more` | 0.66s, 9.2MB / 15.4MB |  |  | 0.60s, 297MB / 293MB |  | less memory |
| `filter:emboss` | 0.49s, 9.1MB / 15.4MB |  |  | 0.48s, 297MB / 293MB |  | less memory |
| `filter:find-edges` | 0.66s, 9.4MB / 15.6MB |  |  | 0.59s, 298MB / 293MB |  | less memory |
| `filter:sharpen` | 0.52s, 9.2MB / 15.4MB |  |  | 0.50s, 297MB / 293MB |  | less memory |
| `filter:smooth` | 0.47s, 9.2MB / 15.4MB |  |  | 0.48s, 297MB / 293MB |  | less memory |
| `filter:smooth-more` | 0.80s, 9.2MB / 15.4MB |  |  | 0.84s, 298MB / 293MB |  | less memory |
| `flatten:white` | 0.18s, 8.5MB / 14.8MB |  |  |  |  | this package only |
| `flip` | 0.40s, 14.3MB / 20.8MB, 91.6MB to temporary files |  |  |  |  | this package only |
| `flop` | 0.20s, 8.9MB / 15.3MB |  |  |  |  | this package only |
| `gamma:2.2` | 0.19s, 8.8MB / 15.0MB | 0.12s, 33.6MB / 21.9MB | 0.24s, 220MB / 218MB | 0.28s, 298MB / 293MB |  | less memory |
| `grayscale` | 0.20s, 8.3MB / 14.6MB |  |  |  |  | this package only |
| `grayscale:linear` | 0.29s, 8.8MB / 15.1MB |  |  |  |  | this package only |
| `line:100,100,5900,3900,100,3900,stroke=red,width=25` | 0.20s, 9.1MB / 15.2MB |  |  | 0.28s, 298MB / 293MB |  | less memory |
| `linear:1.2,-20` | 0.20s, 8.8MB / 15.0MB | 0.18s, 36.0MB / 24.4MB | 0.27s, 219MB / 217MB | 0.28s, 297MB / 293MB |  | less memory |
| `median:3` | 0.17s, 9.4MB / 15.6MB | 4.23s, 33.6MB / 22.0MB | 4.28s, 220MB / 218MB | 4.76s, 297MB / 293MB |  | less memory |
| `median:7` | 1.70s, 9.5MB / 15.7MB | 3.02s, 34.0MB / 22.3MB | 3.15s, 220MB / 218MB | 27.08s, 298MB / 293MB |  | less memory |
| `modulate:1.1,1.3,20` | 0.46s, 9.2MB / 15.3MB |  |  |  |  | this package only |
| `negate` | 0.20s, 8.8MB / 15.0MB | 0.17s, 33.1MB / 21.5MB | 0.33s, 219MB / 217MB | 0.28s, 298MB / 293MB |  | less memory |
| `normalize` | 0.93s, 9.8MB / 16.0MB, 91.6MB to temporary files |  | 1.21s, 285MB / 290MB |  |  | less memory |
| `pie:3000,2000,1500,1000,30,300,fill=red` | 0.19s, 9.0MB / 15.1MB |  |  | 0.28s, 298MB / 293MB |  | less memory |
| `polygon:500,500,5500,800,4000,3500,800,3000,fill=#00ff0080,stroke=black,width=10` | 0.26s, 9.1MB / 15.2MB |  |  | 0.30s, 298MB / 293MB |  | less memory |
| `posterize:3` | 0.20s, 8.8MB / 15.0MB |  |  | 0.28s, 297MB / 293MB |  | less memory |
| `rank:5,12` | 1.52s, 9.4MB / 15.6MB | 2.96s, 33.7MB / 22.0MB |  | 15.08s, 297MB / 293MB |  | less memory |
| `recomb:0.393,0.769,0.189,0.349,0.686,0.168,0.272,0.534,0.131` | 0.35s, 8.9MB / 15.0MB | 0.34s, 36.0MB / 24.3MB | 0.49s, 223MB / 221MB | 0.39s, 297MB / 293MB |  | less memory |
| `rect:500,500,5000,3000,fill=#0000ff60,radius=200` | 0.26s, 9.0MB / 15.2MB |  |  | 0.30s, 297MB / 293MB |  | less memory |
| `resize:1536,0` | 0.18s, 9.4MB / 15.7MB |  |  |  |  | this package only |
| `rotate:15,white,pillow,bicubic,expand` | 2.46s, 13.4MB / 19.8MB, 91.8MB to temporary files |  |  | 1.34s, 397MB / 392MB |  | less memory |
| `sharpen:2,libvips` | 1.88s, 12.7MB / 18.7MB |  |  |  |  | this package only |
| `solarize` | 0.19s, 8.8MB / 15.0MB |  |  | 0.27s, 298MB / 293MB |  | less memory |
| `threshold` | 0.28s, 8.6MB / 14.8MB |  | 0.29s, 221MB / 219MB | 0.28s, 229MB / 224MB |  | less memory |
| `threshold:128,channels` | 0.20s, 8.8MB / 15.1MB |  |  |  |  | this package only |
| `tint:#704214` | 0.30s, 8.9MB / 15.3MB |  | 0.33s, 222MB / 219MB | 0.31s, 297MB / 293MB |  | less memory |
| `transform:6000,4000,perspective,1,0.1,0,0.05,1,0,0.00002,0.00001` | 1.02s, 12.4MB / 18.3MB, 91.8MB to temporary files |  |  | 0.76s, 298MB / 293MB |  | less memory |
| `trim` | 0.25s, 9.4MB / 15.8MB, 91.6MB to temporary files |  |  |  |  | this package only |
| `blur:3,libvips` | 0.26s, 11.5MB / 18.0MB | 0.20s, 31.7MB / 20.0MB | 0.29s, 269MB / 276MB |  |  | less memory |
| `ensure-alpha:0.5` | 0.15s, 8.2MB / 14.5MB |  | 0.20s, 94.0MB / 92.3MB | 0.26s, 206MB / 201MB |  | less memory |
| `unflatten` | 0.17s, 8.2MB / 14.5MB |  | 0.27s, 95.9MB / 94.1MB |  |  | less memory |
| `remove-alpha` | 0.14s, 8.2MB / 14.6MB |  | 0.17s, 89.0MB / 87.0MB | 0.24s, 208MB / 203MB |  | less memory |
| `negate:alpha` | 0.16s, 8.4MB / 14.7MB |  | 0.19s, 94.8MB / 93.1MB |  |  | less memory |

#### Watermarks and text

| Feature | goimgtools | libvips | sharp | Pillow | Other | Verdict |
|---|---|---|---|---|---|---|
| logo, southeast | 0.17s, 9.4MB / 15.7MB |  |  |  |  | this package only |
| logo tiled, 30 degrees, opacity 0.3 | 0.26s, 12.0MB / 18.5MB |  | 0.42s, 218MB / 215MB | 0.31s, 206MB / 201MB |  | less memory |
| logo 20% wide, multiply | 0.19s, 14.5MB / 20.9MB |  |  |  |  | this package only |
| text watermark | 0.20s, 14.1MB / 19.8MB |  | 0.32s, 222MB / 216MB | 0.27s, 206MB / 201MB |  | less memory |
| text watermark tiled | 0.26s, 13.6MB / 19.5MB |  |  |  |  | this package only |
| invisible mark, blind | 1.22s, 14.4MB / 20.2MB |  |  |  |  | this package only |
| invisible mark, screen | 1.20s, 14.4MB / 20.5MB |  |  |  |  | this package only |
| text image, wrapped | 0.14s, 13.5MB / 20.7MB |  | 0.10s, 70.3MB / 71.2MB | 0.21s, 32.0MB / 26.8MB |  | less memory |

#### Animation (the GIF of 18 photographs, 768×512)

| Feature | goimgtools | libvips | sharp | Pillow | Other | Verdict |
|---|---|---|---|---|---|---|
| APNG to GIF | 1.16s, 28.1MB / 34.4MB, 27.0MB to temporary files, file 4.9MB |  |  | 0.61s, 41.3MB / 36.3MB, file 5.0MB |  | less memory |
| GIF to APNG | 1.34s, 32.3MB / 40.6MB, 27.8MB to temporary files, file 5.5MB |  |  | 0.67s, 57.8MB / 53.0MB, file 9.6MB |  | less memory, 2.0 times the fastest's time |
| animated WebP to GIF | 1.59s, 27.8MB / 34.3MB, 27.0MB to temporary files, file 3.4MB | 4.11s, 48.4MB / 45.1MB, file 4.3MB | 4.24s, 96.6MB / 103MB, file 4.3MB | 1.50s, 47.5MB / 42.9MB, file 4.2MB |  | less memory |
| GIF lossy 24 | 0.96s, 23.8MB / 29.9MB, 27.0MB to temporary files, file 2.3MB |  |  |  | gifsicle: 1.08s, 16.3MB / 16.2MB, file 4.6MB | **more memory** |
| GIF effort 1, frame delay 50 | 0.59s, 22.6MB / 28.6MB, 27.0MB to temporary files, file 5.0MB |  |  |  | gifsicle: 0.20s, 10.9MB / 11.7MB, file 5.0MB | **more memory**, 3.0 times the fastest's time |
| GIF watermark | 1.57s, 26.9MB / 34.3MB, 27.0MB to temporary files, file 3.1MB |  |  |  |  | this package only |

#### Commands that only look

| Feature | goimgtools | libvips | sharp | Pillow | Other | Verdict |
|---|---|---|---|---|---|---|
| info | 0.01s, 7.1MB / 13.6MB | vipsheader: 0.01s, 8.9MB / 4.7MB | 0.08s, 57.6MB / 62.6MB | 0.09s, 22.3MB / 17.5MB |  | less memory |
| info, PSD | 0.01s, 7.5MB / 13.9MB |  |  | 0.11s, 24.4MB / 19.8MB |  | less memory |
| check JPEG | 0.10s, 7.9MB / 14.3MB | libvips avg: 0.13s, 27.3MB / 15.9MB |  | 0.18s, 114MB / 109MB |  | less memory |
| check PNG | 0.29s, 7.8MB / 14.8MB | libvips avg: 0.35s, 19.6MB / 15.4MB |  | 0.40s, 114MB / 109MB |  | less memory |
| compare | 0.28s, 8.8MB / 15.2MB |  |  |  |  | this package only |
| stats | 0.40s, 8.5MB / 14.9MB | 0.13s, 27.4MB / 16.0MB | 0.53s, 144MB / 142MB | 0.23s, 114MB / 109MB |  | less memory, 3.0 times the fastest's time |
| hash | 0.08s, 7.5MB / 13.9MB |  |  |  |  | this package only |
| placeholder | 0.15s, 8.3MB / 14.6MB |  |  |  |  | this package only |
| exif | 0.01s, 7.2MB / 13.7MB |  |  | 0.09s, 22.5MB / 17.5MB |  | less memory |
| readmark blind | 1.02s, 57.8MB / 64.2MB |  |  |  |  | this package only |
| readmark, no mark | 4.34s, 50.8MB / 57.4MB |  |  |  |  | this package only |
| pages: 3 photographs to PDF | 0.02s, 8.8MB / 16.0MB |  |  | 0.61s, 389MB / 385MB |  | less memory |
| batch: 10 photographs to 1536 wide | 0.73s, 25.2MB / 32.0MB | vipsthumbnail: 1.30s, 103MB / 53.4MB | 0.54s, 142MB / 133MB | 2.65s, 144MB / 139MB |  | less memory |
| batch -jobs 1 | 1.84s, 13.9MB / 20.0MB |  |  |  |  | this package only |

Where the tools do not do the same work:

- `in_pyr.tif`: `vipsthumbnail` always reads a pyramid from its nearest level; the row below it is this package doing the same (`-fast-shrink`).
- `in.svg`: another renderer (librsvg); across series the working set here has measured 38–49MB against librsvg's 40MB.
- `icns standard sizes`: the PNG images at level 9, the smallest (1.9MB against Pillow's 2.3MB); with `-png-level 7` 0.36s for 2.08MB.
- `png level 9`: libvips', sharp's and Pillow's level 9 is zlib's; here a near-optimal parse with each row's filter tried against the rows before it, for a file 28% smaller than libvips' and 8% smaller than Pillow's (which filters). Level 7, the default, is in the first table.
- `flip`, `rotate 180`: libvips holds the decoded image (99MB); here it goes through a temporary file, within the 64MB limit.
- `GIF to APNG`: Pillow writes each frame whole; here each frame is the smallest of several ways of writing it, for a file 43% smaller.
- `GIF lossy 24`: the same number is not the same loss (see Quality: 24 here loses more than gifsicle's 200, SSIMULACRA2 60.3 against 70.5), and gifsicle keeps frames as 8-bit indexes without true-colour work; `GIF effort 1, frame delay 50`: gifsicle's `--delay` rewrites the delays without decoding the frames.
- `stats`: libvips' `stats` gives each channel's extremes, sums and deviation only; sharp's `stats()`, which also gives the entropy, the sharpness and the dominant colour, as here, takes 0.53s.
- `text image, wrapped`: sharp's text has no background (one channel and alpha); here it is on white.

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
| PNG encoding time | On one thread 11% more time than libpng's (libvips, `filter=all`) for a file 0.8% smaller: 1.83s against 1.65s. On four threads ahead: 0.91s against 1.19s (libvips' unfiltered file) | Left: the time is the Deflate coder's, a Go port of libdeflate's lazy parser that takes this picture at about 60MB a second whatever the depth of its search (depths of 16, 35, 50 and 100 measured: 1.77s to 1.81s, the files within 0.02% of each other). Level 6 is faster than libpng's `filter=all` (1.38s) for a file 3% larger |
| Blur | On one thread 0.28s against libvips' 0.15s (0.19s of CPU: libvips decodes and encodes on other cores even when told to use one); on four threads 0.15s against 0.14s. `BlurLibvips` here, libvips' kernel to the byte, takes 0.24s (0.11s on four threads); it took 0.73s before its passes ran with AVX2 | Left: decoding and encoding the JPEG alone take 0.16s on one core here, so one thread cannot reach 0.15s; libvips does that part on other cores |
| Rotation by 90°, flips, rotation by 180° | 0.32s against 0.17s (0.68s before the rows of a round were written as one block); flip 0.40s against 0.17s, rotation by 180° 0.42s against 0.20s; each through a temporary file of 92MB | Left, by design: libvips holds the image in memory (97–99MB); here it is turned through a store that may be a file, in 14MB |
| Rotation by other angles, transforms | 0.64s against 0.50s on one thread, 0.56s against 0.31s on four. As Pillow: `RotatePillow` (bicubic) 2.46s against Pillow's 1.33s, `Transform` (perspective) 1.02s against 0.76s; they took 4.60s and 1.48s before a window of pixels within one cached tile was read from it at once | Left, by design: the source is kept in working storage in tiles of 32 pixels and read back through a small cache, so the rotation takes 14MB where libvips holds the decoded image (98MB) and Pillow 376MB. Reading the tiles back is a quarter of the time (0.155s of 0.60s measured); larger tiles would buy time with memory |
| GIF decoding | Was 0.24s against 0.19s for a 24 megapixel GIF | Closed: 0.17s, with a decoder of its own for the LZW data (the same pixels and errors as `compress/lzw`) |
| Animated GIF resized | At the default effort gifsicle is twice as fast (0.32s against 0.69s; 0.47s with its Lanczos method and `-O3`); at `Effort: 1` the times are level (0.31s) and the file is still a third smaller than gifsicle's (925KB against 1.35MB). Memory is 4–5MB more than gifsicle's (17.9MB against 22.2–23.0MB): 7MB is the program itself and 6MB what is in use (the picture so far, the colour counts, a frame in work); the rest is what Go's collector lets the heap grow by before it collects | Time closed with `GIFOptions.Effort`. Memory left: collecting sooner (GOGC=25) brings this task to 18.6MB but costs the tasks that stream 1 to 2MB each (measured), so it is not set; gifsicle keeps frames as 8-bit indexes and does no true-colour work. Cutting the allocations from 48MB to 18MB in all did not move the peak |
| Animated GIF written (`GIF lossy 24`, `GIF effort 1` above) | 23–24MB against gifsicle's 16MB and 11MB, the frames through a temporary file of 27MB | Left: the frames are kept for a second pass (one palette for all, the smallest way of writing each), 4.6MB of them in memory and the rest in the file; gifsicle keeps 8-bit indexes and does no true-colour work. All the allocations together come to 13.5MB; the rest of the peak is the program (7MB) and what Go's collector lets the heap grow by. 34.8MB before the ways of writing a frame were tried in the same storage |
| PNG at level 9 (and the images of ICO and ICNS) | 44.9s for the 6000×4000 photograph, where zlib's level 9 takes 1.4–3.6s, for a file 7–28% smaller | Faster, the rest by design: the search for the split points of a block counted the symbols of a whole part at each point it tried, and now takes them from counts kept every 8KB of the parse; the hash tables of the search for matches of the rows' filters have 15 bits rather than 13, the chains of a photograph having been mostly other data of the same hash; and the three loops that took most of the rest (the parse of a block, the search of each row's filter, the estimate of each segment of rows) no longer branch on which matches a position has, which the processor guessed wrong at most positions of a photograph: the positions are looked at a stretch at a time (eight at once with AVX2), and the inner loop of the parse is in assembly on x86-64 (12.7s, the file the same, the memory the same; oxipng 8.0 `-o max` takes 54.1s and 616MB on one thread, for a file 0.6% larger); the rest is the search for the smallest file (see [Compression](compression.md)), the filter of each row tried five ways and each block parsed up to ten times; the default, level 7, takes 1.83s. ICO and ICNS keep level 9 by default, their images being small; with `CompressionLevel` (`-png-level`) 7 an ICNS of the standard sizes takes 0.36s against Pillow's 0.43s, its file still 11% smaller |
| Temporary files | Lossy WebP 37MB, lossless WebP 93MB, turns and flips 92MB, animations 27MB, for the 24 megapixel photograph | Left, by design: these need the whole picture before they write, and 92MB of pixels do not fit the 64MB limit. With a larger `MemoryLimit` the turns and flips hold the picture in memory instead (`-mem 1024`: no file, 102MB); the WebP encoders write theirs whatever the limit. Tiles, BMP and TIFF in strips or tiles (a pyramid's smaller levels aside) no longer write any (they wrote 18–69MB) |
| `stats` against libvips | 0.40s against 0.13s | Not like for like: libvips' `stats` gives each channel's extremes, sums and deviation; sharp's `stats()`, the same figures as here (entropy, sharpness, dominant colour), takes 0.53s |
| GIF to APNG, APNG to GIF | 1.34s and 1.16s against Pillow's 0.67s and 0.61s (3.06s and 1.41s before) | Left: each frame is coded in several ways and the smallest kept, for an APNG 43% smaller than Pillow's; ways whose data would be that of one already tried, or that are already larger than the best, are no longer coded |
| Pyramid TIFF read | Was 0.30s against `vipsthumbnail`'s 0.07s | Closed: with `FastShrinkOnLoad` the nearest level is read, 0.09s against 0.08s, in 13MB against 23MB |
| Tiles (Deep Zoom, Google, Zoomify) | Were 0.51s and up to 94MB with 18MB of temporary files, against libvips' 0.30s and 42MB | Closed: 0.34–0.36s and 34–40MB without temporary files, against 0.31–0.36s and 41–46MB |
| PDF of photographs, the invisible mark read, text images, icons | A JPEG to PDF took 2.0s for a 46MB document; reading a mark 177MB; a text image 0.22s against sharp's 0.10s; an ICNS 1.49s against Pillow's 0.43s | Closed: the JPEG goes in as it is (0.009s, 7.6MB; three photographs 0.02s against Pillow's 0.61s); the mark read in 58MB; grey text is a grey image (0.14s); `CompressionLevel` for icons (above) |
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
