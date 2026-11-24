package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/whhhh1500/goimgtools"
)

const batchUsage = `usage: goimgtools batch [options] input-dir output-dir

Processes every image in input-dir (by file extension: .jpg .jpeg .png
.webp .gif .bmp .tif .tiff) with the options of the single-file mode and
writes each one to the
same name under output-dir. With -format the extension becomes that
format's; without it every file keeps its own format. A file that fails is
reported and the others go on.

Several files are processed at the same time: 3 unless -jobs says otherwise,
any number, or all of them at once with -jobs 0. Each one uses at most -mem
of working memory (typically 6 to 15MB) and -threads cores, so the total is
that times the number of jobs.

options:
`

// isImage reports whether a file name has the extension of a format the
// library handles.
func isImage(path string) bool { return formatOf(path) != "" }

// job is one file to process.
type job struct {
	in, out string
	rel     string // in, relative to the input directory
	err     error  // set when the job cannot be run at all
}

// collect lists the image files of inDir and where each one goes. ext, when
// not empty, replaces the extension. Files that are not images are counted.
func collect(inDir, outDir string, recursive bool, ext string) (jobs []job, others int, err error) {
	absOut, _ := filepath.Abs(outDir)
	absIn, _ := filepath.Abs(inDir)
	taken := map[string]string{} // output path -> the input that writes it
	err = filepath.WalkDir(inDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			abs, _ := filepath.Abs(path)
			switch {
			case abs == absIn:
				return nil
			case !recursive:
				return filepath.SkipDir
			case abs == absOut:
				return filepath.SkipDir // our own output, inside the input directory
			}
			return nil
		}
		if !d.Type().IsRegular() || strings.HasPrefix(d.Name(), ".goimgtools-") {
			return nil
		}
		if !isImage(path) {
			others++
			return nil
		}
		rel, err := filepath.Rel(inDir, path)
		if err != nil {
			return err
		}
		out := filepath.Join(outDir, rel)
		if ext != "" {
			out = strings.TrimSuffix(out, filepath.Ext(out)) + ext
		}
		j := job{in: path, out: out, rel: rel}
		// Two inputs that differ only in their extension would write the
		// same file once the extension is replaced.
		key := strings.ToLower(out)
		if first, dup := taken[key]; dup {
			j.err = fmt.Errorf("would write the same file as %s", first)
		} else {
			taken[key] = rel
		}
		jobs = append(jobs, j)
		return nil
	})
	return jobs, others, err
}

// human writes a byte count the way file managers do.
func human(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.2fGB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1fMB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0fKB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%dB", n)
}

func percent(out, in int64) string {
	if in <= 0 {
		return "-"
	}
	return fmt.Sprintf("%.0f%%", 100*float64(out)/float64(in))
}

// batch runs the single-file chain over every image of a directory.
func batch(args []string, stdout, stderr io.Writer) error {
	fl := flag.NewFlagSet("goimgtools batch", flag.ContinueOnError)
	fl.SetOutput(stderr)
	o := register(fl)
	var (
		jobs      = fl.Int("jobs", 3, "how many files are processed at the same time; 0 puts no limit on it")
		recursive = fl.Bool("recursive", false, "also process the subdirectories, recreating them under output-dir")
		overwrite = fl.Bool("overwrite", false, "replace output files that already exist (default: leave them and skip their input)")
		quiet     = fl.Bool("quiet", false, "print only the failures and the summary")
	)
	fl.Usage = func() {
		fmt.Fprint(stderr, batchUsage)
		fl.PrintDefaults()
	}
	if err := fl.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fl.NArg() != 2 {
		fl.Usage()
		return errors.New("batch: need an input and an output directory")
	}
	inDir, outDir := fl.Arg(0), fl.Arg(1)
	if *o.text != "" {
		return errors.New("batch: -text makes a single image; use -watermark-text to put text on every file")
	}
	if *jobs < 0 {
		return errors.New("batch: -jobs must be 0 (no limit) or more")
	}
	ext := ""
	if *o.format != "" {
		f, ok := goimgtools.FormatByName(*o.format)
		if c, _ := goimgtools.CodecByName(f.Name); !ok || c.Encode == nil { // unknown, or only read
			return fmt.Errorf("-format: cannot write %q (%s)", *o.format, formatNames())
		}
		ext = f.Extensions[0]
	}
	if st, err := os.Stat(inDir); err != nil {
		return fmt.Errorf("batch: %w", err)
	} else if !st.IsDir() {
		return fmt.Errorf("batch: %s is not a directory", inDir)
	}
	list, others, err := collect(inDir, outDir, *recursive, ext)
	if err != nil {
		return fmt.Errorf("batch: %w", err)
	}
	if len(list) == 0 {
		return fmt.Errorf("batch: no image files in %s", inDir)
	}
	// A wrong option is wrong for every file: say so once, before starting.
	if _, err := o.image(list[0].in, list[0].out); err != nil {
		return err
	}

	var (
		mu                    sync.Mutex
		done, skipped, failed int
		keptFiles             int
		bytesIn, bytesOut     int64
		started               = time.Now()
		queue                 = make(chan job)
		wg                    sync.WaitGroup
	)
	process := func(j job) {
		report := func(line string, toErr bool) {
			mu.Lock()
			defer mu.Unlock()
			if toErr {
				fmt.Fprintln(stderr, line)
			} else if !*quiet {
				fmt.Fprintln(stdout, line)
			}
		}
		fail := func(err error) {
			mu.Lock()
			failed++
			mu.Unlock()
			report(fmt.Sprintf("FAILED %s: %v", j.rel, err), true)
		}
		if j.err != nil {
			fail(j.err)
			return
		}
		if !*overwrite {
			if _, err := os.Stat(j.out); err == nil {
				mu.Lock()
				skipped++
				mu.Unlock()
				report(fmt.Sprintf("skipped %s: %s exists", j.rel, j.out), false)
				return
			}
		}
		if err := os.MkdirAll(filepath.Dir(j.out), 0o755); err != nil {
			fail(err)
			return
		}
		im, err := o.image(j.in, j.out)
		if err != nil {
			fail(err)
			return
		}
		var inSize int64
		if st, err := os.Stat(j.in); err == nil {
			inSize = st.Size() // before the file is replaced, when it is written in place
		}
		t := time.Now()
		kept, err := o.finish(im, j.in, j.out)
		if err != nil {
			fail(err)
			return
		}
		var outSize int64
		if st, err := os.Stat(j.out); err == nil {
			outSize = st.Size()
		}
		mu.Lock()
		done++
		bytesIn, bytesOut = bytesIn+inSize, bytesOut+outSize
		if kept {
			keptFiles++
		}
		mu.Unlock()
		if kept {
			report(fmt.Sprintf("%s -> %s  kept as it is (%s): the result would not be smaller", j.rel, j.out, human(inSize)), false)
			return
		}
		report(fmt.Sprintf("%s -> %s  %s -> %s (%s)  %.2fs", j.rel, j.out, human(inSize), human(outSize),
			percent(outSize, inSize), time.Since(t).Seconds()), false)
	}
	workers := *jobs
	if workers == 0 || workers > len(list) {
		workers = len(list)
	}
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range queue {
				process(j)
			}
		}()
	}
	for _, j := range list {
		queue <- j
	}
	close(queue)
	wg.Wait()

	summary := fmt.Sprintf("%d processed", done)
	if keptFiles > 0 {
		summary += fmt.Sprintf(" (%d kept as they were: not smaller)", keptFiles)
	}
	if skipped > 0 {
		summary += fmt.Sprintf(", %d skipped (output exists; -overwrite replaces it)", skipped)
	}
	if failed > 0 {
		summary += fmt.Sprintf(", %d failed", failed)
	}
	if others > 0 {
		summary += fmt.Sprintf(", %d other files ignored", others)
	}
	if done > 0 {
		summary += fmt.Sprintf("; %s -> %s (%s)", human(bytesIn), human(bytesOut), percent(bytesOut, bytesIn))
	}
	fmt.Fprintf(stdout, "%s in %.1fs\n", summary, time.Since(started).Seconds())
	if failed > 0 {
		return fmt.Errorf("batch: %d of %d files failed", failed, len(list))
	}
	return nil
}
