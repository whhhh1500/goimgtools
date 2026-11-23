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

// ioCounters is IO_COUNTERS.
type ioCounters struct {
	ReadOperationCount, WriteOperationCount, OtherOperationCount uint64
	ReadTransferCount, WriteTransferCount, OtherTransferCount    uint64
}

var (
	kernel32             = syscall.NewLazyDLL("kernel32.dll")
	getProcessMemoryInfo = kernel32.NewProc("K32GetProcessMemoryInfo")
	getProcessIoCounters = kernel32.NewProc("GetProcessIoCounters")
)

// watcher holds a started process open, to ask what it took once it ends.
type watcher struct{ h syscall.Handle }

func watch(cmd *exec.Cmd) watcher {
	h, _ := syscall.OpenProcess(0x0400|0x0010, false, uint32(cmd.Process.Pid))
	return watcher{h}
}

// done is what the process took: the peak of its working set and of its
// private (committed) memory, the bytes it wrote through files, pipes and
// devices, and its CPU time.
func (w watcher) done(*exec.Cmd) usage {
	defer syscall.CloseHandle(w.h)
	var c pmc
	c.cb = uint32(unsafe.Sizeof(c))
	getProcessMemoryInfo.Call(uintptr(w.h), uintptr(unsafe.Pointer(&c)), uintptr(c.cb))
	var io ioCounters
	getProcessIoCounters.Call(uintptr(w.h), uintptr(unsafe.Pointer(&io)))
	var created, exited, kernel, user syscall.Filetime
	syscall.GetProcessTimes(w.h, &created, &exited, &kernel, &user)
	ticks := func(f syscall.Filetime) int64 { return int64(f.HighDateTime)<<32 | int64(f.LowDateTime) }
	return usage{
		peak:    int64(c.PeakWorkingSetSize),
		private: int64(c.PeakPagefileUsage),
		written: int64(io.WriteTransferCount),
		cpu:     time.Duration((ticks(kernel) + ticks(user)) * 100),
	}
}
