package dispatcher

import (
	"testing"
	"time"

	configmocks "github.com/imyousuf/webhook-broker/config/mocks"
	storagemocks "github.com/imyousuf/webhook-broker/storage/mocks"
	"github.com/stretchr/testify/assert"
)

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
		mRepo := new(storagemocks.MessageRepository)
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
		mRepo := new(storagemocks.MessageRepository)
		assert.NotNil(t, NewMessageDispatcher(mRepo, nil, mockBrokerConfig, mockConsumerConfig))
	})
	t.Run("BrokerConfigNil", func(t *testing.T) {
		t.Parallel()
		defer deferFunc()
		mockConsumerConfig := new(configmocks.ConsumerConnectionConfig)
		mRepo := new(storagemocks.MessageRepository)
		cRepo := new(storagemocks.ConsumerRepository)
		assert.NotNil(t, NewMessageDispatcher(mRepo, cRepo, nil, mockConsumerConfig))
	})
	t.Run("ConsumerConnectionConfigNil", func(t *testing.T) {
		t.Parallel()
		defer deferFunc()
		mockBrokerConfig := new(configmocks.BrokerConfig)
		mRepo := new(storagemocks.MessageRepository)
		cRepo := new(storagemocks.ConsumerRepository)
		assert.NotNil(t, NewMessageDispatcher(mRepo, cRepo, mockBrokerConfig, nil))
	})
}

func TestMessageDispatcherImpl_Dispatch(t *testing.T) {
	t.Parallel()
	mRepo := new(storagemocks.MessageRepository)
	cRepo := new(storagemocks.ConsumerRepository)
	mockBrokerConfig := getMockedBrokerConfig()
	mockConsumerConfig := getMockedConsumerConfig()
	dispatcher := NewMessageDispatcher(mRepo, cRepo, mockBrokerConfig, mockConsumerConfig)
	assert.NotNil(t, dispatcher)
	dispatcher.Dispatch(nil)
	mockBrokerConfig.AssertExpectations(t)
}
