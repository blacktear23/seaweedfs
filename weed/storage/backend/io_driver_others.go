//go:build !linux && !darwin
// +build !linux,!darwin

package backend

import (
	"fmt"
	"os"
)

var (
	errUnsupportIODriver = fmt.Errorf("Unsupport IO driver")
)

func NewIOUringDriver(file *os.File) (IODriver, error) {
	return nil, errUnsupportIODriver
}

func (d *SyscallIODriver) Sync() error {
	return d.File.Sync()
}
