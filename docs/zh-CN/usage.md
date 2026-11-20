[English](../usage.md) | 中文

# 用法

## 安装与基本用法

```bash
go get github.com/whhhh1500/goimgtools
```

```go
import "github.com/whhhh1500/goimgtools"
```

需要 Go 1.26 及以上版本（依赖的 `golang.org/x/image` 的最低要求）。Go 1.27 重写了 `compress/flate`，因此 Go 1.26 与 Go 1.27 写出的 PNG 文件字节不同，像素相同（8、9 级除外，它们用自己的压缩器）。编写扩展时另须引入 `github.com/whhhh1500/goimgtools/pipeline`。

API 采用与 [sharp](https://sharp.pixelplumbing.com/) 类似的链式调用：打开输入、追加操作、选择输出格式、写出。操作按调用顺序执行，错误由最后一步统一返回。

```go
err := goimgtools.Open("in.jpg").
    AutoOrient().                          // 按 EXIF 方向校正
    Resize(1536, 0).                       // 宽 1536，高按比例
    Flatten(color.White).                  // 透明区域铺白底
    Composite(goimgtools.Open("logo.png"), goimgtools.CompositeOptions{
        Gravity: goimgtools.SouthEast, Left: -24, Top: -24, // 右下角，边距 24 像素
    }).
    JPEG(goimgtools.JPEGOptions{Quality: 85}).
    ToFile("out.jpg")
```

### 输入

| 方法 | 说明 |
|---|---|
| `Open(path)` / `FromBytes(b)` / `FromReader(r)` | 文件、内存数据、流 |
| `Text(text, TextOptions{...})` | 将文字渲染为图像，可用作水印，也可单独输出（见[文字](#文字)） |
| `FromImage(img)` | 内存中已有的 `image.Image` |
| `FromSource(open)` | 调用方提供的像素流，如计算生成的图像、从其他来源读取的像素（见[扩展](extending.md)） |

### 操作

凡是本库能按 libvips（即 sharp）或 Pillow 的算法逐字节算出的操作，其选项都有 `Method`：`MethodDefault`（该操作自己的默认，见下文各项）、`MethodLibvips` 或 `MethodPillow`。`Resize`、`Blur`、`Sharpen`、`Rotate` 与 `Convolve` 都有。

| 方法 | 说明 |
|---|---|
| `Resize(w, h, ResizeOptions{...})` | 缩放。`w` 或 `h` 为 0 时按比例计算。默认方法下，16 位采样的图像（PNG、TIFF）缩放后仍为 16 位，数值与 `x/image` 的 `Scale` 写入 16 位目标图相同（`LinearLight` 以及 Pillow、libvips 两种方法按 8 位处理，不在此列） |
| `Crop(left, top, w, h)` | 裁剪 |
| `AutoOrient()` | 按 EXIF 方向自动校正（无方向信息时不作处理） |
| `Rotate(degrees, RotateOptions{Background, Method, Interpolator, Expand})` | 顺时针旋转任意角度（负值为逆时针）。90 的倍数为精确的直角或半周翻转，不插值。其他角度时画布为旋转后图像的外接框，四角以 `Background` 填充（`nil` 为黑色，`color.Transparent` 为透明）；默认双线性插值（`Interpolator` 可选最近邻或双三次）。与 sharp 的 `rotate` 逐字节一致，含透明与不含透明的图像均如此。`Method: MethodPillow` 时即 Pillow 的 `Image.rotate(-degrees)`，逐字节一致：除非 `Expand`，画布保持原尺寸，四角为 `Background`（`nil` 为透明，不含透明的图像为黑色）；Pillow 自身默认的插值为最近邻。按名字调用 `rotate DEGREES[,BACKGROUND][,pillow][,nearest\|bilinear\|bicubic][,expand]` |
| `Affine(a, b, c, d, AffineOptions{Background, InputX, InputY, OutputX, OutputY, Interpolator})` | 将每个点 (x, y) 移到 (a·x + b·y, c·x + d·y)：可一次完成旋转、镜像、倾斜与拉伸。画布为移动后图像的外接框；各偏移即 sharp 的 `idx`、`idy`、`odx`、`ody`。使用 `InterpolateBilinear`（默认）、`InterpolateNearest`、`InterpolateBicubic` 时与 sharp 的 `affine` 逐字节一致，含透明与不含透明的图像均如此 |
| `Transform(width, height, t, TransformOptions{Background, Interpolator})` | 即 Pillow 的 `Image.transform((width, height), method, data)`，逐字节一致：输出的每个像素取 `t` 把其中心映射到的图像上那一点的颜色。`TransformAffine(a, b, c, d, e, f)`：(x, y) 映射到 (a·x + b·y + c, d·x + e·y + f)，矩阵从输出到图像；`TransformExtent(x0, y0, x1, y1)`：把图像的这一矩形拉伸铺满输出；`TransformPerspective(a, …, h)`：透视变换，可把拍斜的页面摆正；`TransformQuad(x0, y0, …, y3)`：把以这四点为角（依次对应输出的左上、左下、右下、右上）的四边形按双线性铺到输出上；`TransformPerspectiveQuad(x0, y0, …, y3)`：同样的四个角，按由它们算出的真正透视变换，给出拍斜页面的四角即可把它摆正（像素与 Pillow 使用其手册中求出的系数时相同）。落在图像外的点取 `Background`（`nil` 为透明，不含透明的图像为黑色）。默认双线性插值（Pillow 自身默认最近邻）。按名字调用 `transform WIDTH,HEIGHT,affine\|extent\|perspective\|quad\|perspective-quad,NUMBERS…[,nearest\|bilinear\|bicubic][,BACKGROUND]` |
| `Flip()` / `Flop()` | 上下镜像 / 左右镜像 |
| `Trim(threshold, TrimOptions{Background, LineArt, Margin})` | 去除四周与左上角像素（或 `Background`）同色的边（红、绿、蓝、透明任一通道之差超过 `threshold` 才视为不同；sharp 的默认值 10 可消除 JPEG 噪点）。与 sharp 的 `trim`（libvips 的 `find_trim`）一样，先对图像做 3×3 中值滤波再找边，使边上的灰尘斑点与 JPEG 瑕疵不妨碍裁剪（保留的像素不受滤波影响）；`LineArt` 直接比较原像素，适用于由单像素细线构成的线稿（中值滤波会把细线滤掉）。`Margin` 在找到的范围外保留相应像素。像素相同时裁剪范围与 sharp 完全一致 |
| `Extend(top, right, bottom, left, ExtendOptions{Background, Mode})` | 四周扩边，宽度按 CSS 的上、右、下、左顺序给出。默认以颜色 `Background` 填充（`nil` 为黑色，`color.Transparent` 为透明）；`Mode` 改用图像本身扩边：`ExtendCopy` 重复边缘像素，`ExtendRepeat` 平铺重复图像，`ExtendMirror` 镜像（边缘像素出现两次），边框比图像宽时继续重复或镜像。与 sharp 带 `extendWith` 的 `extend` 逐像素一致。顶部或底部边框需要重复或镜像时，图像存入工作存储 |
| `Grayscale(GrayscaleOptions{Linear})` | 灰度化：Rec. 601 亮度四舍五入至最近的灰阶，与 Pillow 的 `convert("L")` 逐字节一致（JPEG 亦然，其解码结果与 Pillow 相同）。`Linear` 改用线性光的亮度（Rec. 709 权重），与 sharp 的 `greyscale` 逐字节一致：红色与蓝色更暗、绿色更亮。不透明图像输出为单通道灰度文件 |
| `Modulate(brightness, saturation, hue, ModulateOptions{Lightness})` | 在 LCh 色彩空间中将亮度、饱和度乘以系数，并将色相旋转指定度数（与 sharp 的做法相同）；`Lightness` 随后加到明度上（L\*，0–100），即 sharp 的 `lightness`。每个像素与 sharp 的结果相差不超过 1：`Modulate(1.2, 0.8, 0)` 使亮度提高 20%、色彩略淡 |
| `Negate(NegateOptions{Alpha})` | 反色：每个颜色取其相反值（255 − 色阶），透明度不变；指定 `Alpha` 时透明度也取反。与 sharp 的 `negate({alpha: false})` 及 `negate()` 逐字节一致 |
| `Linear(a, b)` | 每个红、绿、蓝色阶 v 变为 a·v + b，舍去小数并限制在 0–255，与 sharp 的 `linear` 逐字节一致：`Linear(1.2, -25)` 提高对比度 |
| `Curves(points, CurvesOptions{Red, Green, Blue})` | 按色调曲线映射红、绿、蓝各级，同图像编辑器的“曲线”：经过 `points`（`{输入, 输出}`，0–255）的平滑曲线，单调三次样条（Fritsch–Carlson：不过冲），首尾点之外保持水平；`Red`、`Green`、`Blue` 为单个通道的曲线，在其后应用。透明度保持不变。按名字调用 `curves X:Y...[,red=X:Y/X:Y...]` |
| `Gamma(g)` | 每个色阶 v 变为 255·(v/255)^(1/g)，与 ImageMagick 的 `-gamma` 相同：大于 1 提亮中间调，小于 1 压暗；黑白不变 |
| `Threshold(level, ThresholdOptions{PerChannel})` | 黑白二值：灰度（按线性光计算，与 sharp 相同）不小于 `level` 的像素变白，其余变黑。不含透明的图像与 sharp 的 `threshold` 逐字节一致，并输出为单通道文件。`PerChannel` 让红、绿、蓝各自取 0 或 255（即 sharp 的 `threshold(level, {greyscale: false})`，逐字节一致） |
| `Tint(color)` | 着色：按每个像素的亮度赋予该颜色，越接近黑白越淡（sharp 的 `tint`，与其相差不超过 1）：`Tint(color.RGBA{112, 66, 20, 255})` 为褐色调 |
| `Normalize(NormalizeOptions{Lower, Upper})` | 拉伸明度（L*a*b* 中的 L*），使最暗的 `Lower`% 变黑、最亮的 `100-Upper`% 变白（默认 1 与 99），保持色相与饱和度；即 sharp 的 `normalise`，相差不超过 1（百分位与其完全相同）。需先读完整幅图像并保存在工作区 |
| `Filter(f)` | Pillow 的内置滤镜，同 `im.filter(f)`，逐字节一致：`FilterBlur`、`FilterContour`、`FilterDetail`、`FilterEdgeEnhance`、`FilterEdgeEnhanceMore`、`FilterEmboss`、`FilterFindEdges`、`FilterSharpen`、`FilterSmooth`、`FilterSmoothMore`。`Convolve` 设 `Method: MethodPillow` 即 `ImageFilter.Kernel`（3×3 或 5×5）：用 Pillow 的运算，核上下颠倒，边缘像素保留，带透明的图像与 Pillow 一样连 alpha 一起滤 |
| `Enhance(e, factor)` | Pillow 的 `ImageEnhance`，逐字节一致（torchvision 的 `ColorJitter` 与 `adjust_*` 对图像正是用它）：按 `factor` 调整 `EnhanceColor`（饱和度）、`EnhanceContrast`、`EnhanceBrightness` 或 `EnhanceSharpness`，可为任意数，1 不变：每个色阶从退化图像（灰色；平均明度的灰；黑色；经 Pillow `SMOOTH` 平滑的图像）向原图走 `factor` 倍的距离。`EnhanceContrast` 需先读完整幅图像以求平均明度 |
| `AutoContrast(AutoContrastOptions{CutoffLow, CutoffHigh, Ignore, PreserveTone})` | 把红、绿、蓝各自拉伸到最暗为黑、最亮为白，两端各去掉 `CutoffLow`、`CutoffHigh` 百分比的像素，不计 `Ignore` 中的色阶；`PreserveTone` 按明度拉伸、三个通道共用一张表，颜色不偏。即 Pillow 的 `ImageOps.autocontrast`，逐字节一致。需先读完整幅图像 |
| `Equalize()` | 均衡各通道的色阶，使其出现次数大致相同：Pillow 的 `ImageOps.equalize`，逐字节一致。需先读完整幅图像 |
| `Posterize(bits)` / `Solarize(threshold)` | 保留每个色阶最高的 `bits` 位（1–8）/ 把不小于 `threshold`（0–256；Pillow 默认 128）的色阶反转：Pillow 的 `ImageOps.posterize` 与 `solarize`，逐字节一致 |
| `CLAHE(CLAHEOptions{Width, Height, MaxSlope})` | 局部对比度增强（限制对比度的自适应直方图均衡），提亮暗部、压低亮部以显出细节：每个色阶变为其在像素周围 `Width` × `Height` 窗口中的排位，窗口中每个色阶最多计 `MaxSlope` 个像素（默认 3）。不含透明的图像与 sharp 的 `clahe` 逐字节一致；24 MP、64×64 窗口单核 18s，4 核 7s（sharp：7.6s、8.7s） |
| `Median(size)` | 中值滤波：每个像素的每个通道取其周围 size × size 方块（奇数，3–99）的中值，去除噪点而保留边缘。与 Pillow 的 `MedianFilter` 及 sharp 的 `median` 逐字节一致 |
| `Rank(size, rank)` | 排序滤波：每个像素的每个通道取 size × size 方块（奇数，1–99）中第 rank 小的值（从 0 起）。rank 为 0 即 Pillow 的 `MinFilter`（暗部扩张），size×size−1 即其 `MaxFilter`（亮部扩张）；与 Pillow 的 `RankFilter` 逐字节一致 |
| `Dilate(width)`、`Erode(width)` | 与 sharp 的 `dilate`、`erode` 逐字节一致：每个通道取 2·width+1 像素方块（1–65536）的按位与（dilate）或按位或（erode）。在黑白图像（如 `Threshold` 的结果）上，dilate 让黑色图形（白底上的文字）扩张 width 像素，erode 让其收缩；其他图像请用 `Rank`。耗时几乎不随 width 增长 |
| `Convolve(Convolution{Width, Height, Weights, Scale, Offset})` | 自定义卷积核（边长为奇数，最大 99）：窗口加权和除以 `Scale`（默认为权重之和）再加 `Offset`。与 sharp 的 `convolve` 逐字节一致；例如权重 `0,-1,0,-1,5,-1,0,-1,0` 为锐化，`-1,-1,-1,-1,8,-1,-1,-1,-1` 配合 `Scale: 1` 为边缘检测 |
| `Blur(sigma, BlurOptions{Method, MinAmplitude})` | 高斯模糊，sigma 须大于 0 且不超过 1000 像素。默认（`MethodPillow`）为 Pillow 的 `GaussianBlur`：三次方框模糊，不含透明的图像与 Pillow 逐字节一致。`MethodLibvips` 与 sharp 的 `blur` 逐字节一致（含透明图像）：直接用高斯核，权重为 libvips 的整数权重，在低于峰值的 `MinAmplitude` 处截断（默认 0.2，即 sharp 的 `minAmplitude`，因此同一 sigma 下比 Pillow 模糊得轻）。两种方式的耗时都几乎不随 sigma 变化 |
| `BoxBlur(radius)` | 方框模糊：每个像素取其周围边长 2×radius+1 的正方形的均值（radius 可为小数，边缘像素按比例计入），0 至 1000；即 Pillow 的 `ImageFilter.BoxBlur`，不含透明的图像逐字节一致。按名字调用 `boxblur RADIUS` |
| `Sharpen()` | 轻度锐化（sharp 的 3×3 卷积核），适用于缩小之后 |
| `Sharpen(SharpenOptions{Sigma, Amount, Threshold})` | 反锐化掩模（Pillow 的 `UnsharpMask`），`Amount` 默认为 1.5 |
| `Sharpen(SharpenOptions{Sigma, Method: MethodLibvips, Curve})` | sharp 带 sigma 的 `sharpen`：只锐化明度（L\*a\*b\* 的 L\*），颜色不偏移；`Curve`（`SharpenCurve{Flat, Jagged, JaggedFrom, MaxBrighten, MaxDarken}`，即 sharp 的 m1、m2、x1、y2、y3；nil 为 sharp 的默认值）把与模糊的差映射为改变量。与 sharp 的结果仅在少数像素上相差 1 级 |
| `Flatten(color)` | 去除透明，铺底色，与 sharp 的 `flatten`（libvips）逐字节相同；带透明的图像输出为 JPEG 时同样按此铺黑底，与 sharp 写出的相同 |
| `Recomb(matrix)` | 按矩阵重组通道：3 行 3 列（或 4 行 4 列，第四行生成透明度），每行给出结果的一个通道，即各通道值乘以该行各数之和，舍去小数。与 sharp 的 `recomb` 逐字节一致；`{{0.393, 0.769, 0.189}, {0.349, 0.686, 0.168}, {0.272, 0.534, 0.131}}` 为褐色调 |
| `Unflatten()` | 白色像素变为透明（灰度按 sharp 的方式计算为 255 的像素）；在 `Threshold` 之后使用，则达到阈值的像素全部变透明。与 sharp 的 `unflatten` 逐字节一致 |
| `ExtractChannel(c)` | 取出一个通道作为灰度图像：`ChannelRed`、`ChannelGreen`、`ChannelBlue` 或 `ChannelAlpha`（sharp 的 `extractChannel`） |
| `EnsureAlpha(alpha)` | 为不含透明的图像加上给定不透明度（0–1）的透明通道（值为 alpha·255 舍去小数，与 sharp 的 `ensureAlpha` 逐字节一致）；已含透明的图像保持不变 |
| `RemoveAlpha()` | 去掉透明度：每个像素显示其存储的颜色（sharp 的 `removeAlpha`）；与 `Flatten` 不同，不与背景色混合 |
| `Composite(overlay, CompositeOptions{...})` | 图像叠加 / 水印（见[水印](#水印)） |
| `InvisibleWatermark(mark, InvisibleWatermarkOptions{Kind, Key, Strength, Density})` | 隐形地藏入 1–32 个字节，用 `inspect.ReadWatermark` 与密钥读回，不需要原图；`WatermarkBlind` 经得起对文件的处理，`WatermarkScreen` 还经得起对着屏幕拍照（见[隐形水印](watermark.md)） |
| `Animated()` | 处理 GIF、WebP 或 PNG 动画（APNG）的每一帧并输出 GIF、WebP 或 PNG 动画（APNG）。WebP 的各帧按 libwebp 动画解码器的方式绘制（混合与否、清除与否），逐像素一致；APNG 的各帧按其规范绘制。静态图像不受影响；无法保留的动画（输出为其他格式）返回包装了 `errors.ErrUnsupported` 的错误。WebP 第一帧完整存储，之后各帧仅存储变化的矩形区域。GIF 的所有帧共用一套按画面内容选取的调色板（`GIFOptions` 的 `Colors`、`NoDither` 依然有效；颜色数不超过该值时原样保留），自第二帧起仅存储变化的部分（见[压缩](compression.md)）。APNG 为无损：所有帧共不超过 256 种颜色时共用一套调色板，否则为 RGB 或 RGBA；之后各帧存储与其底图不同的矩形（前一帧保留、清除或撤销，按 apngopt 的方法选择），原样写入或把未变像素设为透明后叠加，取最小者；`CompressionLevel` 有效，`Colors` 不适用（报错） |
| `Pixels(fn)` | 自定义逐像素操作：`fn` 逐条带接收 8 位 NRGBA 数据并原地修改（见[扩展](extending.md)） |
| `Pipe(op)` | 在链的当前位置插入任意自定义操作（见[扩展](extending.md)） |
| `Apply(steps...)` | 将一组步骤写为函数，在链的中间调用 |

`ResizeOptions`：

- `Fit`：`FitInside`（默认，等比缩放至框内，不裁剪、不变形）、`FitCover`（填满框后居中裁去多余部分，即 sharp 的默认方式）、`FitContain`（等比缩放至框内，再以 `Background` 补边至恰为 w×h，默认黑色）、`FitOutside`、`FitFill`（拉伸）。
- `WithoutEnlargement`：禁止放大。
- `Position`：`FitCover` 保留的部分，以及 `FitContain` 放置图像的位置：默认居中，也可为 `NorthWest` … `SouthEast` 九个方位之一（sharp 的 `position`；居中裁剪的取整方式与 sharp 相同）。
- `LinearLight`：在线性光下缩放（同 libvips `thumbnail` 的 `linear`）：先把色阶转为线性光再求平均，之后重新编码，因此缩小图像时细小的亮部细节（细线、文字、星空）保持亮度而不会变暗，颜色也按光的方式混合。黑白相间的像素缩小一半后为 188 级（白色一半的光），而不是 128 级。把 2400 万像素的 PNG 缩小到 1500×1000 用时 0.74 秒（不开启为 0.64 秒）。不能与 `MethodLibvips` 同用。
- `Interesting`：用于 `FitCover`，按画面内容选择保留的部分，即 sharp 的 `strategy.entropy` / `strategy.attention`（libvips 的 smartcrop）：`InterestingEntropy` 保留细节最多的部分（像素相同时与 libvips 的裁剪完全一致），`InterestingAttention` 保留最引人注意的部分（边缘、肤色与饱和的红色，在按 libvips 方式缩小到 32×32 的图像上评分）。选择期间缩放后的图像保存在内存中。
- `Kernel`：`KernelCubic`（默认，Catmull-Rom）、`KernelNearest`、`KernelLinear`、`KernelMitchell`、`KernelLanczos2`、`KernelLanczos3`、`KernelMKS2013`、`KernelMKS2021`（Magic Kernel Sharp，即 sharp 的 `mks2013`、`mks2021`）；`KernelBox`、`KernelHamming`（Pillow 的 `BOX` 与 `HAMMING`）。
- `CustomKernel`：自定义缩放核（`&goimgtools.CustomKernel{Support, At}`），设置后取代 `Kernel`。
- `FastShrinkOnLoad`：JPEG 解码时先行缩小（见 [JPEG 解码时缩小](performance.md#jpeg-解码时缩小可选)）；无 alpha 的有损 WebP 亦然；配合 `MethodLibvips` 时所有 WebP 都同 sharp 一样在解码时缩小。
- `Method`：像素的计算方式。`MethodDefault`（默认）为每个像素在其自身位置以双精度加权源像素，同 `golang.org/x/image/draw` 的 `Scale`。`MethodLibvips` 即 sharp 的 `resize`，逐字节一致：按 sharp 的做法对颜色先以 8 位乘上 alpha，再走 libvips 的 `vips_resize`，支持所有 `Kernel`（sharp 默认 `KernelLanczos3`）、`Fit`、`Position`、`Background` 与 `WithoutEnlargement`。与 sharp 一样，写在 `Resize` 之前的转向（`Rotate`、`Flip`、`Flop`）先做，EXIF 转向（`AutoOrient()`）在缩放之后做；JPEG 按 sharp 选择的倍数在解码时缩小，用 libjpeg 的缩放解码：`FastShrinkOnLoad` 即 sharp 的 `fastShrinkOnLoad`（sharp 默认开启；关闭时至少留 2 倍给缩放本身）。开启时 WebP 也在解码时缩放，同 sharp 让 libwebp 所做：按两个方向缩小倍数中较小的一个，每边四舍五入；有损图像的各平面各自从自身尺寸缩放（两边都缩到四分之三以下时跳过环路滤波），无损图像先把颜色乘上 alpha 再缩放；余下的由缩放完成（WebP 解码出来已是所要尺寸时不再缩放）。有损 WebP 缩到三分之一，耗时不到原来的一半。不能与 `LinearLight` 同用。耗时与 `MethodDefault` 相当（2400 万像素 PNG 缩到 1500×1000，单核 0.62 秒对 0.65 秒）。`MethodPillow` 即 Pillow 的 `Image.resize`，逐字节一致：先横向再纵向，权重为 22 位定点，带透明的图像先把颜色乘上 alpha 再缩放（Pillow 的 `RGBa`）；`KernelCubic`（默认，与 Pillow 相同）即 `BICUBIC`，`KernelLanczos3` 即 `LANCZOS`，`KernelLinear` 即 `BILINEAR`，`KernelBox` 即 `BOX`，`KernelHamming` 即 `HAMMING`，`KernelNearest` 即 `NEAREST`。转向在缩放之前做；配合 `FastShrinkOnLoad` 时，JPEG 按 Pillow 在 `resize(size)` 之前调用 `draft(mode, size)` 的方式在解码时缩小（libjpeg 的缩放解码）。Pillow 对调色板图像总用 `NEAREST`，本库则按所选核缩放其颜色。用 `MethodPillow` 时各种 fit 即 Pillow 的 `ImageOps`：`FitInside` 为 `contain`，`FitOutside` 为 `cover`，`FitContain` 为 `pad`（居中方式取自 `Position`，颜色取自 `Background`），`FitCover` 为 `fit`（取图像中与框同宽高比的部分，按 `Position` 定位，直接缩放到框）；配合 `ReducingGap` 时 `FitInside` 的尺寸按 `Image.thumbnail` 计算（每边取下取整或上取整中更接近原宽高比的一个）。
- `ReducingGap`（配合 `MethodPillow`）：Pillow 的 `reducing_gap`。缩小超过该倍数的不透明图像先按整数倍做方框平均（`Image.reduce`），至少留该倍数给缩放本身；配合 `FastShrinkOnLoad` 时 JPEG 按框的该倍数在解码时缩小。`Resize(w, h, ResizeOptions{Method: MethodPillow, ReducingGap: 2, WithoutEnlargement: true, FastShrinkOnLoad: true})` 即 `im.thumbnail((w, h))`，逐字节一致。

灰度图像缩放后仍为灰度图像（单通道），不会转为彩色。

模糊、锐化、色彩调整与扩边均按行流式处理，仅额外占用少量行的内存（模糊约多占 6×sigma 行，`MethodLibvips` 约 3.6×sigma 行，反锐化掩模再多占 3×sigma 行；超出内存份额时写入临时文件）。`Trim` 与 `Rotate` 须先读取全图：像素先写入暂存区（超出内存份额时为临时文件），再重新读取；因此链中含 `Trim` 时，`OutputSize` 也须读取一遍像素。带透明通道的图像在模糊、锐化时按预乘透明度计算，透明区域的颜色不会渗入相邻像素。

### 输出

| 方法 | 说明 |
|---|---|
| `JPEG(JPEGOptions{Quality, Optimize, Progressive, Trellis, QuantTable, ChromaSubsampling})` | 质量 `Quality` 为 1–100，0 表示 80。`Optimize`、`Progressive` 在像素不变的前提下减小文件；`Trellis`、`QuantTable`（0–8，编号同 mozjpeg 的 `-quant-table`；或 `QuantTableJpegli`）会改变像素（见[压缩](compression.md)）。`ChromaSubsampling`：`Chroma420`（默认：色度为宽高各一半，同标准库与 libjpeg）、`Chroma422`（宽度一半）或 `Chroma444`（保留全部色度：彩色文字与边缘清晰，文件约大 30%），即 sharp 的 `chromaSubsampling` |
| `PNG(PNGOptions{CompressionLevel, Colors, NoDither})` | 压缩级别 1（最快）至 10（最小；8、9 级搜索最小文件，单核耗时分别为 7 级的 5–20 倍和 18–65 倍；10 级用 Zopfli 压缩，8 线程时耗时约为 9 级的 22 倍）；`Colors` 将颜色减至最多 256 色（见[压缩](compression.md)） |
| `WebP(WebPOptions{Quality, Lossless, Effort})` | 有损（质量 1–100，默认 75）或无损；`Effort` 1 最快至 9 最小，默认 5（有损时 5 已用上除透明通道外的全部手段，2 耗时约减半，1 约为三分之一） |
| `GIF(GIFOptions{Colors, NoDither, FixedPalette})` | 最多 256 色，按图像内容选取；`FixedPalette` 改用标准库的固定调色板 |
| `BMP()` | |
| `TIFF(TIFFOptions{Compression, Predictor})` | 不压缩、`TIFFDeflate` 或 `TIFFLZW`（libtiff 的写法）；`Predictor` 把每个样本存为与左侧像素之差（水平差分），照片压缩效果好得多（即 sharp 的 `predictor: "horizontal"`；一张 2400 万像素照片：LZW 82 → 52MB，Deflate 67 → 45MB）。像素数据超过 1MB 的压缩图像按约 1MB 一条分条带写入 |
| `ToFormat(name, 选项)` | 按名称选择输出格式（`"jpeg"`、`"png"`、`".tif"`……，以及已注册的格式）；选项为该格式的选项结构体，可省略 |
| `MaxFileSize(n)` | 输出不超过 n 字节：自动选用满足该限制的最高质量（JPEG、有损 WebP，以及提供 `Quality` 的注册格式）；PNG 与 GIF 选用能满足的最多颜色数（PNG 先试无损）。与 `Animated()` 同用于动画时返回包装了 `errors.ErrUnsupported` 的错误；单帧 GIF 按静态图像处理 |
| `ToFile(path)` / `ToWriter(w)` / `ToBytes()` | 执行并写出。`ToFile` 未指定格式时按扩展名确定；仅当全程成功时才生成文件 |
| `ToTiles(path, TileOptions{...})` | 把图像写成瓦片金字塔，供可缩放查看器使用，与 sharp 的 `tile`（libvips 的 `dzsave`）相同，JPEG 瓦片及描述文件逐字节一致（每一级由上一级每四个像素取平均缩小，奇数的末行或末列重复一次；带透明时颜色按 alpha 加权）。`Layout`：`TileDeepZoom`（默认；OpenSeadragon：`path.dzi` 与 `path_files/级别/列_行.jpeg`）、`TileGoogle`（Leaflet、OpenLayers：`path/级别/行/列.jpg`，画布由 `Background`（默认白色）铺成整块瓦片，图像放在左上角或按 `Center` 居中，只有背景的瓦片不写，由 `path/blank.png` 代替）、`TileIIIF` 与 `TileIIIF3`（IIIF Image API 0 级，`path/info.json`，地址取 `ID`）、`TileZoomify`（`path/ImageProperties.xml`、`path/TileGroupN/级别-列-行.jpg`）。瓦片边长 `Size`（256）；`Overlap` 为与相邻瓦片共享的像素（0）；`Depth` 缩到一个像素、一块瓦片或只一级（`TileDepthOnePixel`、`TileDepthOneTile`、`TileDepthOne`；默认取布局的设定）；`SkipBlanks` 不写与背景相差不超过该值的瓦片（Google 默认 5）。格式取链上的输出格式，默认 JPEG 质量 80、优化霍夫曼表（同 sharp）。流式处理：每级只保留一行瓦片所需的像素，同一行瓦片交给辅助线程并行编码。`ToFile("x.dzi")`（或 `.dz`）即按默认设置的 `ToTiles`。路径以 `.zip` 结尾时写入 ZIP 压缩包，与 sharp 相同（按 libvips 的方式不压缩存储；每个文件与 sharp 相同，顺序不随线程变化，libvips 的顺序则随线程变化）；`ToFile("x.zip")` 亦然。灰度图像得到灰度瓦片（sharp 为 RGB） |
| `ToTilesWriter(w, name, TileOptions{...})` | 把 `ToTiles` 的瓦片金字塔作为 ZIP 压缩包写入 `w`（sharp 的 `tile` 输出到缓冲区），图像在包内名为 `name`：可直接作为 Web 服务的响应或上传到云存储。每次只保留一行瓦片 |
| `Metadata()` | 仅读取文件头，描述**输入**：格式、存储的宽高、颜色类型（灰度 / 调色板 / RGB / YCbCr / CMYK）与位深、是否含透明、EXIF 方向、是否含 ICC / EXIF，以及分辨率（`Density`，每英寸像素数：有 EXIF 分辨率时取 EXIF 的，否则取格式自身的；25.4 及以下视为未记录，因为 libvips 在不知道分辨率时写入该值，与 sharp 相同）。不受链上操作影响 |
| `Frames()` | 输入的帧数、单次播放时长、循环次数（仅 GIF 可多于一帧）。须读取整个文件，但不解码像素 |
| `OutputSize()` | **结果**的尺寸：链上全部操作完成后的宽高，不解码像素 |
| `ToImage()` | 执行并返回完整的 `image.Image`（全图驻留内存，计入内存上限） |
| `Stream(env)` | 执行并返回结果的像素流，不写文件（见[扩展](extending.md)） |
| `Formats()` / `FormatByName(s)` | 返回支持的格式及其扩展名；按名称或扩展名查找（`"jpeg"`、`"JPG"`、`".tif"` 均可识别） |
| `RegisterFormat(codec)` / `CodecByName(s)` / `UnregisterFormat(name)` | 新增格式，或取出某一格式的读写函数加以改写；撤销注册后恢复内置实现（见[扩展](extending.md)） |

未指定输出格式且无扩展名可依据时，输出格式与输入相同；文字输入默认输出 PNG。

### 元数据与资源控制

| 方法 | 说明 |
|---|---|
| `KeepExif()` | 将输入的 EXIF（相机、时间、GPS）保留至输出。默认丢弃 |
| `KeepXmp()` | 将输入的 XMP 数据包（Adobe 的元数据：关键词、评级、版权、编辑历史）保留至 JPEG、PNG、WebP、TIFF 输出，即 sharp 的 `keepXmp`。默认丢弃 |
| `StripICC()` | 丢弃 ICC 色彩配置。默认保留，否则广色域照片会出现偏色 |
| `Density(dpi)` | 在输出中记录每英寸 `dpi` 个像素的分辨率（1–65535）：JPEG 的 JFIF 段、PNG 的 pHYs 块、TIFF 的分辨率标签、BMP 的文件头，以及保留的 EXIF 块中的分辨率字段（WebP 与 GIF 没有专门的位置）。默认保留输入的分辨率，缩放后也保留，与 libvips 和 sharp 相同；`Density(0)` 表示不记录 |
| `ToSRGB()` | 依据输入的色彩配置（原色与色调曲线，或查找表）将颜色转换至 sRGB，输出不再附带配置：在忽略色彩配置的程序中颜色同样正确，文件也略小。转换为 GIF、BMP（无法附带配置）时自动执行。CMYK 图像（JPEG、TIFF）解码时总会经其色彩配置转为 sRGB，与 sharp 相同；不带配置的按 libvips 的 CMYK 配置转换，同 sharp |
| `MemoryLimit(bytes)` | 工作内存上限，默认 64MB。默认级别的 PNG 输出，其压缩器约占其中 2MB（小图更少），8、9 级分别占 3MB 与 5.6MB，10 级每个压缩线程约 51MB（默认上限下只用一个线程压缩），6 级及以下很少 |
| `Threads(n)` | 单次处理最多使用的核数，默认 1，最大为 CPU 核数（见 [CPU 与线程](performance.md#cpu-与线程)） |
| `SetDefaultThreads(n)` | 全程序的默认线程数（供未调用 `Threads` 的处理使用），无需在每个调用处重复设置 |
| `TempDir(dir)` | 临时文件目录，默认为系统临时目录 |
| `LimitInputPixels(n)` | 输入像素数上限，默认 16383×16383（同 sharp）。对使用 `Animated()` 的 GIF 或 WebP 动画，按所有帧的像素总和计算 |
| `LimitOutputPixels(n)` | 链上任一步骤所生成图像（因而也包括结果）的像素数上限，默认值同上。用于防止小文件被放大为占满磁盘、耗时数小时的结果 |
| `WithContext(ctx)` | `ctx` 取消或超时后中止处理，返回 `ctx.Err()` |
| `Clone()` | 复制配方，用于从同一起点派生多个结果 |

ICC 色彩配置支持在 JPEG、PNG、WebP、TIFF 中读写；EXIF 支持在 JPEG、PNG、WebP、TIFF 中读写（TIFF 把它放在第一个目录及其标签指向的 EXIF、GPS 目录中：描述图像的标签从那里读出，含方向，也写到那里）；XMP 支持在 JPEG（不超过 64KB 的一个 APP1 段；更大数据包的扩展 XMP 不保留）、PNG（从 iTXt、zTXt 或 tEXt 读取，写为 iTXt）、WebP、TIFF 中读写。GIF、BMP 无法附带色彩配置，转换为这两种格式时依据输入的配置将颜色转换至 sRGB（实测：Display P3 图像不经转换时色差最大为 17.6，肉眼可见偏淡）。`AutoOrient()` 校正方向后，保留的 EXIF 中的方向改为「正向」。

转换支持以三原色加色调曲线描述的配置（相机、手机、Display P3、Adobe RGB、ProPhoto 均属此类），每个值与 LittleCMS（相对比色，超出 sRGB 的颜色截断）相差不超过 1；查找表类配置（少见，多用于打印机）原样保留，不作转换。

## 服务端使用

**一个配方，多个结果。** 链上的方法修改的是同一个 `Image` 并返回其自身，不作复制。如需从同一起点生成多个结果，应先调用 `Clone()`：

```go
base := goimgtools.Open("in.jpg").AutoOrient()
thumb := base.Clone().Resize(200, 0)
large := base.Clone().Resize(1600, 0)
```

已构建的配方可重复执行，也可由多个协程同时执行（`FromReader` 的输入只能读取一次）。

**错误分类。** 使用 `errors.Is` 判断：

| 错误 | 含义 | 服务端的常见处理 |
|---|---|---|
| `ErrFormat` | 输入不是可读取的图像：其他类型的文件、不支持的格式或变体、数据损坏或截断 | 上传的文件有误 |
| `ErrPixelLimit` | 输入像素数超过 `LimitInputPixels`（动画按所有帧合计），或链上某一步骤的结果超过 `LimitOutputPixels` | 图像过大，或请求的尺寸过大 |
| `ErrMemoryLimit` | 无法在内存上限内完成处理 | 调高 `MemoryLimit` 或拒绝请求 |
| `ErrFileSize` | 即使采用最低质量（PNG、GIF 为两种颜色）也无法达到 `MaxFileSize` 的目标 | 先缩小尺寸 |
| `context.Canceled` / `DeadlineExceeded` | `WithContext` 的上下文已结束 | 请求已取消 |
| `*PanicError`（使用 `errors.As`） | 处理该输入时库或扩展代码中的缺陷（panic），附带原因与调用栈 | 记录并上报；本次请求失败，程序继续运行 |
| 其他 | 读取输入失败、磁盘已满、参数错误等，保留原始错误 | 系统故障或调用方错误 |

判定规则：仅当读取输入本身未出错，且不属于内存或临时文件问题时，解码器报告的错误才归为 `ErrFormat`。测试中已使用经截断、损坏的真实图像逐一核对：凡标准解码器报错之处，本库均报告 `ErrFormat`；连接中途断开时返回连接自身的错误。

**取消。** `WithContext(ctx)` 在解码与编码过程中于每两个条带（数行像素）之间检查一次，取消后迅速返回，并删除未写完的文件。

**输出文件的访问权限。** `ToFile` 先写入同一目录下的隐藏文件，全部成功后再重命名，因此不会残留未写完的结果。该文件按普通新建文件的权限创建（Linux 上通常为 `0644`，由进程的 umask 决定），替换已有文件时沿用原文件的权限；不采用临时文件所用的仅属主可读的 `0600` 权限，否则以其他用户身份运行的 Web 服务器将无法读取处理后的图像。

**并发与内存总量。** 每次执行拥有独立的内存预算，互不影响；库本身不限制同时执行的数量。同时处理 N 幅图像时，内存与 CPU 的总用量约为单幅的 N 倍（单幅通常为 6–15MB，最多为 `MemoryLimit`）。如需限制总量，可使用信号量控制并发数：

```go
var slots = make(chan struct{}, 4) // 同时最多处理 4 幅

func handle(ctx context.Context, in io.Reader, out io.Writer) error {
    select {
    case slots <- struct{}{}:
        defer func() { <-slots }()
    case <-ctx.Done():
        return ctx.Err()
    }
    return goimgtools.FromReader(in).WithContext(ctx).Resize(1600, 1600).JPEG().ToWriter(out)
}
```

实测（批量模式，42 张照片）：并发 3 时峰值为 19.5MB，并发 8 时为 32MB，42 张同时处理时为 46MB。

**健壮性：不会崩溃，单个文件也不会拖垮服务。**

- `Composite(nil)`、`Flatten(nil)`、对同一方法传入两份选项、质量或颜色数越界等情形，均在执行时返回错误。
- 执行期间的任何 panic 均转为 `*PanicError` 返回，包括多线程时工作协程中的 panic；临时文件与未写完的输出照常清理。
- 输入像素、每一步结果的像素、GIF 动画所有帧的像素总和均设有上限（见上表）。前两项在开始计算之前即予拒绝；GIF 动画先统计帧数，总和超限时不处理任何一帧，直接拒绝（使用 `FixedPalette` 时处理至超限的那一帧为止）。
- 上述行为由模糊测试保障（见[测试](testing.md)）：将任意字节作为图像读取，以任意参数组合运行整条处理链，要求不发生 panic、不超出内存预算、预算全部归还、不残留临时文件。

## 水印

`Composite` 的定位分为三步，每一步均为可选：

1. **在底图上选取位置**：九宫格位置 `Gravity`（默认左上角）＋像素偏移 `Left`、`Top` ＋比例偏移 `LeftRatio`、`TopRatio`（按底图宽高的比例）。
2. **将水印的锚点置于该位置**：`Anchor` 使用与 `Gravity` 相同的九个名称（`NorthWest` … `Center` … `SouthEast`）选择水印的四角、四边中点或中心；也可使用 `CustomPosition` 加 `AnchorX`、`AnchorY`（自水印左上角起算的像素）指定水印上的任意一点。未设置时锚点与 `Gravity` 同名，因此 `Gravity: SouthEast, Left: -24, Top: -24` 即「右下角，边距 24 像素」。
3. **绕旋转中心旋转指定角度**：`Angle` 为顺时针角度，可取任意值；`Pivot` 为旋转中心，同样使用上述九个名称，或使用 `CustomPosition` 加 `PivotX`、`PivotY`，默认为锚点。

其他选项：

- `Opacity`：0–1，整体不透明度，与水印自身的透明度相乘；未设置时保持原样。
- `Blend`：叠加图颜色与底图颜色的混合方式：`BlendOver`（默认，与 `image/draw` 的 `Over` 相同）、`BlendMultiply`（正片叠底）、`BlendScreen`（滤色）、`BlendOverlay`（叠加）、`BlendDarken`（变暗）、`BlendLighten`（变亮）、`BlendColorDodge`（颜色减淡）、`BlendColorBurn`（颜色加深）、`BlendHardLight`（强光）、`BlendSoftLight`（柔光）、`BlendDifference`（差值）、`BlendExclusion`（排除）；计算方式与 sharp 的 `blend`（libvips 的 composite）相同，底图不透明时相差不超过 1。另有 Porter-Duff 算子，与 sharp 同名者相同：`BlendClear`、`BlendSource`、`BlendIn`、`BlendOut`、`BlendAtop`、`BlendDest`、`BlendDestOver`、`BlendDestIn`（遮罩：只保留叠加图不透明处的底图）、`BlendDestOut`（挖空）、`BlendDestAtop`、`BlendXor`，以及 `BlendAdd`、`BlendSaturate`；其中 clear、source、in、out、dest-in、dest-atop 还会清空叠加图以外的底图。带透明时颜色可见部分与 sharp 相差不超过 2（像素以 8 位预乘保存，libvips 用浮点）。命令行：`-watermark-blend multiply`、`-watermark-blend dest-in`。
- `Tile`、`TileGapX`、`TileGapY`：在全图范围内平铺，间距可设置。与 `Angle` 配合时，各水印单独旋转，行列仍保持水平与垂直。
- `WidthRatio`、`HeightRatio`：先将水印缩放至底图宽度（高度）的该比例，保持宽高比；两者均设置时，缩放至同时满足两者。批量处理尺寸不一的图像时，水印在每幅图像上的视觉大小因此一致。命令行写法为 `-watermark-width 20%`。

```go
// 全图平铺半透明的倾斜文字水印
goimgtools.Open("in.jpg").
    Composite(
        goimgtools.Text("© 示例水印", goimgtools.TextOptions{Size: 26, Color: color.White}),
        goimgtools.CompositeOptions{Tile: true, TileGapX: 60, TileGapY: 50, Angle: -30, Opacity: 0.25},
    ).ToFile("out.jpg")

// 水印中心位于底图宽 1/3、高 1/3 处
Composite(logo, goimgtools.CompositeOptions{LeftRatio: 1.0 / 3, TopRatio: 1.0 / 3, Anchor: goimgtools.Center})

// 右下角，宽度为底图的 15%，与底图尺寸无关
Composite(logo, goimgtools.CompositeOptions{Gravity: goimgtools.SouthEast, Left: -24, Top: -24, WidthRatio: 0.15})
```

水印整体驻留内存（计入内存上限），因此应使用小尺寸图像。样图见 [sample-effects.png](../sample-effects.png)。

## 文字

`Text` 与 `Open` 同为输入，其后可接任意操作。

```go
goimgtools.Text("第一行\n第二行 Mixed", goimgtools.TextOptions{
    Fonts: []string{"Microsoft YaHei"},           // 或字体文件路径
    Size:  48, Color: color.White,
    StrokeWidth: 3, StrokeColor: color.Black,     // 描边
    ShadowColor: color.RGBA{0, 0, 0, 180}, ShadowX: 4, ShadowY: 4, ShadowBlur: 3, // 阴影
}).ToFile("text.png")
```

- **字体**：`Fonts` 为列表，对每个字符按顺序尝试，前一字体缺少该字符时使用后一字体。每一项为字体文件（`.ttf`、`.otf`、`.ttc`；集合文件以 `文件名#1` 选择第二个字体）或系统已安装的字体名称。`FontData` 用于传入内存中的字体数据。
- **默认字体**：未指定时优先使用系统中常见的中文字体，最后为内置的 Go Regular（不含中文）。`DefaultFonts()` 列出当前系统的默认顺序，`SystemFonts()` 列出系统已安装的全部字体。
- **排版**：字号、颜色（含透明度时为半透明文字）、背景色（默认透明）、按 `Width` 自动换行、左 / 中 / 右对齐、行距、内边距。
- **描边 / 阴影**：`StrokeWidth`、`StrokeColor`；`ShadowColor`、`ShadowX`、`ShadowY`、`ShadowBlur`（偏移为 0 并加模糊即为发光效果）。图像尺寸自动扩大以容纳这些效果。
- **限制**：不支持从右到左书写的文字、需要连字整形的文字及竖排文字。所有字体均缺少的字符绘制为缺字方框。

字体的内存规则：

- 字体文件不整体读入，仅读取文本所用的字形。实测：微软雅黑为 18.8MB，整体读入占用 18.8MB，按此方式读取仅占用 0.13MB。
- 备用字体仅在前面的字体缺字时才打开。
- 每个已打开的字体按 256KB、绘制缓冲按「6 × 字号²」字节计入内存上限（1000 像素的文字约 6MB）；超出时返回内存超限错误。
- 文字按行渲染，内存中仅保留一行。

## 小工具：查看和检查图片

以下工具仅读取图像，不作修改。在命令行中为子命令；在库中，除上文的 `Metadata()`、`Frames()` 外，由公开子包 `inspect` 提供（该包仅基于库的公开接口实现，同样按行处理且内存有上限）。

| 命令行 | 库 | 说明 |
|---|---|---|
| `goimgtools info 文件...` | `img.Metadata()`、`img.Frames()` | 每个文件输出一行：格式、宽高、颜色类型与位深、像素数、文件大小、动画（GIF、WebP、APNG）的帧数与时长、EXIF 方向、分辨率（dpi）、是否含 ICC / EXIF。加 `-json` 时每个文件输出一行 JSON |
| `goimgtools check 文件...` | `inspect.Verify(img)` | 完整解码一遍，报告损坏、截断或并非图像的文件。`info` 仅读取文件头，无法发现文件后半部分的损坏 |
| `goimgtools compare a b` | `inspect.Compare(a, b)` | 逐像素比较两幅图像，格式不同亦可：给出是否完全相同；若不同，则给出 PSNR（越高越接近，40 以上肉眼难以分辨）、最大差值及差异像素数。`-min-psnr 35` 可用作质量门槛：未达到时以非零状态退出 |
| `goimgtools stats 文件...` | `inspect.Stats(img)` | 遍历全部像素：平均色、亮度、面积占比最大的几种颜色、是否实为灰度图像、是否确有透明像素、颜色数（不超过 256 种时）。可用于选取占位色、决定使用深色或浅色文字、判断能否存储为灰度或调色板图像。另有 sharp 的 `stats()` 所报告的各项，数值一致到末几位：`Channels`（红、绿、蓝、透明各通道的最低与最高值及其首次出现的位置、总和、平均值、标准差）、`Entropy`（熵）与 `Sharpness`（锐度） |
| `goimgtools hash 文件...` | `inspect.Hash(img)` | 64 位感知哈希：同一图像改变尺寸、格式或压缩率后，哈希值仅相差数位；不同图像约相差一半。`-similar 10` 列出彼此相近的文件，用于查找重复图像 |
| `goimgtools exif 文件...` | `inspect.ReadExif(img)` | EXIF 中的相机、镜头、拍摄时间、曝光参数、GPS 位置等 |
| `goimgtools readmark -key 密钥 文件...` | `inspect.ReadWatermark(img, key)` | 用 `InvisibleWatermark` 与同一密钥藏入的标记，不需要原图；没有时如实报告（见[隐形水印](watermark.md)） |
| | `inspect.ReadXmp(img)` | 文件的 XMP 数据包（XML），即 sharp 的 `metadata().xmp`；`info` 会显示文件是否含 XMP |
| `goimgtools formats` | `Formats()`、`CodecByName()` | 支持的格式、各格式的扩展名及是否可读、可写 |

`info`、`stats`、`exif` 均支持 `-json`，每个文件输出一行 JSON。

```bash
goimgtools info photo.jpg anim.gif
```

```
photo.jpg: jpeg 6000x4000, ycbcr 8-bit, 24.0 MP, 5.6MB, EXIF orientation 6 (upright it is 4000x6000), ICC profile, EXIF
anim.gif: gif 400x300, palette 8-bit, 0.1 MP, 1.2MB, 24 frames in 2.40s, plays for ever
```

```go
// 检查上传的文件是否为完好的图像
if err := inspect.Verify(goimgtools.FromBytes(upload)); err != nil {
    // errors.Is(err, goimgtools.ErrFormat)：文件有误
}

// 压缩后与原图的差异
d, err := inspect.Compare(goimgtools.Open("orig.png"), goimgtools.Open("small.jpg"))
fmt.Println(d) // PSNR 41.87 dB, largest difference 23 of 255, 96.4% of the pixels differ
```

```go
// 查找重复：哈希值相差 10 位以内，很可能为同一图像
a, _ := inspect.Hash(goimgtools.Open("a.jpg"))
b, _ := inspect.Hash(goimgtools.Open("b.webp"))
if a.Distance(b) <= 10 { /* 同一图像的不同版本 */ }

// 取图像主色作为占位色
st, _ := inspect.Stats(goimgtools.Open("photo.jpg"))
fmt.Println(inspect.Hex(st.Average), st.Brightness) // #2e2f29 0.18

// 相机与拍摄信息
if e, ok, _ := inspect.ReadExif(goimgtools.Open("photo.jpg")); ok {
    fmt.Println(e.Model, e.Taken, e.HasGPS)
}
```

实测（单线程）：`info`、`exif` 仅读取文件头，约 0.015 秒；对 2400 万像素的 JPEG，`check` 与 `hash` 约 0.2 秒，`stats` 约 0.5 秒（sharp 的 `stats()` 为 0.43 秒），`compare` 比较两幅此类图像约 0.5 秒。

说明：

- 感知哈希反映整幅画面的明暗分布，因此图像经裁剪、旋转或镜像后即无法识别；不具明暗分布的图像（纯色、从一侧到另一侧的平滑渐变）对其而言无从区分。
- EXIF 可从 JPEG、PNG、WebP、TIFF 中读取。库写出文件时默认丢弃 EXIF（包括位置信息），如需保留，须显式调用 `KeepExif()`。
- 上述工具均按行读取图像，内存有上限；`stats` 仅保留约 100KB 的颜色表，与图像尺寸无关。

`Compare` 比较的是两个配方处理后的像素，因此可直接比较两种处理方式，无需先写出文件，例如同一图像采用两种缩放算法：`inspect.Compare(a.Resize(800, 0), b.Resize(800, 0, goimgtools.ResizeOptions{Kernel: goimgtools.KernelLinear}))`。输出格式（`JPEG()`、`WebP()` 等）不参与比较；如需比较编码造成的损失，应先调用 `ToBytes()`，再以 `FromBytes` 读回后比较。
