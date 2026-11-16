// Command memprobe measures the peak memory, CPU time and wall time of
// "decode, shrink, white background, optional watermark, encode" done two
// ways: "api" (this module) and "std" (whole-image decoding with the
// standard library and golang.org/x/image). Run one mode per process.
//
//	memprobe api|std in out width height [logo.png]
//
// width × height is the box the image is shrunk into, keeping proportions.
// The output format follows the extension of out (.jpg or .png).
package main

import (
	"bufio"
	"crypto/sha256"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	"image/jpeg"
	"image/png"
	"math"
	"os"
	"runtime"
	"runtime/pprof"
	"strconv"
	"strings"
	"time"

	_ "golang.org/x/image/bmp"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"

	"github.com/whhhh1500/goimgtools"
)

func main() {
	if len(os.Args) < 6 {
		fmt.Fprintln(os.Stderr, "usage: memprobe api|std in out width height [logo.png]")
		os.Exit(2)
	}
	mode, in, out := os.Args[1], os.Args[2], os.Args[3]
	bw, _ := strconv.Atoi(os.Args[4])
	bh, _ := strconv.Atoi(os.Args[5])
	logoPath := ""
	if len(os.Args) > 6 {
		logoPath = os.Args[6]
	}
	isPNG := strings.HasSuffix(strings.ToLower(out), ".png")
	base := peakWorkingSet()
	if p := os.Getenv("GOIMG_PROF"); p != "" { // write a CPU profile here
		pf, err := os.Create(p)
		check(err)
		check(pprof.StartCPUProfile(pf))
		defer pprof.StopCPUProfile()
	}
	if p := os.Getenv("GOIMG_HEAP"); p != "" { // write every allocation site here
		runtime.MemProfileRate = 1
		defer func() {
			hf, err := os.Create(p)
			check(err)
			check(pprof.Lookup("allocs").WriteTo(hf, 0))
			hf.Close()
		}()
	}
	start := time.Now()
	switch mode {
	case "api":
		im := goimgtools.Open(in)
		if os.Getenv("GOIMG_ROTATE") != "" { // turn a quarter first
			im.Rotate(90)
		}
		if os.Getenv("GOIMG_NORESIZE") != "" { // keep the full size
			bw, bh = 1<<20, 1<<20
		}
		im.Resize(bw, bh, goimgtools.ResizeOptions{Fit: goimgtools.FitInside, WithoutEnlargement: os.Getenv("GOIMG_ENLARGE") == "", FastShrinkOnLoad: os.Getenv("GOIMG_FAST") != ""}).
			Flatten(color.White)
		// One of the operations that change how the image looks, after the
		// resize: gray, modulate, blur:SIGMA, sharpen, usm:SIGMA, trim,
		// extend, rotate:DEGREES.
		if op := os.Getenv("GOIMG_ADJUST"); op != "" {
			name, arg, _ := strings.Cut(op, ":")
			v, _ := strconv.ParseFloat(arg, 64)
			switch name {
			case "gray":
				im.Grayscale()
			case "modulate":
				im.Modulate(1.1, 1.3, 20)
			case "blur":
				im.Blur(v)
			case "sharpen":
				im.Sharpen()
			case "usm":
				im.Sharpen(goimgtools.SharpenOptions{Sigma: v})
			case "trim":
				im.Trim(10)
			case "extend":
				im.Extend(50, 50, 50, 50, color.White)
			case "rotate":
				im.RotateBy(v, color.White)
			default:
				check(fmt.Errorf("GOIMG_ADJUST: unknown operation %q", op))
			}
		}
		if v, err := strconv.Atoi(os.Getenv("GOIMG_MEM")); err == nil { // limit in MB
			im.MemoryLimit(int64(v) << 20)
		}
		if v, err := strconv.Atoi(os.Getenv("GOIMG_THREADS")); err == nil {
			im.Threads(v)
		}
		if logoPath != "" {
			im.Composite(goimgtools.Open(logoPath), goimgtools.CompositeOptions{Gravity: goimgtools.SouthEast, Left: -24, Top: -24})
		}
		if lo := strings.ToLower(out); strings.HasSuffix(lo, ".jpg") {
			jo := goimgtools.JPEGOptions{Optimize: os.Getenv("GOIMG_JPEG") == "opt", Progressive: os.Getenv("GOIMG_JPEG") == "prog"}
			im.JPEG(85, jo)
		} else if strings.HasSuffix(lo, ".png") && os.Getenv("GOIMG_COLORS") != "" {
			n, _ := strconv.Atoi(os.Getenv("GOIMG_COLORS"))
			im.PNG(goimgtools.PNGOptions{Colors: n})
		} else if strings.HasSuffix(lo, ".webp") {
			effort, _ := strconv.Atoi(os.Getenv("GOIMG_EFFORT")) // 0: the default
			im.WebP(goimgtools.WebPOptions{Lossless: os.Getenv("GOIMG_LOSSLESS") != "", Effort: effort})
		}
		if v, err := strconv.Atoi(os.Getenv("GOIMG_MAXSIZE")); err == nil { // file size limit in KB
			im.MaxFileSize(int64(v) << 10)
		}
		if t := os.Getenv("GOIMG_TEXT"); t != "" { // a tiled text watermark
			im.Composite(goimgtools.Text(t, goimgtools.TextOptions{Size: 48, Color: color.White, StrokeWidth: 2}),
				goimgtools.CompositeOptions{Tile: true, TileGapX: 80, TileGapY: 60, Angle: -30, Opacity: 0.3})
		}
		check(im.ToFile(out))
	case "std":
		f, err := os.Open(in)
		check(err)
		m, _, err := image.Decode(bufio.NewReaderSize(f, 64<<10))
		check(err)
		f.Close()
		sb := m.Bounds()
		fx := math.Min(float64(bw)/float64(sb.Dx()), float64(bh)/float64(sb.Dy()))
		fx = math.Min(fx, 1)
		dw := max(1, int(math.Round(float64(sb.Dx())*fx)))
		dh := max(1, int(math.Round(float64(sb.Dy())*fx)))
		var small image.Image = m
		if dw != sb.Dx() || dh != sb.Dy() {
			s := image.NewRGBA(image.Rect(0, 0, dw, dh))
			draw.CatmullRom.Scale(s, s.Bounds(), m, sb, draw.Src, nil)
			small = s
		}
		dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
		draw.Draw(dst, dst.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
		draw.Draw(dst, dst.Bounds(), small, small.Bounds().Min, draw.Over)
		if logoPath != "" {
			lf, err := os.Open(logoPath)
			check(err)
			logo, _, err := image.Decode(lf)
			check(err)
			lf.Close()
			lb := logo.Bounds()
			at := lb.Sub(lb.Min).Add(image.Pt(dw-lb.Dx()-24, dh-lb.Dy()-24))
			draw.Draw(dst, at, logo, lb.Min, draw.Over)
		}
		o, err := os.Create(out)
		check(err)
		w := bufio.NewWriter(o)
		if isPNG {
			check(png.Encode(w, dst))
		} else {
			check(jpeg.Encode(w, dst, &jpeg.Options{Quality: 85}))
		}
		check(w.Flush())
		check(o.Close())
	default:
		check(fmt.Errorf("unknown mode %q", mode))
	}
	el := time.Since(start)
	// Taken before the output is read back for hashing, which would count.
	peak := peakWorkingSet()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	data, err := os.ReadFile(out)
	check(err)
	fmt.Printf("%-4s time=%-7s cpu=%-7s peakRSS=%6.1fMB (start %.1fMB) totalAlloc=%7.1fMB out=%d sha=%x\n",
		mode, el.Round(time.Millisecond), cpuTime().Round(time.Millisecond),
		mb(peak), mb(base), mb(ms.TotalAlloc), len(data), sha256.Sum256(data))
}

func mb(n uint64) float64 { return float64(n) / (1 << 20) }

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "memprobe:", err)
		os.Exit(1)
	}
}
