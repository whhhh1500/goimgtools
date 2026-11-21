package cli

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/inspect"
)

// More of the small tools, the ones that look at every pixel or into the
// EXIF block:
//
//	goimgtools stats [-json] file...        the colours of each image
//	goimgtools hash [-similar bits] file... a fingerprint for finding duplicates
//	goimgtools exif [-json] file...         camera, time and place

// eachFile runs a tool over its files: one line per file from line, or one
// JSON object per file from object when -json is given. A file that fails
// gets its error as its line and makes the command fail at the end.
func eachFile(name, about string, args []string, stdout, stderr io.Writer,
	do func(file string) (line string, object any, err error)) error {
	fl := flag.NewFlagSet("goimgtools "+name, flag.ContinueOnError)
	fl.SetOutput(stderr)
	asJSON := fl.Bool("json", false, "print one JSON object per file, each on a line of its own")
	fl.Usage = func() {
		fmt.Fprintf(stderr, "usage: goimgtools %s [-json] file...\n\n%s\n\n", name, about)
		fl.PrintDefaults()
	}
	if err := fl.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fl.NArg() == 0 {
		return fmt.Errorf("%s: need at least one file", name)
	}
	failed := 0
	for _, f := range fl.Args() {
		line, object, err := do(f)
		if err != nil {
			failed++
			line, object = err.Error(), map[string]string{"file": f, "error": err.Error()}
		}
		if *asJSON {
			out, jerr := json.Marshal(object)
			if jerr != nil {
				return jerr
			}
			fmt.Fprintf(stdout, "%s\n", out)
		} else {
			fmt.Fprintf(stdout, "%s: %s\n", f, line)
		}
	}
	if failed > 0 {
		return fmt.Errorf("%s: %d of %d files could not be read", name, failed, fl.NArg())
	}
	return nil
}

// stats prints the colours of each image.
func stats(args []string, stdout, stderr io.Writer) error {
	type share struct {
		Color string  `json:"color"`
		Share float64 `json:"share"`
	}
	return eachFile("stats", "Reads every pixel and reports the average colour, the brightness, the\ncolours that cover most of the image, whether it is really greyscale and\nreally opaque, how many colours it has (when at most 256), and, as sharp's\nstats(), the entropy, the sharpness and (with -json) each channel's levels.",
		args, stdout, stderr, func(file string) (string, any, error) {
			st, err := inspect.Stats(goimgtools.Open(file))
			if err != nil {
				return "", nil, err
			}
			dominant := []share{}
			for _, d := range st.Dominant {
				dominant = append(dominant, share{inspect.Hex(d.Color), d.Share})
			}
			// The channels as sharp's stats() names them.
			type channel struct {
				Min        uint8   `json:"min"`
				Max        uint8   `json:"max"`
				Sum        uint64  `json:"sum"`
				SquaresSum uint64  `json:"squaresSum"`
				Mean       float64 `json:"mean"`
				Stdev      float64 `json:"stdev"`
				MinX       int     `json:"minX"`
				MinY       int     `json:"minY"`
				MaxX       int     `json:"maxX"`
				MaxY       int     `json:"maxY"`
			}
			var channels []channel
			for _, c := range st.Channels {
				channels = append(channels, channel{c.Min, c.Max, c.Sum, c.SquaresSum, c.Mean, c.Stdev, c.MinAt.X, c.MinAt.Y, c.MaxAt.X, c.MaxAt.Y})
			}
			return st.String(), struct {
				File        string    `json:"file"`
				Width       int       `json:"width"`
				Height      int       `json:"height"`
				Average     string    `json:"average"`
				Brightness  float64   `json:"brightness"`
				Gray        bool      `json:"gray"`
				Opaque      bool      `json:"opaque"`
				Transparent float64   `json:"transparent"`
				Colors      int       `json:"colors"`
				Dominant    []share   `json:"dominant"`
				Entropy     float64   `json:"entropy"`
				Sharpness   float64   `json:"sharpness"`
				Channels    []channel `json:"channels"`
			}{file, st.Width, st.Height, inspect.Hex(st.Average), st.Brightness, st.Gray, st.Opaque, st.Transparent, st.Colors, dominant,
				st.Entropy, st.Sharpness, channels}, nil
		})
}

// exifInfo prints what the EXIF block of each file says.
func exifInfo(args []string, stdout, stderr io.Writer) error {
	return eachFile("exif", "Prints what the EXIF block of each file records: camera, lens, when the\npicture was taken, exposure, and the position when there is one.",
		args, stdout, stderr, func(file string) (string, any, error) {
			e, ok, err := inspect.ReadExif(goimgtools.Open(file))
			if err != nil {
				return "", nil, err
			}
			if !ok {
				return "no EXIF", map[string]any{"file": file, "exif": false}, nil
			}
			o := map[string]any{"file": file, "exif": true}
			put := func(key string, v any, has bool) {
				if has {
					o[key] = v
				}
			}
			put("make", e.Make, e.Make != "")
			put("model", e.Model, e.Model != "")
			put("lens", e.Lens, e.Lens != "")
			put("software", e.Software, e.Software != "")
			put("artist", e.Artist, e.Artist != "")
			put("copyright", e.Copyright, e.Copyright != "")
			put("taken", e.Taken.Format(time.RFC3339), e.HasTaken)
			put("orientation", e.Orientation, e.Orientation != 0)
			put("exposure", e.Exposure, e.Exposure > 0)
			put("aperture", e.Aperture, e.Aperture > 0)
			put("iso", e.ISO, e.ISO > 0)
			put("focal_length", e.FocalLength, e.FocalLength > 0)
			put("latitude", e.Latitude, e.HasGPS)
			put("longitude", e.Longitude, e.HasGPS)
			put("altitude", e.Altitude, e.HasGPS && e.HasAltitude)
			return e.String(), o, nil
		})
}

// hash prints the fingerprint of each image and, on request, the pairs
// that look alike.
func hash(args []string, stdout, stderr io.Writer) error {
	fl := flag.NewFlagSet("goimgtools hash", flag.ContinueOnError)
	fl.SetOutput(stderr)
	similar := fl.Int("similar", -1, "also list the pairs of files whose fingerprints differ in at most this many `bits` (0: the same; up to about 10: probably the same picture)")
	fl.Usage = func() {
		fmt.Fprint(stderr, "usage: goimgtools hash [-similar bits] file...\n\nPrints a 64-bit fingerprint of each image. Pictures that look alike (the\nsame photo at another size, in another format, compressed harder) have\nfingerprints that differ in a few bits; different pictures differ in about\nhalf of them.\n\n")
		fl.PrintDefaults()
	}
	if err := fl.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fl.NArg() == 0 {
		return errors.New("hash: need at least one file")
	}
	if *similar > 64 {
		return errors.New("hash: -similar is a number of bits, 0 to 64")
	}
	type hashed struct {
		file string
		f    inspect.Fingerprint
	}
	var done []hashed
	failed := 0
	for _, f := range fl.Args() {
		h, err := inspect.Hash(goimgtools.Open(f))
		if err != nil {
			failed++
			fmt.Fprintf(stdout, "%s: %v\n", f, err)
			continue
		}
		fmt.Fprintf(stdout, "%v  %s\n", h, f)
		done = append(done, hashed{f, h})
	}
	if *similar >= 0 {
		pairs := 0
		for i := range done {
			for j := i + 1; j < len(done); j++ {
				if d := done[i].f.Distance(done[j].f); d <= *similar {
					if pairs == 0 {
						fmt.Fprintln(stdout)
					}
					pairs++
					fmt.Fprintf(stdout, "%s ~ %s (%d bits apart)\n", done[i].file, done[j].file, d)
				}
			}
		}
		if pairs == 0 {
			fmt.Fprintf(stdout, "\nno two files within %d bits of each other\n", *similar)
		}
	}
	if failed > 0 {
		return fmt.Errorf("hash: %d of %d files could not be read", failed, fl.NArg())
	}
	return nil
}

// placeholder prints the BlurHash and the ThumbHash of each image.
func placeholder(args []string, stdout, stderr io.Writer) error {
	fl := flag.NewFlagSet("goimgtools placeholder", flag.ContinueOnError)
	fl.SetOutput(stderr)
	comps := fl.String("components", "4x3", "the BlurHash `components` across and down, 1 to 9 each")
	fl.Usage = func() {
		fmt.Fprint(stderr, "usage: goimgtools placeholder [-components 4x3] file...\n\nPrints the BlurHash and the ThumbHash (in base64) of each image: short\ncodes that a page decodes into a blurred picture of it, shown while the\nimage loads.\n\n")
		fl.PrintDefaults()
	}
	if err := fl.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fl.NArg() == 0 {
		return errors.New("placeholder: need at least one file")
	}
	var cx, cy int
	if n, err := fmt.Sscanf(*comps, "%dx%d", &cx, &cy); err != nil || n != 2 || cx < 1 || cx > 9 || cy < 1 || cy > 9 {
		return fmt.Errorf("placeholder: -components %q: want two numbers 1 to 9, such as 4x3", *comps)
	}
	failed := 0
	for _, f := range fl.Args() {
		b, err := inspect.BlurHash(goimgtools.Open(f), cx, cy)
		var th []byte
		if err == nil {
			th, err = inspect.ThumbHash(goimgtools.Open(f))
		}
		if err != nil {
			failed++
			fmt.Fprintf(stdout, "%s: %v\n", f, err)
			continue
		}
		fmt.Fprintf(stdout, "%s  %s  %s\n", b, base64.StdEncoding.EncodeToString(th), f)
	}
	if failed > 0 {
		return fmt.Errorf("placeholder: %d of %d files could not be read", failed, fl.NArg())
	}
	return nil
}

// pages writes several images as the pages of one PDF document.
func pages(args []string, stdout, stderr io.Writer) error {
	fl := flag.NewFlagSet("goimgtools pages", flag.ContinueOnError)
	fl.SetOutput(stderr)
	quality := fl.Int("quality", 0, "store each page as a JPEG of this quality, 1-100 (default: without loss)")
	fl.Usage = func() {
		fmt.Fprint(stderr, "usage: goimgtools pages [-quality n] image... out.pdf\n\nWrites the images as the pages of one PDF document, in order, each page\nas large as its image at its resolution. Each image is read while its\npage is written.\n\n")
		fl.PrintDefaults()
	}
	if err := fl.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fl.NArg() < 2 {
		return errors.New("pages: need at least one image and the PDF to write")
	}
	in, out := fl.Args()[:fl.NArg()-1], fl.Arg(fl.NArg()-1)
	imgs := make([]*goimgtools.Image, len(in))
	for i, f := range in {
		imgs[i] = goimgtools.Open(f)
	}
	if err := goimgtools.Pages(imgs).PDF(goimgtools.PDFOptions{Quality: *quality}).ToFile(out); err != nil {
		return fmt.Errorf("pages: %w", err)
	}
	fmt.Fprintf(stdout, "%s: %d pages\n", out, len(in))
	return nil
}
