package data

import (
	"errors"
	"time"
)

var (
	// ErrLockableNil represents the error returned when lockable is nil in NewLock
	ErrLockableNil = errors.New("lockable can not be nil")
)

// Lockable represents the API necessary to lock an object for distributed MUTEX operation
type Lockable interface {
	GetLockID() string
}

// Lock represents the construct for lock information
type Lock struct {
	LockID     string
	AttainedAt time.Time
}

// NewLock returns a new instance of lock from the lockable
func NewLock(lockable Lockable) (lock *Lock, err error) {
	if lockable == nil {
		err = ErrLockableNil
	} else {
		lock = &Lock{LockID: lockable.GetLockID(), AttainedAt: time.Now()}
	}
	return lock, err
}
