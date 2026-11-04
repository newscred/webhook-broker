package data

import (
	"time"
)

const (
	// GlobalDispatchPauseScope is the scope of the broker-wide dispatch pause row
	GlobalDispatchPauseScope = "__global__"
	// MaxDispatchPauseReasonLength is the maximum length of a pause reason
	MaxDispatchPauseReasonLength = 1024
	jobParkingDrainerLockID      = "job-parking-drainer"
)

// DispatchPause represents a pause of dispatching either for a single consumer or for the whole broker.
// A consumer pause has Scope == ConsumerID == the internal consumer.id; the global pause has
// Scope == GlobalDispatchPauseScope and an empty ConsumerID.
type DispatchPause struct {
	Scope      string
	ConsumerID string
	Reason     string
	PausedAt   time.Time
}

// IsGlobal returns true if this is the broker-wide pause
func (pause *DispatchPause) IsGlobal() bool {
	return pause.Scope == GlobalDispatchPauseScope
}

// QuickFix defaults PausedAt to now and trims Reason to MaxDispatchPauseReasonLength
func (pause *DispatchPause) QuickFix() bool {
	madeChanges := false
	if pause.PausedAt.IsZero() {
		pause.PausedAt = time.Now()
		madeChanges = true
	}
	if len(pause.Reason) > MaxDispatchPauseReasonLength {
		pause.Reason = pause.Reason[:MaxDispatchPauseReasonLength]
		madeChanges = true
	}
	return madeChanges
}

// IsInValidState returns true if the scope and consumer id are consistent, the reason is within
// limits and PausedAt is set
func (pause *DispatchPause) IsInValidState() bool {
	if len(pause.Scope) == 0 || pause.PausedAt.IsZero() || len(pause.Reason) > MaxDispatchPauseReasonLength {
		return false
	}
	if pause.IsGlobal() {
		return len(pause.ConsumerID) == 0
	}
	return pause.ConsumerID == pause.Scope
}

// NewGlobalDispatchPause creates the broker-wide dispatch pause
func NewGlobalDispatchPause(reason string) *DispatchPause {
	pause := &DispatchPause{Scope: GlobalDispatchPauseScope, Reason: reason}
	pause.QuickFix()
	return pause
}

// NewConsumerDispatchPause creates a dispatch pause for a single consumer
func NewConsumerDispatchPause(consumer *Consumer, reason string) (*DispatchPause, error) {
	if consumer == nil || consumer.ID.IsNil() {
		return nil, ErrInsufficientInformationForCreating
	}
	consumerID := consumer.ID.String()
	pause := &DispatchPause{Scope: consumerID, ConsumerID: consumerID, Reason: reason}
	pause.QuickFix()
	return pause, nil
}

// DrainLock implements Lockable for the job parking drainer
type DrainLock struct{}

// GetLockID returns the fixed lock ID for the job parking drainer
func (l *DrainLock) GetLockID() string {
	return jobParkingDrainerLockID
}

// Generated with assistance from Claude AI
