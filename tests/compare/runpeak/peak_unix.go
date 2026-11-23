//go:build unix

package main

import (
	"os/exec"
	"runtime"
	"syscall"
)

type watcher struct{}

func watch(*exec.Cmd) watcher { return watcher{} }

// done is the peak resident set and the CPU time of the process; the
// private memory and the bytes written are not known here (-1).
func (watcher) done(cmd *exec.Cmd) usage {
	u := usage{private: -1, written: -1}
	ru, ok := cmd.ProcessState.SysUsage().(*syscall.Rusage)
	if !ok {
		return u
	}
	u.peak = int64(ru.Maxrss) // kilobytes, but bytes on macOS and iOS
	if runtime.GOOS != "darwin" && runtime.GOOS != "ios" {
		u.peak <<= 10
	}
	u.cpu = cmd.ProcessState.UserTime() + cmd.ProcessState.SystemTime()
	return u
}
