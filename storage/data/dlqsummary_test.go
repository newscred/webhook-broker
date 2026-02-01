package data

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDLQSummaryLockGetLockID(t *testing.T) {
	lock := &DLQSummaryLock{}
	assert.Equal(t, "dlq-summary-updater", lock.GetLockID())
}

func TestDLQSummaryLockNewLock(t *testing.T) {
	lockable := &DLQSummaryLock{}
	lock, err := NewLock(lockable)
	assert.NoError(t, err)
	assert.NotNil(t, lock)
	assert.Equal(t, "dlq-summary-updater", lock.LockID)
	assert.False(t, lock.AttainedAt.IsZero())
}

// Generated with assistance from Claude AI
