package dispatcher

import (
	"bytes"
	"errors"
	"log"
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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

var (
	migrationLocation, _ = filepath.Abs("../migration/sqls/")
	defaultMigrationConf = &storage.MigrationConfig{MigrationEnabled: true, MigrationSource: "file://" + migrationLocation}
	dataAccessor         storage.DataAccessor
	channel              *data.Channel
	producer             *data.Producer
	consumers            []*data.Consumer
	callbackURL          *url.URL
	configuration        *config.Config
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
	}
	dataAccessor.Close()
}

func SetupTestFixture() {
	callbackURL, _ = url.Parse("https://imytech.net/")
	channel, _ = data.NewChannel("dispatch-test-channel", "token")
	channel.QuickFix()
	channel, _ = dataAccessor.GetChannelRepository().Store(channel)
	producer, _ = data.NewProducer("dispatch-test-producer", "token")
	producer.QuickFix()
	producer, _ = dataAccessor.GetProducerRepository().Store(producer)
	testConsumers := 30
	consumers = make([]*data.Consumer, 0, testConsumers)
	for i := 0; i < testConsumers; i++ {
		consumer, _ := data.NewConsumer(channel, "test-consumer-for-dispatcher-"+strconv.Itoa(i), "consumerToken", callbackURL)
		consumer.QuickFix()
		dataAccessor.GetConsumerRepository().Store(consumer)
		consumers = append(consumers, consumer)
	}
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
		var buf bytes.Buffer
		log.SetOutput(&buf)
		oldDequeue := asyncDequeueToWorker
		defer func() {
			asyncDequeueToWorker = oldDequeue
			log.SetOutput(os.Stderr)
		}()
		var wg sync.WaitGroup
		asyncDequeueToWorker = func(msgDispatcher *MessageDispatcherImpl) {
			oldDequeue(msgDispatcher)
			wg.Done()
		}
		wg.Add(len(consumers))
		dispatcher := NewMessageDispatcher(dataAccessor.GetDeliveryJobRepository(), dataAccessor.GetConsumerRepository(), configuration, configuration)
		msg, _ := data.NewMessage(channel, producer, "payload", "type")
		err := dataAccessor.GetMessageRepository().Create(msg)
		assert.Nil(t, err)
		dispatcher.Dispatch(msg)
		wg.Wait()
	})
	t.Run("t", func(t *testing.T) { t.Parallel() })

}
