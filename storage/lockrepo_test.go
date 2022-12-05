package storage

import (
	"testing"
	"time"

	"github.com/newscred/webhook-broker/storage/data"
	"github.com/stretchr/testify/assert"
)

func TestTryReleaseLock(t *testing.T) {
	lockRepo := NewLockRepository(testDB)
	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		msg := getMessageForJob()
		lock, _ := data.NewLock(msg)
		err := lockRepo.TryLock(lock)
		assert.Nil(t, err)
		err = lockRepo.ReleaseLock(lock)
		assert.Nil(t, err)
	})
	t.Run("lock-conflict", func(t *testing.T) {
		t.Parallel()
		msg := getMessageForJob()
		lock, _ := data.NewLock(msg)
		err := lockRepo.TryLock(lock)
		assert.Nil(t, err)
		err = lockRepo.TryLock(lock)
		assert.NotNil(t, err)
		assert.NotNil(t, ErrAlreadyLocked, err)
		err = lockRepo.ReleaseLock(lock)
		assert.Nil(t, err)
	})
	t.Run("nil-lock", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, ErrNoLock, lockRepo.TryLock(nil))
		assert.Equal(t, ErrNoLock, lockRepo.ReleaseLock(nil))
	})
}

func TestForceTimeout(t *testing.T) {
	lockRepo := NewLockRepository(testDB)
	msg := getMessageForJob()
	msg2 := getMessageForJob()
	msg3 := getMessageForJob()
	lock, _ := data.NewLock(msg)
	lock2, _ := data.NewLock(msg2)
	lock3, _ := data.NewLock(msg3)
	lock.AttainedAt = lock.AttainedAt.Add(-1 * time.Hour)
	lock3.AttainedAt = lock.AttainedAt.Add(-1 * time.Hour)
	err := lockRepo.TryLock(lock)
	assert.Nil(t, err)
	err = lockRepo.TryLock(lock2)
	assert.Nil(t, err)
	err = lockRepo.TryLock(lock3)
	assert.Nil(t, err)
	err = lockRepo.TimeoutLocks(5 * time.Second)
	assert.Nil(t, err)
	err = lockRepo.ReleaseLock(lock)
	assert.Equal(t, ErrNoRowsUpdated, err)
	err = lockRepo.ReleaseLock(lock3)
	assert.Equal(t, ErrNoRowsUpdated, err)
	err = lockRepo.ReleaseLock(lock2)
	assert.Nil(t, err)
}
