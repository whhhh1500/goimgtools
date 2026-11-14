//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package main

import (
	"runtime"
	"syscall"
	"time"
)

// peakWorkingSet returns the peak resident memory of the process.
func peakWorkingSet() uint64 {
	var ru syscall.Rusage
	if syscall.Getrusage(syscall.RUSAGE_SELF, &ru) != nil {
		return 0
	}
	// The kernel reports kilobytes, except on macOS, where it is bytes.
	if runtime.GOOS == "darwin" {
		return uint64(ru.Maxrss)
	}
	return uint64(ru.Maxrss) << 10
}

// cpuTime returns the user + kernel CPU time of the process.
func cpuTime() time.Duration {
	var ru syscall.Rusage
	if syscall.Getrusage(syscall.RUSAGE_SELF, &ru) != nil {
		return 0
	}
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
}
