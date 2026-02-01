package controllers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/julienschmidt/httprouter"
	"github.com/newscred/webhook-broker/dispatcher"
	"github.com/newscred/webhook-broker/storage/data"
	configmocks "github.com/newscred/webhook-broker/config/mocks"
	storagemocks "github.com/newscred/webhook-broker/storage/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

var (
	dlqDeleteChannel  *data.Channel
	dlqDeleteConsumer *data.Consumer
	dlqDeleteMetrics  *dispatcher.MetricsContainer
)

func DLQDeleteTestSetup() {
	var err error
	dlqDeleteChannel, _ = data.NewChannel("dlq-delete-test-channel", successfulGetTestToken)
	dlqDeleteChannel, err = channelRepo.Store(dlqDeleteChannel)
	if err != nil {
		panic(err)
	}
	dlqDeleteConsumer, _ = data.NewConsumer(dlqDeleteChannel, "dlq-delete-test-consumer", successfulGetTestToken, callbackURL, "")
	dlqDeleteConsumer, err = consumerRepo.Store(dlqDeleteConsumer)
	if err != nil {
		panic(err)
	}
	dlqDeleteMetrics = dispatcher.NewMetricsContainer()
}

func newMockBrokerConfig() *configmocks.BrokerConfig {
	m := new(configmocks.BrokerConfig)
	m.On("GetMaxRetry").Return(uint8(5))
	return m
}

func TestDLQPurgeControllerGetPath(t *testing.T) {
	controller := NewDLQPurgeController(nil, nil, nil, nil, nil, nil)
	assert.Equal(t, "/channel/:channelId/consumer/:consumerId/dlq", controller.GetPath())
}

func TestDLQPurgeControllerFormatAsRelativeLink(t *testing.T) {
	controller := NewDLQPurgeController(nil, nil, nil, nil, nil, nil)
	link := controller.FormatAsRelativeLink(
		httprouter.Param{Key: channelIDPathParamKey, Value: "ch1"},
		httprouter.Param{Key: consumerIDPathParamKey, Value: "c1"},
	)
	assert.Equal(t, "/channel/ch1/consumer/c1/dlq", link)
}

func TestDLQPurgeDelete(t *testing.T) {
	channelID := dlqDeleteChannel.ChannelID
	consumerID := dlqDeleteConsumer.ConsumerID
	baseURL := "/channel/" + channelID + "/consumer/" + consumerID + "/dlq"

	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		mockDJRepo := new(storagemocks.DeliveryJobRepository)
		mockSummaryRepo := new(storagemocks.DLQSummaryRepository)
		controller := NewDLQPurgeController(channelRepo, consumerRepo, mockDJRepo, mockSummaryRepo, newMockBrokerConfig(), dlqDeleteMetrics)
		mockDJRepo.On("DeleteDeadJobsForConsumer", mock.AnythingOfType("*data.Consumer"), uint(5)).Return(int64(3), nil)
		mockSummaryRepo.On("DecrementCount", mock.AnythingOfType("string"), int64(3)).Return(nil)
		testRouter := createTestRouter(controller)
		req, _ := http.NewRequest(http.MethodDelete, baseURL, nil)
		req.Header.Set(headerChannelToken, dlqDeleteChannel.Token)
		req.Header.Set(headerConsumerToken, dlqDeleteConsumer.Token)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code)
		var result map[string]int64
		json.Unmarshal(rr.Body.Bytes(), &result)
		assert.Equal(t, int64(3), result["deletedCount"])
		mockDJRepo.AssertExpectations(t)
	})

	t.Run("NoDeadJobs", func(t *testing.T) {
		t.Parallel()
		mockDJRepo := new(storagemocks.DeliveryJobRepository)
		mockSummaryRepo := new(storagemocks.DLQSummaryRepository)
		controller := NewDLQPurgeController(channelRepo, consumerRepo, mockDJRepo, mockSummaryRepo, newMockBrokerConfig(), dlqDeleteMetrics)
		mockDJRepo.On("DeleteDeadJobsForConsumer", mock.AnythingOfType("*data.Consumer"), uint(5)).Return(int64(0), nil)
		testRouter := createTestRouter(controller)
		req, _ := http.NewRequest(http.MethodDelete, baseURL, nil)
		req.Header.Set(headerChannelToken, dlqDeleteChannel.Token)
		req.Header.Set(headerConsumerToken, dlqDeleteConsumer.Token)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code)
		var result map[string]int64
		json.Unmarshal(rr.Body.Bytes(), &result)
		assert.Equal(t, int64(0), result["deletedCount"])
		mockDJRepo.AssertExpectations(t)
	})

	t.Run("WrongChannelToken", func(t *testing.T) {
		t.Parallel()
		controller := NewDLQPurgeController(channelRepo, consumerRepo, nil, nil, nil, nil)
		testRouter := createTestRouter(controller)
		req, _ := http.NewRequest(http.MethodDelete, baseURL, nil)
		req.Header.Set(headerChannelToken, "wrong-token")
		req.Header.Set(headerConsumerToken, dlqDeleteConsumer.Token)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusForbidden, rr.Code)
	})

	t.Run("WrongConsumerToken", func(t *testing.T) {
		t.Parallel()
		controller := NewDLQPurgeController(channelRepo, consumerRepo, nil, nil, nil, nil)
		testRouter := createTestRouter(controller)
		req, _ := http.NewRequest(http.MethodDelete, baseURL, nil)
		req.Header.Set(headerChannelToken, dlqDeleteChannel.Token)
		req.Header.Set(headerConsumerToken, "wrong-token")
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusForbidden, rr.Code)
	})

	t.Run("ChannelNotFound", func(t *testing.T) {
		t.Parallel()
		controller := NewDLQPurgeController(channelRepo, consumerRepo, nil, nil, nil, nil)
		testRouter := createTestRouter(controller)
		req, _ := http.NewRequest(http.MethodDelete, "/channel/nonexistent/consumer/"+consumerID+"/dlq", nil)
		req.Header.Set(headerChannelToken, dlqDeleteChannel.Token)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusNotFound, rr.Code)
	})

	t.Run("DeleteError", func(t *testing.T) {
		t.Parallel()
		mockDJRepo := new(storagemocks.DeliveryJobRepository)
		controller := NewDLQPurgeController(channelRepo, consumerRepo, mockDJRepo, nil, newMockBrokerConfig(), nil)
		mockDJRepo.On("DeleteDeadJobsForConsumer", mock.AnythingOfType("*data.Consumer"), uint(5)).Return(int64(0), errors.New("db error"))
		testRouter := createTestRouter(controller)
		req, _ := http.NewRequest(http.MethodDelete, baseURL, nil)
		req.Header.Set(headerChannelToken, dlqDeleteChannel.Token)
		req.Header.Set(headerConsumerToken, dlqDeleteConsumer.Token)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusInternalServerError, rr.Code)
		mockDJRepo.AssertExpectations(t)
	})
}

func TestDeadJobDeleteControllerGetPath(t *testing.T) {
	controller := NewDeadJobDeleteController(nil, nil, nil, nil, nil, nil)
	assert.Equal(t, "/channel/:channelId/consumer/:consumerId/job/:jobId", controller.GetPath())
}

func TestDeadJobDeleteControllerFormatAsRelativeLink(t *testing.T) {
	controller := NewDeadJobDeleteController(nil, nil, nil, nil, nil, nil)
	link := controller.FormatAsRelativeLink(
		httprouter.Param{Key: channelIDPathParamKey, Value: "ch1"},
		httprouter.Param{Key: consumerIDPathParamKey, Value: "c1"},
		httprouter.Param{Key: jobIDPathParamKey, Value: "j1"},
	)
	assert.Equal(t, "/channel/ch1/consumer/c1/job/j1", link)
}

func TestDeadJobDelete(t *testing.T) {
	channelID := dlqDeleteChannel.ChannelID
	consumerID := dlqDeleteConsumer.ConsumerID

	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		mockDJRepo := new(storagemocks.DeliveryJobRepository)
		mockSummaryRepo := new(storagemocks.DLQSummaryRepository)
		controller := NewDeadJobDeleteController(channelRepo, consumerRepo, mockDJRepo, mockSummaryRepo, newMockBrokerConfig(), dlqDeleteMetrics)
		job := &data.DeliveryJob{Status: data.JobDead}
		job.Listener = &data.Consumer{ConsumerID: consumerID}
		mockDJRepo.On("GetByID", "job-1").Return(job, nil)
		mockDJRepo.On("DeleteDeadJob", job, uint(5)).Return(int64(1), nil)
		mockSummaryRepo.On("DecrementCount", mock.AnythingOfType("string"), int64(1)).Return(nil)
		testRouter := createTestRouter(controller)
		req, _ := http.NewRequest(http.MethodDelete, "/channel/"+channelID+"/consumer/"+consumerID+"/job/job-1", nil)
		req.Header.Set(headerChannelToken, dlqDeleteChannel.Token)
		req.Header.Set(headerConsumerToken, dlqDeleteConsumer.Token)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusNoContent, rr.Code)
		mockDJRepo.AssertExpectations(t)
	})

	t.Run("JobNotFound", func(t *testing.T) {
		t.Parallel()
		mockDJRepo := new(storagemocks.DeliveryJobRepository)
		controller := NewDeadJobDeleteController(channelRepo, consumerRepo, mockDJRepo, nil, newMockBrokerConfig(), nil)
		mockDJRepo.On("GetByID", "nonexistent").Return(nil, errors.New("not found"))
		testRouter := createTestRouter(controller)
		req, _ := http.NewRequest(http.MethodDelete, "/channel/"+channelID+"/consumer/"+consumerID+"/job/nonexistent", nil)
		req.Header.Set(headerChannelToken, dlqDeleteChannel.Token)
		req.Header.Set(headerConsumerToken, dlqDeleteConsumer.Token)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusNotFound, rr.Code)
		mockDJRepo.AssertExpectations(t)
	})

	t.Run("JobNotDead", func(t *testing.T) {
		t.Parallel()
		mockDJRepo := new(storagemocks.DeliveryJobRepository)
		controller := NewDeadJobDeleteController(channelRepo, consumerRepo, mockDJRepo, nil, newMockBrokerConfig(), nil)
		job := &data.DeliveryJob{Status: data.JobQueued}
		job.Listener = &data.Consumer{ConsumerID: consumerID}
		mockDJRepo.On("GetByID", "job-queued").Return(job, nil)
		testRouter := createTestRouter(controller)
		req, _ := http.NewRequest(http.MethodDelete, "/channel/"+channelID+"/consumer/"+consumerID+"/job/job-queued", nil)
		req.Header.Set(headerChannelToken, dlqDeleteChannel.Token)
		req.Header.Set(headerConsumerToken, dlqDeleteConsumer.Token)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusNotFound, rr.Code)
		mockDJRepo.AssertExpectations(t)
	})

	t.Run("WrongConsumer", func(t *testing.T) {
		t.Parallel()
		mockDJRepo := new(storagemocks.DeliveryJobRepository)
		controller := NewDeadJobDeleteController(channelRepo, consumerRepo, mockDJRepo, nil, newMockBrokerConfig(), nil)
		job := &data.DeliveryJob{Status: data.JobDead}
		job.Listener = &data.Consumer{ConsumerID: "other-consumer"}
		mockDJRepo.On("GetByID", "job-other").Return(job, nil)
		testRouter := createTestRouter(controller)
		req, _ := http.NewRequest(http.MethodDelete, "/channel/"+channelID+"/consumer/"+consumerID+"/job/job-other", nil)
		req.Header.Set(headerChannelToken, dlqDeleteChannel.Token)
		req.Header.Set(headerConsumerToken, dlqDeleteConsumer.Token)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusNotFound, rr.Code)
		mockDJRepo.AssertExpectations(t)
	})

	t.Run("DeleteReturnsZeroRows", func(t *testing.T) {
		t.Parallel()
		mockDJRepo := new(storagemocks.DeliveryJobRepository)
		controller := NewDeadJobDeleteController(channelRepo, consumerRepo, mockDJRepo, nil, newMockBrokerConfig(), nil)
		job := &data.DeliveryJob{Status: data.JobDead}
		job.Listener = &data.Consumer{ConsumerID: consumerID}
		mockDJRepo.On("GetByID", "job-retry").Return(job, nil)
		mockDJRepo.On("DeleteDeadJob", job, uint(5)).Return(int64(0), nil)
		testRouter := createTestRouter(controller)
		req, _ := http.NewRequest(http.MethodDelete, "/channel/"+channelID+"/consumer/"+consumerID+"/job/job-retry", nil)
		req.Header.Set(headerChannelToken, dlqDeleteChannel.Token)
		req.Header.Set(headerConsumerToken, dlqDeleteConsumer.Token)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusNotFound, rr.Code)
		mockDJRepo.AssertExpectations(t)
	})

	t.Run("WrongChannelToken", func(t *testing.T) {
		t.Parallel()
		controller := NewDeadJobDeleteController(channelRepo, consumerRepo, nil, nil, nil, nil)
		testRouter := createTestRouter(controller)
		req, _ := http.NewRequest(http.MethodDelete, "/channel/"+channelID+"/consumer/"+consumerID+"/job/j1", nil)
		req.Header.Set(headerChannelToken, "wrong-token")
		req.Header.Set(headerConsumerToken, dlqDeleteConsumer.Token)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusForbidden, rr.Code)
	})

	t.Run("DeleteError", func(t *testing.T) {
		t.Parallel()
		mockDJRepo := new(storagemocks.DeliveryJobRepository)
		controller := NewDeadJobDeleteController(channelRepo, consumerRepo, mockDJRepo, nil, newMockBrokerConfig(), nil)
		job := &data.DeliveryJob{Status: data.JobDead}
		job.Listener = &data.Consumer{ConsumerID: consumerID}
		mockDJRepo.On("GetByID", "job-err").Return(job, nil)
		mockDJRepo.On("DeleteDeadJob", job, uint(5)).Return(int64(0), errors.New("db error"))
		testRouter := createTestRouter(controller)
		req, _ := http.NewRequest(http.MethodDelete, "/channel/"+channelID+"/consumer/"+consumerID+"/job/job-err", nil)
		req.Header.Set(headerChannelToken, dlqDeleteChannel.Token)
		req.Header.Set(headerConsumerToken, dlqDeleteConsumer.Token)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusInternalServerError, rr.Code)
		mockDJRepo.AssertExpectations(t)
	})
}

// Generated with assistance from Claude AI
