English | [中文](zh-CN/extending.md)

# Extending

The library is built so that you can add features without changing its code. Every stage has an opening for this, and the library's own features use the same opening: the six formats sit in the same table as the formats you register, and built-in operations are the same kind of thing as the operations you write.

| What you want to do | What to use |
|---|---|
| An operation that only changes pixel values (modulate, grayscale, invert, per-pixel filters) | `Pixels(fn)` |
| Any operation (change the size, add a border, statistics, progress…) | `Pipe(op)`; an operation is a stage on a `pipeline.Source` |
| Give a group of steps a name and use it in the middle of a chain | `Apply(steps...)` |
| Your own type, with all the methods of `Image` plus new ones (the equivalent of inheritance) | Embed `*goimgtools.Image` |
| Your own input (an image in memory, a computed image, pixels read from elsewhere) | `FromImage(img)`, `FromSource(open)` |
| Your own file format; or override how the library reads / writes one of its formats | `RegisterFormat(codec)`, `CodecByName(name)`, `ToFormat(name, options)` |
| Your own resize filter | `ResizeOptions{Filter: &goimgtools.Filter{...}}` |
| Hand the result to your own code instead of writing a file | `Stream(env)` (pixel stream), `ToImage()` (a whole `image.Image`) |
| Give your own operation a name and call it by name like a built-in operation (`Do`, command line `-op`) | `RegisterOperation(def)`, `Do(name, args...)`, `Operations()` |
| Override the behaviour of built-in operations: add your own processing before and after each step, or replace a whole step with your own approach (the equivalent of overriding a method) | `Around(hook)` |
| A command-line program that carries your own operations | `cli` package: register, then call `cli.Main()` |

The examples below use only public packages. [example_test.go](../example_test.go) has complete versions you can run directly (`go test -run Example`).

## Changing pixel values only: `Pixels`

Whatever the image's own storage (JPEG's YCbCr, a palette, 16-bit…), the function always receives 8-bit NRGBA, one strip at a time. Modify it in place:

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

This adds only one strip of memory (about 256KB), counted within the memory limit.

## Any operation: `Pipe` and the pixel stream

Inside the library, an image travels from decoding to encoding as a pixel stream: a few rows at a time, from top to bottom. An operation is a stage that reads one stream and gives out another. The interface is in the public package `github.com/whhhh1500/goimgtools/pipeline`:

```go
type Source interface {
    Bounds() image.Rectangle      // bounds of the whole image
    ColorModel() color.Model
    Next() (image.Image, error)   // next strip; returns io.EOF at the end
    Close() error
    Env() *Env                    // memory budget and temporary directory for this run
}
```

When you write your own stage, **embed** the stream it reads. Methods you do not change carry over automatically; override only the ones you need. This is the same as inheriting from a class and overriding a few methods:

```go
// counts how many rows pass through
type rowCounter struct {
    pipeline.Source // Bounds, ColorModel and Env all come from here
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

An operation can change the size (`OutputSize()` reports the size it gives) and can sit anywhere in the chain. The tests have a complete "add a border" operation (`border` in [tests/extend_test.go](../tests/extend_test.go)) that you can follow.

The `pipeline` package also has parts that help when you write a stage: `Map` (what `Pixels` is built on), `RowReader` / `Rechunk` (re-cut strips to the number of rows you want), `NewBand` / `CopyRows` (allocate and copy by pixel type), `Spill` / `Scratch` (storage that moves to a temporary file automatically once it exceeds its share of memory), `env.Parallel(n, fn)` (spreads a batch of independent pieces of work over this chain's idle cores; this only happens from `Threads(4)` up. When no core is idle, the work runs in turn on the current thread, so the total number of cores is never exceeded and nothing stalls).

```go
env := pipeline.EnvOf(src)
return pipeline.Map(src, func(strip *image.NRGBA) error {
    rows := strip.Rect.Dy()
    env.Parallel(4, func(k int) { // each part is a quarter of the rows
        for y := k * rows / 4; y < (k+1)*rows/4; y++ {
            invert(strip.Pix[y*strip.Stride:][:4*strip.Rect.Dx()])
        }
    })
    return nil
}), nil
```

**Rules a stage must follow** (every stage in the library relies on them):

- Strips are given in increasing row order, each spans the full width, and together they cover the image exactly once; after that, return `io.EOF`.
- A strip is valid only until the next `Next` or `Close`. If you need to keep it, copy it yourself.
- Do not read pixels when the stage is created (creation must be cheap and the size known at once); start reading at the first `Next`.
- `Close` must close the stream the stage reads and return the memory budget it holds. It is called only once, and it is called after an error too.
- For buffers that grow with the image (a strip, a full row, data that must be kept until the end), call `Env().Budget.Reserve` before allocating and `Release` when freeing. Put anything that may be large into `Spill` or `Scratch`. This rule is what holds the memory limit.

## Your own type: embedding `*Image`

The chain methods modify the same `Image`, so once you embed it in your own struct, the existing methods work as before and you add your own:

```go
type Product struct {
    *goimgtools.Image
    SKU string
}

func OpenProduct(path, sku string) Product {
    return Product{goimgtools.Open(path).Rotate(), sku}
}

// Listing adds the shop's standard steps
func (p Product) Listing() Product {
    p.Resize(300, 300, goimgtools.ResizeOptions{Fit: goimgtools.FitContain, Background: color.White}).
        Composite(goimgtools.Text(p.SKU, goimgtools.TextOptions{Size: 14}),
            goimgtools.CompositeOptions{Gravity: goimgtools.SouthEast, Left: -6, Top: -6})
    return p
}

p := OpenProduct("in.jpg", "SKU-0042")
p.Flatten(color.White)            // a method of Image itself
err := p.Listing().JPEG(85).ToFile("out.jpg")
```

If you only want to reuse a group of steps and do not need a new type, write it as a function and use `Apply`:

```go
func Thumbnail(size int) func(*goimgtools.Image) *goimgtools.Image {
    return func(m *goimgtools.Image) *goimgtools.Image {
        return m.Rotate().Resize(size, size, goimgtools.ResizeOptions{Fit: goimgtools.FitCover})
    }
}
goimgtools.Open("in.jpg").Apply(Thumbnail(200)).JPEG(80).ToFile("out.jpg")
```

## Your own input

```go
// an image already in memory (decoded by another library, drawn by your program)
goimgtools.FromImage(img).Resize(400, 0).ToFile("out.png")

// computed pixels, or pixels read from elsewhere: give a function that "builds a stream on each run"
board := goimgtools.FromSource(func(env *pipeline.Env) (pipeline.Source, error) {
    return &checker{size: 800, cell: 100, env: env}, nil
})
```

## Your own format, or overriding a built-in format

Once registered, files in this format are recognised by `Open` / `FromBytes` / `FromReader`, `Metadata()` reports its name, `ToFile` writes it by extension, and all operations and the memory limit work as usual. If you give a `Quality` function, `MaxFileSize` works too.

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

You can give only `Decode` (read only) or only `Encode` (write only). A complete PPM reader and writer takes under a hundred lines; see [example_test.go](../example_test.go).

**Adding your own behaviour to a built-in format**: take its `Codec`, wrap the function you want to change, and register it again (the same name replaces it). Inside, you can call the original function, the same as overriding a method and then calling the parent class's version:

```go
jpeg, _ := goimgtools.CodecByName("jpeg")
encode := jpeg.Encode
jpeg.Encode = func(w io.Writer, src pipeline.Source, o goimgtools.EncodeOptions) error {
    start := time.Now()
    defer func() { log.Printf("jpeg %v took %v", src.Bounds().Size(), time.Since(start)) }()
    return encode(w, src, o)
}
goimgtools.RegisterFormat(jpeg)
```

## Getting the result, and using the library's operations inside your own stage

```go
// the whole image in memory (the only place that holds a whole image; counted within the memory limit; returns ErrMemoryLimit if it does not fit)
img, err := goimgtools.Open("in.jpg").Resize(400, 0).ToImage()

// pixel stream: hand it to your own encoder, display or analysis
src, err := goimgtools.Open("in.jpg").Resize(400, 0).Stream(nil)
defer src.Close()
```

The other way round: if you already have a stream and want to apply the library's operations to it (for example, shrinking first inside your own stage), use it as an input, take a stream again, and pass in the same `Env`. That way the whole run still counts against a single memory budget:

```go
small, err := goimgtools.FromSource(func(*pipeline.Env) (pipeline.Source, error) {
    return src, nil
}).Resize(200, 0).Stream(src.Env())
```

## Registering operations by name: `RegisterOperation`, `Do`

Just as `RegisterFormat` registers formats, operations can be registered by name. Once registered, `Do("name", args...)` works, the command line (see the `cli` package below) can call it with `-op name:args`, and `goimgtools ops` lists it. Built-in operations have names too (`resize`, `blur`, `rotate`…; `Operations()` lists them all), so `Do("blur", "2")` is `Blur(2)`.

```go
goimgtools.RegisterOperation(goimgtools.OperationDef{
    Name: "sepia", Usage: "[STRENGTH]", Help: "an old sepia photograph look",
    New: func(args ...string) (goimgtools.Operation, error) { /* build the operation from the arguments */ },
})
goimgtools.Open("in.jpg").Do("resize", "800", "0").Do("sepia", "0.8").ToFile("out.jpg")
```

Registering under a built-in name (for example `blur`) replaces it: `Do("blur", ...)` and the command line `-op blur:...` use yours instead. The `Blur()` method itself does not change (to change it, see hooks below). `UnregisterOperation` undoes this and restores the built-in one. To build your own idea on top of a built-in operation, first take the built-in definition with `LookupOperation("blur")`, then call its `New` inside your own `New` (for example, limit the argument range, then blur as usual).

## Overriding built-in behaviour: the `Around` hook

A hook wraps every step of the recipe, built-in steps and your own alike. It receives the step's name and arguments (the same as `Do` takes), the step's input, and a `run` that performs the step. It can change the input before, change the result after, or skip `run` and do the whole step its own way:

```go
// replace every blur with your own implementation; other steps run as usual
myBlur := func(step goimgtools.Step, in pipeline.Source, run func(pipeline.Source) (pipeline.Source, error)) (pipeline.Source, error) {
    if step.Name == "blur" {
        sigma, _ := strconv.ParseFloat(step.Args[0], 64)
        return gpuBlur(in, sigma), nil
    }
    return run(in)
}
goimgtools.Open("in.jpg").Resize(800, 0).Blur(2).Around(myBlur).ToFile("out.jpg")
```

Combined with embedding `*Image`, this is a subclass overriding a parent-class method: your own type installs the hook in its constructor, and every recipe made with it then carries the overridden behaviour. `Example_overriding` in [example_test.go](../example_test.go) applies a mild sharpen automatically after every shrink.

- The hook added first is the outermost. With hooks, the steps run strictly one by one in the order written (without hooks, "a rotation directly followed by a resize" is merged into resize first, then rotate, to save memory). When a recipe starts with a resize, shrinking during JPEG decoding still applies.
- An error or panic in a hook, `run` called twice, or a nil return are all errors of this run. Whatever the step has already built is closed by the library, the whole memory budget is returned, and no temporary files are left behind (covered by tests).
- `OutputSize` and every frame of an animated GIF also pass through hooks.

## A command-line program with your own operations: the `cli` package

The command-line program is itself the `cli` package; `cmd/goimgtools` is just one line, `cli.Main()`. If your own program registers its operations and then hands over to it, you get a goimgtools that carries those operations:

```go
package main

import (
    "github.com/whhhh1500/goimgtools"
    "github.com/whhhh1500/goimgtools/cli"
)

func main() {
    goimgtools.RegisterOperation(sepiaDef) // a definition like the one above
    cli.Main()                             // now -op sepia:0.8 exists, and goimgtools ops lists it too
}
```

`cli.Run(args, stdout, stderr)` does the same but does not exit the process, which makes it easy to test or to embed in another program.

## When extension code fails

Errors returned by your own operations and formats come back unchanged from `ToFile` and the other methods. If extension code (or the library itself) panics while processing an input, the run returns `*goimgtools.PanicError` (with the cause and the stack trace) instead of bringing the whole program down. Temporary files and unfinished output are cleaned up as usual, and the same holds with multiple threads.
