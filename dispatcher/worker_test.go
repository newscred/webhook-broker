package dispatcher

import (
	"bytes"
	"errors"
	"strings"
	"strconv"
	"net/http"
	"testing"
	"time"

	"github.com/newscred/webhook-broker/storage/data"
	storagemocks "github.com/newscred/webhook-broker/storage/mocks"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/assert"
)

// Most of the tests are covered in msgdispatcher_test.go there are some exceptional scenarios tested here

func TestDeliverJob_MarkDead(t *testing.T) {
	messagePayload := `{"key": "Custom JSON"}`
	contentType := "application/json"
	brokerConf := getMockedBrokerConfig()
	outerDispatcher := NewMessageDispatcher(getCompleteDispatcherConfiguration(dataAccessor.GetMessageRepository(), dataAccessor.GetDeliveryJobRepository(), dataAccessor.GetConsumerRepository(), brokerConf, configuration, dataAccessor.GetLockRepository()))
	msg, _ := data.NewMessage(channel, producer, messagePayload, contentType, data.HeadersMap{})
	msg.ReceivedAt = msg.ReceivedAt.Add(-5 * time.Second)
	err := dataAccessor.GetMessageRepository().Create(msg)
	assert.Nil(t, err)
	msgDispatcher := outerDispatcher.(*MessageDispatcherImpl)
	jobs, err := createJobs(msgDispatcher, msg)
	if err == nil {
		err = msgDispatcher.djRepo.DispatchMessage(msg, jobs...)
	}
	jobs, _, err = dataAccessor.GetDeliveryJobRepository().GetJobsForMessage(msg, data.NewPagination(nil, nil))
	assert.Nil(t, err)
	inflightJob := jobs[0]
	inflightJob.RetryAttemptCount = 5
	worker := msgDispatcher.workers[0]
	oldCallConsumer := callConsumer
	defer func() {
		callConsumer = oldCallConsumer
	}()
	expectedErr := errors.New("Expected error")
	callConsumer = func(worker *Worker, requestID string, logger zerolog.Logger, job *Job) (err error) {
		return expectedErr
	}
	deliverJob(worker, NewJob(inflightJob))
}

func TestDeliverJob_MarkOpFailed(t *testing.T) {
	var buf bytes.Buffer
	oldLogger := log.Logger
	log.Logger = log.Output(&buf)
	defer func() {
		log.Logger = oldLogger
	}()
	messagePayload := `{"key": "Custom JSON"}`
	contentType := "application/json"
	brokerConf := getMockedBrokerConfig()
	outerDispatcher := NewMessageDispatcher(getCompleteDispatcherConfiguration(dataAccessor.GetMessageRepository(), dataAccessor.GetDeliveryJobRepository(), dataAccessor.GetConsumerRepository(), brokerConf, configuration, dataAccessor.GetLockRepository()))
	msg, _ := data.NewMessage(channel, producer, messagePayload, contentType, data.HeadersMap{})
	msg.ReceivedAt = msg.ReceivedAt.Add(-5 * time.Second)
	err := dataAccessor.GetMessageRepository().Create(msg)
	assert.Nil(t, err)
	msgDispatcher := outerDispatcher.(*MessageDispatcherImpl)
	jobs, err := createJobs(msgDispatcher, msg)
	if err == nil {
		err = msgDispatcher.djRepo.DispatchMessage(msg, jobs...)
	}
	jobs, _, err = dataAccessor.GetDeliveryJobRepository().GetJobsForMessage(msg, data.NewPagination(nil, nil))
	assert.Nil(t, err)
	inflightJob := jobs[0]
	inflightJob.RetryAttemptCount = 5
	worker := msgDispatcher.workers[0]
	mockDJRepo := new(storagemocks.DeliveryJobRepository)
	worker.djRepo = mockDJRepo
	expectedErr := errors.New("Expected error")
	mockDJRepo.On("MarkJobInflight", inflightJob).Return(nil)
	mockDJRepo.On("MarkJobDelivered", inflightJob).Return(expectedErr)
	deliverJob(worker, NewJob(inflightJob))
	mockDJRepo.AssertExpectations(t)
	assert.Contains(t, buf.String(), "delivered job")
	assert.Contains(t, buf.String(), "Could not update job status")
	assert.Contains(t, buf.String(), inflightJob.ID.String())
}

func TestCallConsumerPanic(t *testing.T) {
	var buf bytes.Buffer
	oldLogger := log.Logger
	log.Logger = log.Output(&buf)
	defer func() {
		log.Logger = oldLogger
	}()
	messagePayload := `{"key": "Custom JSON"}`
	contentType := "application/json"
	brokerConf := getMockedBrokerConfig()
	outerDispatcher := NewMessageDispatcher(getCompleteDispatcherConfiguration(dataAccessor.GetMessageRepository(), dataAccessor.GetDeliveryJobRepository(), dataAccessor.GetConsumerRepository(), brokerConf, configuration, dataAccessor.GetLockRepository()))
	msg, _ := data.NewMessage(channel, producer, messagePayload, contentType, data.HeadersMap{})
	msg.ReceivedAt = msg.ReceivedAt.Add(-5 * time.Second)
	err := dataAccessor.GetMessageRepository().Create(msg)
	assert.Nil(t, err)
	msgDispatcher := outerDispatcher.(*MessageDispatcherImpl)
	jobs, err := createJobs(msgDispatcher, msg)
	if err == nil {
		err = msgDispatcher.djRepo.DispatchMessage(msg, jobs...)
	}
	jobs, _, err = dataAccessor.GetDeliveryJobRepository().GetJobsForMessage(msg, data.NewPagination(nil, nil))
	assert.Nil(t, err)
	inflightJob := jobs[0]
	inflightJob.RetryAttemptCount = 5
	worker := msgDispatcher.workers[0]
	oldCallConsumer := callConsumer
	defer func() {
		callConsumer = oldCallConsumer
	}()
	callConsumer = func(worker *Worker, requestID string, logger zerolog.Logger, job *Job) (err error) {
		panic("test panic")
	}
	deliverJob(worker, NewJob(inflightJob))
	assert.Contains(t, buf.String(), "job marked dead")
	assert.Contains(t, buf.String(), "panic in executeJob")
	assert.Contains(t, buf.String(), inflightJob.ID.String())

}

func TestCallConsumer(t *testing.T) {
	worker := Worker{
		consumerConnectionConfig: configuration,
		brokerConfig:             getMockedBrokerConfig(),
		httpClient:               createHTTPClient(configuration),
	}
	job, err := setupTestJob(consumers[0])
	assert.Nil(t, err)
	t.Run("Success", func(t *testing.T) {
		t.Cleanup(clearConsumerHandler)
		consumerHandler[strings.ReplaceAll(consumers[0].ConsumerID, consumerIDPrefix, "consumer-")] = func(s string, rw http.ResponseWriter, r *http.Request) {
			assert.Equal(t, job.Message.ContentType, r.Header.Get(headerContentType))
			assert.Equal(t, strconv.Itoa(int(NewJob(job).Priority)), r.Header.Get(headerBrokerPriority))
			assert.Equal(t, channel.ChannelID, r.Header.Get(headerChannelID))
			assert.Equal(t, consumers[0].ConsumerID, r.Header.Get(headerConsumerID))
			assert.Equal(t, consumers[0].Token, r.Header.Get(configuration.GetTokenRequestHeaderName()))
			assert.Equal(t, configuration.GetUserAgent(), r.Header.Get(headerUserAgent))
			assert.NotNil(t, r.Header.Get(headerRequestID))
			assert.Equal(t, job.Message.MessageID, r.Header.Get(headerMessageID))
			rw.WriteHeader(http.StatusNoContent)
		}
		err := callConsumer(&worker, "test-request", log.Logger, NewJob(job))
		assert.Nil(t, err)
	})
}
