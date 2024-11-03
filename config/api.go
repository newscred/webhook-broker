package config

import (
	"bytes"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"net/url"
	"strings"
	"time"
)

// RelationalDatabaseConfig represents DB configuration related behaviors
type RelationalDatabaseConfig interface {
	GetDBDialect() DBDialect
	GetDBConnectionURL() string
	GetDBConnectionMaxIdleTime() time.Duration
	GetDBConnectionMaxLifetime() time.Duration
	GetMaxIdleDBConnections() uint16
	GetMaxOpenDBConnections() uint16
}

// HTTPConfig represents the HTTP configuration related behaviors
type HTTPConfig interface {
	GetHTTPListeningAddr() string
	GetHTTPReadTimeout() time.Duration
	GetHTTPWriteTimeout() time.Duration
}

// LogConfig represents the interface for log related configuration
type LogConfig interface {
	GetLogLevel() LogLevel
	IsLoggerConfigAvailable() bool
	GetLogFilename() string
	GetMaxLogFileSize() uint
	GetMaxLogBackups() uint
	GetMaxAgeForALogFile() uint
	IsCompressionEnabledOnLogBackups() bool
}

// RemoteMessageDestination represents the destination for exported messages.
type RemoteMessageDestination string

const (
	// RemoteMessageDestinationS3 represents AWS S3 as the export destination.
	RemoteMessageDestinationS3 RemoteMessageDestination = "s3"
	// RemoteMessageDestinationGCS represents Google Cloud Storage as the export destination.
	RemoteMessageDestinationGCS RemoteMessageDestination = "gcs"
)

// MessagePruningConfig provides the interface for configuring message pruning.
type MessagePruningConfig interface {
	// IsPruningEnabled returns true if message pruning is enabled.
	IsPruningEnabled() bool
	// GetExportPath returns the local filesystem path where messages will be exported before being uploaded.
	GetExportPath() string
	// GetExportNodeName returns a prefix to be added to the exported file name.
	GetExportNodeName() string
	// GetMessageRetentionDays returns the number of days to retain messages for which all jobs have completed.
	GetMessageRetentionDays() uint
	// GetRemoteExportDestination returns the remote destination for exported messages (e.g., "s3" or "gcs").
	GetRemoteExportDestination() RemoteMessageDestination
	// GetRemoteExportURL returns the root URL for the remote export destination (e.g., S3 bucket URL or GCS bucket URL).
	GetRemoteExportURL() *url.URL
	// GetRemoteExportPrefix returns the prefix to be added to the exported file name when uploading to the remote destination.
	GetRemoteFilePrefix() string
	// GetMaxArchiveFileSizeInMB returns the maximum size of the exported file in MB before it is rotated to a new file
	GetMaxArchiveFileSizeInMB() uint
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
	CallbackURL *url.URL
	// Channel represents which channel this consumer listens to
	Channel string
	Type    string
}

const (
	PullConsumerStr = "pull"
	PushConsumerStr = "push"
)

// SeedData represents data specified in configuration to ensure is present when app starts up
type SeedData struct {
	DataHash  string
	Producers []SeedProducer
	Channels  []SeedChannel
	Consumers []SeedConsumer
}

// Scan de-serializes SeedData for reading from DB
func (u *SeedData) Scan(value interface{}) (err error) {
	if stringVal, ok := value.(string); ok {
		err = json.NewDecoder(strings.NewReader(stringVal)).Decode(u)
	} else if sqlRawBytes, ok := value.(sql.RawBytes); ok {
		err = json.NewDecoder(strings.NewReader(string(sqlRawBytes))).Decode(u)
	} else if rawBytes, ok := value.([]byte); ok {
		err = json.NewDecoder(strings.NewReader(string(rawBytes))).Decode(u)
	}
	return err
}

// Value serializes SeedData to write to DB
func (u SeedData) Value() (driver.Value, error) {
	var buf bytes.Buffer
	err := json.NewEncoder(&buf).Encode(u)
	return buf.Bytes(), err
}

// SeedDataConfig provides the interface for working with SeedData in configuration
type SeedDataConfig interface {
	GetSeedData() SeedData
}

// ConsumerConnectionConfig provides the interface for working with consumer connection related configuration
type ConsumerConnectionConfig interface {
	GetTokenRequestHeaderName() string
	GetUserAgent() string
	GetConnectionTimeout() time.Duration
}

// BrokerConfig provides the interface for configuring the broker
type BrokerConfig interface {
	GetMaxMessageQueueSize() uint
	GetMaxWorkers() uint
	IsPriorityDispatcherEnabled() bool
	GetRetriggerBaseEndpoint() string
	GetMaxRetry() uint8
	GetRationalDelay() time.Duration
	GetRetryBackoffDelays() []time.Duration
	IsRecoveryWorkersEnabled() bool
}
