package controllers

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/julienschmidt/httprouter"
	"github.com/newscred/webhook-broker/storage"
	"github.com/newscred/webhook-broker/storage/data"
	storagemocks "github.com/newscred/webhook-broker/storage/mocks"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

var (
	deliveryJobRepo          storage.DeliveryJobRepository
	jobTestChannel           *data.Channel
	jobTestProducer          *data.Producer
	jobTestPullConsumer      *data.Consumer
	jobTestPushConsumer      *data.Consumer
	jobTestOtherPullConsumer *data.Consumer
)

const (
	jobTestChannelID       = "job-test-channel-id"
	jobTestProducerID      = "job-test-producer-id"
	jobTestPullConsumerID  = "job-test-pull-consumer-id"
	jobTestPushConsumerID  = "job-test-push-consumer-id"
	jobPostTestContentType = "application/json"
)

// JobTestSetup is called from TestMain for the package
func JobTestSetup() {
	setupTestChannel()
	setupTestProducer()
	jobTestPullConsumer = setupTestConsumer(jobTestPullConsumerID, data.PullConsumerStr)
	jobTestPushConsumer = setupTestConsumer(jobTestPushConsumerID, data.PushConsumerStr)
	jobTestOtherPullConsumer = setupTestConsumer("other-"+jobTestPullConsumerID, data.PullConsumerStr)

	deliveryJobRepo = storage.NewDeliveryJobRepository(db, messageRepo, consumerRepo)

	for index := 0; index < 50; index++ {
		indexString := strconv.Itoa(index)
		message, err := data.NewMessage(jobTestChannel, jobTestProducer, "payload "+indexString, "type", data.HeadersMap{})
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

func setupTestJob() (*data.DeliveryJob, error) {
	message, err := data.NewMessage(jobTestChannel, jobTestProducer, "payload", "type", data.HeadersMap{})
	if err != nil {
		log.Fatal()
		return nil, err
	}
	messageRepo.Create(message)
	job, err := data.NewDeliveryJob(message, jobTestPullConsumer)
	if err != nil {
		log.Fatal()
		return nil, err
	}
	deliveryJobRepo.DispatchMessage(message, job)
	return job, nil
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

func getJobController() *JobController {
	return NewJobController(getMessageController(), getNewChannelController(channelRepo),
		NewProducerController(producerRepo), getNewConsumerController(consumerRepo),
		channelRepo, consumerRepo, deliveryJobRepo)
}

func TestJobFormatAsRelativeLink(t *testing.T) {
	t.Parallel()
	controller := getJobController()
	assert.Equal(t, jobPath, controller.GetPath())
	formattedPath := controller.FormatAsRelativeLink(
		httprouter.Param{Key: channelIDPathParamKey, Value: "someChannelId"},
		httprouter.Param{Key: consumerIDPathParamKey, Value: "someConsumerId"},
		httprouter.Param{Key: jobIDPathParamKey, Value: "someJobId"},
	)
	assert.Equal(t, "/channel/someChannelId/consumer/someConsumerId/job/someJobId", formattedPath)
}

func TestJobControllerPost_Success(t *testing.T) {
	t.Parallel()
	job, err := setupTestJob()
	assert.NoError(t, err)

	jobController := getJobController()
	testRouter := createTestRouter(jobController)
	testURI := jobController.FormatAsRelativeLink(
		httprouter.Param{Key: channelIDPathParamKey, Value: jobTestChannel.ChannelID},
		httprouter.Param{Key: consumerIDPathParamKey, Value: jobTestPullConsumer.ConsumerID},
		httprouter.Param{Key: jobIDPathParamKey, Value: job.ID.String()},
	)
	t.Log(testURI)

	validTransitions := []struct {
		current data.JobStatus
		next    data.JobStatus
	}{
		{current: data.JobQueued, next: data.JobQueued},
		{current: data.JobQueued, next: data.JobInflight},
		{current: data.JobInflight, next: data.JobInflight},
		{current: data.JobInflight, next: data.JobDead},
		{current: data.JobDead, next: data.JobDead},
		{current: data.JobDead, next: data.JobInflight},
		{current: data.JobInflight, next: data.JobDelivered},
		{current: data.JobDelivered, next: data.JobDelivered},
	}
	for _, transition := range validTransitions {
		testName := "Success:202-Accepted " + transition.current.String() + " to " + transition.next.String()
		t.Run(testName, func(t *testing.T) {
			// While testing Job's POST endpoint, we also test the Job's GET endpoint
			req, err := http.NewRequest(http.MethodGet, testURI, nil)
			assert.NoError(t, err)
			req.Header.Add(headerContentType, jobPostTestContentType)
			req.Header.Add(headerChannelToken, jobTestChannel.Token)
			req.Header.Add(headerConsumerToken, jobTestPullConsumer.Token)
			rr := httptest.NewRecorder()
			testRouter.ServeHTTP(rr, req)
			assert.Equal(t, http.StatusOK, rr.Code)
			body := rr.Body.String()
			t.Log(body)
			jobModel := &HyperlinkedDeliveryJobModel{}
			json.NewDecoder(strings.NewReader(body)).Decode(jobModel)
			assert.NotEmpty(t, jobModel.ChannelURL)
			assert.NotEmpty(t, jobModel.MessageURL)
			assert.NotEmpty(t, jobModel.ProducerURL)
			assert.NotEmpty(t, jobModel.ConsumerURL)
			if transition.current == data.JobDead {
				assert.NotEmpty(t, jobModel.JobRequeueURL)
			} else {
				assert.Empty(t, jobModel.JobRequeueURL)
			}

			// THE POST ENDPOINT TESTS
			bodyString := "{\"NextState\": \"" + transition.next.String() + "\"}"
			requestBody := ioutil.NopCloser(strings.NewReader(bodyString))
			req, err = http.NewRequest(http.MethodPost, testURI, requestBody)
			assert.NoError(t, err)

			req.Header.Add(headerContentType, jobPostTestContentType)
			req.Header.Add(headerChannelToken, jobTestChannel.Token)
			req.Header.Add(headerConsumerToken, jobTestPullConsumer.Token)

			rr = httptest.NewRecorder()
			testRouter.ServeHTTP(rr, req)
			assert.Equal(t, http.StatusAccepted, rr.Code)

			updatedJob, err := deliveryJobRepo.GetByID(job.ID.String())
			assert.NoError(t, err)
			assert.Equal(t, transition.next, updatedJob.Status)
		})

	}
}

func TestJobControllerPost_TransitionFailure(t *testing.T) {
	t.Parallel()
	job, err := setupTestJob()
	assert.NoError(t, err)

	jobController := getJobController()
	testRouter := createTestRouter(jobController)
	testURI := jobController.FormatAsRelativeLink(
		httprouter.Param{Key: channelIDPathParamKey, Value: jobTestChannel.ChannelID},
		httprouter.Param{Key: consumerIDPathParamKey, Value: jobTestPullConsumer.ConsumerID},
		httprouter.Param{Key: jobIDPathParamKey, Value: job.ID.String()},
	)
	t.Log(testURI)

	runInvalidTransitionTest := func(invalidNextState data.JobStatus) {
		testName := "400 Bad Request " + job.Status.String() + " to " + invalidNextState.String()
		t.Run(testName, func(t *testing.T) {
			bodyString := "{\"NextState\": \"" + invalidNextState.String() + "\"}"
			requestBody := ioutil.NopCloser(strings.NewReader(bodyString))
			req, err := http.NewRequest(http.MethodPost, testURI, requestBody)
			assert.NoError(t, err)

			req.Header.Add(headerContentType, jobPostTestContentType)
			req.Header.Add(headerChannelToken, jobTestChannel.Token)
			req.Header.Add(headerConsumerToken, jobTestPullConsumer.Token)

			rr := httptest.NewRecorder()
			testRouter.ServeHTTP(rr, req)
			assert.Equal(t, http.StatusBadRequest, rr.Code)
			assert.Equal(t, rr.Body.String(), errInvalidTransitionRequest.Error())

			updatedJob, err := deliveryJobRepo.GetByID(job.ID.String())
			assert.NoError(t, err)
			assert.Equal(t, job.Status, updatedJob.Status)
		})
	}

	invalidNextStates := []data.JobStatus{data.JobDelivered, data.JobDead}
	for _, invalidNextState := range invalidNextStates {
		runInvalidTransitionTest(invalidNextState)
	}

	jobController.DeliveryJobRepo.MarkJobInflight(job)
	invalidNextStates = []data.JobStatus{data.JobQueued}
	for _, invalidNextState := range invalidNextStates {
		runInvalidTransitionTest(invalidNextState)
	}

	jobController.DeliveryJobRepo.MarkJobDead(job)
	invalidNextStates = []data.JobStatus{data.JobQueued, data.JobDelivered}
	for _, invalidNextState := range invalidNextStates {
		runInvalidTransitionTest(invalidNextState)
	}

	jobController.DeliveryJobRepo.MarkDeadJobAsInflight(job)
	jobController.DeliveryJobRepo.MarkJobDelivered(job)
	invalidNextStates = []data.JobStatus{data.JobQueued, data.JobInflight, data.JobDead}
	for _, invalidNextState := range invalidNextStates {
		runInvalidTransitionTest(invalidNextState)
	}
}

func TestJobControllerPost_Error(t *testing.T) {
	jobController := getJobController()
	testRouter := createTestRouter(jobController)
	t.Run("400 Channel Not Found", func(t *testing.T) {
		t.Parallel()
		testURI := jobController.FormatAsRelativeLink(
			httprouter.Param{Key: channelIDPathParamKey, Value: "invalid-channel-id"},
			httprouter.Param{Key: consumerIDPathParamKey, Value: "invalid-consumer-id"},
			httprouter.Param{Key: jobIDPathParamKey, Value: "invalid-job-id"},
		)
		t.Log(testURI)

		bodyString := "{\"NextState\": \"INFLIGHT\"}"
		requestBody := ioutil.NopCloser(strings.NewReader(bodyString))
		req, err := http.NewRequest(http.MethodPost, testURI, requestBody)
		assert.NoError(t, err)

		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusNotFound, rr.Code)
		assert.Equal(t, rr.Body.String(), ErrNotFound.Error())
	})

	t.Run("403 Channel Token Mismatch", func(t *testing.T) {
		t.Parallel()
		testURI := jobController.FormatAsRelativeLink(
			httprouter.Param{Key: channelIDPathParamKey, Value: jobTestChannel.ChannelID},
			httprouter.Param{Key: consumerIDPathParamKey, Value: "invalid-consumer-id"},
			httprouter.Param{Key: jobIDPathParamKey, Value: "invalid-job-id"},
		)
		t.Log(testURI)

		bodyString := "{\"NextState\": \"INFLIGHT\"}"
		requestBody := ioutil.NopCloser(strings.NewReader(bodyString))
		req, err := http.NewRequest(http.MethodPost, testURI, requestBody)
		assert.NoError(t, err)

		req.Header.Add(headerContentType, jobPostTestContentType)

		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusForbidden, rr.Code)
		assert.Equal(t, rr.Body.String(), errChannelTokenNotMatching.Error())
	})

	t.Run("401 Consumer Not Found", func(t *testing.T) {
		t.Parallel()
		testURI := jobController.FormatAsRelativeLink(
			httprouter.Param{Key: channelIDPathParamKey, Value: jobTestChannel.ChannelID},
			httprouter.Param{Key: consumerIDPathParamKey, Value: "invalid-consumer-id"},
			httprouter.Param{Key: jobIDPathParamKey, Value: "invalid-job-id"},
		)
		t.Log(testURI)

		bodyString := "{\"NextState\": \"INFLIGHT\"}"
		requestBody := ioutil.NopCloser(strings.NewReader(bodyString))
		req, err := http.NewRequest(http.MethodPost, testURI, requestBody)
		assert.NoError(t, err)

		req.Header.Add(headerContentType, jobPostTestContentType)
		req.Header.Add(headerChannelToken, jobTestChannel.Token)

		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusUnauthorized, rr.Code)
		assert.Equal(t, rr.Body.String(), errConsumerDoesNotExist.Error())
	})

	t.Run("403 Consumer Token Mismatch", func(t *testing.T) {
		t.Parallel()
		testURI := jobController.FormatAsRelativeLink(
			httprouter.Param{Key: channelIDPathParamKey, Value: jobTestChannel.ChannelID},
			httprouter.Param{Key: consumerIDPathParamKey, Value: jobTestPullConsumer.ConsumerID},
			httprouter.Param{Key: jobIDPathParamKey, Value: "invalid-job-id"},
		)
		t.Log(testURI)

		bodyString := "{\"NextState\": \"INFLIGHT\"}"
		requestBody := ioutil.NopCloser(strings.NewReader(bodyString))
		req, err := http.NewRequest(http.MethodPost, testURI, requestBody)
		assert.NoError(t, err)

		req.Header.Add(headerContentType, jobPostTestContentType)
		req.Header.Add(headerChannelToken, jobTestChannel.Token)

		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusForbidden, rr.Code)
		assert.Equal(t, rr.Body.String(), errConsumerTokenNotMatching.Error())
	})

	t.Run("412 Consumer Not Pull Based", func(t *testing.T) {
		t.Parallel()
		testURI := jobController.FormatAsRelativeLink(
			httprouter.Param{Key: channelIDPathParamKey, Value: jobTestChannel.ChannelID},
			httprouter.Param{Key: consumerIDPathParamKey, Value: jobTestPushConsumer.ConsumerID},
			httprouter.Param{Key: jobIDPathParamKey, Value: "invalid-job-id"},
		)
		t.Log(testURI)

		bodyString := "{\"NextState\": \"INFLIGHT\"}"
		requestBody := ioutil.NopCloser(strings.NewReader(bodyString))
		req, err := http.NewRequest(http.MethodPost, testURI, requestBody)
		assert.NoError(t, err)

		req.Header.Add(headerContentType, jobPostTestContentType)
		req.Header.Add(headerChannelToken, jobTestChannel.Token)
		req.Header.Add(headerConsumerToken, jobTestPushConsumer.Token)

		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusPreconditionFailed, rr.Code)
		assert.Equal(t, rr.Body.String(), errConsumerNotPullBased.Error())
	})

	t.Run("404 Job NotFound", func(t *testing.T) {
		t.Parallel()
		testURI := jobController.FormatAsRelativeLink(
			httprouter.Param{Key: channelIDPathParamKey, Value: jobTestChannel.ChannelID},
			httprouter.Param{Key: consumerIDPathParamKey, Value: jobTestPullConsumer.ConsumerID},
			httprouter.Param{Key: jobIDPathParamKey, Value: "invalid-job-id"},
		)
		t.Log(testURI)

		bodyString := "{\"NextState\": \"INFLIGHT\"}"
		requestBody := ioutil.NopCloser(strings.NewReader(bodyString))
		req, err := http.NewRequest(http.MethodPost, testURI, requestBody)
		assert.NoError(t, err)

		req.Header.Add(headerContentType, jobPostTestContentType)
		req.Header.Add(headerChannelToken, jobTestChannel.Token)
		req.Header.Add(headerConsumerToken, jobTestPullConsumer.Token)

		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusNotFound, rr.Code)
		assert.Equal(t, rr.Body.String(), errJobDoesNotExist.Error())
	})

	t.Run("401 Job Not for Consumer", func(t *testing.T) {
		t.Parallel()
		job, err := setupTestJob()
		assert.NoError(t, err)

		testURI := jobController.FormatAsRelativeLink(
			httprouter.Param{Key: channelIDPathParamKey, Value: jobTestChannel.ChannelID},
			httprouter.Param{Key: consumerIDPathParamKey, Value: jobTestOtherPullConsumer.ConsumerID},
			httprouter.Param{Key: jobIDPathParamKey, Value: job.ID.String()},
		)
		t.Log(testURI)

		bodyString := "{\"NextState\": \"INFLIGHT\"}"
		requestBody := ioutil.NopCloser(strings.NewReader(bodyString))
		req, err := http.NewRequest(http.MethodPost, testURI, requestBody)
		assert.NoError(t, err)

		req.Header.Add(headerContentType, jobPostTestContentType)
		req.Header.Add(headerChannelToken, jobTestChannel.Token)
		req.Header.Add(headerConsumerToken, jobTestPullConsumer.Token)

		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusUnauthorized, rr.Code)
		assert.Equal(t, rr.Body.String(), errJobDoesNotExist.Error())
	})

	t.Run("400 Invalid Request Body", func(t *testing.T) {
		t.Parallel()
		job, err := setupTestJob()
		assert.NoError(t, err)

		testURI := jobController.FormatAsRelativeLink(
			httprouter.Param{Key: channelIDPathParamKey, Value: jobTestChannel.ChannelID},
			httprouter.Param{Key: consumerIDPathParamKey, Value: jobTestPullConsumer.ConsumerID},
			httprouter.Param{Key: jobIDPathParamKey, Value: job.ID.String()},
		)
		t.Log(testURI)

		bodyString := "{\"\"}"
		requestBody := ioutil.NopCloser(strings.NewReader(bodyString))
		req, err := http.NewRequest(http.MethodPost, testURI, requestBody)
		assert.NoError(t, err)

		req.Header.Add(headerContentType, jobPostTestContentType)
		req.Header.Add(headerChannelToken, jobTestChannel.Token)
		req.Header.Add(headerConsumerToken, jobTestPullConsumer.Token)

		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusInternalServerError, rr.Code)
	})

	t.Run("400 Invalid NextState", func(t *testing.T) {
		t.Parallel()
		job, err := setupTestJob()
		assert.NoError(t, err)

		testURI := jobController.FormatAsRelativeLink(
			httprouter.Param{Key: channelIDPathParamKey, Value: jobTestChannel.ChannelID},
			httprouter.Param{Key: consumerIDPathParamKey, Value: jobTestPullConsumer.ConsumerID},
			httprouter.Param{Key: jobIDPathParamKey, Value: job.ID.String()},
		)
		t.Log(testURI)

		bodyString := "{\"NextState\": \"invalid-state\"}"
		requestBody := ioutil.NopCloser(strings.NewReader(bodyString))
		req, err := http.NewRequest(http.MethodPost, testURI, requestBody)
		assert.NoError(t, err)

		req.Header.Add(headerContentType, jobPostTestContentType)
		req.Header.Add(headerChannelToken, jobTestChannel.Token)
		req.Header.Add(headerConsumerToken, jobTestPullConsumer.Token)

		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
		assert.Equal(t, rr.Body.String(), errInvalidTransitionRequest.Error())
	})
}

func TestJobControllerPost_Timeout_DifferentTime(t *testing.T) {
	t.Parallel()
	tests := []struct {
		current data.JobStatus
		next    data.JobStatus
		timeout uint
	}{
		{current: data.JobQueued, next: data.JobInflight, timeout: 10},
		{current: data.JobQueued, next: data.JobInflight, timeout: 1000},
		{current: data.JobQueued, next: data.JobInflight, timeout: 100000},
	}
	for _, test := range tests {
		testName := "Success:202-Accepted Timeout Different Time " + strconv.FormatUint(uint64(test.timeout), 10) + " " + test.current.String() + " to " + test.next.String()
		test := test
		t.Run(testName, func(t *testing.T) {
			t.Parallel()
			job, err := setupTestJob()
			assert.NoError(t, err)

			jobController := getJobController()
			testRouter := createTestRouter(jobController)
			testURI := jobController.FormatAsRelativeLink(
				httprouter.Param{Key: channelIDPathParamKey, Value: jobTestChannel.ChannelID},
				httprouter.Param{Key: consumerIDPathParamKey, Value: jobTestPullConsumer.ConsumerID},
				httprouter.Param{Key: jobIDPathParamKey, Value: job.ID.String()},
			)
			t.Log(testURI)

			bodyString := "{\"NextState\": \"" + test.next.String() + "\",\"IncrementalTimeout\":" + strconv.Itoa(int(test.timeout)) + "}"
			requestBody := ioutil.NopCloser(strings.NewReader(bodyString))
			req, err := http.NewRequest(http.MethodPost, testURI, requestBody)
			assert.NoError(t, err)

			req.Header.Add(headerContentType, jobPostTestContentType)
			req.Header.Add(headerChannelToken, jobTestChannel.Token)
			req.Header.Add(headerConsumerToken, jobTestPullConsumer.Token)

			rr := httptest.NewRecorder()
			testRouter.ServeHTTP(rr, req)
			assert.Equal(t, http.StatusAccepted, rr.Code)

			updatedJob, err := deliveryJobRepo.GetByID(job.ID.String())
			assert.NoError(t, err)
			assert.Equal(t, test.next, updatedJob.Status)
			assert.Equal(t, test.timeout, updatedJob.IncrementalTimeout)
		})
	}
}

func TestJobControllerPost_TimeoutValid(t *testing.T) {
	t.Parallel()
	job, err := setupTestJob()
	assert.NoError(t, err)

	jobController := getJobController()
	testRouter := createTestRouter(jobController)
	testURI := jobController.FormatAsRelativeLink(
		httprouter.Param{Key: channelIDPathParamKey, Value: jobTestChannel.ChannelID},
		httprouter.Param{Key: consumerIDPathParamKey, Value: jobTestPullConsumer.ConsumerID},
		httprouter.Param{Key: jobIDPathParamKey, Value: job.ID.String()},
	)
	t.Log(testURI)
	tests := []struct {
		current          data.JobStatus
		next             data.JobStatus
		timeout          uint
		expected_timeout uint
	}{
		{current: data.JobQueued, next: data.JobInflight, timeout: 10, expected_timeout: 10},
		{current: data.JobInflight, next: data.JobInflight, expected_timeout: 10},
		{current: data.JobInflight, next: data.JobDead, expected_timeout: 10},
		{current: data.JobDead, next: data.JobInflight, timeout: 15, expected_timeout: 15},
	}
	for _, test := range tests {
		testName := "Success:202-Accepted Timeout " + strconv.FormatUint(uint64(test.timeout), 10) + " " + test.current.String() + " to " + test.next.String()
		test := test
		t.Run(testName, func(t *testing.T) {

			bodyString := "{\"NextState\": \"" + test.next.String() + "\",\"IncrementalTimeout\":" + strconv.FormatUint(uint64(test.timeout), 10) + "}"
			requestBody := ioutil.NopCloser(strings.NewReader(bodyString))
			req, err := http.NewRequest(http.MethodPost, testURI, requestBody)
			assert.NoError(t, err)

			req.Header.Add(headerContentType, jobPostTestContentType)
			req.Header.Add(headerChannelToken, jobTestChannel.Token)
			req.Header.Add(headerConsumerToken, jobTestPullConsumer.Token)

			rr := httptest.NewRecorder()
			testRouter.ServeHTTP(rr, req)
			assert.Equal(t, http.StatusAccepted, rr.Code)

			updatedJob, err := deliveryJobRepo.GetByID(job.ID.String())
			assert.NoError(t, err)
			assert.Equal(t, test.next, updatedJob.Status)
			assert.Equal(t, test.expected_timeout, updatedJob.IncrementalTimeout)
		})
	}
}

func TestJobControllerPost_TransitionFailureTimeout(t *testing.T) {
	t.Parallel()
	job, err := setupTestJob()
	assert.NoError(t, err)

	jobController := getJobController()
	testRouter := createTestRouter(jobController)
	testURI := jobController.FormatAsRelativeLink(
		httprouter.Param{Key: channelIDPathParamKey, Value: jobTestChannel.ChannelID},
		httprouter.Param{Key: consumerIDPathParamKey, Value: jobTestPullConsumer.ConsumerID},
		httprouter.Param{Key: jobIDPathParamKey, Value: job.ID.String()},
	)
	t.Log(testURI)

	runInvalidTransitionTest := func(invalidNextState data.JobStatus, timeout uint) {
		testName := "400 Bad Request " + job.Status.String() + " to " + invalidNextState.String()
		t.Run(testName, func(t *testing.T) {
			bodyString := "{\"NextState\": \"" + invalidNextState.String() + "\",\"IncrementalTimeout\":" + strconv.FormatUint(uint64(timeout), 10) + "}"
			requestBody := ioutil.NopCloser(strings.NewReader(bodyString))
			req, err := http.NewRequest(http.MethodPost, testURI, requestBody)
			assert.NoError(t, err)

			req.Header.Add(headerContentType, jobPostTestContentType)
			req.Header.Add(headerChannelToken, jobTestChannel.Token)
			req.Header.Add(headerConsumerToken, jobTestPullConsumer.Token)

			rr := httptest.NewRecorder()
			testRouter.ServeHTTP(rr, req)
			assert.Equal(t, http.StatusBadRequest, rr.Code)
			assert.Equal(t, rr.Body.String(), errTimeoutWithInvalidState.Error())

			updatedJob, err := deliveryJobRepo.GetByID(job.ID.String())
			assert.NoError(t, err)
			assert.Equal(t, job.Status, updatedJob.Status)
		})
	}

	invalidTransitions := []struct {
		nextState data.JobStatus
		timeout   uint
	}{
		{nextState: data.JobQueued, timeout: 10},
	}
	for _, trans := range invalidTransitions {
		runInvalidTransitionTest(trans.nextState, trans.timeout)
	}

	job.IncrementalTimeout = 10
	jobController.DeliveryJobRepo.MarkJobInflight(job)
	invalidTransitions = []struct {
		nextState data.JobStatus
		timeout   uint
	}{
		{nextState: data.JobDelivered, timeout: 10},
		{nextState: data.JobInflight, timeout: 10},
		{nextState: data.JobDead, timeout: 10},
		{nextState: data.JobDelivered, timeout: 10},
	}
	for _, invalidTransition := range invalidTransitions {
		runInvalidTransitionTest(invalidTransition.nextState, invalidTransition.timeout)
	}

	jobController.DeliveryJobRepo.MarkJobDead(job)
	invalidTransitions = []struct {
		nextState data.JobStatus
		timeout   uint
	}{
		{nextState: data.JobDead, timeout: 10},
		{nextState: data.JobDelivered, timeout: 10},
	}
	for _, invalidTransition := range invalidTransitions {
		runInvalidTransitionTest(invalidTransition.nextState, invalidTransition.timeout)
	}

	jobController.DeliveryJobRepo.MarkDeadJobAsInflight(job)
	jobController.DeliveryJobRepo.MarkJobDelivered(job)
	invalidTransitions = []struct {
		nextState data.JobStatus
		timeout   uint
	}{
		{nextState: data.JobDelivered, timeout: 10},
	}
	for _, invalidTransition := range invalidTransitions {
		runInvalidTransitionTest(invalidTransition.nextState, invalidTransition.timeout)
	}
}

func TestJobRequeueController_Post(t *testing.T) {
	repo := new(storagemocks.DeliveryJobRepository)
	channelRepo := new(storagemocks.ChannelRepository)
	consumerRepo := new(storagemocks.ConsumerRepository)

	controller := NewJobRequeueController(repo, channelRepo, consumerRepo)
	router := createTestRouter(controller)

	channelID := "test-channel"
	consumerID := "test-consumer"
	jobID := "test-job-id"
	token := "test-token"

	t.Run("Invalid Job Status", func(t *testing.T) {
		mockChannel, _ := data.NewChannel(channelID, token)
		mockChannel.QuickFix()
		channelRepo.On("Get", channelID).Return(mockChannel, nil)
		mockConsumer, _ := data.NewConsumer(mockChannel, consumerID, token, callbackURL, data.PullConsumer.String())
		mockConsumer.QuickFix()
		consumerRepo.On("Get", channelID, consumerID).Return(mockConsumer, nil)
		mockProducer, _ := data.NewProducer("test-producer", token)
		mockProducer.QuickFix()
		mockMsg, _ := data.NewMessage(mockChannel, mockProducer, "test-payload", "text/plain", data.HeadersMap{})
		mockMsg.QuickFix()
		job, _ := data.NewDeliveryJob(mockMsg, mockConsumer)
		job.QuickFix()
		job.Status = data.JobQueued
		repo.On("GetByID", jobID).Return(job, nil).Once()

		url := fmt.Sprintf("/channel/%s/consumer/%s/job/%s/requeue-dead-job", channelID, consumerID, jobID)
		req, _ := http.NewRequest("POST", url, nil)
		req.Header.Set(headerChannelToken, token)
		req.Header.Set(headerConsumerToken, token)
		rr := httptest.NewRecorder()

		router.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
		repo.AssertExpectations(t)
		channelRepo.AssertExpectations(t)
		consumerRepo.AssertExpectations(t)

	})

	t.Run("Job Requeue Successful", func(t *testing.T) {
		mockChannel, _ := data.NewChannel(channelID, token)
		mockChannel.QuickFix()
		channelRepo.On("Get", channelID).Return(mockChannel, nil)
		mockConsumer, _ := data.NewConsumer(mockChannel, consumerID, token, callbackURL, data.PullConsumer.String())
		mockConsumer.QuickFix()
		consumerRepo.On("Get", channelID, consumerID).Return(mockConsumer, nil)
		mockProducer, _ := data.NewProducer("test-producer", token)
		mockProducer.QuickFix()
		mockMsg, _ := data.NewMessage(mockChannel, mockProducer, "test-payload", "text/plain", data.HeadersMap{})
		mockMsg.QuickFix()
		job, _ := data.NewDeliveryJob(mockMsg, mockConsumer)
		job.QuickFix()
		job.Status = data.JobDead
		repo.On("GetByID", jobID).Return(job, nil).Once()
		repo.On("RequeueDeadJob", job).Return(nil).Once()

		url := fmt.Sprintf("/channel/%s/consumer/%s/job/%s/requeue-dead-job", channelID, consumerID, jobID)
		req, _ := http.NewRequest("POST", url, nil)
		req.Header.Set(headerChannelToken, token)
		req.Header.Set(headerConsumerToken, token)

		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusAccepted, rr.Code)
		repo.AssertExpectations(t)
		channelRepo.AssertExpectations(t)
		consumerRepo.AssertExpectations(t)
	})

	t.Run("Job Requeue Error", func(t *testing.T) {
		expectedErr := errors.New("requeue error")
		mockChannel, _ := data.NewChannel(channelID, token)
		mockChannel.QuickFix()
		channelRepo.On("Get", channelID).Return(mockChannel, nil)
		mockConsumer, _ := data.NewConsumer(mockChannel, consumerID, token, callbackURL, data.PullConsumer.String())
		mockConsumer.QuickFix()
		consumerRepo.On("Get", channelID, consumerID).Return(mockConsumer, nil)
		mockProducer, _ := data.NewProducer("test-producer", token)
		mockProducer.QuickFix()
		mockMsg, _ := data.NewMessage(mockChannel, mockProducer, "test-payload", "text/plain", data.HeadersMap{})
		mockMsg.QuickFix()
		job, _ := data.NewDeliveryJob(mockMsg, mockConsumer)
		job.QuickFix()
		job.Status = data.JobDead
		repo.On("GetByID", jobID).Return(job, nil).Once()
		repo.On("RequeueDeadJob", job).Return(expectedErr).Once()

		url := fmt.Sprintf("/channel/%s/consumer/%s/job/%s/requeue-dead-job", channelID, consumerID, jobID)
		req, _ := http.NewRequest("POST", url, nil)
		req.Header.Set(headerChannelToken, token)
		req.Header.Set(headerConsumerToken, token)

		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusInternalServerError, rr.Code)
		assert.Equal(t, expectedErr.Error(), rr.Body.String())

		repo.AssertExpectations(t)
		channelRepo.AssertExpectations(t)
		consumerRepo.AssertExpectations(t)
	})
}
