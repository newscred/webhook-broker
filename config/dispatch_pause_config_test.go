package config

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/go-ini/ini"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/assert"
)

func TestDispatchPauseConfigDefaults(t *testing.T) {
	t.Run("ZeroValueConfig", func(t *testing.T) {
		config := &Config{MaxMessageQueueSize: 100}
		assert.Equal(t, "", config.GetAdminToken())
		assert.False(t, config.IsAdminTokenConfigured())
		assert.Equal(t, 5*time.Second, config.GetRegistryRefreshInterval())
		assert.Equal(t, 1*time.Second, config.GetDrainInterval())
		assert.Equal(t, 50, config.GetDrainPageSize())
		assert.Equal(t, HandoffModeHTTP, config.GetHandoffMode())
		assert.False(t, config.IsHTTPHandoffEnabled(), "HTTP hand-off needs an admin token")
		assert.Equal(t, 60*time.Second, config.GetHandoffLease())
		assert.Equal(t, 5*time.Second, config.GetHandoffTimeout())
		assert.Equal(t, 3, config.GetHandoffMaxAttempts())
		assert.Equal(t, 4, config.GetHandoffConcurrency())
		assert.Equal(t, 50, config.GetHandoffHighWaterMark())
		assert.True(t, config.IsHandoffLocalFallbackEnabled())
	})
	t.Run("DefaultConfiguration", func(t *testing.T) {
		config, err := GetConfigurationFromParseConfig(loadTestConfiguration(""))
		assert.Nil(t, err)
		assert.False(t, config.IsAdminTokenConfigured())
		assert.Equal(t, uint(5), config.RegistryRefreshIntervalSeconds)
		assert.Equal(t, uint(1), config.DrainIntervalSeconds)
		assert.Equal(t, uint(50), config.DrainPageSize)
		assert.Equal(t, HandoffModeHTTP, config.HandoffMode)
		assert.Equal(t, uint(60), config.HandoffLeaseSeconds)
		assert.Equal(t, int(config.MaxMessageQueueSize/2), config.GetHandoffHighWaterMark())
		assert.True(t, config.IsHandoffLocalFallbackEnabled())
	})
	t.Run("MissingSection", func(t *testing.T) {
		iniFile, err := ini.Load([]byte("[other]\nkey=value\n"))
		assert.NoError(t, err)
		config := &Config{}
		setupDispatchPauseConfiguration(iniFile, config)
		assert.Equal(t, uint(0), config.DrainPageSize)
		assert.Equal(t, 50, config.GetDrainPageSize())
	})
	t.Run("HighWaterMarkNeverZero", func(t *testing.T) {
		config := &Config{MaxMessageQueueSize: 1}
		assert.Equal(t, 1, config.GetHandoffHighWaterMark())
	})
}

func TestDispatchPauseConfigCustomValues(t *testing.T) {
	testConfig := `[broker]
rational-delay-in-seconds=2
[dispatch-pause]
admin-token=secret-admin-token
registry-refresh-interval-seconds=10
drain-interval-seconds=3
drain-page-size=200
handoff-mode=local
handoff-lease-seconds=120
handoff-timeout-seconds=7
handoff-max-attempts=5
handoff-concurrency=8
handoff-high-water-mark=1234
handoff-local-fallback=false
`
	config, err := GetConfigurationFromParseConfig(loadTestConfiguration(testConfig))
	assert.Nil(t, err)
	assert.Equal(t, "secret-admin-token", config.GetAdminToken())
	assert.True(t, config.IsAdminTokenConfigured())
	assert.Equal(t, 10*time.Second, config.GetRegistryRefreshInterval())
	assert.Equal(t, 3*time.Second, config.GetDrainInterval())
	assert.Equal(t, 200, config.GetDrainPageSize())
	assert.Equal(t, HandoffModeLocal, config.GetHandoffMode())
	assert.False(t, config.IsHTTPHandoffEnabled(), "local mode never hands off over HTTP")
	assert.Equal(t, 120*time.Second, config.GetHandoffLease())
	assert.Equal(t, 7*time.Second, config.GetHandoffTimeout())
	assert.Equal(t, 5, config.GetHandoffMaxAttempts())
	assert.Equal(t, 8, config.GetHandoffConcurrency())
	assert.Equal(t, 1234, config.GetHandoffHighWaterMark())
	assert.False(t, config.IsHandoffLocalFallbackEnabled())

	config.HandoffMode = HandoffModeHTTP
	assert.True(t, config.IsHTTPHandoffEnabled())
}

func TestDispatchPauseConfigWrongValues(t *testing.T) {
	var buf bytes.Buffer
	oldLogger := log.Logger
	log.Logger = log.Output(&buf)
	defer func() { log.Logger = oldLogger }()
	testConfig := `[dispatch-pause]
registry-refresh-interval-seconds=0
drain-interval-seconds=61
drain-page-size=501
handoff-mode=carrier-pigeon
handoff-lease-seconds=abc
handoff-timeout-seconds=0
handoff-max-attempts=11
handoff-concurrency=65
handoff-high-water-mark=-1
`
	config, err := GetConfigurationFromParseConfig(loadTestConfiguration(testConfig))
	assert.Nil(t, err)
	assert.Equal(t, DefaultRegistryRefreshIntervalSeconds, config.RegistryRefreshIntervalSeconds)
	assert.Equal(t, DefaultDrainIntervalSeconds, config.DrainIntervalSeconds)
	assert.Equal(t, DefaultDrainPageSize, config.DrainPageSize)
	assert.Equal(t, HandoffModeHTTP, config.HandoffMode)
	assert.Equal(t, DefaultHandoffLeaseSeconds, config.HandoffLeaseSeconds)
	assert.Equal(t, DefaultHandoffTimeoutSeconds, config.HandoffTimeoutSeconds)
	assert.Equal(t, DefaultHandoffMaxAttempts, config.HandoffMaxAttempts)
	assert.Equal(t, DefaultHandoffConcurrency, config.HandoffConcurrency)
	assert.Equal(t, uint(0), config.HandoffHighWaterMark)
	for _, key := range []string{"registry-refresh-interval-seconds", "drain-interval-seconds", "drain-page-size", "handoff-mode", "handoff-lease-seconds", "handoff-timeout-seconds", "handoff-max-attempts", "handoff-concurrency", "handoff-high-water-mark"} {
		assert.Contains(t, buf.String(), key, "a warning must name the invalid key")
	}
}

func TestDispatchPauseConfigHandoffLeaseMinimum(t *testing.T) {
	config := &Config{HandoffLeaseSeconds: 1, RationalDelay: 2 * time.Second}
	assert.Equal(t, 10*time.Second, config.GetHandoffLease(), "lease is never shorter than five rational delays")
}

func TestDispatchPauseConfigAdminTokenNeverSerialized(t *testing.T) {
	config := &Config{DispatchPauseAdminToken: "very-secret"}
	encoded, err := json.Marshal(config)
	assert.NoError(t, err)
	assert.NotContains(t, string(encoded), "very-secret", "the config is logged at startup; the admin token must not be in it")
}

// Generated with assistance from Claude AI
