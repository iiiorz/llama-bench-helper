//go:build linux

package lbh

import (
	"os"
	"syscall"
)

// diskFreeBytes returns the free bytes available to the caller on the
// filesystem containing path (Linux implementation).
func diskFreeBytes(path string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return st.Bavail * uint64(st.Bsize), nil
}

// terminateProcess sends SIGTERM so the child can shut down cleanly.
func terminateProcess(p *os.Process) error {
	return p.Signal(syscall.SIGTERM)
}
