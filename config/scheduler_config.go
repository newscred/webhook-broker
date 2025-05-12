package config

import (
	"time"
)

const (
	// DefaultSchedulerIntervalMs is the default interval between scheduler runs in milliseconds
	DefaultSchedulerIntervalMs = 5000
	// DefaultMinScheduleDelayMinutes is the minimum delay required for scheduled messages in minutes
	DefaultMinScheduleDelayMinutes = 2
	// DefaultSchedulerBatchSize is the maximum number of messages to process per scheduler run
	DefaultSchedulerBatchSize = 100
)

// SchedulerConfig represents configuration related to message scheduling
type SchedulerConfig interface {
	// GetSchedulerInterval returns the interval between scheduler runs
	GetSchedulerInterval() time.Duration
	// GetMinScheduleDelay returns the minimum delay required for scheduled messages
	GetMinScheduleDelay() time.Duration
	// GetSchedulerBatchSize returns the maximum number of messages to process per scheduler run
	GetSchedulerBatchSize() int
}

// Add scheduler-related fields to Config struct
var _ = func() struct{} {
	// Add scheduler fields to Config struct
	type schedulerFieldsConfig struct {
		SchedulerIntervalMs     uint
		MinScheduleDelayMinutes uint
		SchedulerBatchSize      uint
	}
	return struct{}{}
}()

// GetSchedulerInterval returns the interval between scheduler runs
func (config *Config) GetSchedulerInterval() time.Duration {
	if config.SchedulerIntervalMs == 0 {
		return time.Duration(DefaultSchedulerIntervalMs) * time.Millisecond
	}
	return time.Duration(config.SchedulerIntervalMs) * time.Millisecond
}

// GetMinScheduleDelay returns the minimum delay required for scheduled messages
func (config *Config) GetMinScheduleDelay() time.Duration {
	if config.MinScheduleDelayMinutes == 0 {
		return time.Duration(DefaultMinScheduleDelayMinutes) * time.Minute
	}
	return time.Duration(config.MinScheduleDelayMinutes) * time.Minute
}

// GetSchedulerBatchSize returns the maximum number of messages to process per scheduler run
func (config *Config) GetSchedulerBatchSize() int {
	if config.SchedulerBatchSize == 0 {
		return DefaultSchedulerBatchSize
	}
	return int(config.SchedulerBatchSize)
}
