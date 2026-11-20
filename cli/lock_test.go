package cli

import (
	"bytes"
	"flag"
	"strings"
	"testing"
)

// The command line is part of the API (see CONTRIBUTING.md): a flag or a
// command of a release is never taken away nor renamed, and a new need
// gets a new name. These are those of 0.2.0.
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

var commands020 = []string{"batch", "check", "compare", "exif", "fonts", "formats", "hash", "info", "ops", "readmark", "stats"}

// TestFlagsKept: every flag of 0.2.0 is still there.
func TestFlagsKept(t *testing.T) {
	fs := flag.NewFlagSet("goimgtools", flag.ContinueOnError)
	register(fs)
	for _, name := range flags020 {
		if fs.Lookup(name) == nil {
			t.Errorf("flag -%s of 0.2.0 is gone", name)
		}
	}
}

// TestCommandsKept: every command of 0.2.0 is still there: those with
// options answer -h with their usage, the others (lists) run.
func TestCommandsKept(t *testing.T) {
	for _, c := range commands020 {
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
