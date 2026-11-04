package config

import (
	"time"

	"github.com/go-ini/ini"
	"github.com/rs/zerolog/log"
)

const (
	dispatchPauseSectionName = "dispatch-pause"
	// HandoffModeHTTP hands drained jobs to the cluster through the load-balanced broker endpoint
	HandoffModeHTTP = "http"
	// HandoffModeLocal enqueues drained jobs on the draining node only
	HandoffModeLocal = "local"

	// DefaultRegistryRefreshIntervalSeconds is the default interval for refreshing the per-node pause snapshot
	DefaultRegistryRefreshIntervalSeconds uint = 5
	// DefaultDrainIntervalSeconds is the default interval between drainer runs
	DefaultDrainIntervalSeconds uint = 1
	// DefaultDrainPageSize is the default number of parked jobs moved per drain page
	DefaultDrainPageSize uint = 50
	// DefaultHandoffMode is the default hand-off mode
	DefaultHandoffMode = HandoffModeHTTP
	// DefaultHandoffLeaseSeconds is the default delay before the retry worker may pick up handed-off jobs
	DefaultHandoffLeaseSeconds uint = 60
	// DefaultHandoffTimeoutSeconds is the default timeout of a hand-off HTTP request
	DefaultHandoffTimeoutSeconds uint = 5
	// DefaultHandoffMaxAttempts is the default number of hand-off attempts per batch
	DefaultHandoffMaxAttempts uint = 3
	// DefaultHandoffConcurrency is the default number of concurrent hand-off requests
	DefaultHandoffConcurrency uint = 4
	// DefaultHandoffLocalFallback is whether a batch is enqueued locally once every hand-off attempt failed
	DefaultHandoffLocalFallback = true

	minHandoffLeaseRationalDelayMultiple = 5
)

// DispatchPauseConfig represents configuration for dispatch pause, job parking drain and hand-off
type DispatchPauseConfig interface {
	// GetAdminToken returns the shared admin token for global pause and internal hand-off endpoints
	GetAdminToken() string
	// IsAdminTokenConfigured returns true if an admin token is set; global and internal endpoints are disabled otherwise
	IsAdminTokenConfigured() bool
	// GetRegistryRefreshInterval returns how often each node refreshes its pause snapshot
	GetRegistryRefreshInterval() time.Duration
	// GetDrainInterval returns the interval between drainer runs
	GetDrainInterval() time.Duration
	// GetDrainPageSize returns the maximum number of parked jobs moved per drain page
	GetDrainPageSize() int
	// GetHandoffMode returns HandoffModeHTTP or HandoffModeLocal
	GetHandoffMode() string
	// IsHTTPHandoffEnabled returns true if drained jobs are handed to the cluster over HTTP (needs an admin token)
	IsHTTPHandoffEnabled() bool
	// GetHandoffLease returns how long handed-off jobs are hidden from the retry worker
	GetHandoffLease() time.Duration
	// GetHandoffTimeout returns the timeout of a hand-off request
	GetHandoffTimeout() time.Duration
	// GetHandoffMaxAttempts returns the number of hand-off attempts per batch
	GetHandoffMaxAttempts() int
	// GetHandoffConcurrency returns the number of concurrent hand-off requests
	GetHandoffConcurrency() int
	// GetHandoffHighWaterMark returns the local queue depth above which a node rejects hand-offs with 503
	GetHandoffHighWaterMark() int
	// IsHandoffLocalFallbackEnabled returns true if a batch is enqueued locally after all hand-off attempts failed
	IsHandoffLocalFallbackEnabled() bool
}

// GetAdminToken returns the shared admin token
func (config *Config) GetAdminToken() string {
	return config.DispatchPauseAdminToken
}

// IsAdminTokenConfigured returns true if an admin token is set
func (config *Config) IsAdminTokenConfigured() bool {
	return len(config.DispatchPauseAdminToken) > 0
}

func secondsOrDefault(value, defaultValue uint) time.Duration {
	if value == 0 {
		value = defaultValue
	}
	return time.Duration(value) * time.Second
}

func uintOrDefault(value, defaultValue uint) int {
	if value == 0 {
		value = defaultValue
	}
	return int(value)
}

// GetRegistryRefreshInterval returns how often each node refreshes its pause snapshot
func (config *Config) GetRegistryRefreshInterval() time.Duration {
	return secondsOrDefault(config.RegistryRefreshIntervalSeconds, DefaultRegistryRefreshIntervalSeconds)
}

// GetDrainInterval returns the interval between drainer runs
func (config *Config) GetDrainInterval() time.Duration {
	return secondsOrDefault(config.DrainIntervalSeconds, DefaultDrainIntervalSeconds)
}

// GetDrainPageSize returns the maximum number of parked jobs moved per drain page
func (config *Config) GetDrainPageSize() int {
	return uintOrDefault(config.DrainPageSize, DefaultDrainPageSize)
}

// GetHandoffMode returns HandoffModeHTTP or HandoffModeLocal
func (config *Config) GetHandoffMode() string {
	if config.HandoffMode != HandoffModeHTTP && config.HandoffMode != HandoffModeLocal {
		return DefaultHandoffMode
	}
	return config.HandoffMode
}

// IsHTTPHandoffEnabled returns true if drained jobs are handed to the cluster over HTTP
func (config *Config) IsHTTPHandoffEnabled() bool {
	return config.GetHandoffMode() == HandoffModeHTTP && config.IsAdminTokenConfigured()
}

// GetHandoffLease returns how long handed-off jobs are hidden from the retry worker; never less than five rational delays
func (config *Config) GetHandoffLease() time.Duration {
	lease := secondsOrDefault(config.HandoffLeaseSeconds, DefaultHandoffLeaseSeconds)
	if minimum := minHandoffLeaseRationalDelayMultiple * config.RationalDelay; lease < minimum {
		return minimum
	}
	return lease
}

// GetHandoffTimeout returns the timeout of a hand-off request
func (config *Config) GetHandoffTimeout() time.Duration {
	return secondsOrDefault(config.HandoffTimeoutSeconds, DefaultHandoffTimeoutSeconds)
}

// GetHandoffMaxAttempts returns the number of hand-off attempts per batch
func (config *Config) GetHandoffMaxAttempts() int {
	return uintOrDefault(config.HandoffMaxAttempts, DefaultHandoffMaxAttempts)
}

// GetHandoffConcurrency returns the number of concurrent hand-off requests
func (config *Config) GetHandoffConcurrency() int {
	return uintOrDefault(config.HandoffConcurrency, DefaultHandoffConcurrency)
}

// GetHandoffHighWaterMark returns the local queue depth above which a node rejects hand-offs; defaults to half the
// max message queue size
func (config *Config) GetHandoffHighWaterMark() int {
	if config.HandoffHighWaterMark > 0 {
		return int(config.HandoffHighWaterMark)
	}
	mark := int(config.MaxMessageQueueSize / 2)
	if mark < 1 {
		mark = 1
	}
	return mark
}

// IsHandoffLocalFallbackEnabled returns true if a batch is enqueued locally after all hand-off attempts failed
func (config *Config) IsHandoffLocalFallbackEnabled() bool {
	return !config.HandoffLocalFallbackDisabled
}

// boundedUint reads a uint key, falling back to the default with a warning when unparsable or out of [min, max]
func boundedUint(section *ini.Section, key string, defaultValue, min, max uint) uint {
	if !section.HasKey(key) {
		return defaultValue
	}
	value, err := section.Key(key).Uint()
	if err != nil || value < min || value > max {
		log.Warn().Str("section", dispatchPauseSectionName).Str("key", key).Str("value", section.Key(key).String()).
			Uint("default", defaultValue).Msg("invalid configuration value, using default")
		return defaultValue
	}
	return value
}

func setupDispatchPauseConfiguration(cfg *ini.File, configuration *Config) {
	section, err := cfg.GetSection(dispatchPauseSectionName)
	if err != nil {
		return
	}
	configuration.DispatchPauseAdminToken = section.Key("admin-token").MustString("")
	configuration.RegistryRefreshIntervalSeconds = boundedUint(section, "registry-refresh-interval-seconds", DefaultRegistryRefreshIntervalSeconds, 1, 300)
	configuration.DrainIntervalSeconds = boundedUint(section, "drain-interval-seconds", DefaultDrainIntervalSeconds, 1, 60)
	configuration.DrainPageSize = boundedUint(section, "drain-page-size", DefaultDrainPageSize, 1, 500)
	configuration.HandoffMode = section.Key("handoff-mode").MustString(DefaultHandoffMode)
	if configuration.HandoffMode != HandoffModeHTTP && configuration.HandoffMode != HandoffModeLocal {
		log.Warn().Str("section", dispatchPauseSectionName).Str("handoff-mode", configuration.HandoffMode).Msg("invalid handoff-mode, using http")
		configuration.HandoffMode = DefaultHandoffMode
	}
	configuration.HandoffLeaseSeconds = boundedUint(section, "handoff-lease-seconds", DefaultHandoffLeaseSeconds, 1, 3600)
	configuration.HandoffTimeoutSeconds = boundedUint(section, "handoff-timeout-seconds", DefaultHandoffTimeoutSeconds, 1, 60)
	configuration.HandoffMaxAttempts = boundedUint(section, "handoff-max-attempts", DefaultHandoffMaxAttempts, 1, 10)
	configuration.HandoffConcurrency = boundedUint(section, "handoff-concurrency", DefaultHandoffConcurrency, 1, 64)
	configuration.HandoffHighWaterMark = boundedUint(section, "handoff-high-water-mark", 0, 0, 1<<31-1)
	configuration.HandoffLocalFallbackDisabled = !section.Key("handoff-local-fallback").MustBool(DefaultHandoffLocalFallback)
}

// Generated with assistance from Claude AI
