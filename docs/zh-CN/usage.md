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
    Rotate().                              // 按 EXIF 方向校正
    Resize(1536, 0).                       // 宽 1536，高按比例
    Flatten(color.White).                  // 透明区域铺白底
    Composite(goimgtools.Open("logo.png"), goimgtools.CompositeOptions{
        Gravity: goimgtools.SouthEast, Left: -24, Top: -24, // 右下角，边距 24 像素
    }).
    JPEG(85).
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

| 方法 | 说明 |
|---|---|
| `Resize(w, h, ResizeOptions{...})` | 缩放。`w` 或 `h` 为 0 时按比例计算 |
| `Extract(left, top, w, h)` | 裁剪 |
| `Rotate()` | 按 EXIF 方向自动校正（无方向信息时不作处理） |
| `Rotate(angle)` | 顺时针旋转，角度为 90 的倍数 |
| `RotateBy(degrees, background)` | 顺时针旋转任意角度（负值为逆时针），画布扩大以容纳全图，四角以 `background` 填充（`nil` 为黑色，`color.Transparent` 为透明）；采用双线性插值。角度为 90 的倍数时等同于 `Rotate`，不插值 |
| `Flip()` / `Flop()` | 上下镜像 / 左右镜像 |
| `Trim(threshold)` | 去除四周与左上角像素同色的边（红、绿、蓝、透明任一通道之差超过 `threshold` 才视为不同；取 10 可消除 JPEG 噪点，与 sharp 相同） |
| `Extend(top, right, bottom, left, background)` | 四周扩边，宽度按 CSS 的上、右、下、左顺序给出 |
| `Grayscale()` | 灰度化：Rec. 601 亮度四舍五入至最近的灰阶，与 Pillow 的 `convert("L")` 逐字节一致；JPEG 直接采用文件中存储的亮度。不透明图像输出为单通道灰度文件 |
| `Modulate(brightness, saturation, hue)` | 在 LCh 色彩空间中将亮度、饱和度乘以系数，并将色相旋转指定度数（与 sharp 的做法相同），每个像素与 sharp 的结果相差不超过 1：`Modulate(1.2, 0.8, 0)` 使亮度提高 20%、色彩略淡 |
| `Blur(sigma)` | 高斯模糊，sigma 须大于 0 且不超过 1000 像素；采用 Pillow 的 `GaussianBlur` 算法，耗时不随 sigma 变化 |
| `Sharpen()` | 轻度锐化（sharp 的 3×3 卷积核），适用于缩小之后 |
| `Sharpen(SharpenOptions{Sigma, Amount, Threshold})` | 反锐化掩模（Pillow 的 `UnsharpMask`），`Amount` 默认为 1.5 |
| `Flatten(color)` | 去除透明，铺底色 |
| `Composite(overlay, CompositeOptions{...})` | 图像叠加 / 水印（见[水印](#水印)） |
| `Animated()` | 处理 GIF 动画的每一帧并输出 GIF 或 WebP 动画。静态图像不受影响；无法保留的动画（GIF 输出为其他格式；WebP 动画与 APNG 不逐帧处理）返回包装了 `errors.ErrUnsupported` 的错误。WebP 第一帧完整存储，之后各帧仅存储变化的矩形区域。GIF 的所有帧共用一套按画面内容选取的调色板（`GIFOptions` 的 `Colors`、`NoDither` 依然有效；颜色数不超过该值时原样保留），自第二帧起仅存储变化的部分（见[压缩](compression.md)） |
| `Pixels(fn)` | 自定义逐像素操作：`fn` 逐条带接收 8 位 NRGBA 数据并原地修改（见[扩展](extending.md)） |
| `Pipe(op)` | 在链的当前位置插入任意自定义操作（见[扩展](extending.md)） |
| `Apply(steps...)` | 将一组步骤写为函数，在链的中间调用 |

`ResizeOptions`：

- `Fit`：`FitInside`（默认，等比缩放至框内，不裁剪、不变形）、`FitCover`（填满框后居中裁去多余部分，即 sharp 的默认方式）、`FitContain`（等比缩放至框内，再以 `Background` 补边至恰为 w×h，默认黑色）、`FitOutside`、`FitFill`（拉伸）。
- `WithoutEnlargement`：禁止放大。
- `Kernel`：`KernelCubic`（默认，Catmull-Rom）、`KernelNearest`、`KernelLinear`、`KernelMitchell`、`KernelLanczos2`、`KernelLanczos3`。
- `Filter`：自定义缩放滤镜（`&goimgtools.Filter{Support, At}`），设置后取代 `Kernel`。
- `FastShrinkOnLoad`：JPEG 解码时先行缩小（见 [JPEG 解码时缩小](performance.md#jpeg-解码时缩小可选)）。

灰度图像缩放后仍为灰度图像（单通道），不会转为彩色。

模糊、锐化、色彩调整与扩边均按行流式处理，仅额外占用少量行的内存（模糊约多占 6×sigma 行，反锐化掩模再多占 3×sigma 行；超出内存份额时写入临时文件）。`Trim` 与 `RotateBy` 须先读取全图：像素先写入暂存区（超出内存份额时为临时文件），再重新读取；因此链中含 `Trim` 时，`OutputSize` 也须读取一遍像素。带透明通道的图像在模糊、锐化时按预乘透明度计算，透明区域的颜色不会渗入相邻像素。

### 输出

| 方法 | 说明 |
|---|---|
| `JPEG(quality, JPEGOptions{Optimize, Progressive, Trellis, QuantTable})` | 质量 1–100，0 表示 80。`Optimize`、`Progressive` 在像素不变的前提下减小文件；`Trellis`、`QuantTable`（0–8，编号同 mozjpeg 的 `-quant-table`；或 `QuantTableJpegli`）会改变像素（见[压缩](compression.md)） |
| `PNG(PNGOptions{CompressionLevel, Colors, NoDither})` | 压缩级别 1（最快）至 9（最小；8、9 级搜索最小文件，耗时约为 7 级的 10 倍和 20 倍）；`Colors` 将颜色减至最多 256 色（见[压缩](compression.md)） |
| `WebP(WebPOptions{Quality, Lossless, Effort})` | 有损（质量 1–100，默认 75）或无损（`Effort` 1 最快至 9 最小，默认 5） |
| `GIF(GIFOptions{Colors, NoDither, FixedPalette})` | 最多 256 色，按图像内容选取；`FixedPalette` 改用标准库的固定调色板 |
| `BMP()` | |
| `TIFF(TIFFOptions{Compression})` | 不压缩或 `TIFFDeflate` |
| `ToFormat(name, 选项)` | 按名称选择输出格式（`"jpeg"`、`"png"`、`".tif"`……，以及已注册的格式）；选项为该格式的选项结构体，可省略 |
| `MaxFileSize(n)` | 输出不超过 n 字节：自动选用满足该限制的最高质量（JPEG、有损 WebP，以及提供 `Quality` 的注册格式）。与 `Animated()` 同用于动画时返回包装了 `errors.ErrUnsupported` 的错误；单帧 GIF 按静态图像处理 |
| `ToFile(path)` / `ToWriter(w)` / `ToBytes()` | 执行并写出。`ToFile` 未指定格式时按扩展名确定；仅当全程成功时才生成文件 |
| `Metadata()` | 仅读取文件头，描述**输入**：格式、存储的宽高、颜色类型（灰度 / 调色板 / RGB / YCbCr / CMYK）与位深、是否含透明、EXIF 方向、是否含 ICC / EXIF。不受链上操作影响 |
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
| `StripICC()` | 丢弃 ICC 色彩配置。默认保留，否则广色域照片会出现偏色 |
| `ToSRGB()` | 依据输入的色彩配置将颜色转换至 sRGB，输出不再附带配置：在忽略色彩配置的程序中颜色同样正确，文件也略小。转换为 GIF、BMP（无法附带配置）时自动执行 |
| `MemoryLimit(bytes)` | 工作内存上限，默认 64MB |
| `Threads(n)` | 单次处理最多使用的核数，默认 1，最大为 CPU 核数（见 [CPU 与线程](performance.md#cpu-与线程)） |
| `SetDefaultThreads(n)` | 全程序的默认线程数（供未调用 `Threads` 的处理使用），无需在每个调用处重复设置 |
| `TempDir(dir)` | 临时文件目录，默认为系统临时目录 |
| `LimitInputPixels(n)` | 输入像素数上限，默认 16383×16383（同 sharp）。对使用 `Animated()` 的 GIF 动画，按所有帧的像素总和计算 |
| `LimitOutputPixels(n)` | 链上任一步骤所生成图像（因而也包括结果）的像素数上限，默认值同上。用于防止小文件被放大为占满磁盘、耗时数小时的结果 |
| `WithContext(ctx)` | `ctx` 取消或超时后中止处理，返回 `ctx.Err()` |
| `Clone()` | 复制配方，用于从同一起点派生多个结果 |

ICC 色彩配置支持在 JPEG、PNG、WebP、TIFF 中读写；EXIF 支持在 JPEG、PNG、WebP 中读写（TIFF 的 EXIF 位于其独立目录中，不读不写）。GIF、BMP 无法附带色彩配置，转换为这两种格式时依据输入的配置将颜色转换至 sRGB（实测：Display P3 图像不经转换时色差最大为 17.6，肉眼可见偏淡）。`Rotate()` 校正方向后，保留的 EXIF 中的方向改为「正向」。

转换支持以三原色加色调曲线描述的配置（相机、手机、Display P3、Adobe RGB、ProPhoto 均属此类），每个值与 LittleCMS（相对比色，超出 sRGB 的颜色截断）相差不超过 1；查找表类配置（少见，多用于打印机）原样保留，不作转换。

## 服务端使用

**一个配方，多个结果。** 链上的方法修改的是同一个 `Image` 并返回其自身，不作复制。如需从同一起点生成多个结果，应先调用 `Clone()`：

```go
base := goimgtools.Open("in.jpg").Rotate()
thumb := base.Clone().Resize(200, 0)
large := base.Clone().Resize(1600, 0)
```

已构建的配方可重复执行，也可由多个协程同时执行（`FromReader` 的输入只能读取一次）。

**错误分类。** 使用 `errors.Is` 判断：

| 错误 | 含义 | 服务端的常见处理 |
|---|---|---|
| `ErrFormat` | 输入不是可读取的图像：其他类型的文件、不支持的格式或变体、数据损坏或截断 | 上传的文件有误 |
| `ErrPixelLimit` | 输入像素数超过 `LimitInputPixels`（GIF 动画按所有帧合计），或链上某一步骤的结果超过 `LimitOutputPixels` | 图像过大，或请求的尺寸过大 |
| `ErrMemoryLimit` | 无法在内存上限内完成处理 | 调高 `MemoryLimit` 或拒绝请求 |
| `ErrFileSize` | 即使采用最低质量也无法达到 `MaxFileSize` 的目标 | 先缩小尺寸 |
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
    return goimgtools.FromReader(in).WithContext(ctx).Resize(1600, 1600).JPEG(80).ToWriter(out)
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
| `goimgtools info 文件...` | `img.Metadata()`、`img.Frames()` | 每个文件输出一行：格式、宽高、颜色类型与位深、像素数、文件大小、动画（GIF、WebP、APNG）的帧数与时长、EXIF 方向、是否含 ICC / EXIF。加 `-json` 时每个文件输出一行 JSON |
| `goimgtools check 文件...` | `inspect.Verify(img)` | 完整解码一遍，报告损坏、截断或并非图像的文件。`info` 仅读取文件头，无法发现文件后半部分的损坏 |
| `goimgtools compare a b` | `inspect.Compare(a, b)` | 逐像素比较两幅图像，格式不同亦可：给出是否完全相同；若不同，则给出 PSNR（越高越接近，40 以上肉眼难以分辨）、最大差值及差异像素数。`-min-psnr 35` 可用作质量门槛：未达到时以非零状态退出 |
| `goimgtools stats 文件...` | `inspect.Stats(img)` | 遍历全部像素：平均色、亮度、面积占比最大的几种颜色、是否实为灰度图像、是否确有透明像素、颜色数（不超过 256 种时）。可用于选取占位色、决定使用深色或浅色文字、判断能否存储为灰度或调色板图像 |
| `goimgtools hash 文件...` | `inspect.Hash(img)` | 64 位感知哈希：同一图像改变尺寸、格式或压缩率后，哈希值仅相差数位；不同图像约相差一半。`-similar 10` 列出彼此相近的文件，用于查找重复图像 |
| `goimgtools exif 文件...` | `inspect.ReadExif(img)` | EXIF 中的相机、镜头、拍摄时间、曝光参数、GPS 位置等 |
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

实测（单线程）：`info`、`exif` 仅读取文件头，约 0.015 秒；对 2400 万像素的 JPEG，`check` 与 `hash` 约 0.2 秒，`stats` 约 0.34 秒，`compare` 比较两幅此类图像约 0.5 秒。

说明：

- 感知哈希反映整幅画面的明暗分布，因此图像经裁剪、旋转或镜像后即无法识别；不具明暗分布的图像（纯色、从一侧到另一侧的平滑渐变）对其而言无从区分。
- EXIF 可从 JPEG、PNG、WebP 中读取。库写出文件时默认丢弃 EXIF（包括位置信息），如需保留，须显式调用 `KeepExif()`。
- 上述工具均按行读取图像，内存有上限；`stats` 仅保留约 100KB 的颜色表，与图像尺寸无关。

`Compare` 比较的是两个配方处理后的像素，因此可直接比较两种处理方式，无需先写出文件，例如同一图像采用两种缩放算法：`inspect.Compare(a.Resize(800, 0), b.Resize(800, 0, goimgtools.ResizeOptions{Kernel: goimgtools.KernelLinear}))`。输出格式（`JPEG()`、`WebP()` 等）不参与比较；如需比较编码造成的损失，应先调用 `ToBytes()`，再以 `FromBytes` 读回后比较。
