package cli

import (
	"bytes"
	"flag"
	"slices"
	"strings"
	"testing"
)

// The command line is part of the API (see CONTRIBUTING.md): a flag or a
// command of a release is never taken away nor renamed, and a new need
// gets a new name. These are those of each release: what it added to the
// ones before. Add a release's new ones when it is made.
var flags020 = []string{
	"animated", "background", "blur", "blur-method", "blur-min-amplitude",
	"crop", "density", "extend", "extend-with", "fast-shrink", "fit",
	"flatten", "flip", "flop", "font", "font-size", "format", "gif-colors",
	"gif-fixed-palette", "gif-lossy", "gravity", "grayscale",
	"grayscale-linear", "jpeg-chroma", "jpeg-optimize", "jpeg-progressive",
	"jpeg-quant-table", "jpeg-trellis", "keep-exif", "keep-xmp", "kernel",
	"linear-light", "mark", "mark-density", "mark-key", "mark-kind",
	"mark-strength", "max-pixels", "max-size", "mem", "modulate", "no-dither",
	"no-enlarge", "offset", "op", "png-colors", "png-level", "position",
	"quality", "reducing-gap", "resize", "resize-method", "rotate",
	"shadow-blur", "shadow-color", "shadow-offset", "sharpen", "skip-larger",
	"srgb", "strip-icc", "stroke-color", "stroke-width", "text",
	"text-background", "text-color", "text-width", "threads",
	"tiff-compression", "tiff-predictor", "tile-layout", "tile-overlap",
	"tile-size", "tmp", "trim", "trim-background", "trim-line-art",
	"trim-margin", "watermark", "watermark-anchor", "watermark-angle",
	"watermark-blend", "watermark-gap", "watermark-opacity",
	"watermark-pivot", "watermark-ratio", "watermark-text", "watermark-tile",
	"watermark-width", "webp-effort", "webp-lossless",
}

var flags030 = []string{
	"exif-artist", "exif-copyright", "exif-description", "frame-delay",
	"gif-effort", "icns-sizes", "ico-sizes", "jpeg-overshoot-deringing",
	"keep-iptc", "loop", "lut", "min-ssim", "profile", "tiff-pyramid",
	"tiff-tile", "webp-alpha-quality", "webp-exact", "webp-near-lossless",
}

var commands020 = []string{"batch", "check", "compare", "exif", "fonts", "formats", "hash", "info", "ops", "readmark", "stats"}

var commands030 = []string{"pages", "placeholder"}

// TestFlagsKept: every flag of a release is still there.
func TestFlagsKept(t *testing.T) {
	fs := flag.NewFlagSet("goimgtools", flag.ContinueOnError)
	register(fs)
	for release, names := range map[string][]string{"0.2.0": flags020, "0.3.0": flags030} {
		for _, name := range names {
			if fs.Lookup(name) == nil {
				t.Errorf("flag -%s of %s is gone", name, release)
			}
		}
	}
	// And none is new without being in a list: a flag added since the last
	// release goes into the list of the next one when that is made.
	fs.VisitAll(func(f *flag.Flag) {
		if !slices.Contains(flags020, f.Name) && !slices.Contains(flags030, f.Name) {
			t.Logf("flag -%s is new since 0.3.0: list it when the next release is made", f.Name)
		}
	})
}

// TestCommandsKept: every command of a release is still there: those with
// options answer -h with their usage, the others (lists) run.
func TestCommandsKept(t *testing.T) {
	for _, c := range slices.Concat(commands020, commands030) {
		args := []string{c, "-h"}
		if c == "formats" || c == "ops" || c == "fonts" {
			args = args[:1]
		}
		var out, errs bytes.Buffer
		err := Run(args, &out, &errs)
		if err != nil && err != flag.ErrHelp && !strings.Contains(err.Error(), "help") {
			t.Errorf("%s: %v", strings.Join(args, " "), err)
		}
		if out.Len()+errs.Len() == 0 {
			t.Errorf("%s: no output", strings.Join(args, " "))
		}
	}
}
