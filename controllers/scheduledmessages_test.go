package controllers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/julienschmidt/httprouter"
	"github.com/newscred/webhook-broker/storage"
	"github.com/newscred/webhook-broker/storage/data"
	storagemocks "github.com/newscred/webhook-broker/storage/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func getScheduledMessagesController() *ScheduledMessagesController {
	channelRepo := storage.NewChannelRepository(db)
	return NewScheduledMessagesController(storage.NewScheduledMessageRepository(db, channelRepo, storage.NewProducerRepository(db)), channelRepo)
}


func TestScheduledMessagesControllerGetPath(t *testing.T) {
	scheduledMsgRepo := new(storagemocks.ScheduledMessageRepository)
	controller := NewScheduledMessagesController(scheduledMsgRepo, nil)
	path := controller.GetPath()
	assert.Equal(t, "/channel/:channelId/scheduled-messages", path)
}

func TestScheduledMessagesControllerFormatAsRelativeLink(t *testing.T) {
	scheduledMsgRepo := new(storagemocks.ScheduledMessageRepository)
	controller := NewScheduledMessagesController(scheduledMsgRepo, nil)
	link := controller.FormatAsRelativeLink(
		httprouter.Param{Key: channelIDPathParamKey, Value: "test-channel"},
	)
	assert.Equal(t, "/channel/test-channel/scheduled-messages", link)
}

func TestScheduledMessagesControllerGet(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		scheduledMsgRepo := new(storagemocks.ScheduledMessageRepository)
		channelRepo := new(storagemocks.ChannelRepository)
		controller := NewScheduledMessagesController(scheduledMsgRepo, channelRepo)

		// Create a test channel
		channel, _ := data.NewChannel("test-channel", "test-token")
		channelRepo.On("Get", "test-channel").Return(channel, nil)

		// Create test scheduled messages
		producer, _ := data.NewProducer("test-producer", "test-token")
		scheduledMsgs := []*data.ScheduledMessage{}

		for i := 0; i < 3; i++ {
			dispatchTime := time.Now().Add(time.Duration(i+1) * time.Minute)
			scheduledMsg, _ := data.NewScheduledMessage(channel, producer, "test-payload", "test/content-type", dispatchTime, data.HeadersMap{})
			scheduledMsg.MessageID = "test-message-" + strconv.Itoa(i)
			scheduledMsgs = append(scheduledMsgs, scheduledMsg)
		}

		// Setup mock repository
		resultPagination := data.NewPagination(scheduledMsgs[len(scheduledMsgs)-1], scheduledMsgs[0])
		scheduledMsgRepo.On("GetScheduledMessagesForChannel", "test-channel", mock.AnythingOfType("*data.Pagination")).Return(scheduledMsgs, resultPagination, nil)

		// Create test request
		req, _ := http.NewRequest("GET", "/channel/test-channel/scheduled-messages", nil)
		params := []httprouter.Param{
			{Key: channelIDPathParamKey, Value: "test-channel"},
		}

		rr := httptest.NewRecorder()
		controller.Get(rr, req, params)

		// Verify response
		assert.Equal(t, http.StatusOK, rr.Code)

		var responseData map[string]interface{}
		err := json.Unmarshal(rr.Body.Bytes(), &responseData)
		assert.NoError(t, err)

		results, ok := responseData["result"].([]interface{})
		assert.True(t, ok)
		assert.Equal(t, 3, len(results))

		for _, uri := range results {
			assert.Contains(t, uri, "/channel/test-channel/scheduled-message/")
		}

		scheduledMsgRepo.AssertExpectations(t)
		channelRepo.AssertExpectations(t)
	})

	t.Run("ChannelNotFound", func(t *testing.T) {
		scheduledMsgRepo := new(storagemocks.ScheduledMessageRepository)
		channelRepo := new(storagemocks.ChannelRepository)
		controller := NewScheduledMessagesController(scheduledMsgRepo, channelRepo)

		// Setup mock repository to return error
		notFoundErr := errors.New("not found")
		channelRepo.On("Get", "non-existent-channel").Return(&data.Channel{}, notFoundErr)

		// Create test request
		req, _ := http.NewRequest("GET", "/channel/non-existent-channel/scheduled-messages", nil)
		params := []httprouter.Param{
			{Key: channelIDPathParamKey, Value: "non-existent-channel"},
		}

		rr := httptest.NewRecorder()
		controller.Get(rr, req, params)

		// Verify response
		assert.Equal(t, http.StatusNotFound, rr.Code)
		scheduledMsgRepo.AssertExpectations(t)
		channelRepo.AssertExpectations(t)
	})

	t.Run("WithStatusFilter", func(t *testing.T) {
		scheduledMsgRepo := new(storagemocks.ScheduledMessageRepository)
		channelRepo := new(storagemocks.ChannelRepository)
		controller := NewScheduledMessagesController(scheduledMsgRepo, channelRepo)

		// Create a test channel
		channel, _ := data.NewChannel("test-channel", "test-token")
		channelRepo.On("Get", "test-channel").Return(channel, nil)

		// Create test scheduled messages
		producer, _ := data.NewProducer("test-producer", "test-token")
		scheduledMsgs := []*data.ScheduledMessage{}

		// Only create SCHEDULED status messages
		for i := 0; i < 2; i++ {
			dispatchTime := time.Now().Add(time.Duration(i+1) * time.Minute)
			scheduledMsg, _ := data.NewScheduledMessage(channel, producer, "test-payload", "test/content-type", dispatchTime, data.HeadersMap{})
			scheduledMsg.MessageID = "test-message-" + strconv.Itoa(i)
			scheduledMsgs = append(scheduledMsgs, scheduledMsg)
		}

		// Setup mock repository with status filter for SCHEDULED status (100)
		resultPagination := data.NewPagination(scheduledMsgs[len(scheduledMsgs)-1], scheduledMsgs[0])
		// We need to match exactly what will be passed when using strconv.Atoi from the request: data.ScheduledMsgStatus(100) instead of data.ScheduledMsgStatusScheduled
		scheduledMsgRepo.On("GetScheduledMessagesForChannel", "test-channel", mock.AnythingOfType("*data.Pagination"), data.ScheduledMsgStatus(100)).Return(scheduledMsgs, resultPagination, nil)

		// Create test request with status filter
		req, _ := http.NewRequest("GET", "/channel/test-channel/scheduled-messages?status=100", nil)
		params := []httprouter.Param{
			{Key: channelIDPathParamKey, Value: "test-channel"},
		}

		rr := httptest.NewRecorder()
		controller.Get(rr, req, params)

		// Verify response
		assert.Equal(t, http.StatusOK, rr.Code)

		var responseData map[string]interface{}
		err := json.Unmarshal(rr.Body.Bytes(), &responseData)
		assert.NoError(t, err)

		results, ok := responseData["result"].([]interface{})
		assert.True(t, ok)
		assert.Equal(t, 2, len(results))

		scheduledMsgRepo.AssertExpectations(t)
		channelRepo.AssertExpectations(t)
	})
}
