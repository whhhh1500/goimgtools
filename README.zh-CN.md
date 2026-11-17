# goimgtools

| 缩放至 1536 以内 + 白底 + 水印（单线程） | goimgtools | 标准库（整图解码） |
|---|---|---|
| JPEG 6000×4000（2400 万像素）进程峰值内存 | **8.3MB** | 242MB |
| PNG 6900×9200（6300 万像素，含透明通道）进程峰值内存 | **10.4MB** | 588MB |
| JPEG 6000×4000 耗时 | **0.38s** | 0.87s |
| PNG 6900×9200 耗时 | **1.63s** | 3.10s |

goimgtools 是纯 Go 实现的流式图像处理库与命令行工具。图像以若干行组成的条带在处理链中逐段流动，不整体解码到内存，内存占用与图像尺寸基本无关，处理速度也优于整图解码。

注：峰值内存包含 Go 运行时约 5.4MB 的基础占用；测试环境与完整数据见[性能与基准测试](docs/zh-CN/performance.md)。

[![Go Reference](https://pkg.go.dev/badge/github.com/whhhh1500/goimgtools.svg)](https://pkg.go.dev/github.com/whhhh1500/goimgtools)
[![test](https://github.com/whhhh1500/goimgtools/actions/workflows/test.yml/badge.svg)](https://github.com/whhhh1500/goimgtools/actions/workflows/test.yml)

[English](README.md) | 中文

## 特性

- **内存有界**：默认上限 64MB，可通过 `MemoryLimit` 配置。依赖全图的步骤（旋转、减色、从流读取的渐进式 JPEG 等）在暂存数据超出配额时转存临时文件；所有随图像尺寸增长的缓冲均计入上限，超限时返回 `ErrMemoryLimit`。适用于内存受限的服务器、容器以及高并发图像服务。
- **图像操作**：缩放（6 种插值核、5 种适配模式）、裁剪、任意角度旋转、翻转、去边与扩边、铺底色、灰度化、色彩调整、反色、线性色阶、伽马、二值化、着色、明度拉伸、高斯模糊、锐化（含反锐化掩模）、中值滤波、自定义卷积核；图片与文字水印（九宫格定位、按比例缩放、旋转、平铺、不透明度）；文字渲染（逐字字体回退、描边、阴影）。
- **格式**：JPEG、PNG、WebP（有损、无损）、GIF、BMP、TIFF 的读写；GIF、WebP 与 PNG 动画（APNG）可输出为 GIF 或 WebP 动画；默认保留 ICC 色彩配置，可转换至 sRGB；EXIF 可选保留。
- **压缩**：JPEG 霍夫曼表优化、按 mozjpeg 方法搜索扫描方案的渐进式编码、网格量化，以及 mozjpeg 或 jpegli 的量化表（用 jpegli 的表时，14 张图上同等 SSIMULACRA2 下比 mozjpeg 默认设置小 15%）；PNG 自适应调色板减色，以及无损 PNG 8–9 级：每行滤波逐一试过（oxipng 的 Brute），并用 libdeflate「接近最优解析」的移植压缩、像 zopfli 那样按代价分块（17 张图上比 oxipng `-o max` 小 0.9%，耗时约为其五分之一，与 ECT `-9` 相差 0.3% 以内）；有损 WebP 空间噪声整形、sharp YUV、网格量化、按测量选取环路滤波强度、色度误差扩散（14 张图上，同等 SSIMULACRA2 下比 libwebp 默认设置小 6.6%，比其最好设置小 1.3%），无损 WebP 按穿过像素的最便宜路径选匹配（17 张图上比 libwebp 最慢的设置小 5.3%，耗时约为其七分之一）；按目标文件大小自动选择质量；GIF 与 WebP 动画帧间差分存储、GIF 有损 LZW（照片类 GIF 动画转有损 WebP：同等 PSNR 下比 libwebp 动画编码器小 33%）。
- **并行**：`Threads(n)` 启用多核处理，输出与单线程逐字节一致。
- **健壮性**：运行期 panic 转为错误返回；输入与输出像素数设有上限；支持 `context` 取消；持续进行模糊测试。
- **可扩展**：支持自定义操作、输入源、文件格式与插值滤镜；按名称注册操作；通过钩子改写内置行为；可构建包含自定义操作的命令行程序。
- **纯 Go**：不依赖 cgo；依赖仅有 `golang.org/x/image`（及字体解析所需的 `x/text`、`x/sys`）与 `github.com/klauspost/compress`（用于 zlib 解压，以及 Go 1.27 及以上版本中大 PNG 的 deflate 压缩，字节与标准库相同）。

## 安装

```bash
go get github.com/whhhh1500/goimgtools
```

命令行工具：

```bash
go install github.com/whhhh1500/goimgtools/cmd/goimgtools@latest
```

需要 Go 1.26 及以上版本。Go 1.27 重写了标准库的 `compress/flate`，因此两者生成的 PNG 文件字节不同（像素相同）；本文档中的实测数据均使用 Go 1.27。

## 快速开始

API 采用与 [sharp](https://sharp.pixelplumbing.com/) 类似的链式调用：打开输入、追加操作、选择输出格式、写出。操作按调用顺序执行，错误由最终的写出方法统一返回。

```go
err := goimgtools.Open("in.jpg").
    Rotate().                              // 按 EXIF 方向校正
    Resize(1536, 0).                       // 宽 1536，高按比例
    Flatten(color.White).                  // 透明区域铺白底
    Composite(goimgtools.Open("logo.png"), goimgtools.CompositeOptions{
        Gravity: goimgtools.SouthEast, Left: -24, Top: -24, // 右下角，边距 24 像素
    }).
    JPEG(85).
    ToFile("out.jpg")
```

命令行：

```bash
goimgtools -rotate auto -resize 1536x1536 -no-enlarge -flatten white -watermark logo.png -quality 85 in.jpg out.jpg
```

```bash
goimgtools batch -resize 1920x1920 -no-enlarge -quality 80 -format jpeg in_dir out_dir
```

## 文档

| 文档 | 内容 |
|---|---|
| [用法](docs/zh-CN/usage.md) | 输入、操作、输出、元数据与资源控制；服务端使用（错误分类、取消、并发）；水印；文字；图像检查工具 |
| [压缩](docs/zh-CN/compression.md) | JPEG、PNG、WebP 与 GIF 动画的压缩方法及实测数据 |
| [命令行](docs/zh-CN/cli.md) | 全部选项与目录批处理 |
| [扩展](docs/zh-CN/extending.md) | 自定义操作、输入与格式，按名称注册操作，钩子，自定义命令行程序 |
| [性能与基准测试](docs/zh-CN/performance.md) | 内存控制机制、多核并行、实测数据与复测方法 |
| [一致性与对比](docs/zh-CN/compatibility.md) | 与标准库逐字节一致的范围、有意的差异、与 libwebp 的对比 |
| [测试](docs/zh-CN/testing.md) | 测试方法、已验证平台、模糊测试 |
| [变更记录](docs/zh-CN/CHANGELOG.md) | 各版本的变更 |
| [API 参考](https://pkg.go.dev/github.com/whhhh1500/goimgtools) | 全部类型与方法（英文） |

## 支持的格式

| 格式 | 读 | 写 | 读取方式 |
|---|---|---|---|
| PNG | ✓（APNG：默认图像，使用 `Animated()` 时为每一帧） | ✓ | 逐行流式解码；隔行（Adam7）图像先将各遍扫描写入暂存区，再按行输出 |
| JPEG | ✓ | ✓ | 基线式逐行流式解码；渐进式将 DCT 系数写入暂存区，末次扫描完成后按行重建 |
| WebP | ✓（动画：第一帧） | ✓（有损、无损、动画） | 有损按宏块行流式解码；无损仅保留格式规定的最大回溯窗口（约 4.2MB） |
| GIF | ✓（含动画） | ✓（含动画） | 逐行流式解码；隔行处理方式同 PNG |
| BMP | ✓ | ✓ | 按文件偏移逐行读取 |
| TIFF | ✓ | ✓ | 逐行流式解压 |

暂存区数据不超过内存上限的 1/8（默认 8MB）时驻留内存，超出部分写入临时文件，用后删除。

## 限制

- 不支持 AVIF 与 HEIC。
- 不读写 TIFF 中的 EXIF（ICC 色彩配置可读写）。
- 查找表类（A2B0 等）与灰度色彩配置不转换至 sRGB，按原样写出（包括输出 GIF、BMP 时）。
- JPEG 解码中的熵解码为顺序过程，IDCT 与色彩转换尚未并行化；多线程下它是 JPEG 缩小流程中最慢的环节。
- 动画只输出为 GIF 或 WebP（不输出 APNG）。GIF 输出中相邻的相同帧不合并（每帧仍单独存储，仅占数十字节）。
- 未内置局部对比度（CLAHE）与颜色曲线；逐像素的调整可用 `Pixels` 以数行代码实现（参见[扩展](docs/zh-CN/extending.md)中的灰度示例），依赖邻域像素的滤镜可实现为 `Pipe` 环节或 `Convolve` 卷积核。
- GIF、BMP、TIFF 编码为单线程；无损 WebP 的 LZ77 匹配与第二遍分组为顺序过程，多线程仅提速 10–20%。
- JPEG 网格量化按标准霍夫曼表估算码长（mozjpeg 先统计全图再用优化后的表估算）：本库按条带流式处理，不为此保存整图系数。
- `MaxFileSize` 仅支持 JPEG 与有损 WebP（以及提供 `Quality` 的注册格式），不支持动画。

## 版本

1.0 之前 API 可能发生不兼容变更。各版本变更见[变更记录](docs/zh-CN/CHANGELOG.md)。

## 参与贡献

欢迎提交 Issue 与 Pull Request，详见[贡献指南](.github/CONTRIBUTING.md)。

## 许可

本项目采用 MIT 许可（[LICENSE](LICENSE)）。改写自 Go 标准库与 `golang.org/x/image` 的代码沿用其 BSD 许可（[licenses/LICENSE-GO](licenses/LICENSE-GO)）；移植自 libjpeg 的两部分及移植自 mozjpeg 的网格量化沿用其许可（[licenses/LICENSE-LIBJPEG-TURBO](licenses/LICENSE-LIBJPEG-TURBO)）；移植自 Pillow 的模糊与反锐化掩模沿用其 MIT-CMU 许可（[licenses/LICENSE-PILLOW](licenses/LICENSE-PILLOW)）；移植自 libwebp 的有损 WebP 编码部分与 WebP 解码的颜色换算沿用其 BSD 许可及专利授权（[licenses/LICENSE-LIBWEBP](licenses/LICENSE-LIBWEBP)）；依 jpegli 生成的 JPEG 量化表沿用其 BSD 许可（[licenses/LICENSE-JPEGLI](licenses/LICENSE-JPEGLI)）；移植自 libdeflate 的 DEFLATE 压缩器沿用其 MIT 许可（[licenses/LICENSE-LIBDEFLATE](licenses/LICENSE-LIBDEFLATE)），其中参照 zopfli 的分块沿用 Apache 2.0 许可（[licenses/LICENSE-ZOPFLI](licenses/LICENSE-ZOPFLI)），参照 oxipng 的 PNG 行滤波搜索沿用其 MIT 许可（[licenses/LICENSE-OXIPNG](licenses/LICENSE-OXIPNG)）。各文件的来源详见 [licenses/THIRD-PARTY.md](licenses/THIRD-PARTY.md)。
