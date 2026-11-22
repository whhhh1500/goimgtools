//go:build unix

package main

import (
	"os/exec"
	"runtime"
	"syscall"
	"time"
)

type watcher struct{}

func watch(*exec.Cmd) watcher { return watcher{} }

// done is the peak resident set and the CPU time of the process.
func (watcher) done(cmd *exec.Cmd) (int64, time.Duration) {
	ru, ok := cmd.ProcessState.SysUsage().(*syscall.Rusage)
	if !ok {
		return 0, 0
	}
	rss := int64(ru.Maxrss) // kilobytes, but bytes on macOS and iOS
	if runtime.GOOS != "darwin" && runtime.GOOS != "ios" {
		rss <<= 10
	}
	return rss, cmd.ProcessState.UserTime() + cmd.ProcessState.SystemTime()
}
