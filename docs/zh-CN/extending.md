[English](../extending.md) | 中文

# 扩展：在这个库上面加自己的东西

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
| 给自己的操作起名字，像内置操作一样用名字调用（`Do`、命令行 `-op`） | `RegisterOperation(def)`、`Do(name, 参数...)`、`Operations()` |
| 改写内置操作的行为：在每一步前后加自己的处理，或者整步换成自己的做法（相当于「重写方法」） | `Around(hook)` |
| 带上自己操作的命令行程序 | `cli` 包：注册后调用 `cli.Main()` |

下面的例子都只用公开的包，[example_test.go](../../example_test.go) 里有能直接运行的完整版本（`go test -run Example`）。

## 只改像素值：`Pixels`

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

## 任意操作：`Pipe` 和像素流

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

操作可以改尺寸（`OutputSize()` 会报出它给的尺寸），可以放在链的任何位置。测试里有一个完整的「加边框」操作（[tests/extend_test.go](../../tests/extend_test.go) 的 `border`）可以照着写。

`pipeline` 包里还有写环节用得上的零件：`Map`（`Pixels` 的底层）、`RowReader` / `Rechunk`（按自己要的行数重新切带）、`NewBand` / `CopyRows`（按像素类型分配和拷贝）、`Spill` / `Scratch`（超出内存份额就自动落到临时文件的存储）、`env.Parallel(n, fn)`（把一批互不相干的工作分给这条链空闲的核，`Threads(4)` 起才有；没有空闲的核时就在当前线程依次做，所以总核数不会超，也不会卡住）。

```go
env := pipeline.EnvOf(src)
return pipeline.Map(src, func(strip *image.NRGBA) error {
    rows := strip.Rect.Dy()
    env.Parallel(4, func(k int) { // 每份四分之一的行
        for y := k * rows / 4; y < (k+1)*rows/4; y++ {
            invert(strip.Pix[y*strip.Stride:][:4*strip.Rect.Dx()])
        }
    })
    return nil
}), nil
```

**写环节要守的几条规则**（库里每个环节都靠它们）：

- 带按行号递增给出，每条横跨整个宽度，合起来正好覆盖一次；之后返回 `io.EOF`。
- 一条带只在下一次 `Next` 或 `Close` 之前有效，要留着就自己拷贝。
- 创建环节时不读像素（要便宜、尺寸立刻可知），第一次 `Next` 才开始读。
- `Close` 要关掉自己读的那条流，并归还占用的内存预算；只会被调用一次，出错后也会调。
- 会随图片变大的缓冲区（一条带、一整行、要留到最后的数据），分配前先 `Env().Budget.Reserve`，释放时 `Release`；可能很大的放进 `Spill` 或 `Scratch`。内存上限就是靠这一条守住的。

## 自己的类型：嵌入 `*Image`

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

## 自己的输入

```go
// 内存里已有的图（别的库解出来的、程序画的）
goimgtools.FromImage(img).Resize(400, 0).ToFile("out.png")

// 算出来或从别处读来的像素：给一个「每次执行时造出一条流」的函数
board := goimgtools.FromSource(func(env *pipeline.Env) (pipeline.Source, error) {
    return &checker{size: 800, cell: 100, env: env}, nil
})
```

## 自己的格式，或者改写内置格式

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

可以只给 `Decode`（只读）或只给 `Encode`（只写）。完整的 PPM 读写不到一百行，见 [example_test.go](../../example_test.go)。

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

## 取出结果，以及在自己的环节里用库的操作

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

## 按名字注册操作：`RegisterOperation`、`Do`

和 `RegisterFormat` 注册格式一样，操作也可以按名字注册。注册后 `Do("名字", 参数...)` 就能用，命令行（见下面的 `cli` 包）也能用 `-op 名字:参数` 调用，`goimgtools ops` 会列出来。内置操作也都有名字（`resize`、`blur`、`rotate`……，`Operations()` 列出全部），所以 `Do("blur", "2")` 就是 `Blur(2)`。

```go
goimgtools.RegisterOperation(goimgtools.OperationDef{
    Name: "sepia", Usage: "[STRENGTH]", Help: "棕色老照片效果",
    New: func(args ...string) (goimgtools.Operation, error) { /* 按参数做出操作 */ },
})
goimgtools.Open("in.jpg").Do("resize", "800", "0").Do("sepia", "0.8").ToFile("out.jpg")
```

用内置的名字注册（比如 `blur`）就替换掉它：`Do("blur", ...)` 和命令行 `-op blur:...` 改用你的；`Blur()` 方法本身不变（要改它见下面的钩子）。`UnregisterOperation` 撤销后恢复内置。要在内置操作的基础上加自己的想法，先用 `LookupOperation("blur")` 取出内置的定义，在自己的 `New` 里调用它的 `New`（比如限制参数范围后照常模糊）。

## 改写内置行为：`Around` 钩子

钩子包在配方的每一步外面，内置的和自己的都算。它拿到这一步的名字和参数（和 `Do` 收的一样）、这一步的输入，和一个执行这一步的 `run`；可以在前面改输入、在后面改结果，也可以不调用 `run`、整步用自己的做法：

```go
// 所有模糊都换成自己的实现，其余步骤照常
myBlur := func(step goimgtools.Step, in pipeline.Source, run func(pipeline.Source) (pipeline.Source, error)) (pipeline.Source, error) {
    if step.Name == "blur" {
        sigma, _ := strconv.ParseFloat(step.Args[0], 64)
        return gpuBlur(in, sigma), nil
    }
    return run(in)
}
goimgtools.Open("in.jpg").Resize(800, 0).Blur(2).Around(myBlur).ToFile("out.jpg")
```

配合嵌入 `*Image`，就是「子类重写父类方法」：自己的类型在构造函数里装上钩子，之后用它做的每个配方都带着改写过的行为。[example_test.go](../../example_test.go) 里的 `Example_overriding` 让每次缩小之后自动轻度锐化。

- 先加的钩子在最外层。有钩子时各步严格按写的顺序一步步做（没有钩子时「转向后紧跟缩放」会合并成先缩放再转向，省内存）；配方以缩放开头时 JPEG 解码时缩小仍然有效。
- 钩子里出错、panic、`run` 调了两次、返回空，都是这次执行的错误；这一步已经做出来的东西由库关闭，内存预算全部归还、不留临时文件（测试覆盖）。
- `OutputSize` 和动图的每一帧也会经过钩子。

## 带上自己操作的命令行程序：`cli` 包

命令行程序本身就是 `cli` 包，`cmd/goimgtools` 只有一行 `cli.Main()`。自己的程序注册好操作再交给它，就是一个带着这些操作的 goimgtools：

```go
package main

import (
    "github.com/whhhh1500/goimgtools"
    "github.com/whhhh1500/goimgtools/cli"
)

func main() {
    goimgtools.RegisterOperation(sepiaDef) // 上面那样的定义
    cli.Main()                             // 现在有 -op sepia:0.8，goimgtools ops 也会列出它
}
```

`cli.Run(args, stdout, stderr)` 做同样的事但不退出进程，方便测试或嵌进别的程序。

## 扩展代码出错时

自己的操作、格式返回的错误会原样从 `ToFile` 等方法返回。如果扩展代码（或库本身）在处理某个输入时 panic，这次执行返回 `*goimgtools.PanicError`（带原因和堆栈），不会让整个程序退出，临时文件和未写完的输出照常清理——开了多线程也一样。
