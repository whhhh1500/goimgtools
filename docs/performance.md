English | [中文](zh-CN/performance.md)

# Performance and benchmarks

## How memory is kept low

- Each stage takes only a few rows at a time (a strip of about 256KB) and hands them to the next stage when it is done.
- Resizing keeps no intermediate buffer: each source row, as it is read, is resized horizontally and then added by weight straight into the few target rows it affects.
- Where the whole image must be seen before the first row can be produced, the data goes into working storage, and is written straight to disk when it is known not to fit: interlaced PNG / GIF, progressive JPEG, rotation and vertical flip, BMP output (the file must be written from the last row), and encodings that need information about the whole image before they can write the file header (the PNG transparency check, WebP, compressed TIFF; what is staged for these is the already compressed or encoded data).
- Staged data takes at most 1/8 of the memory limit; the rest is written to disk. Writing to disk costs little (measured on 12–24 MP: interlaced GIF time unchanged, progressive JPEG about 20% slower, rotation about 0.1 seconds longer), whereas keeping it in memory would push the peak for medium-sized images to 20–45MB. To get the speed back, raise `MemoryLimit`: when staged data is smaller than 1/8 of the limit, the disk is not touched.
- Every buffer that grows with image size counts towards the memory limit. When the limit is not enough, `ErrMemoryLimit` is returned; more memory is never used silently.
- When `Rotate` is followed directly by `Resize`, the image is shrunk first and then rotated, so what is staged is the small image.

## CPU and threads

Single-threaded by default; CPU use stays within one core. `Threads(n)` sets the limit (up to the number of CPU cores):

- `Threads(2)`: decoding on one thread; resizing, compositing and encoding on another.
- `Threads(3)`: encoding gets a thread of its own as well (useful only for transcoding with large output and no shrinking).
- `Threads(4)` and above: the extra cores go to stages that can share out their work — resizing, blur, both kinds of sharpening, colour adjustment, rotation by any angle, JPEG encoding (colour conversion, DCT, quantization; entropy coding is still sequential), PNG compression (compressed in 256KB blocks, see below), lossy WebP encoding (macroblock analysis, macroblock rows in a wavefront, re-encoding each of the 2/4/8 coefficient partitions separately), and the predictor selection and residuals of lossless WebP (LZ77 and the second pass are still sequential). They share out each batch of rows, columns or blocks to idle cores, which compute it together. Several such stages in one chain still do not go over the limit: the extra cores are one pool shared by the whole chain, borrowed only when idle and returned when the work is done.
- At no time are more threads working than the set value; output is byte-for-byte identical to single-threaded output (the tests compare the results with 0, 1, 3 and 7 helpers for every operation).

Threads hand data over through a fixed buffer of 3 strips; when the consumer cannot keep up, the producer stops and waits. Operations that share out work buffer a few more rows per batch. So each extra thread takes about 1–2MB more, which counts towards the memory limit. When staged rows have been written to a temporary file (very small memory limit), blur and sharpening fall back to computing one row at a time and do not share out work.

Measured (6000×4000; results byte-for-byte identical at every thread count):

| Case | 1 thread | 3 threads | 4 threads | 8 threads |
|---|---|---|---|---|
| JPEG to JPEG, no shrinking | 0.61s | 0.43s | 0.33s | 0.27s |
| JPEG to PNG, no shrinking (level 7) | 2.10s | 1.89s | 1.16s | 0.49s |
| RGB PNG to PNG, no shrinking (level 7) | 1.58s | 1.01s | 0.71s | 0.45s |
| JPEG shrunk to 1536 | 0.41s | 0.23s | 0.22s | 0.22s |
| JPEG to lossy WebP, no shrinking | 3.29s | — | 1.79s | 0.77s |
| Real photo 3840×2160 to lossy WebP | 0.87s | — | 0.55s | 0.34s |
| 1080p enlarged to 4K | 0.24s | 0.13s | 0.13s | 0.11s |
| 1080p enlarged to 8K | 0.84s | 0.44s | 0.36s | 0.29s |

**PNG block compression**: as in pigz, the filtered data is cut into 256KB blocks; each block is compressed on its own with the preceding 32KB as its dictionary, ends with a sync flush, and the blocks are joined end to end into one zlib stream. The block split does not depend on the thread count, so neither does the file; with no helpers, data is compressed as it arrives and written straight out, using no extra memory. When the data fits in one block (RGB of about 90,000 pixels), the output is byte-for-byte identical to `compress/zlib`; larger images give slightly larger files because each block starts its search for repeats afresh (measured +0.005% on 24 MP).

## Shrinking JPEGs while decoding (optional)

`ResizeOptions{FastShrinkOnLoad: true}` (command line `-fast-shrink`): when a JPEG is to be shrunk to less than half its size and `Resize` is the first operation (only rotation / flips before it also counts), each decoded plane is first averaged in blocks of 2, 4 or 8, and then the rest of the resize is done. The output size is the same as without it; pixels differ slightly (less than 2 levels on average on smooth images), so it is off by default.

## Benchmarks

Windows 10, Go 1.27.1. "The standard library's way" means decoding the whole image with `image.Decode` + `CatmullRom.Scale` from `x/image` + standard library encoding. Peak memory is the peak physical memory of the process as recorded by the operating system; about 5.4MB of it is what a Go program takes at start-up.

### Shrink to fit within 1536 + white background + watermark + output

| Input (pixels) | Output | Peak memory, this library (1 thread / 2 threads) | Peak memory, the standard library's way | Time, 1 thread | Time, 2 threads | Time, the standard library's way |
|---|---|---|---|---|---|---|
| PNG RGBA 6900×9200 | JPEG | 10.4MB / 11.2MB | 588MB | 1.63s | 1.21s | 3.10s |
| PNG RGBA 6900×9200 | PNG | 11.2MB / 12.2MB | 591MB | 1.71s | 1.27s | 3.16s |
| PNG interlaced RGB 6000×4000 | JPEG | 8.8MB / 10.9MB | 379MB | 0.56s | 0.50s | 1.02s |
| JPEG baseline 4:2:0 6000×4000 | JPEG | 8.3MB / 9.0MB | 242MB | 0.38s | 0.21s | 0.87s |
| JPEG baseline 4:4:4 6000×4000 | JPEG | 8.3MB / 8.8MB | 277MB | 0.57s | 0.37s | 1.02s |
| JPEG progressive 6000×4000 | JPEG | 8.8MB / 10.5MB | 373MB | 0.87s | 0.75s | 1.07s |
| JPEG progressive 5000×8000 | JPEG | 8.8MB / 10.4MB | 535MB | 1.43s | 1.23s | 1.58s |
| GIF 6000×4000 | JPEG | 8.4MB / 10.5MB | 230MB | 0.29s | 0.15s | 0.85s |
| GIF interlaced 6000×4000 | JPEG | 9.1MB / 10.7MB | 248MB | 0.32s | 0.30s | 0.86s |
| BMP 24-bit 6000×4000 | JPEG | 8.7MB / 10.5MB | 299MB | 0.21s | 0.18s | 0.58s |
| TIFF uncompressed 6000×4000 | JPEG | 8.4MB / 10.5MB | 535MB | 0.23s | 0.18s | 0.67s |
| TIFF LZW 6000×4000 | JPEG | 8.6MB / 10.6MB | 535MB | 0.74s | 0.57s | 1.18s |
| TIFF Deflate 6000×4000 | JPEG | 13.2MB / 14.2MB | 461MB | 0.58s | 0.40s | 1.08s |
| WebP lossy 6000×4000 | JPEG | 8.9MB / 11.1MB | 243MB | 0.94s | 0.73s | 1.42s |
| WebP lossless 6000×4000 | JPEG | 14.2MB / 15.0MB | 300MB | 1.00s | 0.80s | 1.52s |

- Total CPU time does not change with the thread count (2 threads only spread the same work over two cores).
- The coefficients of the progressive JPEGs (72MB at 6000×4000) were written to a temporary file; with the limit raised to 1GB so that the coefficients stay in memory, it takes 0.76s with a peak of 78MB.
- With `FastShrinkOnLoad` on (single thread): JPEG baseline 4:2:0 about 0.3s, 4:4:4 about 0.5s.

### Other operations (input always JPEG baseline 4:2:0 6000×4000, single thread)

| Operation | Output file | Peak memory | Time |
|---|---|---|---|
| No shrinking, to JPEG (quality 85) | 5.9MB | 6.0MB | 0.61s |
| No shrinking, to PNG | 43.7MB | 8.0MB | 1.66s |
| No shrinking, to lossy WebP (quality 75) | 2.6MB | 18.1MB | 3.29s (0.77s with 8 threads) |
| No shrinking, to lossless WebP | 27.6MB | 23.2MB | 3.74s (3.36s with 8 threads) |
| No shrinking, to BMP | 72MB | 6.7MB | 0.36s |
| No shrinking, to TIFF (uncompressed) | 96MB | 6.4MB | 0.34s |
| Rotate 90°, no shrinking | 6.0MB | 12.4MB | 1.24s |
| Rotate 90°, then shrink to 1536 | 0.1MB | 15.5MB | 0.39s |
| Shrink to 1536, output GIF | 0.5MB | 12.9MB | 0.62s |
| No shrinking, outlined text watermark tiled over the whole image | 6.2MB | 10.6MB | 0.75s |
| Shrink to 1536, output lossy WebP | 8KB | 8.8MB | 0.44s |

- When rotating 90° without shrinking, the 96MB of pixels are in a temporary file; with everything in memory (limit of 768MB or more) it takes about 0.8s with a peak of about 108MB.
- Lossless WebP on PNG RGB 6000×4000 (41.6MB, the same image as in the table above): output 27.6MB, peak 25.7MB, 3.96s (3.29s with 8 threads).
- Lossy WebP must first convert the whole image to YUV and store it, and measure every macroblock, before encoding (segment-based quantization needs this; see [Compatibility and comparisons](compatibility.md#webp-encoders)): small images of no more than 256KB after conversion are kept in memory; larger ones are written straight to a temporary file and read back only row by row. Measured, this does not affect the time.
- GIF output without shrinking takes 2.0s (peak 11.8MB). With a fixed palette (`FixedPalette`) it takes 4.3s: the colour search order on that path has been optimized; the result is byte-for-byte identical to the standard library, and it is more than 3 times as fast as comparing one colour at a time.

### Operations that adjust the picture (JPEG baseline 4:2:0 6000×4000, no shrinking, output JPEG)

This library's times include decoding and encoding. The Pillow column is the time Pillow 12.1.1 takes on the same machine for this one step only (image already decoded in memory, single thread), to compare the speed of the algorithms themselves.

| Operation | 1 thread | 3 threads | 6 threads | Peak memory (1 / 6 threads) | Pillow, this step only |
|---|---|---|---|---|---|
| Nothing | 0.61s | 0.40s | 0.40s | 6.4 / 7.5MB | — |
| `Grayscale()` | 0.49s | 0.28s | 0.28s | 6.6 / 7.6MB | 0.02s |
| `Modulate(1.1, 1.3, 20)` | 1.17s | 0.55s | 0.42s | 6.9 / 9.9MB | — |
| `Blur(2)` | 1.11s | 0.55s | 0.32s | 9.1 / 15.2MB | 0.38s |
| `Blur(40)` | 0.96s | 0.56s | 0.25s | 15.8 / 20.8MB | 0.38s |
| `Sharpen()` (light) | 0.88s | 0.45s | 0.43s | 7.8 / 12.5MB | 0.24s (Pillow's `SHARPEN`, also 3×3) |
| `Sharpen(SharpenOptions{Sigma: 2})` | 1.26s | 0.58s | 0.46s | 10.4 / 17.1MB | 0.48s |
| `Trim(10)` | 0.73s | 0.60s | 0.60s | 7.4 / 8.4MB | — |
| `Extend(50, 50, 50, 50, white)` | 0.72s | 0.45s | 0.45s | 7.5 / 11.0MB | 0.02s |
| `RotateBy(30, white)`, output 7197×6465 | 1.27s | 0.76s | 0.75s | 12.2 / 14.7MB | 0.57s |

- Single-threaded (after subtracting the 0.61s of doing nothing): blur adds 0.5s, the same order as Pillow's 0.38s; rotating by 30° adds 0.66s, of which about 0.3s is encoding the extra 22 MP (estimated from the encoding time per megapixel), so the rotation itself is faster than Pillow's 0.57s.
- With 6 threads, light sharpening, unsharp masking and rotation stop at 0.43–0.75s: what is left is the single-threaded JPEG encoding (a sharpened image is harder to compress, and a rotated image is twice as large).
- With the memory limit pushed down to 16MB (staged rows written to a temporary file), the results of blur, sharpening, rotation and trimming are pixel-for-pixel identical to those with no memory limit (compared as PNG output); time is 0–15% longer, and peak memory stays within 13MB in every case.

Evaluated but not done:

- Resizing directly on the three YCbCr planes (the chroma planes are only a quarter of the size, so the resizing part could be nearly twice as fast). The result would no longer be byte-for-byte identical to `x/image`, so it could only go into an option like `FastShrinkOnLoad`, where "pixels differ slightly"; in that mode resizing takes only about a sixth of the time, so the whole run would be an estimated 9% or so faster, while the change would not be small.
- Switching PNG compression to `klauspost/compress`: the compression in the Go 1.27 standard library is already the same kind of fast implementation, so switching gains nothing.

To measure again:

```bash
go build -o memprobe.exe ./cmd/memprobe
```

```bash
./memprobe.exe api in.png out.jpg 1536 1536 logo.png
```

```bash
./memprobe.exe std in.png out.jpg 1536 1536 logo.png
```

Environment variables: `GOIMG_MEM` (limit, MB), `GOIMG_ADJUST=gray|modulate|blur:SIGMA|sharpen|usm:SIGMA|trim|extend|rotate:DEGREES` (one more adjustment after resizing), `GOIMG_THREADS` (thread count, may be more than 3), `GOIMG_FAST=1`, `GOIMG_NORESIZE=1` (no shrinking), `GOIMG_ROTATE=1` (rotate by 90° first), `GOIMG_LOSSLESS=1`, `GOIMG_JPEG=opt|prog`, `GOIMG_COLORS=N` (PNG colour reduction), `GOIMG_MAXSIZE=KB`, `GOIMG_ENLARGE=1`, `GOIMG_TEXT=TEXT` (tiled text watermark), `GOIMG_PROF=FILE` (CPU profile), `GOIMG_HEAP=FILE` (memory allocation profile).
