English | [中文](zh-CN/performance.md)

# Performance and benchmarks

## Memory control

- Each stage processes a few rows at a time (a strip of about 256KB) and passes them to the next stage once processed.
- Resizing uses no intermediate buffer: each source row is resized horizontally as it is read, then accumulated by weight directly into the few target rows it affects.
- Where the first output row cannot be produced until the whole image has been read, the data is placed in the staging area, and written directly to disk when it is known not to fit. This applies to interlaced PNG and GIF, progressive JPEG read from a stream (`FromReader`), rotation and vertical flip, BMP output (the file must be written starting from the last row), and encoders that require information about the whole image before writing the file header (the PNG transparency check, WebP, compressed TIFF; for these, the staged data is already compressed or encoded).
- Staged data occupies at most 1/8 of the memory limit; the remainder is written to disk. The cost of writing to disk is small (measured on 12–24 MP images: interlaced GIF time unchanged, progressive JPEG about 20% slower, rotation about 0.1 seconds longer), whereas keeping the data in memory would raise the peak for medium-sized images to 20–45MB. To favour speed, raise `MemoryLimit`: when staged data is smaller than 1/8 of the limit, the disk is not accessed.
- Every buffer that grows with image size counts towards the memory limit. If the limit is insufficient, `ErrMemoryLimit` is returned; additional memory is never used silently.
- When `Rotate` is immediately followed by `Resize`, the image is shrunk before it is rotated, so only the smaller image is staged.
- A progressive JPEG (or one whose colour components are in separate scans) read from a file or from memory is decoded with its scans in step: a first pass finds where each scan's data starts, then each row of blocks is decoded by every scan in turn, each read from its own place in the file, and becomes pixels. Only that row's coefficients are kept (0.6MB at 6000 pixels wide), instead of the whole image's (72MB at 6000×4000, a temporary file under the default limit). Refinement scans visit only the coefficients that are not zero. Read from a stream, which cannot be read again, the image still has its coefficients stored.

## CPU and threads

Processing is single-threaded by default, and CPU use stays within one core. `Threads(n)` sets the thread limit (up to the number of CPU cores):

- `Threads(2)`: decoding runs on one thread; resizing, compositing and encoding run on another.
- `Threads(3)`: encoding additionally runs on a dedicated thread (beneficial only when transcoding without shrinking and with large output).
- `Threads(4)` and above: the additional cores are assigned to stages that can divide their work: resizing, blur, both kinds of sharpening, colour adjustment, median filter, convolution and CLAHE, rotation by any angle, JPEG encoding (colour conversion, DCT and quantization; entropy coding remains sequential), PNG compression (in 256KB blocks; see below), lossy WebP encoding (macroblock analysis, macroblock rows processed as a wavefront, and separate re-encoding of each of the 2/4/8 coefficient partitions), lossless WebP (predictor selection and residuals; the variants raced against each other, every row going to all of them at once, each with an optimal parse of its own; and the search for the matches of the optimal parse, a stretch of 8K pixels at a time, all of it entered into the hash chains first and each search starting from its own pixel's link, the links the stretch overwrites read from a copy: the matches are those of searching one pixel after the other; and the replays of the Huffman grouping, each block choosing its group on its own; the parse itself and the second pass remain sequential: 17 screenshots, photographs and game artwork at the default effort in 21s on eight threads instead of 41–43s, a palette screenshot of 3426×1912 in 5.6s instead of 14s, the 24 Kodak photographs in 7.1s instead of 11.1–11.6s, for 2 to 7MB more at the peak), the colour reduction of GIF and of PNG with a palette (the k-means passes over the colours and over the pixels; the dithered mapping stays sequential), and lossy WebP decoding, as libwebp's threaded decoder: the next band of macroblock rows is decoded while the last one is used, and each row is reconstructed and filtered while the next is parsed (the parsing itself is sequential), the pixels the same. These stages distribute each batch of rows, columns or blocks among idle cores, which compute it jointly. Several such stages in one chain still do not exceed the limit: the additional cores form a single pool shared by the whole chain, borrowed only when idle and returned when the work is complete.
- The number of active threads never exceeds the configured value, and output is byte-for-byte identical to single-threaded output (the tests compare the results with 0, 1, 3 and 7 helper threads for every operation).

Threads exchange data through a fixed buffer of 3 strips; when the consumer falls behind, the producer stops and waits. Operations that divide their work buffer a few additional rows per batch. Each additional thread therefore uses about 1–2MB more memory, which counts towards the memory limit. When staged rows have been written to a temporary file (with a very small memory limit), blur and sharpening revert to computing one row at a time and do not divide their work.

Measurements (6000×4000; output byte-for-byte identical at every thread count):

| Case | 1 thread | 3 threads | 4 threads | 8 threads |
|---|---|---|---|---|
| JPEG to JPEG, without shrinking | 0.14s | 0.08s | 0.08s | 0.08s |
| JPEG to PNG, without shrinking (level 7) | 1.63s | 1.55s | 0.89s | 0.54s |
| RGB PNG to PNG, without shrinking (level 7) | 1.59s | 1.38s | 0.80s | 0.44s |
| JPEG shrunk to 1536 | 0.17s | 0.10s | 0.11s | 0.09s |
| JPEG to lossy WebP, without shrinking | 3.79s | — | 2.10s | 0.97s |
| Real photograph 3840×2160 to lossy WebP | 0.95s | — | 0.54s | 0.33s |
| 1080p enlarged to 4K | 0.10s | 0.08s | 0.08s | 0.08s |
| 1080p enlarged to 8K | 0.36s | 0.31s | 0.26s | 0.21s |

**SIMD on amd64 and arm64**: every kernel below also has a NEON version for arm64 (Apple silicon, Graviton, Ampere; with Go 1.27 or later, whose assembler has the instructions: with Go 1.26 arm64 uses the Go code), tested against the Go code the same way. On processors with AVX2 (unless built with the `purego` tag) the JPEG decoder's inverse DCT and its conversion of colour, with the upsampling of the chroma (halved both ways or across, or at full size), run as assembly after libjpeg-turbo's SIMD code, on the arithmetic of the Go code, lane by lane: every byte is the same on every processor (the tests compare them on 200,000 blocks, corrupt ones included, and every row width up to 300). A 6000×4000 JPEG (quality 90) decodes on one core in 99ms (4:2:0), 109ms (4:2:2) and 126ms (4:4:4); libjpeg-turbo with its SIMD, through Pillow 12.1.1, takes 101, 110 and 121ms, the Go code alone 208, 226 and 250ms. The JPEG encoder's conversion of RGB to YCbCr, its downsampling of the chroma, its forward DCT and its quantization run the same way: the 6000×4000 photograph written at quality 85 in 70ms (libjpeg-turbo with its SIMD, through Pillow: 66ms; the Go code alone: 213ms), with optimized Huffman tables in 107ms (167ms; 388ms); the forward DCT and the quantization run in 16-bit lanes as libjpeg-turbo's AVX2 code has them (its division by two high multiplies, checked to give the rounded quotient for every coefficient and every quantization value), the zig-zag order is made with byte shuffles, and the codes and value bits go out through a 64-bit buffer, eight bytes at a time, as libjpeg-turbo's do. `MethodLibvips`'s reduce (each output the weighted sum of its window, in 12-bit fixed point, down the columns and across the rows) and the decoding of a JPEG shrunk by half on load (libjpeg's 4×4 inverse DCT) run the same way: a 6000×4000 JPEG (4:2:0) fitted into 1536×1536 with Lanczos 3 and `FastShrinkOnLoad`, written as a JPEG of quality 80 with optimized tables, sharp's file to the byte, in 98ms on one core (sharp 0.35.5 on one thread: 90ms; a progressive one 250ms against 246ms; with the default method 86ms; the Go code alone, the `purego` tag: 215ms). Most of it is the entropy decoding of the 7.5MB file. The lossy WebP decoder's inverse transform (and the DC-only one of blocks with no other coefficient), its loop filter (on amd64 as libwebp's SSE2 code has it, in 8-bit lanes with saturating arithmetic, the three inner edges of a macroblock's luma in one call that keeps the columns it has loaded for the next) and its upsampling and conversion of colour run the same way, after libwebp's SSE2 code; its reading of the coefficients runs as assembly on amd64 and arm64 (plain integer instructions, with any Go version), every bit read as the Go code reads it, the range renormalized from tables after every bit, without a branch: a 6000×4000 lossy WebP decodes on one core in 0.25s (libwebp 1.6's `dwebp`, built with its SIMD: 0.256s; libwebp 1.6 through Pillow: 0.50s), the Go code alone in 0.51s. The default resize (`x/image`'s arithmetic, in float64) makes its sums down and across with the four channels of a pixel in the lanes of one register (two on arm64), each product rounded and then added, never fused, in the order of the Go code, so every sum is the same to the bit: a 6000×4000 JPEG fitted into 1536×1536 and written as a JPEG in 0.17s on one core instead of 0.25s. Lossy WebP from colours (not Y'CbCr: PNG, a resized or flattened image) goes through sharp YUV, whose four passes share each step's columns among the helpers and run its upsampling and corrections with AVX2 or NEON, every sample libwebp's: 0.67s of the encoding of a 6000×4000 RGB PNG on one core, where libwebp's (sharp's `smartSubsample`) adds 1.2s to sharp's (3.27s instead of 2.07s at effort 4). The lossy WebP encoder's 4×4 forward transform (after libwebp's FTransform_SSE2), inverse transform, quantization and squared error, and the histogram of coefficients by which it sorts macroblocks into segments, run the same way: a 6000×4000 photograph at quality 80 is encoded on one core in 4.1s at the default effort, 1.7s at effort 1 (the Go code alone: 6.9s and 2.4s; libwebp 1.6 with its SIMD, through sharp: 5.7s at its slowest method 6, 2.0s at its default method 4).

**Optimized Huffman tables** (`JPEGOptions.Optimize`): the first pass stages each block's symbols in a spill (a byte each, the bits of the value after it; about half a byte per pixel of a photograph, in memory under the limit and on disk above it) while counting them, and the second writes them again with the tables made of the counts; libjpeg keeps the coefficients of the whole image instead (3 bytes per pixel at 4:2:0).

**PNG block compression**: as in pigz, the filtered data is divided into 256KB blocks; each block is compressed independently, using the preceding 32KB as its dictionary, and terminated with a sync flush, and the blocks are concatenated into a single zlib stream. The block division does not depend on the thread count, and therefore neither does the output file. At levels 1 to 6, without helper threads, data is compressed as it arrives and written out directly, with no additional memory; at the default level, 7, and at 8 and 9 each block is gathered and compressed whole (libdeflate compresses a buffer, not a stream). At level 10 the blocks are Zopfli's master blocks of 1,000,000 bytes, and each block's bits follow the last bits of the one before, without a sync flush: the stream is the one Zopfli writes for all the data, compressed on as many threads as the memory limit has room for a compressor (about 50MB each); the parts Zopfli splits a block into are parsed on the threads free, and a block filled while none is free waits for one (`pipeline.Env.TryGo`) rather than hold up the filtering of the rows. Blocks of 256KB with flushes would have made the 17 test images 0.034% larger, of 512KB without flushes 0.012%. With Go 1.27 or later, data of more than one block at levels 1 to 6 is compressed by one compressor reset for each block with that block's dictionary: Go 1.27's `compress/flate`, copied into `internal/gflate` with that reset added (the standard library's writer can only be made anew, 0.9MB of garbage for every block, which made level 6 16% slower) and, on little-endian processors, its words loaded at once as `klauspost/compress` loads them (10% faster at level 6), which writes the bytes the standard library writes (Go 1.26, whose own differ, compresses each block with them). At those levels, when the data fits in a single block (RGB of about 90,000 pixels), the output is byte-for-byte identical to `compress/zlib`; larger images produce slightly larger files because each block restarts its search for repeated data (measured at +0.005% on 24 MP).

**PNG and TIFF decompression**: PNG image data and Deflate-compressed TIFF strips and tiles are decompressed by `internal/inflate`, which follows libdeflate's decompressor: the input is taken into a 64-bit bit buffer eight bytes at a time without a branch, a table look-up gives a code's symbol, its extra bits and their base together, the entry of the next code is looked up before the buffer is refilled (as the refill leaves the bits below as they are), and matches are copied eight bytes at a time. It runs at about twice the speed of `klauspost/compress` and 2.4 times that of `compress/flate` (450–500MB/s of output for photographs, on one core). It reads the file as `compress/zlib` does through its `bufio.Reader` (4096 bytes at a time, only once a byte is needed), so that the checks image/png makes after the data see the same, and holds about 115KB.

## Shrinking JPEGs while decoding (optional)

`ResizeOptions{FastShrinkOnLoad: true}` (command line: `-fast-shrink`): when a JPEG is to be shrunk to less than half its size and `Resize` is the first operation (or is preceded only by rotation or flips), it is decoded straight to 1/2, 1/4 or 1/8 of its size, and the remainder of the resize is then applied. Each 8×8 block of coefficients becomes 4×4, 2×2 or 1×1 pixels by libjpeg's reduced inverse DCTs (its `jidctred.c`), which leave out most of the arithmetic. Shrunk by 8, a block is the mean of its pixels, its DC coefficient over 8, so a progressive JPEG has only its scans of DC coefficients decoded. The rest of the resize then scales the Y, Cb and Cr planes (or the gray one) as they are, each from its own size, the chroma from its subsampled one, to the size of the result, with the kernel's weights in 14-bit fixed point, with AVX2 or NEON (the same bytes): for 4:2:0 about half as much to scale as in RGBA, and no colour conversion before or after (every plane within 1 level of the same scale in floating point). CMYK and RGB JPEGs, and unusual subsampling, are decoded in full and then block-averaged, and resized in RGBA. The output dimensions are the same as without this option; pixel values differ slightly (by less than 2 levels on average on smooth images, and by a few levels where ringing next to black or white is clipped), so it is disabled by default. On one core, a 6000×4000 baseline JPEG (4:2:0) is shrunk to fit 400×400 and written as a JPEG in 80ms (libvips 8.18's `vipsthumbnail`, also on one core: 94ms) and to fit 1536×1536 in 100ms (150ms); a progressive one in 45ms (222ms) and 238ms (272ms); a 4:4:4 one in 139ms (153ms) and 160ms (202ms): times of the whole program, of which about 28ms is starting it on the Windows machine measured. With 2 threads the 1536 cases take 87ms, 233ms and 154ms (`vipsthumbnail` with its own threads: 166ms, 298ms and 236ms). In a running program (no start-up), a 6000×4000 JPEG of quality 90 shrunk to fit 1536×1536 and written as a JPEG of quality 80 takes 37ms on one core (45ms with `MethodLibvips`), against 49ms in sharp 0.35.5 on one thread, which shrinks on load by default. Against a resize of the fully decoded image, the result is at 46.7–50.5dB (PSNR), at most 9 levels apart. With `MethodLibvips` a JPEG is shrunk on load as sharp does it (see [Usage](usage.md)), with libjpeg's scaled decoding to the byte, the chroma of a 4:2:0 file decoded at twice the size; and a WebP is scaled while it is decoded, as libwebp does it for sharp: a 1600×1097 lossy WebP resized to 333 pixels wide in 11.8ms on one core and 8.8ms on two (`Threads(4)`), against 8.9ms in sharp 0.35.5 set to one thread (libwebp decodes on a thread of its own there; without shrinking on load: 19.4ms), a 1001×687 lossless one with alpha in 13.4ms (sharp: 12.9ms; 21.8ms). Decoding those files whole takes 10.3ms and 10.6ms here, 11ms and 10ms in libwebp 1.6's `dwebp`.

A lossy WebP without alpha that is to be reduced to a third of its size or less is decoded at twice the size of the result, scaled as libwebp scales while decoding (each plane averaged over the area of each pixel, the loop filter skipped), and the kernel does the rest: a 6000×4000 one (quality 80) shrunk to fit 400×400 in 0.26s instead of 0.36s on one core, to fit 1536×1536 in 0.34s instead of 0.39s (with `MethodLibvips`, sharp's pixels by sharp's way, 0.24s and 0.27s; decoding the file whole takes 0.25s here and in libwebp's `dwebp` on the same machine). sharp, set to one thread, takes 0.20s and 0.21s: libvips has libwebp decode on a second thread of its own (`use_threads`). With `Threads(5)` (two cores to share work out, see above) the times here are 0.20s and 0.21s with `MethodLibvips`, 0.21s to fit 1536 by default, and 0.23s each on four (before the decoding of lossy WebP was shared out, on four: 0.24s, 0.27s and 0.30s), the result 48–51dB (PSNR) from that of the full image. A lossless WebP, most of whose time goes into decoding, and one with alpha, whose colours libwebp scales without weighing them by alpha, are decoded whole.

## Benchmarks

Environment: Windows 10, Go 1.27.1. "Standard library approach" denotes whole-image decoding with `image.Decode`, resizing with `CatmullRom.Scale` from `x/image`, and standard library encoding. Peak memory is the peak physical memory of the process as recorded by the operating system, of which about 5.4MB is the start-up footprint of a Go program.

### Shrink to fit within 1536 + white background + watermark + output

| Input (pixels) | Output | Peak memory, this library (1 thread / 2 threads) | Peak memory, standard library approach | Time, 1 thread | Time, 2 threads | Time, standard library approach |
|---|---|---|---|---|---|---|
| PNG RGBA 6900×9200 | JPEG | 10.4MB / 11.2MB | 588MB | 1.63s | 1.21s | 3.10s |
| PNG RGBA 6900×9200 | PNG | 11.2MB / 12.2MB | 591MB | 1.71s | 1.27s | 3.16s |
| PNG interlaced RGB 6000×4000 | JPEG | 8.8MB / 10.9MB | 379MB | 0.56s | 0.50s | 1.02s |
| JPEG baseline 4:2:0 6000×4000 | JPEG | 9.9MB / 12.0MB | 243MB | 0.17s | 0.10s | 0.85s |
| JPEG baseline 4:4:4 6000×4000 | JPEG | 9.8MB / 11.6MB | 278MB | 0.24s | 0.15s | 0.98s |
| JPEG progressive 6000×4000 | JPEG | 11.7MB / 12.6MB | 375MB | 0.32s | 0.23s | 1.05s |
| JPEG progressive 5000×8000 | JPEG | 11.7MB / 12.3MB | 537MB | 0.82s | 0.56s | 1.51s |
| GIF 6000×4000 | JPEG | 10.2MB / 12.2MB | 230MB | 0.14s | 0.09s | 0.85s |
| GIF interlaced 6000×4000 | JPEG | 11.8MB / 12.6MB | 248MB | 0.16s | 0.15s | 0.86s |
| BMP 24-bit 6000×4000 | JPEG | 8.7MB / 10.5MB | 299MB | 0.21s | 0.18s | 0.58s |
| TIFF uncompressed 6000×4000 | JPEG | 8.4MB / 10.5MB | 535MB | 0.23s | 0.18s | 0.67s |
| TIFF LZW 6000×4000 | JPEG | 8.6MB / 10.6MB | 535MB | 0.74s | 0.57s | 1.18s |
| TIFF Deflate 6000×4000 | JPEG | 13.2MB / 14.2MB | 461MB | 0.58s | 0.40s | 1.08s |
| WebP lossy 6000×4000 | JPEG | 9.5MB / 11.9MB | 243MB | 1.07s | 0.85s | 1.42s |
| WebP lossless 6000×4000 | JPEG | 14.2MB / 15.0MB | 300MB | 1.00s | 0.80s | 1.52s |

- Total CPU time does not vary with the thread count (2 threads merely distribute the same work across two cores).
- The JPEG rows (quality 90, made from one photograph) were measured again when JPEG decoding became libjpeg-turbo's (Pillow's and sharp's pixels), whose accurate inverse DCT and interpolation of the chroma first cost about 0.09s more for the 4:2:0 file on one thread (0.29s before), and again once decoding, encoding and the resize ran with AVX2: 0.17s.
- The progressive JPEGs are decoded with their scans in step (see [Memory control](#memory-control)); before that change their coefficients (72MB at 6000×4000) went to a temporary file, and the 6000×4000 row took 0.87s at 8.8MB. Of the 1.6–1.8MB more at the peak, 0.6MB is the one row of coefficients; the rest is a garbage collection that the larger heap starts, which touches more memory the more cores there are (0.5MB more in all with `GOMAXPROCS=1`).
- With `FastShrinkOnLoad` enabled (single thread): JPEG baseline 4:2:0 0.07s, 4:4:4 0.14s, progressive 0.22s.

### Against another Go library

The same task in disintegration/imaging 1.6.2 (decoding with `image/jpeg`, resizing with its parallel Lanczos, encoding with `image/jpeg`) and here: a 6000×4000 baseline JPEG (4:2:0) resized to 1600 wide (Lanczos 3) and written as a JPEG of quality 85; best of three, the peak of the Go heap sampled every 2ms, `GOMAXPROCS` limiting both to the same cores (imaging uses them all for its resize).

| Cores | imaging | goimgtools | goimgtools, `FastShrinkOnLoad` |
|---|---|---|---|
| 1 | 661ms, 79MB | 167ms, 9MB (`Threads(1)`) | 65ms, 9MB |
| 4 | 345ms, 79MB | 104ms, 12MB (`Threads(4)`) | 61ms, 9MB |

The JPEG written here is 5% smaller (120,406 bytes against 127,034, libjpeg-turbo's coding against `image/jpeg`'s), its pixels decoded as libjpeg-turbo decodes them.

### Other operations (input: JPEG baseline 4:2:0 6000×4000 in all cases; single thread)

| Operation | Output file | Peak memory | Time |
|---|---|---|---|
| Without shrinking, to JPEG (quality 85) | 5.9MB | 7.9MB | 0.15s |
| Without shrinking, to PNG | 42.2MB | 11.6MB | 1.62s (sharp, one thread, with adaptive filtering: 42.6MB in 1.39s; at its default, without: 66.2MB in 1.12s) |
| Without shrinking, to lossy WebP (quality 75) | 3.2MB | 20.8MB | 4.41s (1.25s with 8 threads; effort 1: 1.47s, 0.40s) |
| Without shrinking, to lossless WebP | 28.9MB | 26.1MB | 4.63s (3.88s with 8 threads) |
| Without shrinking, to BMP | 72MB | 8.1MB | 0.13s |
| Without shrinking, to TIFF (uncompressed) | 96MB | 7.5MB | 0.09s |
| Rotate 90°, without shrinking | 6.0MB | 13.9MB | 0.31s |
| Rotate 90°, then shrink to 1536 | 0.1MB | 14.2MB | 0.19s |
| Shrink to 1536, GIF output | 0.4MB | 14.2MB | 0.43s |
| Without shrinking, outlined text watermark tiled across the whole image | 6.3MB | 11.5MB | 0.20s |
| Shrink to 1536, lossy WebP output | 10KB | 11.4MB | 0.28s |

- Measured with `cmd/memprobe` (which flattens over white, as the benchmark above does) on 21 November 2026; the probe's start-up footprint is now about 6.1MB.
- When rotating by 90° without shrinking, the 96MB of pixel data is held in a temporary file; with all data in memory (a limit of 768MB or more), the time is 0.28s and the peak 102MB. (Before each round of rows went to the store as one block: 0.64s.)
- Lossless WebP from PNG RGB 6000×4000 (42.2MB; the same image as in the table above): output 28.9MB, peak 25.9MB, 4.99s (4.50s with 8 threads). (Before the encoder stopped trying the same Huffman grouping once for every block size it asks for, when a large image makes them all the same: 32.6MB and 8.87s. Version 0.1.0, without the optimal parse: 22.2MB and 4.15s, for a file 0.004% larger: the noise of a photograph from a JPEG leaves little for the parse to find.)
- Lossy WebP with transparency (the 6000×4000 photograph in a soft round window, quality 75): 2.85MB, peak 26.6MB, 12.9s (6.0s with 8 threads; before the alpha plane was coded by the lossless encoder, 4.29MB, 20.9MB, 10.9s and 3.1s). The alpha plane is coded on one core, in up to 8MB of its own; at 24 megapixels, it is coded without the lossless encoder's optimal parse below effort 7.
- Before encoding, lossy WebP must convert the whole image to YUV, store it, and measure every macroblock (as required by segment-based quantization; see [Compatibility and comparisons](compatibility.md#webp-encoders)). Images of no more than 256KB after conversion are kept in memory; larger ones are written directly to a temporary file and read back only row by row. Measurements show no effect on the time.
- GIF output without shrinking takes 2.0s (peak 11.8MB). With a fixed palette (`FixedPalette`), it takes 4.3s: the colour search order on that path has been optimized; the result is byte-for-byte identical to the standard library and is produced more than 3 times as fast as by comparing one colour at a time.

### Image adjustment operations (JPEG baseline 4:2:0 6000×4000, without shrinking, JPEG output)

Times for this library include decoding and encoding. The Pillow column gives the time Pillow 12.1.1 takes on the same machine for this step alone (image already decoded in memory, single thread), for comparing the speed of the algorithms themselves.

| Operation | 1 thread | 3 threads | 6 threads | Peak memory (1 / 6 threads) | Pillow, this step only |
|---|---|---|---|---|---|
| No operation | 0.16s | 0.09s | 0.08s | 6.9 / 12.7MB | — |
| `Grayscale()` | 0.20s | 0.09s | 0.08s | 6.9 / 12.6MB | 0.02s |
| `Modulate(1.1, 1.3, 20)` | 0.41s | 0.27s | 0.24s | 7.3 / 14.2MB | — |
| `Blur(2)` | 0.26s | 0.13s | 0.10s | 12.1 / 25.0MB | 0.38s |
| `Blur(40)` | 0.26s | 0.13s | 0.13s | 18.1 / 25.5MB | 0.38s |
| `Sharpen()` (light) | 0.34s | 0.18s | 0.10s | 9.6 / 21.3MB | 0.24s (Pillow's `SHARPEN`, also 3×3) |
| `Sharpen(SharpenOptions{Sigma: 2})` | 0.34s | 0.16s | 0.13s | 12.4 / 26.2MB | 0.48s |
| `Trim(10)` | 0.24s | 0.17s | 0.15s | 7.6 / 13.3MB | — |
| `Extend(50, 50, 50, 50, white)` | 0.17s | 0.09s | 0.08s | 7.9 / 15.4MB | 0.02s |
| `Rotate(30, RotateOptions{Background: white})`, output 7197×6465 | 0.66s | 0.53s | 0.38s | 12.6 / 19.7MB | 0.57s |

- Single-threaded, after subtracting the 0.16s of the no-operation case: blur adds 0.10s (Pillow 0.38s) and the unsharp mask 0.18s (0.48s), their box passes running with AVX2 or NEON on Pillow's integer arithmetic (along the rows, with AVX2, from running sums of the row), every byte the same; trimming adds 0.08s, its 3×3 median taken 32 bytes at a time; rotation by 30° adds 0.50s, of which about 0.08s is spent encoding the additional 22 MP (estimated from the encoding time per megapixel), so the rotation itself takes about 0.42s, against Pillow's 0.57s; `Modulate(1.1, 1.3, 20)` adds 0.25s, where sharp's `modulate` with the same factors adds 0.89s (1.06s in all, one thread), every pixel within 1 of sharp's.
- With the memory limit lowered to 16MB (staged rows written to a temporary file), blur, sharpening, rotation and trimming produce results pixel-for-pixel identical to those without a memory limit (compared as PNG output); time increases by 0–15%, and peak memory remains within 13MB in all cases.

Evaluated but not implemented:

- Depending on `klauspost/compress` for PNG compression: compression in the Go 1.27 standard library is the same implementation (it writes the same bytes); copied into `internal/gflate` with a reset that takes a new dictionary and its word loads, it runs within 3% of it, and spares those who use this package a module of 38MB to download (see [PNG block compression](#cpu-and-threads)).

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
