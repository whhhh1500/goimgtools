# Contributing

English | [中文](#中文)

Questions, bug reports and pull requests are welcome. Issues may be written in English or Chinese.

## Building and testing

Go 1.27 or newer; no cgo.

```bash
go vet ./...
```

```bash
go test ./...
```

- The full suite takes 3–5 minutes. The cross-check of the WebP encoders against libwebp needs Python with Pillow and is skipped without it.
- The race detector: `go test -race -timeout 45m ./...`.
- Fuzz tests and the platforms CI runs on: [docs/testing.md](../docs/testing.md).

## What the code keeps to

- **Memory.** Images flow as strips of a few rows. Every buffer that grows with the image is reserved from the memory budget (`Env().Budget.Reserve`) before it is allocated, and data that has to wait for the whole image goes to `pipeline.Spill` or `pipeline.Scratch`. A change that raises peak memory says so, with measurements.
- **The same output everywhere.** Output must not depend on the number of threads, the memory limit, how the source is cut into strips, or the processor. Tests compare these ([tests/threads_test.go](../tests/threads_test.go), [tests/platform_test.go](../tests/platform_test.go)).
- **Errors, not panics.** Bad input and bad arguments return errors; a panic during a run becomes a `*PanicError`. Memory budget and temporary files are given back on every path, errors and panics included.
- **Tests.** Behaviour tests go in `tests/` and use only the public API; tests of internal packages sit next to their code.
- **Performance changes come with numbers**: time and peak memory before and after, measured with `cmd/memprobe` (see [docs/performance.md](../docs/performance.md#benchmarks)).
- **Code from other projects** only under licences compatible with MIT (no GPL). What was ported, and from where, goes in [licenses/THIRD-PARTY.md](../licenses/THIRD-PARTY.md), with the licence text in `licenses/`.
- `gofmt`, and a clean `go vet`.

## Code layout

The Go files at the top are the library itself (one package: one `import` gives all of it). Everything else is in subdirectories:

```
goimgtools/
├── goimgtools.go    package doc, the Image type, inputs, memory / thread / limit settings
├── operations.go    operations and their options (resize, rotate, crop, flatten, composite)
├── adjust.go        adjustments: grayscale, modulate, blur, sharpen, extend, trim, rotate by any angle
├── output.go        output formats and their options, MaxFileSize, writing
├── run.go           one run: opening the input, building the chain, Metadata, OutputSize
├── chain.go         the list of operations and the order they run in
├── formats.go       the format table, built-in and registered formats alike; RegisterFormat
├── extend.go        extension points: Pipe, Pixels, Apply, FromImage, FromSource, ToFormat, Stream, ToImage
├── steps.go         operations by name (RegisterOperation, Do), hooks (Around), ParseColor
├── input.go         opening and reading inputs, error classification
├── text.go          text input
├── animate.go       animated GIF
├── example_test.go  runnable examples for the documentation
│
├── pipeline/        public package: the pixel stream (Source), memory budget, working storage, parts for extensions
├── inspect/         public package: verify, compare, colour statistics, perceptual hash, EXIF
├── cli/             public package: the command line (the goimgtools program is cli.Main)
├── internal/        decoders and encoders of each format, resizing, operations, text, colour reduction
├── cmd/             the goimgtools program, and memprobe, which measures memory and time
├── tests/           behaviour tests, through the public API only
├── licenses/        third-party licences and where code came from
└── docs/            documentation (English; Chinese in docs/zh-CN)
```

| Path | Contents |
|---|---|
| `internal/pngstream`, `jpegstream`, `gifstream`, `bmpstream`, `tiffstream` | streaming decoders and encoders |
| `internal/webpstream` | streaming WebP decoder |
| `internal/vp8enc`, `internal/vp8lenc` | lossy and lossless WebP encoders |
| `internal/resize` | streaming resize, block-average shrinking, kernels |
| `internal/ops` | crop, flatten, composite, rotate and flip, extend, trim, rotate by any angle, grayscale, modulate, blur, sharpen |
| `internal/textsrc` | text layout and rendering, font lookup |
| `internal/quant` | colour reduction: palette chosen for the image, dithering |
| `internal/iccprof` | colour profiles: parsing and conversion to sRGB |

---

## 中文

欢迎提问题、报 bug、提交改动；用中文或英文写都可以。

- **编译和测试**：Go 1.27 以上，不依赖 cgo。`go vet ./...`、`go test ./...`（全量 3–5 分钟；与 libwebp 的交叉验证需要装有 Pillow 的 Python，没有时跳过）；竞态检测 `go test -race -timeout 45m ./...`；模糊测试和自动测试的平台见 [docs/zh-CN/testing.md](../docs/zh-CN/testing.md)。
- **内存**：随图片变大的缓冲分配前都先向内存预算申请（`Env().Budget.Reserve`），要等整张图的数据放进 `pipeline.Spill` 或 `pipeline.Scratch`。会让峰值内存变大的改动要说明并附实测。
- **结果处处相同**：输出不能随线程数、内存上限、分条方式、处理器而变，测试会比较这些。
- **出错返回错误，不 panic**：执行中的 panic 转成 `*PanicError`；无论成功、出错还是 panic，内存预算和临时文件都要归还。
- **测试**：库的行为测试放 `tests/`，只用公开接口；内部包的测试挨着代码。
- **性能改动附实测**：改动前后的耗时和峰值内存，用 `cmd/memprobe` 测（见 [docs/zh-CN/performance.md](../docs/zh-CN/performance.md#实测)）。
- **用别的项目的代码**：只用与 MIT 兼容的许可（不用 GPL），在 [licenses/THIRD-PARTY.md](../licenses/THIRD-PARTY.md) 记下移植了什么、来自哪里，许可原文放进 `licenses/`。
- `gofmt` 格式化，`go vet` 无报错。目录结构见上面的 Code layout。
