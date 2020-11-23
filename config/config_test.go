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
}

func TestGetVersion(t *testing.T) {
	assert.NotEmpty(t, GetVersion())
}

func TestConfigInterfaces(t *testing.T) {
	var _ DBConfig = (*Config)(nil)
}
