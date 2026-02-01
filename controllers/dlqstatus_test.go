package controllers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/julienschmidt/httprouter"
	"github.com/newscred/webhook-broker/storage/data"
	storagemocks "github.com/newscred/webhook-broker/storage/mocks"
	"github.com/stretchr/testify/assert"
)

func TestDLQStatusControllerGetPath(t *testing.T) {
	controller := NewDLQStatusController(new(storagemocks.DLQSummaryRepository))
	assert.Equal(t, "/dlq-status", controller.GetPath())
}

func TestDLQStatusControllerFormatAsRelativeLink(t *testing.T) {
	controller := NewDLQStatusController(new(storagemocks.DLQSummaryRepository))
	assert.Equal(t, "/dlq-status", controller.FormatAsRelativeLink(httprouter.Param{Key: "test", Value: "val"}))
}

func TestDLQStatusGet(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		mockRepo := new(storagemocks.DLQSummaryRepository)
		summaries := []*data.DLQSummary{
			{ConsumerID: "c1", ConsumerName: "consumer-1", ChannelID: "ch1", ChannelName: "channel-1", DeadCount: 5},
			{ConsumerID: "c2", ConsumerName: "consumer-2", ChannelID: "ch2", ChannelName: "channel-2", DeadCount: 10},
		}
		mockRepo.On("GetAll").Return(summaries, nil)
		controller := NewDLQStatusController(mockRepo)
		testRouter := createTestRouter(controller)
		req, _ := http.NewRequest(http.MethodGet, "/dlq-status", nil)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code)

		var result DLQStatusModel
		err := json.Unmarshal(rr.Body.Bytes(), &result)
		assert.NoError(t, err)
		assert.Len(t, result.Consumers, 2)
		assert.Equal(t, "c1", result.Consumers[0].ConsumerID)
		assert.Equal(t, int64(5), result.Consumers[0].DeadCount)
		assert.Equal(t, "ch2", result.Consumers[1].ChannelID)
		mockRepo.AssertExpectations(t)
	})

	t.Run("Empty", func(t *testing.T) {
		t.Parallel()
		mockRepo := new(storagemocks.DLQSummaryRepository)
		mockRepo.On("GetAll").Return([]*data.DLQSummary{}, nil)
		controller := NewDLQStatusController(mockRepo)
		testRouter := createTestRouter(controller)
		req, _ := http.NewRequest(http.MethodGet, "/dlq-status", nil)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code)

		var result DLQStatusModel
		err := json.Unmarshal(rr.Body.Bytes(), &result)
		assert.NoError(t, err)
		assert.Empty(t, result.Consumers)
		mockRepo.AssertExpectations(t)
	})

	t.Run("Error", func(t *testing.T) {
		t.Parallel()
		mockRepo := new(storagemocks.DLQSummaryRepository)
		mockRepo.On("GetAll").Return(nil, errors.New("db error"))
		controller := NewDLQStatusController(mockRepo)
		testRouter := createTestRouter(controller)
		req, _ := http.NewRequest(http.MethodGet, "/dlq-status", nil)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusInternalServerError, rr.Code)
		mockRepo.AssertExpectations(t)
	})
}

// Generated with assistance from Claude AI
