package config

import (
	"testing"

	"github.com/go-ini/ini"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConsumerMaxConcurrency(t *testing.T) {
	t.Run("default is no limit", func(t *testing.T) {
		iniFile, err := ini.LooseLoad([]byte(DefaultConfiguration))
		require.NoError(t, err)
		config := &Config{}

		setupBrokerConfiguration(iniFile, config)

		assert.Equal(t, uint(0), config.GetConsumerMaxConcurrency("any-channel", "any-consumer"))
	})

	t.Run("broker default and overrides from ini file", func(t *testing.T) {
		iniStr := `
[broker]
consumer-max-concurrency=8
[consumer-max-concurrency]
Slow-Consumer=2
step-events/Slow-Consumer=5
unlimited-consumer=0
broken-consumer=abc
`
		iniFile, err := ini.LooseLoad([]byte(DefaultConfiguration), []byte(iniStr))
		require.NoError(t, err)
		config := &Config{}

		setupBrokerConfiguration(iniFile, config)

		assert.Equal(t, uint(8), config.GetConsumerMaxConcurrency("any-channel", "other-consumer"))
		assert.Equal(t, uint(2), config.GetConsumerMaxConcurrency("workflow-events", "Slow-Consumer"))
		assert.Equal(t, uint(5), config.GetConsumerMaxConcurrency("step-events", "Slow-Consumer"))
		assert.Equal(t, uint(0), config.GetConsumerMaxConcurrency("any-channel", "unlimited-consumer"))
		assert.Equal(t, uint(8), config.GetConsumerMaxConcurrency("any-channel", "broken-consumer"))
		assert.Equal(t, uint(8), config.GetConsumerMaxConcurrency("any-channel", "slow-consumer"), "consumer ids are case sensitive")
	})
}

// Generated with assistance from Claude AI
