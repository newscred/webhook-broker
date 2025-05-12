package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/google/wire"
	"github.com/newscred/webhook-broker/config"
	"github.com/newscred/webhook-broker/controllers"
	"github.com/newscred/webhook-broker/dispatcher"
	"github.com/newscred/webhook-broker/prune"
	"github.com/newscred/webhook-broker/scheduler"
	"github.com/newscred/webhook-broker/storage"
	"github.com/newscred/webhook-broker/storage/data"
	"github.com/newscred/webhook-broker/utils"
	lumberjack "gopkg.in/natefinch/lumberjack.v2"
)

// ServerLifecycleListenerImpl is a blocking implementation around main method to wait on server actions
type ServerLifecycleListenerImpl struct {
	shutdownListener chan bool
}

// StartingServer called when listening is being started
func (impl *ServerLifecycleListenerImpl) StartingServer() {}

// ServerStartFailed called when server start failed due to error
func (impl *ServerLifecycleListenerImpl) ServerStartFailed(err error) {}

// ServerShutdownCompleted called once server has been shutdown
func (impl *ServerLifecycleListenerImpl) ServerShutdownCompleted() {
	go func() {
		impl.shutdownListener <- true
	}()
}

// HTTPServiceContainer wrapper for IoC too return
type HTTPServiceContainer struct {
	Configuration *config.Config
	Server        *http.Server
	DataAccessor  storage.DataAccessor
	Listener      *ServerLifecycleListenerImpl
	Dispatcher    dispatcher.MessageDispatcher
	Scheduler     scheduler.MessageScheduler
}

var (
	exit = func(code int) {
		os.Exit(code)
	}
	consolePrintln = func(output string) {
		fmt.Println(output)
	}

	// ErrMigrationSrcNotDir for error when migration source specified is not a directory
	ErrMigrationSrcNotDir = errors.New("migration source not a dir")

	parseArgs = func(programName string, args []string) (cliConfig *config.CLIConfig, output string, err error) {
		flags := flag.NewFlagSet(programName, flag.ContinueOnError)
		var buf bytes.Buffer
		flags.SetOutput(&buf)

		var conf config.CLIConfig
		commandString := flags.String("command", string(config.BrokerCMD), "What operation this process is supposed to do")
		flags.StringVar(&conf.ConfigPath, "config", "", "Config file location")
		flags.StringVar(&conf.MigrationSource, "migrate", "", "Migration source folder")
		flags.BoolVar(&conf.StopOnConfigChange, "stop-on-conf-change", false, "Restart internally on -config change if this flag is absent")
		flags.BoolVar(&conf.DoNotWatchConfigChange, "do-not-watch-conf-change", false, "Do not watch config change")

		err = flags.Parse(args)
		if err != nil {
			return nil, buf.String(), err
		}

		err = conf.SetCommandIfValid(*commandString)
		if err != nil {
			return nil, "command error", err
		}

		if len(conf.MigrationSource) > 0 {
			fileInfo, err := os.Stat(conf.MigrationSource)
			if err != nil {
				return nil, "Could not determine migration source details", err
			}
			if !fileInfo.IsDir() {
				return nil, "Migration source must be a dir", ErrMigrationSrcNotDir
			}
			if !filepath.IsAbs(conf.MigrationSource) {
				conf.MigrationSource, _ = filepath.Abs(conf.MigrationSource)
			}
			conf.MigrationSource = "file://" + conf.MigrationSource
		}

		return &conf, buf.String(), nil
	}

	getApp = func(httpServiceContainer *HTTPServiceContainer) (*data.App, error) {
		return httpServiceContainer.DataAccessor.GetAppRepository().GetApp()
	}

	startAppInit = func(httpServiceContainer *HTTPServiceContainer, seedData *config.SeedData) error {
		return httpServiceContainer.DataAccessor.GetAppRepository().StartAppInit(seedData)
	}

	getTimeoutTimer = func() <-chan time.Time {
		return time.After(time.Second * 10)
	}

	waitDuration = 1 * time.Second

	initApp = func(httpServiceContainer *HTTPServiceContainer) {
		app, err := getApp(httpServiceContainer)
		var initFinished chan bool = make(chan bool)
		timeout := getTimeoutTimer()
		if err == nil && app.GetStatus() == data.NotInitialized || (app.GetStatus() == data.Initialized && app.GetSeedData().DataHash != httpServiceContainer.Configuration.GetSeedData().DataHash) {
			go func() {
				run := true
				for run {
					select {
					case <-timeout:
						initFinished <- true
						run = false
					default:
						seedData := httpServiceContainer.Configuration.GetSeedData()
						initErr := startAppInit(httpServiceContainer, &seedData)
						switch initErr {
						case nil:
							createSeedData(httpServiceContainer.DataAccessor, httpServiceContainer.Configuration)
							completeErr := httpServiceContainer.DataAccessor.GetAppRepository().CompleteAppInit()
							log.Error().Err(completeErr).Msg("init error in setting complete flag")
							run = false
						case storage.ErrAppInitializing:
							run = false
						case storage.ErrOptimisticAppInit:
							run = false
						default:
							log.Error().Err(initErr).Msg("unexpected init error")
							time.Sleep(waitDuration)
						}
					}
				}
				initFinished <- true
			}()
			<-initFinished
		}
	}

	createSeedData = func(dataAccessor storage.DataAccessor, seedDataConfig config.SeedDataConfig) {
		for _, seedProducer := range seedDataConfig.GetSeedData().Producers {
			producer, err := data.NewProducer(seedProducer.ID, seedProducer.Token)
			if err == nil {
				producer.Name = seedProducer.Name
				_, err = dataAccessor.GetProducerRepository().Store(producer)
			}
			if err != nil {
				log.Error().Err(err).Msg("Error creating producer: " + seedProducer.ID)
			}
		}
		for _, seedChannel := range seedDataConfig.GetSeedData().Channels {
			channel, err := data.NewChannel(seedChannel.ID, seedChannel.Token)
			if err == nil {
				channel.Name = seedChannel.Name
				_, err = dataAccessor.GetChannelRepository().Store(channel)
			}
			if err != nil {
				log.Error().Err(err).Msg("Error creating channel" + seedChannel.ID)
			}
		}
		for _, seedConsumer := range seedDataConfig.GetSeedData().Consumers {
			channel, err := dataAccessor.GetChannelRepository().Get(seedConsumer.Channel)
			if err != nil {
				log.Error().Err(err).Msg("no channel for the consumer as per spec")
				continue
			}
			consumer, err := data.NewConsumer(channel, seedConsumer.ID, seedConsumer.Token, seedConsumer.CallbackURL, seedConsumer.Type)
			if err == nil {
				consumer.Name = seedConsumer.Name
				consumer.ConsumingFrom = channel
				consumer.CallbackURL = seedConsumer.CallbackURL.String()
				_, err = dataAccessor.GetConsumerRepository().Store(consumer)
			}
			if err != nil {
				log.Error().Err(err).Msg("Error creating consumer" + seedConsumer.ID)
			}
		}
	}
)

func main() {
	log.Print("Webhook Broker - " + string(GetAppVersion()))
	inConfig, output, cliCfgErr := parseArgs(os.Args[0], os.Args[1:])
	if cliCfgErr != nil {
		consolePrintln(output)
		if cliCfgErr != flag.ErrHelp {
			log.Error().Err(cliCfgErr).Msg("CLI config error")
		}
		exit(1)
	}
	log.Print("Configuration File (optional): " + inConfig.ConfigPath)
	if inConfig.Command == config.BrokerCMD {
		// Start the webhook broker service
		startWebhookBroker(inConfig)
	} else {
		pruneMessages(inConfig)
	}
}

func pruneMessages(inConfig *config.CLIConfig) {
	appConfig, err := config.GetConfigurationFromCLIConfig(inConfig) // appConfig
	if err != nil {
		log.Error().Err(err).Msg("could not use app config")
		exit(10)
	}
	setupLogger(appConfig)
	migrationConfig := GetMigrationConfig(inConfig)
	dataAccessor, err := storage.GetNewDataAccessor(appConfig, migrationConfig, appConfig)
	if err != nil {
		log.Error().Err(err).Msg("could not connect to data storage")
		exit(11)
	}
	err = prune.PruneMessages(dataAccessor, appConfig)
	if err != nil {
		log.Error().Err(err).Msg("could not prune messages")
		exit(12)
	}
}

func startWebhookBroker(inConfig *config.CLIConfig) {
	hasConfigChange := true
	var mutex sync.Mutex
	var setHasConfigChange = func(newVal bool) {
		mutex.Lock()
		defer mutex.Unlock()
		hasConfigChange = newVal
	}
	log.Print("On config change will stop? - ", inConfig.StopOnConfigChange)
	pid := syscall.Getpid()
	inConfig.NotifyOnConfigFileChange(func() {
		log.Print("Config file changed")
		if !inConfig.StopOnConfigChange {
			log.Print("Restarting")
			setHasConfigChange(true)
		}
		utils.NewProcessKiller().Kill(pid, syscall.SIGINT)
	})
	for hasConfigChange {
		setHasConfigChange(false)
		// Setup HTTP Server and listen (implicitly init DB and run migration if arg passed)
		httpServiceContainer, err := GetHTTPServer(inConfig)
		if err != nil {
			log.Error().Err(err).Msg("could not start http service")
			exit(3)
		}
		_, err = getApp(httpServiceContainer)
		if err == nil {
			initApp(httpServiceContainer)
		} else {
			log.Error().Err(err).Msg("could not retrieve app to initialize")
			exit(4)
		}
		var buf bytes.Buffer
		json.NewEncoder(&buf).Encode(httpServiceContainer.Configuration)
		log.Print("Configuration in Use : " + buf.String())
		// Setup Log Output
		setupLogger(httpServiceContainer.Configuration)

		// Start the scheduler
		httpServiceContainer.Scheduler.Start()

		<-httpServiceContainer.Listener.shutdownListener

		httpServiceContainer.Scheduler.Stop()
		httpServiceContainer.Dispatcher.Stop()
	}
	inConfig.StopWatcher()
}

func setupLogger(logConfig config.LogConfig) {
	switch logConfig.GetLogLevel() {
	case config.Debug:
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	case config.Info:
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	case config.Error:
		zerolog.SetGlobalLevel(zerolog.ErrorLevel)
	case config.Fatal:
		zerolog.SetGlobalLevel(zerolog.FatalLevel)
	}
	if logConfig.IsLoggerConfigAvailable() {
		log.Logger = log.Output(&lumberjack.Logger{
			Filename:   logConfig.GetLogFilename(),
			MaxSize:    int(logConfig.GetMaxLogFileSize()), // megabytes
			MaxBackups: int(logConfig.GetMaxLogBackups()),
			MaxAge:     int(logConfig.GetMaxAgeForALogFile()),        //days
			Compress:   logConfig.IsCompressionEnabledOnLogBackups(), // disabled by default
		})
	}
}

// Providers & Injectors

// NewServerListener initializes new server listener
func NewServerListener() *ServerLifecycleListenerImpl {
	return &ServerLifecycleListenerImpl{shutdownListener: make(chan bool)}
}

// GetMigrationConfig is provider for migration config
func GetMigrationConfig(cliConfig *config.CLIConfig) *storage.MigrationConfig {
	return &storage.MigrationConfig{MigrationEnabled: cliConfig.IsMigrationEnabled(), MigrationSource: cliConfig.MigrationSource}
}

func newAppRepository(dataAccessor storage.DataAccessor) storage.AppRepository {
	return dataAccessor.GetAppRepository()
}

func newProducerRepository(dataAccessor storage.DataAccessor) storage.ProducerRepository {
	return dataAccessor.GetProducerRepository()
}

func newChannelRepository(dataAccessor storage.DataAccessor) storage.ChannelRepository {
	return dataAccessor.GetChannelRepository()
}

func newConsumerRepository(dataAccessor storage.DataAccessor) storage.ConsumerRepository {
	return dataAccessor.GetConsumerRepository()
}

func newMessageRepository(dataAccessor storage.DataAccessor) storage.MessageRepository {
	return dataAccessor.GetMessageRepository()
}

func newDeliveryJobRepository(dataAccessor storage.DataAccessor) storage.DeliveryJobRepository {
	return dataAccessor.GetDeliveryJobRepository()
}

func newLockRepository(dataAccessor storage.DataAccessor) storage.LockRepository {
	return dataAccessor.GetLockRepository()
}

func newScheduledMessageRepository(dataAccessor storage.DataAccessor) storage.ScheduledMessageRepository {
	return dataAccessor.GetScheduledMessageRepository()
}

var (
	httpServiceContainerInjectorSet = wire.NewSet(wire.Struct(new(HTTPServiceContainer), "Configuration", "Server", "DataAccessor", "Listener", "Dispatcher", "Scheduler"))
	configInjectorSet               = wire.NewSet(httpServiceContainerInjectorSet, NewServerListener, GetMigrationConfig, wire.Bind(new(controllers.ServerLifecycleListener), new(*ServerLifecycleListenerImpl)), config.ConfigInjector)
	relationalDBWithControllerSet   = wire.NewSet(controllers.ControllerInjector, storage.GetNewDataAccessor, newLockRepository, newDeliveryJobRepository, newAppRepository, newChannelRepository, newProducerRepository, newConsumerRepository, newMessageRepository, newScheduledMessageRepository, dispatcher.MetricsInjector, dispatcher.DispatcherInjector, scheduler.SchedulerInjector)
)
