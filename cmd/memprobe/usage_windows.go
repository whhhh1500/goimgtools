package main

import (
	"syscall"
	"time"
	"unsafe"
)

// peakWorkingSet returns the peak resident memory of the process.
func peakWorkingSet() uint64 {
	var c struct {
		cb                         uint32
		pageFaultCount             uint32
		peakWorkingSetSize         uintptr
		workingSetSize             uintptr
		quotaPeakPagedPoolUsage    uintptr
		quotaPagedPoolUsage        uintptr
		quotaPeakNonPagedPoolUsage uintptr
		quotaNonPagedPoolUsage     uintptr
		pagefileUsage              uintptr
		peakPagefileUsage          uintptr
	}
	c.cb = uint32(unsafe.Sizeof(c))
	h, _ := syscall.GetCurrentProcess()
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("K32GetProcessMemoryInfo")
	proc.Call(uintptr(h), uintptr(unsafe.Pointer(&c)), uintptr(c.cb))
	return uint64(c.peakWorkingSetSize)
}

// cpuTime returns the user + kernel CPU time of the process.
func cpuTime() time.Duration {
	var c, e, k, u syscall.Filetime
	h, _ := syscall.GetCurrentProcess()
	syscall.GetProcessTimes(h, &c, &e, &k, &u)
	raw := func(f syscall.Filetime) int64 { return (int64(f.HighDateTime)<<32 | int64(f.LowDateTime)) * 100 }
	return time.Duration(raw(k) + raw(u))
}
