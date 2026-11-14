# goimgtools

纯 Go 的图片处理工具：缩放、裁剪、旋转、铺底色、加水印（图片或文字）、转格式 / 压缩。图片按行流过处理链，不整张解到内存里，所以处理几千万像素的大图也只占十来 MB 内存。既是一个 Go 库，也带一个命令行程序。

可以在它上面加自己的东西而不用改它的代码：自己的操作、输入、文件格式、缩放滤镜都有接入口，也可以把 `Image` 嵌入自己的类型来扩充方法（见「扩展」）。

不依赖 cgo。依赖只有 Go 官方扩展库（`golang.org/x/image`，以及它解析字体要用的 `x/text`、`x/sys`）和 `github.com/klauspost/compress`（只用它的 zlib 解压，比标准库快）。WebP 的解码和编码都在本模块内。

## 库的用法

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
| `Text(text, TextOptions{...})` | 把文字画成一张图，可以当水印，也可以单独输出（见「文字」） |
| `FromImage(img)` | 内存里已有的 `image.Image` |
| `FromSource(open)` | 自己提供的像素流：算出来的图、从别处读来的像素（见「扩展」） |

### 操作

| 方法 | 作用 |
|---|---|
| `Resize(w, h, ResizeOptions{...})` | 缩放。`w` 或 `h` 填 0 表示按比例算 |
| `Extract(left, top, w, h)` | 裁剪 |
| `Rotate()` | 按 EXIF 方向自动转正（没有方向信息时不动） |
| `Rotate(angle)` | 顺时针旋转，90 的倍数 |
| `Flip()` / `Flop()` | 上下镜像 / 左右镜像 |
| `Flatten(color)` | 去透明，铺底色 |
| `Composite(overlay, CompositeOptions{...})` | 叠图 / 水印（见「水印」） |
| `Animated()` | 处理动图 GIF 的每一帧并输出动图（GIF 进、GIF 出时生效） |
| `Pixels(fn)` | 自己的逐像素操作：`fn` 逐条拿到 8 位 NRGBA，原地修改（见「扩展」） |
| `Pipe(op)` | 自己的任意操作，接在链的这个位置（见「扩展」） |
| `Apply(steps...)` | 把一组步骤写成函数，在链中间调用 |

`ResizeOptions`：

- `Fit`：`FitInside`（默认，等比缩到框内，不裁不变形）、`FitCover`（填满后居中裁掉多余，sharp 的默认）、`FitContain`（等比缩到框内，再用 `Background` 补边到正好 w×h，默认黑色）、`FitOutside`、`FitFill`（拉伸）。
- `WithoutEnlargement`：禁止放大。
- `Kernel`：`KernelCubic`（默认，Catmull-Rom）、`KernelNearest`、`KernelLinear`、`KernelMitchell`、`KernelLanczos2`、`KernelLanczos3`。
- `Filter`：自己的缩放滤镜（`&goimgtools.Filter{Support, At}`），给了就代替 `Kernel`。
- `FastShrinkOnLoad`：JPEG 解码时先缩小（见下文）。

灰度图缩放后仍是灰度图（单通道），不会变成彩色。

### 输出

| 方法 | 作用 |
|---|---|
| `JPEG(quality, JPEGOptions{Optimize, Progressive})` | 质量 1–100，填 0 为 80。两个选项让文件更小、像素不变（见「压缩」） |
| `PNG(PNGOptions{CompressionLevel, Colors, NoDither})` | 压缩级别 1（最快）到 9（最小）；`Colors` 减色到最多 256 色（见「压缩」） |
| `WebP(WebPOptions{Quality, Lossless, Effort})` | 有损（质量 1–100，默认 75）或无损（`Effort` 1 最快到 9 最小，默认 5） |
| `GIF(GIFOptions{Colors, NoDither, FixedPalette})` | 最多 256 色，颜色按图片内容选；`FixedPalette` 改用标准库的固定调色板 |
| `BMP()` | |
| `TIFF(TIFFOptions{Compression})` | 不压缩或 `TIFFDeflate` |
| `ToFormat(name, 选项)` | 按名字选输出格式（`"jpeg"`、`"png"`、`".tif"`……，以及注册的格式）；选项是该格式的选项结构体，可不给 |
| `MaxFileSize(n)` | 输出不超过 n 字节：自动用能放下的最高质量（JPEG、有损 WebP，以及带 `Quality` 的注册格式） |
| `ToFile(path)` / `ToWriter(w)` / `ToBytes()` | 执行并写出。`ToFile` 不指定格式时按扩展名；只有全程成功才留下文件 |
| `Metadata()` | 只读文件头，说明**输入**是什么：格式、存储的宽高、是否带透明、EXIF 方向、是否有 ICC / EXIF。与链上的操作无关 |
| `OutputSize()` | **结果**会是多大：链上所有操作之后的宽高，不解码像素 |
| `ToImage()` | 执行并返回整张 `image.Image`（整张在内存里，计入内存上限） |
| `Stream(env)` | 执行并返回结果的像素流，不写文件（见「扩展」） |
| `Formats()` / `FormatByName(s)` | 支持的格式及其扩展名；按名字或扩展名查（`"jpeg"`、`"JPG"`、`".tif"` 都认） |
| `RegisterFormat(codec)` / `CodecByName(s)` / `UnregisterFormat(name)` | 增加一种格式，或取出某种格式的读写函数来改写；撤销注册后内置的实现恢复（见「扩展」） |

不指定输出格式、也没有扩展名可依据时，输出格式同输入；文字输入默认输出 PNG。

### 元数据和资源控制

| 方法 | 作用 |
|---|---|
| `KeepExif()` | 把输入的 EXIF（相机、时间、GPS）带到输出。默认丢弃 |
| `StripICC()` | 丢掉 ICC 色彩配置。默认保留，否则广色域照片会偏色 |
| `MemoryLimit(bytes)` | 工作内存上限，默认 64MB |
| `Threads(n)` | 一次处理最多用几个核，1–3，默认 1 |
| `SetDefaultThreads(n)` | 整个程序的默认线程数（没有调用 `Threads` 的处理用它），不用在每个调用处都写一遍 |
| `TempDir(dir)` | 临时文件目录，默认系统临时目录 |
| `LimitInputPixels(n)` | 输入像素数上限，默认 16383×16383（同 sharp）。对 `Animated()` 的动图是所有帧的像素合计 |
| `LimitOutputPixels(n)` | 链上任何一步产生的图（因此也包括结果）的像素数上限，默认同上。防止一个小文件被放大成占满磁盘、算上几小时的结果 |
| `WithContext(ctx)` | `ctx` 取消或超时后中止处理，返回 `ctx.Err()` |
| `Clone()` | 复制一份配方，用来从同一个起点派生多个结果 |

元数据目前只在 JPEG、PNG 之间读写；`Rotate()` 转正后保留的 EXIF 会把方向改成「正」。

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
- 输入像素、每一步结果的像素、动图所有帧的像素合计都有上限（见上表）。前两项在开始计算之前就拒绝；动图处理到超出上限的那一帧时停下。
- 这些是用模糊测试守着的（见「测试」）：任意字节当图片读、任意参数组合跑整条链，要求不 panic、不超内存预算、预算全部归还、不留临时文件。

## 扩展：在这个库上面加自己的东西

这个库是按「不改它的代码也能加功能」来做的。每个环节都留了口子，库自带的功能走的也是同一个口子：六种格式和你注册的格式放在同一张表里，内置操作和你写的操作是同一种东西。

| 想做的事 | 用什么 |
|---|---|
| 只改像素值的操作（调色、灰度、反色、逐像素滤镜） | `Pixels(fn)` |
| 任意操作（改尺寸、加边框、统计、进度……） | `Pipe(op)`，操作是 `pipeline.Source` 上的一环 |
| 给一组步骤起个名字，在链中间用 | `Apply(steps...)` |
| 自己的类型，带上 `Image` 的全部方法再加新方法（相当于「继承」） | 嵌入 `*goimgtools.Image` |
| 自己的输入（内存里的图、算出来的图、别处读来的像素） | `FromImage(img)`、`FromSource(open)` |
| 自己的文件格式；或者改写库里某种格式的读 / 写 | `RegisterFormat(codec)`、`CodecByName(name)`、`ToFormat(name, 选项)` |
| 自己的缩放滤镜 | `ResizeOptions{Filter: &goimgtools.Filter{...}}` |
| 结果不写文件，交给自己的代码 | `Stream(env)`（像素流）、`ToImage()`（整张 `image.Image`） |

下面的例子都只用公开的包，[example_test.go](example_test.go) 里有能直接运行的完整版本（`go test -run Example`）。

### 只改像素值：`Pixels`

不管图片本身是什么存法（JPEG 的 YCbCr、调色板、16 位……），函数拿到的都是 8 位 NRGBA，一条一条地来，原地修改即可：

```go
grey := func(strip *image.NRGBA) error {
    for i := 0; i < len(strip.Pix); i += 4 {
        p := strip.Pix[i : i+4]
        y := uint8((299*int(p[0]) + 587*int(p[1]) + 114*int(p[2])) / 1000)
        p[0], p[1], p[2] = y, y, y
    }
    return nil
}
goimgtools.Open("in.jpg").Resize(1200, 0).Pixels(grey).ToFile("out.jpg")
```

内存只多一条带（约 256KB），算在内存上限里。

### 任意操作：`Pipe` 和像素流

库里的图片从解码到编码是一条「像素流」：每次给出几行，从上到下。一个操作就是读一条流、给出另一条流的环节。接口在公开包 `github.com/whhhh1500/goimgtools/pipeline` 里：

```go
type Source interface {
    Bounds() image.Rectangle      // 整张图的范围
    ColorModel() color.Model
    Next() (image.Image, error)   // 下一条带；结束时返回 io.EOF
    Close() error
    Env() *Env                    // 这次执行的内存预算和临时目录
}
```

写自己的环节时，把读的那条流**嵌入**进来，没改的方法就自动沿用，只重写要改的——和继承一个类、覆盖几个方法是一回事：

```go
// 统计经过了多少行
type rowCounter struct {
    pipeline.Source // Bounds、ColorModel、Env 都从这里来
    rows int
}

func (c *rowCounter) Next() (image.Image, error) {
    strip, err := c.Source.Next()
    if err == nil {
        c.rows += strip.Bounds().Dy()
    }
    return strip, err
}

count := goimgtools.OperationFunc(func(src pipeline.Source) (pipeline.Source, error) {
    return &rowCounter{Source: src}, nil
})
goimgtools.Open("in.jpg").Resize(800, 0).Pipe(count).ToFile("out.jpg")
```

操作可以改尺寸（`OutputSize()` 会报出它给的尺寸），可以放在链的任何位置。测试里有一个完整的「加边框」操作（[tests/extend_test.go](tests/extend_test.go) 的 `border`）可以照着写。

`pipeline` 包里还有写环节用得上的零件：`Map`（`Pixels` 的底层）、`RowReader` / `Rechunk`（按自己要的行数重新切带）、`NewBand` / `CopyRows`（按像素类型分配和拷贝）、`Spill` / `Scratch`（超出内存份额就自动落到临时文件的存储）。

**写环节要守的几条规则**（库里每个环节都靠它们）：

- 带按行号递增给出，每条横跨整个宽度，合起来正好覆盖一次；之后返回 `io.EOF`。
- 一条带只在下一次 `Next` 或 `Close` 之前有效，要留着就自己拷贝。
- 创建环节时不读像素（要便宜、尺寸立刻可知），第一次 `Next` 才开始读。
- `Close` 要关掉自己读的那条流，并归还占用的内存预算；只会被调用一次，出错后也会调。
- 会随图片变大的缓冲区（一条带、一整行、要留到最后的数据），分配前先 `Env().Budget.Reserve`，释放时 `Release`；可能很大的放进 `Spill` 或 `Scratch`。内存上限就是靠这一条守住的。

### 自己的类型：嵌入 `*Image`

链上的方法改的是同一个 `Image`，所以把它嵌入自己的结构体后，原有方法照用，自己再加方法：

```go
type Product struct {
    *goimgtools.Image
    SKU string
}

func OpenProduct(path, sku string) Product {
    return Product{goimgtools.Open(path).Rotate(), sku}
}

// Listing 加上店铺统一的步骤
func (p Product) Listing() Product {
    p.Resize(300, 300, goimgtools.ResizeOptions{Fit: goimgtools.FitContain, Background: color.White}).
        Composite(goimgtools.Text(p.SKU, goimgtools.TextOptions{Size: 14}),
            goimgtools.CompositeOptions{Gravity: goimgtools.SouthEast, Left: -6, Top: -6})
    return p
}

p := OpenProduct("in.jpg", "SKU-0042")
p.Flatten(color.White)            // Image 自己的方法
err := p.Listing().JPEG(85).ToFile("out.jpg")
```

只是想复用一组步骤、不需要新类型的话，写成函数用 `Apply` 就行：

```go
func Thumbnail(size int) func(*goimgtools.Image) *goimgtools.Image {
    return func(m *goimgtools.Image) *goimgtools.Image {
        return m.Rotate().Resize(size, size, goimgtools.ResizeOptions{Fit: goimgtools.FitCover})
    }
}
goimgtools.Open("in.jpg").Apply(Thumbnail(200)).JPEG(80).ToFile("out.jpg")
```

### 自己的输入

```go
// 内存里已有的图（别的库解出来的、程序画的）
goimgtools.FromImage(img).Resize(400, 0).ToFile("out.png")

// 算出来或从别处读来的像素：给一个「每次执行时造出一条流」的函数
board := goimgtools.FromSource(func(env *pipeline.Env) (pipeline.Source, error) {
    return &checker{size: 800, cell: 100, env: env}, nil
})
```

### 自己的格式，或者改写内置格式

注册之后，这种格式的文件会被 `Open` / `FromBytes` / `FromReader` 认出来，`Metadata()` 报它的名字，`ToFile` 按扩展名写它，所有操作、内存上限都照常生效；给了 `Quality` 函数的话 `MaxFileSize` 也能用。

```go
goimgtools.RegisterFormat(goimgtools.Codec{
    Format: goimgtools.Format{Name: "ppm", Extensions: []string{".ppm"}},
    Match:  func(head []byte) bool { return bytes.HasPrefix(head, []byte("P6")) },
    Decode: ppmDecode, // func(in goimgtools.Input, env *pipeline.Env) (pipeline.Source, error)
    Encode: ppmEncode, // func(w io.Writer, src pipeline.Source, o goimgtools.EncodeOptions) error
})

goimgtools.Open("in.jpg").Resize(800, 0).ToFile("out.ppm")
goimgtools.Open("in.ppm").Rotate(90).JPEG(80).ToFile("out.jpg")
```

可以只给 `Decode`（只读）或只给 `Encode`（只写）。完整的 PPM 读写不到一百行，见 [example_test.go](example_test.go)。

**在内置格式上加自己的东西**：取出它的 `Codec`，把想改的函数包一层，再注册回去（同名即替换）。里面可以调用原来的函数，相当于覆盖方法后再调父类的：

```go
jpeg, _ := goimgtools.CodecByName("jpeg")
encode := jpeg.Encode
jpeg.Encode = func(w io.Writer, src pipeline.Source, o goimgtools.EncodeOptions) error {
    start := time.Now()
    defer func() { log.Printf("jpeg %v 用时 %v", src.Bounds().Size(), time.Since(start)) }()
    return encode(w, src, o)
}
goimgtools.RegisterFormat(jpeg)
```

### 取出结果，以及在自己的环节里用库的操作

```go
// 整张图放进内存（唯一会整张持有的地方，算在内存上限里，放不下返回 ErrMemoryLimit）
img, err := goimgtools.Open("in.jpg").Resize(400, 0).ToImage()

// 像素流：交给自己的编码器、显示、分析
src, err := goimgtools.Open("in.jpg").Resize(400, 0).Stream(nil)
defer src.Close()
```

反过来，手上已经有一条流、想对它用库里的操作（比如在自己的环节里先缩小）：把它当成输入再取流，并传入同一个 `Env`，这样整次执行仍然只算一份内存预算：

```go
small, err := goimgtools.FromSource(func(*pipeline.Env) (pipeline.Source, error) {
    return src, nil
}).Resize(200, 0).Stream(src.Env())
```

### 扩展代码出错时

自己的操作、格式返回的错误会原样从 `ToFile` 等方法返回。如果扩展代码（或库本身）在处理某个输入时 panic，这次执行返回 `*goimgtools.PanicError`（带原因和堆栈），不会让整个程序退出，临时文件和未写完的输出照常清理——开了多线程也一样。

## 压缩

| 目标 | 做法 | 800 万像素真实照片上的实测 |
|---|---|---|
| JPEG 更小、画质不变 | `JPEG(q, JPEGOptions{Optimize: true})`：为这张图单独算编码表 | 质量 85：983KB → 965KB（小 1.9%），0.20s，7.3MB |
| JPEG 再小一些、画质不变 | `JPEGOptions{Progressive: true}`：渐进式（含优化编码表） | 983KB → 905KB（小 7.9%），0.34s，6.6MB |
| PNG 大幅变小（有损） | `PNG(PNGOptions{Colors: 256})`：减到最多 256 色，颜色按图选，带抖动 | 6474KB → 2653KB（小 59%），与原图 39.1dB，0.90s，12.6MB |
| PNG 减色、不抖动 | 再加 `NoDither: true`：文件更小，渐变处会有色带 | 2142KB，40.7dB |
| 压到指定大小以内 | `MaxFileSize(n)`：自动找不超过 n 的最高质量 | 限 300KB：得到 297KB，0.66s，12MB |
| 换格式 | `WebP(WebPOptions{Quality: 80})` | PNG 6474KB → WebP 500KB |

- **JPEG 的两个选项**不改变任何像素（用 libjpeg 解码验证过逐像素相同），文件大小与 libjpeg-turbo 的同名选项相差不到 0.04%；算法照 libjpeg 移植。不加选项时输出仍与 Go 标准库逐字节相同。
- **PNG 减色**同等做法下比 Pillow 的中位切分文件更小、画质更高（上面那张照片：Pillow 抖动 2877KB / 34.5dB，不抖动 2309KB / 37.6dB）。透明保留：全透明仍全透明，不透明仍不透明，柔边保留中间的透明度。颜色不超过 `Colors` 种的图原样保留、不损失。没能和 pngquant 直接对比（本机没有装）。
- **减色不适合本来就很好压的图**：一张纯渐变图全彩只有 17KB，减色加抖动后变成 346KB。命令行的 `-skip-larger` 会在结果不比原文件小时保留原文件（见下）。
- **`MaxFileSize`** 要编码约 6 遍，耗时是不限大小时的 4–6 倍；原质量已经够小时只编码一次。质量降到 1 还放不下时返回 `ErrFileSize`，需要先缩小尺寸。配合 `Progressive` 时同样大小能用更高的质量。
- 这几项都要先看完整张图，中间数据进暂存区（超过内存份额写临时文件）。

## 水印

`Composite` 的定位分三步，每一步都可选：

1. **在大图上选一个位置**：九宫格点 `Gravity`（默认左上角）＋像素偏移 `Left`、`Top` ＋比例偏移 `LeftRatio`、`TopRatio`（按大图宽高的比例）。
2. **把水印的锚点放到这个位置**：`Anchor` 用和 `Gravity` 同一套九个名字（`NorthWest` … `Center` … `SouthEast`）选水印的四角、四边中点或中心，或者用 `CustomPosition` 加 `AnchorX`、`AnchorY`（从水印左上角算的像素）指定水印上的任意一点。不填时锚点和 `Gravity` 同名，所以 `Gravity: SouthEast, Left: -24, Top: -24` 就是「右下角留 24 像素」。
3. **绕旋转点转一个角度**：`Angle` 是顺时针度数，任意值；`Pivot` 是旋转点，同样用这九个名字或 `CustomPosition` 加 `PivotX`、`PivotY`，默认就是锚点。

另外：

- `Opacity`：0–1，整体透明度，和水印自带的透明度相乘；不填为原样。
- `Tile`、`TileGapX`、`TileGapY`：平铺到整张图，间隔可设。配合 `Angle` 时每个水印各自旋转，行列仍横平竖直。

```go
// 整张图铺满半透明斜向文字水印
goimgtools.Open("in.jpg").
    Composite(
        goimgtools.Text("© 示例水印", goimgtools.TextOptions{Size: 26, Color: color.White}),
        goimgtools.CompositeOptions{Tile: true, TileGapX: 60, TileGapY: 50, Angle: -30, Opacity: 0.25},
    ).ToFile("out.jpg")

// 水印中心对准大图宽 1/3、高 1/3 处
Composite(logo, goimgtools.CompositeOptions{LeftRatio: 1.0 / 3, TopRatio: 1.0 / 3, Anchor: goimgtools.Center})
```

水印整张放在内存里（计入内存上限），所以应该用小图。样图见 [docs/sample-effects.png](docs/sample-effects.png)。

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

## 命令行

```
goimgtools [选项] 输入文件 输出文件
goimgtools batch [选项] 输入目录 输出目录
goimgtools -text "文字" [选项] 输出文件
goimgtools info 文件...
goimgtools fonts [-all]
```

```bash
go build -o goimgtools ./cmd/goimgtools
```

发布之后也可以直接安装：

```bash
go install github.com/whhhh1500/goimgtools/cmd/goimgtools@latest
```

```bash
goimgtools -rotate auto -resize 1536x1536 -no-enlarge -flatten white -watermark logo.png -quality 85 in.jpg out.jpg
```

```bash
goimgtools -watermark-text "© 示例" -font-size 40 -text-color white -stroke-width 2 -watermark-tile -watermark-gap 80,60 -watermark-angle -30 -watermark-opacity 0.3 in.jpg out.webp
```

执行顺序固定为：旋转 → 翻转 → 裁剪 → 缩放 → 铺底色 → 水印。

| 选项 | 说明 |
|---|---|
| `-rotate auto\|90\|180\|270` | `auto` 按 EXIF 转正 |
| `-flip` / `-flop` | 上下 / 左右镜像 |
| `-crop X,Y,W,H` | 裁剪 |
| `-resize WxH` | 目标尺寸；可只写一边（`1536x`、`x1024`） |
| `-fit` | `inside`（默认）、`cover`、`contain`、`outside`、`fill` |
| `-background 颜色` | `-fit contain` 补边的颜色 |
| `-kernel` | `nearest`、`linear`、`cubic`（默认）、`mitchell`、`lanczos2`、`lanczos3` |
| `-no-enlarge` | 不放大 |
| `-fast-shrink` | JPEG 缩到一半以下时在解码阶段先缩小 |
| `-flatten 颜色` | 铺底色 |
| `-watermark 文件` / `-watermark-text 文字` | 图片水印 / 文字水印（`\n` 换行） |
| `-watermark-width N` | 把水印缩到 N 像素宽 |
| `-gravity`、`-offset X,Y` | 九宫格位置和像素偏移；只用这两项时默认离边 24 像素 |
| `-watermark-ratio X,Y` | 按大图宽高比例偏移 |
| `-watermark-anchor`、`-watermark-pivot` | 锚点、旋转点：九宫格名或 `X,Y` |
| `-watermark-angle 度` | 顺时针旋转 |
| `-watermark-opacity 0–1` | 整体透明度 |
| `-watermark-tile`、`-watermark-gap X,Y` | 平铺和间隔 |
| `-text 文字` | 不要输入文件，直接把文字生成图片 |
| `-font`、`-font-size`、`-text-color`、`-text-background`、`-text-width` | 字体（逗号分隔，文件或字体名）、字号、颜色、背景、折行宽度 |
| `-stroke-width`、`-stroke-color` | 描边 |
| `-shadow-color`、`-shadow-offset X,Y`、`-shadow-blur` | 阴影 |
| `-format` | `jpeg`、`png`、`webp`、`gif`、`bmp`、`tiff`；默认按扩展名 |
| `-quality N` | JPEG 或有损 WebP 的质量 |
| `-jpeg-optimize`、`-jpeg-progressive` | JPEG：优化编码表、渐进式（更小，像素不变） |
| `-png-colors N`、`-no-dither` | PNG：减到 N 色（2–256）；不抖动 |
| `-max-size 大小` | 文件不超过这个大小（`200KB`、`1.5MB` 或字节数），自动降质量 |
| `-skip-larger` | 结果不比原文件小（同格式）时保留原文件；用于只做压缩的场合 |
| `-webp-lossless` | 无损 WebP |
| `-png-level N`、`-gif-colors N`、`-gif-fixed-palette`、`-tiff-compression none\|deflate` | 各格式的选项 |
| `-animated` | 处理动图 GIF 的每一帧 |
| `-keep-exif`、`-strip-icc` | 元数据 |
| `-mem MB`、`-threads N`、`-tmp 目录` | 内存上限（默认 64）、线程数（默认 1）、临时目录 |
| `-max-pixels N` | 接受和产生的最大图片，单位百万像素（默认 268，即 16383×16383）；`-animated` 时按所有帧合计 |

### 批量处理目录

```bash
goimgtools batch -resize 1920x1920 -no-enlarge -quality 80 -format jpeg 输入目录 输出目录
```

对输入目录里的每张图（`.jpg .jpeg .png .webp .gif .bmp .tif .tiff`）执行同样的处理，写到输出目录的同名文件。单文件模式的选项都能用，另有：

| 选项 | 说明 |
|---|---|
| `-jobs N` | 同时处理几个文件，默认 3；任意数都可以，`0` 表示不限制（全部同时） |
| `-recursive` | 连子目录一起处理，在输出目录里重建目录结构 |
| `-overwrite` | 覆盖已存在的输出文件（默认跳过）；输入输出是同一目录时加它才会原地替换 |
| `-quiet` | 只打印失败和汇总 |

- 指定 `-format` 时扩展名改成对应格式，否则每个文件保持原格式。
- 某个文件失败会报出来，其余继续；有失败时退出码非零。
- 每个文件最多用 `-mem` 的工作内存和 `-threads` 个核，总量是它乘以并发数。不限制并发时文件一多，内存和 CPU 都会成倍上去。

实测 42 张真实照片（共 2.5 亿像素，20 核），缩到 1920 并转 JPEG：

| 并发 | 耗时 | 峰值内存 |
|---|---|---|
| 1 | 4.6s | 13MB |
| 3（默认） | 1.5s | 19.5MB |
| 8 | 0.87s | 32MB |
| 不限制（42 个同时） | 0.83s | 46MB |

颜色写 `white`、`black`、`#rrggbb` 或 `#rrggbbaa`。

## 支持的格式

| 格式 | 读 | 写 | 读取方式 |
|---|---|---|---|
| PNG | 是 | 是 | 按行流式；隔行（Adam7）先把各遍像素存入暂存区再按行取出 |
| JPEG | 是 | 是 | 基线式按行流式；渐进式把 DCT 系数存入暂存区，最后一遍扫描结束后按行还原 |
| WebP | 是 | 是（有损、无损） | 有损按宏块行流式；无损只保留格式规定的最远回看窗口（约 4.2MB） |
| GIF | 是（含动图） | 是（含动图） | 按行流式；隔行同 PNG |
| BMP | 是 | 是 | 按行按文件偏移读 |
| TIFF | 是 | 是 | 按行流式解压 |

「暂存区」的数据不超过内存上限的 1/8（默认 8MB）时放内存，超过就写临时文件，用完即删。AVIF、HEIC 不支持。

## 内存是怎么省下来的

- 每个环节一次只拿几行（约 256KB 一条），处理完交给下一环节。
- 缩放不保留中间缓冲：每读一行原图，横向缩好后直接按权重累加到它影响的那几行目标行上。
- 必须看到整张图才能出第一行的情况，数据进暂存区，已知放不下时直接写盘：隔行 PNG / GIF、渐进式 JPEG、旋转和上下翻转、BMP 输出（文件要求从最后一行写起）、需要先知道整体信息才能写文件头的编码（PNG 的透明判断、WebP、压缩的 TIFF，暂存的是已压缩或已编码的数据）。
- 暂存数据最多只占内存上限的 1/8，其余写盘。落盘的代价很小（实测 1200–2400 万像素：隔行 GIF 耗时不变，渐进式 JPEG 慢约 20%，旋转多约 0.1 秒），而放内存会让中等大小的图峰值达到 20–45MB。想换回速度就调高 `MemoryLimit`：暂存数据小于上限的 1/8 时不碰磁盘。
- 所有随图片尺寸变大的缓冲都计入内存上限。上限不够时返回 `ErrMemoryLimit`，不会悄悄多用。
- `Rotate` 后面紧跟 `Resize` 时，先缩小再旋转，暂存的是小图。

## CPU 与线程

默认单线程，CPU 占用恒定在一个核以内。`Threads(n)` 设上限：

- `Threads(2)`：解码在一个线程，缩放、合成、编码在另一个线程。
- `Threads(3)`：编码再单独占一个线程（输出很大、不缩小的转码才有用）。
- 更大的值按 3 处理。占用的核数不会超过设定值，输出与单线程逐字节相同。

线程之间用固定 3 条带的缓冲交接，消费方跟不上时生产方停下等待，所以每多一个线程只多占约 1–2MB，并计入内存上限。

## JPEG 解码时缩小（可选）

`ResizeOptions{FastShrinkOnLoad: true}`（命令行 `-fast-shrink`）：JPEG 要缩到原来的一半以下、且 `Resize` 是第一个操作（前面只有旋转 / 翻转也算）时，解码出的每个平面先按 2、4 或 8 倍取块平均，再做剩下的缩放。输出尺寸与不开时相同，像素略有差异（平滑图上平均不到 2 个色阶），所以默认关闭。

## 实测

Windows 10，Go 1.27.1。「标准库做法」指 `image.Decode` 整张解码 + `x/image` 的 `CatmullRom.Scale` + 标准库编码。峰值内存是操作系统记录的进程物理内存峰值，其中约 5.4MB 是 Go 程序启动自带的。

### 缩到 1536 框内 + 白底 + 水印 + 输出

| 输入（像素） | 输出 | 本工具峰值内存（1 线程 / 2 线程） | 标准库做法峰值内存 | 1 线程耗时 | 2 线程耗时 | 标准库做法耗时 |
|---|---|---|---|---|---|---|
| PNG RGBA 6900×9200 | JPEG | 10.4MB / 11.2MB | 588MB | 1.63s | 1.21s | 3.10s |
| PNG RGBA 6900×9200 | PNG | 11.2MB / 12.2MB | 591MB | 1.71s | 1.27s | 3.16s |
| PNG 隔行 RGB 6000×4000 | JPEG | 8.8MB / 10.9MB | 379MB | 0.56s | 0.50s | 1.02s |
| JPEG 基线 4:2:0 6000×4000 | JPEG | 8.3MB / 9.0MB | 242MB | 0.38s | 0.21s | 0.87s |
| JPEG 基线 4:4:4 6000×4000 | JPEG | 8.3MB / 8.8MB | 277MB | 0.57s | 0.37s | 1.02s |
| JPEG 渐进式 6000×4000 | JPEG | 8.8MB / 10.5MB | 373MB | 0.87s | 0.75s | 1.07s |
| JPEG 渐进式 5000×8000 | JPEG | 8.8MB / 10.4MB | 535MB | 1.43s | 1.23s | 1.58s |
| GIF 6000×4000 | JPEG | 8.4MB / 10.5MB | 230MB | 0.29s | 0.15s | 0.85s |
| GIF 隔行 6000×4000 | JPEG | 9.1MB / 10.7MB | 248MB | 0.32s | 0.30s | 0.86s |
| BMP 24 位 6000×4000 | JPEG | 8.7MB / 10.5MB | 299MB | 0.21s | 0.18s | 0.58s |
| TIFF 未压缩 6000×4000 | JPEG | 8.4MB / 10.5MB | 535MB | 0.23s | 0.18s | 0.67s |
| TIFF LZW 6000×4000 | JPEG | 8.6MB / 10.6MB | 535MB | 0.74s | 0.57s | 1.18s |
| TIFF Deflate 6000×4000 | JPEG | 13.2MB / 14.2MB | 461MB | 0.58s | 0.40s | 1.08s |
| WebP 有损 6000×4000 | JPEG | 8.9MB / 11.1MB | 243MB | 0.94s | 0.73s | 1.42s |
| WebP 无损 6000×4000 | JPEG | 14.2MB / 15.0MB | 300MB | 1.00s | 0.80s | 1.52s |

- 总 CPU 时间不随线程数变（2 线程只是把同样的工作分到两个核上）。
- 渐进式 JPEG 的系数（6000×4000 时 72MB）写了临时文件；上限调到 1GB 让系数留在内存时为 0.76s、峰值 78MB。
- 开 `FastShrinkOnLoad`（单线程）：JPEG 基线 4:2:0 约 0.3s，4:4:4 约 0.5s。

### 其他操作（输入均为 JPEG 基线 4:2:0 6000×4000，单线程）

| 操作 | 输出文件 | 峰值内存 | 耗时 |
|---|---|---|---|
| 不缩小，转 JPEG（质量 85） | 5.9MB | 6.0MB | 0.61s |
| 不缩小，转 PNG | 43.7MB | 8.0MB | 1.66s |
| 不缩小，转有损 WebP（质量 75） | 2.6MB | 15.7MB | 2.77s |
| 不缩小，转无损 WebP | 27.6MB | 21.8MB | 3.96s |
| 不缩小，转 BMP | 72MB | 6.7MB | 0.36s |
| 不缩小，转 TIFF（不压缩） | 96MB | 6.4MB | 0.34s |
| 旋转 90°，不缩小 | 6.0MB | 12.4MB | 1.24s |
| 旋转 90° 再缩到 1536 | 0.1MB | 15.5MB | 0.39s |
| 缩到 1536，输出 GIF | 0.5MB | 12.9MB | 0.62s |
| 不缩小，铺满平铺的描边文字水印 | 6.2MB | 10.6MB | 0.75s |
| 缩到 1536，输出有损 WebP | 8KB | 7.9MB | 0.42s |

- 旋转 90° 不缩小时，96MB 的像素在临时文件里；全放内存（上限 768MB 以上）时约 0.8s、峰值约 108MB。
- 无损 WebP 对 PNG RGB 6000×4000（76.1MB）：输出 51.5MB，峰值 23.7MB，5.24s。
- 不缩小转 GIF 要 2.0s（峰值 11.8MB）。用固定调色板（`FixedPalette`）时为 4.3s：那条路的找色顺序做了优化，结果与标准库逐字节相同，速度是逐个比较的 3 倍多。

### 灰度图、带透明通道的图、PNG 输出（单线程）

这几类原来多做了无用功，改过之后的前后对比。除灰度图外输出文件逐字节不变；灰度图的输出从彩色文件变成了灰度文件，像素值相同。

| 场景 | 之前 | 现在 | 改了什么 |
|---|---|---|---|
| 灰度 JPEG 6000×4000 → 缩到 1536 → JPEG | 0.32s，100KB | 0.26s，93KB | 灰度图缩放只算一个通道，结果仍是灰度图 |
| 灰度 PNG 6000×4000 → 缩到 1536 → PNG | 0.34s，859KB | 0.24s，701KB | 同上 |
| 灰度 JPEG 6000×4000 → 缩到 4000 → JPEG | 0.56s | 0.37s | 同上 |
| 灰度 PNG 6000×4000 → 缩到 4000 → PNG | 0.93s，7.9MB | 0.44s，6.2MB | 同上，加上 PNG 写出变快 |
| JPEG 6000×4000 → PNG，不缩小 | 2.38s | 1.66s | PNG 逐行选滤波器的循环重写（去掉逐字节的判断和分支，选出的滤波器不变） |
| PNG RGB 6000×4000 → PNG，不缩小 | 1.96s | 0.76s | 同上 |
| PNG RGBA 6900×9200 → JPEG，不缩小 | 2.85s | 2.26s | JPEG 编码器直接读带透明通道的像素，不再逐像素走通用接口 |
| 调色板 PNG 6000×4000 → JPEG，不缩小 | 0.60s | 0.49s | 调色板先整体换算一次 |
| JPEG 6000×4000 → 缩到 1536 → JPEG | 0.39s | 0.375s | 4:2:0 的相邻两个像素共用色度运算 |
| JPEG 6000×4000 → JPEG，不缩小 | 0.62s | 0.61s | 量化的除法换成等价的乘法 |

评估过、没有做的：

- 缩放直接在 YCbCr 三个平面上做（色度平面只有四分之一大小，缩放那部分能快近一倍）。结果会与 `x/image` 不再逐字节一致，只能放进 `FastShrinkOnLoad` 这类「像素略有差异」的可选项里；在那个模式下缩放只占耗时的约六分之一，估计整体再快 9% 左右，改动面却不小。
- PNG 压缩换成 `klauspost/compress`：Go 1.27 标准库的压缩已经是同一类快速实现，换了没有收益。
- 有损 WebP 编码（2400 万像素约 2.8 秒）：时间花在预测模式搜索和量化上，没有不改变输出的便宜改法。

复测：

```bash
go build -o memprobe.exe ./cmd/memprobe
```

```bash
./memprobe.exe api in.png out.jpg 1536 1536 logo.png
```

```bash
./memprobe.exe std in.png out.jpg 1536 1536 logo.png
```

环境变量：`GOIMG_MEM`（上限，MB）、`GOIMG_THREADS`、`GOIMG_FAST=1`、`GOIMG_NORESIZE=1`（不缩小）、`GOIMG_ROTATE=1`（先转 90°）、`GOIMG_LOSSLESS=1`、`GOIMG_JPEG=opt|prog`、`GOIMG_COLORS=N`（PNG 减色）、`GOIMG_MAXSIZE=KB`、`GOIMG_ENLARGE=1`、`GOIMG_TEXT=文字`（平铺文字水印）、`GOIMG_PROF=文件名`（CPU 剖析）、`GOIMG_HEAP=文件名`（内存分配剖析）。

## 与标准库的一致性

PNG、JPEG、GIF、BMP、TIFF 的解码和编码，以及默认的缩放，都是从标准库和 `x/image` 的代码改出来的，只改了「整张图」到「按行」的部分，像素运算和顺序不变。测试逐字节对比：

- 解码：Go 和 `x/image` 自带的 164 个测试图（PNG 85、JPEG 31、TIFF 17、WebP 17、BMP 9、GIF 5）解出的像素与标准解码器相同（WebP 见下）；截断、单字节损坏的文件，标准解码器报错的这里也报错。
- 缩放：默认算法与 `draw.CatmullRom.Scale` 逐字节相同。灰度图只算一个通道、输出灰度图，数值就是 `x/image` 给出的红绿蓝三个相等通道的值。
- 编码：JPEG（不加选项时）、PNG（不减色时）、GIF（`FixedPalette` 时，以及动图）与标准库逐字节相同，BMP、TIFF 与 `x/image` 逐字节相同（不带元数据时）。

故意不同的地方：

- **GIF 的调色板**：标准库把非调色板图一律降到固定的 256 色（Plan 9 调色板），照片只有 25dB。本工具默认按图片内容选色，同一张照片 39dB；界面截图从 29dB 提高到 58dB，文件还更小。`FixedPalette` 可换回标准库的做法。动图仍用固定调色板。
- **有损 WebP 的颜色**：`x/image` 把 WebP 的亮度色度当成 JPEG 的取值范围换算，颜色与 libwebp（浏览器）相比对比度偏低，实测平均差 6–8 个色阶、最大 33。本工具做了范围校正，在真实照片上与 libwebp 平均差 0.4–0.6 个色阶；色彩边缘处仍有差异（色度插值方式不同，最大 20–40）。
- **ICC**：输入带 ICC 时默认写进输出，所以这类图的输出比标准库编码多出 ICC 数据；`StripICC()` 后相同。
- JPEG、YCbCr、CMYK 等来源输出 PNG 时写成 8 位 RGB；`png.Encode` 会写成 16 位。
- 基线 JPEG 在一次完整扫描之后又出现一次扫描的文件（极少见）被拒绝。
- 尺寸为 0 的 GIF / BMP / WebP 被拒绝；标准库返回一张空图。
- JPEG 质量超出 1–100 时报错；标准库会悄悄截到范围内。
- 渐进式 JPEG 的系数按 16 位存，超出范围（只在损坏文件里出现）时报错。

WebP 编码器是本模块自己写的，没有标准库可对照，用两种解码器验证：`x/image` 和 libwebp（通过 Python PIL）解出的结果一致，无损编码逐像素还原。与 libwebp 的对比（均为子任务在本机实测）：

- 有损：13 张照片上，同等文件大小时 PSNR 比 libwebp 高 0.25–0.37 dB。没有做分段量化和网格量化，低质量下纹理比 libwebp 略平。
- 无损：默认档位下文件大小在 libwebp 的 −8.5% 到 +6% 之间，照片大多更小。

## 没做的

- AVIF、HEIC（决定不做）。
- TIFF、WebP 输入的 EXIF / ICC 不读；元数据只写进 JPEG、PNG。
- 动图只支持 GIF；动图输出统一用一套固定的 256 色加抖动，不保留原调色板，也还没用上按图选色。
- 任意角度旋转整张图（水印可以任意角度）。
- 颜色调整（灰度、亮度、对比度）没有内置方法，用 `Pixels` 几行就能写（「扩展」里有灰度的例子）；模糊、锐化这类要看相邻像素的滤镜没有内置，要自己写成 `Pipe` 的一环。
- JPEG 的网格量化（mozjpeg 的做法，同等观感下还能小 5%–10%，但会改变像素）。
- `MaxFileSize` 只支持 JPEG 和有损 WebP（以及带 `Quality` 的注册格式）。

## 发布状态

代码公开在 https://github.com/whhhh1500/goimgtools ，模块路径 `github.com/whhhh1500/goimgtools`。还差两件事：

1. **还没有版本标签。** 现在 `go get` 拿到的是主分支的最新提交；打了标签（例如 `v0.1.0`）之后别人才能固定到一个版本。
2. **libjpeg-turbo 的 `README.ijg` 还没有放进来。** JPEG 的「优化编码表」和「渐进式」两段代码是照它移植的；它的许可条文已经在 [licenses/LICENSE-LIBJPEG-TURBO](licenses/LICENSE-LIBJPEG-TURBO) 里，但 IJG 的许可对源码衍生作品要求附上完整的 `README.ijg`（详见 [licenses/THIRD-PARTY.md](licenses/THIRD-PARTY.md)）。

这是一个库，不需要按平台打包：使用者拿到的是源码，由各自的 Go 工具链编译；纯 Go、不依赖 cgo，Go 支持的平台都能直接编译。只有想让没装 Go 的人直接下载命令行程序时，才需要给各平台各编一份可执行文件（没有 cgo，一台机器上就能全部交叉编译出来）。

## 许可

本项目用 MIT 许可（[LICENSE](LICENSE)）。其中从 Go 标准库和 `golang.org/x/image` 改写的代码沿用其 BSD 许可（[licenses/LICENSE-GO](licenses/LICENSE-GO)）；从 libjpeg 移植的两段沿用其许可（[licenses/LICENSE-LIBJPEG-TURBO](licenses/LICENSE-LIBJPEG-TURBO)）。[licenses/THIRD-PARTY.md](licenses/THIRD-PARTY.md) 逐项列了哪些文件来自哪里。

## 目录

根目录里的 Go 文件就是这个库本身（一个包，`import` 一次就能用全部功能；Go 要求同一个包的文件放在同一个目录里）。其余都在子目录里：

```
goimgtools/
├── goimgtools.go    包说明、Image 类型、三种输入来源、内存 / 线程 / 上限等设置
├── operations.go    各个操作的方法和选项（缩放、旋转、裁剪、铺底、叠图）
├── output.go        选输出格式及其选项、MaxFileSize、写出
├── run.go           一次执行：打开输入、搭起处理链、Metadata、OutputSize
├── chain.go         操作列表和执行顺序的规划
├── formats.go       格式表：内置和注册的格式都在这一张表里；RegisterFormat
├── extend.go        扩展入口：Pipe、Pixels、Apply、FromImage、FromSource、ToFormat、Stream、ToImage
├── input.go         输入的打开、读取出错的记录、错误分类
├── text.go          文字输入
├── animate.go       动图
├── example_test.go  文档里的可运行示例
│
├── pipeline/        公开包：像素流接口 Source、内存预算、暂存区，以及写扩展用的零件
├── internal/        各格式的解码和编码、缩放、基本操作、文字渲染、减色（见下表）
├── cmd/             命令行程序 goimgtools，实测程序 memprobe
├── tests/           库的行为测试：只通过公开接口使用这个库，和使用者的程序一样
├── licenses/        第三方代码的许可和来源说明
└── docs/            文档用的图片
```

| 路径 | 内容 |
|---|---|
| `cmd/goimgtools` | 命令行 |
| `cmd/memprobe` | 内存 / CPU / 耗时实测程序（Windows、Linux、macOS、BSD 上读取进程自身的峰值内存和 CPU 时间） |
| `internal/pngstream`、`jpegstream`、`gifstream`、`bmpstream`、`tiffstream` | 各格式的流式解码和编码 |
| `internal/webpstream` | WebP 流式解码 |
| `internal/vp8enc`、`internal/vp8lenc` | WebP 有损、无损编码 |
| `internal/resize` | 流式缩放、整数倍块平均缩小、缩放算法 |
| `internal/ops` | 裁剪、铺底色、叠图、旋转翻转、补边 |
| `internal/textsrc` | 文字排版渲染、字体查找 |
| `internal/quant` | 减色：按图选调色板、抖动 |

## 测试

```bash
go test ./...
```

全量约 3–5 分钟（各包并行跑）。WebP 编码器与 libwebp 的交叉验证需要装有 Pillow 的 Python，没有时自动跳过。

库本身的行为测试都在 `tests/` 里，只通过公开接口使用这个库；各个零件（`internal/` 下的各格式、`pipeline`）的测试挨着它们的代码。

**在哪些系统上验证过。** 全量测试在下面这些环境里实跑通过：

| 系统 | 处理器 | 怎么跑的 |
|---|---|---|
| Windows | x86-64 | 开发机，以及 GitHub 的自动测试 |
| Linux（Ubuntu） | x86-64 | 本机 WSL，以及 GitHub 的自动测试 |
| Linux（Ubuntu） | arm64 | GitHub 的自动测试 |
| macOS | arm64 | GitHub 的自动测试 |
| 32 位程序 | x86（windows/386、linux/386） | 开发机和 GitHub 的自动测试；包括 6 万像素宽高和各格式最大边长的那几组 |

同一张图在 Linux 和 Windows 上输出的文件逐字节相同。命令行程序在 Linux 上用真实照片实际跑过（缩放、文字水印、批量模式、系统字体查找）。

自动测试的配置在 [.github/workflows/test.yml](.github/workflows/test.yml)，每次推送都会跑上表的全量测试，外加竞态检测、三个模糊测试的短跑，以及其余目标的编译和静态检查。

只做了编译和静态检查、**没有实跑**的：FreeBSD、OpenBSD、NetBSD、Solaris、AIX、Plan 9、Android，以及 arm（32 位）、riscv64、ppc64le、s390x、mips、loong64。

WebAssembly（js/wasm，在 node 里运行）只跑通了四个小包的测试：`pipeline`、缩放、基本操作、命令行。根包的整套测试在这个环境里跑不下来：文件读写极慢，node 进程的内存涨到了 25GB，被中止，原因没有查。这个平台目前应当视为**未验证**。

除了各格式与标准库逐字节对比的测试，还有三类专门守边界的：

- **极限情况**（[tests/limits_test.go](tests/limits_test.go)）：1×1、1×N、N×1 以及 8 / 16 像素块边界附近的尺寸，每种像素类型写成每种格式再读回；这些小图走每一个操作和每一种缩放算法；每种格式能容纳的最大边长（JPEG、GIF 65535，有损 WebP 16383，无损 WebP 16384）以及多一个像素时的报错；6 万像素宽 / 高的图在默认内存上限内跑完；一百多组参数极值（质量、颜色数、角度、比例、偏移、字号的上下界和无穷大、NaN）；一次只给一个字节的输入流、中途断开的输入流；覆盖输入文件、输出目录不存在等文件系统情况。
- **扩展接口**（[tests/extend_test.go](tests/extend_test.go)、[example_test.go](example_test.go)）：只用公开包写出的操作、输入和文件格式；示例在包外编译运行。
- **模糊测试**（[tests/fuzz_test.go](tests/fuzz_test.go)）：

```bash
go test -run '^$' -fuzz FuzzDecode -fuzztime 5m ./tests
```

```bash
go test -run '^$' -fuzz FuzzChain -fuzztime 5m ./tests
```

```bash
go test -run '^$' -fuzz FuzzText -fuzztime 5m ./tests
```

`FuzzDecode` 把任意字节当图片解码，`FuzzChain` 让任意字节走任意操作组合再写成任意格式，`FuzzText` 画任意文字。要求：不 panic、不卡住、各条带正好覆盖整张图、内存预算不超且全部归还、不留临时文件。种子是 Go 和 `x/image` 自带的测试图加上本库写出的各种格式的小图。
