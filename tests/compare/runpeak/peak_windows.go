//go:build windows

package main

import (
	"os/exec"
	"syscall"
	"time"
	"unsafe"
)

// pmc is PROCESS_MEMORY_COUNTERS.
type pmc struct {
	cb                         uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uintptr
	WorkingSetSize             uintptr
	QuotaPeakPagedPoolUsage    uintptr
	QuotaPagedPoolUsage        uintptr
	QuotaPeakNonPagedPoolUsage uintptr
	QuotaNonPagedPoolUsage     uintptr
	PagefileUsage              uintptr
	PeakPagefileUsage          uintptr
}

var getProcessMemoryInfo = syscall.NewLazyDLL("kernel32.dll").NewProc("K32GetProcessMemoryInfo")

// watcher holds a started process open, to ask what it took once it ends.
type watcher struct{ h syscall.Handle }

func watch(cmd *exec.Cmd) watcher {
	h, _ := syscall.OpenProcess(0x0400|0x0010, false, uint32(cmd.Process.Pid))
	return watcher{h}
}

// done is the peak working set and the CPU time of the process.
func (w watcher) done(*exec.Cmd) (int64, time.Duration) {
	defer syscall.CloseHandle(w.h)
	var c pmc
	c.cb = uint32(unsafe.Sizeof(c))
	getProcessMemoryInfo.Call(uintptr(w.h), uintptr(unsafe.Pointer(&c)), uintptr(c.cb))
	var created, exited, kernel, user syscall.Filetime
	syscall.GetProcessTimes(w.h, &created, &exited, &kernel, &user)
	ticks := func(f syscall.Filetime) int64 { return int64(f.HighDateTime)<<32 | int64(f.LowDateTime) }
	return int64(c.PeakWorkingSetSize), time.Duration((ticks(kernel) + ticks(user)) * 100)
}
