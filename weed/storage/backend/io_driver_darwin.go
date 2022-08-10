//go:build darwin
// +build darwin

package backend

import (
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

const (
	DM_SYNC   = 1
	DM_FSYNC  = 2
	DM_BFSYNC = 3

	F_BARRIERFSYNC = 85
)

var (
	errUnsupportIODriver     = fmt.Errorf("Unsupport IO driver")
	DarwinSyncMode       int = DM_BFSYNC
)

func NewIOUringDriver(file *os.File) (IODriver, error) {
	return nil, errUnsupportIODriver
}

func (d *SyscallIODriver) Sync() error {
	switch DarwinSyncMode {
	case DM_SYNC:
		return d.File.Sync()
	case DM_BFSYNC:
		_, err := unix.FcntlInt(uintptr(d.fd), F_BARRIERFSYNC, 0)
		return err
	default:
		return syscall.Fsync(d.fd)
	}
}
