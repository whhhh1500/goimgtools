English | [中文](zh-CN/compression.md)

# Compression

| Goal | How | Measured on a real 8-megapixel photo |
|---|---|---|
| Smaller JPEG, same image quality | `JPEG(q, JPEGOptions{Optimize: true})`: computes coding tables for this particular image | Quality 85: 983KB → 965KB (1.9% smaller), 0.20s, 7.3MB |
| JPEG a little smaller still, same image quality | `JPEGOptions{Progressive: true}`: progressive (includes optimised coding tables) | 983KB → 905KB (7.9% smaller), 0.34s, 6.6MB |
| Smaller JPEG at the same perceived quality (changes pixels) | `JPEGOptions{Trellis: true}`: trellis quantization (mozjpeg's approach); can be combined with the two options above | 7 real photos: 2.7–10.6% smaller (6.9% on average) at equal SSIM, 6% smaller on average at equal PSNR; 0.4s more on 33 megapixels (single thread) |
| Smaller PNG by default (lossless) | The default compression level is 7 (the standard library uses 6): Go's level 6 is weak at finding repeats; level 7 is about 5 times smaller on gradients, skies, icons and similar images, and 4–25% smaller on photos; on large 24-megapixel images it is 17–70% slower (single thread), which multithreading wins back by compressing in blocks. For the standard library's speed, write `PNG(PNGOptions{CompressionLevel: 6})` / `-png-level 6` | Gradient 1200×800: 21KB → 4.7KB; 24-megapixel RGB PNG: 70.7MB → 53.0MB |
| Much smaller PNG (lossy) | `PNG(PNGOptions{Colors: 256})`: reduces to at most 256 colours, chosen from the image, with dithering | 6474KB → 2653KB (59% smaller), 39.1dB against the original, 0.90s, 12.6MB |
| PNG colour reduction without dithering | Also add `NoDither: true`: smaller file, banding in gradients | 2142KB, 40.7dB |
| Compress to within a given size | `MaxFileSize(n)`: automatically finds the highest quality that does not exceed n | Limit 300KB: result 297KB, 0.66s, 12MB |
| Change format | `WebP(WebPOptions{Quality: 80})` | PNG 6474KB → WebP 440KB |
| Smaller animated GIF | `Animated()` by default: all frames share one palette, from the second frame on only the changed rectangle is stored, and unchanged pixels are written as transparent; with no more than 256 colours every pixel is unchanged | An 8-frame animated GIF that stores every frame whole (160×120, only one small square moving): 35KB → 5.6KB, picture unchanged (`FixedPalette`, writing every frame whole, gives 55KB) |
| Animated GIF smaller still (lossy) | Also add something like `GIF(GIFOptions{Colors: 128, NoDither: true})`: use fewer colours | An already optimised photographic animated GIF (400×533, 8 frames, 557KB): without colour reduction 557KB → 556KB (every pixel unchanged); 128 colours 501KB (10% smaller, 39.6dB per frame); 64 colours 404KB (27% smaller, 34.7dB); `FixedPalette` (fixed palette with dithering, every frame whole) gives 814KB |

- **The two JPEG options** change no pixels (verified identical pixel for pixel by decoding with libjpeg), and the file sizes are within 0.04% of libjpeg-turbo's options of the same name; the algorithms are ported from libjpeg. Without the options, the output is still byte-for-byte identical to Go's standard library.
- **PNG colour reduction**, done the same way, gives smaller files and higher quality than Pillow's median cut (the photo above: Pillow with dithering 2877KB / 34.5dB, without dithering 2309KB / 37.6dB). Transparency is kept: fully transparent stays fully transparent, opaque stays opaque, and soft edges keep their intermediate transparency. An image with no more than `Colors` colours is kept as it is, without loss.
- **Compared with pngquant 2.17** (256 colours, with dithering; quality is the PSNR against the original):

  | Image | Distinct colours | goimgtools | pngquant |
  |---|---|---|---|
  | Sky gradient 1920×1080 | 259 | 77.4dB, 11.4KB | 63.2dB, 4.7KB |
  | Radial gradient 1000×1000 | 302 | 67.4dB, 90.5KB | 62.6dB, 67.7KB |
  | Sky + colour blocks 1920×1080 | 261 | 77.2dB, 14.0KB | 63.3dB, 6.9KB |
  | Two-way gradient 1200×800 | 65536 | 33.8dB, 242KB | 34.0dB, 221KB |
  | Real photo 960×540 | 87364 | 40.0dB, 185KB | 41.1dB, 196KB |

  For images with no more than 16384 colours (mostly gradients, icons and colour blocks), the palette is chosen from the exact colours, and quality is 5–14dB higher than pngquant; on photos with more colours the two are about equal. Where the files are larger than pngquant's, the difference comes from compression (it uses zlib level 9); with `-png-level 9` the sky gradient is 6.3KB.
- **Smooth gradients that change in both directions** (65,000 colours) are still larger than full colour after colour reduction with dithering, and the same is true of pngquant; without dithering (`NoDither`) such an image is only 8.6KB, 36.6dB. The command line's `-skip-larger` keeps the original file when the result is not smaller than it (see [Command line](cli.md)).
- **`MaxFileSize`** encodes about 6 times, taking 4–6 times as long as without a size limit; when the original quality is already small enough, it encodes only once. When the image still does not fit at quality 1, it returns `ErrFileSize`, and the dimensions need to be reduced first. With `Progressive`, a higher quality fits in the same size.
- All of these must see the whole image first; intermediate data goes into working storage (a temporary file beyond the memory share). An animated GIF must have every frame computed first: all frames go into working storage, the input is read twice (the frames are counted first), and choosing the palette from all frames needs about 4–5MB of memory; with `GIFOptions{FixedPalette: true}` it writes while reading and stores no frames.
- **Dithering in animated GIFs** starts afresh in each frame, so unchanged areas are dithered the same way in every frame and do not flicker; the decision on which part changed (so that only it is stored) is made on the original picture, unaffected by dithering.
