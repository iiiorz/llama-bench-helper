//go:build windows

package lbh

import (
	"os"
	"syscall"
	"unsafe"
)

var kernel32 = syscall.NewLazyDLL("kernel32.dll")
var procGetDiskFreeSpaceExW = kernel32.NewProc("GetDiskFreeSpaceExW")

// diskFreeBytes returns the free bytes available to the caller on the
// volume containing path (Windows implementation).
func diskFreeBytes(path string) (uint64, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var freeAvailable, total, totalFree uint64
	r, _, callErr := procGetDiskFreeSpaceExW.Call(
		uintptr(unsafe.Pointer(p)),
		uintptr(unsafe.Pointer(&freeAvailable)),
		uintptr(unsafe.Pointer(&total)),
		uintptr(unsafe.Pointer(&totalFree)),
	)
	if r == 0 {
		return 0, callErr
	}
	return freeAvailable, nil
}

// terminateProcess asks a child process to exit. Windows has no SIGTERM
// equivalent for console processes, so this is a hard kill there.
func terminateProcess(p *os.Process) error {
	return p.Kill()
}
