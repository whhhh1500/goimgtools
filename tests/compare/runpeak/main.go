//go:build unix || windows

// Command runpeak runs a command n times and prints its best wall time,
// its largest peak memory (working set, resident set) and the CPU time of
// the best run, for the rows of docs/comparison.md:
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
	var peak int64
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
		mem, cpu := w.done(cmd)
		if err != nil {
			fmt.Printf("%-44s FAILED %v\n", label, err)
			os.Exit(1)
		}
		peak = max(peak, mem)
		if d < best {
			best, bestCPU = d, cpu
		}
	}
	fmt.Printf("%-44s %7.3fs  %7.1fMB  cpu %6.3fs\n", label, best.Seconds(), float64(peak)/(1<<20), bestCPU.Seconds())
}
