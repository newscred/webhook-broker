package dispatcher

import (
	"bytes"
	"context"
	"errors"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/imyousuf/webhook-broker/config"
	configmocks "github.com/imyousuf/webhook-broker/config/mocks"
	"github.com/imyousuf/webhook-broker/storage"
	"github.com/imyousuf/webhook-broker/storage/data"
	storagemocks "github.com/imyousuf/webhook-broker/storage/mocks"
	"github.com/julienschmidt/httprouter"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

const (
	consumerReceivedURLParamPrefix = "/consumer-"
	consumerToken                  = "random-consumer-token"
	consumerIDPrefix               = "test-consumer-for-dispatcher-"
)

var (
	migrationLocation, _ = filepath.Abs("../migration/sqls/")
	defaultMigrationConf = &storage.MigrationConfig{MigrationEnabled: true, MigrationSource: "file://" + migrationLocation}
	dataAccessor         storage.DataAccessor
	channel              *data.Channel
	producer             *data.Producer
	consumers            []*data.Consumer
	configuration        *config.Config
	server               *http.Server
	consumerHandler      map[string]func(string, http.ResponseWriter, *http.Request)
)

func TestMain(m *testing.M) {
	// Setup DB and migration
	os.Remove("./webhook-broker.sqlite3")
	configuration, _ = config.GetAutoConfiguration()
	var dbErr error
	dataAccessor, dbErr = storage.GetNewDataAccessor(configuration, defaultMigrationConf, configuration)
	if dbErr == nil {
		SetupTestFixture()
		m.Run()
		serverShutdownContext, shutdownTimeoutCancelFunc := context.WithTimeout(context.Background(), 15*time.Second)
		defer shutdownTimeoutCancelFunc()
		server.Shutdown(serverShutdownContext)
		defer dataAccessor.Close()
	}
}

func findPort() int {
	for port := 55666; port < 60000; port++ {
		if checkPort(port) == nil {
			return port
		}
	}
	return 0
}

func checkPort(port int) (err error) {
	ln, netErr := net.Listen("tcp", ":"+strconv.Itoa(port))
	defer ln.Close()
	if netErr != nil {
		log.Println(netErr)
		err = netErr
	}
	return err
}

func consumerController(w http.ResponseWriter, r *http.Request, params httprouter.Params) {
	consumerID := params.ByName("consumerId")
	if customController, ok := consumerHandler[consumerID]; ok {
		customController(consumerID, w, r)
	} else {
		w.WriteHeader(http.StatusNoContent)
	}
}

func SetupTestFixture() {
	port := findPort()
	if port == 0 {
		log.Fatalln("could not find port to start test consumer service")
	}
	portString := ":" + strconv.Itoa(port)
	baseURLString := "http://localhost" + portString
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		testConsumerRouter := httprouter.New()
		testConsumerRouter.POST("/:consumerId", consumerController)
		server = &http.Server{
			Handler: testConsumerRouter,
			Addr:    portString,
		}
		go func() {
			if serverListenErr := server.ListenAndServe(); serverListenErr != nil {
				log.Println(serverListenErr)
			}
		}()
		wg.Done()
	}()
	go func() {
		consumerHandler = make(map[string]func(string, http.ResponseWriter, *http.Request))
		channel, _ = data.NewChannel("dispatch-test-channel", "token")
		channel.QuickFix()
		channel, _ = dataAccessor.GetChannelRepository().Store(channel)
		producer, _ = data.NewProducer("dispatch-test-producer", "token")
		producer.QuickFix()
		producer, _ = dataAccessor.GetProducerRepository().Store(producer)
		testConsumers := 30
		consumers = make([]*data.Consumer, 0, testConsumers)
		for i := 0; i < testConsumers; i++ {
			callbackURL, _ := url.Parse(baseURLString + consumerReceivedURLParamPrefix + strconv.Itoa(i))
			consumer, _ := data.NewConsumer(channel, consumerIDPrefix+strconv.Itoa(i), consumerToken, callbackURL)
			consumer.QuickFix()
			dataAccessor.GetConsumerRepository().Store(consumer)
			consumers = append(consumers, consumer)
		}
		wg.Done()
	}()
	wg.Wait()
}

func getMockedBrokerConfig() *configmocks.BrokerConfig {
	mockedConfig := new(configmocks.BrokerConfig)
	mockedConfig.On("GetMaxMessageQueueSize").Return(uint(10))
	mockedConfig.On("GetMaxWorkers").Return(uint(10))
	return mockedConfig
}

func getMockedConsumerConfig() *configmocks.ConsumerConnectionConfig {
	mockedConfig := new(configmocks.ConsumerConnectionConfig)
	mockedConfig.On("GetConnectionTimeout").Return(100 * time.Millisecond)
	return mockedConfig
}

func TestNewMessageDispatcher(t *testing.T) {
	deferFunc := func() {
		if r := recover(); r != panicString {
			t.Fail()
		}
	}
	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		mockBrokerConfig := getMockedBrokerConfig()
		mockConsumerConfig := getMockedConsumerConfig()
		mRepo := new(storagemocks.DeliveryJobRepository)
		cRepo := new(storagemocks.ConsumerRepository)
		dispatcher := NewMessageDispatcher(mRepo, cRepo, mockBrokerConfig, mockConsumerConfig)
		assert.NotNil(t, dispatcher)
		dispatcher.Stop()
		mockBrokerConfig.AssertExpectations(t)
	})
	t.Run("MsgRepoNil", func(t *testing.T) {
		t.Parallel()
		defer deferFunc()
		mockBrokerConfig := new(configmocks.BrokerConfig)
		mockConsumerConfig := new(configmocks.ConsumerConnectionConfig)
		cRepo := new(storagemocks.ConsumerRepository)
		assert.NotNil(t, NewMessageDispatcher(nil, cRepo, mockBrokerConfig, mockConsumerConfig))
	})
	t.Run("ConsumerRepoNil", func(t *testing.T) {
		t.Parallel()
		defer deferFunc()
		mockBrokerConfig := new(configmocks.BrokerConfig)
		mockConsumerConfig := new(configmocks.ConsumerConnectionConfig)
		mRepo := new(storagemocks.DeliveryJobRepository)
		assert.NotNil(t, NewMessageDispatcher(mRepo, nil, mockBrokerConfig, mockConsumerConfig))
	})
	t.Run("BrokerConfigNil", func(t *testing.T) {
		t.Parallel()
		defer deferFunc()
		mockConsumerConfig := new(configmocks.ConsumerConnectionConfig)
		mRepo := new(storagemocks.DeliveryJobRepository)
		cRepo := new(storagemocks.ConsumerRepository)
		assert.NotNil(t, NewMessageDispatcher(mRepo, cRepo, nil, mockConsumerConfig))
	})
	t.Run("ConsumerConnectionConfigNil", func(t *testing.T) {
		t.Parallel()
		defer deferFunc()
		mockBrokerConfig := new(configmocks.BrokerConfig)
		mRepo := new(storagemocks.DeliveryJobRepository)
		cRepo := new(storagemocks.ConsumerRepository)
		assert.NotNil(t, NewMessageDispatcher(mRepo, cRepo, mockBrokerConfig, nil))
	})
}

func clearConsumerHandler() {
	for key := range consumerHandler {
		delete(consumerHandler, key)
	}
}

func TestMessageDispatcherImplDispatch(t *testing.T) {
	t.Run("NilMessage", func(t *testing.T) {
		t.Parallel()
		mRepo := new(storagemocks.DeliveryJobRepository)
		cRepo := new(storagemocks.ConsumerRepository)
		mockBrokerConfig := getMockedBrokerConfig()
		mockConsumerConfig := getMockedConsumerConfig()
		dispatcher := NewMessageDispatcher(mRepo, cRepo, mockBrokerConfig, mockConsumerConfig)
		assert.NotNil(t, dispatcher)
		dispatcher.Dispatch(nil)
		mockBrokerConfig.AssertExpectations(t)
	})
	t.Run("InvalidMessage", func(t *testing.T) {
		t.Parallel()
		mRepo := new(storagemocks.DeliveryJobRepository)
		cRepo := new(storagemocks.ConsumerRepository)
		mockBrokerConfig := getMockedBrokerConfig()
		mockConsumerConfig := getMockedConsumerConfig()
		dispatcher := NewMessageDispatcher(mRepo, cRepo, mockBrokerConfig, mockConsumerConfig)
		assert.NotNil(t, dispatcher)
		msg, _ := data.NewMessage(channel, producer, "payload", "type")
		msg.ReceivedAt = time.Time{}
		dispatcher.Dispatch(msg)
		mockBrokerConfig.AssertExpectations(t)
	})
	t.Run("ListError", func(t *testing.T) {
		var buf bytes.Buffer
		log.SetOutput(&buf)
		defer func() {
			log.SetOutput(os.Stderr)
		}()
		mRepo := new(storagemocks.DeliveryJobRepository)
		cRepo := new(storagemocks.ConsumerRepository)
		mockBrokerConfig := getMockedBrokerConfig()
		mockConsumerConfig := getMockedConsumerConfig()
		dispatcher := NewMessageDispatcher(mRepo, cRepo, mockBrokerConfig, mockConsumerConfig)
		assert.NotNil(t, dispatcher)
		msg, _ := data.NewMessage(channel, producer, "payload", "type")
		err := dataAccessor.GetMessageRepository().Create(msg)
		assert.Nil(t, err)
		expectedErr := errors.New("error on list call")
		cRepo.On("GetList", channel.ChannelID, mock.Anything).Return(make([]*data.Consumer, 0), data.NewPagination(nil, nil), expectedErr)
		dispatcher.Dispatch(msg)
		assert.Contains(t, buf.String(), expectedErr.Error())
	})
	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		t.Cleanup(clearConsumerHandler)
		var wg sync.WaitGroup
		oldDeliverJob := deliverJob
		deliverJob = func(w *Worker, job *Job) {
			oldDeliverJob(w, job)
			wg.Done()
		}
		defer func() {
			deliverJob = oldDeliverJob
		}()
		messagePayload := `{"key": "Custom JSON"}`
		contentType := "application/json"
		for index := 0; index < len(consumers); index++ {
			consumerHandler["consumer-"+strconv.Itoa(index)] = func(s string, rw http.ResponseWriter, r *http.Request) {
				// check content body and type
				assert.Equal(t, contentType, r.Header.Get(headerContentType))
				assert.Equal(t, consumerToken, r.Header.Get(headerConsumerToken))
				body, err := ioutil.ReadAll(r.Body)
				assert.Nil(t, err)
				assert.Equal(t, messagePayload, string(body))
				if s == "consumer-0" {
					rw.WriteHeader(http.StatusBadGateway)
				} else {
					rw.WriteHeader(http.StatusNoContent)
				}
			}
		}
		wg.Add(len(consumers))
		dispatcher := NewMessageDispatcher(dataAccessor.GetDeliveryJobRepository(), dataAccessor.GetConsumerRepository(), configuration, configuration)
		msg, _ := data.NewMessage(channel, producer, messagePayload, contentType)
		err := dataAccessor.GetMessageRepository().Create(msg)
		assert.Nil(t, err)
		dispatcher.Dispatch(msg)
		wg.Wait()
		jobs, _, err := dataAccessor.GetDeliveryJobRepository().GetJobsForMessage(msg, data.NewPagination(nil, nil))
		assert.Nil(t, err)
		assert.Equal(t, len(consumers), len(jobs))
		for _, job := range jobs {
			if job.Listener.ConsumerID == consumerIDPrefix+"0" {
				assert.Equal(t, data.JobQueued, job.Status)
			} else {
				assert.Equal(t, data.JobDelivered, job.Status)
			}
		}
	})
	t.Run("t", func(t *testing.T) { t.Parallel() })

}
