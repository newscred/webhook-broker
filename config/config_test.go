package config

import (
	"errors"
	"os/user"
	"testing"
	"time"

	"github.com/go-ini/ini"
	"github.com/stretchr/testify/assert"
)

const (
	wrongValueConfig = `[database]
	dialect=mysql
	connection-url=webhook_broker:zxc909zxc@tcp(mysql:3306)/webhook-broker?charset=utf8&parseTime=True
	connxn-max-idle-time-seconds=-10
	connxn-max-lifetime-seconds=ascx0x
	max-idle-connxns=as30
	max-open-connxns=-100
	[http]
	listener=:6080
	read-timeout=asd240
	write-timeout=zf240
	[log]
	filename=/var/log/webhook-broker.log
	max-file-size-in-mb=as200
	max-backups=asd3
	max-age-in-days=dasd28
	compress-backups=asdtrue
	# Generic Webhook Broker config such as - Max message queue size, max workers, priority dispatcher on, retrigger base-endpoint
	[broker]
	max-message-queue-size=asd10000
	max-workers=asd200
	priority-dispatcher-enabled=adtrue
	retrigger-base-endpoint=http:/localhost:8080
	max-retry=5ad
	rational-delay-in-seconds=2sd0
	retry-backoff-delays-in-seconds=5,30,asd 6a 0

	# Generic consumer configuration such as - Token Header name, User Agent, Consumer connection timeout
	[consumer-connection]
	token-header-name=X-Broker-Consumer-Token
	user-agent=Webhook Message Broker
	connection-timeout-in-seconds=a d3d0

	# Preemptive Channel, Producer, Consumer setup
	[initial-channels]
	sample-channel=Sample Channel

	[initial-producers]
	sample-producer=Sample Producer

	[initial-consumers]
	sample-consumer=http://sample-endpoint/webhook-receiver

	# Support for preemptive token setup for the aboves
	[initial-channel-tokens]
	sample-channel=sample-channel-token

	[initial-producer-tokens]
	sample-producer=sample-producer-token

	[initial-consumer-tokens]
	sample-consumer=sample-consumer-token
	`
	errorConfig = `[database]
	asda sdads
	connection-url=webhook_broker:zxc909zxc@tcp(mysql:3306)/webhook-broker?charset=utf8&parseTime=True
	`
)

func TestGetAutoConfiguration_Default(t *testing.T) {
	config, cfgErr := GetAutoConfiguration()
	if cfgErr != nil {
		t.Error("Auto Configuration failed", cfgErr)
	}
	assert.Equal(t, "mysql", config.GetDBDialect())
	assert.Equal(t, "webhook_broker:zxc909zxc@tcp(mysql:3306)/webhook-broker?charset=utf8&parseTime=True", config.GetDBConnectionURL())
	assert.Equal(t, time.Duration(0), config.GetDBConnectionMaxIdleTime())
	assert.Equal(t, time.Duration(0), config.GetDBConnectionMaxLifetime())
	assert.Equal(t, uint16(30), config.GetMaxIdleDBConnections())
	assert.Equal(t, uint16(100), config.GetMaxOpenDBConnections())
	assert.Equal(t, ":8080", config.GetHTTPListeningAddr())
	assert.Equal(t, uint(240), config.GetHTTPReadTimeout())
	assert.Equal(t, uint(240), config.GetHTTPWriteTimeout())
	assert.Equal(t, "/var/log/webhook-broker.log", config.GetLogFilename())
	assert.Equal(t, uint(200), config.GetMaxLogFileSize())
	assert.Equal(t, uint(28), config.GetMaxAgeForALogFile())
	assert.Equal(t, uint(3), config.GetMaxLogBackups())
	assert.Equal(t, true, config.IsCompressionEnabledOnLogBackups())
	assert.Equal(t, true, config.IsLoggerConfigAvailable())
}

func TestGetAutoConfiguration_WrongValues(t *testing.T) {
	loadConfiguration = func(location string) (*ini.File, error) {
		return ini.InsensitiveLoad([]byte(wrongValueConfig))
	}
	config, cfgErr := GetAutoConfiguration()
	if cfgErr != nil {
		t.Error("Auto Configuration failed", cfgErr)
	}
	assert.Equal(t, time.Duration(0), config.GetDBConnectionMaxIdleTime())
	assert.Equal(t, time.Duration(0), config.GetDBConnectionMaxLifetime())
	assert.Equal(t, uint16(10), config.GetMaxIdleDBConnections())
	assert.Equal(t, uint16(50), config.GetMaxOpenDBConnections())
	assert.Equal(t, ":6080", config.GetHTTPListeningAddr())
	assert.Equal(t, uint(180), config.GetHTTPReadTimeout())
	assert.Equal(t, uint(180), config.GetHTTPWriteTimeout())
	assert.Equal(t, "/var/log/webhook-broker.log", config.GetLogFilename())
	assert.Equal(t, uint(50), config.GetMaxLogFileSize())
	assert.Equal(t, uint(30), config.GetMaxAgeForALogFile())
	assert.Equal(t, uint(1), config.GetMaxLogBackups())
	assert.Equal(t, false, config.IsCompressionEnabledOnLogBackups())
	assert.Equal(t, true, config.IsLoggerConfigAvailable())
	defer func() {
		loadConfiguration = defaultLoadFunc
	}()
}

func TestGetAutoConfiguration_Error(t *testing.T) {
	loadConfiguration = func(location string) (*ini.File, error) {
		return ini.InsensitiveLoad([]byte(errorConfig))
	}
	config, cfgErr := GetAutoConfiguration()
	if cfgErr == nil {
		t.Error("Auto Configuration should have failed")
	}
	assert.Equal(t, EmptyConfigurationForError, config)
	defer func() {
		loadConfiguration = defaultLoadFunc
	}()
}

func TestGetAutoConfiguration_CurrentUserError(t *testing.T) {
	oldCurrentUser := currentUser
	currentUser = func() (*user.User, error) {
		return nil, errors.New("Unit test error")
	}
	_, cfgErr := GetAutoConfiguration()
	if cfgErr != nil {
		t.Error("Auto Configuration failed", cfgErr)
	}
	defer func() {
		currentUser = oldCurrentUser
	}()
}

func TestGetConfiguration(t *testing.T) {
	config, cfgErr := GetConfiguration("./test-webhook-broker.cfg")
	if cfgErr != nil {
		t.Error("Auto Configuration failed", cfgErr)
	}
	assert.Equal(t, "mysql", config.GetDBDialect())
	assert.Equal(t, "somesqliteurl", config.GetDBConnectionURL())
	assert.Equal(t, time.Duration(10)*time.Second, config.GetDBConnectionMaxIdleTime())
	assert.Equal(t, time.Duration(10)*time.Second, config.GetDBConnectionMaxLifetime())
	assert.Equal(t, uint16(300), config.GetMaxIdleDBConnections())
	assert.Equal(t, uint16(1000), config.GetMaxOpenDBConnections())
	assert.Equal(t, ":7080", config.GetHTTPListeningAddr())
	assert.Equal(t, uint(2401), config.GetHTTPReadTimeout())
	assert.Equal(t, uint(2401), config.GetHTTPWriteTimeout())
	assert.Equal(t, "", config.GetLogFilename())
	assert.Equal(t, uint(20), config.GetMaxLogFileSize())
	assert.Equal(t, uint(280), config.GetMaxAgeForALogFile())
	assert.Equal(t, uint(30), config.GetMaxLogBackups())
	assert.Equal(t, false, config.IsCompressionEnabledOnLogBackups())
	assert.Equal(t, false, config.IsLoggerConfigAvailable())
}

func TestGetVersion(t *testing.T) {
	assert.NotEmpty(t, GetVersion())
}

func TestConfigInterfaces(t *testing.T) {
	var _ DBConfig = (*Config)(nil)
	var _ HTTPConfig = (*Config)(nil)
	var _ LogConfig = (*Config)(nil)
}
