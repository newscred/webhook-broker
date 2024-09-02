//go:build linux || darwin

package utils

import (
	"os"
	"syscall"
)

// DefaultProcessKiller is a default implementation of ProcessKiller interface
type DefaultProcessKiller struct{}

func (pk *DefaultProcessKiller) Kill(pid int, signal syscall.Signal) error {
	_, err := os.FindProcess(pid)
	if err != nil {
		return err
	}

	err = syscall.Kill(pid, syscall.SIGINT)
	if err != nil {
		return err
	}

	return nil
}
