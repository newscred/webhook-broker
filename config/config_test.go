package config

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"net"
	"os/user"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/go-ini/ini"
	"github.com/stretchr/testify/assert"
)

const (
	wrongValueConfig = `[rdbms]
	dialect=sqlite3
	connection-url=webhook-broker.sqlite3
	connxn-max-idle-time-seconds=-10
	connxn-max-lifetime-seconds=ascx0x
	max-idle-connxns=as30
	max-open-connxns=-100
	[http]
	listener=:7050
	read-timeout=asd240
	write-timeout=zf240
	[log]
	filename=/var/log/webhook-broker.log
	max-file-size-in-mb=as200
	max-backups=asd3
	max-age-in-days=dasd28
	compress-backups=asdtrue
	log-level=random
	# Generic Webhook Broker config such as - Max message queue size, max workers, priority dispatcher on, retrigger base-endpoint
	[broker]
	max-message-queue-size=asd10000
	max-workers=asd200
	priority-dispatcher-enabled=adtrue
	retrigger-base-endpoint=http://localhost:6080
	max-retry=5ad
	rational-delay-in-seconds=2sd0
	retry-backoff-delays-in-seconds=5,30,asd 6a 
	recovery-workers-enabled=random

	# Generic consumer configuration such as - Token Header name, User Agent, Consumer connection timeout
	[consumer-connection]
	token-header-name=
	user-agent=
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

	[sample-consumer]
	token=sample-consumer-token
	`
	errorConfig = `[rdbms]
	asda sdads
	connection-url=webhook_broker:zxc909zxc@tcp(mysql:3306)/webhook-broker?charset=utf8mb4&collation=utf8mb4_0900_ai_ci&parseTime=True
	`
	retriggerErrorMsg = "retrigger base endpoint is not in absolute URL form"
)

var (
	loadTestConfiguration = func(testConfiguration string) *ini.File {
		cfg, _ := ini.LooseLoad([]byte(DefaultConfiguration), DefaultSystemConfigFilePath, getUserHomeDirBasedDefaultConfigFileLocation(), DefaultCurrentDirConfigFilePath, []byte(testConfiguration))
		return cfg
	}
)

func toSecond(second uint) time.Duration {
	return time.Duration(second) * time.Second
}

func TestGetAutoConfiguration_Default(t *testing.T) {
	config, cfgErr := GetAutoConfiguration()
	if cfgErr != nil {
		t.Error("Auto Configuration failed", cfgErr)
		t.Fail()
	}
	assert.Equal(t, SQLite3Dialect, config.GetDBDialect())
	assert.Equal(t, "webhook-broker.sqlite3?_foreign_keys=on", config.GetDBConnectionURL())
	assert.Equal(t, time.Duration(0), config.GetDBConnectionMaxIdleTime())
	assert.Equal(t, time.Duration(0), config.GetDBConnectionMaxLifetime())
	assert.Equal(t, uint16(30), config.GetMaxIdleDBConnections())
	assert.Equal(t, uint16(100), config.GetMaxOpenDBConnections())
	assert.Equal(t, ":7050", config.GetHTTPListeningAddr())
	assert.Equal(t, toSecond(uint(240)), config.GetHTTPReadTimeout())
	assert.Equal(t, toSecond(uint(240)), config.GetHTTPWriteTimeout())
	assert.Equal(t, "", config.GetLogFilename())
	assert.Equal(t, Debug, config.GetLogLevel())
	assert.Equal(t, uint(200), config.GetMaxLogFileSize())
	assert.Equal(t, uint(28), config.GetMaxAgeForALogFile())
	assert.Equal(t, uint(3), config.GetMaxLogBackups())
	assert.Equal(t, true, config.IsCompressionEnabledOnLogBackups())
	assert.Equal(t, false, config.IsLoggerConfigAvailable())
	seedData := config.GetSeedData()
	assert.Equal(t, 1, len(seedData.Channels))
	assert.Equal(t, 1, len(seedData.Producers))
	assert.Equal(t, 1, len(seedData.Consumers))
	assert.NotEmpty(t, seedData.DataHash)
	seedChannel := seedData.Channels[0]
	assert.Equal(t, "sample-channel", seedChannel.ID)
	assert.Equal(t, "Sample Channel", seedChannel.Name)
	assert.Equal(t, "sample-channel-token", seedChannel.Token)
	seedProducer := seedData.Producers[0]
	assert.Equal(t, "sample-producer", seedProducer.ID)
	assert.Equal(t, "Sample Producer", seedProducer.Name)
	assert.Equal(t, "sample-producer-token", seedProducer.Token)
	seedConsumer := seedData.Consumers[0]
	assert.Equal(t, "sample-consumer", seedConsumer.ID)
	assert.Equal(t, "sample-consumer", seedConsumer.Name)
	assert.Equal(t, "sample-consumer-token", seedConsumer.Token)
	assert.Equal(t, "http://sample-endpoint/webhook-receiver", seedConsumer.CallbackURL.String())
	assert.Equal(t, "sample-channel", seedConsumer.Channel)
	assert.Equal(t, "Webhook Message Broker", config.GetUserAgent())
	assert.Equal(t, "X-Broker-Consumer-Token", config.GetTokenRequestHeaderName())
	assert.Equal(t, toSecond(30), config.GetConnectionTimeout())
	assert.Equal(t, uint(10000), config.GetMaxMessageQueueSize())
	assert.Equal(t, uint(200), config.GetMaxWorkers())
	assert.Equal(t, true, config.IsPriorityDispatcherEnabled())
	assert.Equal(t, "http://localhost:8080", config.GetRetriggerBaseEndpoint())
	assert.Equal(t, uint8(5), config.GetMaxRetry())
	assert.Equal(t, toSecond(2), config.GetRationalDelay())
	assert.Equal(t, true, config.IsRecoveryWorkersEnabled())
	assert.Equal(t, []time.Duration{toSecond(5), toSecond(30), toSecond(60)}, config.GetRetryBackoffDelays())
}

func TestGetAutoConfiguration_WrongValues(t *testing.T) {
	loadConfiguration = func(location string) (*ini.File, error) {
		return ini.InsensitiveLoad([]byte(wrongValueConfig))
	}
	config, cfgErr := GetAutoConfiguration()
	if cfgErr != nil {
		t.Error("Auto Configuration failed", cfgErr)
		t.Fail()
	}
	assert.Equal(t, time.Duration(0), config.GetDBConnectionMaxIdleTime())
	assert.Equal(t, time.Duration(0), config.GetDBConnectionMaxLifetime())
	assert.Equal(t, uint16(10), config.GetMaxIdleDBConnections())
	assert.Equal(t, uint16(50), config.GetMaxOpenDBConnections())
	assert.Equal(t, ":7050", config.GetHTTPListeningAddr())
	assert.Equal(t, toSecond(uint(180)), config.GetHTTPReadTimeout())
	assert.Equal(t, toSecond(uint(180)), config.GetHTTPWriteTimeout())
	assert.Equal(t, "/var/log/webhook-broker.log", config.GetLogFilename())
	assert.Equal(t, Debug, config.GetLogLevel())
	assert.Equal(t, uint(50), config.GetMaxLogFileSize())
	assert.Equal(t, uint(30), config.GetMaxAgeForALogFile())
	assert.Equal(t, uint(1), config.GetMaxLogBackups())
	assert.Equal(t, false, config.IsCompressionEnabledOnLogBackups())
	assert.Equal(t, true, config.IsLoggerConfigAvailable())
	assert.Equal(t, "Webhook Message Broker", config.GetUserAgent())
	assert.Equal(t, "X-Broker-Consumer-Token", config.GetTokenRequestHeaderName())
	assert.Equal(t, toSecond(60), config.GetConnectionTimeout())
	assert.Equal(t, uint(100000), config.GetMaxMessageQueueSize())
	assert.Equal(t, uint(100), config.GetMaxWorkers())
	assert.Equal(t, false, config.IsPriorityDispatcherEnabled())
	assert.Equal(t, "http://localhost:6080", config.GetRetriggerBaseEndpoint())
	assert.Equal(t, uint8(10), config.GetMaxRetry())
	assert.Equal(t, toSecond(30), config.GetRationalDelay())
	assert.Equal(t, []time.Duration{toSecond(5), toSecond(30), toSecond(15)}, config.GetRetryBackoffDelays())
	assert.Equal(t, 0, len(config.GetSeedData().Consumers))
	assert.Equal(t, true, config.IsRecoveryWorkersEnabled())
	defer func() {
		loadConfiguration = defaultLoadFunc
	}()
}

func TestGetAutoConfiguration_LoadConfigurationError(t *testing.T) {
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
		t.Fail()
	}
	assert.Equal(t, SQLite3Dialect, config.GetDBDialect())
	assert.Equal(t, "database.sqlite3", config.GetDBConnectionURL())
	assert.Equal(t, toSecond(10), config.GetDBConnectionMaxIdleTime())
	assert.Equal(t, toSecond(10), config.GetDBConnectionMaxLifetime())
	assert.Equal(t, uint16(300), config.GetMaxIdleDBConnections())
	assert.Equal(t, uint16(1000), config.GetMaxOpenDBConnections())
	assert.Equal(t, ":7080", config.GetHTTPListeningAddr())
	assert.Equal(t, toSecond(uint(2401)), config.GetHTTPReadTimeout())
	assert.Equal(t, toSecond(uint(2401)), config.GetHTTPWriteTimeout())
	assert.Equal(t, "/var/log/webhook-broker.log", config.GetLogFilename())
	assert.Equal(t, Error, config.GetLogLevel())
	assert.Equal(t, uint(20), config.GetMaxLogFileSize())
	assert.Equal(t, uint(280), config.GetMaxAgeForALogFile())
	assert.Equal(t, uint(30), config.GetMaxLogBackups())
	assert.Equal(t, false, config.IsCompressionEnabledOnLogBackups())
	assert.Equal(t, true, config.IsLoggerConfigAvailable())
	seedData := config.GetSeedData()
	assert.Equal(t, 3, len(seedData.Channels))
	assert.Equal(t, 3, len(seedData.Producers))
	assert.Equal(t, 6, len(seedData.Consumers))
	assert.NotEmpty(t, seedData.DataHash)
	seedChannel := seedData.Channels[0]
	assert.Equal(t, "sample-channel", seedChannel.ID)
	assert.Equal(t, "Sample Channel", seedChannel.Name)
	assert.Equal(t, "sample-channel-token", seedChannel.Token)
	seedChannel = seedData.Channels[1]
	assert.Equal(t, "test-channel", seedChannel.ID)
	assert.Equal(t, "Test Channel", seedChannel.Name)
	assert.Equal(t, "test-channel-token", seedChannel.Token)
	seedChannel = seedData.Channels[2]
	assert.Equal(t, "test-channel2", seedChannel.ID)
	assert.Equal(t, "Test Channel 2", seedChannel.Name)
	assert.Equal(t, "", seedChannel.Token)
	seedProducer := seedData.Producers[0]
	assert.Equal(t, "sample-producer", seedProducer.ID)
	assert.Equal(t, "Sample Producer", seedProducer.Name)
	assert.Equal(t, "sample-producer-token", seedProducer.Token)
	seedProducer = seedData.Producers[1]
	assert.Equal(t, "test-producer", seedProducer.ID)
	assert.Equal(t, "Test Producer", seedProducer.Name)
	assert.Equal(t, "test-producer-token", seedProducer.Token)
	seedProducer = seedData.Producers[2]
	assert.Equal(t, "test-producer2", seedProducer.ID)
	assert.Equal(t, "Test Producer 2", seedProducer.Name)
	assert.Equal(t, "", seedProducer.Token)
	seedConsumer := seedData.Consumers[0]
	assert.Equal(t, "sample-consumer", seedConsumer.ID)
	assert.Equal(t, "sample-consumer", seedConsumer.Name)
	assert.Equal(t, "sample-consumer-token", seedConsumer.Token)
	assert.Equal(t, "http://sample-endpoint/webhook-receiver", seedConsumer.CallbackURL.String())
	assert.Equal(t, PushConsumerStr, seedConsumer.Type)
	seedConsumer = seedData.Consumers[1]
	assert.Equal(t, "test-consumer", seedConsumer.ID)
	assert.Equal(t, "test-consumer", seedConsumer.Name)
	assert.Equal(t, "test-consumer-token", seedConsumer.Token)
	assert.Equal(t, "http://imy13.us/webhook-receiver", seedConsumer.CallbackURL.String())
	assert.Equal(t, PushConsumerStr, seedConsumer.Type)
	seedConsumer = seedData.Consumers[2]
	assert.Equal(t, "test-consumer4", seedConsumer.ID)
	assert.Equal(t, "test-consumer4", seedConsumer.Name)
	assert.Equal(t, "", seedConsumer.Token)
	assert.Equal(t, "http://imy13.us/webhook-receiver1", seedConsumer.CallbackURL.String())
	assert.Equal(t, PushConsumerStr, seedConsumer.Type)
	assert.Equal(t, "Test User Agent", config.GetUserAgent())
	assert.Equal(t, "X-Test-Consumer-Token", config.GetTokenRequestHeaderName())
	assert.Equal(t, toSecond(300), config.GetConnectionTimeout())
	assert.Equal(t, uint(20000), config.GetMaxMessageQueueSize())
	assert.Equal(t, uint(250), config.GetMaxWorkers())
	assert.Equal(t, true, config.IsPriorityDispatcherEnabled())
	assert.Equal(t, "http://localhost:7080", config.GetRetriggerBaseEndpoint())
	assert.Equal(t, uint8(7), config.GetMaxRetry())
	assert.Equal(t, toSecond(30), config.GetRationalDelay())
	assert.Equal(t, []time.Duration{toSecond(15), toSecond(30), toSecond(60), toSecond(120)}, config.GetRetryBackoffDelays())
	assert.False(t, config.IsRecoveryWorkersEnabled())
	seedConsumer = seedData.Consumers[3]
	assert.Equal(t, "test-consumer-push", seedConsumer.ID)
	assert.Equal(t, "test-consumer-push", seedConsumer.Name)
	assert.Equal(t, "test-consumer-token2", seedConsumer.Token)
	assert.Equal(t, "http://imy13.us/webhook-receiver", seedConsumer.CallbackURL.String())
	assert.Equal(t, PushConsumerStr, seedConsumer.Type)
	seedConsumer = seedData.Consumers[4]
	assert.Equal(t, "test-consumer-pull", seedConsumer.ID)
	assert.Equal(t, "test-consumer-pull", seedConsumer.Name)
	assert.Equal(t, "test-consumer-token2", seedConsumer.Token)
	assert.Equal(t, "http://imy13.us/webhook-receiver", seedConsumer.CallbackURL.String())
	assert.Equal(t, PullConsumerStr, seedConsumer.Type)
	seedConsumer = seedData.Consumers[5]
	assert.Equal(t, "test-consumer-default", seedConsumer.ID)
	assert.Equal(t, "test-consumer-default", seedConsumer.Name)
	assert.Equal(t, "test-consumer-token2", seedConsumer.Token)
	assert.Equal(t, "http://imy13.us/webhook-receiver", seedConsumer.CallbackURL.String())
	assert.Equal(t, PushConsumerStr, seedConsumer.Type)
	testConfig := `[log]
	log-level=info
	`
	config, err := GetConfigurationFromParseConfig(loadTestConfiguration(testConfig))
	assert.Equal(t, Info, config.GetLogLevel())
	assert.Nil(t, err)
	testConfig = `[log]
	log-level=fatal
	`
	config, err = GetConfigurationFromParseConfig(loadTestConfiguration(testConfig))
	assert.Equal(t, Fatal, config.GetLogLevel())
	assert.Nil(t, err)
}

func TestGetConfigurationFromParseConfig_ValueError(t *testing.T) {
	// Do not make it parallel
	t.Run("ConfigErrorDueToSQLlite3", func(t *testing.T) {
		oldPingSqlite3 := pingSqlite3
		dbErr := errors.New("db error")
		pingSqlite3 = func(db *sql.DB) error {
			return dbErr
		}
		config, err := GetConfigurationFromParseConfig(loadTestConfiguration("[testConfig]"))
		assert.Equal(t, EmptyConfigurationForError, config)
		assert.Equal(t, dbErr, err)
		pingSqlite3 = oldPingSqlite3
	})
	// Do not make it parallel
	t.Run("ConfigErrorDueToMySQL", func(t *testing.T) {
		oldPingMysql := pingMysql
		dbErr := errors.New("db error")
		pingMysql = func(db *sql.DB) error {
			return dbErr
		}
		testConfig := `[rdbms]
		dialect=mysql
		connection-url=webhook_broker:zxc909zxc@tcp(mysql:3306)/webhook-broker?charset=utf8mb4&collation=utf8mb4_0900_ai_ci&parseTime=true
		`
		config, err := GetConfigurationFromParseConfig(loadTestConfiguration(testConfig))
		assert.Equal(t, EmptyConfigurationForError, config)
		assert.Equal(t, dbErr, err)
		pingMysql = oldPingMysql
	})
	t.Run("RetriggerURLIsNotACorrectURL", func(t *testing.T) {
		t.Parallel()
		testConfig := `[broker]
		retrigger-base-endpoint= &*3@$%
		[http]
		listener=:18080
		`
		config, err := GetConfigurationFromParseConfig(loadTestConfiguration(testConfig))
		assert.Equal(t, EmptyConfigurationForError, config)
		assert.NotNil(t, err)
		assert.NotEqual(t, err.Error(), retriggerErrorMsg)
	})
	t.Run("RetriggerURLIsABlankString", func(t *testing.T) {
		t.Parallel()
		testConfig := `[broker]
		retrigger-base-endpoint=
		[http]
		listener=:28080
		`
		config, err := GetConfigurationFromParseConfig(loadTestConfiguration(testConfig))
		assert.Equal(t, EmptyConfigurationForError, config)
		assert.NotNil(t, err)
		assert.Equal(t, err.Error(), retriggerErrorMsg)
	})
	t.Run("RetriggerURLIsNotAAbsoluteURL", func(t *testing.T) {
		t.Parallel()
		testConfig := `[broker]
		retrigger-base-endpoint=/relative-url
		[http]
		listener=:38080
		`
		config, err := GetConfigurationFromParseConfig(loadTestConfiguration(testConfig))
		assert.Equal(t, EmptyConfigurationForError, config)
		assert.NotNil(t, err)
		assert.Equal(t, err.Error(), retriggerErrorMsg)
	})
	t.Run("DBDialectNotSupported", func(t *testing.T) {
		t.Parallel()
		testConfig := `[rdbms]
		dialect=mockdb
		[http]
		listener=:48080
		`
		config, err := GetConfigurationFromParseConfig(loadTestConfiguration(testConfig))
		assert.Equal(t, EmptyConfigurationForError, config)
		assert.NotNil(t, err)
		assert.Equal(t, errDBDialect, err)
	})
	t.Run("DBConnectionError", func(t *testing.T) {
		t.Parallel()
		testConfig := `[rdbms]
		dialect=mysql
		connection-url=expect dsn error
		[http]
		listener=:48090
		`
		config, err := GetConfigurationFromParseConfig(loadTestConfiguration(testConfig))
		assert.Equal(t, EmptyConfigurationForError, config)
		assert.NotNil(t, err)
	})
	t.Run("HTTPListenerNotAvailable", func(t *testing.T) {
		t.Parallel()
		testConfig := `
		[http]
		listener=:47070
		`
		ln, netErr := net.Listen("tcp", ":47070")
		if netErr == nil {
			defer ln.Close()
			config, err := GetConfigurationFromParseConfig(loadTestConfiguration(testConfig))
			assert.Equal(t, EmptyConfigurationForError, config)
			assert.NotNil(t, err)
		}
	})
	t.Run("DBPingErrorSQLite3", func(t *testing.T) {
		t.Parallel()
		db, mock, _ := sqlmock.New()
		mockedErr := errors.New("mock db error")
		mock.ExpectQuery("SELECT name FROM sqlite_master WHERE type='table'").WillReturnError(mockedErr)
		err := pingSqlite3(db)
		assert.Equal(t, mockedErr, err)
	})
	t.Run("DBPingErrorMySQL", func(t *testing.T) {
		t.Parallel()
		db, mock, _ := sqlmock.New()
		mockedErr := errors.New("mock db error")
		mock.ExpectQuery("SHOW Tables").WillReturnError(mockedErr)
		err := pingMysql(db)
		assert.Equal(t, mockedErr, err)
	})
	t.Run("DBPingMySQL", func(t *testing.T) {
		t.Parallel()
		db, mock, _ := sqlmock.New()
		rows := sqlmock.NewRows([]string{"ID", "Table Name"})
		mock.ExpectQuery("SHOW Tables").WillReturnRows(rows)
		err := pingMysql(db)
		assert.Nil(t, err)
	})
	t.Run("PruningConfigWrongRemoteDestination", func(t *testing.T) {
		t.Parallel()
		testConfig := `[prune]
		export-node-name=test-node
		message-retention-days=1
		remote-export-destination=invalid-destination
		[http]
		listener=:48091
		`
		config, err := GetConfigurationFromParseConfig(loadTestConfiguration(testConfig))
		assert.Equal(t, EmptyConfigurationForError, config)
		assert.NotNil(t, err)
		assert.Equal(t, "remote export destination is not valid", err.Error())
	})
	t.Run("PruningConfigNoRemoteDestinationForURL", func(t *testing.T) {
		t.Parallel()
		testConfig := `[prune]
		export-node-name=test-node
		message-retention-days=1
		remote-export-url=s3://my-bucket/prefix/
		[http]
		listener=:48092
		`
		config, err := GetConfigurationFromParseConfig(loadTestConfiguration(testConfig))
		assert.Equal(t, EmptyConfigurationForError, config)
		assert.NotNil(t, err)
		assert.Equal(t, "no remote destination set for remote export URL", err.Error())
	})
	t.Run("PruningConfigNoRemoteURLForDestination", func(t *testing.T) {
		t.Parallel()
		testConfig := `[prune]
		export-node-name=test-node
		message-retention-days=1
		remote-export-destination=s3
		[http]
		listener=:48093
		`
		config, err := GetConfigurationFromParseConfig(loadTestConfiguration(testConfig))
		if err == nil {
			t.Error("Should have failed for missing remote destination when remote saving is enabled")
			t.Fail()
		}
		assert.Equal(t, EmptyConfigurationForError, config)
		assert.NotNil(t, err)
		assert.Equal(t, "missing valid remote export URL", err.Error())
	})
}

func TestGetConfigurationFromCLIConfig(t *testing.T) {
	t.Run("EmptyPath", func(t *testing.T) {
		_, err := GetConfigurationFromCLIConfig(&CLIConfig{})
		assert.Nil(t, err)
	})
	t.Run("WithPath", func(t *testing.T) {
		_, err := GetConfigurationFromCLIConfig(&CLIConfig{ConfigPath: "./test-webhook-broker.cfg"})
		assert.Nil(t, err)
	})
}

func TestMigrationEnabled(t *testing.T) {
	t.Run("MigrationEnabled", func(t *testing.T) {
		t.Parallel()
		cliConfig := &CLIConfig{}
		assert.False(t, cliConfig.IsMigrationEnabled())
	})
	t.Run("MigrationDisabled", func(t *testing.T) {
		t.Parallel()
		cliConfig := &CLIConfig{MigrationSource: "file:///test/"}
		assert.True(t, cliConfig.IsMigrationEnabled())
	})
}

func TestSeedDataDBDriverFuncs(t *testing.T) {
	configuration, _ := GetAutoConfiguration()
	var buf bytes.Buffer
	_ = json.NewEncoder(&buf).Encode(configuration.GetSeedData())
	jsonSeedData := sql.RawBytes(buf.Bytes())
	t.Run("Scan", func(t *testing.T) {
		t.Parallel()
		seedData := &SeedData{}
		seedData.Scan(jsonSeedData)
		assert.Equal(t, configuration.GetSeedData(), *seedData)
	})
	t.Run("Value", func(t *testing.T) {
		t.Parallel()
		jsonDBVal, err := configuration.GetSeedData().Value()
		assert.Nil(t, err)
		assert.Equal(t, []byte(jsonSeedData), jsonDBVal.([]byte))
	})
}

func TestSetupMessagePruningConfiguration(t *testing.T) {
	t.Run("AllFieldsConfigured", func(t *testing.T) {
		cfg := ini.Empty()
		cfg.Section("prune").Key("export-node-name").SetValue("test-node")
		cfg.Section("prune").Key("message-retention-days").SetValue("14")
		cfg.Section("prune").Key("remote-export-destination").SetValue("s3")
		cfg.Section("prune").Key("remote-export-url").SetValue("s3://my-bucket/prefix/")
		cfg.Section("prune").Key("remote-file-prefix").SetValue("my-prefix")
		cfg.Section("prune").Key("max-archive-file-size-in-mb").SetValue("200")

		configuration := &Config{}
		setupMessagePruningConfiguration(cfg, configuration)

		assert.Equal(t, "test-node", configuration.ExportNodeName)
		assert.Equal(t, uint(14), configuration.MessageRetentionDays)
		assert.Equal(t, RemoteMessageDestinationS3, configuration.RemoteExportDestination)
		assert.Equal(t, "s3://my-bucket/prefix/", configuration.RemoteExportURL.String())
		assert.Equal(t, "my-prefix", configuration.RemoteFilePrefix)
		assert.Equal(t, uint(200), configuration.MaxArchiveFileSizeInMB)
	})

	t.Run("OnlyRequiredFieldsConfigured", func(t *testing.T) {
		cfg := ini.Empty()
		cfg.Section("prune").Key("export-node-name").SetValue("test-node")
		cfg.Section("prune").Key("message-retention-days").SetValue("7")

		configuration := &Config{}
		setupMessagePruningConfiguration(cfg, configuration)

		assert.Equal(t, "test-node", configuration.ExportNodeName)
		assert.Equal(t, uint(7), configuration.MessageRetentionDays)
		assert.Equal(t, "", string(configuration.RemoteExportDestination))
		assert.Nil(t, configuration.RemoteExportURL)
	})

	t.Run("InvalidRemoteURL", func(t *testing.T) {
		cfg := ini.Empty()
		cfg.Section("prune").Key("export-node-name").SetValue("test-node")
		cfg.Section("prune").Key("message-retention-days").SetValue("1")
		cfg.Section("prune").Key("remote-export-destination").SetValue("s3")
		cfg.Section("prune").Key("remote-export-url").SetValue("invalid-url")

		configuration := &Config{}
		setupMessagePruningConfiguration(cfg, configuration)

		// Assert that the RemoteExportURL is nil because of the invalid URL
		assert.Nil(t, configuration.RemoteExportURL)
	})

	t.Run("DefaultsWhenSectionMissing", func(t *testing.T) {
		cfg := ini.Empty()

		configuration := &Config{}
		setupMessagePruningConfiguration(cfg, configuration)

		assert.Equal(t, "", configuration.ExportNodeName)
		assert.Equal(t, uint(0), configuration.MessageRetentionDays)
		assert.Equal(t, "", string(configuration.RemoteExportDestination))
		assert.Nil(t, configuration.RemoteExportURL)
	})
}

func TestGetVersion(t *testing.T) {
	assert.NotEmpty(t, GetVersion())
}

func TestConfigInterfaces(t *testing.T) {
	var _ RelationalDatabaseConfig = (*Config)(nil)
	var _ HTTPConfig = (*Config)(nil)
	var _ LogConfig = (*Config)(nil)
	var _ SeedDataConfig = (*Config)(nil)
	var _ ConsumerConnectionConfig = (*Config)(nil)
	var _ MessagePruningConfig = (*Config)(nil)
}
