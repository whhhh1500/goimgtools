package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/inspect"
)

// The small tools: sub-commands that look at images without writing any.
//
//	goimgtools info [-json] file...      what each file is
//	goimgtools check file...             whether each file is intact
//	goimgtools compare [-min-psnr dB] a b  how much two images differ
//	goimgtools formats                   the formats read and written

// fileInfo is everything info knows about one file. It is also the JSON
// that info -json prints, one object to a line.
type fileInfo struct {
	File        string  `json:"file"`
	Error       string  `json:"error,omitempty"`
	Format      string  `json:"format,omitempty"`
	Width       int     `json:"width,omitempty"`
	Height      int     `json:"height,omitempty"`
	Megapixels  float64 `json:"megapixels,omitempty"`
	Bytes       int64   `json:"bytes,omitempty"`
	Color       string  `json:"color,omitempty"`
	Bits        int     `json:"bits,omitempty"`
	Alpha       bool    `json:"alpha"`
	Orientation int     `json:"orientation,omitempty"`
	ICC         bool    `json:"icc"`
	Exif        bool    `json:"exif"`
	Density     float64 `json:"density,omitempty"` // pixels per inch
	Frames      int     `json:"frames,omitempty"`
	DurationMS  int64   `json:"duration_ms,omitempty"`
	// LoopCount: 0 for ever, n times, -1 when the file does not say.
	LoopCount *int `json:"loop_count,omitempty"`
}

// describe gathers what info prints about one file.
func describe(file string) fileInfo {
	out := fileInfo{File: file}
	img := goimgtools.Open(file)
	m, err := img.Metadata()
	if err != nil {
		out.Error = err.Error()
		return out
	}
	out.Format, out.Width, out.Height = m.Format, m.Width, m.Height
	out.Megapixels = float64(m.Width) * float64(m.Height) / 1e6
	out.Color, out.Bits, out.Alpha = m.Color, m.Bits, m.HasAlpha
	out.Orientation, out.ICC, out.Exif, out.Density = m.Orientation, m.HasProfile, m.HasExif, m.Density
	out.Frames = 1
	if st, err := os.Stat(file); err == nil {
		out.Bytes = st.Size()
	}
	if m.Format == "gif" || m.Format == "webp" || m.Format == "png" { // the formats with frames: count them
		fr, err := img.Frames()
		if err != nil {
			out.Error = err.Error()
			return out
		}
		out.Frames, out.DurationMS = fr.Count, fr.Duration.Milliseconds()
		if fr.Count > 1 {
			out.LoopCount = &fr.LoopCount
		}
	}
	return out
}

// line is a file's information in words.
func (f fileInfo) line() string {
	if f.Format == "" {
		return f.File + ": " + f.Error
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %s %dx%d", f.File, f.Format, f.Width, f.Height)
	if f.Alpha {
		b.WriteString(" alpha")
	}
	fmt.Fprintf(&b, ", %s %d-bit", f.Color, f.Bits)
	if f.Megapixels >= 0.1 {
		fmt.Fprintf(&b, ", %.1f MP", f.Megapixels)
	}
	if f.Bytes > 0 {
		fmt.Fprintf(&b, ", %s", human(f.Bytes))
	}
	if f.Frames > 1 {
		fmt.Fprintf(&b, ", %d frames in %.2fs", f.Frames, float64(f.DurationMS)/1000)
		switch n := *f.LoopCount; {
		case n == 0:
			b.WriteString(", plays for ever")
		case n > 0:
			fmt.Fprintf(&b, ", plays %d times", n)
		}
	}
	if f.Orientation > 1 {
		fmt.Fprintf(&b, ", EXIF orientation %d", f.Orientation)
		if f.Orientation >= 5 {
			fmt.Fprintf(&b, " (upright it is %dx%d)", f.Height, f.Width)
		}
	}
	if f.Density > 0 {
		fmt.Fprintf(&b, ", %s dpi", strconv.FormatFloat(math.Round(f.Density*100)/100, 'f', -1, 64))
	}
	if f.ICC {
		b.WriteString(", ICC profile")
	}
	if f.Exif {
		b.WriteString(", EXIF")
	}
	if f.Error != "" {
		b.WriteString("; " + f.Error)
	}
	return b.String()
}

// info prints what each file is: read from its header, plus a walk through
// the frames of a GIF.
func info(args []string, stdout, stderr io.Writer) error {
	fl := flag.NewFlagSet("goimgtools info", flag.ContinueOnError)
	fl.SetOutput(stderr)
	asJSON := fl.Bool("json", false, "print one JSON object per file, each on a line of its own")
	fl.Usage = func() {
		fmt.Fprint(stderr, "usage: goimgtools info [-json] file...\n\nPrints format, size, colours, metadata and frames of each file.\n\n")
		fl.PrintDefaults()
	}
	if err := fl.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fl.NArg() == 0 {
		return errors.New("info: need at least one file")
	}
	failed := 0
	for _, f := range fl.Args() {
		d := describe(f)
		if d.Error != "" {
			failed++
		}
		if *asJSON {
			line, err := json.Marshal(d)
			if err != nil {
				return err
			}
			fmt.Fprintf(stdout, "%s\n", line)
		} else {
			fmt.Fprintln(stdout, d.line())
		}
	}
	if failed > 0 {
		return fmt.Errorf("info: %d of %d files could not be read", failed, fl.NArg())
	}
	return nil
}

// check decodes each file to its end.
func check(args []string, stdout, stderr io.Writer) error {
	fl := flag.NewFlagSet("goimgtools check", flag.ContinueOnError)
	fl.SetOutput(stderr)
	quiet := fl.Bool("quiet", false, "print only the files with something wrong")
	fl.Usage = func() {
		fmt.Fprint(stderr, "usage: goimgtools check [-quiet] file...\n\nDecodes every file completely and reports the ones that are damaged, cut\nshort or not images. (info only reads the header.)\n\n")
		fl.PrintDefaults()
	}
	if err := fl.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fl.NArg() == 0 {
		return errors.New("check: need at least one file")
	}
	bad := 0
	for _, f := range fl.Args() {
		if err := inspect.Verify(goimgtools.Open(f)); err != nil {
			bad++
			fmt.Fprintf(stdout, "%s: %v\n", f, err)
		} else if !*quiet {
			fmt.Fprintf(stdout, "%s: ok\n", f)
		}
	}
	if bad > 0 {
		return fmt.Errorf("check: %d of %d files are damaged or cannot be read", bad, fl.NArg())
	}
	return nil
}

// compare measures how much two images differ.
func compare(args []string, stdout, stderr io.Writer) error {
	fl := flag.NewFlagSet("goimgtools compare", flag.ContinueOnError)
	fl.SetOutput(stderr)
	minPSNR := fl.Float64("min-psnr", 0, "fail (exit status 1) unless the images are of the same size and at least this many `dB` close")
	fl.Usage = func() {
		fmt.Fprint(stderr, "usage: goimgtools compare [-min-psnr dB] a b\n\nCompares two images pixel by pixel, whatever their formats: whether they\nare identical, and if not, the PSNR in dB (higher is closer: above 40 the\ndifference is hard to see), the largest difference and how many pixels\ndiffer.\n\n")
		fl.PrintDefaults()
	}
	if err := fl.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fl.NArg() != 2 {
		return errors.New("compare: need two files")
	}
	a, b := goimgtools.Open(fl.Arg(0)), goimgtools.Open(fl.Arg(1))
	d, err := inspect.Compare(a, b)
	if err != nil {
		return fmt.Errorf("compare: %w", err)
	}
	if !d.SameSize {
		w, h, _ := b.OutputSize()
		fmt.Fprintf(stdout, "different sizes: %dx%d and %dx%d\n", d.Width, d.Height, w, h)
	} else {
		fmt.Fprintln(stdout, d.String())
	}
	if *minPSNR > 0 && (!d.SameSize || d.PSNR < *minPSNR) {
		return fmt.Errorf("compare: below the %.2f dB asked for", *minPSNR)
	}
	return nil
}

// formatTable lists the formats the library knows.
func formatTable(args []string, stdout io.Writer) error {
	if len(args) != 0 {
		return errors.New("formats: takes no arguments")
	}
	for _, f := range goimgtools.Formats() {
		c, _ := goimgtools.CodecByName(f.Name)
		can := []string{}
		if c.Decode != nil {
			can = append(can, "read")
		}
		if c.Encode != nil {
			can = append(can, "write")
		}
		fmt.Fprintf(stdout, "%-6s %-14s %s\n", f.Name, strings.Join(f.Extensions, " "), strings.Join(can, ", "))
	}
	return nil
}
