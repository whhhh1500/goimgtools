English | [中文](zh-CN/performance.md)

# Performance and benchmarks

## Memory control

- Each stage processes a few rows at a time (a strip of about 256KB) and passes them to the next stage once processed.
- Resizing uses no intermediate buffer: each source row is resized horizontally as it is read, then accumulated by weight directly into the few target rows it affects.
- Where the first output row cannot be produced until the whole image has been read, the data is placed in the staging area, and written directly to disk when it is known not to fit. This applies to interlaced PNG and GIF, progressive JPEG, rotation and vertical flip, BMP output (the file must be written starting from the last row), and encoders that require information about the whole image before writing the file header (the PNG transparency check, WebP, compressed TIFF; for these, the staged data is already compressed or encoded).
- Staged data occupies at most 1/8 of the memory limit; the remainder is written to disk. The cost of writing to disk is small (measured on 12–24 MP images: interlaced GIF time unchanged, progressive JPEG about 20% slower, rotation about 0.1 seconds longer), whereas keeping the data in memory would raise the peak for medium-sized images to 20–45MB. To favour speed, raise `MemoryLimit`: when staged data is smaller than 1/8 of the limit, the disk is not accessed.
- Every buffer that grows with image size counts towards the memory limit. If the limit is insufficient, `ErrMemoryLimit` is returned; additional memory is never used silently.
- When `Rotate` is immediately followed by `Resize`, the image is shrunk before it is rotated, so only the smaller image is staged.

## CPU and threads

Processing is single-threaded by default, and CPU use stays within one core. `Threads(n)` sets the thread limit (up to the number of CPU cores):

- `Threads(2)`: decoding runs on one thread; resizing, compositing and encoding run on another.
- `Threads(3)`: encoding additionally runs on a dedicated thread (beneficial only when transcoding without shrinking and with large output).
- `Threads(4)` and above: the additional cores are assigned to stages that can divide their work: resizing, blur, both kinds of sharpening, colour adjustment, rotation by any angle, JPEG encoding (colour conversion, DCT and quantization; entropy coding remains sequential), PNG compression (in 256KB blocks; see below), lossy WebP encoding (macroblock analysis, macroblock rows processed as a wavefront, and separate re-encoding of each of the 2/4/8 coefficient partitions), and predictor selection and residuals in lossless WebP (LZ77 and the second pass remain sequential). These stages distribute each batch of rows, columns or blocks among idle cores, which compute it jointly. Several such stages in one chain still do not exceed the limit: the additional cores form a single pool shared by the whole chain, borrowed only when idle and returned when the work is complete.
- The number of active threads never exceeds the configured value, and output is byte-for-byte identical to single-threaded output (the tests compare the results with 0, 1, 3 and 7 helper threads for every operation).

Threads exchange data through a fixed buffer of 3 strips; when the consumer falls behind, the producer stops and waits. Operations that divide their work buffer a few additional rows per batch. Each additional thread therefore uses about 1–2MB more memory, which counts towards the memory limit. When staged rows have been written to a temporary file (with a very small memory limit), blur and sharpening revert to computing one row at a time and do not divide their work.

Measurements (6000×4000; output byte-for-byte identical at every thread count):

| Case | 1 thread | 3 threads | 4 threads | 8 threads |
|---|---|---|---|---|
| JPEG to JPEG, without shrinking | 0.61s | 0.43s | 0.33s | 0.27s |
| JPEG to PNG, without shrinking (level 7) | 2.10s | 1.89s | 1.16s | 0.49s |
| RGB PNG to PNG, without shrinking (level 7) | 1.58s | 1.01s | 0.71s | 0.45s |
| JPEG shrunk to 1536 | 0.41s | 0.23s | 0.22s | 0.22s |
| JPEG to lossy WebP, without shrinking | 8.81s | — | 4.57s | 1.85s |
| Real photograph 3840×2160 to lossy WebP | 1.51s | — | 0.80s | 0.34s |
| 1080p enlarged to 4K | 0.24s | 0.13s | 0.13s | 0.11s |
| 1080p enlarged to 8K | 0.84s | 0.44s | 0.36s | 0.29s |

**PNG block compression**: as in pigz, the filtered data is divided into 256KB blocks; each block is compressed independently, using the preceding 32KB as its dictionary, and terminated with a sync flush, and the blocks are concatenated into a single zlib stream. The block division does not depend on the thread count, and therefore neither does the output file. Without helper threads, data is compressed as it arrives and written out directly, with no additional memory. When the data fits in a single block (RGB of about 90,000 pixels), the output is byte-for-byte identical to `compress/zlib`; larger images produce slightly larger files because each block restarts its search for repeated data (measured at +0.005% on 24 MP).

## Shrinking JPEGs while decoding (optional)

`ResizeOptions{FastShrinkOnLoad: true}` (command line: `-fast-shrink`): when a JPEG is to be shrunk to less than half its size and `Resize` is the first operation (or is preceded only by rotation or flips), each decoded plane is first block-averaged by a factor of 2, 4 or 8, and the remainder of the resize is then applied. The output dimensions are the same as without this option; pixel values differ slightly (by less than 2 levels on average on smooth images), so it is disabled by default.

## Benchmarks

Environment: Windows 10, Go 1.27.1. "Standard library approach" denotes whole-image decoding with `image.Decode`, resizing with `CatmullRom.Scale` from `x/image`, and standard library encoding. Peak memory is the peak physical memory of the process as recorded by the operating system, of which about 5.4MB is the start-up footprint of a Go program.

### Shrink to fit within 1536 + white background + watermark + output

| Input (pixels) | Output | Peak memory, this library (1 thread / 2 threads) | Peak memory, standard library approach | Time, 1 thread | Time, 2 threads | Time, standard library approach |
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
| WebP lossy 6000×4000 | JPEG | 9.5MB / 11.9MB | 243MB | 1.07s | 0.85s | 1.42s |
| WebP lossless 6000×4000 | JPEG | 14.2MB / 15.0MB | 300MB | 1.00s | 0.80s | 1.52s |

- Total CPU time does not vary with the thread count (2 threads merely distribute the same work across two cores).
- For the progressive JPEGs, the coefficients (72MB at 6000×4000) were written to a temporary file; with the limit raised to 1GB so that the coefficients remain in memory, the time is 0.76s and the peak 78MB.
- With `FastShrinkOnLoad` enabled (single thread): JPEG baseline 4:2:0 about 0.3s, 4:4:4 about 0.5s.

### Other operations (input: JPEG baseline 4:2:0 6000×4000 in all cases; single thread)

| Operation | Output file | Peak memory | Time |
|---|---|---|---|
| Without shrinking, to JPEG (quality 85) | 5.9MB | 6.0MB | 0.61s |
| Without shrinking, to PNG | 43.7MB | 8.0MB | 1.66s |
| Without shrinking, to lossy WebP (quality 75) | 3.2MB | 19.7MB | 8.81s (1.85s with 8 threads; effort 1: 3.29s, 0.79s) |
| Without shrinking, to lossless WebP | 27.6MB | 32.2MB | 8.75s (8.32s with 8 threads) |
| Without shrinking, to BMP | 72MB | 6.7MB | 0.36s |
| Without shrinking, to TIFF (uncompressed) | 96MB | 6.4MB | 0.34s |
| Rotate 90°, without shrinking | 6.0MB | 12.4MB | 1.24s |
| Rotate 90°, then shrink to 1536 | 0.1MB | 15.5MB | 0.39s |
| Shrink to 1536, GIF output | 0.5MB | 12.9MB | 0.62s |
| Without shrinking, outlined text watermark tiled across the whole image | 6.2MB | 10.6MB | 0.75s |
| Shrink to 1536, lossy WebP output | 10KB | 9.2MB | 0.61s |

- When rotating by 90° without shrinking, the 96MB of pixel data is held in a temporary file; with all data in memory (a limit of 768MB or more), the time is about 0.8s and the peak about 108MB.
- Lossless WebP from PNG RGB 6000×4000 (41.6MB; the same image as in the table above): output 27.6MB, peak 32.6MB, 8.87s (8.21s with 8 threads).
- Before encoding, lossy WebP must convert the whole image to YUV, store it, and measure every macroblock (as required by segment-based quantization; see [Compatibility and comparisons](compatibility.md#webp-encoders)). Images of no more than 256KB after conversion are kept in memory; larger ones are written directly to a temporary file and read back only row by row. Measurements show no effect on the time.
- GIF output without shrinking takes 2.0s (peak 11.8MB). With a fixed palette (`FixedPalette`), it takes 4.3s: the colour search order on that path has been optimized; the result is byte-for-byte identical to the standard library and is produced more than 3 times as fast as by comparing one colour at a time.

### Image adjustment operations (JPEG baseline 4:2:0 6000×4000, without shrinking, JPEG output)

Times for this library include decoding and encoding. The Pillow column gives the time Pillow 12.1.1 takes on the same machine for this step alone (image already decoded in memory, single thread), for comparing the speed of the algorithms themselves.

| Operation | 1 thread | 3 threads | 6 threads | Peak memory (1 / 6 threads) | Pillow, this step only |
|---|---|---|---|---|---|
| No operation | 0.61s | 0.40s | 0.40s | 6.4 / 7.5MB | — |
| `Grayscale()` | 0.49s | 0.28s | 0.28s | 6.6 / 7.6MB | 0.02s |
| `Modulate(1.1, 1.3, 20)` | 1.17s | 0.55s | 0.42s | 6.9 / 9.9MB | — |
| `Blur(2)` | 1.11s | 0.55s | 0.32s | 9.1 / 15.2MB | 0.38s |
| `Blur(40)` | 0.96s | 0.56s | 0.25s | 15.8 / 20.8MB | 0.38s |
| `Sharpen()` (light) | 0.88s | 0.45s | 0.43s | 7.8 / 12.5MB | 0.24s (Pillow's `SHARPEN`, also 3×3) |
| `Sharpen(SharpenOptions{Sigma: 2})` | 1.26s | 0.58s | 0.46s | 10.4 / 17.1MB | 0.48s |
| `Trim(10)` | 0.73s | 0.60s | 0.60s | 7.4 / 8.4MB | — |
| `Extend(50, 50, 50, 50, white)` | 0.72s | 0.45s | 0.45s | 7.5 / 11.0MB | 0.02s |
| `RotateBy(30, white)`, output 7197×6465 | 1.27s | 0.76s | 0.75s | 12.2 / 14.7MB | 0.57s |

- Single-threaded, after subtracting the 0.61s of the no-operation case: blur adds 0.5s, of the same order as Pillow's 0.38s; rotation by 30° adds 0.66s, of which about 0.3s is spent encoding the additional 22 MP (estimated from the encoding time per megapixel), so the rotation itself is faster than Pillow's 0.57s.
- With 6 threads, light sharpening, unsharp masking and rotation level off at 0.43–0.75s; the remainder is single-threaded JPEG encoding (a sharpened image is harder to compress, and a rotated image is twice as large).
- With the memory limit lowered to 16MB (staged rows written to a temporary file), blur, sharpening, rotation and trimming produce results pixel-for-pixel identical to those without a memory limit (compared as PNG output); time increases by 0–15%, and peak memory remains within 13MB in all cases.

Evaluated but not implemented:

- Resizing directly on the three YCbCr planes (the chroma planes are only a quarter of the size, so the resizing stage could be nearly twice as fast). The output would no longer be byte-for-byte identical to `x/image`, so this could only be offered under an option such as `FastShrinkOnLoad`, where "pixels differ slightly"; in that mode, resizing accounts for only about one sixth of the time, so the estimated overall gain is about 9%, while the change would be substantial.
- Replacing PNG compression with `klauspost/compress`: compression in the Go 1.27 standard library is already a fast implementation of the same kind, so the replacement would bring no gain.

To reproduce the measurements:

```bash
go build -o memprobe.exe ./cmd/memprobe
```

```bash
./memprobe.exe api in.png out.jpg 1536 1536 logo.png
```

```bash
./memprobe.exe std in.png out.jpg 1536 1536 logo.png
```

Environment variables: `GOIMG_MEM` (memory limit, MB), `GOIMG_ADJUST=gray|modulate|blur:SIGMA|sharpen|usm:SIGMA|trim|extend|rotate:DEGREES` (one additional adjustment after resizing), `GOIMG_THREADS` (thread count; may exceed 3), `GOIMG_FAST=1`, `GOIMG_NORESIZE=1` (no shrinking), `GOIMG_ROTATE=1` (rotate by 90° first), `GOIMG_LOSSLESS=1`, `GOIMG_JPEG=opt|prog`, `GOIMG_COLORS=N` (PNG colour reduction), `GOIMG_MAXSIZE=KB`, `GOIMG_ENLARGE=1`, `GOIMG_TEXT=TEXT` (tiled text watermark), `GOIMG_PROF=FILE` (CPU profile), `GOIMG_HEAP=FILE` (memory allocation profile).
