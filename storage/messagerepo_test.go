package storage

import (
	"database/sql"
	"testing"

	"github.com/imyousuf/webhook-broker/storage/data"
	"github.com/stretchr/testify/assert"
)

const (
	samplePayload      = "some payload"
	sampleContentType  = "a content type"
	duplicateMessageID = "a-duplicate-message-id"
)

func getMessageRepository() MessageRepository {
	return NewMessageRepository(testDB, NewChannelRepository(testDB))
}

func TestMessageGetCreate(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		repo := getMessageRepository()
		msg, err := data.NewMessage(channel1, samplePayload, sampleContentType)
		assert.Nil(t, err)
		_, err = repo.Get(channel1.ChannelID, msg.MessageID)
		assert.NotNil(t, err)
		assert.Nil(t, repo.Create(msg))
		var readMessage *data.Message
		readMessage, err = repo.Get(channel1.ChannelID, msg.MessageID)
		assert.Nil(t, err)
		assert.Equal(t, msg.MessageID, readMessage.MessageID)
		assert.Equal(t, msg.ID, readMessage.ID)
		assert.Equal(t, msg.BroadcastedTo.ChannelID, readMessage.BroadcastedTo.ChannelID)
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
		msg, err := data.NewMessage(channel1, samplePayload, sampleContentType)
		assert.Nil(t, err)
		msg.MessageID = ""
		repo := getMessageRepository()
		assert.NotNil(t, repo.Create(msg))
	})
	t.Run("NonExistingChannel", func(t *testing.T) {
		t.Parallel()
		channel, _ := data.NewChannel("testchannel4msgtest", "token")
		channel.QuickFix()
		msg, err := data.NewMessage(channel, samplePayload, sampleContentType)
		assert.Nil(t, err)
		repo := getMessageRepository()
		err = repo.Create(msg)
		assert.NotNil(t, err)
		_, err = repo.Get(channel.ChannelID, msg.MessageID)
		assert.NotNil(t, err)
		assert.Equal(t, sql.ErrNoRows, err)
	})
	t.Run("DuplicateMessage", func(t *testing.T) {
		t.Parallel()
		msg, err := data.NewMessage(channel1, samplePayload, sampleContentType)
		assert.Nil(t, err)
		repo := getMessageRepository()
		assert.Nil(t, repo.Create(msg))
		err = repo.Create(msg)
		assert.NotNil(t, err)
		assert.Equal(t, ErrDuplicateMessageIDForChannel, err)
	})
}
