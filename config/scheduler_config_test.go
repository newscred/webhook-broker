package config

import (
	"testing"
	"time"

	"github.com/go-ini/ini"
	"github.com/stretchr/testify/assert"
)

func TestSchedulerConfig(t *testing.T) {
	t.Run("default values", func(t *testing.T) {
		// Arrange
		config := &Config{}

		// Act & Assert
		assert.Equal(t, time.Duration(DefaultSchedulerIntervalMs)*time.Millisecond, config.GetSchedulerInterval())
		assert.Equal(t, time.Duration(DefaultMinScheduleDelayMinutes)*time.Minute, config.GetMinScheduleDelay())
		assert.Equal(t, DefaultSchedulerBatchSize, config.GetSchedulerBatchSize())
	})

	t.Run("custom values", func(t *testing.T) {
		// Arrange
		config := &Config{
			SchedulerIntervalMs:     10000,
			MinScheduleDelayMinutes: 5,
			SchedulerBatchSize:      200,
		}

		// Act & Assert
		assert.Equal(t, 10*time.Second, config.GetSchedulerInterval())
		assert.Equal(t, 5*time.Minute, config.GetMinScheduleDelay())
		assert.Equal(t, 200, config.GetSchedulerBatchSize())
	})

	t.Run("configuration from ini file", func(t *testing.T) {
		// Arrange
		iniStr := `
[scheduler]
scheduler-interval-ms=15000
min-schedule-delay-minutes=10
scheduler-batch-size=300
`
		iniFile, err := ini.Load([]byte(iniStr))
		assert.NoError(t, err)
		config := &Config{}

		// Act
		setupSchedulerConfiguration(iniFile, config)

		// Assert
		assert.Equal(t, uint(15000), config.SchedulerIntervalMs)
		assert.Equal(t, uint(10), config.MinScheduleDelayMinutes)
		assert.Equal(t, uint(300), config.SchedulerBatchSize)
		assert.Equal(t, 15*time.Second, config.GetSchedulerInterval())
		assert.Equal(t, 10*time.Minute, config.GetMinScheduleDelay())
		assert.Equal(t, 300, config.GetSchedulerBatchSize())
	})

	t.Run("missing section uses defaults", func(t *testing.T) {
		// Arrange
		iniStr := `
[other_section]
some-key=some-value
`
		iniFile, err := ini.Load([]byte(iniStr))
		assert.NoError(t, err)
		config := &Config{}

		// Act
		setupSchedulerConfiguration(iniFile, config)

		// Assert
		assert.Equal(t, uint(0), config.SchedulerIntervalMs)
		assert.Equal(t, uint(0), config.MinScheduleDelayMinutes)
		assert.Equal(t, uint(0), config.SchedulerBatchSize)
		assert.Equal(t, time.Duration(DefaultSchedulerIntervalMs)*time.Millisecond, config.GetSchedulerInterval())
		assert.Equal(t, time.Duration(DefaultMinScheduleDelayMinutes)*time.Minute, config.GetMinScheduleDelay())
		assert.Equal(t, DefaultSchedulerBatchSize, config.GetSchedulerBatchSize())
	})
}