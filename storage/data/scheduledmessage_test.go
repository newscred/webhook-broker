package data

import (
	"testing"
	"time"

	"github.com/rs/xid"
	"github.com/stretchr/testify/assert"
)

func getCompleteScheduledMessageFixture() *ScheduledMessage {
	channel := getChannel()
	producer := getProducer()
	return &ScheduledMessage{
		BasePaginateable: BasePaginateable{ID: xid.New(), CreatedAt: time.Now(), UpdatedAt: time.Now()},
		MessageID:        xid.New().String(),
		Payload:          "Sample Payload",
		ContentType:      "SampleContent/type",
		Priority:         1,
		Status:           ScheduledMsgStatusScheduled,
		BroadcastedTo:    channel,
		ProducedBy:       producer,
		DispatchSchedule: time.Now().Add(5 * time.Minute),
		Headers:          HeadersMap{},
	}
}

func TestScheduledMessageQuickFix(t *testing.T) {
	t.Run("NoQuickFixRequire", func(t *testing.T) {
		t.Parallel()
		msg := getCompleteScheduledMessageFixture()
		assert.False(t, msg.QuickFix())
	})
	t.Run("OnlyParentFixRequire", func(t *testing.T) {
		t.Parallel()
		msg := getCompleteScheduledMessageFixture()
		msg.BasePaginateable.CreatedAt = time.Time{}
		assert.True(t, msg.QuickFix())
	})
	t.Run("DispatchScheduleFixRequire", func(t *testing.T) {
		t.Parallel()
		msg := getCompleteScheduledMessageFixture()
		msg.DispatchSchedule = time.Time{}
		assert.True(t, msg.QuickFix())
	})
	t.Run("StatusFixRequire", func(t *testing.T) {
		t.Parallel()
		msg := getCompleteScheduledMessageFixture()
		msg.Status = ScheduledMsgStatus(0)
		assert.True(t, msg.QuickFix())
	})
	t.Run("MessageIDFixRequire", func(t *testing.T) {
		t.Parallel()
		msg := getCompleteScheduledMessageFixture()
		msg.MessageID = ""
		assert.True(t, msg.QuickFix())
	})
}

func TestScheduledMessageIsInValidState(t *testing.T) {
	t.Run("Valid", func(t *testing.T) {
		t.Parallel()
		msg := getCompleteScheduledMessageFixture()
		assert.True(t, msg.IsInValidState())
	})
	t.Run("ChannelNil", func(t *testing.T) {
		t.Parallel()
		msg := getCompleteScheduledMessageFixture()
		msg.BroadcastedTo = nil
		assert.False(t, msg.IsInValidState())
	})
	t.Run("ChannelInvalid", func(t *testing.T) {
		t.Parallel()
		msg := getCompleteScheduledMessageFixture()
		msg.BroadcastedTo = &Channel{}
		assert.False(t, msg.IsInValidState())
	})
	t.Run("ProducerNil", func(t *testing.T) {
		t.Parallel()
		msg := getCompleteScheduledMessageFixture()
		msg.ProducedBy = nil
		assert.False(t, msg.IsInValidState())
	})
	t.Run("ProducerInvalid", func(t *testing.T) {
		t.Parallel()
		msg := getCompleteScheduledMessageFixture()
		msg.ProducedBy = &Producer{}
		assert.False(t, msg.IsInValidState())
	})
	t.Run("ContentType", func(t *testing.T) {
		t.Parallel()
		msg := getCompleteScheduledMessageFixture()
		msg.ContentType = ""
		assert.False(t, msg.IsInValidState())
	})
	t.Run("Payload", func(t *testing.T) {
		t.Parallel()
		msg := getCompleteScheduledMessageFixture()
		msg.Payload = ""
		assert.False(t, msg.IsInValidState())
	})
	t.Run("MessageID", func(t *testing.T) {
		t.Parallel()
		msg := getCompleteScheduledMessageFixture()
		msg.MessageID = ""
		assert.False(t, msg.IsInValidState())
	})
	t.Run("InvalidStatus", func(t *testing.T) {
		t.Parallel()
		msg := getCompleteScheduledMessageFixture()
		msg.Status = ScheduledMsgStatus(0)
		assert.False(t, msg.IsInValidState())
	})
	t.Run("InvalidDispatchSchedule", func(t *testing.T) {
		t.Parallel()
		msg := getCompleteScheduledMessageFixture()
		msg.DispatchSchedule = time.Time{}
		assert.False(t, msg.IsInValidState())
	})
	t.Run("InvalidDispatchedDate", func(t *testing.T) {
		t.Parallel()
		msg := getCompleteScheduledMessageFixture()
		msg.Status = ScheduledMsgStatusDispatched
		msg.DispatchedDate = time.Time{}
		assert.False(t, msg.IsInValidState())
	})
}

func TestScheduledMessageGetChannelIDSafely(t *testing.T) {
	msg := getCompleteScheduledMessageFixture()
	assert.Equal(t, "testchannelforconsumer", msg.GetChannelIDSafely())
	msg.BroadcastedTo = nil
	assert.Equal(t, "", msg.GetChannelIDSafely())
}

func TestNewScheduledMessage(t *testing.T) {
	t.Run("Valid", func(t *testing.T) {
		t.Parallel()
		dispatchTime := time.Now().Add(5 * time.Minute)
		msg, err := NewScheduledMessage(getChannel(), getProducer(), "valid", "valid-ct", dispatchTime, HeadersMap{})
		assert.Nil(t, err)
		assert.Equal(t, "valid", msg.Payload)
		assert.Equal(t, "valid-ct", msg.ContentType)
		assert.Equal(t, dispatchTime, msg.DispatchSchedule)
	})
	t.Run("NilChannel", func(t *testing.T) {
		t.Parallel()
		dispatchTime := time.Now().Add(5 * time.Minute)
		_, err := NewScheduledMessage(nil, getProducer(), "valid", "valid-ct", dispatchTime, HeadersMap{})
		assert.NotNil(t, err)
	})
	t.Run("InvalidChannel", func(t *testing.T) {
		t.Parallel()
		channel := getChannel()
		channel.ChannelID = ""
		dispatchTime := time.Now().Add(5 * time.Minute)
		_, err := NewScheduledMessage(channel, getProducer(), "valid", "valid-ct", dispatchTime, HeadersMap{})
		assert.NotNil(t, err)
	})
	t.Run("NilProducer", func(t *testing.T) {
		t.Parallel()
		dispatchTime := time.Now().Add(5 * time.Minute)
		_, err := NewScheduledMessage(getChannel(), nil, "valid", "valid-ct", dispatchTime, HeadersMap{})
		assert.NotNil(t, err)
	})
	t.Run("InvalidProducer", func(t *testing.T) {
		t.Parallel()
		producer := getProducer()
		producer.ProducerID = ""
		dispatchTime := time.Now().Add(5 * time.Minute)
		_, err := NewScheduledMessage(getChannel(), producer, "valid", "valid-ct", dispatchTime, HeadersMap{})
		assert.NotNil(t, err)
	})
	t.Run("EmptyPayload", func(t *testing.T) {
		t.Parallel()
		dispatchTime := time.Now().Add(5 * time.Minute)
		_, err := NewScheduledMessage(getChannel(), getProducer(), "", "valid-ct", dispatchTime, HeadersMap{})
		assert.NotNil(t, err)
	})
	t.Run("EmptyContentType", func(t *testing.T) {
		t.Parallel()
		dispatchTime := time.Now().Add(5 * time.Minute)
		_, err := NewScheduledMessage(getChannel(), getProducer(), "valid", "", dispatchTime, HeadersMap{})
		assert.NotNil(t, err)
	})
}

func TestScheduledMessageGetNewLockID(t *testing.T) {
	dispatchTime := time.Now().Add(5 * time.Minute)
	msg, _ := NewScheduledMessage(getChannel(), getProducer(), "valid", "valid-ct", dispatchTime, HeadersMap{})
	lock, err := NewLock(msg)
	assert.Nil(t, err)
	assert.Equal(t, scheduledMessageLockPrefix+msg.ID.String(), lock.LockID)
}

func TestScheduledMsgStatusString(t *testing.T) {
	assert.Equal(t, ScheduledMsgStatusScheduledStr, ScheduledMsgStatusScheduled.String())
	assert.Equal(t, ScheduledMsgStatusDispatchedStr, ScheduledMsgStatusDispatched.String())
	var status ScheduledMsgStatus
	assert.Equal(t, "0", status.String())
}