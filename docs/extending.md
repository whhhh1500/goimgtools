English | [中文](zh-CN/extending.md)

# Extending

One of the library's design goals is that functionality can be extended without modifying its source. Every stage of processing provides an extension point, and the built-in features use the same extension points: the six built-in formats are held in the same table as registered formats, and built-in operations are of the same kind as custom operations.

| Purpose | API |
|---|---|
| An operation that changes pixel values only (colour modulation, grayscale, inversion, per-pixel filters) | `Pixels(fn)` |
| An arbitrary operation (resizing, adding a border, statistics, progress reporting…) | `Pipe(op)`; an operation is a stage over a `pipeline.Source` |
| Name a group of steps and use it within a chain | `Apply(steps...)` |
| A custom type with all the methods of `Image` plus new ones (equivalent to inheritance) | Embed `*goimgtools.Image` |
| A custom input (an image in memory, a computed image, pixels read from another source) | `FromImage(img)`, `FromSource(open)` |
| A custom file format, or overriding how the library reads or writes one of its formats | `RegisterFormat(codec)`, `CodecByName(name)`, `ToFormat(name, options)` |
| A custom resize filter | `ResizeOptions{CustomKernel: &goimgtools.CustomKernel{...}}` |
| Pass the result to the calling code instead of writing a file | `Stream(env)` (pixel stream), `ToImage()` (a complete `image.Image`) |
| Name a custom operation and call it by name like a built-in operation (`Do`, command-line `-op`) | `RegisterOperation(def)`, `Do(name, args...)`, `Operations()` |
| Override the behaviour of built-in operations: add custom processing before and after each step, or replace an entire step with a custom implementation (equivalent to overriding a method) | `Around(hook)` |
| A command-line program that includes custom operations | `cli` package: register the operations, then call `cli.Main()` |

The examples below use public packages only. Complete, runnable versions are in [example_test.go](../example_test.go) (`go test -run Example`).

## Pixel values only: `Pixels`

Regardless of how the image is stored (YCbCr in JPEG, a palette, 16-bit…), the function always receives 8-bit NRGBA, one strip at a time, and modifies it in place:

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

The additional memory is a single strip (about 256KB), which counts against the memory limit.

## Arbitrary operations: `Pipe` and the pixel stream

Within the library, an image passes from decoder to encoder as a pixel stream, delivered a few rows at a time from top to bottom. An operation is a stage that reads one stream and produces another. The interface is defined in the public package `github.com/whhhh1500/goimgtools/pipeline`:

```go
type Source interface {
    Bounds() image.Rectangle      // bounds of the whole image
    ColorModel() color.Model
    Next() (image.Image, error)   // next strip; returns io.EOF at the end
    Close() error
    Env() *Env                    // memory budget and temporary directory of this run
}
```

A custom stage should **embed** the stream it reads: methods that are not overridden carry over automatically, and only those that need to change are overridden. This is equivalent to inheriting from a class and overriding some of its methods:

```go
// counts the rows that pass through
type rowCounter struct {
    pipeline.Source // supplies Bounds, ColorModel and Env
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

An operation may change the image size (`OutputSize()` reports the size it produces) and may be placed anywhere in the chain. The tests include a complete border operation (`border` in [tests/extend_test.go](../tests/extend_test.go)) that can serve as a reference.

The `pipeline` package also provides components for writing stages: `Map` (the basis of `Pixels`), `RowReader` / `Rechunk` (re-split strips into a chosen number of rows), `NewBand` / `CopyRows` (allocation and copying by pixel type), `Spill` / `Scratch` (storage that moves to a temporary file automatically once it exceeds its share of memory), `env.Parallel(n, fn)` (distributes a batch of independent tasks across the idle cores of this chain, and those that become idle while tasks are left, from `Threads(4)` upwards only; when no core is idle, the tasks run sequentially on the current thread, so the total number of cores is never exceeded and execution never blocks), and `env.Go(fn)` / `env.TryGo(fn)` (run work a stage can go on without, such as compressing a block while it fills the next, on an idle core: `Go` runs it on the current thread when none is idle, `TryGo` then leaves it for the stage to try again later).

```go
env := pipeline.EnvOf(src)
return pipeline.Map(src, func(strip *image.NRGBA) error {
    rows := strip.Rect.Dy()
    env.Parallel(4, func(k int) { // each task covers a quarter of the rows
        for y := k * rows / 4; y < (k+1)*rows/4; y++ {
            invert(strip.Pix[y*strip.Stride:][:4*strip.Rect.Dx()])
        }
    })
    return nil
}), nil
```

**Rules for stages** (every stage in the library depends on them):

- Strips are delivered in increasing row order, each spans the full width, and together they cover the image exactly once; after that, `io.EOF` is returned.
- A strip remains valid only until the next call to `Next` or `Close`; a stage that needs to retain it must copy it.
- Do not read pixels when the stage is created (creation must be inexpensive and the size known immediately); reading begins at the first `Next`.
- `Close` must close the stream the stage reads and return the memory budget it holds. It is called only once, and is also called after an error.
- For buffers that grow with the image (a strip, a full row, data retained until the end), call `Env().Budget.Reserve` before allocation and `Release` when freeing; data that may be large belongs in `Spill` or `Scratch`. The memory limit is enforced by this rule.

## Custom types: embedding `*Image`

Chain methods modify the same `Image`, so after it is embedded in a custom struct, the existing methods work unchanged and new methods can be added:

```go
type Product struct {
    *goimgtools.Image
    SKU string
}

func OpenProduct(path, sku string) Product {
    return Product{goimgtools.Open(path).AutoOrient(), sku}
}

// Listing appends the shop's standard steps
func (p Product) Listing() Product {
    p.Resize(300, 300, goimgtools.ResizeOptions{Fit: goimgtools.FitContain, Background: color.White}).
        Composite(goimgtools.Text(p.SKU, goimgtools.TextOptions{Size: 14}),
            goimgtools.CompositeOptions{Gravity: goimgtools.SouthEast, Left: -6, Top: -6})
    return p
}

p := OpenProduct("in.jpg", "SKU-0042")
p.Flatten(color.White)            // a method of Image itself
err := p.Listing().JPEG(goimgtools.JPEGOptions{Quality: 85}).ToFile("out.jpg")
```

To reuse a group of steps without defining a new type, write them as a function and apply it with `Apply`:

```go
func Thumbnail(size int) func(*goimgtools.Image) *goimgtools.Image {
    return func(m *goimgtools.Image) *goimgtools.Image {
        return m.AutoOrient().Resize(size, size, goimgtools.ResizeOptions{Fit: goimgtools.FitCover})
    }
}
goimgtools.Open("in.jpg").Apply(Thumbnail(200)).JPEG().ToFile("out.jpg")
```

## Custom inputs

```go
// an image already in memory (decoded by another library or drawn by the program)
goimgtools.FromImage(img).Resize(400, 0).ToFile("out.png")

// computed pixels or pixels from another source: supply a function that builds a stream on each run
board := goimgtools.FromSource(func(env *pipeline.Env) (pipeline.Source, error) {
    return &checker{size: 800, cell: 100, env: env}, nil
})
```

## Custom formats and overriding built-in formats

Once a format is registered, its files are recognised by `Open` / `FromBytes` / `FromReader`, `Metadata()` reports its name, `ToFile` writes it by extension, and all operations and the memory limit apply as usual. If a `Quality` function is supplied, `MaxFileSize` is also supported.

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

Either `Decode` alone (read-only) or `Encode` alone (write-only) may be supplied. A complete PPM reader and writer takes fewer than a hundred lines; see [example_test.go](../example_test.go).

**Adding custom behaviour to a built-in format**: obtain its `Codec`, wrap the function to be changed, and register it again (registering under the same name replaces the original). The wrapper may call the original function, which is equivalent to overriding a method and calling the parent class's implementation:

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

## Retrieving results, and using library operations within a custom stage

```go
// the whole image in memory (the only case that holds a whole image; counts against the memory limit; returns ErrMemoryLimit if it does not fit)
img, err := goimgtools.Open("in.jpg").Resize(400, 0).ToImage()

// pixel stream: for a custom encoder, display or analysis
src, err := goimgtools.Open("in.jpg").Resize(400, 0).Stream(nil)
defer src.Close()
```

Conversely, to apply the library's operations to an existing stream (for example, to shrink the image first within a custom stage), use the stream as an input, obtain a stream from it again, and pass the same `Env`, so that the entire run still counts against a single memory budget:

```go
small, err := goimgtools.FromSource(func(*pipeline.Env) (pipeline.Source, error) {
    return src, nil
}).Resize(200, 0).Stream(src.Env())
```

## Registering operations by name: `RegisterOperation`, `Do`

As `RegisterFormat` registers formats, operations can be registered by name. A registered operation can be invoked with `Do("name", args...)`, called from the command line (see the `cli` package below) with `-op name:args`, and is listed by `goimgtools ops`. Built-in operations also have names (`resize`, `blur`, `rotate`…; `Operations()` lists them all), so `Do("blur", "2")` is equivalent to `Blur(2)`.

```go
goimgtools.RegisterOperation(goimgtools.OperationDef{
    Name: "sepia", Usage: "[STRENGTH]", Help: "sepia-toned vintage photograph effect",
    New: func(args ...string) (goimgtools.Operation, error) { /* build the operation from the arguments */ },
})
goimgtools.Open("in.jpg").Do("resize", "800", "0").Do("sepia", "0.8").ToFile("out.jpg")
```

Registering under a built-in name (for example `blur`) replaces the built-in operation: `Do("blur", ...)` and the command-line `-op blur:...` then use the custom one. The `Blur()` method itself is unaffected (to change it, see hooks below). `UnregisterOperation` reverts this and restores the built-in operation. To build on a built-in operation, first obtain its definition with `LookupOperation("blur")`, then call its `New` from within the custom `New` (for example, to restrict the argument range and then blur as usual).

## Overriding built-in behaviour: the `Around` hook

A hook wraps every step of the recipe, both built-in and custom. It receives the step's name and arguments (the same as those accepted by `Do`), the step's input, and a `run` function that performs the step. It may modify the input beforehand or the result afterwards, or skip `run` and implement the entire step itself:

```go
// replaces every blur with a custom implementation; other steps run as usual
myBlur := func(step goimgtools.Step, in pipeline.Source, run func(pipeline.Source) (pipeline.Source, error)) (pipeline.Source, error) {
    if step.Name == "blur" {
        sigma, _ := strconv.ParseFloat(step.Args[0], 64)
        return gpuBlur(in, sigma), nil
    }
    return run(in)
}
goimgtools.Open("in.jpg").Resize(800, 0).Blur(2).Around(myBlur).ToFile("out.jpg")
```

Combined with embedding `*Image`, this is equivalent to a subclass overriding a parent-class method: the custom type installs the hook in its constructor, and every recipe built from it then carries the overridden behaviour. `Example_overriding` in [example_test.go](../example_test.go) automatically applies a mild sharpen after every shrink.

- The hook added first is the outermost. When hooks are present, steps run strictly one at a time in the order written (without hooks, an orientation change (rotation by a multiple of 90° or a flip) immediately followed by a resize is merged into a resize followed by the orientation change, to save memory). When a recipe begins with a resize, shrinking during JPEG decoding still applies.
- An error or panic in a hook, calling `run` twice, or returning nil is treated as an error of the current run. Anything the step has already built is closed by the library, the entire memory budget is returned, and no temporary files remain (covered by tests).
- `OutputSize` and every frame of an animated GIF also pass through the hooks.

## Command-line programs with custom operations: the `cli` package

The command-line program is itself the `cli` package; `cmd/goimgtools` consists of a single line, `cli.Main()`. A program that registers its operations and then hands control to `cli.Main()` becomes a goimgtools that includes those operations:

```go
package main

import (
    "github.com/whhhh1500/goimgtools"
    "github.com/whhhh1500/goimgtools/cli"
)

func main() {
    goimgtools.RegisterOperation(sepiaDef) // a definition such as the one above
    cli.Main()                             // -op sepia:0.8 is now available and listed by goimgtools ops
}
```

`cli.Run(args, stdout, stderr)` behaves the same but does not exit the process, which makes it suitable for testing or for embedding in another program.

## Errors in extension code

Errors returned by custom operations and formats are returned unchanged by `ToFile` and the other methods. If extension code (or the library itself) panics while processing an input, the run returns `*goimgtools.PanicError` (containing the cause and the stack trace) instead of terminating the whole program. Temporary files and incomplete output are cleaned up as usual, including with multiple threads.
