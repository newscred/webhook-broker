package controllers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/imyousuf/webhook-broker/storage"
	"github.com/imyousuf/webhook-broker/storage/data"
	storagemocks "github.com/imyousuf/webhook-broker/storage/mocks"
	"github.com/julienschmidt/httprouter"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

const (
	dlqTestConsumerID  = "consumer-dlq-test"
	messageProducerID  = "message-test-producer-id"
	messageChannelID   = "message-test-channel-id"
	messageIDPrefix    = "message-test-id-"
	messagePayload     = "<test>hello world</test>"
	messageContentType = "text/xml"
	messagesCount      = 45
)

var (
	djRepo          storage.DeliveryJobRepository
	messageChannel  *data.Channel
	messageProducer *data.Producer
	dlqConsumer     *data.Consumer
	messages        []*data.Message
	jobs            map[*data.Message]*data.DeliveryJob
	errExpected     = errors.New("expected error")
)

func MessageTestSetup() {
	channel, _ := data.NewChannel(messageChannelID, successfulGetTestToken)
	messageChannel, _ = channelRepo.Store(channel)
	producer, _ := data.NewProducer(messageProducerID, successfulGetTestToken)
	messageProducer, _ = producerRepo.Store(producer)
	consumer, _ := data.NewConsumer(messageChannel, dlqTestConsumerID, successfulGetTestToken, callbackURL)
	dlqConsumer, _ = consumerRepo.Store(consumer)
	messages = make([]*data.Message, messagesCount)
	jobs = make(map[*data.Message]*data.DeliveryJob, messagesCount)
	djRepo = storage.NewDeliveryJobRepository(db, messageRepo, consumerRepo)
	for index := 0; index < messagesCount; index++ {
		messages[index], _ = data.NewMessage(messageChannel, messageProducer, messagePayload, messageContentType)
		messages[index].MessageID = messageIDPrefix + strconv.Itoa(index)
		messageRepo.Create(messages[index])
		jobs[messages[index]], _ = data.NewDeliveryJob(messages[index], dlqConsumer)
		err := djRepo.DispatchMessage(messages[index], jobs[messages[index]])
		if err != nil {
			panic(err)
		}
		err = djRepo.MarkJobInflight(jobs[messages[index]])
		if err != nil {
			panic(err)
		}
		if index%5 == 0 {
			err = djRepo.MarkJobDead(jobs[messages[index]])
			if err != nil {
				panic(err)
			}
			log.Debug().Int("index", index).Msg("Dead")
		} else if index%2 == 1 {
			err = djRepo.MarkJobDelivered(jobs[messages[index]])
			if err != nil {
				panic(err)
			}
			log.Debug().Int("index", index).Msg("Delivered")
		}
	}
}

func getMessageController() *MessageController {
	return NewMessageController(messageRepo, djRepo)
}

func getMessagesController() *MessagesController {
	return NewMessagesController(getMessageController(), messageRepo)
}

func getDLQControllerWithMockedRepo() *DLQController {
	consumerRepo := new(storagemocks.ConsumerRepository)
	djRepo := new(storagemocks.DeliveryJobRepository)
	return NewDLQController(getMessageController(), djRepo, consumerRepo)
}

func TestMessageFormatRelativeLink(t *testing.T) {
	controller := getMessageController()
	assert.Equal(t, "/channel/"+messageChannelID+"/message/"+messageIDPrefix, controller.FormatAsRelativeLink(httprouter.Param{Key: channelIDPathParamKey, Value: messageChannelID}, httprouter.Param{Key: messageIDParamKey, Value: messageIDPrefix}))
}

func TestMessageGet(t *testing.T) {
	baseURL := "/channel/" + messageChannelID + "/message/" + messageIDPrefix
	testRouter := createTestRouter(getMessageController())
	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		var wg sync.WaitGroup
		wg.Add(messagesCount)
		for index := 0; index < messagesCount; index++ {
			go func(index int) {
				messageURL := baseURL + strconv.Itoa(index)
				req, _ := http.NewRequest(http.MethodGet, messageURL, nil)
				rr := httptest.NewRecorder()
				testRouter.ServeHTTP(rr, req)
				assert.Equal(t, http.StatusOK, rr.Code)
				log.Debug().Msg(rr.Body.String())
				decoder := json.NewDecoder(rr.Body)
				msgModel := &MessageModel{}
				err := decoder.Decode(msgModel)
				assert.Nil(t, err)
				assert.Equal(t, messagePayload, msgModel.Payload)
				assert.Equal(t, messageContentType, msgModel.ContentType)
				assert.Equal(t, data.MsgStatusDispatched.String(), msgModel.Status)
				assert.Equal(t, 1, len(msgModel.Jobs))
				assert.Equal(t, messageProducer.Name, msgModel.ProducedBy)
				job := msgModel.Jobs[0]
				assert.Equal(t, callbackURL.String(), job.ListenerEndpoint)
				assert.Equal(t, dlqConsumer.Name, job.ListenerName)
				if index%5 == 0 {
					assert.Equal(t, data.JobDead.String(), job.Status)
				} else if index%2 == 1 {
					assert.Equal(t, data.JobDelivered.String(), job.Status)
				} else {
					assert.Equal(t, data.JobInflight.String(), job.Status)
				}
				wg.Done()
			}(index)
		}
		wg.Wait()
	})
	t.Run("404", func(t *testing.T) {
		t.Parallel()
		req, _ := http.NewRequest(http.MethodGet, baseURL, nil)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusNotFound, rr.Code)
	})
	t.Run("500", func(t *testing.T) {
		t.Parallel()
		mockedDJRepo := new(storagemocks.DeliveryJobRepository)
		expectedErr := errors.New("test error")
		mockedDJRepo.On("GetJobsForMessage", mock.Anything, mock.Anything).Return(nil, nil, expectedErr)
		mockedRouter := createTestRouter(NewMessageController(messageRepo, mockedDJRepo))
		messageURL := baseURL + "0"
		req, _ := http.NewRequest(http.MethodGet, messageURL, nil)
		rr := httptest.NewRecorder()
		mockedRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusInternalServerError, rr.Code)
		assert.Equal(t, expectedErr.Error(), rr.Body.String())
	})
}

func TestMessagesFormatRelativeLink(t *testing.T) {
	controller := getMessagesController()
	assert.Equal(t, "/channel/"+messageChannelID+"/messages", controller.FormatAsRelativeLink(httprouter.Param{Key: channelIDPathParamKey, Value: messageChannelID}))
}

func TestMessagesGet(t *testing.T) {
	baseURL := "/channel/" + messageChannelID + "/messages"
	t.Run("500", func(t *testing.T) {
		t.Parallel()
		mockedMessageRepo := new(storagemocks.MessageRepository)
		controller := getMessagesController()
		controller.MessageRepo = mockedMessageRepo
		expectedErr := errors.New("test error")
		mockedMessageRepo.On("GetMessagesForChannel", messageChannelID, mock.Anything).Return(nil, nil, expectedErr)
		testRouter := createTestRouter(controller)
		req, _ := http.NewRequest(http.MethodGet, baseURL, nil)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusInternalServerError, rr.Code)
		assert.Equal(t, expectedErr.Error(), rr.Body.String())
	})
	t.Run("404", func(t *testing.T) {
		t.Parallel()
		mockedMessageRepo := new(storagemocks.MessageRepository)
		controller := getMessagesController()
		controller.MessageRepo = mockedMessageRepo
		mockedMessageRepo.On("GetMessagesForChannel", messageChannelID, mock.Anything).Return(nil, nil, sql.ErrNoRows)
		testRouter := createTestRouter(controller)
		req, _ := http.NewRequest(http.MethodGet, baseURL, nil)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusNotFound, rr.Code)
		assert.Equal(t, ErrNotFound.Error(), rr.Body.String())
	})
	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		controller := getMessagesController()
		testRouter := createTestRouter(controller)
		req, _ := http.NewRequest(http.MethodGet, baseURL, nil)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		bodyChannels := &ListResult{}
		json.NewDecoder(strings.NewReader(rr.Body.String())).Decode(bodyChannels)
		assert.Equal(t, 25, len(bodyChannels.Result))
		next := bodyChannels.Pages[nextPaginationQueryParamKey]
		previous := bodyChannels.Pages[previousPaginationQueryParamKey]
		req, _ = http.NewRequest(http.MethodGet, next, nil)
		rr = httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		bodyChannels = &ListResult{}
		json.NewDecoder(strings.NewReader(rr.Body.String())).Decode(bodyChannels)
		assert.Equal(t, 20, len(bodyChannels.Result))
		req, _ = http.NewRequest(http.MethodGet, previous, nil)
		rr = httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		bodyChannels = &ListResult{}
		json.NewDecoder(strings.NewReader(rr.Body.String())).Decode(bodyChannels)
		assert.Equal(t, 0, len(bodyChannels.Result))
	})
}

func TestDLQFormatLinks(t *testing.T) {
	controller := getDLQControllerWithMockedRepo()
	assert.Equal(t, "/channel/"+messageChannelID+"/consumer/"+dlqTestConsumerID+"/dlq", controller.FormatAsRelativeLink(httprouter.Param{Key: channelIDPathParamKey, Value: messageChannelID}, httprouter.Param{Key: consumerIDPathParamKey, Value: dlqTestConsumerID}))
}

func TestDLQGet(t *testing.T) {
	baseURL := "/channel/" + messageChannelID + "/consumer/" + dlqTestConsumerID + "/dlq"
	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		controller := getDLQControllerWithMockedRepo()
		mockedConsumerRepo := controller.ConsumerRepo.(*storagemocks.ConsumerRepository)
		mockedDJRepo := controller.DeliveryJobRepo.(*storagemocks.DeliveryJobRepository)
		mockedConsumerRepo.On("Get", messageChannelID, dlqTestConsumerID).Return(dlqConsumer, nil)
		deadJobs, page, err := djRepo.GetJobsForConsumer(dlqConsumer, data.JobDead, data.NewPagination(nil, nil))
		mockedDJRepo.On("GetJobsForConsumer", dlqConsumer, data.JobDead, mock.Anything).Return(deadJobs, page, err)
		testRouter := createTestRouter(controller)
		req, _ := http.NewRequest(http.MethodGet, baseURL, nil)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code)
		bodyChannels := &DLQList{}
		json.NewDecoder(strings.NewReader(rr.Body.String())).Decode(bodyChannels)
		assert.Equal(t, messagesCount/5, len(bodyChannels.DeadJobs))
		expectedURLs := make(map[string]bool, messagesCount/5)
		exBaseURL := "/channel/" + messageChannelID + "/message/" + messageIDPrefix
		for index := 0; index < messagesCount; index++ {
			if index%5 == 0 {
				expectedURLs[exBaseURL+strconv.Itoa(index)] = true
			}
		}
		for _, deadJob := range bodyChannels.DeadJobs {
			_, ok := expectedURLs[deadJob.MessageURL]
			assert.True(t, ok)
		}
	})
	t.Run("404", func(t *testing.T) {
		t.Parallel()
		controller := getDLQControllerWithMockedRepo()
		mockedConsumerRepo := controller.ConsumerRepo.(*storagemocks.ConsumerRepository)
		mockedConsumerRepo.On("Get", messageChannelID, dlqTestConsumerID).Return(dlqConsumer, sql.ErrNoRows)
		testRouter := createTestRouter(controller)
		req, _ := http.NewRequest(http.MethodGet, baseURL, nil)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusNotFound, rr.Code)
	})
	t.Run("500:ConsumerGet", func(t *testing.T) {
		t.Parallel()
		controller := getDLQControllerWithMockedRepo()
		mockedConsumerRepo := controller.ConsumerRepo.(*storagemocks.ConsumerRepository)
		mockedConsumerRepo.On("Get", messageChannelID, dlqTestConsumerID).Return(dlqConsumer, errExpected)
		testRouter := createTestRouter(controller)
		req, _ := http.NewRequest(http.MethodGet, baseURL, nil)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusInternalServerError, rr.Code)
		assert.Equal(t, rr.Body.String(), errExpected.Error())
	})
	t.Run("500:DeadJobsGet", func(t *testing.T) {
		t.Parallel()
		controller := getDLQControllerWithMockedRepo()
		mockedConsumerRepo := controller.ConsumerRepo.(*storagemocks.ConsumerRepository)
		mockedDJRepo := controller.DeliveryJobRepo.(*storagemocks.DeliveryJobRepository)
		mockedConsumerRepo.On("Get", messageChannelID, dlqTestConsumerID).Return(dlqConsumer, nil)
		deadJobs, page, _ := djRepo.GetJobsForConsumer(dlqConsumer, data.JobDead, data.NewPagination(nil, nil))
		mockedDJRepo.On("GetJobsForConsumer", dlqConsumer, data.JobDead, mock.Anything).Return(deadJobs, page, errExpected)
		testRouter := createTestRouter(controller)
		req, _ := http.NewRequest(http.MethodGet, baseURL, nil)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusInternalServerError, rr.Code)
		assert.Equal(t, rr.Body.String(), errExpected.Error())
	})
}

func TestDLQRequeue(t *testing.T) {
	baseURL := "/channel/" + messageChannelID + "/consumer/" + dlqTestConsumerID + "/dlq"
	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		controller := getDLQControllerWithMockedRepo()
		mockedConsumerRepo := controller.ConsumerRepo.(*storagemocks.ConsumerRepository)
		mockedConsumerRepo.On("Get", messageChannelID, dlqTestConsumerID).Return(dlqConsumer, nil)
		mockedDJRepo := controller.DeliveryJobRepo.(*storagemocks.DeliveryJobRepository)
		mockedDJRepo.On("RequeueDeadJobsForConsumer", dlqConsumer).Return(nil)
		testRouter := createTestRouter(controller)
		req, _ := http.NewRequest(http.MethodPost, baseURL, nil)
		req.Header.Add(headerContentType, formDataContentTypeHeaderValue)
		req.PostForm = url.Values{}
		req.PostForm.Add(requeueFormParamName, successfulGetTestToken)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusAccepted, rr.Code)
		assert.Equal(t, rr.Body.String(), "")
	})
	t.Run("404", func(t *testing.T) {
		t.Parallel()
		controller := getDLQControllerWithMockedRepo()
		mockedConsumerRepo := controller.ConsumerRepo.(*storagemocks.ConsumerRepository)
		mockedConsumerRepo.On("Get", messageChannelID, dlqTestConsumerID).Return(dlqConsumer, sql.ErrNoRows)
		testRouter := createTestRouter(controller)
		req, _ := http.NewRequest(http.MethodPost, baseURL, nil)
		req.Header.Add(headerContentType, formDataContentTypeHeaderValue)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusNotFound, rr.Code)
	})
	t.Run("415", func(t *testing.T) {
		t.Parallel()
		controller := getDLQControllerWithMockedRepo()
		testRouter := createTestRouter(controller)
		req, _ := http.NewRequest(http.MethodPost, baseURL, nil)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusUnsupportedMediaType, rr.Code)
	})
	t.Run("500:ConsumerGet", func(t *testing.T) {
		t.Parallel()
		controller := getDLQControllerWithMockedRepo()
		mockedConsumerRepo := controller.ConsumerRepo.(*storagemocks.ConsumerRepository)
		mockedConsumerRepo.On("Get", messageChannelID, dlqTestConsumerID).Return(dlqConsumer, errExpected)
		testRouter := createTestRouter(controller)
		req, _ := http.NewRequest(http.MethodPost, baseURL, nil)
		req.Header.Add(headerContentType, formDataContentTypeHeaderValue)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusInternalServerError, rr.Code)
		assert.Equal(t, rr.Body.String(), errExpected.Error())
	})
	t.Run("400", func(t *testing.T) {
		t.Parallel()
		controller := getDLQControllerWithMockedRepo()
		mockedConsumerRepo := controller.ConsumerRepo.(*storagemocks.ConsumerRepository)
		mockedConsumerRepo.On("Get", messageChannelID, dlqTestConsumerID).Return(dlqConsumer, nil)
		testRouter := createTestRouter(controller)
		req, _ := http.NewRequest(http.MethodPost, baseURL, nil)
		req.Header.Add(headerContentType, formDataContentTypeHeaderValue)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
		assert.Equal(t, rr.Body.String(), ErrBadRequestForRequeue.Error())
		req, _ = http.NewRequest(http.MethodPost, baseURL, nil)
		req.Header.Add(headerContentType, formDataContentTypeHeaderValue)
		req.PostForm = url.Values{}
		req.PostForm.Add(requeueFormParamName, successfulGetTestToken+"asd")
		rr = httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
		assert.Equal(t, rr.Body.String(), ErrBadRequestForRequeue.Error())
	})
	t.Run("500:RequeueErr", func(t *testing.T) {
		t.Parallel()
		controller := getDLQControllerWithMockedRepo()
		mockedConsumerRepo := controller.ConsumerRepo.(*storagemocks.ConsumerRepository)
		mockedConsumerRepo.On("Get", messageChannelID, dlqTestConsumerID).Return(dlqConsumer, nil)
		mockedDJRepo := controller.DeliveryJobRepo.(*storagemocks.DeliveryJobRepository)
		mockedDJRepo.On("RequeueDeadJobsForConsumer", dlqConsumer).Return(errExpected)
		testRouter := createTestRouter(controller)
		req, _ := http.NewRequest(http.MethodPost, baseURL, nil)
		req.Header.Add(headerContentType, formDataContentTypeHeaderValue)
		req.PostForm = url.Values{}
		req.PostForm.Add(requeueFormParamName, successfulGetTestToken)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusInternalServerError, rr.Code)
		assert.Equal(t, rr.Body.String(), errExpected.Error())
	})
}
