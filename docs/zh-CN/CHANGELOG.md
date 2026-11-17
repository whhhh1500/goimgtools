[English](../CHANGELOG.md) | 中文

# 变更记录

## 未发布

### 从 0.1.0 升级

0.1.0 导出的内容没有删除，签名也没有改动（用 `apidiff` 核对；此后每次推送都会与上一发布版本比较）。为 0.1.0 写的代码仍可能遇到的变化：

- `Animated()` 遇到无法保留的动画（GIF、WebP 或 PNG 动画转 JPEG、PNG、BMP 或 TIFF）时返回包装 `errors.ErrUnsupported` 的错误，0.1.0 会静默只写第一帧。去掉 `Animated()` 即得第一帧。
- 有损 WebP 解码为 `*image.NRGBA` 条带，不再是 `*image.YCbCr`，以便颜色与 libwebp 一致；`Metadata()` 仍报告 `ycbcr`。对解码结果做类型断言的代码应接受任意 `image.Image`。
- GIF 的 `Frames().LoopCount` 为播放次数（与文档一致），比 0.1.0 返回的多 1。
- Go 1.27 下 PNG 默认级别为 7（原为 6）：文件更小，写得更慢。`PNGOptions{CompressionLevel: 6}` 可得原来的文件。
- `Threads(n)` 不再以 3 为上限：n 更大时即用更多核心。

### 新增

- 图像调整操作：`Grayscale`、`Modulate`、`Blur`、`Sharpen`（轻度锐化或反锐化掩模）、`Extend`、`Trim`、`RotateBy`（任意角度，与 sharp 的 `rotate` 逐字节一致）。
- `inspect.Stats`、`inspect.Hash`（感知哈希）、`inspect.ReadExif` / `ParseExif`；子命令 `stats`、`hash`、`exif`。`Stats` 还报告 sharp 的 `stats()` 所报告的各项，数值一致到末几位：`Channels`（类型 `ChannelStats`）、`Entropy`、`Sharpness`。
- 按名称注册操作：`RegisterOperation`、`UnregisterOperation`、`LookupOperation`、`Operations`、`Image.Do`。包裹每一步骤的钩子：`Image.Around`。`ParseColor`。
- `cli` 包：以库的形式提供命令行（`cli.Main`、`cli.Run`），程序可向其中加入自定义操作；命令行选项 `-op 名字[:参数]` 与子命令 `ops`。
- `ToSRGB()` / `-srgb`：依据色彩配置将颜色转换至 sRGB。GIF、BMP 无法附带色彩配置，输出时自动转换。
- WebP、TIFF 支持读写色彩配置；WebP 支持读写 EXIF。
- `JPEGOptions.Trellis` / `-jpeg-trellis`：网格量化（采用 mozjpeg 的方法）。
- `JPEGOptions.QuantTable`（`QuantTable` 类型，每种选择有具名常量）/ `-jpeg-quant-table`：mozjpeg 的 9 套量化表，编号与其 `-quant-table` 一致；默认 `QuantTableStandard`（0）为 JPEG 标准表。
- `QuantTableJpegli`（`-jpeg-quant-table 9`）：按 jpegli 的方法生成 JPEG 量化表，不开 `Trellis` 时采用其死区；与 `Progressive`、`Trellis` 同用时，同等 SSIMULACRA2 下比 mozjpeg 默认设置小 15%。
- `-webp-effort`：命令行可设 WebP 的力度（库里为 `WebPOptions.Effort`）。
- `GIFOptions.Lossy` / `-gif-lossy`：GIF 有损 LZW 压缩，静态与动画均可（gifsicle `--lossy` 的方法）：照片类动画取 16 时小 18%，取 24 时小 32%。
- GIF 动图：各帧合计超过 256 色但每帧不超过 256 色时，每帧用自己的调色板，逐像素不变（以前会减成一个调色板，既改了像素又让文件变大）；变化的帧在三种写法中取编码最小者，LZW 字典写满后在划算时继续沿用：三段动画上比 gifsicle `-O3` 小 0.2–0.45%。
- WebP 动画输出：`Animated()` 且输出为 WebP 时，将 GIF 动画写为 WebP 动画（有损或无损），每帧仅存储变化的矩形区域。
- WebP 动画输入：读取其第一帧（此前作为无法读取的图像拒绝）；使用 `Animated()` 时逐帧处理并写为 GIF 或 WebP 动画：每帧按 libwebp 动画解码器的方式画到画面上（混合或直接覆盖，之后清除其矩形与否），逐像素一致；各帧时长与播放次数随之保留（写为 GIF 时以百分之一秒计，次数按首次之后的重复次数）。`Frames` 可统计 WebP 动画与 PNG 动画（APNG）的帧数，`info` 一并显示。
- PNG 动画（APNG）输入：使用 `Animated()` 时逐帧处理并写为 GIF 或 WebP 动画，每帧按 APNG 规范绘制（直接覆盖或按 apngdis 的方式合成在上；之后清除、复原或保留），逐帧与按规范编写的渲染程序对照一致。
- `CompositeOptions.WidthRatio` / `HeightRatio`：按底图的比例确定水印尺寸；`-watermark-width N%`。
- 有损 WebP 分段量化：各段使用独立的量化级别（移植自 libwebp 的空间噪声整形）。
- `pipeline.Env.SetHelpers`、`Helpers`、`Parallel`、`Go`：供处理环节将工作分摊至多个核。
- TIFF 输出：`TIFFLZW` 压缩（libtiff 的写法）与 `TIFFOptions.Predictor`（水平差分，sharp 的默认设置）：一张 2400 万像素照片 LZW 82 → 52MB、Deflate 67 → 45MB；`-tiff-compression lzw`、`-tiff-predictor`。
- `SharpenOptions.Method` 新增 `SharpenLightness`，配合 `SharpenOptions.Curve`（`SharpenCurve`、`DefaultSharpenCurve`）：sharp 带 sigma 的 `sharpen`，只锐化明度（仅在少数像素上相差 1 级）；按名字调用 `sharpen SIGMA,lightness[,FLAT,JAGGED,JAGGED_FROM,MAX_BRIGHTEN,MAX_DARKEN]`，命令行 `-sharpen` 同样写法。
- `Affine(a, b, c, d, AffineOptions{...})`：与 sharp 的 `affine` 逐字节一致（`Interpolator` 可选双线性、最近邻、双三次，含透明与不含透明）；按名字调用 `affine A,B,C,D[,BACKGROUND[,IDX,IDY,ODX,ODY]][,bilinear|nearest|bicubic]`。
- `EnsureAlpha`（与 sharp 的 `ensureAlpha` 逐字节一致）；按名字调用 `ensure-alpha [ALPHA]`。
- 缩放核 `KernelMKS2013` 与 `KernelMKS2021`（Magic Kernel Sharp，即 sharp 的 `mks2013`、`mks2021`）；命令行 `-kernel mks2013|mks2021`。
- `Rank`（Pillow 的 `RankFilter`、`MinFilter`、`MaxFilter`）、`Dilate` 与 `Erode`（sharp 的 `dilate`、`erode`），均逐字节一致；按名字调用 `rank SIZE,RANK`、`dilate [WIDTH]`、`erode [WIDTH]`。dilate 与 erode 的耗时几乎不随宽度增长（libvips 随宽度的平方增长）。
- `BlurOptions`（`Method`：默认 `BlurBoxes` 即 Pillow 的算法，或 `BlurKernel`，与 sharp 的 `blur` 逐字节一致，含透明图像；`MinAmplitude` 即 sharp 的 `minAmplitude`）；按名字调用 `blur SIGMA,kernel[,MIN_AMPLITUDE]`；命令行 `-blur-method`、`-blur-min-amplitude`。高斯核拆成至多 20 个嵌套方框计算，耗时几乎不随 sigma 增长。
- 仿照 sharp 的选项：`NegateOptions.Alpha`（透明度也取反，sharp 的默认做法）、`ThresholdOptions.PerChannel`（即 sharp 的 `greyscale: false`）、`ModulateOptions.Lightness`（即 sharp 的 `lightness`）与 `GrayscaleOptions.Linear`（即 sharp 的 `greyscale`，逐字节一致）；按名字调用 `negate alpha`、`threshold LEVEL,channels`、`modulate B,S,H,L`、`grayscale linear`；命令行 `-grayscale-linear`，`-modulate` 可加第四个数。
- XMP：`KeepXmp()` / `-keep-xmp` 在 JPEG、PNG、WebP、TIFF 输出中保留 XMP 数据包；`Info.HasXmp`、`inspect.ReadXmp`；自定义格式可用 `EncodeOptions.Xmp` 与 `pipeline.Meta.Xmp`。读写方式与 sharp 相同（在 sharp 写出的文件上双向核对）。
- `Trim` 的找边方式与 sharp 的 `trim`（libvips 的 `find_trim`）相同：在 3×3 中值滤波后的图像上找边，噪点与斑点不再妨碍裁剪（像素相同时裁剪范围与 sharp 完全一致）；`TrimOptions` 可指定 `Background`、`LineArt`（不做中值滤波，适用于细线条绘图）与 `Margin`；命令行 `-trim-background`、`-trim-line-art`、`-trim-margin`。
- `JPEGOptions.ChromaSubsampling`（类型 `ChromaSubsampling`：默认 `Chroma420`、`Chroma422`、`Chroma444`）/ `-jpeg-chroma`：JPEG 输出可保留全部色度或横向一半，普通、优化、渐进与网格量化均支持（即 sharp 的 `chromaSubsampling`）。
- 可读取 JPEG 压缩的 TIFF 文件（压缩方式 7：libtiff 的写法，libvips 与 sharp 的默认设置），条带与图块、彩色与灰度均支持。
- 分辨率：`Info.Density`（每英寸像素数，读取自 JFIF、EXIF、pHYs、TIFF 与 BMP），默认保留到输出，`Density(dpi)` / `-density` 可设置；自定义格式可用 `EncodeOptions.Density` 与 `pipeline.Meta.Density`。`info` 会显示它。
- `ExtendWith`（类型 `Extension`：`ExtendCopy`、`ExtendRepeat`、`ExtendMirror`）/ `-extend-with`：用图像本身扩边，与 sharp 带 `extendWith` 的 `extend` 逐像素一致；`Do("extend")` 在颜色的位置接受 copy、repeat 或 mirror。
- Pillow 的 `ImageOps` 与 `ImageEnhance`，逐字节一致：`Posterize`、`Solarize`、`Equalize`、`AutoContrast`（`AutoContrastOptions`）与 `Enhance`（类型 `Enhancement`：`EnhanceColor`、`EnhanceContrast`、`EnhanceBrightness`、`EnhanceSharpness`）；按名字调用 posterize、solarize、equalize、autocontrast、enhance。
- `ResizeOptions.Method`（类型 `ResizeMethod`）/ `-resize-method`：`ResizeLibvips` 即 sharp 的 `resize`，逐字节一致，涵盖所有核、fit、位置与背景，包括解码时缩小的 JPEG（在 sharp 上核对了 495 次缩放）；`ResizePillow` 即 Pillow 的 `Image.resize`，逐字节一致（核对了 346 次缩放），配合 `ResizeOptions.ReducingGap`（`-reducing-gap`）即其 `reducing_gap` 与 `Image.thumbnail`；默认的 `ResizeDraw` 与以前相同。`Do("resize")` 以第七个参数接受 `draw`、`libvips` 或 `pillow`。`KernelBox` 与 `KernelHamming`（`-kernel box`、`hamming`）：Pillow 的 `BOX` 与 `HAMMING`。
- `ResizeOptions.LinearLight` / `-linear-light`：在线性光下缩放，同 libvips `thumbnail` 的 `linear`；`Do("resize")` 以第六个参数 `linear` 接受。
- `ResizeOptions.Position`（`FitCover` 保留的部分、`FitContain` 放置图像的位置）与 `ResizeOptions.Interesting`（类型 `Interesting`：`InterestingEntropy`、`InterestingAttention`，即 sharp 的智能裁剪策略）/ `-position`。`Do("resize")` 以第五个参数接受它们。
- `Recomb`、`Unflatten`、`ExtractChannel`（类型 `Channel`）、`RemoveAlpha`，仿照 sharp 的 `recomb`、`unflatten`、`extractChannel`、`removeAlpha`（前两者逐字节一致），也可按名称调用：recomb、unflatten、extract-channel、remove-alpha。
- `CompositeOptions.Blend`（类型 `BlendMode`）/ `-watermark-blend`：sharp 合成的各种混合模式（正片叠底、滤色、叠加、变暗、变亮、颜色减淡与加深、强光与柔光、差值、排除），与 sharp 的结果相差不超过 1。
- 仿照 sharp 的色阶与滤镜：`Negate`、`Linear`、`Gamma`、`Threshold`、`Tint`、`Normalize`（`NormalizeOptions`）、`CLAHE`（`CLAHEOptions`）、`Median`、`Convolve`（`Convolution`），也可按名称调用（`Do`、`-op`）：negate、linear、gamma、threshold、tint、normalize、clahe、median、convolve。`Negate`、`Linear`、`Threshold`、`CLAHE`、`Median`、`Convolve` 与 sharp 的结果逐字节一致，`Normalize` 与 `Tint` 相差不超过 1；`Median` 也与 Pillow 的 `MedianFilter` 逐字节一致。

### 变更

- `FastShrinkOnLoad` 改用 libjpeg 的缩小反 DCT（`jidctred.c`）缩小各块，不再用自行推导的平均，速度不变：4:4:4 与灰度 JPEG 的平面与 libjpeg 的缩放解码（Pillow 的 `draft`）相同，与完整解码后缩放的结果一样接近（46.7–50.5dB）。
- JPEG 改为按 libjpeg-turbo 的方式解码，因此与 Pillow、sharp 逐字节一致（精确整数反 DCT、下采样色度的 fancy 上采样、YCbCr 转 RGB 查表），不再与 `image/jpeg` 相同：下采样色彩的边缘更平滑（一张 4:2:2 照片与原图的差距少 0.36 dB），所有与 Pillow、sharp 结果一致的操作在 JPEG 输入上也一致。彩色 JPEG 的行带改为 RGBA 而非 YCbCr，直接写成 JPEG 或有损 WebP 时除外。
- TIFF 解码：LZW 快 1.5 倍（6000×4000 的 RGBA 图像 0.45 秒，原为 0.68 秒；libtiff 0.31 秒），对任何输入的输出与错误都与 `x/image` 相同（经模糊测试核对）；Deflate 块按 64KB 读取（0.26 秒，原为 0.30 秒；libtiff 0.27 秒）。
- `FitCover` 居中裁剪的位置与 sharp 相同：图像比框多出奇数个像素时，多出的那一个从左侧（上方）而非右侧（下方）裁去。
- JPEG 解码快 45%，像素不变：熵编码数据一次读 64 位，经 11 位查表把多数系数的值连同其码一起解出，系数边解边反量化，逆 DCT 跳过全零的行（6000×4000 的基线照片单核 98ms，原为 188ms）。启用 `FastShrinkOnLoad` 时由系数直接解出原尺寸 1/2、1/4 或 1/8 的图（同 libjpeg），不再完整解码后求平均，其余缩放直接在 Y、Cb、Cr 平面上以定点进行，不再转成 RGBA：把这张照片缩到 400×400 以内用时 82ms，原为 231ms（libvips 的 `vipsthumbnail`：97ms）；缩到 1536×1536 以内 157ms，原为 320ms（148ms；2 线程 108ms，对方 172ms）。
- 从文件或内存读取的渐进式 JPEG 按扫描同步解码，逐行块进行，不再保存整幅图的系数（6000×4000 时 72MB，默认上限下写入临时文件），细化扫描只访问非零系数：6000×4000 的照片解码用时约为原来的三分之一（单核 0.24s，原为 0.70s），不用临时文件，内存多 0.5MB。启用 `FastShrinkOnLoad` 缩小 8 倍时每块即其像素的平均值，只解码 DC 系数的扫描：这张照片缩到 400×400 以内用时 50ms，原为 745ms（libvips 的 `vipsthumbnail`：228ms）。损坏的扫描留下未完的块结束游程时，游程随该扫描结束（同 libjpeg）；标准库会把它带入下一个扫描。
- JPEG 编码快一倍多，写出的字节与之前相同（即标准库 `jpeg.Encode` 的输出）：熵编码数据 64 位一组收集、32 位一组写出，一个块的位始终保存在寄存器中；只访问非零系数，每次查找四个；量化不再分支；RGBA、NRGBA 像素的颜色转换内联完成；4:2:0 子采样的 YCbCr 图像（解码得到的 JPEG 大多如此）直接取用其色度，不再先铺到每个像素再平均回去（4:4:4 则直接从平面求平均）。单核重新编码 6000×4000 的 JPEG：0.23s，原为 0.54s（libvips：0.12s；2 线程 0.13s，与其 0.13s 相当，4 线程 0.10s）。
- PNG 图像与 Deflate 压缩的 TIFF 图像解压快约一倍（`internal/inflate`，按 libdeflate 的方法编写：64 位位缓冲无分支补充，查表同时得到码的附加位，补充前先查好下一个码，匹配每次复制 8 字节），接受与拒绝的数据和以前相同，从文件读取的量也相同。单核解码 6000×4000 的 RGB PNG 用时 0.25s，原为 0.37s（libvips：0.25s）；转为 JPEG 用时 0.39s，原为 0.51s（0.25s）。
- 用 Paeth 滤波的 PNG 行改为一个像素的各通道一起、无分支地还原（标准库沿整行还原一个通道后再还原下一个）：6000×4000 的 RGBA 照片缩到 1536×1536 以内，单核 0.45s，原为 0.51s；2 线程 0.26s，原为 0.32s（vipsthumbnail：0.43s）。
- 默认 effort 的有损 WebP 编码快 25%，写出的文件不变：两遍之间保存的已编码记号按 libwebp 的位读取方式读回（每次 56 位），选择环路滤波强度所用的 SSIM 求和直接在固定的 16×16、8×8 块上进行，trellis 每个系数只查一次代价表。6000×4000 的照片：单核 7.5s，原为 10.1s；8 线程 1.55s，原为 1.79s。
- 有损 WebP 解码快 15–18%，像素不变：按 libwebp 的 VP8GetBit 读取位，一个块的系数用局部变量保存的读取状态解析（质量 95 的 6000×4000 图像：0.78s，原为 0.95s；dwebp 0.48s）。
- 色度在垂直方向子采样、上边缘位于 0 以上奇数行的 YCbCr 或 NYCbCrA 图像，编码为 JPEG（或其他需要重新分带的处理）时不再报内部错误。
- PNG 7 级起每 32KB 的行比较「不滤波」与各行自选的滤波，保留较小者：截图、图标、绘图类小 10–19%，照片不变。
- PNG 8、9 级改为搜索最小文件：各行按 oxipng 的 Brute 策略滤波（每种滤波都试），数据改用 libdeflate「接近最优解析」的 Go 移植压缩，不再用 `compress/flate`，并像 zopfli 那样按代价切分 deflate 块、按熵给符号计价。17 张截图、照片与游戏美术图上比 7 级小 14.2%（8 级）和 15.5%（9 级），oxipng `-o max` 为 14.8%（耗时是我们的 6 倍），ECT `-9` 为 15.8%；各 Go 版本输出同一文件。每线程多占约 4MB（8 级）和 6.5MB（9 级）内存。
- 最低 Go 版本降为 1.26（0.1.0 要求 1.27）。Go 1.27 重写了 `compress/flate`，因此 Go 1.26 与 Go 1.27 写出的 PNG 文件字节不同，像素相同。Go 1.26 下 PNG 默认级别为 6（见下文）。
- 渐进式 JPEG 按 mozjpeg 的方法（`optimize_scans`）选择扫描：文件小 0.4–14%（18 张图平均 2.4%，与 `Trellis`、`QuantTable: QuantTableImageMagick` 同用时平均 3.9%；四者同用时同等 PSNR 下比 mozjpeg 默认设置小 0.7%），像素不变。单线程多用 3–19% 的时间；候选扫描的统计随 `Threads` 并行。
- `Animated()` 不再忽略无法保留的动画：GIF、WebP 或 PNG 动画输出为 GIF、WebP 以外的格式时，返回包装了 `errors.ErrUnsupported` 的错误，而不是写出第一帧。静态图像不受影响。`MaxFileSize` 与 `Animated()` 同用于动画时同样报错（此前被忽略）。
- `Threads(n)` 的上限由 3 改为 CPU 核数。自 4 线程起，缩放、模糊、锐化、色彩调整、旋转以及 JPEG、PNG、有损 WebP 编码将工作分摊至多个核。输出与线程数无关，逐字节一致。
- Go 1.27 下 PNG 默认压缩级别由 6（标准库的默认值）改为 7（Go 1.26 下仍为 6：该版本 7 级仅小 1–1.5%，耗时多一半）：Go 1.27 的 6 级查找重复数据的能力较弱，7 级在渐变、图标类图像上约小 5 倍，照片小 4–25%。
- PNG 按 256KB 分块压缩（与 pigz 的做法相同），因而可由多核分摊。图像数据不超过一块时，文件与此前版本相同。Go 1.27 及以上版本中，超过一块的数据在 7 级及以下改用 `klauspost/compress` 的 flate 压缩：它写出的字节与 Go 1.27 的 `compress/flate` 相同，复位时可换成每块自己的字典，而 `compress/flate` 的压缩器每块都得新建。2400 万像素的 PNG 分配 3MB（原为 276MB），峰值 9.2MB（原为 16.6MB；4 线程 15.0MB，原为 23.0MB），快 6%，字节不变。
- GIF 动画输出：所有帧共用一套按画面内容选取的调色板（`GIFOptions` 的 `Colors`、`NoDither` 对动画生效），自第二帧起仅存储变化的矩形区域。`GIFOptions{FixedPalette: true}` 仍使用固定调色板逐帧写出整帧。在处理任何一帧之前即检查像素上限。`FixedPalette` 下亦可使用 `NoDither`。
- 有损 WebP：按 libwebp 的空间噪声整形做分段量化。
- 有损 WebP：`WebPOptions.Effort` 也作用于有损输出（原先只用于无损）。默认力度 5 按 libwebp 最好设置的做法，用 sharp YUV 换算颜色、对 4×4 块做网格量化、按测量选取环路滤波强度；所有力度都做色度直流误差扩散；量化与率失真权重按 SSIMULACRA2 与 butteraugli 调定。14 张图上，同等 SSIMULACRA2 / butteraugli 下比 libwebp 默认设置小 6.6% / 9.5%，比其最好设置（`-m 6 -af -sharp_yuv`）小 1.3% / 2.2%。编码耗时为原来的 2–2.6 倍；`Effort: 1` 保持原来的速度，仍比 libwebp 默认设置小 6.1% / 4.8%。
- 带透明的有损 WebP：透明通道改由无损编码器以其默认力度编码，取「不滤波」与水平滤波中较小者（力度 7 起四种滤波都试），4 百万像素以内的平面用最优解析（力度 7 起不限大小），并按 libwebp 的做法让透明区域的颜色尽量省字节：一张置于柔和圆形窗口中的照片小 50%（质量 75 下 146KB → 73KB，libwebp 为 88KB），透明通道块最多小到 1/6（Tux 比 libwebp 小 2%）。编码透明通道时最多多用 8MB 内存；内存上限很低时透明通道块（而非像素）可能不同，与无损文件相同。
- 有损 WebP 解码结果与 libwebp（因而与浏览器）逐字节相同：色度在样本之间插值，并采用 libwebp 的整数换算（原先每个色度样本覆盖整个 2×2 块，颜色平均相差约半个色阶，色彩边缘处最大 20–40）。有损 WebP 解码后为 RGB；`Metadata()` 仍报告为 `ycbcr`。
- 无损 WebP 按穿过像素的最便宜路径选取 LZ77 匹配（libwebp TraceBackwards 的思路），截图上搜索重复更远，哈夫曼分组试多种块大小与组数：默认力度下 17 张图小 5.5%（截图 6–9%），耗时为原来的 3.8 倍（单核 42s 对 11s；libwebp 默认设置为 24s）；总量比 libwebp 最慢的设置小 5.3%（力度 9 时 5.7%，耗时 106s，对方 308s）。每个像素处除哈希链的匹配外，还把上方与左侧最近各距离上最长的匹配交给路径选择（一张渐变蒙版小 16%）；前缀码在划算时按 libwebp 的做法平滑计数，让码长适于游程编码。4 百万像素以内的图另以带 libwebp 交叉颜色变换的变体参与竞赛（带颜色渐变的图标与界面图小 2–9%），哈夫曼分组也在不用颜色缓存的情况下再试一次（一个图标小 4%），不带预测的变体还另试只用距离 1 和上一行的匹配（libwebp 的「RLE」引用；颜色少的图标小 3–4%）。由 JPEG 解码而来的 6000×4000 照片满是噪点，最优解析找不到多少可省的：文件大小不变，耗时由 4.1s 变为 5.1s，峰值由 22.2MB 变为最多 25.6MB。
- 无损 WebP：每个行带的预测器依据该行带开始时的代价模型选择，以便并行选择；输出文件略有差异。
- 命令行代码由 `cmd/goimgtools` 移至 `cli` 包；`cmd/goimgtools` 构建的仍为同一程序。

### 修复

- 以 NRGBA 保存的带透明图像（带 alpha 的 PNG）做 `Extend` 与 `FitContain` 时经过预乘的 8 位 RGBA，半透明像素的颜色会偏移（最多 255/alpha 级：alpha 为 55 时 4 级）；现在保持不变。
- 对分量采样方式特殊、解码器无法自行缩小的 JPEG（Go 测试文件 121121、211211、221122、222112）使用 `FastShrinkOnLoad` 时报错 “shrink: unsupported pixel type”；现在改为对解码结果再做平均。

- 大图像的压缩 TIFF 只有一个条带，压缩数据超过 50MB 时 libvips（因而 sharp）拒绝读取：现在像素数据超过 1MB 的压缩图像按约 1MB 一条分条带写入。
- 在把乘法与加法融合为一条指令的处理器上（ARM64，如 Apple 芯片与 AWS Graviton；POWER、s390x、RISC-V、LoongArch），部分结果可能与 x86 相差一级或一个像素：`Linear`、混合模式、带小数权重的 `Convolve`、GIF 与少色 PNG 的调色板、Mitchell 核、按比例定位的水印、`RotateBy` 的尺寸、`Blur` 的方框、文字描边，以及无损 WebP 与 jpegli 编码器所做的选择。现在每个乘积都先取整再相加（与 x86 相同），自动测试会检查编译器在这些处理器上没有融合任何一处。
- 操作中发生 panic 时，处理链的输入可能未被关闭，继续占用内存预算与临时文件。
- GIF 的 `Frames().LoopCount` 此前为其重复次数，比字段文档所述的播放次数少 1。

## 0.1.0（2026-11-14）

首个公开版本：在内存上限内按行读写 PNG、JPEG、WebP、GIF（含动画）、BMP、TIFF；缩放（6 种插值核、5 种适配模式）、裁剪、90 度倍数旋转、翻转、铺底色、图像叠加与水印、文字；JPEG 优化编码表与渐进式编码、PNG 减色、`MaxFileSize`；批量处理；扩展接口（`Pipe`、`Pixels`、`Apply`、`FromImage`、`FromSource`、`RegisterFormat`、`Stream`、`ToImage`，`pipeline` 包）；子命令 `info`、`check`、`compare`、`formats` 与 `inspect` 包；输入与输出像素上限、取消、panic 转为错误返回。
