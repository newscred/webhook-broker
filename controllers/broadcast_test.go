package controllers

import (
	"bytes"
	"database/sql"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/julienschmidt/httprouter"
	dispatchermocks "github.com/newscred/webhook-broker/dispatcher/mocks"
	"github.com/newscred/webhook-broker/storage"
	"github.com/newscred/webhook-broker/storage/data"
	storagemocks "github.com/newscred/webhook-broker/storage/mocks"
	"github.com/rs/xid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

var (
	messageRepo          storage.MessageRepository
	scheduledMessageRepo storage.ScheduledMessageRepository
)

func BroadcastTestSetup() {
	messageRepo = storage.NewMessageRepository(db, channelRepo, producerRepo)
	scheduledMessageRepo = storage.NewScheduledMessageRepository(db, channelRepo, producerRepo)
}

func getNewBroadcastController(msgRepo storage.MessageRepository) (*BroadcastController, *dispatchermocks.MessageDispatcher) {
	mockDispatcher := new(dispatchermocks.MessageDispatcher)
	mockScheduledMsgRepo := new(storagemocks.ScheduledMessageRepository)
	return NewBroadcastController(channelRepo, msgRepo, producerRepo, mockScheduledMsgRepo, mockDispatcher), mockDispatcher
}

func getNewBroadcastControllerWithScheduledRepo(msgRepo storage.MessageRepository, scheduledMsgRepo storage.ScheduledMessageRepository) (*BroadcastController, *dispatchermocks.MessageDispatcher) {
	mockDispatcher := new(dispatchermocks.MessageDispatcher)
	return NewBroadcastController(channelRepo, msgRepo, producerRepo, scheduledMsgRepo, mockDispatcher), mockDispatcher
}

type mockCloser struct {
}

func (mockCloser) Read(p []byte) (n int, err error) {
	return 0, sql.ErrNoRows
}

func (mockCloser) Close() error { return nil }

func TestBroadcastControllerFormatAsRelativeLink(t *testing.T) {
	controller, mockdispatcher := getNewBroadcastController(messageRepo)
	url := controller.FormatAsRelativeLink(httprouter.Param{Key: channelIDPathParamKey, Value: consumerTestChannel.ChannelID})
	assert.Equal(t, "/channel/"+channelTestConsumerID+"/broadcast", url)
	mockdispatcher.AssertExpectations(t)
}

func getRouterParam(channelID string) httprouter.Param {
	return httprouter.Param{Key: channelIDPathParamKey, Value: channelID}
}

func TestBroadcastControllerPost(t *testing.T) {
	t.Run("404", func(t *testing.T) {
		t.Parallel()
		msgRepo := new(storagemocks.MessageRepository)
		controller, mockDispatcher := getNewBroadcastController(msgRepo)
		testRouter := createTestRouter(controller)
		testURI := controller.FormatAsRelativeLink(getRouterParam("broadcast-channel-404"))
		req, _ := http.NewRequest("POST", testURI, nil)
		bodyString := "test message body"
		req.Body = io.NopCloser(strings.NewReader(bodyString))
		req.Header.Add(headerContentType, formDataContentTypeHeaderValue)
		priority := 11
		req.Header.Add(headerPriority, strconv.Itoa(priority))
		req.Header.Add(headerChannelToken, consumerTestChannel.Token)
		indexString := "0"
		producerID := listTestProducerIDPrefix + indexString
		req.Header.Add(headerProducerID, producerID)
		req.Header.Add(headerProducerToken, successfulGetTestToken+" - "+indexString)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusNotFound, rr.Code)
		msgRepo.AssertExpectations(t)
		mockDispatcher.AssertExpectations(t)
	})
	t.Run("403:ChannelToken", func(t *testing.T) {
		t.Parallel()
		msgRepo := new(storagemocks.MessageRepository)
		controller, mockDispatcher := getNewBroadcastController(msgRepo)
		testRouter := createTestRouter(controller)
		testURI := controller.FormatAsRelativeLink(getRouterParam(consumerTestChannel.ChannelID))
		req, _ := http.NewRequest("POST", testURI, nil)
		bodyString := "test message body"
		req.Body = io.NopCloser(strings.NewReader(bodyString))
		req.Header.Add(headerContentType, formDataContentTypeHeaderValue)
		priority := 11
		req.Header.Add(headerPriority, strconv.Itoa(priority))
		req.Header.Add(headerChannelToken, "no-such-token-for-broadcast")
		indexString := "0"
		producerID := listTestProducerIDPrefix + indexString
		req.Header.Add(headerProducerID, producerID)
		req.Header.Add(headerProducerToken, successfulGetTestToken+" - "+indexString)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusForbidden, rr.Code)
		msgRepo.AssertExpectations(t)
		mockDispatcher.AssertExpectations(t)
	})
	t.Run("401:NoProducerID", func(t *testing.T) {
		t.Parallel()
		msgRepo := new(storagemocks.MessageRepository)
		controller, mockDispatcher := getNewBroadcastController(msgRepo)
		testRouter := createTestRouter(controller)
		testURI := controller.FormatAsRelativeLink(getRouterParam(consumerTestChannel.ChannelID))
		req, _ := http.NewRequest("POST", testURI, nil)
		bodyString := "test message body"
		req.Body = io.NopCloser(strings.NewReader(bodyString))
		req.Header.Add(headerContentType, formDataContentTypeHeaderValue)
		priority := 11
		req.Header.Add(headerPriority, strconv.Itoa(priority))
		req.Header.Add(headerChannelToken, consumerTestChannel.Token)
		indexString := "0"
		req.Header.Add(headerProducerToken, successfulGetTestToken+" - "+indexString)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusUnauthorized, rr.Code)
		msgRepo.AssertExpectations(t)
		mockDispatcher.AssertExpectations(t)
	})
	t.Run("401:ProducerDoesNotExist", func(t *testing.T) {
		t.Parallel()
		msgRepo := new(storagemocks.MessageRepository)
		controller, mockDispatcher := getNewBroadcastController(msgRepo)
		testRouter := createTestRouter(controller)
		testURI := controller.FormatAsRelativeLink(getRouterParam(consumerTestChannel.ChannelID))
		req, _ := http.NewRequest("POST", testURI, nil)
		bodyString := "test message body"
		req.Body = io.NopCloser(strings.NewReader(bodyString))
		req.Header.Add(headerContentType, formDataContentTypeHeaderValue)
		priority := 11
		req.Header.Add(headerPriority, strconv.Itoa(priority))
		req.Header.Add(headerChannelToken, consumerTestChannel.Token)
		indexString := "0"
		producerID := "no-such-producer-id-for-broadcast"
		req.Header.Add(headerProducerID, producerID)
		req.Header.Add(headerProducerToken, successfulGetTestToken+" - "+indexString)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusUnauthorized, rr.Code)
		msgRepo.AssertExpectations(t)
		mockDispatcher.AssertExpectations(t)
	})
	t.Run("403:ProducerToken", func(t *testing.T) {
		t.Parallel()
		msgRepo := new(storagemocks.MessageRepository)
		controller, mockDispatcher := getNewBroadcastController(msgRepo)
		testRouter := createTestRouter(controller)
		testURI := controller.FormatAsRelativeLink(getRouterParam(consumerTestChannel.ChannelID))
		req, _ := http.NewRequest("POST", testURI, nil)
		bodyString := "test message body"
		req.Body = io.NopCloser(strings.NewReader(bodyString))
		req.Header.Add(headerContentType, formDataContentTypeHeaderValue)
		priority := 11
		req.Header.Add(headerPriority, strconv.Itoa(priority))
		req.Header.Add(headerChannelToken, consumerTestChannel.Token)
		indexString := "0"
		producerID := listTestProducerIDPrefix + indexString
		req.Header.Add(headerProducerID, producerID)
		req.Header.Add(headerProducerToken, " - ")
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusForbidden, rr.Code)
		msgRepo.AssertExpectations(t)
		mockDispatcher.AssertExpectations(t)
	})
	t.Run("500:BodyRead", func(t *testing.T) {
		t.Parallel()
		msgRepo := new(storagemocks.MessageRepository)
		controller, mockDispatcher := getNewBroadcastController(msgRepo)
		testRouter := createTestRouter(controller)
		testURI := controller.FormatAsRelativeLink(getRouterParam(consumerTestChannel.ChannelID))
		req, _ := http.NewRequest("POST", testURI, nil)
		req.Body = &mockCloser{}
		req.Header.Add(headerContentType, formDataContentTypeHeaderValue)
		priority := 11
		req.Header.Add(headerPriority, strconv.Itoa(priority))
		req.Header.Add(headerChannelToken, consumerTestChannel.Token)
		indexString := "0"
		producerID := listTestProducerIDPrefix + indexString
		req.Header.Add(headerProducerID, producerID)
		req.Header.Add(headerProducerToken, successfulGetTestToken+" - "+indexString)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusInternalServerError, rr.Code)
		msgRepo.AssertExpectations(t)
		mockDispatcher.AssertExpectations(t)
	})
	t.Run("Success:201-Created", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		oldLogger := log.Logger
		log.Logger = log.Output(&buf)
		defer func() {
			log.Logger = oldLogger
		}()
		msgRepo := new(storagemocks.MessageRepository)
		controller, mockDispatcher := getNewBroadcastController(msgRepo)
		testRouter := createTestRouter(controller)
		testURI := controller.FormatAsRelativeLink(getRouterParam(consumerTestChannel.ChannelID))
		req, _ := http.NewRequest("POST", testURI, nil)
		bodyString := "test message body"
		req.Body = io.NopCloser(strings.NewReader(bodyString))
		req.Header.Add(headerContentType, formDataContentTypeHeaderValue)
		priority := 11
		req.Header.Add(headerPriority, strconv.Itoa(priority))
		req.Header.Add(headerChannelToken, consumerTestChannel.Token)
		indexString := "0"
		producerID := listTestProducerIDPrefix + indexString
		req.Header.Add(headerProducerID, producerID)
		req.Header.Add(headerProducerToken, successfulGetTestToken+" - "+indexString)
		matcher := func(msg *data.Message) bool {
			return msg.Priority == uint(priority) && msg.ContentType == formDataContentTypeHeaderValue && msg.Payload == bodyString &&
				msg.Status == data.MsgStatusAcknowledged && msg.BroadcastedTo.ChannelID == consumerTestChannel.ChannelID && msg.ProducedBy.ProducerID == producerID &&
				msg.IsInValidState()
		}
		msgRepo.On("Create", mock.MatchedBy(matcher)).Return(nil)
		wg := setupAsyncDispatchMock(mockDispatcher, matcher)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		wg.Wait()
		assert.Equal(t, http.StatusCreated, rr.Code)
		responseReqID := rr.Header().Get(headerRequestID)
		location := rr.Header().Get(headerLocation)
		assert.Contains(t, location, "/channel/"+consumerTestChannel.ChannelID+"/message/")
		assert.GreaterOrEqual(t, len(responseReqID), 12)
		assert.Contains(t, buf.String(), responseReqID)
		assert.Contains(t, buf.String(), messageIDLogFieldKey)
		msgRepo.AssertExpectations(t)
		mockDispatcher.AssertExpectations(t)
	})
	t.Run("500:CreateError", func(t *testing.T) {
		t.Parallel()
		msgRepo := new(storagemocks.MessageRepository)
		controller, mockDispatcher := getNewBroadcastController(msgRepo)
		testRouter := createTestRouter(controller)
		testURI := controller.FormatAsRelativeLink(getRouterParam(consumerTestChannel.ChannelID))
		req, _ := http.NewRequest("POST", testURI, nil)
		bodyString := "test message body"
		req.Body = io.NopCloser(strings.NewReader(bodyString))
		req.Header.Add(headerContentType, formDataContentTypeHeaderValue)
		priority := 11
		req.Header.Add(headerPriority, strconv.Itoa(priority))
		req.Header.Add(headerChannelToken, consumerTestChannel.Token)
		indexString := "0"
		producerID := listTestProducerIDPrefix + indexString
		req.Header.Add(headerProducerID, producerID)
		req.Header.Add(headerProducerToken, successfulGetTestToken+" - "+indexString)
		matcher := func(msg *data.Message) bool {
			return msg.Priority == uint(priority) && msg.ContentType == formDataContentTypeHeaderValue && msg.Payload == bodyString &&
				msg.Status == data.MsgStatusAcknowledged && msg.BroadcastedTo.ChannelID == consumerTestChannel.ChannelID && msg.ProducedBy.ProducerID == producerID &&
				msg.IsInValidState()
		}
		msgRepo.On("Create", mock.MatchedBy(matcher)).Return(sql.ErrNoRows)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusInternalServerError, rr.Code)
		msgRepo.AssertExpectations(t)
		mockDispatcher.AssertExpectations(t)
	})
	t.Run("DefaultMessageContentType", func(t *testing.T) {
		t.Parallel()
		msgRepo := new(storagemocks.MessageRepository)
		controller, mockDispatcher := getNewBroadcastController(msgRepo)
		testRouter := createTestRouter(controller)
		testURI := controller.FormatAsRelativeLink(getRouterParam(consumerTestChannel.ChannelID))
		req, _ := http.NewRequest("POST", testURI, nil)
		bodyString := "test message body"
		req.Body = io.NopCloser(strings.NewReader(bodyString))
		priority := 11
		req.Header.Add(headerPriority, strconv.Itoa(priority))
		req.Header.Add(headerChannelToken, consumerTestChannel.Token)
		indexString := "0"
		producerID := listTestProducerIDPrefix + indexString
		req.Header.Add(headerProducerID, producerID)
		req.Header.Add(headerProducerToken, successfulGetTestToken+" - "+indexString)
		matcher := func(msg *data.Message) bool {
			return msg.Priority == uint(priority) && msg.ContentType == defaultMessageContentType && msg.Payload == bodyString &&
				msg.Status == data.MsgStatusAcknowledged && msg.BroadcastedTo.ChannelID == consumerTestChannel.ChannelID && msg.ProducedBy.ProducerID == producerID &&
				msg.IsInValidState()
		}
		msgRepo.On("Create", mock.MatchedBy(matcher)).Return(nil)
		wg := setupAsyncDispatchMock(mockDispatcher, matcher)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		wg.Wait()
		assert.Equal(t, http.StatusCreated, rr.Code)
		msgRepo.AssertExpectations(t)
		mockDispatcher.AssertExpectations(t)
	})
	t.Run("DefaultPriority", func(t *testing.T) {
		t.Parallel()
		msgRepo := new(storagemocks.MessageRepository)
		controller, mockDispatcher := getNewBroadcastController(msgRepo)
		testRouter := createTestRouter(controller)
		testURI := controller.FormatAsRelativeLink(getRouterParam(consumerTestChannel.ChannelID))
		req, _ := http.NewRequest("POST", testURI, nil)
		bodyString := "test message body"
		req.Body = io.NopCloser(strings.NewReader(bodyString))
		req.Header.Add(headerContentType, formDataContentTypeHeaderValue)
		req.Header.Add(headerChannelToken, consumerTestChannel.Token)
		indexString := "0"
		producerID := listTestProducerIDPrefix + indexString
		req.Header.Add(headerProducerID, producerID)
		req.Header.Add(headerProducerToken, successfulGetTestToken+" - "+indexString)
		matcher := func(msg *data.Message) bool {
			return msg.Priority == uint(0) && msg.ContentType == formDataContentTypeHeaderValue && msg.Payload == bodyString &&
				msg.Status == data.MsgStatusAcknowledged && msg.BroadcastedTo.ChannelID == consumerTestChannel.ChannelID && msg.ProducedBy.ProducerID == producerID &&
				msg.IsInValidState()
		}
		msgRepo.On("Create", mock.MatchedBy(matcher)).Return(nil)
		wg := setupAsyncDispatchMock(mockDispatcher, matcher)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		wg.Wait()
		assert.Equal(t, http.StatusCreated, rr.Code)
		msgRepo.AssertExpectations(t)
		mockDispatcher.AssertExpectations(t)
	})
	t.Run("WithMessageIDAndRequestID", func(t *testing.T) {
		t.Parallel()
		msgRepo := new(storagemocks.MessageRepository)
		controller, mockDispatcher := getNewBroadcastController(msgRepo)
		testRouter := createTestRouter(controller)
		testURI := controller.FormatAsRelativeLink(getRouterParam(consumerTestChannel.ChannelID))
		req, _ := http.NewRequest("POST", testURI, nil)
		bodyString := "test message body"
		req.Body = io.NopCloser(strings.NewReader(bodyString))
		req.Header.Add(headerContentType, formDataContentTypeHeaderValue)
		req.Header.Add(headerChannelToken, consumerTestChannel.Token)
		indexString := "0"
		messageID := "non-conflict-message-id"
		producerID := listTestProducerIDPrefix + indexString
		req.Header.Add(headerProducerID, producerID)
		req.Header.Add(headerProducerToken, successfulGetTestToken+" - "+indexString)
		req.Header.Add(headerMessageID, messageID)
		reqID := xid.New().String()
		req.Header.Add(headerRequestID, reqID)
		matcher := func(msg *data.Message) bool {
			return msg.Priority == uint(0) && msg.ContentType == formDataContentTypeHeaderValue && msg.Payload == bodyString &&
				msg.Status == data.MsgStatusAcknowledged && msg.BroadcastedTo.ChannelID == consumerTestChannel.ChannelID && msg.ProducedBy.ProducerID == producerID &&
				msg.IsInValidState() && msg.MessageID == messageID
		}
		msgRepo.On("Create", mock.MatchedBy(matcher)).Return(nil)
		wg := setupAsyncDispatchMock(mockDispatcher, matcher)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		wg.Wait()
		assert.Equal(t, http.StatusCreated, rr.Code)
		responseReqID := rr.Header().Get(headerRequestID)
		assert.Equal(t, reqID, responseReqID)
		msgRepo.AssertExpectations(t)
		mockDispatcher.AssertExpectations(t)
	})
	t.Run("MessageIDConflict", func(t *testing.T) {
		t.Parallel()
		msgRepo := new(storagemocks.MessageRepository)
		controller, mockDispatcher := getNewBroadcastController(msgRepo)
		testRouter := createTestRouter(controller)
		testURI := controller.FormatAsRelativeLink(getRouterParam(consumerTestChannel.ChannelID))
		req, _ := http.NewRequest("POST", testURI, nil)
		bodyString := "test message body"
		req.Body = io.NopCloser(strings.NewReader(bodyString))
		req.Header.Add(headerContentType, formDataContentTypeHeaderValue)
		req.Header.Add(headerChannelToken, consumerTestChannel.Token)
		indexString := "0"
		messageID := "conflict-message-id"
		req.Header.Add(headerMessageID, messageID)
		producerID := listTestProducerIDPrefix + indexString
		req.Header.Add(headerProducerID, producerID)
		req.Header.Add(headerProducerToken, successfulGetTestToken+" - "+indexString)
		matcher := func(msg *data.Message) bool {
			return msg.Priority == uint(0) && msg.ContentType == formDataContentTypeHeaderValue && msg.Payload == bodyString &&
				msg.Status == data.MsgStatusAcknowledged && msg.BroadcastedTo.ChannelID == consumerTestChannel.ChannelID && msg.ProducedBy.ProducerID == producerID &&
				msg.IsInValidState() && msg.MessageID == messageID
		}
		msgRepo.On("Create", mock.MatchedBy(matcher)).Return(storage.ErrDuplicateMessageIDForChannel)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusConflict, rr.Code)
		msgRepo.AssertExpectations(t)
		mockDispatcher.AssertExpectations(t)
	})
	t.Run("WithMetdataHeaders", func(t *testing.T) {
		t.Parallel()
		msgRepo := new(storagemocks.MessageRepository)
		controller, mockDispatcher := getNewBroadcastController(msgRepo)
		testRouter := createTestRouter(controller)
		testURI := controller.FormatAsRelativeLink(getRouterParam(consumerTestChannel.ChannelID))
		req, _ := http.NewRequest("POST", testURI, nil)
		bodyString := "test message body"
		req.Body = io.NopCloser(strings.NewReader(bodyString))
		req.Header.Add(headerContentType, formDataContentTypeHeaderValue)
		req.Header.Add(headerChannelToken, consumerTestChannel.Token)
		indexString := "0"
		producerID := listTestProducerIDPrefix + indexString
		req.Header.Add(headerProducerID, producerID)
		req.Header.Add(headerProducerToken, successfulGetTestToken+" - "+indexString)
		req.Header.Add("X-Test-A", "1")
		req.Header.Add("X-Test-B", "2")
		req.Header.Add(headerMetadataHeaders, "X-Test-A,X-Test-B")
		matcher := func(msg *data.Message) bool {
			return msg.Headers["X-Test-A"] == "1" && msg.Headers["X-Test-B"] == "2"
		}
		msgRepo.On("Create", mock.MatchedBy(matcher)).Return(nil)
		wg := setupAsyncDispatchMock(mockDispatcher, matcher)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		wg.Wait()
		assert.Equal(t, http.StatusCreated, rr.Code)
		msgRepo.AssertExpectations(t)
		mockDispatcher.AssertExpectations(t)
	})
}

func setupAsyncDispatchMock(mockDispatcher *dispatchermocks.MessageDispatcher, matcher func(msg *data.Message) bool) *sync.WaitGroup {
	var wg sync.WaitGroup
	wg.Add(1)
	mockDispatcher.On("Dispatch", mock.MatchedBy(matcher)).Return().Run(func(args mock.Arguments) {
		wg.Done()
	})
	return &wg
}

func TestScheduledMessageFunctionality(t *testing.T) {
	t.Run("InvalidScheduledDateTime", func(t *testing.T) {
		t.Parallel()
		msgRepo := new(storagemocks.MessageRepository)
		scheduledMsgRepo := new(storagemocks.ScheduledMessageRepository)
		controller, mockDispatcher := getNewBroadcastControllerWithScheduledRepo(msgRepo, scheduledMsgRepo)
		testRouter := createTestRouter(controller)
		testURI := controller.FormatAsRelativeLink(getRouterParam(consumerTestChannel.ChannelID))
		req, _ := http.NewRequest("POST", testURI, nil)
		bodyString := "test scheduled message body"
		req.Body = io.NopCloser(strings.NewReader(bodyString))
		req.Header.Add(headerContentType, formDataContentTypeHeaderValue)
		req.Header.Add(headerChannelToken, consumerTestChannel.Token)
		producerID := listTestProducerIDPrefix + "0"
		req.Header.Add(headerProducerID, producerID)
		req.Header.Add(headerProducerToken, successfulGetTestToken+" - 0")

		// Set an invalid date format
		req.Header.Add(headerScheduledFor, "2023-13-32T25:61:99")

		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusBadRequest, rr.Code)
		msgRepo.AssertExpectations(t)
		scheduledMsgRepo.AssertExpectations(t)
		mockDispatcher.AssertExpectations(t)
	})

	t.Run("ScheduledTimeTooClose", func(t *testing.T) {
		t.Parallel()
		msgRepo := new(storagemocks.MessageRepository)
		scheduledMsgRepo := new(storagemocks.ScheduledMessageRepository)
		controller, mockDispatcher := getNewBroadcastControllerWithScheduledRepo(msgRepo, scheduledMsgRepo)
		testRouter := createTestRouter(controller)
		testURI := controller.FormatAsRelativeLink(getRouterParam(consumerTestChannel.ChannelID))
		req, _ := http.NewRequest("POST", testURI, nil)
		bodyString := "test scheduled message body"
		req.Body = io.NopCloser(strings.NewReader(bodyString))
		req.Header.Add(headerContentType, formDataContentTypeHeaderValue)
		req.Header.Add(headerChannelToken, consumerTestChannel.Token)
		producerID := listTestProducerIDPrefix + "0"
		req.Header.Add(headerProducerID, producerID)
		req.Header.Add(headerProducerToken, successfulGetTestToken+" - 0")

		// Set a time less than the minimum delay
		tooSoon := time.Now().Add(1 * time.Minute)
		req.Header.Add(headerScheduledFor, tooSoon.Format(time.RFC3339))

		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusPreconditionFailed, rr.Code)
		msgRepo.AssertExpectations(t)
		scheduledMsgRepo.AssertExpectations(t)
		mockDispatcher.AssertExpectations(t)
	})

	t.Run("SuccessfulScheduledMessage", func(t *testing.T) {
		t.Parallel()
		msgRepo := new(storagemocks.MessageRepository)
		scheduledMsgRepo := new(storagemocks.ScheduledMessageRepository)
		controller, mockDispatcher := getNewBroadcastControllerWithScheduledRepo(msgRepo, scheduledMsgRepo)
		testRouter := createTestRouter(controller)
		testURI := controller.FormatAsRelativeLink(getRouterParam(consumerTestChannel.ChannelID))
		req, _ := http.NewRequest("POST", testURI, nil)
		bodyString := "test scheduled message body"
		req.Body = io.NopCloser(strings.NewReader(bodyString))
		req.Header.Add(headerContentType, formDataContentTypeHeaderValue)
		req.Header.Add(headerChannelToken, consumerTestChannel.Token)
		priority := 5
		req.Header.Add(headerPriority, strconv.Itoa(priority))
		producerID := listTestProducerIDPrefix + "0"
		req.Header.Add(headerProducerID, producerID)
		req.Header.Add(headerProducerToken, successfulGetTestToken+" - 0")
		messageID := "scheduled-test-message-id"
		req.Header.Add(headerMessageID, messageID)

		// Set a valid future time
		scheduledTime := time.Now().Add(10 * time.Minute)
		req.Header.Add(headerScheduledFor, scheduledTime.Format(time.RFC3339))

		matcher := func(msg *data.ScheduledMessage) bool {
			return msg.Priority == uint(priority) &&
				msg.ContentType == formDataContentTypeHeaderValue &&
				msg.Payload == bodyString &&
				msg.Status == data.ScheduledMsgStatusScheduled &&
				msg.BroadcastedTo.ChannelID == consumerTestChannel.ChannelID &&
				msg.ProducedBy.ProducerID == producerID &&
				msg.IsInValidState() &&
				msg.MessageID == messageID &&
				!msg.DispatchSchedule.IsZero()
		}

		scheduledMsgRepo.On("Create", mock.MatchedBy(matcher)).Return(nil)

		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusCreated, rr.Code)
		location := rr.Header().Get(headerLocation)
		assert.Contains(t, location, "/channel/"+consumerTestChannel.ChannelID+"/scheduled-message/"+messageID)

		msgRepo.AssertExpectations(t)
		scheduledMsgRepo.AssertExpectations(t)
		mockDispatcher.AssertExpectations(t)
	})

	t.Run("ScheduledMessageDuplicateID", func(t *testing.T) {
		t.Parallel()
		msgRepo := new(storagemocks.MessageRepository)
		scheduledMsgRepo := new(storagemocks.ScheduledMessageRepository)
		controller, mockDispatcher := getNewBroadcastControllerWithScheduledRepo(msgRepo, scheduledMsgRepo)
		testRouter := createTestRouter(controller)
		testURI := controller.FormatAsRelativeLink(getRouterParam(consumerTestChannel.ChannelID))
		req, _ := http.NewRequest("POST", testURI, nil)
		bodyString := "test scheduled message body"
		req.Body = io.NopCloser(strings.NewReader(bodyString))
		req.Header.Add(headerContentType, formDataContentTypeHeaderValue)
		req.Header.Add(headerChannelToken, consumerTestChannel.Token)
		producerID := listTestProducerIDPrefix + "0"
		req.Header.Add(headerProducerID, producerID)
		req.Header.Add(headerProducerToken, successfulGetTestToken+" - 0")
		messageID := "duplicate-scheduled-message-id"
		req.Header.Add(headerMessageID, messageID)

		// Set a valid future time
		scheduledTime := time.Now().Add(10 * time.Minute)
		req.Header.Add(headerScheduledFor, scheduledTime.Format(time.RFC3339))

		scheduledMsgRepo.On("Create", mock.MatchedBy(func(msg *data.ScheduledMessage) bool {
			return msg.MessageID == messageID
		})).Return(storage.ErrDuplicateScheduledMessageIDForChannel)

		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusConflict, rr.Code)

		msgRepo.AssertExpectations(t)
		scheduledMsgRepo.AssertExpectations(t)
		mockDispatcher.AssertExpectations(t)
	})
}
