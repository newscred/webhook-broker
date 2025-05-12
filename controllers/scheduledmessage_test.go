package controllers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/julienschmidt/httprouter"
	"github.com/newscred/webhook-broker/storage/data"
	storagemocks "github.com/newscred/webhook-broker/storage/mocks"
	"github.com/stretchr/testify/assert"
)

func TestScheduledMessageControllerGetPath(t *testing.T) {
	scheduledMsgRepo := new(storagemocks.ScheduledMessageRepository)
	controller := NewScheduledMessageController(scheduledMsgRepo, nil)
	path := controller.GetPath()
	assert.Equal(t, "/channel/:channelId/scheduled-message/:messageId", path)
}

func TestScheduledMessageControllerFormatAsRelativeLink(t *testing.T) {
	scheduledMsgRepo := new(storagemocks.ScheduledMessageRepository)
	controller := NewScheduledMessageController(scheduledMsgRepo, nil)
	link := controller.FormatAsRelativeLink(
		httprouter.Param{Key: channelIDPathParamKey, Value: "test-channel"},
		httprouter.Param{Key: "messageId", Value: "test-message"},
	)
	assert.Equal(t, "/channel/test-channel/scheduled-message/test-message", link)
}

func TestScheduledMessageControllerGet(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		scheduledMsgRepo := new(storagemocks.ScheduledMessageRepository)
		channelRepo := new(storagemocks.ChannelRepository)
		controller := NewScheduledMessageController(scheduledMsgRepo, channelRepo)

		// Create a test scheduled message
		channel, _ := data.NewChannel("test-channel", "test-token")
		producer, _ := data.NewProducer("test-producer", "test-token")
		dispatchTime := time.Now().Add(10 * time.Minute)
		scheduledMsg, _ := data.NewScheduledMessage(channel, producer, "test-payload", "test/content-type", dispatchTime, data.HeadersMap{})

		// Setup mock repository
		scheduledMsgRepo.On("Get", "test-channel", "test-message").Return(scheduledMsg, nil)

		// Create test request
		req, _ := http.NewRequest("GET", "/channel/test-channel/scheduled-message/test-message", nil)
		params := []httprouter.Param{
			{Key: channelIDPathParamKey, Value: "test-channel"},
			{Key: "messageId", Value: "test-message"},
		}

		rr := httptest.NewRecorder()
		controller.Get(rr, req, params)

		// Verify response
		assert.Equal(t, http.StatusOK, rr.Code)

		var responseData map[string]interface{}
		err := json.Unmarshal(rr.Body.Bytes(), &responseData)
		assert.NoError(t, err)

		assert.Equal(t, scheduledMsg.ID.String(), responseData["id"])
		assert.Equal(t, scheduledMsg.MessageID, responseData["messageId"])
		assert.Equal(t, scheduledMsg.ContentType, responseData["contentType"])
		assert.Equal(t, "SCHEDULED", responseData["status"])
		assert.Equal(t, scheduledMsg.Payload, responseData["payload"])
		assert.Equal(t, float64(scheduledMsg.Priority), responseData["priority"])
		assert.Equal(t, scheduledMsg.ProducedBy.ProducerID, responseData["producedBy"])
		assert.Nil(t, responseData["dispatchedDate"]) // Should be nil for a scheduled message

		scheduledMsgRepo.AssertExpectations(t)
	})

	t.Run("NotFound", func(t *testing.T) {
		scheduledMsgRepo := new(storagemocks.ScheduledMessageRepository)
		channelRepo := new(storagemocks.ChannelRepository)
		controller := NewScheduledMessageController(scheduledMsgRepo, channelRepo)

		// Setup mock repository to return error
		notFoundErr := errors.New("not found")
		scheduledMsgRepo.On("Get", "test-channel", "not-found").Return(&data.ScheduledMessage{}, notFoundErr)

		// Create test request
		req, _ := http.NewRequest("GET", "/channel/test-channel/scheduled-message/not-found", nil)
		params := []httprouter.Param{
			{Key: channelIDPathParamKey, Value: "test-channel"},
			{Key: "messageId", Value: "not-found"},
		}

		rr := httptest.NewRecorder()
		controller.Get(rr, req, params)

		// Verify response
		assert.Equal(t, http.StatusNotFound, rr.Code)
		scheduledMsgRepo.AssertExpectations(t)
	})

	t.Run("WithDispatchedDate", func(t *testing.T) {
		scheduledMsgRepo := new(storagemocks.ScheduledMessageRepository)
		channelRepo := new(storagemocks.ChannelRepository)
		controller := NewScheduledMessageController(scheduledMsgRepo, channelRepo)

		// Create a test scheduled message that has been dispatched
		channel, _ := data.NewChannel("test-channel", "test-token")
		producer, _ := data.NewProducer("test-producer", "test-token")
		dispatchTime := time.Now().Add(-10 * time.Minute)
		dispatchedTime := time.Now().Add(-5 * time.Minute)

		scheduledMsg, _ := data.NewScheduledMessage(channel, producer, "test-payload", "test/content-type", dispatchTime, data.HeadersMap{})
		scheduledMsg.Status = data.ScheduledMsgStatusDispatched
		scheduledMsg.DispatchedDate = dispatchedTime

		// Setup mock repository
		scheduledMsgRepo.On("Get", "test-channel", "dispatched-message").Return(scheduledMsg, nil)

		// Create test request
		req, _ := http.NewRequest("GET", "/channel/test-channel/scheduled-message/dispatched-message", nil)
		params := []httprouter.Param{
			{Key: channelIDPathParamKey, Value: "test-channel"},
			{Key: "messageId", Value: "dispatched-message"},
		}

		rr := httptest.NewRecorder()
		controller.Get(rr, req, params)

		// Verify response
		assert.Equal(t, http.StatusOK, rr.Code)

		var responseData map[string]interface{}
		err := json.Unmarshal(rr.Body.Bytes(), &responseData)
		assert.NoError(t, err)

		assert.Equal(t, scheduledMsg.ID.String(), responseData["id"])
		assert.Equal(t, "DISPATCHED", responseData["status"])
		assert.NotNil(t, responseData["dispatchedDate"]) // Should have a dispatched date

		scheduledMsgRepo.AssertExpectations(t)
	})
}
