package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/google/wire"
	"github.com/imyousuf/webhook-broker/config"
	"github.com/imyousuf/webhook-broker/controllers"
	"github.com/imyousuf/webhook-broker/storage"
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
	Listener      *ServerLifecycleListenerImpl
}

var (
	exit = func(code int) {
		os.Exit(code)
	}
	consolePrintln = func(output string) {
		fmt.Println(output)
	}

	parseArgs = func(programName string, args []string) (cliConfig *config.CLIConfig, output string, err error) {
		flags := flag.NewFlagSet(programName, flag.ContinueOnError)
		var buf bytes.Buffer
		flags.SetOutput(&buf)

		var conf config.CLIConfig
		flags.StringVar(&conf.ConfigPath, "config", "", "Config file location")
		flags.StringVar(&conf.MigrationSource, "migrate", "", "Migration source folder")

		err = flags.Parse(args)
		if err != nil {
			return nil, buf.String(), err
		}

		if len(conf.MigrationSource) > 0 {
			fileInfo, err := os.Stat(conf.MigrationSource)
			if err != nil {
				return nil, "Could not determine migration source details", err
			}
			if !fileInfo.IsDir() {
				return nil, "Migration source must be a dir", errors.New("migration source not a dir")
			}
			if !filepath.IsAbs(conf.MigrationSource) {
				conf.MigrationSource, _ = filepath.Abs(conf.MigrationSource)
			}
			conf.MigrationSource = "file://" + conf.MigrationSource
		}

		return &conf, buf.String(), nil
	}
)

func main() {
	log.Println("Webhook Broker - " + string(GetAppVersion()))
	inConfig, output, cliCfgErr := parseArgs(os.Args[0], os.Args[1:])
	if cliCfgErr != nil {
		consolePrintln(output)
		if cliCfgErr != flag.ErrHelp {
			log.Fatalln(cliCfgErr)
		}
		exit(1)
	}
	log.Println("Configuration File (optional): " + inConfig.ConfigPath)
	// Setup HTTP Server and listen (implicitly init DB and run migration if arg passed)
	httpServiceContainer, err := GetHTTPServer(inConfig)
	if err != nil {
		log.Fatalln(err)
		exit(3)
	}
	var buf bytes.Buffer
	json.NewEncoder(&buf).Encode(httpServiceContainer.Configuration)
	log.Println("Configuration in Use : " + buf.String())
	// Setup Log Output
	setupLogger(httpServiceContainer.Configuration)
	<-httpServiceContainer.Listener.shutdownListener
}

func setupLogger(config config.LogConfig) {
	if config.IsLoggerConfigAvailable() {
		log.SetOutput(&lumberjack.Logger{
			Filename:   config.GetLogFilename(),
			MaxSize:    int(config.GetMaxLogFileSize()), // megabytes
			MaxBackups: int(config.GetMaxLogBackups()),
			MaxAge:     int(config.GetMaxAgeForALogFile()),        //days
			Compress:   config.IsCompressionEnabledOnLogBackups(), // disabled by default
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

// NewHTTPServiceContainer is provider for http service container
func NewHTTPServiceContainer(config *config.Config, listener *ServerLifecycleListenerImpl, server *http.Server) *HTTPServiceContainer {
	return &HTTPServiceContainer{Configuration: config, Server: server, Listener: listener}
}

var (
	configInjectorSet             = wire.NewSet(NewHTTPServiceContainer, NewServerListener, GetMigrationConfig, wire.Bind(new(controllers.ServerLifecycleListener), new(*ServerLifecycleListenerImpl)), config.ConfigInjector)
	relationalDBWithControllerSet = wire.NewSet(controllers.ConfigureAPI, storage.NewDataAccessor)
)
