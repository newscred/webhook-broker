package dispatcher

import (
	"bytes"
	"context"
	"errors"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/julienschmidt/httprouter"
	"github.com/newscred/webhook-broker/config"
	configmocks "github.com/newscred/webhook-broker/config/mocks"
	"github.com/newscred/webhook-broker/storage"
	"github.com/newscred/webhook-broker/storage/data"
	storagemocks "github.com/newscred/webhook-broker/storage/mocks"
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
	counter              = NewMetricsContainer()
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
	if netErr != nil {
		log.Print(netErr)
		err = netErr
	} else {
		defer ln.Close()
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
		log.Fatal().Msg("could not find port to start test consumer service")
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
				log.Print(serverListenErr)
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
			consumerType := config.PushConsumerStr
			if i >= 15 {
				consumerType = config.PullConsumerStr
			}
			consumer, _ := data.NewConsumer(channel, consumerIDPrefix+strconv.Itoa(i), consumerToken, callbackURL, consumerType)
			consumer.QuickFix()
			dataAccessor.GetConsumerRepository().Store(consumer)
			consumers = append(consumers, consumer)
		}
		wg.Done()
	}()
	wg.Wait()
}

func setupTestJob(consumer *data.Consumer) (*data.DeliveryJob, error) {
	message, err := data.NewMessage(channel, producer, "payload", "type", data.HeadersMap{})
	if err != nil {
		log.Fatal()
		return nil, err
	}
	dataAccessor.GetMessageRepository().Create(message)
	job, err := data.NewDeliveryJob(message, consumer)
	if err != nil {
		log.Fatal()
		return nil, err
	}
	dataAccessor.GetDeliveryJobRepository().DispatchMessage(message, job)
	return job, nil
}

func getMockedBrokerConfig(workerEnabled ...interface{}) *configmocks.BrokerConfig {
	mockedConfig := new(configmocks.BrokerConfig)
	mockedConfig.On("GetMaxMessageQueueSize").Return(uint(100))
	mockedConfig.On("GetMaxWorkers").Return(uint(5))
	if len(workerEnabled) <= 0 {
		mockedConfig.On("IsRecoveryWorkersEnabled").Return(false)
	} else {
		mockedConfig.On("IsRecoveryWorkersEnabled").Return(workerEnabled[0])
	}
	mockedConfig.On("GetRationalDelay").Return(100 * time.Millisecond)
	mockedConfig.On("GetMaxRetry").Return(uint8(5))
	if len(workerEnabled) <= 1 {
		mockedConfig.On("GetRetryBackoffDelays").Return([]time.Duration{5 * time.Second})
	} else {
		mockedConfig.On("GetRetryBackoffDelays").Return(workerEnabled[1])
	}
	return mockedConfig
}

func getMockedConsumerConfig() *configmocks.ConsumerConnectionConfig {
	mockedConfig := new(configmocks.ConsumerConnectionConfig)
	mockedConfig.On("GetConnectionTimeout").Return(100 * time.Millisecond)
	return mockedConfig
}

func getCompleteDispatcherConfiguration(msgRepo storage.MessageRepository, djRepo storage.DeliveryJobRepository, consumerRepo storage.ConsumerRepository, brokerConfig config.BrokerConfig, consumerConfig config.ConsumerConnectionConfig, lockRepo storage.LockRepository) *Configuration {
	return &Configuration{
		DeliveryJobRepo:          djRepo,
		ConsumerRepo:             consumerRepo,
		BrokerConfig:             brokerConfig,
		ConsumerConnectionConfig: consumerConfig,
		LockRepo:                 lockRepo,
		MsgRepo:                  msgRepo,
		MetricsCollector:         counter,
	}
}

func getDispatcherConfiguration(djRepo storage.DeliveryJobRepository, consumerRepo storage.ConsumerRepository, brokerConfig config.BrokerConfig, consumerConfig config.ConsumerConnectionConfig, lockRepo storage.LockRepository) *Configuration {
	mockMsgRepo := new(storagemocks.MessageRepository)
	return getCompleteDispatcherConfiguration(mockMsgRepo, djRepo, consumerRepo, brokerConfig, consumerConfig, lockRepo)
}

func TestNewMessageDispatcher(t *testing.T) {
	deferFunc := func() {
		if r := recover(); r != panicString {
			t.Fail()
		}
	}
	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		mockBrokerConfig := getMockedBrokerConfig(true)
		mockConsumerConfig := getMockedConsumerConfig()
		mRepo := new(storagemocks.DeliveryJobRepository)
		cRepo := new(storagemocks.ConsumerRepository)
		lockRepo := new(storagemocks.LockRepository)
		dispatcher := NewMessageDispatcher(getDispatcherConfiguration(mRepo, cRepo, mockBrokerConfig, mockConsumerConfig, lockRepo))
		assert.NotNil(t, dispatcher)
		time.Sleep(110 * time.Millisecond)
		dispatcher.Stop()
	})
	t.Run("MsgRepoNil", func(t *testing.T) {
		t.Parallel()
		defer deferFunc()
		mockBrokerConfig := new(configmocks.BrokerConfig)
		mockConsumerConfig := new(configmocks.ConsumerConnectionConfig)
		cRepo := new(storagemocks.ConsumerRepository)
		lockRepo := new(storagemocks.LockRepository)
		assert.NotNil(t, NewMessageDispatcher(getDispatcherConfiguration(nil, cRepo, mockBrokerConfig, mockConsumerConfig, lockRepo)))
	})
	t.Run("ConsumerRepoNil", func(t *testing.T) {
		t.Parallel()
		defer deferFunc()
		mockBrokerConfig := new(configmocks.BrokerConfig)
		mockConsumerConfig := new(configmocks.ConsumerConnectionConfig)
		mRepo := new(storagemocks.DeliveryJobRepository)
		lockRepo := new(storagemocks.LockRepository)
		assert.NotNil(t, NewMessageDispatcher(getDispatcherConfiguration(mRepo, nil, mockBrokerConfig, mockConsumerConfig, lockRepo)))
	})
	t.Run("BrokerConfigNil", func(t *testing.T) {
		t.Parallel()
		defer deferFunc()
		mockConsumerConfig := new(configmocks.ConsumerConnectionConfig)
		mRepo := new(storagemocks.DeliveryJobRepository)
		cRepo := new(storagemocks.ConsumerRepository)
		lockRepo := new(storagemocks.LockRepository)
		assert.NotNil(t, NewMessageDispatcher(getDispatcherConfiguration(mRepo, cRepo, nil, mockConsumerConfig, lockRepo)))
	})
	t.Run("ConsumerConnectionConfigNil", func(t *testing.T) {
		t.Parallel()
		defer deferFunc()
		mockBrokerConfig := new(configmocks.BrokerConfig)
		mRepo := new(storagemocks.DeliveryJobRepository)
		cRepo := new(storagemocks.ConsumerRepository)
		lockRepo := new(storagemocks.LockRepository)
		assert.NotNil(t, NewMessageDispatcher(getDispatcherConfiguration(mRepo, cRepo, mockBrokerConfig, nil, lockRepo)))
	})
	t.Run("DispatcherLockRepoNil", func(t *testing.T) {
		t.Parallel()
		defer deferFunc()
		mockBrokerConfig := getMockedBrokerConfig()
		mockConsumerConfig := getMockedConsumerConfig()
		mRepo := new(storagemocks.DeliveryJobRepository)
		cRepo := new(storagemocks.ConsumerRepository)
		assert.NotNil(t, NewMessageDispatcher(getDispatcherConfiguration(mRepo, cRepo, mockBrokerConfig, mockConsumerConfig, nil)))
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
		lockRepo := new(storagemocks.LockRepository)
		dispatcher := NewMessageDispatcher(getDispatcherConfiguration(mRepo, cRepo, mockBrokerConfig, mockConsumerConfig, lockRepo))
		assert.NotNil(t, dispatcher)
		dispatcher.Dispatch(nil)
	})
	t.Run("InvalidMessage", func(t *testing.T) {
		t.Parallel()
		mRepo := new(storagemocks.DeliveryJobRepository)
		cRepo := new(storagemocks.ConsumerRepository)
		mockBrokerConfig := getMockedBrokerConfig()
		mockConsumerConfig := getMockedConsumerConfig()
		lockRepo := new(storagemocks.LockRepository)
		dispatcher := NewMessageDispatcher(getDispatcherConfiguration(mRepo, cRepo, mockBrokerConfig, mockConsumerConfig, lockRepo))
		assert.NotNil(t, dispatcher)
		msg, _ := data.NewMessage(channel, producer, "payload", "type", data.HeadersMap{})
		msg.ReceivedAt = time.Time{}
		dispatcher.Dispatch(msg)
	})
	t.Run("ListError", func(t *testing.T) {
		var buf bytes.Buffer
		oldLogger := log.Logger
		log.Logger = log.Output(&buf)
		defer func() {
			log.Logger = oldLogger
		}()
		mRepo := new(storagemocks.DeliveryJobRepository)
		cRepo := new(storagemocks.ConsumerRepository)
		mockBrokerConfig := getMockedBrokerConfig()
		mockConsumerConfig := getMockedConsumerConfig()
		lockRepo := new(storagemocks.LockRepository)
		dispatcher := NewMessageDispatcher(getDispatcherConfiguration(mRepo, cRepo, mockBrokerConfig, mockConsumerConfig, lockRepo))
		assert.NotNil(t, dispatcher)
		msg, _ := data.NewMessage(channel, producer, "payload", "type", data.HeadersMap{})
		err := dataAccessor.GetMessageRepository().Create(msg)
		assert.Nil(t, err)
		expectedErr := errors.New("error on list call")
		cRepo.On("GetList", channel.ChannelID, mock.Anything).Return(make([]*data.Consumer, 0), data.NewPagination(nil, nil), expectedErr)
		dispatcher.Dispatch(msg)
		assert.Contains(t, buf.String(), expectedErr.Error())
	})
	t.Run("Success", func(t *testing.T) {
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
		pushConsumerCount := 0
		for index := 0; index < len(consumers); index++ {
			if consumers[index].Type == data.PushConsumer {
				pushConsumerCount++
			}
		}

		msg, _ := data.NewMessage(channel, producer, messagePayload, contentType, data.HeadersMap{})
		err := dataAccessor.GetMessageRepository().Create(msg)
		assert.Nil(t, err)

		for index := 0; index < len(consumers); index++ {
			consumerHandler["consumer-"+strconv.Itoa(index)] = func(s string, rw http.ResponseWriter, r *http.Request) {
				// check content body and type
				assert.Equal(t, contentType, r.Header.Get(headerContentType))
				assert.Equal(t, consumerToken, r.Header.Get(headerConsumerToken))
				assert.Equal(t, msg.MessageID, r.Header.Get(headerMessageID))
				assert.Greater(t, len(r.Header.Get(headerRequestID)), 12)
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
		wg.Add(pushConsumerCount)
		brokerConf := getMockedBrokerConfig()
		dispatcher := NewMessageDispatcher(getDispatcherConfiguration(dataAccessor.GetDeliveryJobRepository(), dataAccessor.GetConsumerRepository(), brokerConf, configuration, dataAccessor.GetLockRepository()))
		dispatcher.Dispatch(msg)
		wg.Wait()
		jobs, _, err := dataAccessor.GetDeliveryJobRepository().GetJobsForMessage(msg, data.NewPagination(nil, nil))
		assert.Nil(t, err)
		assert.Equal(t, len(consumers), len(jobs))

		getConsumerIndex := func(consumerID string) int {
			parts := strings.Split(consumerID, "-")
			ind, _ := strconv.Atoi(parts[len(parts)-1])
			return ind
		}
		for _, job := range jobs {
			consumerIndex := getConsumerIndex(job.Listener.ConsumerID)
			if job.Listener.ConsumerID == consumerIDPrefix+"0" || consumerIndex >= 15 {
				assert.Equal(t, data.JobQueued, job.Status)
			} else {
				assert.Equal(t, data.JobDelivered, job.Status)
			}
		}
	})
	t.Run("t", func(t *testing.T) { t.Parallel() })

}

func TestInLockRun(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		mockLockRepo := new(storagemocks.LockRepository)
		mockLockable := new(storagemocks.Lockable)
		lockID := "testlocking"
		mockLockable.On("GetLockID").Return(lockID)
		lockMatcher := func(lock *data.Lock) bool {
			return lock.LockID == lockID
		}
		mockLockRepo.On("TryLock", mock.MatchedBy(lockMatcher)).Return(nil)
		mockLockRepo.On("ReleaseLock", mock.MatchedBy(lockMatcher)).Return(nil)
		err := inLockRun(mockLockRepo, mockLockable, func() error {
			return nil
		})
		assert.Nil(t, err)
		mockLockRepo.AssertExpectations(t)
		mockLockable.AssertExpectations(t)
	})
	t.Run("AlreadyLocked", func(t *testing.T) {
		t.Parallel()
		mockLockRepo := new(storagemocks.LockRepository)
		mockLockable := new(storagemocks.Lockable)
		lockID := "testlocking"
		mockLockable.On("GetLockID").Return(lockID)
		lockMatcher := func(lock *data.Lock) bool {
			return lock.LockID == lockID
		}
		mockLockRepo.On("TryLock", mock.MatchedBy(lockMatcher)).Return(storage.ErrAlreadyLocked)
		err := inLockRun(mockLockRepo, mockLockable, func() error {
			return nil
		})
		assert.Nil(t, err)
		mockLockRepo.AssertExpectations(t)
		mockLockable.AssertExpectations(t)
	})
	t.Run("OtherLockError", func(t *testing.T) {
		t.Parallel()
		mockLockRepo := new(storagemocks.LockRepository)
		mockLockable := new(storagemocks.Lockable)
		lockID := "testlocking"
		mockLockable.On("GetLockID").Return(lockID)
		lockMatcher := func(lock *data.Lock) bool {
			return lock.LockID == lockID
		}
		expectedErr := errors.New("unknown error")
		mockLockRepo.On("TryLock", mock.MatchedBy(lockMatcher)).Return(expectedErr)
		err := inLockRun(mockLockRepo, mockLockable, func() error {
			return nil
		})
		assert.NotNil(t, err)
		assert.Equal(t, expectedErr, err)
		mockLockRepo.AssertExpectations(t)
		mockLockable.AssertExpectations(t)
	})
	t.Run("RunError", func(t *testing.T) {
		t.Parallel()
		mockLockRepo := new(storagemocks.LockRepository)
		mockLockable := new(storagemocks.Lockable)
		lockID := "testlocking"
		mockLockable.On("GetLockID").Return(lockID)
		lockMatcher := func(lock *data.Lock) bool {
			return lock.LockID == lockID
		}
		expectedErr := errors.New("unknown error")
		mockLockRepo.On("TryLock", mock.MatchedBy(lockMatcher)).Return(nil)
		mockLockRepo.On("ReleaseLock", mock.MatchedBy(lockMatcher)).Return(nil)
		err := inLockRun(mockLockRepo, mockLockable, func() error {
			return expectedErr
		})
		assert.NotNil(t, err)
		assert.Equal(t, expectedErr, err)
		mockLockRepo.AssertExpectations(t)
		mockLockable.AssertExpectations(t)
	})
}

func TestRecoverMessagesNotYetDispatched(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		messagePayload := `{"key": "Custom JSON"}`
		contentType := "application/json"
		brokerConf := getMockedBrokerConfig()
		dispatcher := NewMessageDispatcher(getCompleteDispatcherConfiguration(dataAccessor.GetMessageRepository(), dataAccessor.GetDeliveryJobRepository(), dataAccessor.GetConsumerRepository(), brokerConf, configuration, dataAccessor.GetLockRepository()))
		msg, _ := data.NewMessage(channel, producer, messagePayload, contentType, data.HeadersMap{})
		msg.ReceivedAt = msg.ReceivedAt.Add(-5 * time.Second)
		err := dataAccessor.GetMessageRepository().Create(msg)
		assert.Nil(t, err)
		recoverMessagesNotYetDispatched(dispatcher.(*MessageDispatcherImpl))
		nMsg, _ := dataAccessor.GetMessageRepository().GetByID(msg.ID.String())
		assert.Equal(t, data.MsgStatusDispatched, nMsg.Status)
	})
	t.Run("LogError", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		oldLogger := log.Logger
		log.Logger = log.Output(&buf)
		defer func() { log.Logger = oldLogger }()
		errString := "sample select error"
		expectedErr := errors.New(errString)
		messagePayload := `{"key": "Custom JSON"}`
		contentType := "application/json"
		brokerConf := getMockedBrokerConfig()
		mockLockRepo := new(storagemocks.LockRepository)
		mockLockRepo.On("TimeoutLocks", mock.Anything).Return(nil)
		mockLockRepo.On("TryLock", mock.Anything).Return(expectedErr)
		dispatcher := NewMessageDispatcher(getCompleteDispatcherConfiguration(dataAccessor.GetMessageRepository(), dataAccessor.GetDeliveryJobRepository(), dataAccessor.GetConsumerRepository(), brokerConf, configuration, mockLockRepo))
		msg, _ := data.NewMessage(channel, producer, messagePayload, contentType, data.HeadersMap{})
		msg.ReceivedAt = msg.ReceivedAt.Add(-5 * time.Second)
		err := dataAccessor.GetMessageRepository().Create(msg)
		assert.Nil(t, err)
		recoverMessagesNotYetDispatched(dispatcher.(*MessageDispatcherImpl))
		nMsg, _ := dataAccessor.GetMessageRepository().GetByID(msg.ID.String())
		assert.Equal(t, data.MsgStatusAcknowledged, nMsg.Status)
		assert.Contains(t, buf.String(), msg.MessageID)
		assert.Contains(t, buf.String(), errString)
	})
}

func TestJobWorkers(t *testing.T) {
	messagePayload := `{"key": "Custom JSON"}`
	contentType := "application/json"
	brokerConf := getMockedBrokerConfig()
	outerDispatcher := NewMessageDispatcher(getCompleteDispatcherConfiguration(dataAccessor.GetMessageRepository(), dataAccessor.GetDeliveryJobRepository(), dataAccessor.GetConsumerRepository(), brokerConf, configuration, dataAccessor.GetLockRepository()))
	msg, _ := data.NewMessage(channel, producer, messagePayload, contentType, data.HeadersMap{})
	msg.ReceivedAt = msg.ReceivedAt.Add(-5 * time.Second)
	err := dataAccessor.GetMessageRepository().Create(msg)
	assert.Nil(t, err)
	msgDispatcher := outerDispatcher.(*MessageDispatcherImpl)
	jobs, err := createJobs(msgDispatcher, msg)
	if err == nil {
		err = msgDispatcher.djRepo.DispatchMessage(msg, jobs...)
	}
	jobs, _, err = dataAccessor.GetDeliveryJobRepository().GetJobsForMessage(msg, data.NewPagination(nil, nil))
	assert.Nil(t, err)
	inflightJob := jobs[0]
	err = dataAccessor.GetDeliveryJobRepository().MarkJobInflight(inflightJob)
	assert.Nil(t, err)
	db, _ := storage.GetConnectionPool(configuration, nil, configuration)
	db.Exec("UPDATE job SET statusChangedAt = ? WHERE messageId = ?", time.Now().Add(-1*time.Hour), msg.ID)
	// Did not make them parallel since states are inter-dependent
	t.Run("Error", func(t *testing.T) {
		var buf bytes.Buffer
		oldLogger := log.Logger
		log.Logger = log.Output(&buf)
		defer func() { log.Logger = oldLogger }()
		errString := "sample select error"
		expectedErr := errors.New(errString)
		brokerConf := getMockedBrokerConfig()
		mockLockRepo := new(storagemocks.LockRepository)
		mockLockRepo.On("TimeoutLocks", mock.Anything).Return(nil)
		mockLockRepo.On("TryLock", mock.Anything).Return(expectedErr)
		dispatcher := NewMessageDispatcher(getCompleteDispatcherConfiguration(dataAccessor.GetMessageRepository(), dataAccessor.GetDeliveryJobRepository(), dataAccessor.GetConsumerRepository(), brokerConf, configuration, mockLockRepo))
		impl := dispatcher.(*MessageDispatcherImpl)
		recoverJobsFromLongInflight(impl)
		retryQueuedJobs(impl)
		assert.Nil(t, err)
	})
	t.Run("SuccessRecoverInflight", func(t *testing.T) {
		brokerConf := getMockedBrokerConfig(false, []time.Duration{5 * time.Second, 10 * time.Second, 20 * time.Second})
		dispatcher := NewMessageDispatcher(getCompleteDispatcherConfiguration(dataAccessor.GetMessageRepository(), dataAccessor.GetDeliveryJobRepository(), dataAccessor.GetConsumerRepository(), brokerConf, configuration, dataAccessor.GetLockRepository()))
		impl := dispatcher.(*MessageDispatcherImpl)
		recoverJobsFromLongInflight(impl)
		nJob, err := msgDispatcher.djRepo.GetByID(inflightJob.ID.String())
		assert.Nil(t, err)
		assert.Equal(t, data.JobQueued, nJob.Status)
		assert.Equal(t, uint(1), nJob.RetryAttemptCount)
	})
	t.Run("SuccessRetry", func(t *testing.T) {
		dispatcher := NewMessageDispatcher(getCompleteDispatcherConfiguration(dataAccessor.GetMessageRepository(), dataAccessor.GetDeliveryJobRepository(), dataAccessor.GetConsumerRepository(), brokerConf, configuration, dataAccessor.GetLockRepository()))
		impl := dispatcher.(*MessageDispatcherImpl)
		impl.stopTimeout = 4 * time.Millisecond
		impl.rationalDelay = 5 * time.Millisecond
		retryQueuedJobs(impl)
		// Try to check for count 20 time to ensure it works in a slower CPU and we are not waiting unnecessarily
		for iter := 0; iter < 20; iter++ {
			time.Sleep(200 * time.Millisecond)
			count := 0
			nJobs, _, err := dataAccessor.GetDeliveryJobRepository().GetJobsForMessage(msg, data.NewPagination(nil, nil))
			assert.Nil(t, err)
			for _, job := range nJobs {
				if job.ID == inflightJob.ID {
					continue
				}
				if job.Status != data.JobQueued {
					count++
				}
			}
			if count < 20 {
				continue
			} else {
				// This assertion is done to avoid the impact of time on test assertion
				assert.GreaterOrEqual(t, count, 20)
				break
			}
		}
	})
	t.Run("SuccessRecoverLongInflightWithTimeout", func(t *testing.T) {
		dispatcher := NewMessageDispatcher(getCompleteDispatcherConfiguration(dataAccessor.GetMessageRepository(), dataAccessor.GetDeliveryJobRepository(), dataAccessor.GetConsumerRepository(), brokerConf, configuration, dataAccessor.GetLockRepository()))
		impl := dispatcher.(*MessageDispatcherImpl)

		var inflightPushJob *data.DeliveryJob = nil
		var inflightPullJob *data.DeliveryJob = nil

		var getJobs = func() {
			for _, job := range jobs {
				if job.Listener.Type == data.PushConsumer {
					inflightPushJob = job
				}
				if job.Listener.Type == data.PullConsumer {
					inflightPullJob = job
				}
			}
		}
		var resetJobs = func() {
			// might be necessary to reset job states because of previous tests
			inflightPushJob.RetryAttemptCount = 0
			inflightPullJob.RetryAttemptCount = 0
			_, err := db.Exec("UPDATE job SET status = ?, retryAttemptCount = ? WHERE id = ?", data.JobQueued, inflightPushJob.RetryAttemptCount, inflightPushJob.ID)
			assert.Nil(t, err)
			_, err = db.Exec("UPDATE job SET status = ?, retryAttemptCount = ? WHERE id = ?", data.JobQueued, inflightPullJob.RetryAttemptCount, inflightPullJob.ID)
			assert.Nil(t, err)

			inflightPullJob.Status = data.JobQueued
			inflightPushJob.Status = data.JobQueued
		}

		getJobs()

		var runRecoverLongInflightTest = func(t *testing.T, timeout uint, lastStatusChanged time.Duration, shouldRetryPushJob, shouldRetryPullJob bool) {
			resetJobs()

			pushJobRetryCount, pullJobRetryCount := 0, 0
			inflightPushJob.IncrementalTimeout = timeout
			inflightPullJob.IncrementalTimeout = timeout
			err = dataAccessor.GetDeliveryJobRepository().MarkJobInflight(inflightPushJob)
			assert.Nil(t, err)
			err = dataAccessor.GetDeliveryJobRepository().MarkJobInflight(inflightPullJob)
			assert.Nil(t, err)
			db.Exec("UPDATE job SET statusChangedAt = ? WHERE id = ?", time.Now().Add(-lastStatusChanged), inflightPushJob.ID)
			db.Exec("UPDATE job SET statusChangedAt = ? WHERE id = ?", time.Now().Add(-lastStatusChanged), inflightPullJob.ID)

			recoverJobsFromLongInflight(impl)

			nJob, err := msgDispatcher.djRepo.GetByID(inflightPushJob.ID.String())
			assert.Nil(t, err)
			if shouldRetryPushJob {
				assert.Equal(t, data.JobQueued, nJob.Status)
				pushJobRetryCount++
			}
			assert.Equal(t, uint(pushJobRetryCount), nJob.RetryAttemptCount)
			inflightPushJob = nJob
			nJob, err = msgDispatcher.djRepo.GetByID(inflightPullJob.ID.String())
			assert.Nil(t, err)
			if shouldRetryPullJob {
				assert.Equal(t, data.JobQueued, nJob.Status)
				pullJobRetryCount++
			}
			assert.Equal(t, uint(pullJobRetryCount), nJob.RetryAttemptCount)
			inflightPullJob = nJob
		}

		cases := []struct {
			name               string
			jobTimeout         time.Duration
			lastStatusChanged  time.Duration
			shouldRetryPushJob bool
			shouldRetryPullJob bool
		}{
			{name: "StatusChangedLongAgo", jobTimeout: 10 * 60, lastStatusChanged: time.Hour, shouldRetryPullJob: true, shouldRetryPushJob: true},
			{name: "RequePushOnly", jobTimeout: 4.6 * 60, lastStatusChanged: 5 * time.Minute, shouldRetryPullJob: false, shouldRetryPushJob: true},
			{name: "RequeBoth", jobTimeout: 10 * 60, lastStatusChanged: 11 * time.Minute, shouldRetryPullJob: true, shouldRetryPushJob: true},
			{name: "RequeNone", jobTimeout: 10 * 60, lastStatusChanged: 10 * time.Second, shouldRetryPullJob: false, shouldRetryPushJob: false},
		}

		for _, test := range cases {
			t.Run(test.name, func(t *testing.T) {
				runRecoverLongInflightTest(t, uint(test.jobTimeout), test.lastStatusChanged, test.shouldRetryPushJob, test.shouldRetryPullJob)
			})
		}
	})
	t.Run("ConsumerAquireLockForRetryQueueWhenNecessary", func(t *testing.T) {
		dispatcher := NewMessageDispatcher(getCompleteDispatcherConfiguration(dataAccessor.GetMessageRepository(), dataAccessor.GetDeliveryJobRepository(), dataAccessor.GetConsumerRepository(), brokerConf, configuration, dataAccessor.GetLockRepository()))
		impl := dispatcher.(*MessageDispatcherImpl)

		_, err := db.Exec("UPDATE job SET status = ?", data.JobDelivered)
		assert.Nil(t, err)
		defer func() {
			_, err := db.Exec("UPDATE job SET status = ?", data.JobQueued)
			assert.Nil(t, err)
		}()

		pushConsumer := consumers[0]
		pullConsumer := consumers[len(consumers)-1]

		pushJob, err := setupTestJob(pushConsumer)
		assert.Nil(t, err)
		pullJob, err := setupTestJob(pullConsumer)
		assert.Nil(t, err)
		assert.Equal(t, data.PushConsumer, pushConsumer.Type)
		assert.Equal(t, data.PullConsumer, pullConsumer.Type)

		inLockCallCount := 0
		oldInLockRun := inLockRun
		inLockRun = func(lockRepo storage.LockRepository, lockable data.Lockable, run func() error) (err error) {
			ret := oldInLockRun(lockRepo, lockable, run)
			inLockCallCount++
			return ret
		}
		defer func() { inLockRun = oldInLockRun }()

		current_time := time.Now()
		_, err = db.Exec("UPDATE job SET status = ?, earliestNextAttemptAt = ?, retryAttemptCount = ? WHERE id = ?", data.JobQueued, current_time.Add(-110*time.Millisecond), 0, pushJob.ID)
		assert.Nil(t, err)
		_, err = db.Exec("UPDATE job SET status = ?, earliestNextAttemptAt = ?, retryAttemptCount = ? WHERE id = ?", data.JobQueued, current_time.Add(-110*time.Millisecond), 0, pullJob.ID)
		assert.Nil(t, err)
		retryQueuedJobs(impl)
		assert.Equal(t, 1, inLockCallCount) // only got called for push job

		current_time = time.Now()
		_, err = db.Exec("UPDATE job SET status = ?, earliestNextAttemptAt = ?, retryAttemptCount = ? WHERE id = ?", data.JobQueued, current_time.Add(-110*time.Millisecond), msgDispatcher.brokerConfig.GetMaxRetry()+1, pushJob.ID)
		assert.Nil(t, err)
		_, err = db.Exec("UPDATE job SET status = ?, earliestNextAttemptAt = ?, retryAttemptCount = ? WHERE id = ?", data.JobQueued, current_time.Add(-110*time.Millisecond), msgDispatcher.brokerConfig.GetMaxRetry()+1, pullJob.ID)
		assert.Nil(t, err)

		retryQueuedJobs(impl)

		assert.Equal(t, 3, inLockCallCount) // got called for both job
		updatedJob, err := impl.djRepo.GetByID(pullJob.ID.String())
		assert.Nil(t, err)
		assert.Equal(t, data.JobDead, updatedJob.Status)
	})
}
