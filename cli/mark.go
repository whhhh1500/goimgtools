package cli

import (
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"unicode/utf8"

	"github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/inspect"
)

// readMark prints the invisible mark of each file.
func readMark(args []string, stdout, stderr io.Writer) error {
	fl := flag.NewFlagSet("goimgtools readmark", flag.ContinueOnError)
	fl.SetOutput(stderr)
	key := fl.String("key", "", "the `key` the mark was made with (-mark-key)")
	asHex := fl.Bool("hex", false, "print the mark's bytes in hexadecimal")
	fl.Usage = func() {
		fmt.Fprint(stderr, "usage: goimgtools readmark [-key key] [-hex] file...\n\nPrints the invisible mark hidden in each image with -mark and the same\n-mark-key, or \"no mark\". The image may have been saved again, resized,\ncropped, turned, screenshotted or, for -mark-kind screen, photographed off a\nscreen (crop the photograph to the screen first). A mark that is printed is\nthe one that was hidden, but for about one chance in seven hundred thousand:\na check of the key and the bytes sees to that.\n\n")
		fl.PrintDefaults()
	}
	if err := fl.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fl.NArg() == 0 {
		return errors.New("readmark: need at least one file")
	}
	failed := 0
	for _, f := range fl.Args() {
		mark, ok, err := inspect.ReadWatermark(goimgtools.Open(f), *key)
		switch {
		case err != nil:
			failed++
			fmt.Fprintf(stdout, "%s: %v\n", f, err)
		case !ok:
			fmt.Fprintf(stdout, "%s: no mark\n", f)
		case *asHex || !utf8.Valid(mark):
			fmt.Fprintf(stdout, "%s: %s\n", f, hex.EncodeToString(mark))
		default:
			fmt.Fprintf(stdout, "%s: %q\n", f, mark)
		}
	}
	if failed > 0 {
		return fmt.Errorf("readmark: %d of %d files could not be read", failed, fl.NArg())
	}
	return nil
}
