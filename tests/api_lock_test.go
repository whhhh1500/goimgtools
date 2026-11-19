package tests

import (
	"context"
	"image"
	"image/color"
	"io"
	"time"

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

// The values of the basic settings' constants, which a saved setting may
// hold: an array of each value's length is of exactly that length.
var (
	_ [0]struct{} = [FitInside]struct{}{}
	_ [1]struct{} = [FitCover]struct{}{}
	_ [2]struct{} = [FitOutside]struct{}{}
	_ [3]struct{} = [FitFill]struct{}{}
	_ [4]struct{} = [FitContain]struct{}{}

	_ [0]struct{} = [KernelCubic]struct{}{}
	_ [1]struct{} = [KernelNearest]struct{}{}
	_ [2]struct{} = [KernelLinear]struct{}{}
	_ [3]struct{} = [KernelMitchell]struct{}{}
	_ [4]struct{} = [KernelLanczos2]struct{}{}
	_ [5]struct{} = [KernelLanczos3]struct{}{}
	_ [6]struct{} = [KernelMKS2013]struct{}{}
	_ [7]struct{} = [KernelMKS2021]struct{}{}
	_ [8]struct{} = [KernelBox]struct{}{}
	_ [9]struct{} = [KernelHamming]struct{}{}

	_ [0]struct{} = [DefaultPosition]struct{}{}
	_ [1]struct{} = [NorthWest]struct{}{}
	_ [2]struct{} = [North]struct{}{}
	_ [3]struct{} = [NorthEast]struct{}{}
	_ [4]struct{} = [West]struct{}{}
	_ [5]struct{} = [Center]struct{}{}
	_ [6]struct{} = [East]struct{}{}
	_ [7]struct{} = [SouthWest]struct{}{}
	_ [8]struct{} = [South]struct{}{}
	_ [9]struct{} = [SouthEast]struct{}{}

	_ [0]struct{} = [MethodDefault]struct{}{}
	_ [1]struct{} = [MethodLibvips]struct{}{}
	_ [2]struct{} = [MethodPillow]struct{}{}

	_ [0]struct{} = [TIFFNone]struct{}{}
	_ [1]struct{} = [TIFFDeflate]struct{}{}
	_ [2]struct{} = [TIFFLZW]struct{}{}

	_ [0]struct{} = [Chroma420]struct{}{}
	_ [1]struct{} = [Chroma422]struct{}{}
	_ [2]struct{} = [Chroma444]struct{}{}

	_ [0]struct{} = [QuantTableStandard]struct{}{}
	_ [1]struct{} = [QuantTableFlat]struct{}{}
	_ [2]struct{} = [QuantTableMSSSIM]struct{}{}
	_ [3]struct{} = [QuantTableImageMagick]struct{}{}
	_ [4]struct{} = [QuantTablePSNRHVS]struct{}{}
	_ [5]struct{} = [QuantTableKlein]struct{}{}
	_ [6]struct{} = [QuantTableWatson]struct{}{}
	_ [7]struct{} = [QuantTableAhumada]struct{}{}
	_ [8]struct{} = [QuantTablePeterson]struct{}{}
	_ [9]struct{} = [QuantTableJpegli]struct{}{}

	_ [0]struct{} = [ExtendBackground]struct{}{}
	_ [1]struct{} = [ExtendCopy]struct{}{}
	_ [2]struct{} = [ExtendRepeat]struct{}{}

	_ [0]struct{} = [WatermarkBlind]struct{}{}
	_ [1]struct{} = [WatermarkScreen]struct{}{}
)

// The fields of the basic options and results, held to their names and
// types (fields may be added; none renamed, retyped or taken away).
func _() {
	var r ResizeOptions
	var (
		_ Fit           = r.Fit
		_ bool          = r.WithoutEnlargement
		_ bool          = r.FastShrinkOnLoad
		_ color.Color   = r.Background
		_ Kernel        = r.Kernel
		_ *CustomKernel = r.CustomKernel
		_ Position      = r.Position
		_ Interesting   = r.Interesting
		_ bool          = r.LinearLight
		_ Method        = r.Method
		_ float64       = r.ReducingGap
	)
	var j JPEGOptions
	var (
		_ int               = j.Quality
		_ bool              = j.Optimize
		_ bool              = j.Progressive
		_ bool              = j.Trellis
		_ QuantTable        = j.QuantTable
		_ ChromaSubsampling = j.ChromaSubsampling
	)
	var p PNGOptions
	var (
		_ int  = p.CompressionLevel
		_ int  = p.Colors
		_ bool = p.NoDither
	)
	var wp WebPOptions
	var (
		_ int  = wp.Quality
		_ bool = wp.Lossless
		_ int  = wp.Effort
	)
	var g GIFOptions
	var (
		_ int  = g.Colors
		_ bool = g.NoDither
		_ bool = g.FixedPalette
		_ int  = g.Lossy
	)
	var t TIFFOptions
	var (
		_ TIFFCompression = t.Compression
		_ bool            = t.Predictor
	)
	var iw InvisibleWatermarkOptions
	var (
		_ WatermarkKind = iw.Kind
		_ string        = iw.Key
		_ float64       = iw.Strength
		_ float64       = iw.Density
	)
	var c CompositeOptions
	var (
		_ Position  = c.Gravity
		_ int       = c.Left
		_ int       = c.Top
		_ float64   = c.LeftRatio
		_ float64   = c.TopRatio
		_ Position  = c.Anchor
		_ float64   = c.AnchorX
		_ float64   = c.AnchorY
		_ float64   = c.Angle
		_ Position  = c.Pivot
		_ float64   = c.PivotX
		_ float64   = c.PivotY
		_ float64   = c.Opacity
		_ bool      = c.Tile
		_ int       = c.TileGapX
		_ int       = c.TileGapY
		_ float64   = c.WidthRatio
		_ float64   = c.HeightRatio
		_ BlendMode = c.Blend
	)
	var e ExtendOptions
	var (
		_ color.Color = e.Background
		_ ExtendMode  = e.Mode
	)
	var tr TrimOptions
	var (
		_ color.Color = tr.Background
		_ bool        = tr.LineArt
		_ int         = tr.Margin
	)
	var ro RotateOptions
	var (
		_ color.Color  = ro.Background
		_ Method       = ro.Method
		_ Interpolator = ro.Interpolator
		_ bool         = ro.Expand
	)
	var b BlurOptions
	var (
		_ Method  = b.Method
		_ float64 = b.MinAmplitude
	)
	var s SharpenOptions
	var (
		_ float64       = s.Sigma
		_ float64       = s.Amount
		_ int           = s.Threshold
		_ Method        = s.Method
		_ *SharpenCurve = s.Curve
	)
	var mo ModulateOptions
	var _ float64 = mo.Lightness
	var gr GrayscaleOptions
	var _ bool = gr.Linear

	var in Info
	var (
		_ string  = in.Format
		_ int     = in.Width
		_ int     = in.Height
		_ bool    = in.HasAlpha
		_ string  = in.Color
		_ int     = in.Bits
		_ int     = in.Orientation
		_ bool    = in.HasICC
		_ bool    = in.HasExif
		_ bool    = in.HasXmp
		_ float64 = in.Density
	)
	var d inspect.Difference
	var (
		_ bool    = d.SameSize
		_ int     = d.Width
		_ int     = d.Height
		_ bool    = d.Identical
		_ int64   = d.DifferentPixels
		_ int     = d.MaxDiff
		_ float64 = d.PSNR
	)
	var st inspect.Statistics
	var (
		_ int                     = st.Width
		_ int                     = st.Height
		_ color.NRGBA             = st.Average
		_ float64                 = st.Brightness
		_ []inspect.ColorShare    = st.Dominant
		_ bool                    = st.Gray
		_ bool                    = st.Opaque
		_ float64                 = st.Transparent
		_ int                     = st.Colors
		_ [4]inspect.ChannelStats = st.Channels
		_ float64                 = st.Entropy
		_ float64                 = st.Sharpness
	)
	var ex inspect.Exif
	var (
		_ string    = ex.Make
		_ string    = ex.Model
		_ string    = ex.Lens
		_ string    = ex.Software
		_ string    = ex.Artist
		_ string    = ex.Copyright
		_ time.Time = ex.Taken
		_ bool      = ex.HasTaken
		_ int       = ex.Orientation
		_ float64   = ex.Exposure
		_ float64   = ex.Aperture
		_ int       = ex.ISO
		_ float64   = ex.FocalLength
		_ bool      = ex.HasGPS
		_ float64   = ex.Latitude
		_ float64   = ex.Longitude
		_ bool      = ex.HasAltitude
		_ float64   = ex.Altitude
	)
	var _ uint64 = uint64(inspect.Fingerprint(0))
}
