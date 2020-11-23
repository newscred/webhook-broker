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
	[log]
	filename=/var/log/webhook-broker.log
	max-file-size-in-mb=200
	max-backups=3
	max-age-in-days=28
	compress-backups=true
	[broker]
	max-message-queue-size=10000
	max-workers=200
	priority-dispatcher-enabled=true
	retrigger-base-endpoint=http://localhost:8080
	max-retry=5
	rational-delay-in-seconds=20
	retry-backoff-delays-in-seconds=5,30,60
	[consumer-connection]
	token-header-name=X-Broker-Consumer-Token
	user-agent=Webhook Message Broker
	connection-timeout-in-seconds=30
	[initial-channels]
	sample-channel=Sample Channel
	[initial-producers]
	sample-producer=Sample Producer
	[initial-consumers]
	sample-consumer=http://sample-endpoint/webhook-receiver
	[initial-channel-tokens]
	sample-channel=sample-channel-token
	[initial-producer-tokens]
	sample-producer=sample-producer-token
	[initial-consumer-tokens]
	sample-consumer=sample-consumer-token
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

// LogConfig represents the interface for log related configuration
type LogConfig interface {
	IsLoggerConfigAvailable() bool
	GetLogFilename() string
	GetMaxLogFileSize() uint
	GetMaxLogBackups() uint
	GetMaxAgeForALogFile() uint
	IsCompressionEnabledOnLogBackups() bool
}

// SeedProducer represents the pre configured producer via configuration
type SeedProducer struct {
	// ID is the ID of the data
	ID string
	// Name is name of the data
	Name string
	// Token is the pre configured token of the data
	Token string
}

// SeedChannel represents pre configured channel via configuration
type SeedChannel SeedProducer

// SeedConsumer represents pre configured consumer via configuration
type SeedConsumer struct {
	SeedProducer
	// CallbackURL represents the URl to call back
	CallbackURL string
}

// SeedData represents data specified in configuration to ensure is present when app starts up
type SeedData struct {
	DataHash  string
	Producers []SeedProducer
	Channels  []SeedChannel
	Consumers []SeedConsumer
}

// SeedDataConfig provides the interface for working with SeedData in configuration
type SeedDataConfig interface {
	GetSeedData() SeedData
}

//Config represents the application configuration
type Config struct {
	DBDialect               string
	DBConnectionURL         string
	DBConnectionMaxIdleTime time.Duration
	DBConnectionMaxLifetime time.Duration
	DBMaxIdleConnections    uint16
	DBMaxOpenConnections    uint16
	HTTPListeningAddr       string
	HTTPReadTimeout         uint
	HTTPWriteTimeout        uint
	LogFilename             string
	MaxFileSize             uint
	MaxBackups              uint
	MaxAge                  uint
	CompressBackupsEnabled  bool
	SeedData                SeedData
}

// GetDBDialect returns the DB dialect of the configuration
func (config *Config) GetDBDialect() string {
	return config.DBDialect
}

// GetDBConnectionURL returns the DB Connection URL string
func (config *Config) GetDBConnectionURL() string {
	return config.DBConnectionURL
}

// GetDBConnectionMaxIdleTime returns the DB Connection max idle time
func (config *Config) GetDBConnectionMaxIdleTime() time.Duration {
	return config.DBConnectionMaxIdleTime
}

// GetDBConnectionMaxLifetime returns the DB Connection max lifetime
func (config *Config) GetDBConnectionMaxLifetime() time.Duration {
	return config.DBConnectionMaxLifetime
}

// GetMaxIdleDBConnections returns the maximum number of idle DB connections to retain in pool
func (config *Config) GetMaxIdleDBConnections() uint16 {
	return config.DBMaxIdleConnections
}

// GetMaxOpenDBConnections returns the maximum number of concurrent DB connections to keep open
func (config *Config) GetMaxOpenDBConnections() uint16 {
	return config.DBMaxOpenConnections
}

// GetHTTPListeningAddr retrieves the connection string to listen to
func (config *Config) GetHTTPListeningAddr() string {
	return config.HTTPListeningAddr
}

// GetHTTPReadTimeout retrieves the connection read timeout
func (config *Config) GetHTTPReadTimeout() uint {
	return config.HTTPReadTimeout
}

// GetHTTPWriteTimeout retrieves the connection write timeout
func (config *Config) GetHTTPWriteTimeout() uint {
	return config.HTTPWriteTimeout
}

// IsLoggerConfigAvailable checks is logger configuration is set since its optional
func (config *Config) IsLoggerConfigAvailable() bool {
	return len(config.LogFilename) > 0
}

// GetLogFilename retrieves the file name of the log
func (config *Config) GetLogFilename() string {
	return config.LogFilename
}

// GetMaxLogFileSize retrieves the max log file size before its rotated in MB
func (config *Config) GetMaxLogFileSize() uint {
	return config.MaxFileSize
}

// GetMaxLogBackups retrieves max rotated logs to retain
func (config *Config) GetMaxLogBackups() uint {
	return config.MaxBackups
}

// GetMaxAgeForALogFile retrieves maximum day to retain a rotated log file
func (config *Config) GetMaxAgeForALogFile() uint {
	return config.MaxAge
}

// IsCompressionEnabledOnLogBackups checks if log backups are compressed
func (config *Config) IsCompressionEnabledOnLogBackups() bool {
	return config.CompressBackupsEnabled
}

// GetSeedData returns the seed data configuration
func (config *Config) GetSeedData() SeedData {
	return config.SeedData
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
	setupLogConfiguration(cfg, configuration)
	setupSeedDataConfiguration(cfg, configuration)
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
	configuration.DBDialect = dbDialect.String()
	configuration.DBConnectionURL = dbConnection.String()
	configuration.DBConnectionMaxIdleTime = time.Duration(dbMaxIdleTimeInSec.MustUint(0)) * time.Second
	configuration.DBConnectionMaxLifetime = time.Duration(dbMaxLifetimeInSec.MustUint(0)) * time.Second
	configuration.DBMaxIdleConnections = uint16(dbMaxIdleConnections.MustUint(10))
	configuration.DBMaxOpenConnections = uint16(dbMaxOpenConnections.MustUint(50))
}

func setupHTTPConfiguration(cfg *ini.File, configuration *Config) {
	httpSection, _ := cfg.GetSection("http")
	httpListener, _ := httpSection.GetKey("listener")
	httpReadTimeout, _ := httpSection.GetKey("read-timeout")
	httpWriteTimeout, _ := httpSection.GetKey("write-timeout")
	configuration.HTTPListeningAddr = httpListener.String()
	configuration.HTTPReadTimeout = httpReadTimeout.MustUint(180)
	configuration.HTTPWriteTimeout = httpWriteTimeout.MustUint(180)
}

func setupLogConfiguration(cfg *ini.File, configuration *Config) {
	logSection, _ := cfg.GetSection("log")
	logFilenameKey, _ := logSection.GetKey("filename")
	maxFileSizeKey, _ := logSection.GetKey("max-file-size-in-mb")
	maxBackupsKey, _ := logSection.GetKey("max-backups")
	maxAgeKey, _ := logSection.GetKey("max-age-in-days")
	compressEnabledKey, _ := logSection.GetKey("compress-backups")
	configuration.LogFilename = logFilenameKey.String()
	configuration.MaxFileSize = maxFileSizeKey.MustUint(50)
	configuration.MaxBackups = maxBackupsKey.MustUint(1)
	configuration.MaxAge = maxAgeKey.MustUint(30)
	configuration.CompressBackupsEnabled = compressEnabledKey.MustBool(false)
}

func setupSeedDataConfiguration(cfg *ini.File, configuration *Config) {

}
