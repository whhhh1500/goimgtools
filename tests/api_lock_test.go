package tests

import (
	"context"
	"image"
	"image/color"
	"io"

	. "github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/inspect"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// The basic API, held to its signatures: changing any of these breaks the
// build here, before the api job of the CI (apidiff against .github/api-base)
// fails on it too. The exported API only grows (see CONTRIBUTING.md); a new
// need gets a new name, option or field.
var (
	_ func(string) *Image      = Open
	_ func([]byte) *Image      = FromBytes
	_ func(io.Reader) *Image   = FromReader
	_ func(image.Image) *Image = FromImage

	_ func(*Image) *Image                                                = (*Image).Clone
	_ func(*Image, context.Context) *Image                               = (*Image).WithContext
	_ func(*Image, int) *Image                                           = (*Image).Threads
	_ func(*Image, int64) *Image                                         = (*Image).MemoryLimit
	_ func(*Image, int, int, ...ResizeOptions) *Image                    = (*Image).Resize
	_ func(*Image, int, int, int, int) *Image                            = (*Image).Crop
	_ func(*Image, float64, ...RotateOptions) *Image                     = (*Image).Rotate
	_ func(*Image) *Image                                                = (*Image).AutoOrient
	_ func(*Image) *Image                                                = (*Image).Flip
	_ func(*Image) *Image                                                = (*Image).Flop
	_ func(*Image, int, int, int, int, ...ExtendOptions) *Image          = (*Image).Extend
	_ func(*Image, float64, ...TrimOptions) *Image                       = (*Image).Trim
	_ func(*Image, color.Color) *Image                                   = (*Image).Flatten
	_ func(*Image, ...GrayscaleOptions) *Image                           = (*Image).Grayscale
	_ func(*Image, float64, ...BlurOptions) *Image                       = (*Image).Blur
	_ func(*Image, ...SharpenOptions) *Image                             = (*Image).Sharpen
	_ func(*Image, float64, float64, float64, ...ModulateOptions) *Image = (*Image).Modulate
	_ func(*Image, *Image, ...CompositeOptions) *Image                   = (*Image).Composite
	_ func(*Image, []byte, ...InvisibleWatermarkOptions) *Image          = (*Image).InvisibleWatermark
	_ func(*Image, string, ...string) *Image                             = (*Image).Do
	_ func(*Image) *Image                                                = (*Image).Animated

	_ func(*Image, ...JPEGOptions) *Image = (*Image).JPEG
	_ func(*Image, ...PNGOptions) *Image  = (*Image).PNG
	_ func(*Image, ...WebPOptions) *Image = (*Image).WebP
	_ func(*Image, ...GIFOptions) *Image  = (*Image).GIF
	_ func(*Image, ...TIFFOptions) *Image = (*Image).TIFF
	_ func(*Image) *Image                 = (*Image).BMP

	_ func(*Image) ([]byte, error)                         = (*Image).ToBytes
	_ func(*Image, string) error                           = (*Image).ToFile
	_ func(*Image, io.Writer) error                        = (*Image).ToWriter
	_ func(*Image) (image.Image, error)                    = (*Image).ToImage
	_ func(*Image, *pipeline.Env) (pipeline.Source, error) = (*Image).Stream
	_ func(*Image) (Info, error)                           = (*Image).Metadata
	_ func(*Image) (int, int, error)                       = (*Image).OutputSize

	_ func(*Image, string) ([]byte, bool, error)       = inspect.ReadWatermark
	_ func(*Image) error                               = inspect.Verify
	_ func(*Image, *Image) (inspect.Difference, error) = inspect.Compare
	_ func(*Image) (inspect.Fingerprint, error)        = inspect.Hash
	_ func(*Image) (inspect.Statistics, error)         = inspect.Stats
	_ func(*Image) (inspect.Exif, bool, error)         = inspect.ReadExif

	_ = MaxWatermarkBytes
	_ = WatermarkBlind
	_ = WatermarkScreen
)
