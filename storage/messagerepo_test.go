package storage

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/imyousuf/webhook-broker/storage/data"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

const (
	samplePayload      = "some payload"
	sampleContentType  = "a content type"
	duplicateMessageID = "a-duplicate-message-id"
)

var (
	producer1 *data.Producer
)

func SetupForMessageTests() {
	producerRepo := NewProducerRepository(testDB)
	producer, _ := data.NewProducer("producer1-for-message", successfulGetTestToken)
	producer.QuickFix()
	producer1, _ = producerRepo.Store(producer)
}

func getMessageRepository() MessageRepository {
	return NewMessageRepository(testDB, NewChannelRepository(testDB), NewProducerRepository(testDB))
}

func TestMessageGetByID(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		repo := getMessageRepository()
		msg, err := data.NewMessage(channel1, producer1, samplePayload, sampleContentType)
		assert.Nil(t, err)
		assert.Nil(t, repo.Create(msg))
		rMsg, err := repo.GetByID(msg.ID.String())
		assert.Nil(t, err)
		assert.NotNil(t, rMsg)
		assert.Equal(t, channel1.ID, msg.BroadcastedTo.ID)
		assert.Equal(t, producer1.ID, msg.ProducedBy.ID)
		assert.Equal(t, samplePayload, msg.Payload)
		assert.Equal(t, sampleContentType, msg.ContentType)
	})
	t.Run("Fail", func(t *testing.T) {
		t.Parallel()
		repo := getMessageRepository()
		_, err := repo.GetByID("non-existing-id")
		assert.NotNil(t, err)
	})
}

func TestMessageGetCreate(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		repo := getMessageRepository()
		msg, err := data.NewMessage(channel1, producer1, samplePayload, sampleContentType)
		assert.Nil(t, err)
		_, err = repo.Get(channel1.ChannelID, msg.MessageID)
		assert.NotNil(t, err)
		assert.Nil(t, repo.Create(msg))
		var readMessage *data.Message
		readMessage, err = repo.Get(channel1.ChannelID, msg.MessageID)
		assert.Nil(t, err)
		assert.Equal(t, msg.MessageID, readMessage.MessageID)
		assert.Equal(t, msg.ID, readMessage.ID)
		assert.Equal(t, channel1.ChannelID, readMessage.BroadcastedTo.ChannelID)
		assert.Equal(t, producer1.ProducerID, readMessage.ProducedBy.ProducerID)
		assert.Equal(t, msg.ContentType, readMessage.ContentType)
		assert.Equal(t, msg.Payload, readMessage.Payload)
		assert.Equal(t, msg.Priority, readMessage.Priority)
		assert.Equal(t, msg.Status, readMessage.Status)
		assert.True(t, msg.ReceivedAt.Equal(readMessage.ReceivedAt))
		assert.True(t, msg.OutboxedAt.Equal(readMessage.OutboxedAt))
		assert.True(t, msg.CreatedAt.Equal(readMessage.CreatedAt))
		assert.True(t, msg.UpdatedAt.Equal(readMessage.UpdatedAt))
	})
	t.Run("InvalidMsgState", func(t *testing.T) {
		t.Parallel()
		msg, err := data.NewMessage(channel1, producer1, samplePayload, sampleContentType)
		assert.Nil(t, err)
		msg.MessageID = ""
		repo := getMessageRepository()
		assert.NotNil(t, repo.Create(msg))
	})
	t.Run("NonExistingChannel", func(t *testing.T) {
		t.Parallel()
		channel, _ := data.NewChannel("testchannel4msgtest", "token")
		channel.QuickFix()
		msg, err := data.NewMessage(channel, producer1, samplePayload, sampleContentType)
		assert.Nil(t, err)
		repo := getMessageRepository()
		err = repo.Create(msg)
		assert.NotNil(t, err)
		_, err = repo.Get(channel.ChannelID, msg.MessageID)
		assert.NotNil(t, err)
		assert.Equal(t, sql.ErrNoRows, err)
	})
	t.Run("NonExistingProducer", func(t *testing.T) {
		t.Parallel()
		producer, _ := data.NewProducer("testproducer4invalidprodinmsgtest", "testtoken")
		producer.QuickFix()
		msg, err := data.NewMessage(channel1, producer, samplePayload, sampleContentType)
		assert.Nil(t, err)
		repo := getMessageRepository()
		err = repo.Create(msg)
		assert.NotNil(t, err)
		_, err = repo.Get(channel1.ChannelID, msg.MessageID)
		assert.NotNil(t, err)
		assert.Equal(t, sql.ErrNoRows, err)
	})
	t.Run("DuplicateMessage", func(t *testing.T) {
		t.Parallel()
		msg, err := data.NewMessage(channel1, producer1, samplePayload, sampleContentType)
		assert.Nil(t, err)
		repo := getMessageRepository()
		assert.Nil(t, repo.Create(msg))
		err = repo.Create(msg)
		assert.NotNil(t, err)
		assert.Equal(t, ErrDuplicateMessageIDForChannel, err)
	})
	t.Run("ProducerReadErr", func(t *testing.T) {
		t.Parallel()
		expectedErr := errors.New("producer could not be read")
		msg, err := data.NewMessage(channel1, producer1, samplePayload, sampleContentType)
		assert.Nil(t, err)
		mockProducerRepository := new(MockProducerRepository)
		repo := NewMessageRepository(testDB, NewChannelRepository(testDB), mockProducerRepository)
		mockProducerRepository.On("Get", mock.Anything).Return(nil, expectedErr)
		assert.Nil(t, repo.Create(msg))
		_, err = repo.Get(channel1.ChannelID, msg.MessageID)
		assert.NotNil(t, err)
		assert.Equal(t, expectedErr, err)
	})
}
