package data

import (
	"testing"
	"time"

	"github.com/rs/xid"
	"github.com/stretchr/testify/assert"
)

func getProducer() *Producer {
	producer, _ := NewProducer("testproducerformessage", "testtoken")
	return producer
}

func getCompleteMessageFixture() *Message {
	channel := getChannel()
	producer := getProducer()
	return &Message{BasePaginateable: BasePaginateable{ID: xid.New(), CreatedAt: time.Now(), UpdatedAt: time.Now()}, MessageID: xid.New().String(),
		Payload: "Sample Payload", ContentType: "SampleContent/type", Priority: 1, Status: MsgStatusAcknowledged, BroadcastedTo: channel, ReceivedAt: time.Now(),
		OutboxedAt: time.Now(), ProducedBy: producer}
}

func TestMessageQuickFix(t *testing.T) {
	t.Run("NoQuickFixRequire", func(t *testing.T) {
		t.Parallel()
		msg := getCompleteMessageFixture()
		assert.False(t, msg.QuickFix())
	})
	t.Run("OnlyParentFixRequire", func(t *testing.T) {
		t.Parallel()
		msg := getCompleteMessageFixture()
		msg.BasePaginateable.CreatedAt = time.Time{}
		assert.True(t, msg.QuickFix())
	})
	t.Run("ReceivedAtFixRequire", func(t *testing.T) {
		t.Parallel()
		msg := getCompleteMessageFixture()
		msg.ReceivedAt = time.Time{}
		assert.True(t, msg.QuickFix())
	})
	t.Run("OutboxedAtFixRequire", func(t *testing.T) {
		t.Parallel()
		msg := getCompleteMessageFixture()
		msg.Status = MsgStatusDispatched
		msg.OutboxedAt = time.Time{}
		assert.True(t, msg.QuickFix())
	})
	t.Run("StatusFixRequire", func(t *testing.T) {
		t.Parallel()
		msg := getCompleteMessageFixture()
		msg.Status = MsgStatus(0)
		assert.True(t, msg.QuickFix())
	})
	t.Run("MessageIDFixRequire", func(t *testing.T) {
		t.Parallel()
		msg := getCompleteMessageFixture()
		msg.MessageID = ""
		assert.True(t, msg.QuickFix())
	})
}

func TestMessageIsInValidState(t *testing.T) {
	t.Run("Valid", func(t *testing.T) {
		t.Parallel()
		msg := getCompleteMessageFixture()
		assert.True(t, msg.IsInValidState())
	})
	t.Run("ChannelNil", func(t *testing.T) {
		t.Parallel()
		msg := getCompleteMessageFixture()
		msg.BroadcastedTo = nil
		assert.False(t, msg.IsInValidState())
	})
	t.Run("ChannelInvalid", func(t *testing.T) {
		t.Parallel()
		msg := getCompleteMessageFixture()
		msg.BroadcastedTo = &Channel{}
		assert.False(t, msg.IsInValidState())
	})
	t.Run("ProducerNil", func(t *testing.T) {
		t.Parallel()
		msg := getCompleteMessageFixture()
		msg.ProducedBy = nil
		assert.False(t, msg.IsInValidState())
	})
	t.Run("ProducerInvalid", func(t *testing.T) {
		t.Parallel()
		msg := getCompleteMessageFixture()
		msg.ProducedBy = &Producer{}
		assert.False(t, msg.IsInValidState())
	})
	t.Run("ContentType", func(t *testing.T) {
		t.Parallel()
		msg := getCompleteMessageFixture()
		msg.ContentType = ""
		assert.False(t, msg.IsInValidState())
	})
	t.Run("Payload", func(t *testing.T) {
		t.Parallel()
		msg := getCompleteMessageFixture()
		msg.Payload = ""
		assert.False(t, msg.IsInValidState())
	})
	t.Run("MessageID", func(t *testing.T) {
		t.Parallel()
		msg := getCompleteMessageFixture()
		msg.MessageID = ""
		assert.False(t, msg.IsInValidState())
	})
	t.Run("InvalidStatus", func(t *testing.T) {
		t.Parallel()
		msg := getCompleteMessageFixture()
		msg.Status = MsgStatus(0)
		assert.False(t, msg.IsInValidState())
	})
	t.Run("InvalidReceivedAt", func(t *testing.T) {
		t.Parallel()
		msg := getCompleteMessageFixture()
		msg.ReceivedAt = time.Time{}
		assert.False(t, msg.IsInValidState())
	})
	t.Run("InvalidOutboxedAt", func(t *testing.T) {
		t.Parallel()
		msg := getCompleteMessageFixture()
		msg.Status = MsgStatusDispatched
		msg.OutboxedAt = time.Time{}
		assert.False(t, msg.IsInValidState())
	})
}

func TestMessageGetChannelIDSafely(t *testing.T) {
	msg := getCompleteMessageFixture()
	assert.Equal(t, "testchannelforconsumer", msg.GetChannelIDSafely())
	msg.BroadcastedTo = nil
	assert.Equal(t, "", msg.GetChannelIDSafely())
}

func TestNewMessage(t *testing.T) {
	t.Run("Valid", func(t *testing.T) {
		t.Parallel()
		msg, err := NewMessage(getChannel(), getProducer(), "valid", "valid-ct")
		assert.Nil(t, err)
		assert.Equal(t, "valid", msg.Payload)
		assert.Equal(t, "valid-ct", msg.ContentType)
	})
	t.Run("NilChannel", func(t *testing.T) {
		t.Parallel()
		_, err := NewMessage(nil, getProducer(), "valid", "valid-ct")
		assert.NotNil(t, err)
	})
	t.Run("InvalidChannel", func(t *testing.T) {
		t.Parallel()
		channel := getChannel()
		channel.ChannelID = ""
		_, err := NewMessage(channel, getProducer(), "valid", "valid-ct")
		assert.NotNil(t, err)
	})
	t.Run("NilProducer", func(t *testing.T) {
		t.Parallel()
		_, err := NewMessage(getChannel(), nil, "valid", "valid-ct")
		assert.NotNil(t, err)
	})
	t.Run("InvalidProducer", func(t *testing.T) {
		t.Parallel()
		producer := getProducer()
		producer.ProducerID = ""
		_, err := NewMessage(getChannel(), producer, "valid", "valid-ct")
		assert.NotNil(t, err)
	})
	t.Run("EmptyPayload", func(t *testing.T) {
		t.Parallel()
		_, err := NewMessage(getChannel(), getProducer(), "", "valid-ct")
		assert.NotNil(t, err)
	})
	t.Run("EmptyContentType", func(t *testing.T) {
		t.Parallel()
		_, err := NewMessage(getChannel(), getProducer(), "valid", "")
		assert.NotNil(t, err)
	})
}

func TestMessageGetNewLockID(t *testing.T) {
	msg, _ := NewMessage(getChannel(), getProducer(), "valid", "valid-ct")
	lock, err := NewLock(msg)
	assert.Nil(t, err)
	assert.Equal(t, messageLockPrefix+msg.ID.String(), lock.LockID)
}
