package controllers

import (
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/imyousuf/webhook-broker/storage/data"
	storagemocks "github.com/imyousuf/webhook-broker/storage/mocks"
	"github.com/stretchr/testify/mock"
)

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
			response, err := client.Get("http://localhost:17654/_status")
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
	defaultApp := data.NewApp(&configuration.SeedData, data.Initialized)
	mAppRepo := new(storagemocks.AppRepository)
	oldNotify := NotifyOnInterrupt
	NotifyOnInterrupt = forceServerExiter
	mListener.On("StartingServer").Return()
	mListener.On("ServerStartFailed", mock.Anything).Return()
	mListener.On("ServerShutdownCompleted").Return()
	mAppRepo.On("GetApp").Return(defaultApp, nil)
	ConfigureAPI(configuration, mListener, NewRouter(&Controllers{StatusController: NewStatusController(mAppRepo),
		ProducersController: &ProducersController{}, ProducerController: &ProducerController{}, ChannelController: &ChannelController{}}))
	<-mListener.serverListener
	mListener.AssertExpectations(t)
	mAppRepo.AssertExpectations(t)
	defer func() { NotifyOnInterrupt = oldNotify }()
}
