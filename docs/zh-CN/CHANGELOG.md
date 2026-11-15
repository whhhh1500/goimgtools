[English](../CHANGELOG.md) | 中文

# 改动日志

## 未发布

### 新增

- 调整画面的操作：`Grayscale`、`Modulate`、`Blur`、`Sharpen`（轻度锐化或反锐化掩模）、`Extend`、`Trim`、`RotateBy`（任意角度）。
- `inspect.Stats`、`inspect.Hash`（感知指纹）、`inspect.ReadExif` / `ParseExif`；命令行 `stats`、`hash`、`exif`。
- 按名字注册操作：`RegisterOperation`、`UnregisterOperation`、`LookupOperation`、`Operations`、`Image.Do`。包在每一步外面的钩子：`Image.Around`。`ParseColor`。
- `cli` 包：命令行做成库（`cli.Main`、`cli.Run`），程序可以把自己的操作加进去；命令行选项 `-op 名字[:参数]` 和子命令 `ops`。
- `ToSRGB()` / `-srgb`：按色彩配置把颜色换算成 sRGB。GIF、BMP 带不了配置，输出时自动换算。
- WebP、TIFF 读写色彩配置；WebP 读写 EXIF。
- `JPEGOptions.Trellis` / `-jpeg-trellis`：网格量化（mozjpeg 的做法）。
- `CompositeOptions.WidthRatio` / `HeightRatio`：水印按大图的比例定大小；`-watermark-width N%`。
- 有损 WebP 分段量化：各段用自己的量化级别（照 libwebp 的空间噪声整形移植）。
- `pipeline.Env.SetHelpers`、`Helpers`、`Parallel`、`Go`：让环节把工作分给多个核。

### 改变

- `Threads(n)` 的上限从 3 改为 CPU 核数。4 线程起，缩放、模糊、锐化、调色、旋转，以及 JPEG、PNG、有损 WebP 编码会把工作分给多个核。输出与线程数无关，逐字节相同。
- PNG 默认压缩级别从 6（标准库的默认）改为 7：Go 的 6 级找重复很弱，7 级在渐变、图标这类图上小约 5 倍，照片小 4–25%。
- PNG 按 256KB 一块压缩（pigz 的做法），所以能多核分摊。图像数据不超过一块时文件与以前相同。
- GIF 动图输出：所有帧共用一套按画面选的调色板（`GIFOptions` 的 `Colors`、`NoDither` 对动图生效），第二帧起只存变化的矩形。`GIFOptions{FixedPalette: true}` 仍用固定调色板逐帧写整张。动图在处理任何一帧之前就检查像素上限。`FixedPalette` 时也能用 `NoDither`。
- 有损 WebP：同一质量设置下文件小约 15%（分段量化）；同等 SSIM 时比 libwebp 1.6 小 2.4%。
- 无损 WebP：每一带的预测器按这一带开始时的代价模型选，以便并行；文件略有不同。
- 命令行代码从 `cmd/goimgtools` 移到 `cli` 包；`cmd/goimgtools` 编出的还是同一个程序。

### 修复

- 操作里 panic 时，处理链的输入可能没有关闭，占着内存预算和临时文件。

## 0.1.0（2026-11-14）

第一个公开版本：在内存上限内按行读写 PNG、JPEG、WebP、GIF（含动图）、BMP、TIFF；缩放（6 种算法、5 种适配方式）、裁剪、90 度倍数的旋转、翻转、铺底色、叠图和水印、文字；JPEG 优化编码表和渐进式、PNG 减色、`MaxFileSize`；批量处理；扩展接口（`Pipe`、`Pixels`、`Apply`、`FromImage`、`FromSource`、`RegisterFormat`、`Stream`、`ToImage`，`pipeline` 包）；命令行 `info`、`check`、`compare`、`formats` 和 `inspect` 包；输入和输出像素上限、取消、panic 转成错误。
