[English](../usage.md) | 中文

# 用法

## 安装和基本写法

```bash
go get github.com/whhhh1500/goimgtools
```

```go
import "github.com/whhhh1500/goimgtools"
```

需要 Go 1.27（依赖的 `golang.org/x/image` 要求 1.26 以上）。写扩展时另外引入 `github.com/whhhh1500/goimgtools/pipeline`。

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

### 输入

| 方法 | 作用 |
|---|---|
| `Open(path)` / `FromBytes(b)` / `FromReader(r)` | 文件、内存、流 |
| `Text(text, TextOptions{...})` | 把文字画成一张图，可以当水印，也可以单独输出（见[文字](#文字)） |
| `FromImage(img)` | 内存里已有的 `image.Image` |
| `FromSource(open)` | 自己提供的像素流：算出来的图、从别处读来的像素（见[扩展](extending.md)） |

### 操作

| 方法 | 作用 |
|---|---|
| `Resize(w, h, ResizeOptions{...})` | 缩放。`w` 或 `h` 填 0 表示按比例算 |
| `Extract(left, top, w, h)` | 裁剪 |
| `Rotate()` | 按 EXIF 方向自动转正（没有方向信息时不动） |
| `Rotate(angle)` | 顺时针旋转，90 的倍数 |
| `RotateBy(degrees, background)` | 顺时针旋转任意角度（负数逆时针），画布放大到装下整张图，四角填 `background`（`nil` 黑色，`color.Transparent` 透明）；双线性插值。90 的倍数时就是 `Rotate`，不插值 |
| `Flip()` / `Flop()` | 上下镜像 / 左右镜像 |
| `Trim(threshold)` | 去掉四周和左上角像素同色的边（红绿蓝透明任一通道差超过 `threshold` 才算不同；10 能吃掉 JPEG 的噪点，与 sharp 相同） |
| `Extend(top, right, bottom, left, background)` | 四周加边，宽度按 CSS 的上右下左顺序 |
| `Grayscale()` | 变灰度：Rec. 601 亮度四舍五入到最近的一级，与 Pillow 的 `convert("L")` 逐字节相同；JPEG 直接取文件里存的亮度。不透明的图输出单通道灰度文件 |
| `Modulate(brightness, saturation, hue)` | 亮度、饱和度乘一个系数，色相转若干度，在 LCh 色彩空间里做（sharp 的做法），每个像素与 sharp 相差不超过 1：`Modulate(1.2, 0.8, 0)` 亮两成、淡一些 |
| `Blur(sigma)` | 高斯模糊，sigma 大于 0、不超过 1000 像素；Pillow 的 `GaussianBlur` 算法，耗时不随 sigma 变 |
| `Sharpen()` | 轻度锐化（sharp 的 3×3 卷积核），适合缩小之后用 |
| `Sharpen(SharpenOptions{Sigma, Amount, Threshold})` | 反锐化掩模（Pillow 的 `UnsharpMask`），`Amount` 默认 1.5 |
| `Flatten(color)` | 去透明，铺底色 |
| `Composite(overlay, CompositeOptions{...})` | 叠图 / 水印（见[水印](#水印)） |
| `Animated()` | 处理动图 GIF 的每一帧并输出动图（GIF 进、GIF 出时生效）。所有帧共用一套按画面选的调色板（`GIFOptions` 的 `Colors`、`NoDither` 照样有效，颜色不超过时原样保留），第二帧起只存变化的部分（见[压缩](compression.md)） |
| `Pixels(fn)` | 自己的逐像素操作：`fn` 逐条拿到 8 位 NRGBA，原地修改（见[扩展](extending.md)） |
| `Pipe(op)` | 自己的任意操作，接在链的这个位置（见[扩展](extending.md)） |
| `Apply(steps...)` | 把一组步骤写成函数，在链中间调用 |

`ResizeOptions`：

- `Fit`：`FitInside`（默认，等比缩到框内，不裁不变形）、`FitCover`（填满后居中裁掉多余，sharp 的默认）、`FitContain`（等比缩到框内，再用 `Background` 补边到正好 w×h，默认黑色）、`FitOutside`、`FitFill`（拉伸）。
- `WithoutEnlargement`：禁止放大。
- `Kernel`：`KernelCubic`（默认，Catmull-Rom）、`KernelNearest`、`KernelLinear`、`KernelMitchell`、`KernelLanczos2`、`KernelLanczos3`。
- `Filter`：自己的缩放滤镜（`&goimgtools.Filter{Support, At}`），给了就代替 `Kernel`。
- `FastShrinkOnLoad`：JPEG 解码时先缩小（见 [JPEG 解码时缩小](performance.md#jpeg-解码时缩小可选)）。

灰度图缩放后仍是灰度图（单通道），不会变成彩色。

模糊、锐化、调色、加边都是一行一行流过去的，内存只多几行（模糊约多 6×sigma 行，反锐化掩模再多 3×sigma 行，超出内存份额时写临时文件）。`Trim` 和 `RotateBy` 要先看完整张图：像素先放进暂存区（超出内存份额时是临时文件），再读一遍；所以链里有 `Trim` 时 `OutputSize` 也要读一遍像素。带透明通道的图在模糊、锐化时按预乘透明度算，透明处的颜色不会渗到旁边。

### 输出

| 方法 | 作用 |
|---|---|
| `JPEG(quality, JPEGOptions{Optimize, Progressive})` | 质量 1–100，填 0 为 80。两个选项让文件更小、像素不变（见[压缩](compression.md)） |
| `PNG(PNGOptions{CompressionLevel, Colors, NoDither})` | 压缩级别 1（最快）到 9（最小）；`Colors` 减色到最多 256 色（见[压缩](compression.md)） |
| `WebP(WebPOptions{Quality, Lossless, Effort})` | 有损（质量 1–100，默认 75）或无损（`Effort` 1 最快到 9 最小，默认 5） |
| `GIF(GIFOptions{Colors, NoDither, FixedPalette})` | 最多 256 色，颜色按图片内容选；`FixedPalette` 改用标准库的固定调色板 |
| `BMP()` | |
| `TIFF(TIFFOptions{Compression})` | 不压缩或 `TIFFDeflate` |
| `ToFormat(name, 选项)` | 按名字选输出格式（`"jpeg"`、`"png"`、`".tif"`……，以及注册的格式）；选项是该格式的选项结构体，可不给 |
| `MaxFileSize(n)` | 输出不超过 n 字节：自动用能放下的最高质量（JPEG、有损 WebP，以及带 `Quality` 的注册格式） |
| `ToFile(path)` / `ToWriter(w)` / `ToBytes()` | 执行并写出。`ToFile` 不指定格式时按扩展名；只有全程成功才留下文件 |
| `Metadata()` | 只读文件头，说明**输入**是什么：格式、存储的宽高、颜色类型（灰度 / 调色板 / RGB / YCbCr / CMYK）和位深、是否带透明、EXIF 方向、是否有 ICC / EXIF。与链上的操作无关 |
| `Frames()` | 输入有几帧、播放一遍多长、循环几次（只有 GIF 会多于一帧）。要读完整个文件，但不解码像素 |
| `OutputSize()` | **结果**会是多大：链上所有操作之后的宽高，不解码像素 |
| `ToImage()` | 执行并返回整张 `image.Image`（整张在内存里，计入内存上限） |
| `Stream(env)` | 执行并返回结果的像素流，不写文件（见[扩展](extending.md)） |
| `Formats()` / `FormatByName(s)` | 支持的格式及其扩展名；按名字或扩展名查（`"jpeg"`、`"JPG"`、`".tif"` 都认） |
| `RegisterFormat(codec)` / `CodecByName(s)` / `UnregisterFormat(name)` | 增加一种格式，或取出某种格式的读写函数来改写；撤销注册后内置的实现恢复（见[扩展](extending.md)） |

不指定输出格式、也没有扩展名可依据时，输出格式同输入；文字输入默认输出 PNG。

### 元数据和资源控制

| 方法 | 作用 |
|---|---|
| `KeepExif()` | 把输入的 EXIF（相机、时间、GPS）带到输出。默认丢弃 |
| `StripICC()` | 丢掉 ICC 色彩配置。默认保留，否则广色域照片会偏色 |
| `ToSRGB()` | 按输入的色彩配置把颜色换算成 sRGB，输出不再带配置：不认配置的程序里颜色也对，文件也小一点。转成 GIF、BMP（带不了配置）时自动这样做 |
| `MemoryLimit(bytes)` | 工作内存上限，默认 64MB |
| `Threads(n)` | 一次处理最多用几个核，默认 1，最多到 CPU 核数（见 [CPU 与线程](performance.md#cpu-与线程)） |
| `SetDefaultThreads(n)` | 整个程序的默认线程数（没有调用 `Threads` 的处理用它），不用在每个调用处都写一遍 |
| `TempDir(dir)` | 临时文件目录，默认系统临时目录 |
| `LimitInputPixels(n)` | 输入像素数上限，默认 16383×16383（同 sharp）。对 `Animated()` 的动图是所有帧的像素合计 |
| `LimitOutputPixels(n)` | 链上任何一步产生的图（因此也包括结果）的像素数上限，默认同上。防止一个小文件被放大成占满磁盘、算上几小时的结果 |
| `WithContext(ctx)` | `ctx` 取消或超时后中止处理，返回 `ctx.Err()` |
| `Clone()` | 复制一份配方，用来从同一个起点派生多个结果 |

色彩配置（ICC）在 JPEG、PNG、WebP、TIFF 之间读写；EXIF 在 JPEG、PNG、WebP 之间读写（TIFF 的 EXIF 在它自己的目录里，不读不写）。GIF、BMP 带不了色彩配置，转成这两种格式时按输入的配置把颜色换算成 sRGB（实测 Display P3 的图不换算时色差最大 17.6，肉眼可见地偏淡）。`Rotate()` 转正后保留的 EXIF 会把方向改成「正」。

换算支持由三原色加色调曲线描述的配置（相机、手机、Display P3、Adobe RGB、ProPhoto 都是这一类），与 LittleCMS（相对比色、超出 sRGB 的颜色截断）每个值相差不超过 1；查找表类的配置（少见，多为打印机）原样保留、不换算。

## 在服务里使用

**一个配方，多个结果。** 链上的方法改的是同一个 `Image` 并返回它自己，不会复制。要从同一个起点做出几个结果，先 `Clone()`：

```go
base := goimgtools.Open("in.jpg").Rotate()
thumb := base.Clone().Resize(200, 0)
large := base.Clone().Resize(1600, 0)
```

写好的配方可以反复执行，也可以多个协程同时执行（`FromReader` 的输入只能读一次）。

**错误分类。** 用 `errors.Is` 判断：

| 错误 | 含义 | 服务里通常怎么处理 |
|---|---|---|
| `ErrFormat` | 输入不是能读的图片：别的文件、不支持的格式或变体、数据损坏或被截断 | 上传的文件有问题 |
| `ErrPixelLimit` | 输入的像素数超过 `LimitInputPixels`（动图按所有帧合计），或链上某一步的结果超过 `LimitOutputPixels` | 图片太大，或要求的尺寸太大 |
| `ErrMemoryLimit` | 在内存上限内处理不了 | 调高 `MemoryLimit` 或拒绝 |
| `ErrFileSize` | `MaxFileSize` 的目标连最低质量也达不到 | 先缩小尺寸 |
| `context.Canceled` / `DeadlineExceeded` | `WithContext` 的上下文结束了 | 请求已取消 |
| `*PanicError`（用 `errors.As`） | 处理这个输入时库或扩展代码里出了 bug（panic），带原因和堆栈 | 记录下来报告；这次请求失败，程序继续运行 |
| 其他 | 读输入失败、磁盘满、参数错误等，保留原始错误 | 系统故障或调用方的错 |

判断依据：只有「读输入本身没出错、也不是内存或临时文件的问题」时，解码器报的错才算 `ErrFormat`。测试用真实图片截断、改坏后逐一核对过：标准解码器报错的地方这里报 `ErrFormat`，连接中途断开报的是连接自己的错误。

**取消。** `WithContext(ctx)` 在解码和编码的每条带之间检查一次（几行像素），取消后很快返回，写到一半的文件会删掉。

**写出的文件谁能读。** `ToFile` 先写到同目录下的一个隐藏文件，全部成功后改名，所以不会留下写到一半的结果。这个文件按普通新文件的权限创建（Linux 上通常是 `0644`，由进程的 umask 决定），替换已有文件时沿用原文件的权限；不是临时文件那种只有属主能读的 `0600`——否则以另一个用户身份运行的 Web 服务器会读不到处理好的图片。

**并发和内存总量。** 每次执行有自己的内存预算，互不影响；库本身不限制同时执行多少个。同时处理 N 张图，内存和 CPU 的总量大致是单张的 N 倍（单张通常 6–15MB，最多到 `MemoryLimit`）。要限制总量，用信号量控制并发数：

```go
var slots = make(chan struct{}, 4) // 同时最多 4 张

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

实测（批量模式，42 张照片）：并发 3 时峰值 19.5MB，并发 8 时 32MB，42 张同时 46MB。

**不会崩溃，也不会被一个文件拖死。**

- `Composite(nil)`、`Flatten(nil)`、给同一个方法传两份选项、越界的质量或颜色数等，都在执行时返回错误。
- 执行过程中任何 panic 都被转成 `*PanicError` 返回，包括多线程时工作协程里的；临时文件和写到一半的输出照常清理。
- 输入像素、每一步结果的像素、动图所有帧的像素合计都有上限（见上表）。前两项在开始计算之前就拒绝；动图先数帧数，超出就一帧都不处理直接拒绝（`FixedPalette` 时处理到超出的那一帧停下）。
- 这些是用模糊测试守着的（见[测试](testing.md)）：任意字节当图片读、任意参数组合跑整条链，要求不 panic、不超内存预算、预算全部归还、不留临时文件。

## 水印

`Composite` 的定位分三步，每一步都可选：

1. **在大图上选一个位置**：九宫格点 `Gravity`（默认左上角）＋像素偏移 `Left`、`Top` ＋比例偏移 `LeftRatio`、`TopRatio`（按大图宽高的比例）。
2. **把水印的锚点放到这个位置**：`Anchor` 用和 `Gravity` 同一套九个名字（`NorthWest` … `Center` … `SouthEast`）选水印的四角、四边中点或中心，或者用 `CustomPosition` 加 `AnchorX`、`AnchorY`（从水印左上角算的像素）指定水印上的任意一点。不填时锚点和 `Gravity` 同名，所以 `Gravity: SouthEast, Left: -24, Top: -24` 就是「右下角留 24 像素」。
3. **绕旋转点转一个角度**：`Angle` 是顺时针度数，任意值；`Pivot` 是旋转点，同样用这九个名字或 `CustomPosition` 加 `PivotX`、`PivotY`，默认就是锚点。

另外：

- `Opacity`：0–1，整体透明度，和水印自带的透明度相乘；不填为原样。
- `Tile`、`TileGapX`、`TileGapY`：平铺到整张图，间隔可设。配合 `Angle` 时每个水印各自旋转，行列仍横平竖直。
- `WidthRatio`、`HeightRatio`：先把水印缩放到大图宽（高）的这个比例，保持宽高比；两个都给时放进两者之内。批量处理大小不一的图时，水印在每张图上看起来一样大。命令行写 `-watermark-width 20%`。

```go
// 整张图铺满半透明斜向文字水印
goimgtools.Open("in.jpg").
    Composite(
        goimgtools.Text("© 示例水印", goimgtools.TextOptions{Size: 26, Color: color.White}),
        goimgtools.CompositeOptions{Tile: true, TileGapX: 60, TileGapY: 50, Angle: -30, Opacity: 0.25},
    ).ToFile("out.jpg")

// 水印中心对准大图宽 1/3、高 1/3 处
Composite(logo, goimgtools.CompositeOptions{LeftRatio: 1.0 / 3, TopRatio: 1.0 / 3, Anchor: goimgtools.Center})

// 右下角，宽度是大图的 15%，不管大图多大
Composite(logo, goimgtools.CompositeOptions{Gravity: goimgtools.SouthEast, Left: -24, Top: -24, WidthRatio: 0.15})
```

水印整张放在内存里（计入内存上限），所以应该用小图。样图见 [sample-effects.png](../sample-effects.png)。

## 文字

`Text` 和 `Open` 一样是一个输入，后面可以接任何操作。

```go
goimgtools.Text("第一行\n第二行 Mixed", goimgtools.TextOptions{
    Fonts: []string{"Microsoft YaHei"},           // 或字体文件路径
    Size:  48, Color: color.White,
    StrokeWidth: 3, StrokeColor: color.Black,     // 描边
    ShadowColor: color.RGBA{0, 0, 0, 180}, ShadowX: 4, ShadowY: 4, ShadowBlur: 3, // 阴影
}).ToFile("text.png")
```

- **字体**：`Fonts` 是列表，逐字按顺序尝试，前面的字体缺字就用后面的。每一项是字体文件（`.ttf`、`.otf`、`.ttc`，集合文件用 `文件名#1` 选第二个字体）或系统已安装的字体名。`FontData` 传内存里的字体数据。
- **默认字体**：不指定时先用系统里常见的中文字体，最后是内置的 Go Regular（没有中文）。`DefaultFonts()` 列出当前系统的默认顺序，`SystemFonts()` 列出系统装的全部字体。
- **排版**：字号、颜色（带透明度即半透明字）、背景色（默认透明）、`Width` 自动折行、左中右对齐、行距、留白。
- **描边 / 阴影**：`StrokeWidth`、`StrokeColor`；`ShadowColor`、`ShadowX`、`ShadowY`、`ShadowBlur`（偏移为 0 加柔化就是发光）。图片会自动加大到放得下这些效果。
- **限制**：不支持从右到左的文字、需要连字变形的文字、竖排。所有字体都没有的字画成缺字方框。

字体的内存规则：

- 字体文件不整个读入，只读文字用到的字形。实测微软雅黑 18.8MB，整读占 18.8MB，这样读只占 0.13MB。
- 备用字体只在前面的字体缺字时才打开。
- 每打开一个字体按 256KB、画字的缓冲按「6 × 字号²」字节计入内存上限（1000 像素的字约 6MB），超出时报内存超限。
- 文字按行出图，内存只占一行。

## 小工具：查看和检查图片

不改图片、只看图片的几样工具。命令行里是子命令；库里除了上面的 `Metadata()`、`Frames()`，还有一个公开子包 `inspect`（它只用库的公开接口写成，内存同样是按行处理、有上限的）。

| 命令行 | 库 | 做什么 |
|---|---|---|
| `goimgtools info 文件...` | `img.Metadata()`、`img.Frames()` | 每个文件一行：格式、宽高、颜色类型和位深、像素数、文件大小、动图的帧数和时长、EXIF 方向、是否带 ICC / EXIF。加 `-json` 每个文件输出一行 JSON |
| `goimgtools check 文件...` | `inspect.Verify(img)` | 把图片完整解码一遍，报告损坏、被截断或根本不是图片的文件。`info` 只读文件头，文件后半截坏了它看不出来 |
| `goimgtools compare a b` | `inspect.Compare(a, b)` | 两张图逐像素比较，格式不同也能比：是否完全相同；不同的话给出 PSNR（越高越接近，40 以上肉眼难辨）、最大差值、有多少像素不同。`-min-psnr 35` 可以当质量关卡用：达不到就以非零状态退出 |
| `goimgtools stats 文件...` | `inspect.Stats(img)` | 看遍每个像素：平均色、亮度、占面积最大的几种颜色、实际上是不是灰度图、实际上有没有透明像素、颜色数（不超过 256 种时）。用来取占位色、决定配深色还是浅色文字、判断能不能存成灰度或调色板 |
| `goimgtools hash 文件...` | `inspect.Hash(img)` | 64 位的「感知指纹」：同一张图换了尺寸、格式、压缩率，指纹只差几位；不同的图差一半左右。`-similar 10` 列出彼此相近的文件，用来找重复图 |
| `goimgtools exif 文件...` | `inspect.ReadExif(img)` | EXIF 里的相机、镜头、拍摄时间、曝光参数、GPS 位置等 |
| `goimgtools formats` | `Formats()`、`CodecByName()` | 支持哪些格式、各自的扩展名、能读还是能写 |

`info`、`stats`、`exif` 都可以加 `-json`，每个文件输出一行 JSON。

```bash
goimgtools info photo.jpg anim.gif
```

```
photo.jpg: jpeg 6000x4000, ycbcr 8-bit, 24.0 MP, 5.6MB, EXIF orientation 6 (upright it is 4000x6000), ICC profile, EXIF
anim.gif: gif 400x300, palette 8-bit, 0.1 MP, 1.2MB, 24 frames in 2.40s, plays for ever
```

```go
// 上传的文件是不是一张完好的图
if err := inspect.Verify(goimgtools.FromBytes(upload)); err != nil {
    // errors.Is(err, goimgtools.ErrFormat)：文件有问题
}

// 压缩之后和原图差多少
d, err := inspect.Compare(goimgtools.Open("orig.png"), goimgtools.Open("small.jpg"))
fmt.Println(d) // PSNR 41.87 dB, largest difference 23 of 255, 96.4% of the pixels differ
```

```go
// 找重复：指纹相差 10 位以内，多半是同一张图
a, _ := inspect.Hash(goimgtools.Open("a.jpg"))
b, _ := inspect.Hash(goimgtools.Open("b.webp"))
if a.Distance(b) <= 10 { /* 同一张图的不同版本 */ }

// 取一张图的主色当占位色
st, _ := inspect.Stats(goimgtools.Open("photo.jpg"))
fmt.Println(inspect.Hex(st.Average), st.Brightness) // #2e2f29 0.18

// 相机和拍摄信息
if e, ok, _ := inspect.ReadExif(goimgtools.Open("photo.jpg")); ok {
    fmt.Println(e.Model, e.Taken, e.HasGPS)
}
```

实测（单线程）：`info`、`exif` 只读文件头，约 0.015 秒；2400 万像素的 JPEG，`check` 和 `hash` 约 0.2 秒，`stats` 约 0.34 秒，`compare` 两张约 0.5 秒。

几点说明：

- 指纹看的是整幅画面的明暗布局，所以裁剪、旋转、镜像之后就认不出了；没有明暗布局的图（纯色、从一边到另一边的平滑渐变）在它看来都一样。
- EXIF 从 JPEG、PNG、WebP 里读。库写文件时默认丢掉 EXIF（包括位置），要保留得显式 `KeepExif()`。
- 这些工具都按行读图、内存有上限；`stats` 只留一张约 100KB 的颜色表，与图片大小无关。

`Compare` 比的是两个配方处理之后的像素，所以两种处理方式可以直接比，不用先写成文件，例如同一张图用两种缩放算法：`inspect.Compare(a.Resize(800, 0), b.Resize(800, 0, goimgtools.ResizeOptions{Kernel: goimgtools.KernelLinear}))`。输出格式（`JPEG()`、`WebP()` 这些）不参与比较；要比编码造成的损失，先 `ToBytes()` 再用 `FromBytes` 读回来比。
