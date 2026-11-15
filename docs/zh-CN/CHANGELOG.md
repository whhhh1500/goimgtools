[English](../CHANGELOG.md) | 中文

# 变更记录

## 未发布

### 新增

- 图像调整操作：`Grayscale`、`Modulate`、`Blur`、`Sharpen`（轻度锐化或反锐化掩模）、`Extend`、`Trim`、`RotateBy`（任意角度）。
- `inspect.Stats`、`inspect.Hash`（感知哈希）、`inspect.ReadExif` / `ParseExif`；子命令 `stats`、`hash`、`exif`。
- 按名称注册操作：`RegisterOperation`、`UnregisterOperation`、`LookupOperation`、`Operations`、`Image.Do`。包裹每一步骤的钩子：`Image.Around`。`ParseColor`。
- `cli` 包：以库的形式提供命令行（`cli.Main`、`cli.Run`），程序可向其中加入自定义操作；命令行选项 `-op 名字[:参数]` 与子命令 `ops`。
- `ToSRGB()` / `-srgb`：依据色彩配置将颜色转换至 sRGB。GIF、BMP 无法附带色彩配置，输出时自动转换。
- WebP、TIFF 支持读写色彩配置；WebP 支持读写 EXIF。
- `JPEGOptions.Trellis` / `-jpeg-trellis`：网格量化（采用 mozjpeg 的方法）。
- `CompositeOptions.WidthRatio` / `HeightRatio`：按底图的比例确定水印尺寸；`-watermark-width N%`。
- 有损 WebP 分段量化：各段使用独立的量化级别（移植自 libwebp 的空间噪声整形）。
- `pipeline.Env.SetHelpers`、`Helpers`、`Parallel`、`Go`：供处理环节将工作分摊至多个核。

### 变更

- `Threads(n)` 的上限由 3 改为 CPU 核数。自 4 线程起，缩放、模糊、锐化、色彩调整、旋转以及 JPEG、PNG、有损 WebP 编码将工作分摊至多个核。输出与线程数无关，逐字节一致。
- PNG 默认压缩级别由 6（标准库的默认值）改为 7：Go 的 6 级查找重复数据的能力较弱，7 级在渐变、图标类图像上约小 5 倍，照片小 4–25%。
- PNG 按 256KB 分块压缩（与 pigz 的做法相同），因而可由多核分摊。图像数据不超过一块时，文件与此前版本相同。
- GIF 动画输出：所有帧共用一套按画面内容选取的调色板（`GIFOptions` 的 `Colors`、`NoDither` 对动画生效），自第二帧起仅存储变化的矩形区域。`GIFOptions{FixedPalette: true}` 仍使用固定调色板逐帧写出整帧。在处理任何一帧之前即检查像素上限。`FixedPalette` 下亦可使用 `NoDither`。
- 有损 WebP：同一质量设置下文件约小 15%（分段量化）；同等 SSIM 下比 libwebp 1.6 小 2.4%。
- 无损 WebP：每个行带的预测器依据该行带开始时的代价模型选择，以便并行选择；输出文件略有差异。
- 命令行代码由 `cmd/goimgtools` 移至 `cli` 包；`cmd/goimgtools` 构建的仍为同一程序。

### 修复

- 操作中发生 panic 时，处理链的输入可能未被关闭，继续占用内存预算与临时文件。

## 0.1.0（2026-11-14）

首个公开版本：在内存上限内按行读写 PNG、JPEG、WebP、GIF（含动画）、BMP、TIFF；缩放（6 种插值核、5 种适配模式）、裁剪、90 度倍数旋转、翻转、铺底色、图像叠加与水印、文字；JPEG 优化编码表与渐进式编码、PNG 减色、`MaxFileSize`；批量处理；扩展接口（`Pipe`、`Pixels`、`Apply`、`FromImage`、`FromSource`、`RegisterFormat`、`Stream`、`ToImage`，`pipeline` 包）；子命令 `info`、`check`、`compare`、`formats` 与 `inspect` 包；输入与输出像素上限、取消、panic 转为错误返回。
