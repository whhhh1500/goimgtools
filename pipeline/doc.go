// Package pipeline is what goimgtools is built on, open for building on it
// further: the stream of pixel rows every decoder, operation and encoder of
// the library speaks, the memory budget they share, and the working storage
// that moves to a temporary file when the budget is used up.
//
// It is the package to import when the library is to do something it does
// not do by itself: an operation of your own (goimgtools.Image.Pipe), an
// input of your own (goimgtools.FromSource), a file format of your own
// (goimgtools.RegisterFormat), or an output of your own
// (goimgtools.Image.Stream). Whoever only opens, resizes and saves images
// needs nothing from here.
//
// # The model
//
// An image is never held whole. It flows from the decoder to the encoder as
// strips: a few rows at a time, top to bottom, each an ordinary image.Image
// that spans the full width. A Source hands out those strips. An operation
// is a Source that reads another Source:
//
//	decoder → resize → your stage → watermark → encoder
//
// Each stage keeps only what it needs to make its next strip, so the memory
// a run takes depends on the width of the image and hardly on its height.
//
// # Writing a stage
//
// The quickest way is Map, which hands every strip to a function as plain
// 8-bit RGBA bytes:
//
//	func Invert(src pipeline.Source) (pipeline.Source, error) {
//		return pipeline.Map(src, func(strip *image.NRGBA) error {
//			for i := 0; i < len(strip.Pix); i += 4 {
//				strip.Pix[i] = 255 - strip.Pix[i]
//				strip.Pix[i+1] = 255 - strip.Pix[i+1]
//				strip.Pix[i+2] = 255 - strip.Pix[i+2]
//			}
//			return nil
//		}), nil
//	}
//
// A stage that changes more than the pixel values (the size, the number of
// rows it needs at once) implements Source itself. Embedding the Source it
// reads gives it every method it does not change, the way a subclass
// inherits from its base class:
//
//	type countRows struct {
//		pipeline.Source     // Bounds, ColorModel, Close and Env come from here
//		rows *int
//	}
//
//	func (c countRows) Next() (image.Image, error) {
//		strip, err := c.Source.Next()
//		if err == nil {
//			*c.rows += strip.Bounds().Dy()
//		}
//		return strip, err
//	}
//
// # The rules of a Source
//
// They are few, and every stage of the library relies on them:
//
//   - Strips come in increasing row order, span the full width of Bounds
//     and together cover Bounds exactly once. Next then returns io.EOF.
//   - A strip is valid until the next call of Next or Close. A stage that
//     needs rows for longer copies them.
//   - Nothing is read when a stage is made: making it must be cheap, and
//     its Bounds must be known at once. Reading starts with the first Next.
//   - Close closes the Source a stage reads, and gives back what the stage
//     reserved from the budget. It is called exactly once, also after an
//     error.
//   - A buffer that grows with the image (a strip, a row of the full
//     width, anything kept until the end) is reserved from Env().Budget
//     before it is allocated, and released when it is freed. What may be
//     large goes into a Spill or a Scratch, which use a temporary file
//     when the budget has no room. This is the whole of how the memory
//     limit holds, so a stage that skips it breaks the limit for everyone.
//   - Env returns the Env of the Source a stage reads, so that the whole
//     pipeline accounts against one budget.
//
// Strips may be of any of the standard library's image types; which one a
// Source hands out is fixed for its whole run. NewBand, BandBytes, CopyRows
// and Pix work on all of them, RowReader and Rechunk re-cut strips to the
// rows a stage wants to see together.
package pipeline
