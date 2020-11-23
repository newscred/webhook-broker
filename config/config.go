package config

import (
	"os/user"
	"time"

	"github.com/go-ini/ini"
)

// AppVersion is the version string type
type AppVersion string

// GetVersion provides the current version of the project
func GetVersion() AppVersion {
	return "0.1-dev"
}

const (
	// ConfigFilename is the default config file name
	ConfigFilename = "webhook-broker.cfg"
	// DefaultSystemConfigFilePath is the default system location of the configuration
	DefaultSystemConfigFilePath = "/etc/webhook-broker/" + ConfigFilename
	// DefaultCurrentDirConfigFilePath is the config file path based on current working dir
	DefaultCurrentDirConfigFilePath = ConfigFilename
	// DefaultConfiguration is the configuration that will be in effect if no configuration is loaded from any of the expected locations
	DefaultConfiguration = `[database]
	dialect=mysql
	connection-url=webhook_broker:zxc909zxc@tcp(mysql:3306)/webhook-broker?charset=utf8&parseTime=True
	connxn-max-idle-time-seconds=0
	connxn-max-lifetime-seconds=0
	max-idle-connxns=30
	max-open-connxns=100
	[http]
	listener=:8080
	read-timeout=240
	write-timeout=240
	`
)

var (
	// EmptyConfigurationForError Represents the configuration instance to be
	// used when there is a configuration error during load
	EmptyConfigurationForError = &Config{}

	defaultLoadFunc = func(configFilePath string) (*ini.File, error) {
		if len(configFilePath) > 0 {
			return ini.LooseLoad([]byte(DefaultConfiguration), DefaultSystemConfigFilePath, getUserHomeDirBasedDefaultConfigFileLocation(), DefaultCurrentDirConfigFilePath, configFilePath)
		}
		return ini.LooseLoad([]byte(DefaultConfiguration), DefaultSystemConfigFilePath, getUserHomeDirBasedDefaultConfigFileLocation(), DefaultCurrentDirConfigFilePath)
	}
	loadConfiguration = defaultLoadFunc
)

var currentUser = user.Current

func getUserHomeDirBasedDefaultConfigFileLocation() string {
	user, err := currentUser()
	if err != nil {
		return DefaultCurrentDirConfigFilePath
	}
	return user.HomeDir + "/.webhook-broker/" + ConfigFilename
}

// DBConfig represents DB configuration related behaviors
type DBConfig interface {
	GetDBDialect() string
	GetDBConnectionURL() string
	GetDBConnectionMaxIdleTime() time.Duration
	GetDBConnectionMaxLifetime() time.Duration
	GetMaxIdleDBConnections() uint16
	GetMaxOpenDBConnections() uint16
}

// HTTPConfig represents the HTTP configuration related behaviors
type HTTPConfig interface {
	GetHTTPListeningAddr() string
	GetHTTPReadTimeout() uint
	GetHTTPWriteTimeout() uint
}

//Config represents the application configuration
type Config struct {
	dbDialect               string
	dbConnectionURL         string
	dbConnectionMaxIdleTime time.Duration
	dbConnectionMaxLifetime time.Duration
	dbMaxIdleConnections    uint16
	dbMaxOpenConnections    uint16
	httpListeningAddr       string
	httpReadTimeout         uint
	httpWriteTimeout        uint
}

// GetDBDialect returns the DB dialect of the configuration
func (config *Config) GetDBDialect() string {
	return config.dbDialect
}

// GetDBConnectionURL returns the DB Connection URL string
func (config *Config) GetDBConnectionURL() string {
	return config.dbConnectionURL
}

// GetDBConnectionMaxIdleTime returns the DB Connection max idle time
func (config *Config) GetDBConnectionMaxIdleTime() time.Duration {
	return config.dbConnectionMaxIdleTime
}

// GetDBConnectionMaxLifetime returns the DB Connection max lifetime
func (config *Config) GetDBConnectionMaxLifetime() time.Duration {
	return config.dbConnectionMaxLifetime
}

// GetMaxIdleDBConnections returns the maximum number of idle DB connections to retain in pool
func (config *Config) GetMaxIdleDBConnections() uint16 {
	return config.dbMaxIdleConnections
}

// GetMaxOpenDBConnections returns the maximum number of concurrent DB connections to keep open
func (config *Config) GetMaxOpenDBConnections() uint16 {
	return config.dbMaxOpenConnections
}

// GetHTTPListeningAddr retrieves the connection string to listen to
func (config *Config) GetHTTPListeningAddr() string {
	return config.httpListeningAddr
}

// GetHTTPReadTimeout retrieves the connection read timeout
func (config *Config) GetHTTPReadTimeout() uint {
	return config.httpReadTimeout
}

// GetHTTPWriteTimeout retrieves the connection write timeout
func (config *Config) GetHTTPWriteTimeout() uint {
	return config.httpWriteTimeout
}

// func (config *Config) () {}

// GetAutoConfiguration gets configuration from default config and system defined path chain of
// /etc/webhook-broker/webhook-broker.cfg, {USER_HOME}/.webhook-broker/webhook-broker.cfg, webhook-broker.cfg (current dir)
func GetAutoConfiguration() (*Config, error) {
	return GetConfiguration("")
}

// GetConfiguration gets the current state of application configuration
func GetConfiguration(configFilePath string) (*Config, error) {
	configuration := &Config{}
	cfg, err := loadConfiguration(configFilePath)
	if err != nil {
		return EmptyConfigurationForError, err
	}
	setupStorageConfiguration(cfg, configuration)
	setupHTTPConfiguration(cfg, configuration)
	return configuration, nil
}

func setupStorageConfiguration(cfg *ini.File, configuration *Config) {
	dbSection, _ := cfg.GetSection("database")
	dbDialect, _ := dbSection.GetKey("dialect")
	dbConnection, _ := dbSection.GetKey("connection-url")
	dbMaxIdleTimeInSec, _ := dbSection.GetKey("connxn-max-idle-time-seconds")
	dbMaxLifetimeInSec, _ := dbSection.GetKey("connxn-max-lifetime-seconds")
	dbMaxIdleConnections, _ := dbSection.GetKey("max-idle-connxns")
	dbMaxOpenConnections, _ := dbSection.GetKey("max-open-connxns")
	configuration.dbDialect = dbDialect.String()
	configuration.dbConnectionURL = dbConnection.String()
	configuration.dbConnectionMaxIdleTime = time.Duration(dbMaxIdleTimeInSec.MustUint(0)) * time.Second
	configuration.dbConnectionMaxLifetime = time.Duration(dbMaxLifetimeInSec.MustUint(0)) * time.Second
	configuration.dbMaxIdleConnections = uint16(dbMaxIdleConnections.MustUint(10))
	configuration.dbMaxOpenConnections = uint16(dbMaxOpenConnections.MustUint(50))
}

func setupHTTPConfiguration(cfg *ini.File, configuration *Config) {
	httpSection, _ := cfg.GetSection("http")
	httpListener, _ := httpSection.GetKey("listener")
	httpReadTimeout, _ := httpSection.GetKey("read-timeout")
	httpWriteTimeout, _ := httpSection.GetKey("write-timeout")
	configuration.httpListeningAddr = httpListener.String()
	configuration.httpReadTimeout = httpReadTimeout.MustUint(180)
	configuration.httpWriteTimeout = httpWriteTimeout.MustUint(180)
}
