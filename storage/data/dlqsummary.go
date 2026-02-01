package data

import "time"

const (
	dlqSummaryUpdaterLockID = "dlq-summary-updater"
)

// DLQSummary represents the pre-computed dead job count for a consumer
type DLQSummary struct {
	ConsumerID   string
	ConsumerName string
	ChannelID    string
	ChannelName  string
	DeadCount    int64
	LastCheckedAt time.Time
}

// DLQSummaryLock implements Lockable for the DLQ summary updater
type DLQSummaryLock struct{}

// GetLockID returns the fixed lock ID for the DLQ summary updater
func (l *DLQSummaryLock) GetLockID() string {
	return dlqSummaryUpdaterLockID
}

// Generated with assistance from Claude AI
