[English](../extending.md) | 中文

# 扩展

本库的设计目标之一是无需修改源码即可扩展功能。处理流程的每个环节都提供扩展点，内置功能也通过同样的扩展点实现：六种内置格式与注册的格式位于同一张表中，内置操作与自定义操作属于同一类型。

| 用途 | 接口 |
|---|---|
| 仅修改像素值的操作（色彩调整、灰度化、反色、逐像素滤镜） | `Pixels(fn)` |
| 任意操作（改变尺寸、加边框、统计、进度报告……） | `Pipe(op)`，操作是 `pipeline.Source` 上的一个环节 |
| 将一组步骤命名，并在链中使用 | `Apply(steps...)` |
| 自定义类型，具备 `Image` 的全部方法并增加新方法（相当于继承） | 嵌入 `*goimgtools.Image` |
| 自定义输入（内存中的图像、计算生成的图像、从其他来源读取的像素） | `FromImage(img)`、`FromSource(open)` |
| 自定义文件格式；或改写库中某种格式的读取 / 写出 | `RegisterFormat(codec)`、`CodecByName(name)`、`ToFormat(name, 选项)` |
| 自定义缩放滤镜 | `ResizeOptions{CustomKernel: &goimgtools.CustomKernel{...}}` |
| 结果不写入文件，而交由调用方代码处理 | `Stream(env)`（像素流）、`ToImage()`（完整的 `image.Image`） |
| 为自定义操作命名，像内置操作一样按名称调用（`Do`、命令行 `-op`） | `RegisterOperation(def)`、`Do(name, 参数...)`、`Operations()` |
| 改写内置操作的行为：在每一步前后加入自定义处理，或以自定义实现替换整步（相当于重写方法） | `Around(hook)` |
| 包含自定义操作的命令行程序 | `cli` 包：注册后调用 `cli.Main()` |

以下示例仅使用公开包，可直接运行的完整版本见 [example_test.go](../../example_test.go)（`go test -run Example`）。

## 仅修改像素值：`Pixels`

无论图像以何种形式存储（JPEG 的 YCbCr、调色板、16 位……），函数接收的均为 8 位 NRGBA，每次一个条带，原地修改即可：

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

额外内存仅为一个条带（约 256KB），计入内存上限。

## 任意操作：`Pipe` 与像素流

在库内部，图像从解码到编码以像素流的形式传递：每次输出若干行，自上而下。操作即读取一条流、输出另一条流的环节。接口定义在公开包 `github.com/whhhh1500/goimgtools/pipeline` 中：

```go
type Source interface {
    Bounds() image.Rectangle      // 全图范围
    ColorModel() color.Model
    Next() (image.Image, error)   // 下一个条带；结束时返回 io.EOF
    Close() error
    Env() *Env                    // 本次执行的内存预算与临时目录
}
```

编写自定义环节时，应**嵌入**其读取的流：未重写的方法自动沿用，仅重写需要修改的方法，相当于继承并覆盖部分方法：

```go
// 统计经过的行数
type rowCounter struct {
    pipeline.Source // 提供 Bounds、ColorModel、Env
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

操作可以改变尺寸（`OutputSize()` 报告其输出尺寸），并可置于链中任意位置。测试中有一个完整的加边框操作（[tests/extend_test.go](../../tests/extend_test.go) 中的 `border`），可作参考。

`pipeline` 包另提供编写环节所需的组件：`Map`（`Pixels` 的底层实现）、`RowReader` / `Rechunk`（按所需行数重新切分条带）、`NewBand` / `CopyRows`（按像素类型分配与复制）、`Spill` / `Scratch`（超出内存份额后自动转存临时文件的存储）、`env.Parallel(n, fn)`（将一批互相独立的任务分配给本链空闲的核及中途空出来的核，仅在 `Threads(4)` 及以上时生效；无空闲核时在当前线程依次执行，因此总核数不会超限，也不会阻塞），以及 `env.Go(fn)` / `env.TryGo(fn)`（在空闲的核上运行环节可以不等的工作，如一边压缩已满的块、一边填下一块：无空闲核时 `Go` 在当前线程执行，`TryGo` 则不执行，留给环节稍后再试）。

```go
env := pipeline.EnvOf(src)
return pipeline.Map(src, func(strip *image.NRGBA) error {
    rows := strip.Rect.Dy()
    env.Parallel(4, func(k int) { // 每份处理四分之一的行
        for y := k * rows / 4; y < (k+1)*rows/4; y++ {
            invert(strip.Pix[y*strip.Stride:][:4*strip.Rect.Dx()])
        }
    })
    return nil
}), nil
```

**环节须遵守的规则**（库中所有环节均依赖这些规则）：

- 条带按行号递增的顺序输出，每个条带横跨全宽，合起来恰好覆盖全图一次；此后返回 `io.EOF`。
- 条带仅在下一次调用 `Next` 或 `Close` 之前有效，需要保留时须自行复制。
- 创建环节时不读取像素（创建开销须小，且尺寸须立即可知），自首次调用 `Next` 起开始读取。
- `Close` 须关闭本环节读取的流，并归还所占用的内存预算；它只会被调用一次，出错后也会被调用。
- 对于随图像增大的缓冲区（一个条带、一整行、须保留到最后的数据），分配前调用 `Env().Budget.Reserve`，释放时调用 `Release`；可能较大的数据应放入 `Spill` 或 `Scratch`。内存上限即依靠这条规则保证。

## 自定义类型：嵌入 `*Image`

链式方法修改的是同一个 `Image`，因此将其嵌入自定义结构体后，原有方法照常可用，并可增加新方法：

```go
type Product struct {
    *goimgtools.Image
    SKU string
}

func OpenProduct(path, sku string) Product {
    return Product{goimgtools.Open(path).AutoOrient(), sku}
}

// Listing 追加店铺统一的处理步骤
func (p Product) Listing() Product {
    p.Resize(300, 300, goimgtools.ResizeOptions{Fit: goimgtools.FitContain, Background: color.White}).
        Composite(goimgtools.Text(p.SKU, goimgtools.TextOptions{Size: 14}),
            goimgtools.CompositeOptions{Gravity: goimgtools.SouthEast, Left: -6, Top: -6})
    return p
}

p := OpenProduct("in.jpg", "SKU-0042")
p.Flatten(color.White)            // Image 自身的方法
err := p.Listing().JPEG(goimgtools.JPEGOptions{Quality: 85}).ToFile("out.jpg")
```

若只需复用一组步骤而无需新类型，可将其写成函数并通过 `Apply` 调用：

```go
func Thumbnail(size int) func(*goimgtools.Image) *goimgtools.Image {
    return func(m *goimgtools.Image) *goimgtools.Image {
        return m.AutoOrient().Resize(size, size, goimgtools.ResizeOptions{Fit: goimgtools.FitCover})
    }
}
goimgtools.Open("in.jpg").Apply(Thumbnail(200)).JPEG().ToFile("out.jpg")
```

## 自定义输入

```go
// 内存中已有的图像（由其他库解码或由程序绘制）
goimgtools.FromImage(img).Resize(400, 0).ToFile("out.png")

// 计算生成或从其他来源读取的像素：提供一个在每次执行时创建流的函数
board := goimgtools.FromSource(func(env *pipeline.Env) (pipeline.Source, error) {
    return &checker{size: 800, cell: 100, env: env}, nil
})
```

## 自定义格式与改写内置格式

注册后，该格式的文件可被 `Open` / `FromBytes` / `FromReader` 识别，`Metadata()` 报告其名称，`ToFile` 按扩展名写出该格式，全部操作与内存上限照常生效；若提供 `Quality` 函数，则同样支持 `MaxFileSize`。

```go
goimgtools.RegisterFormat(goimgtools.Codec{
    Format: goimgtools.Format{Name: "ppm", Extensions: []string{".ppm"}},
    Match:  func(head []byte) bool { return bytes.HasPrefix(head, []byte("P6")) },
    Decode: ppmDecode, // func(in goimgtools.Input, env *pipeline.Env) (pipeline.Source, error)
    Encode: ppmEncode, // func(w io.Writer, src pipeline.Source, o goimgtools.EncodeOptions) error
})

goimgtools.Open("in.jpg").Resize(800, 0).ToFile("out.ppm")
goimgtools.Open("in.ppm").Rotate(90).JPEG().ToFile("out.jpg")
```

可只提供 `Decode`（只读）或只提供 `Encode`（只写）。完整的 PPM 读写实现不足一百行，见 [example_test.go](../../example_test.go)。

**为内置格式添加自定义行为**：取出其 `Codec`，包装需要修改的函数，再重新注册（同名即替换）。包装函数中可调用原函数，相当于覆盖方法后调用父类的实现：

```go
jpeg, _ := goimgtools.CodecByName("jpeg")
encode := jpeg.Encode
jpeg.Encode = func(w io.Writer, src pipeline.Source, o goimgtools.EncodeOptions) error {
    start := time.Now()
    defer func() { log.Printf("jpeg %v 耗时 %v", src.Bounds().Size(), time.Since(start)) }()
    return encode(w, src, o)
}
goimgtools.RegisterFormat(jpeg)
```

## 获取结果，以及在自定义环节中使用库的操作

```go
// 全图载入内存（唯一持有全图之处；计入内存上限，超出时返回 ErrMemoryLimit）
img, err := goimgtools.Open("in.jpg").Resize(400, 0).ToImage()

// 像素流：供自定义的编码、显示或分析代码使用
src, err := goimgtools.Open("in.jpg").Resize(400, 0).Stream(nil)
defer src.Close()
```

反之，若已有一条流并需对其应用库的操作（例如在自定义环节中先行缩小），可将其作为输入再次取流，并传入同一个 `Env`，使整次执行仍只计入一份内存预算：

```go
small, err := goimgtools.FromSource(func(*pipeline.Env) (pipeline.Source, error) {
    return src, nil
}).Resize(200, 0).Stream(src.Env())
```

## 按名称注册操作：`RegisterOperation`、`Do`

与 `RegisterFormat` 注册格式类似，操作也可按名称注册。注册后可通过 `Do("名字", 参数...)` 调用，命令行（见下文 `cli` 包）可通过 `-op 名字:参数` 调用，`goimgtools ops` 会将其列出。内置操作同样具有名称（`resize`、`blur`、`rotate`……，`Operations()` 列出全部），因此 `Do("blur", "2")` 等同于 `Blur(2)`。

```go
goimgtools.RegisterOperation(goimgtools.OperationDef{
    Name: "sepia", Usage: "[STRENGTH]", Help: "棕褐色老照片效果",
    New: func(args ...string) (goimgtools.Operation, error) { /* 根据参数构造操作 */ },
})
goimgtools.Open("in.jpg").Do("resize", "800", "0").Do("sepia", "0.8").ToFile("out.jpg")
```

以内置名称（如 `blur`）注册将替换内置操作：`Do("blur", ...)` 与命令行 `-op blur:...` 改用自定义的操作；`Blur()` 方法本身不受影响（如需修改，见下文钩子）。`UnregisterOperation` 撤销注册并恢复内置操作。若要在内置操作的基础上扩展，先用 `LookupOperation("blur")` 取得内置定义，再在自定义的 `New` 中调用其 `New`（例如限制参数范围后照常模糊）。

## 改写内置行为：`Around` 钩子

钩子包裹配方中的每一步，内置步骤与自定义步骤均包括在内。它接收该步的名称和参数（与 `Do` 接收的相同）、该步的输入，以及执行该步的 `run`；可在执行前修改输入、执行后修改结果，也可不调用 `run`，自行实现整步：

```go
// 所有模糊替换为自定义实现，其余步骤照常执行
myBlur := func(step goimgtools.Step, in pipeline.Source, run func(pipeline.Source) (pipeline.Source, error)) (pipeline.Source, error) {
    if step.Name == "blur" {
        sigma, _ := strconv.ParseFloat(step.Args[0], 64)
        return gpuBlur(in, sigma), nil
    }
    return run(in)
}
goimgtools.Open("in.jpg").Resize(800, 0).Blur(2).Around(myBlur).ToFile("out.jpg")
```

与嵌入 `*Image` 结合，即相当于子类重写父类方法：自定义类型在构造函数中安装钩子，此后由其构建的每个配方都带有改写后的行为。[example_test.go](../../example_test.go) 中的 `Example_overriding` 在每次缩小后自动进行轻度锐化。

- 最先添加的钩子位于最外层。存在钩子时，各步严格按书写顺序逐一执行（无钩子时，「转向（90 度倍数的旋转或翻转）后紧跟缩放」会合并为先缩放后转向，以节省内存）；配方以缩放开头时，JPEG 解码阶段的缩小仍然有效。
- 钩子中出错或 panic、`run` 被调用两次、返回 nil，均作为本次执行的错误处理；该步已构建的对象由库关闭，内存预算全部归还，不留下临时文件（有测试覆盖）。
- `OutputSize` 及 GIF 动画的每一帧同样经过钩子。

## 包含自定义操作的命令行程序：`cli` 包

命令行程序本身即 `cli` 包，`cmd/goimgtools` 仅有一行 `cli.Main()`。程序注册操作后将控制权交给 `cli.Main()`，即得到包含这些操作的 goimgtools：

```go
package main

import (
    "github.com/whhhh1500/goimgtools"
    "github.com/whhhh1500/goimgtools/cli"
)

func main() {
    goimgtools.RegisterOperation(sepiaDef) // 定义方式同上文示例
    cli.Main()                             // 此时可用 -op sepia:0.8，goimgtools ops 也会将其列出
}
```

`cli.Run(args, stdout, stderr)` 功能相同但不退出进程，便于测试或嵌入其他程序。

## 扩展代码出错时的处理

自定义操作和格式返回的错误由 `ToFile` 等方法原样返回。若扩展代码（或库本身）在处理某个输入时发生 panic，本次执行返回 `*goimgtools.PanicError`（含原因与堆栈），而不会导致整个程序退出；临时文件与未写完的输出照常清理，多线程时同样如此。
