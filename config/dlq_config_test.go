package config

import (
	"testing"
	"time"

	"github.com/go-ini/ini"
	"github.com/stretchr/testify/assert"
)

func TestDLQConfig(t *testing.T) {
	t.Run("default values", func(t *testing.T) {
		config := &Config{}
		assert.Equal(t, time.Duration(DefaultDLQSummaryUpdateIntervalSeconds)*time.Second, config.GetDLQSummaryUpdateInterval())
	})

	t.Run("custom values", func(t *testing.T) {
		config := &Config{
			DLQSummaryUpdateIntervalSeconds: 120,
		}
		assert.Equal(t, 2*time.Minute, config.GetDLQSummaryUpdateInterval())
	})

	t.Run("configuration from ini file", func(t *testing.T) {
		iniStr := `
[dlq]
summary-update-interval-seconds=300
`
		iniFile, err := ini.Load([]byte(iniStr))
		assert.NoError(t, err)
		config := &Config{}
		setupDLQConfiguration(iniFile, config)
		assert.Equal(t, uint(300), config.DLQSummaryUpdateIntervalSeconds)
		assert.Equal(t, 5*time.Minute, config.GetDLQSummaryUpdateInterval())
	})

	t.Run("missing section uses defaults", func(t *testing.T) {
		iniStr := `
[other_section]
some-key=some-value
`
		iniFile, err := ini.Load([]byte(iniStr))
		assert.NoError(t, err)
		config := &Config{}
		setupDLQConfiguration(iniFile, config)
		assert.Equal(t, uint(0), config.DLQSummaryUpdateIntervalSeconds)
		assert.Equal(t, time.Duration(DefaultDLQSummaryUpdateIntervalSeconds)*time.Second, config.GetDLQSummaryUpdateInterval())
	})
}

// Generated with assistance from Claude AI
