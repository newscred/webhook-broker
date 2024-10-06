//go:build windows

package utils

import (
	"syscall"
)

// DefaultProcessKiller is a default implementation of ProcessKiller interface
type DefaultProcessKiller struct {
}

func (pk *DefaultProcessKiller) Kill(pid int, signal syscall.Signal) error {
	return nil
}
