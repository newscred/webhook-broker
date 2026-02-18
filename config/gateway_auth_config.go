package config

import (
	"encoding/base64"
	"strings"
	"time"
)

const (
	// DefaultGatewayAuthMode is the default authentication mode (disabled)
	DefaultGatewayAuthMode = "none"
	// DefaultHMACTimestampToleranceSeconds is the default maximum age of a signed request in seconds
	DefaultHMACTimestampToleranceSeconds uint = 300
	// DefaultAuthExemptPaths is the default comma-separated list of path prefixes that bypass gateway auth
	DefaultAuthExemptPaths = "/_status,/metrics,/debug/pprof/"
)

// GatewayAuthConfig represents configuration for gateway-level authentication
type GatewayAuthConfig interface {
	// GetGatewayAuthMode returns the authentication mode ("none" or "hmac")
	GetGatewayAuthMode() string
	// GetHMACSharedSecret returns the base64-decoded shared secret for HMAC signing
	GetHMACSharedSecret() []byte
	// GetHMACTimestampTolerance returns the maximum age of a signed request
	GetHMACTimestampTolerance() time.Duration
	// GetAuthExemptPaths returns path prefixes that bypass gateway auth
	GetAuthExemptPaths() []string
	// IsGatewayAuthEnabled returns true if gateway auth is active
	IsGatewayAuthEnabled() bool
}

// GetGatewayAuthMode returns the authentication mode
func (config *Config) GetGatewayAuthMode() string {
	if len(config.GatewayAuthMode) == 0 {
		return DefaultGatewayAuthMode
	}
	return config.GatewayAuthMode
}

// GetHMACSharedSecret returns the base64-decoded shared secret for HMAC signing
func (config *Config) GetHMACSharedSecret() []byte {
	decoded, err := base64.StdEncoding.DecodeString(config.HMACSharedSecret)
	if err != nil {
		return nil
	}
	return decoded
}

// GetHMACTimestampTolerance returns the maximum age of a signed request
func (config *Config) GetHMACTimestampTolerance() time.Duration {
	if config.HMACTimestampToleranceSeconds == 0 {
		return time.Duration(DefaultHMACTimestampToleranceSeconds) * time.Second
	}
	return time.Duration(config.HMACTimestampToleranceSeconds) * time.Second
}

// GetAuthExemptPaths returns path prefixes that bypass gateway auth
func (config *Config) GetAuthExemptPaths() []string {
	raw := config.AuthExemptPaths
	if len(raw) == 0 {
		raw = DefaultAuthExemptPaths
	}
	parts := strings.Split(raw, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if len(trimmed) > 0 {
			result = append(result, trimmed)
		}
	}
	return result
}

// IsGatewayAuthEnabled returns true if gateway auth is active
func (config *Config) IsGatewayAuthEnabled() bool {
	return config.GetGatewayAuthMode() == "hmac"
}

// Generated with assistance from Claude AI
