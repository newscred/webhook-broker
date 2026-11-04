package data

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestDispatchPauseQuickFix(t *testing.T) {
	t.Run("DefaultsPausedAt", func(t *testing.T) {
		t.Parallel()
		pause := &DispatchPause{Scope: GlobalDispatchPauseScope}
		assert.True(t, pause.QuickFix())
		assert.False(t, pause.PausedAt.IsZero())
	})
	t.Run("TrimsReason", func(t *testing.T) {
		t.Parallel()
		pause := &DispatchPause{Scope: GlobalDispatchPauseScope, PausedAt: time.Now(), Reason: strings.Repeat("r", MaxDispatchPauseReasonLength+10)}
		assert.True(t, pause.QuickFix())
		assert.Equal(t, MaxDispatchPauseReasonLength, len(pause.Reason))
	})
	t.Run("NoChange", func(t *testing.T) {
		t.Parallel()
		pause := &DispatchPause{Scope: GlobalDispatchPauseScope, PausedAt: time.Now(), Reason: "ok"}
		assert.False(t, pause.QuickFix())
	})
}

func TestDispatchPauseIsInValidState(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name  string
		pause DispatchPause
		valid bool
	}{
		{"ValidGlobal", DispatchPause{Scope: GlobalDispatchPauseScope, PausedAt: now}, true},
		{"ValidConsumer", DispatchPause{Scope: "c1", ConsumerID: "c1", PausedAt: now}, true},
		{"GlobalWithConsumerID", DispatchPause{Scope: GlobalDispatchPauseScope, ConsumerID: "c1", PausedAt: now}, false},
		{"ConsumerScopeMismatch", DispatchPause{Scope: "c1", ConsumerID: "c2", PausedAt: now}, false},
		{"ConsumerWithoutConsumerID", DispatchPause{Scope: "c1", PausedAt: now}, false},
		{"EmptyScope", DispatchPause{PausedAt: now}, false},
		{"ZeroPausedAt", DispatchPause{Scope: GlobalDispatchPauseScope}, false},
		{"ReasonTooLong", DispatchPause{Scope: GlobalDispatchPauseScope, PausedAt: now, Reason: strings.Repeat("r", MaxDispatchPauseReasonLength+1)}, false},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, test.valid, test.pause.IsInValidState())
		})
	}
}

func TestNewGlobalDispatchPause(t *testing.T) {
	pause := NewGlobalDispatchPause("incident")
	assert.True(t, pause.IsGlobal())
	assert.Equal(t, "incident", pause.Reason)
	assert.Empty(t, pause.ConsumerID)
	assert.True(t, pause.IsInValidState())
}

func TestNewConsumerDispatchPause(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		consumer := getConsumer()
		pause, err := NewConsumerDispatchPause(consumer, "migration")
		assert.Nil(t, err)
		assert.False(t, pause.IsGlobal())
		assert.Equal(t, consumer.ID.String(), pause.Scope)
		assert.Equal(t, consumer.ID.String(), pause.ConsumerID)
		assert.True(t, pause.IsInValidState())
	})
	t.Run("NilConsumer", func(t *testing.T) {
		t.Parallel()
		_, err := NewConsumerDispatchPause(nil, "")
		assert.Equal(t, ErrInsufficientInformationForCreating, err)
	})
	t.Run("ConsumerWithoutID", func(t *testing.T) {
		t.Parallel()
		_, err := NewConsumerDispatchPause(&Consumer{}, "")
		assert.Equal(t, ErrInsufficientInformationForCreating, err)
	})
}

func TestDrainLockGetLockID(t *testing.T) {
	lockable := &DrainLock{}
	assert.Equal(t, "job-parking-drainer", lockable.GetLockID())
	lock, err := NewLock(lockable)
	assert.Nil(t, err)
	assert.Equal(t, "job-parking-drainer", lock.LockID)
}

// Generated with assistance from Claude AI
