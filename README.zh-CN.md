# goimgtools

| 缩到 1536 框内 + 白底 + 水印（单线程） | goimgtools | 标准库整张解码 |
|---|---|---|
| JPEG 6000×4000（2400 万像素）：进程峰值内存 | **8.3MB** | 242MB |
| PNG 6900×9200（6300 万像素，带透明）：进程峰值内存 | **10.4MB** | 588MB |
| JPEG 6000×4000：耗时 | **0.38s** | 0.87s |
| PNG 6900×9200：耗时 | **1.63s** | 3.10s |

**超低内存占用的纯 Go 图片处理库和命令行程序。** 图片按行流过处理链，从不整张解到内存里，所以内存占用基本不随图片变大而增长，速度也比整张解码快。（峰值里约 5.4MB 是 Go 程序启动本身就占的；测试环境和更多数字见[内存、速度和实测](docs/zh-CN/performance.md)。）

[![Go Reference](https://pkg.go.dev/badge/github.com/whhhh1500/goimgtools.svg)](https://pkg.go.dev/github.com/whhhh1500/goimgtools)
[![test](https://github.com/whhhh1500/goimgtools/actions/workflows/test.yml/badge.svg)](https://github.com/whhhh1500/goimgtools/actions/workflows/test.yml)

[English](README.md) | 中文

## 特点

- **内存有硬上限**：默认 64MB，用 `MemoryLimit` 可调。必须看完整张图才能继续的步骤（旋转、渐进式 JPEG、减色等）暂存数据超出份额就写临时文件；所有随图片尺寸变大的缓冲都计入上限，不够时返回错误，不会悄悄多用。适合小内存的服务器和容器，以及同时处理很多张图的服务。
- **常用操作**：缩放（6 种算法、5 种适配方式）、裁剪、任意角度旋转、翻转、去边 / 加边、铺底色、灰度、调色、模糊、锐化；图片或文字水印（九宫格定位、按比例缩放、旋转、平铺、透明度）；文字渲染（多字体按字回退、描边、阴影）。
- **六种格式读写**：JPEG、PNG、WebP（有损、无损）、GIF（含动图）、BMP、TIFF。ICC 色彩配置默认保留，也可换算成 sRGB；EXIF 可选保留。
- **压缩**：JPEG 优化编码表、渐进式、网格量化；PNG 按图选色减色；有损 WebP 在 7 张真实照片上同等 SSIM 时比 libwebp 默认设置小 2.4%；按文件大小上限自动选质量；动图只存变化的部分。
- **多核可选**：`Threads(n)`，结果与单线程逐字节相同。
- **不会崩溃**：任何 panic 都转成错误返回；输入和输出的像素数有上限；可以取消；用模糊测试守着。
- **可扩展**：自己的操作、输入、文件格式、缩放滤镜；按名字注册的操作；改写内置行为的钩子；带上自己操作的命令行程序。
- **纯 Go，不依赖 cgo**。依赖只有 Go 官方扩展库（`golang.org/x/image`，以及它解析字体要用的 `x/text`、`x/sys`）和 `github.com/klauspost/compress`（只用它的 zlib 解压）。

## 安装

```bash
go get github.com/whhhh1500/goimgtools
```

命令行程序：

```bash
go install github.com/whhhh1500/goimgtools/cmd/goimgtools@latest
```

需要 Go 1.27 以上。

## 快速上手

接口照 [sharp](https://sharp.pixelplumbing.com/) 的链式写法：输入 → 串操作 → 选输出格式 → 写出。操作按调用顺序执行，错误在最后一步返回。

```go
err := goimgtools.Open("in.jpg").
    Rotate().                              // 按 EXIF 方向转正
    Resize(1536, 0).                       // 宽 1536，高按比例
    Flatten(color.White).                  // 透明处铺白底
    Composite(goimgtools.Open("logo.png"), goimgtools.CompositeOptions{
        Gravity: goimgtools.SouthEast, Left: -24, Top: -24, // 右下角，留 24 像素边
    }).
    JPEG(85).
    ToFile("out.jpg")
```

命令行：

```bash
goimgtools -rotate auto -resize 1536x1536 -no-enlarge -flatten white -watermark logo.png -quality 85 in.jpg out.jpg
```

```bash
goimgtools batch -resize 1920x1920 -no-enlarge -quality 80 -format jpeg 输入目录 输出目录
```

## 文档

| 文档 | 内容 |
|---|---|
| [用法](docs/zh-CN/usage.md) | 输入、操作、输出、元数据和资源控制；在服务里使用（错误分类、取消、并发）；水印；文字；查看和检查图片 |
| [压缩](docs/zh-CN/compression.md) | 让 JPEG、PNG、WebP、GIF 动图变小的做法和实测 |
| [命令行](docs/zh-CN/cli.md) | 全部选项，批量处理目录 |
| [扩展](docs/zh-CN/extending.md) | 自己的操作、输入、格式，按名字注册操作，钩子，自己的命令行程序 |
| [内存、速度和实测](docs/zh-CN/performance.md) | 内存是怎么省下来的、多核、实测数据和复测方法 |
| [一致性与对比](docs/zh-CN/compatibility.md) | 与标准库逐字节一致的部分、故意不同的地方、与 libwebp 的对比 |
| [测试](docs/zh-CN/testing.md) | 怎么测、实跑过的平台、模糊测试 |
| [改动日志](docs/zh-CN/CHANGELOG.md) | 各版本的改动 |
| [API 文档](https://pkg.go.dev/github.com/whhhh1500/goimgtools) | 每个类型和方法的说明（英文） |

## 支持的格式

| 格式 | 读 | 写 | 读取方式 |
|---|---|---|---|
| PNG | 是 | 是 | 按行流式；隔行（Adam7）先把各遍像素存入暂存区再按行取出 |
| JPEG | 是 | 是 | 基线式按行流式；渐进式把 DCT 系数存入暂存区，最后一遍扫描结束后按行还原 |
| WebP | 是 | 是（有损、无损） | 有损按宏块行流式；无损只保留格式规定的最远回看窗口（约 4.2MB） |
| GIF | 是（含动图） | 是（含动图） | 按行流式；隔行同 PNG |
| BMP | 是 | 是 | 按行按文件偏移读 |
| TIFF | 是 | 是 | 按行流式解压 |

「暂存区」的数据不超过内存上限的 1/8（默认 8MB）时放内存，超过就写临时文件，用完即删。

## 没做的

- 不支持 AVIF、HEIC。
- TIFF 的 EXIF 不读不写（ICC 读写）。
- 查找表类（A2B0 等）和灰度的色彩配置不换算到 sRGB（转 GIF、BMP 时原样写出）。
- JPEG 解码的熵解码只能按顺序，IDCT 和颜色换算也还没分摊到多个核：线程多时它是缩小 JPEG 最慢的一环。
- 动图只支持 GIF。相同的相邻帧不合并（仍各写一帧，只占几十字节）；没做 gifsicle 的有损 LZW（`--lossy`）。
- 对比度、伽马、色阶、反色这类调色没有内置方法，用 `Pixels` 几行就能写（[扩展](docs/zh-CN/extending.md)里有灰度的例子）；其他要看相邻像素的滤镜（中值、浮雕、边缘检测等）要自己写成 `Pipe` 的一环。
- GIF、BMP、TIFF 的编码只用一个核；无损 WebP 的 LZ77 匹配和第二遍分组只能按顺序做，多线程只快 10–20%。
- JPEG 网格量化的码长用的是标准哈夫曼表（mozjpeg 先扫一遍整张图统计、用优化后的表来估码长）：本库按条带流式处理，不为此多存一遍整图的系数。
- `MaxFileSize` 只支持 JPEG 和有损 WebP（以及带 `Quality` 的注册格式）。

## 版本

版本号在 1.0 之前，接口还可能调整。每个版本的改动见[改动日志](docs/zh-CN/CHANGELOG.md)。

## 参与

欢迎提问题和提交改动，见[贡献说明](.github/CONTRIBUTING.md)。

## 许可

本项目用 MIT 许可（[LICENSE](LICENSE)）。其中从 Go 标准库和 `golang.org/x/image` 改写的代码沿用其 BSD 许可（[licenses/LICENSE-GO](licenses/LICENSE-GO)）；从 libjpeg 移植的两段和从 mozjpeg 移植的网格量化沿用其许可（[licenses/LICENSE-LIBJPEG-TURBO](licenses/LICENSE-LIBJPEG-TURBO)）；从 Pillow 移植的模糊和反锐化掩模沿用其 MIT-CMU 许可（[licenses/LICENSE-PILLOW](licenses/LICENSE-PILLOW)）；从 libwebp 移植的有损 WebP 分段量化沿用其 BSD 许可（[licenses/LICENSE-LIBWEBP](licenses/LICENSE-LIBWEBP)）。[licenses/THIRD-PARTY.md](licenses/THIRD-PARTY.md) 逐项列了哪些文件来自哪里。
