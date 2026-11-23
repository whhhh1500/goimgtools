//go:build unix || windows

// Command runpeak runs a command n times and prints its best wall time,
// its largest peak memory (working set, resident set), the CPU time of the
// best run, and on Windows also its largest peak of private (committed)
// memory and the most bytes it wrote (to files, pipes and devices, which
// with the output's size known gives what went to temporary files), for the
// rows of docs/comparison.md:
//
//	runpeak [-n 3] [-label text] cmd args...
package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"time"
)

func main() {
	args := os.Args[1:]
	n, label := 3, ""
	for len(args) >= 2 && (args[0] == "-n" || args[0] == "-label") {
		if args[0] == "-n" {
			n, _ = strconv.Atoi(args[1])
		} else {
			label = args[1]
		}
		args = args[2:]
	}
	if len(args) == 0 || n < 1 {
		fmt.Fprintln(os.Stderr, "usage: runpeak [-n 3] [-label text] cmd args...")
		os.Exit(2)
	}
	best, bestCPU := time.Duration(1<<62), time.Duration(0)
	var peak, private, written int64
	for i := 0; i < n; i++ {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Stderr = os.Stderr
		t0 := time.Now()
		if err := cmd.Start(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		w := watch(cmd)
		err := cmd.Wait()
		d := time.Since(t0)
		u := w.done(cmd)
		if err != nil {
			fmt.Printf("%-44s FAILED %v\n", label, err)
			os.Exit(1)
		}
		peak, private, written = max(peak, u.peak), max(private, u.private), max(written, u.written)
		if d < best {
			best, bestCPU = d, u.cpu
		}
	}
	more := ""
	if private >= 0 && written >= 0 {
		more = fmt.Sprintf("  priv %7.1fMB  wrote %d", float64(private)/(1<<20), written)
	}
	fmt.Printf("%-44s %7.3fs  %7.1fMB  cpu %6.3fs%s\n", label, best.Seconds(), float64(peak)/(1<<20), bestCPU.Seconds(), more)
}

// usage is what one run of the command took.
type usage struct {
	peak, private int64 // bytes; private is -1 where not known
	written       int64 // bytes; -1 where not known
	cpu           time.Duration
}
