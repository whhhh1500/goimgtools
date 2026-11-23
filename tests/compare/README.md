# Comparisons with other tools

The scripts behind [docs/comparison.md](../../docs/comparison.md). They need the other tools installed: libvips (its `vips` and `vipsthumbnail`), Node with sharp, Python with Pillow and NumPy, libwebp's `cwebp` and `dwebp`, gifsicle, jpegli's `cjpegli`, and `ssimulacra2` and `butteraugli_main` from libjxl.

| Script | What it measures |
|---|---|
| `runpeak` (Go) | Runs a command a few times: its best wall time, its largest peak memory (working set), its CPU time, and on Windows its largest peak of private (committed) memory and the most bytes it wrote, which less the output's size is what went to temporary files |
| `bench.ps1` | Speed and memory: every tool on the same task and input, through `runpeak` (`-threads 4` for the table of four threads, `-only` a pattern of row groups) |
| `bench_more.ps1` | The same for this package's other features, on the same photograph: every format read and written, every named operation, the encoders' options, metadata, colour, watermarks, text, animation, tiles, PDF, batch and the inspection commands (its inputs are made by `mkmore.py` and the package itself the first time) |
| `bench_sharp.js`, `bench_pil.py` | One task with sharp, with Pillow |
| `more_sharp.js`, `more_pil.py`, `mkmore.py` | The tasks of `bench_more.ps1` with sharp and with Pillow, and the inputs it needs that Pillow writes |
| `mktable.py lines en\|zh out.md` | The tables of the other features from what `bench_more.ps1` printed: each cell the time, the peak working set / peak private memory and what went to temporary files; the verdict weighs memory first, then the time when it is more than twice the fastest |
| `quality_more.py images...` | The lossy outputs `quality.py` leaves out (PNG and GIF of 256 colours, lossy GIF, near-lossless WebP, JPEG in TIFF, the JPEG options), every tool at the same setting: total size, mean SSIMULACRA2 and butteraugli, each output decoded by libvips |
| `quality.py jpeg\|webp images...` | Lossy encoders: the file size each needs for the same SSIMULACRA2 and the same butteraugli |
| `resizeq.py width images...` | Downscaling: each tool's Lanczos 3 against the exact convolution |
| `cm.py goimgtools outdir profiles...` | Colour management: conversion to sRGB against LittleCMS |
| `librsvg.ps1 outdir`, `passcount.py tests outdir` | librsvg on resvg's test suite, and how many tests a directory of renderings passes |

The inputs of `bench.ps1` are a photograph of 6000×4000 pixels as a baseline JPEG, a PNG, a lossy WebP, a GIF and an uncompressed TIFF, an animated GIF of 18 photographs of 768×512, and a small logo with transparency; the variables at its top say where they and the tools are.