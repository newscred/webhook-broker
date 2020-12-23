package data

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

const (
	testLockID = "testlockid"
)

type MockLockable struct {
	mock.Mock
}

func (m *MockLockable) GetLockID() string {
	return m.Called().Get(0).(string)
}

func TestNewLock(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		mockLockable := new(MockLockable)
		mockLockable.On("GetLockID").Return(testLockID)
		lock, err := NewLock(mockLockable)
		mockLockable.AssertExpectations(t)
		assert.Nil(t, err)
		assert.Equal(t, testLockID, lock.LockID)
		assert.False(t, lock.AttainedAt.IsZero())
	})
	t.Run("LockableNil", func(t *testing.T) {
		t.Parallel()
		lock, err := NewLock(nil)
		assert.Nil(t, lock)
		assert.Equal(t, ErrLockableNil, err)
	})
}
