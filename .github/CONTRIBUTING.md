# Contributing

English | [中文](#中文)

Questions, bug reports and pull requests are welcome. Issues may be written in English or Chinese.

## Building and testing

Requires Go 1.26 or later; cgo is not used.

```bash
go vet ./...
```

```bash
go test ./...
```

- The full test suite takes 3–5 minutes. The cross-check of the WebP encoders against libwebp requires Python with Pillow and is skipped if it is not available.
- Race detection: `go test -race -timeout 45m ./...`.
- For fuzz testing and the platforms covered by CI, see [docs/testing.md](../docs/testing.md).

## Code requirements

- **Memory.** Images flow through the chain as strips of a few rows. Every buffer that grows with the image is reserved from the memory budget (`Env().Budget.Reserve`) before it is allocated, and data that must wait for the whole image is placed in `pipeline.Spill` or `pipeline.Scratch`. A change that increases peak memory must state so and include measurements.
- **Identical output in every environment.** Output must not depend on the number of threads, the memory limit, how the source is divided into strips, or the processor. Tests compare these cases ([tests/threads_test.go](../tests/threads_test.go), [tests/platform_test.go](../tests/platform_test.go)).
- **Errors, not panics.** Invalid input and invalid arguments return errors; a panic during a run is converted to a `*PanicError`. Memory budget and temporary files are released on every path, including errors and panics.
- **Tests.** Behaviour tests reside in `tests/` and use only the public API; tests of internal packages reside alongside their code.
- **Performance changes must include measurements**: time and peak memory before and after, measured with `cmd/memprobe` (see [docs/performance.md](../docs/performance.md#benchmarks)).
- **Code from other projects** is accepted only under licences compatible with MIT (no GPL). Record what was ported, and from where, in [licenses/THIRD-PARTY.md](../licenses/THIRD-PARTY.md), and place the licence text in `licenses/`.
- Code is formatted with `gofmt` and passes `go vet` without warnings.

## Code layout

The Go files at the top level constitute the library itself (a single package: one `import` provides all of it). Everything else resides in subdirectories:

```
goimgtools/
├── goimgtools.go    package doc, the Image type, inputs, memory / thread / limit settings
├── operations.go    operations and their options (resize, rotate, crop, flatten, composite)
├── adjust.go        adjustments: grayscale, modulate, blur, sharpen, extend, trim, rotate by any angle
├── output.go        output formats and their options, MaxFileSize, writing
├── run.go           a single run: opening the input, building the chain, Metadata, OutputSize
├── chain.go         the operation list and its execution order
├── formats.go       the format table, covering built-in and registered formats; RegisterFormat
├── extend.go        extension points: Pipe, Pixels, Apply, FromImage, FromSource, ToFormat, Stream, ToImage
├── steps.go         operations by name (RegisterOperation, Do), hooks (Around), ParseColor
├── input.go         opening and reading inputs, error classification
├── text.go          text input
├── animate.go       animations: GIF to GIF or WebP, frame counts
├── example_test.go  runnable examples for the documentation
│
├── pipeline/        public package: the pixel stream (Source), memory budget, working storage, components for extensions
├── inspect/         public package: verify, compare, colour statistics, perceptual hash, EXIF
├── cli/             public package: the command line (the goimgtools program is cli.Main)
├── internal/        decoders and encoders for each format, resizing, operations, text, colour reduction
├── cmd/             the goimgtools program; memprobe, which measures memory and time
├── tests/           behaviour tests, using the public API only
├── licenses/        third-party licences and code provenance
└── docs/            documentation (English; Chinese in docs/zh-CN)
```

| Path | Contents |
|---|---|
| `internal/pngstream`, `jpegstream`, `gifstream`, `bmpstream`, `tiffstream` | streaming decoders and encoders |
| `internal/webpstream` | streaming WebP decoder |
| `internal/vp8enc`, `internal/vp8lenc` | lossy and lossless WebP encoders |
| `internal/webpanim` | animated WebP writer |
| `internal/resize` | streaming resize, block-average shrinking, kernels |
| `internal/ops` | crop, flatten, composite, rotate and flip, extend, trim, rotate by any angle, grayscale, modulate, blur, sharpen |
| `internal/textsrc` | text layout and rendering, font lookup |
| `internal/quant` | colour reduction: image-adaptive palette, dithering |
| `internal/iccprof` | colour profiles: parsing and conversion to sRGB |

---

## 中文

欢迎提出问题、报告缺陷与提交 Pull Request；可使用中文或英文撰写。

- **编译与测试**：须 Go 1.26 及以上版本，不依赖 cgo。`go vet ./...`、`go test ./...`（全量测试耗时 3–5 分钟；与 libwebp 的交叉验证需要安装了 Pillow 的 Python，缺失时跳过）；竞态检测：`go test -race -timeout 45m ./...`；模糊测试及自动测试所覆盖的平台见 [docs/zh-CN/testing.md](../docs/zh-CN/testing.md)。
- **内存**：随图像尺寸增长的缓冲在分配前均须向内存预算申请（`Env().Budget.Reserve`），需等待全图的数据暂存于 `pipeline.Spill` 或 `pipeline.Scratch`。导致峰值内存增加的改动须加以说明并附实测数据。
- **输出处处一致**：输出不得随线程数、内存上限、条带划分方式或处理器而变化，测试会对此进行比较。
- **返回错误而非 panic**：执行期间的 panic 转为 `*PanicError`；无论成功、出错还是 panic，内存预算与临时文件均须归还。
- **测试**：库的行为测试置于 `tests/`，仅使用公开接口；内部包的测试与其代码置于同一目录。
- **性能改动须附实测**：改动前后的耗时与峰值内存，使用 `cmd/memprobe` 测量（见 [docs/zh-CN/performance.md](../docs/zh-CN/performance.md#实测)）。
- **引用其他项目的代码**：仅限与 MIT 兼容的许可（不接受 GPL），并在 [licenses/THIRD-PARTY.md](../licenses/THIRD-PARTY.md) 中记录移植的内容及其来源，许可原文置于 `licenses/`。
- 代码须经 `gofmt` 格式化，`go vet` 无报错。目录结构见上文的 Code layout。
