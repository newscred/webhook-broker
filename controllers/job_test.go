package controllers

import (
	"encoding/json"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/rs/zerolog/log"

	"github.com/julienschmidt/httprouter"
	"github.com/newscred/webhook-broker/storage"
	"github.com/newscred/webhook-broker/storage/data"
	storagemocks "github.com/newscred/webhook-broker/storage/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

var (
	deliveryJobRepo     storage.DeliveryJobRepository
	jobTestChannel      *data.Channel
	jobTestProducer     *data.Producer
	jobTestPullConsumer *data.Consumer
	jobTestPushConsumer *data.Consumer
)

const (
	jobTestChannelID      = "job-test-channel-id"
	jobTestProducerID     = "job-test-producer-id"
	jobTestPullConsumerID = "job-test-pull-consumer-id"
	jobTestPushConsumerID = "job-test-push-consumer-id"
)

// JobTestSetup is called from TestMain for the package
func JobTestSetup() {
	setupTestChannel()
	setupTestProducer()
	jobTestPullConsumer = setupTestConsumer(jobTestPullConsumerID, data.PullConsumerStr)
	jobTestPushConsumer = setupTestConsumer(jobTestPushConsumerID, data.PushConsumerStr)

	deliveryJobRepo = storage.NewDeliveryJobRepository(db, messageRepo, consumerRepo)

	for index := 0; index < 50; index++ {
		indexString := strconv.Itoa(index)
		message, err := data.NewMessage(jobTestChannel, jobTestProducer, "payload "+indexString, "type")
		if err != nil {
			log.Fatal()
		}
		message.Priority = randomPriority()
		messageRepo.Create(message)
		job, err := data.NewDeliveryJob(message, jobTestPullConsumer)
		if err != nil {
			log.Fatal()
		}
		deliveryJobRepo.DispatchMessage(message, job)
	}
}

func setupTestChannel() {
	channel, err := data.NewChannel(jobTestChannelID, successfulGetTestToken)
	if err != nil {
		log.Fatal()
	}
	jobTestChannel, err = channelRepo.Store(channel)
	if err != nil {
		log.Fatal().Err(err)
	}
}

func setupTestProducer() {
	producer, err := data.NewProducer(jobTestProducerID, successfulGetTestToken)
	if err != nil {
		log.Fatal()
	}
	jobTestProducer, err = producerRepo.Store(producer)
	if err != nil {
		log.Fatal().Err(err)
	}
}

func setupTestConsumer(consumerID string, consumerType string) *data.Consumer {
	callbackURL, err := url.Parse("https://imytech.net/")
	if err != nil {
		log.Fatal().Err(err)
	}
	consumer, err := data.NewConsumer(jobTestChannel, consumerID, successfulGetTestToken, callbackURL, consumerType)
	if err != nil {
		log.Fatal()
	}
	consumer, err = consumerRepo.Store(consumer)
	if err != nil {
		log.Fatal().Err(err)
	}
	return consumer
}

func randomPriority() uint {
	return uint(1 + rand.Intn(10))
}

func getJobsControllerWithMockedRepo() *JobsController {
	channelRepo := new(storagemocks.ChannelRepository)
	consumerRepo := new(storagemocks.ConsumerRepository)
	djRepo := new(storagemocks.DeliveryJobRepository)
	return NewJobsController(channelRepo, consumerRepo, djRepo)
}

func TestJobsFormatAsRelativeLink(t *testing.T) {
	t.Parallel()
	controller := NewJobsController(channelRepo, consumerRepo, deliveryJobRepo)
	assert.Equal(t, jobsPath, controller.GetPath())
	formattedPath := controller.FormatAsRelativeLink(
		httprouter.Param{Key: channelIDPathParamKey, Value: "someChannelId"},
		httprouter.Param{Key: consumerIDPathParamKey, Value: "someConsumerId"},
	)
	assert.Equal(t, "/channel/someChannelId/consumer/someConsumerId/queued-jobs", formattedPath)
}

func TestJobsControllerGet_Success(t *testing.T) {
	jobsController := NewJobsController(channelRepo, consumerRepo, deliveryJobRepo)
	testRouter := createTestRouter(jobsController)
	testURI := jobsController.FormatAsRelativeLink(
		httprouter.Param{Key: channelIDPathParamKey, Value: jobTestChannel.ChannelID},
		httprouter.Param{Key: consumerIDPathParamKey, Value: jobTestPullConsumer.ConsumerID},
	)
	t.Log(testURI)

	t.Run("200 OK", func(t *testing.T) {
		t.Parallel()
		req, err := http.NewRequest(http.MethodGet, testURI, nil)
		assert.NoError(t, err)
		req.Header.Add(headerChannelToken, jobTestChannel.Token)
		req.Header.Add(headerConsumerToken, jobTestPullConsumer.Token)

		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code)
		body := rr.Body.String()
		t.Log(body)
		jobListResult := &JobListResult{}
		json.NewDecoder(strings.NewReader(body)).Decode(jobListResult)

		jobs := jobListResult.Result
		assert.Equal(t, 25, len(jobs))

		assert.True(t, sort.SliceIsSorted(jobs, func(i, j int) bool {
			return jobs[i].Priority > jobs[j].Priority
		}))
	})

	limitQueries := []struct {
		limit    string
		pageSize int
	}{
		{limit: "", pageSize: 25},
		{limit: "10", pageSize: 10},
		{limit: "10000", pageSize: 50},
	}
	for _, query := range limitQueries {
		t.Run("200 OK with limit="+query.limit, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, testURI+"?limit="+query.limit, nil)
			assert.NoError(t, err)
			req.Header.Add(headerChannelToken, jobTestChannel.Token)
			req.Header.Add(headerConsumerToken, jobTestPullConsumer.Token)

			rr := httptest.NewRecorder()
			testRouter.ServeHTTP(rr, req)
			assert.Equal(t, http.StatusOK, rr.Code)
			body := rr.Body.String()
			t.Log(body)
			jobListResult := &JobListResult{}
			json.NewDecoder(strings.NewReader(body)).Decode(jobListResult)

			jobs := jobListResult.Result
			assert.Equal(t, query.pageSize, len(jobs))

			assert.True(t, sort.SliceIsSorted(jobs, func(i, j int) bool {
				return jobs[i].Priority > jobs[j].Priority
			}))
		})
	}
}

func TestJobsControllerGet_Error(t *testing.T) {
	jobsController := NewJobsController(channelRepo, consumerRepo, deliveryJobRepo)
	testRouter := createTestRouter(jobsController)
	t.Run("400 Channel Not Found", func(t *testing.T) {
		t.Parallel()
		testURI := jobsController.FormatAsRelativeLink(
			httprouter.Param{Key: channelIDPathParamKey, Value: "invalid-channel-id"},
			httprouter.Param{Key: consumerIDPathParamKey, Value: "invalid-consumer-id"},
		)
		t.Log(testURI)

		req, err := http.NewRequest(http.MethodGet, testURI, nil)
		assert.NoError(t, err)

		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusNotFound, rr.Code)
		assert.Equal(t, rr.Body.String(), ErrNotFound.Error())
	})

	t.Run("403 Channel Token Mismatch", func(t *testing.T) {
		t.Parallel()
		testURI := jobsController.FormatAsRelativeLink(
			httprouter.Param{Key: channelIDPathParamKey, Value: jobTestChannel.ChannelID},
			httprouter.Param{Key: consumerIDPathParamKey, Value: "invalid-consumer-id"},
		)
		t.Log(testURI)

		req, err := http.NewRequest(http.MethodGet, testURI, nil)
		assert.NoError(t, err)

		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusForbidden, rr.Code)
		assert.Equal(t, rr.Body.String(), errChannelTokenNotMatching.Error())
	})

	t.Run("401 Consumer Not Found", func(t *testing.T) {
		t.Parallel()
		testURI := jobsController.FormatAsRelativeLink(
			httprouter.Param{Key: channelIDPathParamKey, Value: jobTestChannel.ChannelID},
			httprouter.Param{Key: consumerIDPathParamKey, Value: "invalid-consumer-id"},
		)
		t.Log(testURI)

		req, err := http.NewRequest(http.MethodGet, testURI, nil)
		assert.NoError(t, err)
		req.Header.Add(headerChannelToken, jobTestChannel.Token)

		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusUnauthorized, rr.Code)
		assert.Equal(t, rr.Body.String(), errConsumerDoesNotExist.Error())
	})

	t.Run("403 Consumer Token Mismatch", func(t *testing.T) {
		t.Parallel()
		testURI := jobsController.FormatAsRelativeLink(
			httprouter.Param{Key: channelIDPathParamKey, Value: jobTestChannel.ChannelID},
			httprouter.Param{Key: consumerIDPathParamKey, Value: jobTestPullConsumer.ConsumerID},
		)
		t.Log(testURI)

		req, err := http.NewRequest(http.MethodGet, testURI, nil)
		assert.NoError(t, err)
		req.Header.Add(headerChannelToken, jobTestChannel.Token)

		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusForbidden, rr.Code)
		assert.Equal(t, rr.Body.String(), errConsumerTokenNotMatching.Error())
	})

	t.Run("412 Consumer Not Pull Based", func(t *testing.T) {
		t.Parallel()
		testURI := jobsController.FormatAsRelativeLink(
			httprouter.Param{Key: channelIDPathParamKey, Value: jobTestChannel.ChannelID},
			httprouter.Param{Key: consumerIDPathParamKey, Value: jobTestPushConsumer.ConsumerID},
		)
		t.Log(testURI)

		req, err := http.NewRequest(http.MethodGet, testURI, nil)
		assert.NoError(t, err)
		req.Header.Add(headerChannelToken, jobTestChannel.Token)
		req.Header.Add(headerConsumerToken, jobTestPushConsumer.Token)

		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusPreconditionFailed, rr.Code)
		assert.Equal(t, rr.Body.String(), errConsumerNotPullBased.Error())
	})

	invalidLimits := []string{"-10", "5.33", "invalid"}
	for _, invalidLimit := range invalidLimits {
		t.Run("400 Bad Request with limit="+invalidLimit, func(t *testing.T) {
			testURI := jobsController.FormatAsRelativeLink(
				httprouter.Param{Key: channelIDPathParamKey, Value: jobTestChannel.ChannelID},
				httprouter.Param{Key: consumerIDPathParamKey, Value: jobTestPullConsumer.ConsumerID},
			)
			t.Log(testURI)

			req, err := http.NewRequest(http.MethodGet, testURI+"?limit="+invalidLimit, nil)
			assert.NoError(t, err)
			req.Header.Add(headerChannelToken, jobTestChannel.Token)
			req.Header.Add(headerConsumerToken, jobTestPullConsumer.Token)

			rr := httptest.NewRecorder()
			testRouter.ServeHTTP(rr, req)
			assert.Equal(t, http.StatusBadRequest, rr.Code)
			assert.Equal(t, rr.Body.String(), errInvalidQueryParam.Error())
		})
	}

	t.Run("500 GetPrioritizedJobsForConsumer Error", func(t *testing.T) {
		t.Parallel()
		testURI := jobsController.FormatAsRelativeLink(
			httprouter.Param{Key: channelIDPathParamKey, Value: jobTestChannel.ChannelID},
			httprouter.Param{Key: consumerIDPathParamKey, Value: jobTestPullConsumer.ConsumerID},
		)
		t.Log(testURI)

		controller := getJobsControllerWithMockedRepo()
		mockChannelRepo := controller.ChannelRepo.(*storagemocks.ChannelRepository)
		mockConsumerRepo := controller.ConsumerRepo.(*storagemocks.ConsumerRepository)
		mockedDJRepo := controller.DeliveryJobRepo.(*storagemocks.DeliveryJobRepository)
		mockChannelRepo.On("Get", jobTestChannelID).Return(jobTestChannel, nil)
		mockConsumerRepo.On("Get", jobTestChannelID, jobTestPullConsumerID).Return(jobTestPullConsumer, nil)
		mockedDJRepo.On("GetPrioritizedJobsForConsumer", jobTestPullConsumer, data.JobQueued, mock.AnythingOfType("int")).Return(nil, errExpected)

		req, err := http.NewRequest(http.MethodGet, testURI, nil)
		assert.NoError(t, err)
		req.Header.Add(headerChannelToken, jobTestChannel.Token)
		req.Header.Add(headerConsumerToken, jobTestPullConsumer.Token)

		testRouter := createTestRouter(controller)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusInternalServerError, rr.Code)
		assert.Equal(t, rr.Body.String(), errExpected.Error())
	})
}
