//go:build !windows && !linux && !darwin && !freebsd && !netbsd && !openbsd && !dragonfly

package main

import "time"

// On this system the process's own memory and CPU time are not read: the
// figures are reported as zero.

func peakWorkingSet() uint64 { return 0 }

func cpuTime() time.Duration { return 0 }
