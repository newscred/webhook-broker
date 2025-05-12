package controllers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/julienschmidt/httprouter"
	"github.com/newscred/webhook-broker/storage/data"
	storagemocks "github.com/newscred/webhook-broker/storage/mocks"
	"github.com/stretchr/testify/assert"
)

func TestUpdatedMessagesStatusController(t *testing.T) {
	channelID := "sample-channel"
	msgRepo := new(storagemocks.MessageRepository)
	scheduledMsgRepo := new(storagemocks.ScheduledMessageRepository)
	channelRepo := new(storagemocks.ChannelRepository)
	djRepo := new(storagemocks.DeliveryJobRepository)

	// Create real controllers
	msgController := NewMessageController(msgRepo, djRepo)
	msgsController := NewMessagesController(msgController, msgRepo)

	// Create a real ScheduledMessagesController with necessary dependencies
	scheduledMsgsController := NewScheduledMessagesController(scheduledMsgRepo, channelRepo)

	// Create the controller to test
	msgsStatusController := NewMessagesStatusController(msgsController, scheduledMsgsController, msgRepo, scheduledMsgRepo)

	router := httprouter.New()
	router.GET(messagesStatusPath, msgsStatusController.Get)

	t.Run("All repositories return success", func(t *testing.T) {
		// Setup message status counts
		msgStatusCounts := []*data.StatusCount[data.MsgStatus]{
			{Status: data.MsgStatusAcknowledged, Count: 3},
			{Status: data.MsgStatusDispatched, Count: 7},
		}

		// Setup scheduled message status counts
		scheduledMsgStatusCounts := []*data.StatusCount[data.ScheduledMsgStatus]{
			{Status: data.ScheduledMsgStatusScheduled, Count: 5},
			{Status: data.ScheduledMsgStatusDispatched, Count: 2},
		}

		// Setup next scheduled time
		nextTime := time.Now().Add(10 * time.Minute)

		// Setup expectations
		msgRepo.On("GetMessageStatusCountsByChannel", channelID).Once().Return(msgStatusCounts, nil)
		scheduledMsgRepo.On("GetScheduledMessageStatusCountsByChannel", channelID).Once().Return(scheduledMsgStatusCounts, nil)
		scheduledMsgRepo.On("GetNextScheduledMessageTime", channelID).Once().Return(&nextTime, nil)

		req, _ := http.NewRequest("GET", "/channel/"+channelID+"/messages-status", nil)
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)

		var result map[string]interface{}
		err := json.Unmarshal(rr.Body.Bytes(), &result)
		assert.NoError(t, err)

		counts, ok := result["counts"].(map[string]interface{})
		assert.True(t, ok)

		// Check regular message counts
		acknowledged, ok := counts["ACKNOWLEDGED"].(map[string]interface{})
		assert.True(t, ok)
		assert.Equal(t, float64(3), acknowledged["Count"])

		dispatched, ok := counts["DISPATCHED"].(map[string]interface{})
		assert.True(t, ok)
		assert.Equal(t, float64(7), dispatched["Count"])

		// Check scheduled message counts
		scheduledScheduled, ok := counts["SCHEDULED_SCHEDULED"].(map[string]interface{})
		assert.True(t, ok)
		assert.Equal(t, float64(5), scheduledScheduled["Count"])

		scheduledDispatched, ok := counts["SCHEDULED_DISPATCHED"].(map[string]interface{})
		assert.True(t, ok)
		assert.Equal(t, float64(2), scheduledDispatched["Count"])

		// Check next scheduled time
		nextScheduledTime, ok := result["next_scheduled_message_at"]
		assert.True(t, ok)
		assert.NotNil(t, nextScheduledTime)

		msgRepo.AssertExpectations(t)
		scheduledMsgRepo.AssertExpectations(t)
	})

	t.Run("Message repository error", func(t *testing.T) {
		expectedErr := fmt.Errorf("message repo error")
		msgRepo.On("GetMessageStatusCountsByChannel", channelID).Once().Return(nil, expectedErr)

		req, _ := http.NewRequest("GET", "/channel/"+channelID+"/messages-status", nil)
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusInternalServerError, rr.Code)
		assert.Equal(t, expectedErr.Error(), rr.Body.String())

		msgRepo.AssertExpectations(t)
	})

	t.Run("Scheduled message repository error", func(t *testing.T) {
		msgStatusCounts := []*data.StatusCount[data.MsgStatus]{
			{Status: data.MsgStatusAcknowledged, Count: 3},
		}

		expectedErr := fmt.Errorf("scheduled message repo error")
		msgRepo.On("GetMessageStatusCountsByChannel", channelID).Once().Return(msgStatusCounts, nil)
		scheduledMsgRepo.On("GetScheduledMessageStatusCountsByChannel", channelID).Once().Return(nil, expectedErr)

		req, _ := http.NewRequest("GET", "/channel/"+channelID+"/messages-status", nil)
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusInternalServerError, rr.Code)
		assert.Equal(t, expectedErr.Error(), rr.Body.String())

		msgRepo.AssertExpectations(t)
		scheduledMsgRepo.AssertExpectations(t)
	})

	t.Run("No scheduled messages", func(t *testing.T) {
		msgStatusCounts := []*data.StatusCount[data.MsgStatus]{
			{Status: data.MsgStatusAcknowledged, Count: 3},
		}

		scheduledMsgStatusCounts := []*data.StatusCount[data.ScheduledMsgStatus]{}

		msgRepo.On("GetMessageStatusCountsByChannel", channelID).Once().Return(msgStatusCounts, nil)
		scheduledMsgRepo.On("GetScheduledMessageStatusCountsByChannel", channelID).Once().Return(scheduledMsgStatusCounts, nil)
		scheduledMsgRepo.On("GetNextScheduledMessageTime", channelID).Once().Return(nil, nil)

		req, _ := http.NewRequest("GET", "/channel/"+channelID+"/messages-status", nil)
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)

		var result map[string]interface{}
		err := json.Unmarshal(rr.Body.Bytes(), &result)
		assert.NoError(t, err)

		counts, ok := result["counts"].(map[string]interface{})
		assert.True(t, ok)

		acknowledged, ok := counts["ACKNOWLEDGED"].(map[string]interface{})
		assert.True(t, ok)
		assert.Equal(t, float64(3), acknowledged["Count"])

		_, hasNextScheduledTime := result["next_scheduled_message_at"]
		assert.False(t, hasNextScheduledTime)

		msgRepo.AssertExpectations(t)
		scheduledMsgRepo.AssertExpectations(t)
	})
}
