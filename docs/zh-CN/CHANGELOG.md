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
- `JPEGOptions.QuantTable`（`QuantTable` 类型，每种选择有具名常量）/ `-jpeg-quant-table`：mozjpeg 的 9 套量化表，编号与其 `-quant-table` 一致；默认 `QuantTableStandard`（0）为 JPEG 标准表。
- `QuantTableJpegli`（`-jpeg-quant-table 9`）：按 jpegli 的方法生成 JPEG 量化表，不开 `Trellis` 时采用其死区；与 `Progressive`、`Trellis` 同用时，同等 SSIMULACRA2 下比 mozjpeg 默认设置小 15%。
- `GIFOptions.Lossy` / `-gif-lossy`：GIF 有损 LZW 压缩，静态与动画均可（gifsicle `--lossy` 的方法）：照片类动画取 16 时小 18%，取 24 时小 32%。
- WebP 动画输出：`Animated()` 且输出为 WebP 时，将 GIF 动画写为 WebP 动画（有损或无损），每帧仅存储变化的矩形区域。
- WebP 动画输入：读取其第一帧（此前作为无法读取的图像拒绝）。`Frames` 可统计 WebP 动画与 PNG 动画（APNG）的帧数，`info` 一并显示。
- `CompositeOptions.WidthRatio` / `HeightRatio`：按底图的比例确定水印尺寸；`-watermark-width N%`。
- 有损 WebP 分段量化：各段使用独立的量化级别（移植自 libwebp 的空间噪声整形）。
- `pipeline.Env.SetHelpers`、`Helpers`、`Parallel`、`Go`：供处理环节将工作分摊至多个核。

### 变更

- PNG 7 级起每 32KB 的行比较「不滤波」与各行自选的滤波，保留较小者：截图、图标、绘图类小 10–19%，照片不变。
- 最低 Go 版本降为 1.26（0.1.0 要求 1.27）。Go 1.27 重写了 `compress/flate`，因此 Go 1.26 与 Go 1.27 写出的 PNG 文件字节不同，像素相同。Go 1.26 下 PNG 默认级别为 6（见下文）。
- 渐进式 JPEG 按 mozjpeg 的方法（`optimize_scans`）选择扫描：文件小 0.4–14%（18 张图平均 2.4%，与 `Trellis`、`QuantTable: QuantTableImageMagick` 同用时平均 3.9%；四者同用时同等 PSNR 下比 mozjpeg 默认设置小 0.7%），像素不变。单线程多用 3–19% 的时间；候选扫描的统计随 `Threads` 并行。
- `Animated()` 不再忽略无法保留的动画：GIF 输出为 GIF、WebP 以外的格式，或输入为 WebP 动画、APNG 时，返回包装了 `errors.ErrUnsupported` 的错误，而不是写出第一帧。静态图像不受影响。`MaxFileSize` 与 `Animated()` 同用于动画时同样报错（此前被忽略）。
- `Threads(n)` 的上限由 3 改为 CPU 核数。自 4 线程起，缩放、模糊、锐化、色彩调整、旋转以及 JPEG、PNG、有损 WebP 编码将工作分摊至多个核。输出与线程数无关，逐字节一致。
- Go 1.27 下 PNG 默认压缩级别由 6（标准库的默认值）改为 7（Go 1.26 下仍为 6：该版本 7 级仅小 1–1.5%，耗时多一半）：Go 1.27 的 6 级查找重复数据的能力较弱，7 级在渐变、图标类图像上约小 5 倍，照片小 4–25%。
- PNG 按 256KB 分块压缩（与 pigz 的做法相同），因而可由多核分摊。图像数据不超过一块时，文件与此前版本相同。
- GIF 动画输出：所有帧共用一套按画面内容选取的调色板（`GIFOptions` 的 `Colors`、`NoDither` 对动画生效），自第二帧起仅存储变化的矩形区域。`GIFOptions{FixedPalette: true}` 仍使用固定调色板逐帧写出整帧。在处理任何一帧之前即检查像素上限。`FixedPalette` 下亦可使用 `NoDither`。
- 有损 WebP：同一质量设置下文件约小 15%（分段量化）；同等 SSIM 下比 libwebp 1.6 小 2.4%。
- 无损 WebP：每个行带的预测器依据该行带开始时的代价模型选择，以便并行选择；输出文件略有差异。
- 命令行代码由 `cmd/goimgtools` 移至 `cli` 包；`cmd/goimgtools` 构建的仍为同一程序。

### 修复

- 操作中发生 panic 时，处理链的输入可能未被关闭，继续占用内存预算与临时文件。
- GIF 的 `Frames().LoopCount` 此前为其重复次数，比字段文档所述的播放次数少 1。

## 0.1.0（2026-11-14）

首个公开版本：在内存上限内按行读写 PNG、JPEG、WebP、GIF（含动画）、BMP、TIFF；缩放（6 种插值核、5 种适配模式）、裁剪、90 度倍数旋转、翻转、铺底色、图像叠加与水印、文字；JPEG 优化编码表与渐进式编码、PNG 减色、`MaxFileSize`；批量处理；扩展接口（`Pipe`、`Pixels`、`Apply`、`FromImage`、`FromSource`、`RegisterFormat`、`Stream`、`ToImage`，`pipeline` 包）；子命令 `info`、`check`、`compare`、`formats` 与 `inspect` 包；输入与输出像素上限、取消、panic 转为错误返回。
