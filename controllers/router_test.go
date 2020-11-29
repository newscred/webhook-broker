package controllers

import (
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/imyousuf/webhook-broker/config"
	"github.com/imyousuf/webhook-broker/storage"
	"github.com/imyousuf/webhook-broker/storage/data"
	"github.com/stretchr/testify/mock"
)

type AppRepositoryMockImpl struct {
	mock.Mock
	defaultApp *data.App
}

func (m *AppRepositoryMockImpl) GetApp() (*data.App, error) {
	args := m.Called()
	return args.Get(0).(*data.App), args.Error(1)
}
func (m *AppRepositoryMockImpl) StartAppInit(data *config.SeedData) error {
	m.Called(data)
	return nil
}
func (m *AppRepositoryMockImpl) CompleteAppInit() error {
	m.Called()
	return nil
}

type DataAccessorMockImpl struct {
	mock.Mock
}

func (m *DataAccessorMockImpl) GetAppRepository() storage.AppRepository {
	args := m.Called()
	return args.Get(0).(storage.AppRepository)
}
func (m *DataAccessorMockImpl) Close() { m.Called() }

type ServerLifecycleListenerMockImpl struct {
	mock.Mock
	serverListener chan bool
}

func (m *ServerLifecycleListenerMockImpl) StartingServer()             { m.Called() }
func (m *ServerLifecycleListenerMockImpl) ServerStartFailed(err error) { m.Called(err) }
func (m *ServerLifecycleListenerMockImpl) ServerShutdownCompleted() {
	m.Called()
	m.serverListener <- true
}

var forceServerExiter = func(stop *chan os.Signal) {
	go func() {
		var client = &http.Client{Timeout: time.Second * 10}
		defer func() {
			client.CloseIdleConnections()
		}()
		for {
			response, err := client.Get("http://localhost:8080/_status")
			if err == nil {
				if response.StatusCode == 200 {
					break
				}
			}
		}
		*stop <- os.Interrupt
	}()
}

func TestConfigureAPI(t *testing.T) {
	mListener := &ServerLifecycleListenerMockImpl{serverListener: make(chan bool)}
	configuration, _ := config.GetAutoConfiguration()
	defaultApp := data.NewApp(&configuration.SeedData, data.Initialized)
	mAppRepo := new(AppRepositoryMockImpl)
	oldNotify := NotifyOnInterrupt
	NotifyOnInterrupt = forceServerExiter
	mListener.On("StartingServer").Return()
	mListener.On("ServerStartFailed", mock.Anything).Return()
	mListener.On("ServerShutdownCompleted").Return()
	mAppRepo.On("GetApp").Return(defaultApp, nil)
	ConfigureAPI(configuration, mListener, NewRouter(NewStatusController(mAppRepo)))
	<-mListener.serverListener
	mListener.AssertExpectations(t)
	mAppRepo.AssertExpectations(t)
	defer func() { NotifyOnInterrupt = oldNotify }()
}
