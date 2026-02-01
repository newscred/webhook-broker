package config

import (
	"time"
)

const (
	// DefaultDLQSummaryUpdateIntervalSeconds is the default interval for DLQ summary updates in seconds
	DefaultDLQSummaryUpdateIntervalSeconds = 600
)

// DLQConfig represents configuration related to DLQ observability
type DLQConfig interface {
	// GetDLQSummaryUpdateInterval returns the interval between DLQ summary update runs
	GetDLQSummaryUpdateInterval() time.Duration
}

// GetDLQSummaryUpdateInterval returns the interval between DLQ summary update runs
func (config *Config) GetDLQSummaryUpdateInterval() time.Duration {
	if config.DLQSummaryUpdateIntervalSeconds == 0 {
		return time.Duration(DefaultDLQSummaryUpdateIntervalSeconds) * time.Second
	}
	return time.Duration(config.DLQSummaryUpdateIntervalSeconds) * time.Second
}

// Generated with assistance from Claude AI
