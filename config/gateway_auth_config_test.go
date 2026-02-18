package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestGatewayAuthConfig_DefaultMode(t *testing.T) {
	cfg := loadTestConfiguration("")
	config, err := GetConfigurationFromParseConfig(cfg)
	assert.Nil(t, err)
	assert.Equal(t, "none", config.GetGatewayAuthMode())
	assert.False(t, config.IsGatewayAuthEnabled())
}

func TestGatewayAuthConfig_HMACModeWithValidSecret(t *testing.T) {
	testConfig := `[gateway-auth]
mode=hmac
hmac-shared-secret=dGVzdC1zZWNyZXQ=
`
	cfg := loadTestConfiguration(testConfig)
	config, err := GetConfigurationFromParseConfig(cfg)
	assert.Nil(t, err)
	assert.True(t, config.IsGatewayAuthEnabled())
	assert.Equal(t, "hmac", config.GetGatewayAuthMode())
	assert.Equal(t, []byte("test-secret"), config.GetHMACSharedSecret())
}

func TestGatewayAuthConfig_HMACModeWithMissingSecret(t *testing.T) {
	testConfig := `[gateway-auth]
mode=hmac
hmac-shared-secret=
[http]
listener=:48094
`
	cfg := loadTestConfiguration(testConfig)
	config, err := GetConfigurationFromParseConfig(cfg)
	assert.Equal(t, EmptyConfigurationForError, config)
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "hmac-shared-secret is required")
}

func TestGatewayAuthConfig_HMACModeWithInvalidBase64(t *testing.T) {
	testConfig := `[gateway-auth]
mode=hmac
hmac-shared-secret=!!!notbase64
[http]
listener=:48095
`
	cfg := loadTestConfiguration(testConfig)
	config, err := GetConfigurationFromParseConfig(cfg)
	assert.Equal(t, EmptyConfigurationForError, config)
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "not valid base64")
}

func TestGatewayAuthConfig_InvalidMode(t *testing.T) {
	testConfig := `[gateway-auth]
mode=foobar
[http]
listener=:48096
`
	cfg := loadTestConfiguration(testConfig)
	config, err := GetConfigurationFromParseConfig(cfg)
	assert.Equal(t, EmptyConfigurationForError, config)
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "unsupported mode")
}

func TestGatewayAuthConfig_TimestampToleranceDefault(t *testing.T) {
	cfg := loadTestConfiguration("")
	config, err := GetConfigurationFromParseConfig(cfg)
	assert.Nil(t, err)
	assert.Equal(t, 300*time.Second, config.GetHMACTimestampTolerance())
}

func TestGatewayAuthConfig_TimestampToleranceCustom(t *testing.T) {
	testConfig := `[gateway-auth]
hmac-timestamp-tolerance-seconds=120
`
	cfg := loadTestConfiguration(testConfig)
	config, err := GetConfigurationFromParseConfig(cfg)
	assert.Nil(t, err)
	assert.Equal(t, 120*time.Second, config.GetHMACTimestampTolerance())
}

func TestGatewayAuthConfig_ExemptPathsDefault(t *testing.T) {
	cfg := loadTestConfiguration("")
	config, err := GetConfigurationFromParseConfig(cfg)
	assert.Nil(t, err)
	assert.Equal(t, []string{"/_status", "/metrics", "/debug/pprof/"}, config.GetAuthExemptPaths())
}

func TestGatewayAuthConfig_ExemptPathsCustom(t *testing.T) {
	testConfig := `[gateway-auth]
auth-exempt-paths=/_status,/health
`
	cfg := loadTestConfiguration(testConfig)
	config, err := GetConfigurationFromParseConfig(cfg)
	assert.Nil(t, err)
	assert.Equal(t, []string{"/_status", "/health"}, config.GetAuthExemptPaths())
}

func TestGatewayAuthConfig_SharedSecretNilOnInvalidBase64(t *testing.T) {
	config := &Config{HMACSharedSecret: "!!!invalid"}
	assert.Nil(t, config.GetHMACSharedSecret())
}

func TestGatewayAuthConfig_ImplementsInterface(t *testing.T) {
	var _ GatewayAuthConfig = (*Config)(nil)
}

// Generated with assistance from Claude AI
