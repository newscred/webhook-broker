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
		assert.Equal(t, ErrAlreadyLocked, err)
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

func TestRenewLock(t *testing.T) {
	lockRepo := NewLockRepository(testDB)
	t.Run("Held", func(t *testing.T) {
		t.Parallel()
		lock, _ := data.NewLock(getMessageForJob())
		lock.AttainedAt = lock.AttainedAt.Add(-1 * time.Minute)
		original := lock.AttainedAt
		assert.Nil(t, lockRepo.TryLock(lock))
		assert.Nil(t, lockRepo.RenewLock(lock))
		assert.True(t, lock.AttainedAt.After(original))
		var attainedAt time.Time
		assert.Nil(t, testDB.QueryRow("SELECT attainedAt FROM `lock` WHERE lockId = ?", lock.LockID).Scan(&attainedAt))
		assert.True(t, attainedAt.After(original))
		assert.Nil(t, lockRepo.ReleaseLock(lock))
	})
	t.Run("TimedOut", func(t *testing.T) {
		lock, _ := data.NewLock(getMessageForJob())
		lock.AttainedAt = lock.AttainedAt.Add(-1 * time.Hour)
		assert.Nil(t, lockRepo.TryLock(lock))
		assert.Nil(t, lockRepo.TimeoutLocks(30*time.Minute))
		assert.Equal(t, ErrNoRowsUpdated, lockRepo.RenewLock(lock))
	})
	t.Run("NilLock", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, ErrNoLock, lockRepo.RenewLock(nil))
	})
}

func TestNormalizeDBErrorSQLite(t *testing.T) {
	lockRepo := NewLockRepository(testDB)
	lock, _ := data.NewLock(getMessageForJob())
	assert.Nil(t, lockRepo.TryLock(lock))
	defer lockRepo.ReleaseLock(lock)
	_, err := testDB.Exec("INSERT INTO `lock` (lockId, attainedAt) VALUES (?, ?)", lock.LockID, time.Now())
	assert.NotNil(t, err)
	mapped := normalizeDBError(err, lockErrorMap)
	assert.Equal(t, ErrAlreadyLocked, mapped, "unique violation from go-sqlite3 must map to the duplicate key error")
	_, fkErr := testDB.Exec("INSERT INTO dispatch_pause (scope, consumerId, reason, pausedAt) VALUES (?, ?, ?, ?)", "fk-violation", "no-such-consumer", "", time.Now())
	assert.NotNil(t, fkErr)
	assert.NotEqual(t, ErrAlreadyLocked, normalizeDBError(fkErr, lockErrorMap), "foreign key violations must not be treated as duplicates")
}
