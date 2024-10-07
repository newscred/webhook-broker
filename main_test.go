package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/newscred/webhook-broker/config"
	"github.com/newscred/webhook-broker/controllers"
	"github.com/newscred/webhook-broker/storage"
	"github.com/newscred/webhook-broker/storage/data"
	"github.com/newscred/webhook-broker/storage/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

const (
	configFilePath             = "./testdatadir/webhook-broker.main.cfg"
	configFilePath2            = "./testdatadir/webhook-broker.main-2.cfg"
	notificationInitialContent = `[http]
	listener=:12345	
	`
	notificationDifferentContent = `[http]
	listener=:8080
	`
)

func TestGetAppVersion(t *testing.T) {
	assert.Equal(t, string(GetAppVersion()), "0.2-alpha-early-access.4")
}

var mainFunctionBreaker = func(stop *chan os.Signal) {
	go func() {
		waitForStatusEndpoint(":8080")
		fmt.Println("Interrupt sent")
		*stop <- os.Interrupt
	}()
}

var waitForStatusEndpoint = func(portString string) {
	var client = &http.Client{Timeout: time.Second * 10}
	defer func() {
		client.CloseIdleConnections()
	}()
	for {
		response, err := client.Get("http://localhost" + portString + "/_status")
		if err == nil {
			if response.StatusCode == 200 {
				break
			}
		}
	}
}

var configChangeRestartMainFnBreaker = func(stop *chan os.Signal) {
	go func() {
		waitForStatusEndpoint(":12345")
		ioutil.WriteFile(configFilePath, []byte(notificationDifferentContent), 0644)
		time.Sleep(1 * time.Millisecond)
		fmt.Println("called mainFnBreaker")
		mainFunctionBreaker(stop)
	}()
}

var panicExit = func(code int) {
	panic(code)
}

func TestMainFunc(t *testing.T) {
	os.Remove("./webhook-broker.sqlite3")
	t.Run("GetAppErr", func(t *testing.T) {
		oldExit := exit
		oldArgs := os.Args
		oldGetApp := getApp
		getApp = func(httpServiceContainer *HTTPServiceContainer) (*data.App, error) {
			serverShutdownContext, shutdownTimeoutCancelFunc := context.WithTimeout(context.Background(), 15*time.Second)
			defer shutdownTimeoutCancelFunc()
			httpServiceContainer.Server.Shutdown(serverShutdownContext)
			return nil, errors.New("No App Error")
		}
		exit = panicExit
		os.Args = []string{"webhook-broker", "-migrate", "./migration/sqls/"}
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Print(r)
					assert.Equal(t, 4, r.(int))
				} else {
					t.Fail()
				}
			}()
			main()
		}()
		defer func() {
			exit = oldExit
			os.Args = oldArgs
			getApp = oldGetApp
		}()
	})
	t.Run("StartInitRaceErrInBetween", func(t *testing.T) {
		oldExit := exit
		oldArgs := os.Args
		oldStartAppInit := startAppInit
		oldNotify := controllers.NotifyOnInterrupt
		controllers.NotifyOnInterrupt = mainFunctionBreaker
		startAppInit = func(httpServiceContainer *HTTPServiceContainer, seedData *config.SeedData) error {
			return storage.ErrAppInitializing
		}
		exit = panicExit
		os.Args = []string{"webhook-broker"}
		main()
		defer func() {
			exit = oldExit
			os.Args = oldArgs
			startAppInit = oldStartAppInit
			controllers.NotifyOnInterrupt = oldNotify
		}()
	})
	t.Run("StartInitRaceErrDuringUpdate", func(t *testing.T) {
		oldExit := exit
		oldArgs := os.Args
		oldStartAppInit := startAppInit
		oldNotify := controllers.NotifyOnInterrupt
		controllers.NotifyOnInterrupt = mainFunctionBreaker
		startAppInit = func(httpServiceContainer *HTTPServiceContainer, seedData *config.SeedData) error {
			return storage.ErrOptimisticAppInit
		}
		exit = panicExit
		os.Args = []string{"webhook-broker"}
		main()
		defer func() {
			exit = oldExit
			os.Args = oldArgs
			startAppInit = oldStartAppInit
			controllers.NotifyOnInterrupt = oldNotify
		}()
	})
	t.Run("SuccessRunWithAutoRestartOnConfigChange", func(t *testing.T) {
		var buf bytes.Buffer
		oldLogger := log.Logger
		log.Logger = log.Output(&buf)
		oldArgs := os.Args
		ioutil.WriteFile(configFilePath, []byte(notificationInitialContent), 0644)
		os.Args = []string{"webhook-broker", "-migrate", "./migration/sqls/", "-config", configFilePath}
		oldNotify := controllers.NotifyOnInterrupt
		controllers.NotifyOnInterrupt = configChangeRestartMainFnBreaker
		defer func() {
			log.Logger = oldLogger
			os.Args = oldArgs
			controllers.NotifyOnInterrupt = oldNotify
		}()
		main()
		logString := buf.String()
		assert.Contains(t, logString, "Webhook Broker")
		assert.Contains(t, logString, string(GetAppVersion()))
		t.Log(logString)
		// Assert App initialization completed
		configuration, _ := config.GetAutoConfiguration()
		migrationConf := &storage.MigrationConfig{MigrationEnabled: false}
		dataAccessor, _ := storage.GetNewDataAccessor(configuration, migrationConf, configuration)
		app, err := dataAccessor.GetAppRepository().GetApp()
		assert.Nil(t, err)
		assert.Equal(t, data.Initialized, app.GetStatus())
		// Load and assert seed data
		channel, err := dataAccessor.GetChannelRepository().Get("sample-channel")
		assert.Nil(t, err)
		assert.NotNil(t, channel)
		producer, err := dataAccessor.GetProducerRepository().Get("sample-producer")
		assert.Nil(t, err)
		assert.NotNil(t, producer)
		consumer, err := dataAccessor.GetConsumerRepository().Get("sample-channel", "sample-consumer")
		assert.Nil(t, err)
		assert.NotNil(t, consumer)
	})
	t.Run("SuccessRunWithExitOnConfigChange", func(t *testing.T) {
		ioutil.WriteFile(configFilePath2, []byte(notificationInitialContent), 0644)
		oldArgs := os.Args
		os.Args = []string{"webhook-broker", "-migrate", "./migration/sqls/", "-config", configFilePath2, "-stop-on-conf-change"}
		defer func() {
			os.Args = oldArgs
		}()
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			main()
			wg.Done()
		}()
		go func() {
			waitForStatusEndpoint(":12345")
			ioutil.WriteFile(configFilePath2, []byte(notificationDifferentContent), 0644)
			wg.Done()
		}()
		wg.Wait()
	})
	t.Run("HelpError", func(t *testing.T) {
		oldExit := exit
		oldArgs := os.Args
		oldConsole := consolePrintln
		exit = panicExit
		consolePrintln = func(output string) {
			assert.Contains(t, output, "Usage of")
			assert.Contains(t, output, "-config")
			assert.Contains(t, output, "-migrate")
		}
		os.Args = []string{"webhook-broker", "-h"}
		func() {
			defer func() {
				if r := recover(); r != nil {
					assert.Equal(t, 1, r.(int))
				} else {
					t.Fail()
				}
			}()
			main()
		}()
		defer func() {
			exit = oldExit
			os.Args = oldArgs
			consolePrintln = oldConsole
		}()
	})
	t.Run("ParseError", func(t *testing.T) {
		oldExit := exit
		oldArgs := os.Args
		exit = panicExit
		os.Args = []string{"webhook-broker", "-migrate1=test"}
		func() {
			defer func() {
				if r := recover(); r != nil {
					assert.Equal(t, 1, r.(int))
				} else {
					t.Fail()
				}
			}()
			main()
		}()
		defer func() {
			exit = oldExit
			os.Args = oldArgs
		}()
	})
	t.Run("NoWatcher", func(t *testing.T) {
		oldExit := exit
		oldArgs := os.Args
		exit = panicExit
		os.Args = []string{"webhook-broker", "-do-not-watch-conf-change"}
		inConfig, _, cliCfgErr := parseArgs(os.Args[0], os.Args[1:])
		assert.Nil(t, cliCfgErr)
		assert.True(t, inConfig.DoNotWatchConfigChange)
		inConfig.NotifyOnConfigFileChange(func() {
			t.FailNow()
		})
		assert.False(t, inConfig.IsConfigWatcherStarted())
		defer func() {
			exit = oldExit
			os.Args = oldArgs
		}()
	})
	t.Run("ConfError", func(t *testing.T) {
		ln, netErr := net.Listen("tcp", ":8080")
		if netErr == nil {
			defer ln.Close()
			oldExit := exit
			oldArgs := os.Args
			exit = panicExit
			os.Args = []string{"webhook-broker"}
			func() {
				defer func() {
					if r := recover(); r != nil {
						assert.Equal(t, 3, r.(int))
					} else {
						t.Fail()
					}
				}()
				main()
			}()
			defer func() {
				exit = oldExit
				os.Args = oldArgs
			}()
		}
	})
}

func TestParseArgs(t *testing.T) {
	absPath, _ := filepath.Abs("./migration")
	t.Run("FlagParseError", func(t *testing.T) {
		t.Parallel()
		_, _, err := parseArgs("webhook-broker", []string{"-migrate1", "no such path"})
		assert.NotNil(t, err)
	})
	t.Run("NonExistentMigrationSource", func(t *testing.T) {
		t.Parallel()
		_, _, err := parseArgs("webhook-broker", []string{"-migrate", "no such path"})
		assert.NotNil(t, err)
	})
	t.Run("MigrationSourceNotDir", func(t *testing.T) {
		t.Parallel()
		_, _, err := parseArgs("webhook-broker", []string{"-migrate", "./Makefile"})
		assert.NotNil(t, err)
		assert.Equal(t, err, ErrMigrationSrcNotDir)
	})
	t.Run("ValidMigrationSourceAbs", func(t *testing.T) {
		t.Parallel()
		cliConfig, _, err := parseArgs("webhook-broker", []string{"-migrate", "./migration"})
		assert.Nil(t, err)
		assert.True(t, cliConfig.IsMigrationEnabled())
		assert.Equal(t, "file://"+absPath, cliConfig.MigrationSource)
	})
	t.Run("ValidMigrationSourceRelative", func(t *testing.T) {
		t.Parallel()
		cliConfig, _, err := parseArgs("webhook-broker", []string{"-migrate", absPath})
		assert.Nil(t, err)
		assert.True(t, cliConfig.IsMigrationEnabled())
		assert.Equal(t, "file://"+absPath, cliConfig.MigrationSource)
	})
}

func TestInitAppTime(t *testing.T) {
	oldGetTimeoutTimer := getTimeoutTimer
	oldGetApp := getApp
	oldStartAppInit := startAppInit
	oldGetWaitDuration := waitDuration
	getTimeoutTimer = func() <-chan time.Time {
		return time.After(time.Millisecond * 100)
	}
	waitDuration = 110 * time.Millisecond
	getApp = func(httpServiceContainer *HTTPServiceContainer) (*data.App, error) {
		seedData := &config.SeedData{}
		seedData.DataHash = "TEST"
		return data.NewApp(seedData, data.Initialized), nil
	}
	initErr := errors.New("test err")
	startAppInit = func(httpServiceContainer *HTTPServiceContainer, seedData *config.SeedData) error {
		return initErr
	}
	defaultConfig, err := config.GetAutoConfiguration()
	if err != nil {
		t.Fatal(err)
	}
	container := &HTTPServiceContainer{Configuration: defaultConfig}
	initApp(container)
	// Test finishing without error is itself success as no DB call is attempted
	defer func() {
		getTimeoutTimer = oldGetTimeoutTimer
		getApp = oldGetApp
		startAppInit = oldStartAppInit
		waitDuration = oldGetWaitDuration
	}()
}

const testLogFile = "./log-setup-test-output.log"

type MockLogConfig struct {
	logLevel config.LogLevel
}

func (m MockLogConfig) GetLogLevel() config.LogLevel           { return m.logLevel }
func (m MockLogConfig) GetLogFilename() string                 { return testLogFile }
func (m MockLogConfig) GetMaxLogFileSize() uint                { return 10 }
func (m MockLogConfig) GetMaxLogBackups() uint                 { return 1 }
func (m MockLogConfig) GetMaxAgeForALogFile() uint             { return 1 }
func (m MockLogConfig) IsCompressionEnabledOnLogBackups() bool { return true }
func (m MockLogConfig) IsLoggerConfigAvailable() bool          { return true }

func TestSetupLog(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Log(r)
		}
	}()
	configs := make(map[config.LogLevel]func(), 4)
	configs[config.Debug] = func() {
		log.Print("unit test debug")
		log.Info().Msg("unit test info")
		log.Error().Msg("unit test error")
		dat, err := ioutil.ReadFile(testLogFile)
		assert.Nil(t, err)
		assert.Contains(t, string(dat), "unit test debug")
		assert.Contains(t, string(dat), "unit test info")
		assert.Contains(t, string(dat), "unit test error")
	}
	configs[config.Info] = func() {
		log.Print("unit test debug")
		log.Info().Msg("unit test info")
		log.Error().Msg("unit test error")
		dat, err := ioutil.ReadFile(testLogFile)
		assert.Nil(t, err)
		assert.NotContains(t, string(dat), "unit test debug")
		assert.Contains(t, string(dat), "unit test info")
		assert.Contains(t, string(dat), "unit test error")
	}
	configs[config.Error] = func() {
		log.Print("unit test debug")
		log.Info().Msg("unit test info")
		log.Error().Msg("unit test error")
		dat, err := ioutil.ReadFile(testLogFile)
		assert.Nil(t, err)
		assert.NotContains(t, string(dat), "unit test debug")
		assert.NotContains(t, string(dat), "unit test info")
		assert.Contains(t, string(dat), "unit test error")
	}
	configs[config.Fatal] = func() {
		assert.Equal(t, zerolog.FatalLevel, zerolog.GlobalLevel())
	}
	for logLevel, testFunc := range configs {
		_, err := os.Stat(testLogFile)
		if err == nil {
			os.Remove(testLogFile)
		}
		setupLogger(&MockLogConfig{logLevel: logLevel})
		testFunc()
	}
}

func TestCreateSeedData_ErrorFlows(t *testing.T) {
	var buf bytes.Buffer
	oldLogger := log.Logger
	log.Logger = log.Output(&buf)
	expectedErr := errors.New("expected main seed data error")
	defer func() {
		assert.Contains(t, expectedErr.Error(), buf.String())
		assert.Contains(t, "Error creating producer", buf.String())
		assert.Contains(t, "Error creating channel", buf.String())
		assert.Contains(t, "Error creating consumer", buf.String())
		log.Logger = oldLogger
	}()
	configuration, _ := config.GetAutoConfiguration()
	t.Run("StoreError", func(t *testing.T) {
		t.Parallel()
		dataAccessor := new(mocks.DataAccessor)
		productRepo := new(mocks.ProducerRepository)
		channelRepo := new(mocks.ChannelRepository)
		consumerRepo := new(mocks.ConsumerRepository)
		dataAccessor.On("GetProducerRepository").Return(productRepo)
		dataAccessor.On("GetChannelRepository").Return(channelRepo)
		dataAccessor.On("GetConsumerRepository").Return(consumerRepo)
		productRepo.On("Store", mock.Anything).Return(nil, expectedErr)
		channelRepo.On("Get", mock.Anything).Return(&data.Channel{}, nil)
		channelRepo.On("Store", mock.Anything).Return(nil, expectedErr)
		consumerRepo.On("Store", mock.Anything).Return(nil, expectedErr)
		createSeedData(dataAccessor, configuration)
		dataAccessor.AssertExpectations(t)
	})
	t.Run("ChannelGetError", func(t *testing.T) {
		t.Parallel()
		dataAccessor := new(mocks.DataAccessor)
		productRepo := new(mocks.ProducerRepository)
		channelRepo := new(mocks.ChannelRepository)
		dataAccessor.On("GetProducerRepository").Return(productRepo)
		dataAccessor.On("GetChannelRepository").Return(channelRepo)
		productRepo.On("Store", mock.Anything).Return(nil, expectedErr)
		channelRepo.On("Get", mock.Anything).Return(&data.Channel{}, expectedErr)
		channelRepo.On("Store", mock.Anything).Return(nil, expectedErr)
		createSeedData(dataAccessor, configuration)
		dataAccessor.AssertExpectations(t)
	})
}
