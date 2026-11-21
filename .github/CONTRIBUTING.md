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
- **Identical output in every environment.** Output must not depend on the number of threads, the memory limit, how the source is divided into strips, or the processor. Tests compare these cases ([tests/threads_test.go](../tests/threads_test.go), [tests/platform_test.go](../tests/platform_test.go)). The one exception is the lossless WebP encoder: it tries fewer transform variants when little memory is left, so under a low memory limit its file can differ (and be larger); its pixels never do. The same holds for the alpha chunk of a lossy WebP, which it codes.
- **Errors, not panics.** Invalid input and invalid arguments return errors; a panic during a run is converted to a `*PanicError`. Memory budget and temporary files are released on every path, including errors and panics.
- **The public API only grows.** Nothing exported by the last release is removed or changed incompatibly (a renamed function, a changed signature, a field removed): new behaviour comes as new names, options or fields whose zero value keeps the old behaviour. The `api` job of the tests compares the exported API of the whole module with the last release's (`apidiff -m -incompatible`, from golang.org/x/exp) and fails on any incompatible change; `tests/api_lock_test.go` also holds the basic API (opening, the main operations, the encoders, the outputs and `inspect`) to its signatures, the fields of its options and results to their names and types, and the values of its constants, so that changing one fails `go test` on any machine. The command line is API as well: no flag or command of a release is removed or renamed (`cli/lock_test.go` lists those of 0.2.0; add each release's new ones to it). Interfaces that code of your own implements (`Operation`, `Input`, `pipeline.Source`, `pipeline.OpaqueSource`, `pipeline.MetaSource`) never gain a method, since every implementation outside would stop compiling: a new capability comes as a new small interface a stage may implement, found by a type assertion, as `OpaqueSource` and `MetaSource` are. Name new API with care: once released, it stays. Behaviour counts too: a released function does not start returning errors it did not return, return a different concrete type, or change what a field means, unless a bug forces it; such a change is listed under "Upgrading" in the changelog.
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
- **输出处处一致**：输出不得随线程数、内存上限、条带划分方式或处理器而变化，测试会对此进行比较。唯一的例外是无损 WebP 编码器：剩余内存较少时少试几种变换，因此内存上限较低时文件可能不同（更大），像素始终一致；带透明的有损 WebP 的透明通道块由它编码，亦然。
- **返回错误而非 panic**：执行期间的 panic 转为 `*PanicError`；无论成功、出错还是 panic，内存预算与临时文件均须归还。
- **公开接口只增不改**：上一个发布版本导出的内容不得删除或作不兼容的修改（改名、改签名、删字段）；新行为以新名称、新选项或零值保持原行为的新字段的形式加入。测试中的 `api` 作业用 golang.org/x/exp 的 `apidiff -m -incompatible` 比较整个模块与上一发布版本的导出接口，有任何不兼容改动即失败；`tests/api_lock_test.go` 还把基础接口（打开、主要操作、各编码器、各输出方式与 `inspect`）的签名、其选项与结果结构体各字段的名称和类型、各常量的值逐一写死，改动其中任何一个，在任何机器上 `go test` 都会编译失败。命令行同样是接口：已发布的参数与子命令不得删除或改名（`cli/lock_test.go` 列出了 0.2.0 的全部参数与子命令，每次发布须把新增的补进去）。由使用者自行实现的接口（`Operation`、`Input`、`pipeline.Source`、`pipeline.OpaqueSource`、`pipeline.MetaSource`）不再增加方法，否则外部的每个实现都会编译失败：新能力以一个新的小接口提供，由阶段选择实现，用类型断言发现，如 `OpaqueSource`、`MetaSource`。新接口须仔细斟酌命名：一经发布便要一直保留。行为同样算数：已发布的函数不得开始返回原先不返回的错误、改返回别的具体类型或改变字段的含义，除非是修正缺陷；此类改动须列入变更记录的「升级」一节。
- **测试**：库的行为测试置于 `tests/`，仅使用公开接口；内部包的测试与其代码置于同一目录。
- **性能改动须附实测**：改动前后的耗时与峰值内存，使用 `cmd/memprobe` 测量（见 [docs/zh-CN/performance.md](../docs/zh-CN/performance.md#实测)）。
- **引用其他项目的代码**：仅限与 MIT 兼容的许可（不接受 GPL），并在 [licenses/THIRD-PARTY.md](../licenses/THIRD-PARTY.md) 中记录移植的内容及其来源，许可原文置于 `licenses/`。
- 代码须经 `gofmt` 格式化，`go vet` 无报错。目录结构见上文的 Code layout。
