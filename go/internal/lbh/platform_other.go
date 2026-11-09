//go:build !windows && !linux

package lbh

import (
	"errors"
	"os"
	"os/signal"
	"syscall"
)

// diskFreeBytes is unavailable on this platform; callers treat an error
// as "unknown" and skip the free-space check.
func diskFreeBytes(path string) (uint64, error) {
	return 0, errors.New("disk free space check unsupported on this platform")
}

var _ = signal.Ignore // keep imports stable across platforms

func terminateProcess(p *os.Process) error {
	return p.Signal(syscall.SIGTERM)
}
