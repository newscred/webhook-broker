package controllers

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/julienschmidt/httprouter"
	"github.com/rs/xid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/newscred/webhook-broker/config"
	dispatchermocks "github.com/newscred/webhook-broker/dispatcher/mocks"
	"github.com/newscred/webhook-broker/parking"
	"github.com/newscred/webhook-broker/storage/data"
	storagemocks "github.com/newscred/webhook-broker/storage/mocks"
)

const testAdminToken = "test-admin-token"

func newInternalTestConfig(token string) *config.Config {
	return &config.Config{DispatchPauseAdminToken: token, DrainPageSize: 3, HandoffHighWaterMark: 10}
}

func enqueueRequest(t *testing.T, token string, ids ...string) *http.Request {
	t.Helper()
	body, _ := json.Marshal(parking.EnqueueRequest{JobIDs: ids})
	req, _ := http.NewRequest(http.MethodPost, parking.InternalEnqueuePath, bytes.NewReader(body))
	req.Header.Set(headerContentType, "application/json")
	if token != "" {
		req.Header.Set(parking.AdminTokenHeader, token)
	}
	return req
}

func serveInternal(controller *InternalJobEnqueueController, req *http.Request) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	createTestRouter(controller).ServeHTTP(rr, req)
	return rr
}

func TestInternalJobEnqueueControllerPaths(t *testing.T) {
	controller := NewInternalJobEnqueueController(nil, nil, nil)
	assert.Equal(t, "/_internal/jobs/enqueue", controller.GetPath())
	assert.Equal(t, "/_internal/jobs/enqueue", controller.FormatAsRelativeLink(httprouter.Param{Key: "x", Value: "y"}))
}

func TestInternalJobEnqueueControllerPost(t *testing.T) {
	t.Run("NoTokenConfigured", func(t *testing.T) {
		t.Parallel()
		controller := NewInternalJobEnqueueController(new(storagemocks.DeliveryJobRepository), new(dispatchermocks.MessageDispatcher), newInternalTestConfig(""))
		rr := serveInternal(controller, enqueueRequest(t, testAdminToken, "a"))
		assert.Equal(t, http.StatusNotFound, rr.Code)
	})
	t.Run("MissingOrWrongToken", func(t *testing.T) {
		t.Parallel()
		controller := NewInternalJobEnqueueController(new(storagemocks.DeliveryJobRepository), new(dispatchermocks.MessageDispatcher), newInternalTestConfig(testAdminToken))
		assert.Equal(t, http.StatusUnauthorized, serveInternal(controller, enqueueRequest(t, "", "a")).Code)
		assert.Equal(t, http.StatusUnauthorized, serveInternal(controller, enqueueRequest(t, "wrong", "a")).Code)
	})
	t.Run("MalformedBody", func(t *testing.T) {
		t.Parallel()
		controller := NewInternalJobEnqueueController(new(storagemocks.DeliveryJobRepository), new(dispatchermocks.MessageDispatcher), newInternalTestConfig(testAdminToken))
		req, _ := http.NewRequest(http.MethodPost, parking.InternalEnqueuePath, bytes.NewReader([]byte("{not json")))
		req.Header.Set(parking.AdminTokenHeader, testAdminToken)
		assert.Equal(t, http.StatusBadRequest, serveInternal(controller, req).Code)
	})
	t.Run("TooManyIDs", func(t *testing.T) {
		t.Parallel()
		controller := NewInternalJobEnqueueController(new(storagemocks.DeliveryJobRepository), new(dispatchermocks.MessageDispatcher), newInternalTestConfig(testAdminToken))
		rr := serveInternal(controller, enqueueRequest(t, testAdminToken, "a", "b", "c", "d"))
		assert.Equal(t, http.StatusBadRequest, rr.Code)
		assert.Contains(t, rr.Body.String(), errTooManyJobIDs.Error())
	})
	t.Run("BusyAboveHighWaterMark", func(t *testing.T) {
		t.Parallel()
		messageDispatcher := new(dispatchermocks.MessageDispatcher)
		messageDispatcher.On("QueueDepth").Return(9)
		djRepo := new(storagemocks.DeliveryJobRepository)
		controller := NewInternalJobEnqueueController(djRepo, messageDispatcher, newInternalTestConfig(testAdminToken))
		rr := serveInternal(controller, enqueueRequest(t, testAdminToken, "a", "b"))
		assert.Equal(t, http.StatusServiceUnavailable, rr.Code)
		assert.Equal(t, "1", rr.Header().Get("Retry-After"))
		djRepo.AssertNotCalled(t, "GetQueuedJobsByIDs", mock.Anything)
	})
	t.Run("Accepted", func(t *testing.T) {
		t.Parallel()
		ids := []string{xid.New().String(), xid.New().String()}
		jobs := []*data.DeliveryJob{{}}
		djRepo := new(storagemocks.DeliveryJobRepository)
		djRepo.On("GetQueuedJobsByIDs", ids).Return(jobs, nil)
		messageDispatcher := new(dispatchermocks.MessageDispatcher)
		messageDispatcher.On("QueueDepth").Return(0)
		messageDispatcher.On("EnqueueJobs", jobs).Return(1)
		controller := NewInternalJobEnqueueController(djRepo, messageDispatcher, newInternalTestConfig(testAdminToken))
		rr := serveInternal(controller, enqueueRequest(t, testAdminToken, ids...))
		assert.Equal(t, http.StatusAccepted, rr.Code)
		assert.Equal(t, "application/json", rr.Header().Get(headerContentType))
		response := parking.EnqueueResponse{}
		assert.NoError(t, json.Unmarshal(rr.Body.Bytes(), &response))
		assert.Equal(t, 1, response.Accepted, "accepted excludes jobs that were no longer QUEUED or got re-parked")
		djRepo.AssertExpectations(t)
		messageDispatcher.AssertExpectations(t)
	})
	t.Run("RepositoryError", func(t *testing.T) {
		t.Parallel()
		djRepo := new(storagemocks.DeliveryJobRepository)
		djRepo.On("GetQueuedJobsByIDs", mock.Anything).Return(nil, errors.New("db down"))
		messageDispatcher := new(dispatchermocks.MessageDispatcher)
		messageDispatcher.On("QueueDepth").Return(0)
		controller := NewInternalJobEnqueueController(djRepo, messageDispatcher, newInternalTestConfig(testAdminToken))
		assert.Equal(t, http.StatusInternalServerError, serveInternal(controller, enqueueRequest(t, testAdminToken, "a")).Code)
	})
}

// The internal endpoint is not exempt from gateway HMAC auth: an unsigned hand-off is rejected and one signed with
// parking.SignPayload (what the drainer sends) passes the real middleware
func TestInternalJobEnqueueControllerBehindHMACMiddleware(t *testing.T) {
	djRepo := new(storagemocks.DeliveryJobRepository)
	djRepo.On("GetQueuedJobsByIDs", mock.Anything).Return([]*data.DeliveryJob{}, nil)
	messageDispatcher := new(dispatchermocks.MessageDispatcher)
	messageDispatcher.On("QueueDepth").Return(0)
	messageDispatcher.On("EnqueueJobs", mock.Anything).Return(0)
	controller := NewInternalJobEnqueueController(djRepo, messageDispatcher, newInternalTestConfig(testAdminToken))
	apiRouter := httprouter.New()
	setupAPIRoutes(apiRouter, controller)
	handler := getHandler(apiRouter, &testGatewayAuthConfig{mode: "hmac", secret: testSecret, tolerance: 5 * time.Minute, exempt: []string{"/_status", "/metrics"}})

	unsigned := httptest.NewRecorder()
	handler.ServeHTTP(unsigned, enqueueRequest(t, testAdminToken, "a"))
	assert.Equal(t, http.StatusUnauthorized, unsigned.Code)

	body, _ := json.Marshal(parking.EnqueueRequest{JobIDs: []string{"a"}})
	signedRequest, _ := http.NewRequest(http.MethodPost, parking.InternalEnqueuePath, bytes.NewReader(body))
	signedRequest.Header.Set(parking.AdminTokenHeader, testAdminToken)
	signedRequest.Header.Set(parking.SignatureHeader, parking.SignPayload(testSecret, time.Now().Unix(), body))
	signed := httptest.NewRecorder()
	handler.ServeHTTP(signed, signedRequest)
	assert.Equal(t, http.StatusAccepted, signed.Code)
}

// Generated with assistance from Claude AI
