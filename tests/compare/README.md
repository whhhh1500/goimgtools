# Comparisons with other tools

The scripts behind [docs/comparison.md](../../docs/comparison.md). They need the other tools installed: libvips (its `vips` and `vipsthumbnail`), Node with sharp, Python with Pillow and NumPy, libwebp's `cwebp` and `dwebp`, gifsicle, jpegli's `cjpegli`, and `ssimulacra2` and `butteraugli_main` from libjxl.

| Script | What it measures |
|---|---|
| `runpeak` (Go) | Runs a command a few times: its best wall time, its largest peak memory, its CPU time |
| `bench.ps1` | Speed and memory: every tool on the same task and input, through `runpeak` (`-threads 4` for the table of four threads, `-only` a pattern of row groups) |
| `bench_sharp.js`, `bench_pil.py` | One task with sharp, with Pillow |
| `quality.py jpeg\|webp images...` | Lossy encoders: the file size each needs for the same SSIMULACRA2 and the same butteraugli |
| `resizeq.py width images...` | Downscaling: each tool's Lanczos 3 against the exact convolution |
| `cm.py goimgtools outdir profiles...` | Colour management: conversion to sRGB against LittleCMS |
| `librsvg.ps1 outdir`, `passcount.py tests outdir` | librsvg on resvg's test suite, and how many tests a directory of renderings passes |

The inputs of `bench.ps1` are a photograph of 6000×4000 pixels as a baseline JPEG, a PNG, a lossy WebP, a GIF and an uncompressed TIFF, an animated GIF of 18 photographs of 768×512, and a small logo with transparency; the variables at its top say where they and the tools are.