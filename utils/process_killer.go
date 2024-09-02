package utils

import "syscall"

// ProcessKiller interface for killing a process
type ProcessKiller interface {
	Kill(PID int, signal syscall.Signal) error
}

func NewProcessKiller() ProcessKiller {
	return &DefaultProcessKiller{}
}
