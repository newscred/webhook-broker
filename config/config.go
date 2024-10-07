package config

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os/user"
	"strconv"
	"strings"
	"time"

	"github.com/go-ini/ini"
	"github.com/google/wire"

	// MySQL DB Driver
	_ "github.com/go-sql-driver/mysql"
	// SQLite3 DB Driver
	_ "github.com/mattn/go-sqlite3"
)

// AppVersion is the version string type
type AppVersion string

// DBDialect allows us to define constants for supported DB drivers
type DBDialect string

// LogLevel represents the log level logger should use
type LogLevel uint8

// GetVersion provides the current version of the project
func GetVersion() AppVersion {
	return "0.2-alpha-early-access.4"
}

const (
	// SQLite3Dialect represents the DB Dialect for SQLite3
	SQLite3Dialect = DBDialect("sqlite3")
	// MySQLDialect represents the DB Dialect for MySQL
	MySQLDialect = DBDialect("mysql")
	// ConfigFilename is the default config file name
	ConfigFilename = "webhook-broker.cfg"
	// DefaultSystemConfigFilePath is the default system location of the configuration
	DefaultSystemConfigFilePath = "/etc/webhook-broker/" + ConfigFilename
	// DefaultCurrentDirConfigFilePath is the config file path based on current working dir
	DefaultCurrentDirConfigFilePath = ConfigFilename
	// Debug is the lowest LogLevel, will expose all logs
	Debug LogLevel = 20 + iota
	// Info is the second lowest LogLevel
	Info
	// Error is the second highest LogLevel
	Error
	// Fatal is the highest LogLevel with lowest logs
	Fatal
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
	errDBDialect      = errors.New("DB Dialect not supported")
	// ConfigInjector sets up configuration related bindings
	ConfigInjector = wire.NewSet(GetConfigurationFromCLIConfig, wire.Bind(new(SeedDataConfig), new(*Config)), wire.Bind(new(HTTPConfig), new(*Config)), wire.Bind(new(RelationalDatabaseConfig), new(*Config)), wire.Bind(new(LogConfig), new(*Config)), wire.Bind(new(BrokerConfig), new(*Config)), wire.Bind(new(ConsumerConnectionConfig), new(*Config)))
)

var currentUser = user.Current

func getUserHomeDirBasedDefaultConfigFileLocation() string {
	user, err := currentUser()
	if err != nil {
		return DefaultCurrentDirConfigFilePath
	}
	return user.HomeDir + "/.webhook-broker/" + ConfigFilename
}

//Config represents the application configuration
type Config struct {
	DBDialect                 DBDialect
	DBConnectionURL           string
	DBConnectionMaxIdleTime   time.Duration
	DBConnectionMaxLifetime   time.Duration
	DBMaxIdleConnections      uint16
	DBMaxOpenConnections      uint16
	HTTPListeningAddr         string
	HTTPReadTimeout           time.Duration
	HTTPWriteTimeout          time.Duration
	LogFilename               string
	MaxFileSize               uint
	MaxBackups                uint
	MaxAge                    uint
	CompressBackupsEnabled    bool
	SeedData                  SeedData
	TokenRequestHeaderName    string
	UserAgent                 string
	ConnectionTimeout         time.Duration
	MaxMessageQueueSize       uint
	MaxWorkers                uint
	PriorityDispatcherEnabled bool
	RecoveryWorkersEnabled    bool
	RetriggerBaseEndpoint     string
	MaxRetry                  uint8
	RationalDelay             time.Duration
	RetryBackoffDelays        []time.Duration
	LogLevel                  LogLevel
}

// GetLogLevel returns the log level as per the configuration
func (config *Config) GetLogLevel() LogLevel {
	return config.LogLevel
}

// GetDBDialect returns the DB dialect of the configuration
func (config *Config) GetDBDialect() DBDialect {
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
func (config *Config) GetHTTPReadTimeout() time.Duration {
	return config.HTTPReadTimeout
}

// GetHTTPWriteTimeout retrieves the connection write timeout
func (config *Config) GetHTTPWriteTimeout() time.Duration {
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

// GetTokenRequestHeaderName returns the Token request header to pass to the consumers
func (config *Config) GetTokenRequestHeaderName() string {
	return config.TokenRequestHeaderName
}

// GetUserAgent returns the user agent string for consumer HTTP connection
func (config *Config) GetUserAgent() string {
	return config.UserAgent
}

// GetConnectionTimeout returns the connection timeout from broker to consumer
func (config *Config) GetConnectionTimeout() time.Duration {
	return config.ConnectionTimeout
}

// GetMaxMessageQueueSize returns the maximum number of messages to be queued without being dispatched
func (config *Config) GetMaxMessageQueueSize() uint {
	return config.MaxMessageQueueSize
}

// GetMaxWorkers returns the max number of workers dispatching a message to a consumer
func (config *Config) GetMaxWorkers() uint {
	return config.MaxWorkers
}

// IsPriorityDispatcherEnabled returns whether priority will be respected during dispatching from queue
func (config *Config) IsPriorityDispatcherEnabled() bool {
	return config.PriorityDispatcherEnabled
}

// GetRetriggerBaseEndpoint returns the URL to the load balanced endpoint for the broker for retriggering jobs
func (config *Config) GetRetriggerBaseEndpoint() string {
	return config.RetriggerBaseEndpoint
}

// GetMaxRetry returns the maximum number of attempts for delivering a message to a consumer
func (config *Config) GetMaxRetry() uint8 {
	return config.MaxRetry
}

// GetRationalDelay returns how long to wait before retriggering, i.e., what is the addition to picking up messages in fail-safe process.
func (config *Config) GetRationalDelay() time.Duration {
	return config.RationalDelay
}

// GetRetryBackoffDelays returns the delay steps in retrying delivery; retry will be the index and if index is greater than size use the last value times retry-attempt
func (config *Config) GetRetryBackoffDelays() []time.Duration {
	return config.RetryBackoffDelays
}

// IsRecoveryWorkersEnabled retrieves whether the recovery worker should be enabled or not
func (config *Config) IsRecoveryWorkersEnabled() bool {
	return config.RecoveryWorkersEnabled
}

// func (config *Config) () {}

// GetAutoConfiguration gets configuration from default config and system defined path chain of
// /etc/webhook-broker/webhook-broker.cfg, {USER_HOME}/.webhook-broker/webhook-broker.cfg, webhook-broker.cfg (current dir)
func GetAutoConfiguration() (*Config, error) {
	return GetConfiguration("")
}

// GetConfigurationFromCLIConfig from CLIConfig.
func GetConfigurationFromCLIConfig(cliConfig *CLIConfig) (*Config, error) {
	if len(cliConfig.ConfigPath) > 0 {
		return GetConfiguration(cliConfig.ConfigPath)
	}
	return GetAutoConfiguration()
}

// GetConfiguration gets the current state of application configuration
func GetConfiguration(configFilePath string) (*Config, error) {
	cfg, err := loadConfiguration(configFilePath)
	if err != nil {
		return EmptyConfigurationForError, err
	}
	return GetConfigurationFromParseConfig(cfg)
}

// GetConfigurationFromParseConfig returns configuration from parsed configuration
func GetConfigurationFromParseConfig(cfg *ini.File) (*Config, error) {
	configuration := &Config{}
	setupStorageConfiguration(cfg, configuration)
	setupHTTPConfiguration(cfg, configuration)
	setupLogConfiguration(cfg, configuration)
	setupSeedDataConfiguration(cfg, configuration)
	setupConsumerConnectionConfiguration(cfg, configuration)
	setupBrokerConfiguration(cfg, configuration)
	if validationErr := validateConfigurationState(configuration); validationErr != nil {
		return EmptyConfigurationForError, validationErr
	}
	return configuration, nil
}

func validateConfigurationState(configuration *Config) error {
	if len(configuration.TokenRequestHeaderName) <= 0 {
		configuration.TokenRequestHeaderName = "X-Broker-Consumer-Token"
	}
	if len(configuration.UserAgent) <= 0 {
		configuration.UserAgent = "Webhook Message Broker"
	}
	if len(configuration.HTTPListeningAddr) <= 0 {
		configuration.HTTPListeningAddr = ":8080"
	}
	// Check Listener Address port is open
	ln, netErr := net.Listen("tcp", configuration.HTTPListeningAddr)
	if netErr != nil {
		return netErr
	}
	defer ln.Close()
	// Check DB Connection is valid
	var ping func(*sql.DB) error
	switch configuration.DBDialect {
	case SQLite3Dialect:
		ping = pingSqlite3
	case MySQLDialect:
		ping = pingMysql
	default:
		return errDBDialect
	}
	db, dbConnectionErr := sql.Open(string(configuration.DBDialect), configuration.DBConnectionURL)
	if dbConnectionErr != nil {
		return dbConnectionErr
	}
	defer db.Close()
	db.SetConnMaxLifetime(configuration.DBConnectionMaxLifetime)
	db.SetMaxIdleConns(int(configuration.DBMaxIdleConnections))
	db.SetMaxOpenConns(int(configuration.DBMaxOpenConnections))
	db.SetConnMaxIdleTime(configuration.DBConnectionMaxIdleTime)
	var typicalErr error
	dbErr := ping(db)
	if dbErr != nil {
		typicalErr = dbErr
	}
	if typicalErr == nil {
		// Check retrigger endpoint is a valid Absolute URL
		retriggerEndpoint, endpointErr := url.Parse(configuration.RetriggerBaseEndpoint)
		if endpointErr != nil {
			typicalErr = endpointErr
		} else if !retriggerEndpoint.IsAbs() {
			typicalErr = errors.New("Retrigger Base Endpoint is not in absolute URL form")
		}
	}
	return typicalErr
}

var (
	pingSqlite3 = func(db *sql.DB) error {
		rows, queryErr := db.Query("SELECT name FROM sqlite_master WHERE type='table'")
		if queryErr != nil {
			return queryErr
		}
		defer rows.Close()
		return nil
	}

	pingMysql = func(db *sql.DB) error {
		rows, queryErr := db.Query("SHOW Tables")
		if queryErr != nil {
			return queryErr
		}
		defer rows.Close()
		return nil
	}
)

func setupStorageConfiguration(cfg *ini.File, configuration *Config) {
	dbSection, _ := cfg.GetSection("rdbms")
	dbDialect, _ := dbSection.GetKey("dialect")
	dbConnection, _ := dbSection.GetKey("connection-url")
	dbMaxIdleTimeInSec, _ := dbSection.GetKey("connxn-max-idle-time-seconds")
	dbMaxLifetimeInSec, _ := dbSection.GetKey("connxn-max-lifetime-seconds")
	dbMaxIdleConnections, _ := dbSection.GetKey("max-idle-connxns")
	dbMaxOpenConnections, _ := dbSection.GetKey("max-open-connxns")
	configuration.DBDialect = DBDialect(dbDialect.String())
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
	configuration.HTTPReadTimeout = time.Duration(httpReadTimeout.MustUint(180)) * time.Second
	configuration.HTTPWriteTimeout = time.Duration(httpWriteTimeout.MustUint(180)) * time.Second
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
	logLevelKey, _ := logSection.GetKey("log-level")
	var logLevel LogLevel
	switch logLevelKey.MustString("debug") {
	case "fatal":
		logLevel = Fatal
	case "error":
		logLevel = Error
	case "info":
		logLevel = Info
	default:
		logLevel = Debug
	}
	configuration.LogLevel = logLevel
}

func setupSeedDataConfiguration(cfg *ini.File, configuration *Config) {
	seedData := SeedData{}

	initialChannels, _ := cfg.GetSection("initial-channels")
	initialChannelTokens, _ := cfg.GetSection("initial-channel-tokens")
	seedChannelsAsProducers := parseProducers(initialChannels, initialChannelTokens)
	seedChannels := make([]SeedChannel, len(seedChannelsAsProducers))
	for index, producer := range seedChannelsAsProducers {
		seedChannels[index] = SeedChannel(producer)
	}
	seedData.Channels = seedChannels

	initialProducers, _ := cfg.GetSection("initial-producers")
	initialProducerTokens, _ := cfg.GetSection("initial-producer-tokens")
	seedProducers := parseProducers(initialProducers, initialProducerTokens)
	seedData.Producers = seedProducers

	seedData.Consumers = parseConsumers(cfg, seedChannels)

	var buf bytes.Buffer
	json.NewEncoder(&buf).Encode(seedData)
	hashCalculator := sha256.New()
	seedData.DataHash = base64.StdEncoding.EncodeToString(hashCalculator.Sum(buf.Bytes()))

	configuration.SeedData = seedData
}

func parseConsumers(cfg *ini.File, seedChannels []SeedChannel) []SeedConsumer {
	initialConsumers, _ := cfg.GetSection("initial-consumers")
	seedConsumers := make([]SeedConsumer, 0, len(initialConsumers.Keys()))
	for _, consumer := range initialConsumers.Keys() {
		consumerSection, consumerSectionErr := cfg.GetSection(consumer.Name())
		if consumerSectionErr != nil {
			continue
		}
		channel, channelErr := getChannelForConsumer(consumerSection, seedChannels)
		if channelErr != nil {
			continue
		}
		consumerType, err := getConsumerType(consumerSection)
		if err != nil {
			continue
		}
		seedConsumer := SeedConsumer{
			SeedProducer: SeedProducer{ID: consumer.Name(), Name: consumer.Name()},
			Channel:      channel,
			Type:         consumerType,
		}
		token, tokenErr := consumerSection.GetKey("token")
		if tokenErr == nil {
			seedConsumer.Token = token.MustString("")
		}
		callbackURL := consumer.MustString("")
		consumerCallbackURL, urlErr := url.Parse(callbackURL)
		if urlErr == nil && consumerCallbackURL.IsAbs() {
			seedConsumer.CallbackURL = consumerCallbackURL
			seedConsumers = append(seedConsumers, seedConsumer)
		}
	}
	return seedConsumers
}

func getChannelForConsumer(consumerSection *ini.Section, seedChannels []SeedChannel) (string, error) {
	channelKey, channelsErr := consumerSection.GetKey("channel")
	if channelsErr != nil {
		return "", channelsErr
	}
	channel := channelKey.MustString("")
	for _, seedChannel := range seedChannels {
		if channel == seedChannel.ID {
			return channel, nil
		}
	}
	return channel, sql.ErrNoRows
}

func getConsumerType(consumerSection *ini.Section) (string, error) {
	consumerType := consumerSection.Key("type").MustString("")
	switch consumerType {
	case PushConsumerStr, PullConsumerStr:
		return consumerType, nil
	case "":
		return PushConsumerStr, nil
	default:
		return "", fmt.Errorf("invalid consumer type %v", consumerType)
	}
}

func parseProducers(initialProducers *ini.Section, initialProducerTokens *ini.Section) []SeedProducer {
	seedProducers := make([]SeedProducer, len(initialProducers.Keys()))
	for prodIndex, channel := range initialProducers.Keys() {
		token, tokenErr := initialProducerTokens.GetKey(channel.Name())
		seedProducer := SeedProducer{ID: channel.Name(), Name: channel.MustString("")}
		if tokenErr == nil {
			seedProducer.Token = token.MustString("")
		}
		seedProducers[prodIndex] = seedProducer
	}
	return seedProducers
}

func setupConsumerConnectionConfiguration(cfg *ini.File, configuration *Config) {
	consumerConnection, _ := cfg.GetSection("consumer-connection")
	tokenHeaderName, _ := consumerConnection.GetKey("token-header-name")
	userAgent, _ := consumerConnection.GetKey("user-agent")
	connectionTimeoutInSecs, _ := consumerConnection.GetKey("connection-timeout-in-seconds")
	configuration.TokenRequestHeaderName = tokenHeaderName.MustString("")
	configuration.UserAgent = userAgent.MustString("")
	configuration.ConnectionTimeout = time.Duration(connectionTimeoutInSecs.MustUint(60)) * time.Second
}

func setupBrokerConfiguration(cfg *ini.File, configuration *Config) {
	broker, _ := cfg.GetSection("broker")
	maxMsgQueueSize, _ := broker.GetKey("max-message-queue-size")
	maxWorkers, _ := broker.GetKey("max-workers")
	priorityDispatcher, _ := broker.GetKey("priority-dispatcher-enabled")
	recoveryWorkersEnabled, _ := broker.GetKey("recovery-workers-enabled")
	retriggerBaseEndpoint, _ := broker.GetKey("retrigger-base-endpoint")
	maxRetry, _ := broker.GetKey("max-retry")
	rationalDelayInSecs, _ := broker.GetKey("rational-delay-in-seconds")
	retryBackoffDelayInSecs, _ := broker.GetKey("retry-backoff-delays-in-seconds")
	configuration.MaxMessageQueueSize = maxMsgQueueSize.MustUint(100000)
	configuration.MaxWorkers = maxWorkers.MustUint(100)
	configuration.PriorityDispatcherEnabled = priorityDispatcher.MustBool(false)
	configuration.RecoveryWorkersEnabled = recoveryWorkersEnabled.MustBool(true)
	configuration.RetriggerBaseEndpoint = retriggerBaseEndpoint.MustString("")
	configuration.MaxRetry = uint8(maxRetry.MustUint(10))
	configuration.RationalDelay = time.Duration(rationalDelayInSecs.MustUint(30)) * time.Second
	backoffDelayStrings := strings.Split(retryBackoffDelayInSecs.MustString("15"), ",")
	var backoffDelays []time.Duration = make([]time.Duration, 0, len(backoffDelayStrings))
	for _, backoffDelayString := range backoffDelayStrings {
		parsedBackoff, backoffParseErr := strconv.ParseUint(backoffDelayString, 10, 32)
		if backoffParseErr != nil {
			parsedBackoff = 15
		}
		backoffDelays = append(backoffDelays, time.Duration(parsedBackoff)*time.Second)
	}
	configuration.RetryBackoffDelays = backoffDelays
}
