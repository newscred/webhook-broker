package controllers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/julienschmidt/httprouter"
	"github.com/rs/xid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/newscred/webhook-broker/config"
	dispatchermocks "github.com/newscred/webhook-broker/dispatcher/mocks"
	"github.com/newscred/webhook-broker/parking"
	"github.com/newscred/webhook-broker/storage"
	"github.com/newscred/webhook-broker/storage/data"
	storagemocks "github.com/newscred/webhook-broker/storage/mocks"
)

// NOTE: tests that create the GLOBAL pause are sequential (no t.Parallel) and always resume before returning, so they
// never overlap with the parallel job transition tests in this package.

type pauseTestFixture struct {
	channel  *data.Channel
	producer *data.Producer
	push     *data.Consumer
	pull     *data.Consumer
}

func newPauseTestFixture(t *testing.T, prefix string) *pauseTestFixture {
	t.Helper()
	unique := prefix + "-" + xid.New().String()
	channel, _ := data.NewChannel(unique+"-channel", successfulGetTestToken)
	channel, err := channelRepo.Store(channel)
	if err != nil {
		t.Fatal(err)
	}
	// Reuse the job tests' producer: tests in producer_test.go count every producer in the shared DB
	producer := jobTestProducer
	callback, _ := url.Parse("https://imytech.net/")
	newConsumer := func(suffix, consumerType string) *data.Consumer {
		consumer, _ := data.NewConsumer(channel, unique+suffix, successfulGetTestToken, callback, consumerType)
		consumer, storeErr := consumerRepo.Store(consumer)
		if storeErr != nil {
			t.Fatal(storeErr)
		}
		return consumer
	}
	return &pauseTestFixture{channel: channel, producer: producer, push: newConsumer("-push", data.PushConsumerStr), pull: newConsumer("-pull", data.PullConsumerStr)}
}

// parkJobFor creates a message and parks its job for the consumer
func (fixture *pauseTestFixture) parkJobFor(t *testing.T, consumer *data.Consumer) (*data.Message, *data.DeliveryJob) {
	t.Helper()
	message, _ := data.NewMessage(fixture.channel, fixture.producer, "payload", "text/plain", data.HeadersMap{})
	if err := messageRepo.Create(message); err != nil {
		t.Fatal(err)
	}
	job, _ := data.NewDeliveryJob(message, consumer)
	if err := storage.NewDeliveryJobRepository(db, messageRepo, consumerRepo).DispatchMessageWithParking(message, nil, []*data.DeliveryJob{job}); err != nil {
		t.Fatal(err)
	}
	return message, job
}

// queueJobFor creates a message with a QUEUED job for the consumer
func (fixture *pauseTestFixture) queueJobFor(t *testing.T, consumer *data.Consumer) *data.DeliveryJob {
	t.Helper()
	message, _ := data.NewMessage(fixture.channel, fixture.producer, "payload", "text/plain", data.HeadersMap{})
	if err := messageRepo.Create(message); err != nil {
		t.Fatal(err)
	}
	job, _ := data.NewDeliveryJob(message, consumer)
	if err := storage.NewDeliveryJobRepository(db, messageRepo, consumerRepo).DispatchMessage(message, job); err != nil {
		t.Fatal(err)
	}
	return job
}

func pauseRepoForTest() storage.DispatchPauseRepository {
	return storage.NewDispatchPauseRepository(db)
}

func parkingRepoForTest() storage.JobParkingRepository {
	return storage.NewJobParkingRepository(db, messageRepo, consumerRepo)
}

func pauseConsumer(t *testing.T, consumer *data.Consumer) {
	t.Helper()
	pause, _ := data.NewConsumerDispatchPause(consumer, "test")
	if _, err := pauseRepoForTest().Pause(pause); err != nil {
		t.Fatal(err)
	}
}

func newPermissiveRegistry() *dispatchermocks.PauseRegistry {
	registry := new(dispatchermocks.PauseRegistry)
	registry.On("Apply", mock.Anything).Return()
	registry.On("Remove", mock.Anything).Return()
	return registry
}

func consumerPauseRequest(t *testing.T, method string, fixture *pauseTestFixture, consumer *data.Consumer, body string) *http.Request {
	t.Helper()
	uri := (&ConsumerDispatchPauseController{}).FormatAsRelativeLink(
		httprouter.Param{Key: channelIDPathParamKey, Value: fixture.channel.ChannelID},
		httprouter.Param{Key: consumerIDPathParamKey, Value: consumer.ConsumerID})
	req, _ := http.NewRequest(method, uri, strings.NewReader(body))
	if body != "" {
		req.Header.Set(headerContentType, formDataContentTypeHeaderValue)
	}
	req.Header.Set(headerChannelToken, fixture.channel.Token)
	req.Header.Set(headerConsumerToken, consumer.Token)
	return req
}

func serve(controller EndpointController, req *http.Request) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	createTestRouter(controller).ServeHTTP(rr, req)
	return rr
}

func TestConsumerDispatchPauseControllerPaths(t *testing.T) {
	controller := NewConsumerDispatchPauseController(nil, nil, nil, nil, nil)
	assert.Equal(t, "/channel/:channelId/consumer/:consumerId/dispatch-pause", controller.GetPath())
	assert.Equal(t, "/channel/ch1/consumer/c1/dispatch-pause", controller.FormatAsRelativeLink(
		httprouter.Param{Key: channelIDPathParamKey, Value: "ch1"}, httprouter.Param{Key: consumerIDPathParamKey, Value: "c1"}))
	assert.Equal(t, "/dispatch-pause", NewGlobalDispatchPauseController(nil, nil, nil, nil).GetPath())
	assert.Equal(t, "/dispatch-pause", NewGlobalDispatchPauseController(nil, nil, nil, nil).FormatAsRelativeLink())
	assert.Equal(t, "/dispatch-pauses", NewDispatchPausesController(nil, nil, nil, nil).GetPath())
	assert.Equal(t, "/dispatch-pauses", NewDispatchPausesController(nil, nil, nil, nil).FormatAsRelativeLink())
}

func TestConsumerDispatchPauseController(t *testing.T) {
	t.Run("PauseGetResume", func(t *testing.T) {
		t.Parallel()
		fixture := newPauseTestFixture(t, "cdp-lifecycle")
		registry := newPermissiveRegistry()
		controller := NewConsumerDispatchPauseController(channelRepo, consumerRepo, pauseRepoForTest(), parkingRepoForTest(), registry)

		rr := serve(controller, consumerPauseRequest(t, http.MethodGet, fixture, fixture.push, ""))
		assert.Equal(t, http.StatusNotFound, rr.Code)

		rr = serve(controller, consumerPauseRequest(t, http.MethodPut, fixture, fixture.push, "reason=first"))
		assert.Equal(t, http.StatusOK, rr.Code)
		model := DispatchPauseModel{}
		assert.NoError(t, json.Unmarshal(rr.Body.Bytes(), &model))
		assert.Equal(t, "consumer", model.Scope)
		assert.Equal(t, fixture.channel.ChannelID, model.ChannelID)
		assert.Equal(t, fixture.push.ConsumerID, model.ConsumerID)
		assert.Equal(t, "first", model.Reason)
		assert.False(t, model.GloballyPaused)
		registry.AssertCalled(t, "Apply", mock.MatchedBy(func(pause *data.DispatchPause) bool { return pause.Scope == fixture.push.ID.String() }))

		rr = serve(controller, consumerPauseRequest(t, http.MethodPut, fixture, fixture.push, "reason=second"))
		assert.Equal(t, http.StatusOK, rr.Code)
		assert.NoError(t, json.Unmarshal(rr.Body.Bytes(), &model))
		assert.Equal(t, "first", model.Reason, "pausing again keeps the original pause")

		fixture.parkJobFor(t, fixture.push)
		rr = serve(controller, consumerPauseRequest(t, http.MethodGet, fixture, fixture.push, ""))
		assert.Equal(t, http.StatusOK, rr.Code)
		assert.NoError(t, json.Unmarshal(rr.Body.Bytes(), &model))
		assert.Equal(t, int64(1), model.ParkedJobCount)

		rr = serve(controller, consumerPauseRequest(t, http.MethodDelete, fixture, fixture.push, ""))
		assert.Equal(t, http.StatusNoContent, rr.Code)
		registry.AssertCalled(t, "Remove", fixture.push.ID.String())
		rr = serve(controller, consumerPauseRequest(t, http.MethodDelete, fixture, fixture.push, ""))
		assert.Equal(t, http.StatusNotFound, rr.Code)
	})
	t.Run("ReasonTooLong", func(t *testing.T) {
		t.Parallel()
		fixture := newPauseTestFixture(t, "cdp-reason")
		controller := NewConsumerDispatchPauseController(channelRepo, consumerRepo, pauseRepoForTest(), parkingRepoForTest(), newPermissiveRegistry())
		rr := serve(controller, consumerPauseRequest(t, http.MethodPut, fixture, fixture.push, "reason="+strings.Repeat("r", data.MaxDispatchPauseReasonLength+1)))
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})
	t.Run("AuthMatrix", func(t *testing.T) {
		t.Parallel()
		fixture := newPauseTestFixture(t, "cdp-auth")
		controller := NewConsumerDispatchPauseController(channelRepo, consumerRepo, pauseRepoForTest(), parkingRepoForTest(), newPermissiveRegistry())
		for _, method := range []string{http.MethodPut, http.MethodGet, http.MethodDelete} {
			req := consumerPauseRequest(t, method, fixture, fixture.push, "")
			req.Header.Set(headerChannelToken, "wrong")
			assert.Equal(t, http.StatusForbidden, serve(controller, req).Code, method+" wrong channel token")
			req = consumerPauseRequest(t, method, fixture, fixture.push, "")
			req.Header.Set(headerConsumerToken, "wrong")
			assert.Equal(t, http.StatusForbidden, serve(controller, req).Code, method+" wrong consumer token")
			unknown := &data.Consumer{ConsumerID: "unknown-consumer", MessageStakeholder: data.MessageStakeholder{Token: "x"}}
			assert.Equal(t, http.StatusNotFound, serve(controller, consumerPauseRequest(t, method, fixture, unknown, "")).Code, method+" unknown consumer")
		}
	})
	t.Run("RepositoryErrors", func(t *testing.T) {
		t.Parallel()
		fixture := newPauseTestFixture(t, "cdp-errors")
		failingRepo := new(storagemocks.DispatchPauseRepository)
		failingRepo.On("Pause", mock.Anything).Return(nil, errors.New("db down"))
		failingRepo.On("Get", mock.Anything).Return(nil, errors.New("db down"))
		failingRepo.On("Resume", mock.Anything).Return(false, errors.New("db down"))
		controller := NewConsumerDispatchPauseController(channelRepo, consumerRepo, failingRepo, parkingRepoForTest(), newPermissiveRegistry())
		for _, method := range []string{http.MethodPut, http.MethodGet, http.MethodDelete} {
			assert.Equal(t, http.StatusInternalServerError, serve(controller, consumerPauseRequest(t, method, fixture, fixture.push, "")).Code, method)
		}
	})
}

func globalPauseRequest(method, token string) *http.Request {
	req, _ := http.NewRequest(method, globalDispatchPausePath, strings.NewReader("reason=incident"))
	req.Header.Set(headerContentType, formDataContentTypeHeaderValue)
	if token != "" {
		req.Header.Set(parking.AdminTokenHeader, token)
	}
	return req
}

func TestGlobalDispatchPauseController(t *testing.T) {
	registry := newPermissiveRegistry()
	t.Run("DisabledWithoutToken", func(t *testing.T) {
		controller := NewGlobalDispatchPauseController(pauseRepoForTest(), parkingRepoForTest(), registry, &config.Config{})
		assert.Equal(t, http.StatusNotFound, serve(controller, globalPauseRequest(http.MethodPut, "anything")).Code)
		assert.Equal(t, http.StatusNotFound, serve(controller, globalPauseRequest(http.MethodDelete, "anything")).Code)
	})
	controller := NewGlobalDispatchPauseController(pauseRepoForTest(), parkingRepoForTest(), registry, &config.Config{DispatchPauseAdminToken: testAdminToken})
	t.Run("WrongToken", func(t *testing.T) {
		assert.Equal(t, http.StatusUnauthorized, serve(controller, globalPauseRequest(http.MethodPut, "wrong")).Code)
		assert.Equal(t, http.StatusUnauthorized, serve(controller, globalPauseRequest(http.MethodDelete, "")).Code)
	})
	t.Run("PauseAndResume", func(t *testing.T) {
		defer pauseRepoForTest().Resume(data.GlobalDispatchPauseScope)
		rr := serve(controller, globalPauseRequest(http.MethodPut, testAdminToken))
		assert.Equal(t, http.StatusOK, rr.Code)
		model := DispatchPauseModel{}
		assert.NoError(t, json.Unmarshal(rr.Body.Bytes(), &model))
		assert.Equal(t, "global", model.Scope)
		assert.Equal(t, "incident", model.Reason)
		assert.True(t, model.GloballyPaused)
		assert.Empty(t, model.ConsumerID)
		registry.AssertCalled(t, "Apply", mock.MatchedBy(func(pause *data.DispatchPause) bool { return pause.IsGlobal() }))
		assert.Equal(t, http.StatusOK, serve(controller, globalPauseRequest(http.MethodPut, testAdminToken)).Code, "idempotent")
		assert.Equal(t, http.StatusNoContent, serve(controller, globalPauseRequest(http.MethodDelete, testAdminToken)).Code)
		registry.AssertCalled(t, "Remove", data.GlobalDispatchPauseScope)
		assert.Equal(t, http.StatusNotFound, serve(controller, globalPauseRequest(http.MethodDelete, testAdminToken)).Code)
	})
}

func TestDispatchPausesController(t *testing.T) {
	fixture := newPauseTestFixture(t, "dp-list")
	pauseConsumer(t, fixture.push)
	fixture.parkJobFor(t, fixture.push)
	fixture.parkJobFor(t, fixture.pull) // parked while paused earlier, consumer now resumed: still draining
	listRequest := func(token string) *http.Request {
		req, _ := http.NewRequest(http.MethodGet, dispatchPausesPath, nil)
		if token != "" {
			req.Header.Set(parking.AdminTokenHeader, token)
		}
		return req
	}
	t.Run("OpenWithoutToken", func(t *testing.T) {
		controller := NewDispatchPausesController(pauseRepoForTest(), parkingRepoForTest(), consumerRepo, &config.Config{})
		rr := serve(controller, listRequest(""))
		assert.Equal(t, http.StatusOK, rr.Code)
		model := DispatchPausesModel{}
		assert.NoError(t, json.Unmarshal(rr.Body.Bytes(), &model))
		foundPause := false
		for _, pause := range model.Pauses {
			if pause.ConsumerID == fixture.push.ConsumerID {
				foundPause = true
				assert.Equal(t, fixture.channel.ChannelID, pause.ChannelID)
				assert.Equal(t, int64(1), pause.ParkedJobCount)
			}
		}
		assert.True(t, foundPause)
		parkedPaused := map[string]bool{}
		for _, parked := range model.Parked {
			if parked.ChannelID == fixture.channel.ChannelID {
				parkedPaused[parked.ConsumerID] = parked.Paused
				assert.Equal(t, int64(1), parked.Count)
			}
		}
		assert.Equal(t, map[string]bool{fixture.push.ConsumerID: true, fixture.pull.ConsumerID: false}, parkedPaused)
	})
	t.Run("RequiresConfiguredToken", func(t *testing.T) {
		controller := NewDispatchPausesController(pauseRepoForTest(), parkingRepoForTest(), consumerRepo, &config.Config{DispatchPauseAdminToken: testAdminToken})
		assert.Equal(t, http.StatusUnauthorized, serve(controller, listRequest("")).Code)
		assert.Equal(t, http.StatusOK, serve(controller, listRequest(testAdminToken)).Code)
	})
	t.Run("RepositoryErrors", func(t *testing.T) {
		failingPauses := new(storagemocks.DispatchPauseRepository)
		failingPauses.On("GetAll").Return(nil, errors.New("db down"))
		assert.Equal(t, http.StatusInternalServerError, serve(NewDispatchPausesController(failingPauses, parkingRepoForTest(), consumerRepo, &config.Config{}), listRequest("")).Code)
		failingParking := new(storagemocks.JobParkingRepository)
		failingParking.On("GetCountsByConsumer").Return(nil, errors.New("db down"))
		assert.Equal(t, http.StatusInternalServerError, serve(NewDispatchPausesController(pauseRepoForTest(), failingParking, consumerRepo, &config.Config{}), listRequest("")).Code)
	})
}

func TestConsumerControllerDispatchPaused(t *testing.T) {
	fixture := newPauseTestFixture(t, "consumer-model")
	registry := new(dispatchermocks.PauseRegistry)
	registry.On("IsEffectivelyPaused", fixture.push.ID.String()).Return(true)
	registry.On("IsEffectivelyPaused", fixture.pull.ID.String()).Return(false)
	controller := NewConsumerControllerWithPauseRegistry(channelRepo, consumerRepo, getDLQControllerWithMockedRepo(), registry)
	get := func(controller *ConsumerController, consumer *data.Consumer) ConsumerModel {
		req, _ := http.NewRequest(http.MethodGet, controller.FormatAsRelativeLink(
			httprouter.Param{Key: channelIDPathParamKey, Value: fixture.channel.ChannelID},
			httprouter.Param{Key: consumerIDPathParamKey, Value: consumer.ConsumerID}), nil)
		rr := serve(controller, req)
		assert.Equal(t, http.StatusOK, rr.Code)
		model := ConsumerModel{}
		assert.NoError(t, json.Unmarshal(rr.Body.Bytes(), &model))
		return model
	}
	assert.True(t, get(controller, fixture.push).DispatchPaused)
	assert.False(t, get(controller, fixture.pull).DispatchPaused)
	assert.False(t, get(getNewConsumerController(consumerRepo), fixture.push).DispatchPaused, "no registry means not paused")
}

func TestMessageControllerListsParkedJobs(t *testing.T) {
	fixture := newPauseTestFixture(t, "message-parked")
	message, job := fixture.parkJobFor(t, fixture.push)
	controller := getMessageController()
	req, _ := http.NewRequest(http.MethodGet, controller.FormatAsRelativeLink(
		httprouter.Param{Key: channelIDPathParamKey, Value: fixture.channel.ChannelID},
		httprouter.Param{Key: messageIDParamKey, Value: message.MessageID}), nil)
	rr := serve(controller, req)
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, rr.Body.String(), `"Status":"PARKED"`)
	assert.NotEmpty(t, job.ID.String())

	failingParking := new(storagemocks.JobParkingRepository)
	failingParking.On("GetForMessage", mock.Anything).Return(nil, errors.New("db down"))
	failing := NewMessageController(messageRepo, storage.NewDeliveryJobRepository(db, messageRepo, consumerRepo), failingParking)
	assert.Equal(t, http.StatusInternalServerError, serve(failing, req).Code)
}

func jobRequest(t *testing.T, method string, fixture *pauseTestFixture, consumer *data.Consumer, job *data.DeliveryJob, body string) *http.Request {
	t.Helper()
	uri := (&JobController{}).FormatAsRelativeLink(
		httprouter.Param{Key: channelIDPathParamKey, Value: fixture.channel.ChannelID},
		httprouter.Param{Key: consumerIDPathParamKey, Value: consumer.ConsumerID},
		httprouter.Param{Key: jobIDPathParamKey, Value: job.ID.String()})
	req, _ := http.NewRequest(method, uri, strings.NewReader(body))
	req.Header.Set(headerContentType, jobPostTestContentType)
	req.Header.Set(headerChannelToken, fixture.channel.Token)
	req.Header.Set(headerConsumerToken, consumer.Token)
	return req
}

func TestJobControllerPauseBehavior(t *testing.T) {
	jobController := getJobController()
	t.Run("ParkedJob", func(t *testing.T) {
		t.Parallel()
		fixture := newPauseTestFixture(t, "job-parked")
		_, job := fixture.parkJobFor(t, fixture.pull)
		rr := serve(jobController, jobRequest(t, http.MethodGet, fixture, fixture.pull, job, ""))
		assert.Equal(t, http.StatusOK, rr.Code)
		assert.Contains(t, rr.Body.String(), `"Status":"PARKED"`)
		rr = serve(jobController, jobRequest(t, http.MethodPost, fixture, fixture.pull, job, `{"NextState":"INFLIGHT"}`))
		assert.Equal(t, http.StatusConflict, rr.Code)
		assert.Equal(t, errDispatchPaused.Error(), rr.Body.String())
	})
	t.Run("QueuedToInflightWhilePaused", func(t *testing.T) {
		t.Parallel()
		fixture := newPauseTestFixture(t, "job-queued-paused")
		job := fixture.queueJobFor(t, fixture.pull)
		pauseConsumer(t, fixture.pull)
		rr := serve(jobController, jobRequest(t, http.MethodPost, fixture, fixture.pull, job, `{"NextState":"INFLIGHT"}`))
		assert.Equal(t, http.StatusConflict, rr.Code)
	})
	t.Run("DeadToInflightWhilePausedAndFinishInflight", func(t *testing.T) {
		t.Parallel()
		fixture := newPauseTestFixture(t, "job-dead-paused")
		djRepo := storage.NewDeliveryJobRepository(db, messageRepo, consumerRepo)
		deadJob := fixture.queueJobFor(t, fixture.pull)
		assert.NoError(t, djRepo.MarkJobInflight(deadJob))
		assert.NoError(t, djRepo.MarkJobDead(deadJob))
		inflightJob := fixture.queueJobFor(t, fixture.pull)
		assert.NoError(t, djRepo.MarkJobInflight(inflightJob))
		pauseConsumer(t, fixture.pull)
		rr := serve(jobController, jobRequest(t, http.MethodPost, fixture, fixture.pull, deadJob, `{"NextState":"INFLIGHT"}`))
		assert.Equal(t, http.StatusConflict, rr.Code)
		rr = serve(jobController, jobRequest(t, http.MethodPost, fixture, fixture.pull, inflightJob, `{"NextState":"DELIVERED"}`))
		assert.Equal(t, http.StatusAccepted, rr.Code, "work already in flight may finish while paused")
	})
	t.Run("RequeueAndDeleteOnParkedJob", func(t *testing.T) {
		t.Parallel()
		fixture := newPauseTestFixture(t, "job-parked-dlq")
		_, job := fixture.parkJobFor(t, fixture.pull)
		djRepo := storage.NewDeliveryJobRepository(db, messageRepo, consumerRepo)
		requeueController := NewJobRequeueController(djRepo, channelRepo, consumerRepo)
		requeueURI := requeueController.FormatAsRelativeLink(
			httprouter.Param{Key: channelIDPathParamKey, Value: fixture.channel.ChannelID},
			httprouter.Param{Key: consumerIDPathParamKey, Value: fixture.pull.ConsumerID},
			httprouter.Param{Key: jobIDPathParamKey, Value: job.ID.String()})
		req, _ := http.NewRequest(http.MethodPost, requeueURI, nil)
		req.Header.Set(headerChannelToken, fixture.channel.Token)
		req.Header.Set(headerConsumerToken, fixture.pull.Token)
		assert.Equal(t, http.StatusBadRequest, serve(requeueController, req).Code)
		deleteController := NewDeadJobDeleteController(channelRepo, consumerRepo, djRepo, new(storagemocks.DLQSummaryRepository), newMockBrokerConfig(), dlqDeleteMetrics)
		req = jobRequest(t, http.MethodDelete, fixture, fixture.pull, job, "")
		assert.Equal(t, http.StatusNotFound, serve(deleteController, req).Code)
	})
}

func TestJobsControllerQueuedJobsWhilePaused(t *testing.T) {
	fixture := newPauseTestFixture(t, "queued-jobs-paused")
	fixture.queueJobFor(t, fixture.pull)
	controller := NewJobsController(channelRepo, consumerRepo, storage.NewDeliveryJobRepository(db, messageRepo, consumerRepo), pauseRepoForTest())
	request := func() *http.Request {
		req, _ := http.NewRequest(http.MethodGet, controller.FormatAsRelativeLink(
			httprouter.Param{Key: channelIDPathParamKey, Value: fixture.channel.ChannelID},
			httprouter.Param{Key: consumerIDPathParamKey, Value: fixture.pull.ConsumerID}), nil)
		req.Header.Set(headerChannelToken, fixture.channel.Token)
		req.Header.Set(headerConsumerToken, fixture.pull.Token)
		return req
	}
	rr := serve(controller, request())
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Empty(t, rr.Header().Get(headerDispatchPaused))
	result := JobListResult{}
	assert.NoError(t, json.Unmarshal(rr.Body.Bytes(), &result))
	assert.Equal(t, 1, len(result.Result))

	pauseConsumer(t, fixture.pull)
	rr = serve(controller, request())
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "true", rr.Header().Get(headerDispatchPaused))
	assert.NoError(t, json.Unmarshal(rr.Body.Bytes(), &result))
	assert.Empty(t, result.Result)
}

func TestRouterRegistersDispatchPauseRoutes(t *testing.T) {
	assert.NotPanics(t, func() {
		NewRouter(&Controllers{
			StatusController: NewStatusController(new(storagemocks.AppRepository)), ProducersController: &ProducersController{}, ProducerController: &ProducerController{},
			ChannelController: &ChannelController{}, ConsumerController: &ConsumerController{}, JobsController: &JobsController{}, JobController: &JobController{},
			DLQController: &DLQController{}, JobRequeueController: &JobRequeueController{}, DLQPurgeController: &DLQPurgeController{},
			DeadJobDeleteController: &DeadJobDeleteController{}, InternalJobEnqueueController: &InternalJobEnqueueController{},
			ConsumerDispatchPauseController: &ConsumerDispatchPauseController{}, GlobalDispatchPauseController: &GlobalDispatchPauseController{},
			DispatchPausesController: &DispatchPausesController{},
		})
	})
}

// Generated with assistance from Claude AI

func TestDispatchPauseControllerFailurePaths(t *testing.T) {
	t.Run("ConsumerCountError", func(t *testing.T) {
		fixture := newPauseTestFixture(t, "cdp-count-error")
		failingParking := new(storagemocks.JobParkingRepository)
		failingParking.On("CountForConsumer", mock.Anything).Return(int64(0), errors.New("db down"))
		controller := NewConsumerDispatchPauseController(channelRepo, consumerRepo, pauseRepoForTest(), failingParking, newPermissiveRegistry())
		assert.Equal(t, http.StatusInternalServerError, serve(controller, consumerPauseRequest(t, http.MethodPut, fixture, fixture.push, "")).Code)
	})
	adminConfig := &config.Config{DispatchPauseAdminToken: testAdminToken}
	t.Run("GlobalReasonTooLong", func(t *testing.T) {
		controller := NewGlobalDispatchPauseController(pauseRepoForTest(), parkingRepoForTest(), newPermissiveRegistry(), adminConfig)
		req, _ := http.NewRequest(http.MethodPut, globalDispatchPausePath, strings.NewReader("reason="+strings.Repeat("r", data.MaxDispatchPauseReasonLength+1)))
		req.Header.Set(headerContentType, formDataContentTypeHeaderValue)
		req.Header.Set(parking.AdminTokenHeader, testAdminToken)
		assert.Equal(t, http.StatusBadRequest, serve(controller, req).Code)
	})
	t.Run("GlobalRepositoryErrors", func(t *testing.T) {
		failingPauses := new(storagemocks.DispatchPauseRepository)
		failingPauses.On("Pause", mock.Anything).Return(nil, errors.New("db down"))
		failingPauses.On("Resume", mock.Anything).Return(false, errors.New("db down"))
		controller := NewGlobalDispatchPauseController(failingPauses, parkingRepoForTest(), newPermissiveRegistry(), adminConfig)
		assert.Equal(t, http.StatusInternalServerError, serve(controller, globalPauseRequest(http.MethodPut, testAdminToken)).Code)
		assert.Equal(t, http.StatusInternalServerError, serve(controller, globalPauseRequest(http.MethodDelete, testAdminToken)).Code)
	})
	t.Run("GlobalCountError", func(t *testing.T) {
		pauses := new(storagemocks.DispatchPauseRepository)
		pauses.On("Pause", mock.Anything).Return(data.NewGlobalDispatchPause(""), nil)
		failingParking := new(storagemocks.JobParkingRepository)
		failingParking.On("GetCountsByConsumer").Return(nil, errors.New("db down"))
		controller := NewGlobalDispatchPauseController(pauses, failingParking, newPermissiveRegistry(), adminConfig)
		assert.Equal(t, http.StatusInternalServerError, serve(controller, globalPauseRequest(http.MethodPut, testAdminToken)).Code)
	})
	t.Run("ListWithUnknownConsumers", func(t *testing.T) {
		pauses := new(storagemocks.DispatchPauseRepository)
		pauses.On("GetAll").Return([]*data.DispatchPause{{Scope: "gone", ConsumerID: "gone", Reason: "r"}}, nil)
		parked := new(storagemocks.JobParkingRepository)
		parked.On("GetCountsByConsumer").Return([]*data.ParkedJobCount{{ChannelID: "ch", ConsumerID: "gone", Count: 2}}, nil)
		consumers := new(storagemocks.ConsumerRepository)
		consumers.On("GetByID", "gone").Return(nil, errors.New("not found"))
		req, _ := http.NewRequest(http.MethodGet, dispatchPausesPath, nil)
		rr := serve(NewDispatchPausesController(pauses, parked, consumers, &config.Config{}), req)
		assert.Equal(t, http.StatusOK, rr.Code)
		model := DispatchPausesModel{}
		assert.NoError(t, json.Unmarshal(rr.Body.Bytes(), &model))
		if assert.Equal(t, 1, len(model.Pauses)) {
			assert.Equal(t, "gone", model.Pauses[0].ConsumerID, "falls back to the internal id when the consumer can not be loaded")
			assert.Equal(t, int64(2), model.Pauses[0].ParkedJobCount)
		}
		if assert.Equal(t, 1, len(model.Parked)) {
			assert.Equal(t, "gone", model.Parked[0].ConsumerID)
			assert.True(t, model.Parked[0].Paused)
		}
	})
}
