package controllers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/imyousuf/webhook-broker/storage"
	"github.com/imyousuf/webhook-broker/storage/data"
	storagemocks "github.com/imyousuf/webhook-broker/storage/mocks"
	"github.com/julienschmidt/httprouter"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

var (
	consumerRepo          storage.ConsumerRepository
	callbackURL           *url.URL
	consumerTestChannel   *data.Channel
	deleteUnmodifiedSince string
	deleteFailedConsumer  *data.Consumer
)

const (
	listTestConsumerIDPrefix    = "consumer-get-list-"
	channelTestConsumerID       = "consumer-channel-some-id"
	createConsumerIDWithData    = "put-consumer-id"
	createConsumerIDWithoutData = "put-consumer-id-without-data"
	deleteConsumerIDWithData    = "delete-consumer-id"
	deleteConsumerIDFailed      = "delete-consumer-failed-id"
)

// ChannelTestSetup is called from TestMain for the package
func ConsumerTestSetup() {
	consumerRepo = storage.NewConsumerRepository(db, channelRepo)
	channel, err := data.NewChannel(channelTestConsumerID, successfulGetTestToken)
	if err != nil {
		log.Fatal()
	}
	consumerTestChannel, err = channelRepo.Store(channel)
	if err != nil {
		log.Fatal().Err(err)
	}
	callbackURL, err = url.Parse("https://imytech.net/")
	if err != nil {
		log.Fatal().Err(err)
	}
	for index := 49; index > -1; index = index - 1 {
		indexString := strconv.Itoa(index)
		consumer, err := data.NewConsumer(consumerTestChannel, listTestConsumerIDPrefix+indexString, successfulGetTestToken+" - "+indexString, callbackURL)
		if err == nil {
			_, err = consumerRepo.Store(consumer)
		}
		if err != nil {
			log.Fatal().Err(err)
		}
	}
	for _, deleteID := range []string{deleteConsumerIDWithData, deleteConsumerIDFailed} {
		consumer, err := data.NewConsumer(consumerTestChannel, deleteID, successfulGetTestToken+" - DELETE", callbackURL)
		if err == nil {
			_, err = consumerRepo.Store(consumer)
		}
		if err != nil {
			log.Fatal().Err(err)
		}
		if deleteID == deleteConsumerIDWithData {
			deleteUnmodifiedSince = consumer.GetLastUpdatedHTTPTimeString()
		} else {
			deleteFailedConsumer = consumer
		}
	}
}

func getNewConsumerController(consumerRepo storage.ConsumerRepository) *ConsumerController {
	return NewConsumerController(channelRepo, consumerRepo)
}

func TestConsumerFormatAsRelativeLink(t *testing.T) {
	t.Parallel()
	controller := getNewConsumerController(nil)
	assert.Equal(t, consumerPath, controller.GetPath())
	assert.Equal(t, "/channel/someChannelId/consumer/someConsumerId", controller.FormatAsRelativeLink(httprouter.Param{Key: channelIDPathParamKey, Value: "someChannelId"},
		httprouter.Param{Key: consumerIDPathParamKey, Value: "someConsumerId"}))
}

func TestConsumersFormatAsRelativeLink(t *testing.T) {
	t.Parallel()
	controller := NewConsumersController(getNewConsumerController(nil), nil)
	assert.Equal(t, consumersPath, controller.GetPath())
	assert.Equal(t, "/channel/someChannelId/consumers", controller.FormatAsRelativeLink(httprouter.Param{Key: channelIDPathParamKey, Value: "someChannelId"}))
}

func TestConsumersControllerGet(t *testing.T) {
	t.Parallel()
	listController := NewConsumersController(getNewConsumerController(consumerRepo), consumerRepo)
	testRouter := createTestRouter(listController)
	testURI := listController.FormatAsRelativeLink(httprouter.Param{Key: channelIDPathParamKey, Value: consumerTestChannel.ChannelID})
	t.Log(testURI)
	req, _ := http.NewRequest("GET", testURI, nil)
	rr := httptest.NewRecorder()
	testRouter.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)
	body := rr.Body.String()
	t.Log(body)
	bodyChannels := &ListResult{}
	json.NewDecoder(strings.NewReader(body)).Decode(bodyChannels)
	assert.Equal(t, 25, len(bodyChannels.Result))

	nextURL := bodyChannels.Pages[nextPaginationQueryParamKey]
	previousURL := bodyChannels.Pages[previousPaginationQueryParamKey]

	// Previous of first page should be empty
	t.Log(previousURL)
	preq, _ := http.NewRequest("GET", previousURL, nil)
	pr := httptest.NewRecorder()
	testRouter.ServeHTTP(pr, preq)
	assert.Equal(t, http.StatusOK, pr.Code)
	json.NewDecoder(strings.NewReader(pr.Body.String())).Decode(bodyChannels)
	t.Log(pr.Body.String())
	assert.Equal(t, 0, len(bodyChannels.Result))

	// Next of first page should have 25
	nreq, _ := http.NewRequest("GET", nextURL, nil)
	nr := httptest.NewRecorder()
	testRouter.ServeHTTP(nr, nreq)
	assert.Equal(t, http.StatusOK, nr.Code)
	json.NewDecoder(strings.NewReader(nr.Body.String())).Decode(bodyChannels)
	assert.Equal(t, 25, len(bodyChannels.Result))
	nextURL = bodyChannels.Pages[nextPaginationQueryParamKey]
	previousURL = bodyChannels.Pages[previousPaginationQueryParamKey]

	// Previous of second page should be 25 or first page
	preq, _ = http.NewRequest("GET", previousURL, nil)
	pr = httptest.NewRecorder()
	testRouter.ServeHTTP(pr, preq)
	assert.Equal(t, http.StatusOK, pr.Code)
	json.NewDecoder(strings.NewReader(pr.Body.String())).Decode(bodyChannels)
	assert.Equal(t, 25, len(bodyChannels.Result))

	// Next of second page should be empty
	continueNext := true
	for continueNext {
		nreq, _ = http.NewRequest("GET", nextURL, nil)
		nr = httptest.NewRecorder()
		testRouter.ServeHTTP(nr, nreq)
		assert.Equal(t, http.StatusOK, nr.Code)
		json.NewDecoder(strings.NewReader(nr.Body.String())).Decode(bodyChannels)
		if len(bodyChannels.Result) == 0 {
			continueNext = false
		}
		nextURL = bodyChannels.Pages[nextPaginationQueryParamKey]
	}
}

func TestConsumersControllerGet_Error(t *testing.T) {
	t.Run("GetList:GenericServerError", func(t *testing.T) {
		t.Parallel()
		mockConsumerRepo := new(storagemocks.ConsumerRepository)
		expectedErr := errors.New("GetList error")
		mockConsumerRepo.On("GetList", consumerTestChannel.ChannelID, mock.Anything).Return(nil, nil, expectedErr)
		listController := NewConsumersController(getNewConsumerController(mockConsumerRepo), mockConsumerRepo)
		testRouter := createTestRouter(listController)
		testURI := listController.FormatAsRelativeLink(httprouter.Param{Key: channelIDPathParamKey, Value: consumerTestChannel.ChannelID})
		req, _ := http.NewRequest("GET", testURI, nil)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusInternalServerError, rr.Code)
	})
	t.Run("GetList:NoChannel", func(t *testing.T) {
		t.Parallel()
		listController := NewConsumersController(getNewConsumerController(consumerRepo), consumerRepo)
		testRouter := createTestRouter(listController)
		testURI := listController.FormatAsRelativeLink(httprouter.Param{Key: channelIDPathParamKey, Value: "no-such-channel-for-get-consumers"})
		req, _ := http.NewRequest("GET", testURI, nil)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusNotFound, rr.Code)
	})
}

func TestConsumerGet(t *testing.T) {
	t.Run("SuccessfulGet", func(t *testing.T) {
		t.Parallel()
		getConsumerController := getNewConsumerController(consumerRepo)
		testRouter := createTestRouter(getConsumerController)
		testURI := getConsumerController.FormatAsRelativeLink(httprouter.Param{Key: channelIDPathParamKey, Value: consumerTestChannel.ChannelID}, httprouter.Param{Key: consumerIDPathParamKey, Value: listTestConsumerIDPrefix + "0"})
		t.Log(testURI)
		req, _ := http.NewRequest("GET", testURI, nil)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code)
		bodyChannel := &ConsumerModel{}
		json.NewDecoder(strings.NewReader(rr.Body.String())).Decode(bodyChannel)
		assert.Contains(t, bodyChannel.ID, listTestConsumerIDPrefix)
		assert.Contains(t, bodyChannel.Name, listTestConsumerIDPrefix)
		assert.Contains(t, bodyChannel.Token, successfulGetTestToken)
		assert.Equal(t, callbackURL.String(), bodyChannel.CallbackURL)
		assert.NotNil(t, bodyChannel.ChangedAt)
		assert.Equal(t, bodyChannel.ChangedAt.Format(http.TimeFormat), rr.HeaderMap.Get(headerLastModified))
	})
	t.Run("NotFound", func(t *testing.T) {
		t.Parallel()
		getController := getNewChannelController(channelRepo)
		testRouter := createTestRouter(getController)
		req, _ := http.NewRequest("GET", "/channel/"+consumerTestChannel.ChannelID+"/consumer/"+time.Now().String(), nil)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusNotFound, rr.Code)
	})
}

func TestConsumerDelete(t *testing.T) {
	t.Run("SuccessfulDelete", func(t *testing.T) {
		t.Parallel()
		getConsumerController := getNewConsumerController(consumerRepo)
		testRouter := createTestRouter(getConsumerController)
		testURI := getConsumerController.FormatAsRelativeLink(httprouter.Param{Key: channelIDPathParamKey, Value: consumerTestChannel.ChannelID}, httprouter.Param{Key: consumerIDPathParamKey, Value: deleteConsumerIDWithData})
		t.Log(testURI)
		req, _ := http.NewRequest("DELETE", testURI, nil)
		req.Header.Add(headerUnmodifiedSince, deleteUnmodifiedSince)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusNoContent, rr.Code)
	})
	t.Run("DeleteWithoutLastModified", func(t *testing.T) {
		t.Parallel()
		getConsumerController := getNewConsumerController(consumerRepo)
		testRouter := createTestRouter(getConsumerController)
		testURI := getConsumerController.FormatAsRelativeLink(httprouter.Param{Key: channelIDPathParamKey, Value: consumerTestChannel.ChannelID}, httprouter.Param{Key: consumerIDPathParamKey, Value: deleteConsumerIDFailed})
		t.Log(testURI)
		req, _ := http.NewRequest("DELETE", testURI, nil)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})
	t.Run("DeleteWithIncorrectLastModified", func(t *testing.T) {
		t.Parallel()
		getConsumerController := getNewConsumerController(consumerRepo)
		testRouter := createTestRouter(getConsumerController)
		testURI := getConsumerController.FormatAsRelativeLink(httprouter.Param{Key: channelIDPathParamKey, Value: consumerTestChannel.ChannelID}, httprouter.Param{Key: consumerIDPathParamKey, Value: deleteConsumerIDFailed})
		t.Log(testURI)
		req, _ := http.NewRequest("DELETE", testURI, nil)
		req.Header.Add(headerUnmodifiedSince, time.Now().Add(-1*30*24*60*60*time.Second).Format(http.TimeFormat))
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusPreconditionFailed, rr.Code)
	})
	t.Run("NotFound", func(t *testing.T) {
		t.Parallel()
		getController := getNewConsumerController(consumerRepo)
		testRouter := createTestRouter(getController)
		req, _ := http.NewRequest("DELETE", "/channel/"+consumerTestChannel.ChannelID+"/consumer/"+time.Now().String(), nil)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusNotFound, rr.Code)
	})
	t.Run("GetError", func(t *testing.T) {
		t.Parallel()
		mockConsumerRepo := new(storagemocks.ConsumerRepository)
		expectedErr := errors.New("test error")
		mockConsumerRepo.On("Get", mock.Anything, mock.Anything).Return(nil, expectedErr)
		getController := getNewConsumerController(mockConsumerRepo)
		testRouter := createTestRouter(getController)
		req, _ := http.NewRequest("DELETE", "/channel/"+consumerTestChannel.ChannelID+"/consumer/"+time.Now().String(), nil)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusInternalServerError, rr.Code)
		mockConsumerRepo.AssertExpectations(t)
	})
	t.Run("DeleteError", func(t *testing.T) {
		t.Parallel()
		mockConsumerRepo := new(storagemocks.ConsumerRepository)
		expectedErr := errors.New("test error")
		mockConsumerRepo.On("Get", mock.Anything, mock.Anything).Return(deleteFailedConsumer, nil)
		mockConsumerRepo.On("Delete", mock.Anything).Return(expectedErr)
		getController := getNewConsumerController(mockConsumerRepo)
		testRouter := createTestRouter(getController)
		req, _ := http.NewRequest("DELETE", "/channel/"+consumerTestChannel.ChannelID+"/consumer/"+time.Now().String(), nil)
		req.Header.Add(headerUnmodifiedSince, deleteFailedConsumer.GetLastUpdatedHTTPTimeString())
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusInternalServerError, rr.Code)
		mockConsumerRepo.AssertExpectations(t)

	})
}

func TestConsumerPut(t *testing.T) {
	t.Run("SuccessfulPutCreateWithNameToken", func(t *testing.T) {
		t.Parallel()
		putController := getNewConsumerController(consumerRepo)
		testRouter := createTestRouter(putController)
		testURI := putController.FormatAsRelativeLink(httprouter.Param{Key: channelIDPathParamKey, Value: consumerTestChannel.ChannelID}, httprouter.Param{Key: consumerIDPathParamKey, Value: createConsumerIDWithData})
		t.Log(testURI)
		req, _ := http.NewRequest("PUT", testURI, nil)
		req.Header.Add(headerContentType, formDataContentTypeHeaderValue)
		req.PostForm = url.Values{}
		req.PostForm.Add("token", successfulGetTestToken)
		req.PostForm.Add("name", "CREATE NAME")
		req.PostForm.Add("callbackUrl", callbackURL.String()+"test1")
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code)
		bodyChannel := &ConsumerModel{}
		json.NewDecoder(strings.NewReader(rr.Body.String())).Decode(bodyChannel)
		assert.Equal(t, createConsumerIDWithData, bodyChannel.ID)
		assert.Equal(t, "CREATE NAME", bodyChannel.Name)
		assert.Equal(t, callbackURL.String()+"test1", bodyChannel.CallbackURL)
		assert.Equal(t, successfulGetTestToken, bodyChannel.Token)
	})
	t.Run("SuccessfulPutCreateWithoutNameToken", func(t *testing.T) {
		t.Parallel()
		putController := getNewConsumerController(consumerRepo)
		testRouter := createTestRouter(putController)
		testURI := putController.FormatAsRelativeLink(httprouter.Param{Key: channelIDPathParamKey, Value: consumerTestChannel.ChannelID}, httprouter.Param{Key: consumerIDPathParamKey, Value: createConsumerIDWithoutData})
		t.Log(testURI)
		req, _ := http.NewRequest("PUT", testURI, nil)
		req.Header.Add(headerContentType, formDataContentTypeHeaderValue)
		req.PostForm = url.Values{}
		req.PostForm.Add("callbackUrl", callbackURL.String()+"test1")
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code)
		bodyChannel := &ConsumerModel{}
		json.NewDecoder(strings.NewReader(rr.Body.String())).Decode(bodyChannel)
		assert.Equal(t, createConsumerIDWithoutData, bodyChannel.ID)
		assert.Equal(t, createConsumerIDWithoutData, bodyChannel.Name)
		assert.Equal(t, callbackURL.String()+"test1", bodyChannel.CallbackURL)
		assert.True(t, len(bodyChannel.Token) == 12)
	})
	t.Run("SuccessfulPutUpdate", func(t *testing.T) {
		t.Parallel()
		putController := getNewConsumerController(consumerRepo)
		testRouter := createTestRouter(putController)
		testURI := putController.FormatAsRelativeLink(httprouter.Param{Key: channelIDPathParamKey, Value: consumerTestChannel.ChannelID}, httprouter.Param{Key: consumerIDPathParamKey, Value: listTestConsumerIDPrefix + "0"})
		t.Log(testURI)

		greq, _ := http.NewRequest("GET", testURI, nil)
		grr := httptest.NewRecorder()
		testRouter.ServeHTTP(grr, greq)
		assert.Equal(t, http.StatusOK, grr.Code)
		bodyChannel := &ConsumerModel{}
		json.NewDecoder(strings.NewReader(grr.Body.String())).Decode(bodyChannel)

		req, _ := http.NewRequest("PUT", testURI, nil)
		req.Header.Add(headerContentType, formDataContentTypeHeaderValue)
		req.Header.Add(headerUnmodifiedSince, bodyChannel.ChangedAt.Format(http.TimeFormat))
		req.PostForm = url.Values{}
		req.PostForm.Add("token", successfulGetTestToken+"Updated")
		req.PostForm.Add("callbackUrl", callbackURL.String()+"u-test1")
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code)
		updatedBodyChannel := &ConsumerModel{}
		json.NewDecoder(strings.NewReader(rr.Body.String())).Decode(updatedBodyChannel)
		assert.Equal(t, callbackURL.String()+"u-test1", updatedBodyChannel.CallbackURL)
		assert.Contains(t, updatedBodyChannel.Token, "Updated")
		assert.True(t, bodyChannel.ChangedAt.Before(updatedBodyChannel.ChangedAt))
	})
	t.Run("404", func(t *testing.T) {
		t.Parallel()
		putController := getNewConsumerController(consumerRepo)
		testRouter := createTestRouter(putController)
		testURI := putController.FormatAsRelativeLink(httprouter.Param{Key: channelIDPathParamKey, Value: "channel-does-exist"}, httprouter.Param{Key: consumerIDPathParamKey, Value: listTestConsumerIDPrefix})
		t.Log(testURI)
		req, _ := http.NewRequest("PUT", testURI, nil)
		req.PostForm = url.Values{}
		req.Header.Add(headerContentType, formDataContentTypeHeaderValue)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusNotFound, rr.Code)
	})
	t.Run("415", func(t *testing.T) {
		t.Parallel()
		putController := getNewConsumerController(consumerRepo)
		testRouter := createTestRouter(putController)
		testURI := putController.FormatAsRelativeLink(httprouter.Param{Key: channelIDPathParamKey, Value: consumerTestChannel.ChannelID}, httprouter.Param{Key: consumerIDPathParamKey, Value: deleteConsumerIDFailed})
		t.Log(testURI)
		req, _ := http.NewRequest("PUT", testURI, nil)
		req.PostForm = url.Values{}
		req.Header.Add(headerUnmodifiedSince, deleteFailedConsumer.GetLastUpdatedHTTPTimeString())
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusUnsupportedMediaType, rr.Code)
	})
	t.Run("400:NoCallbackURL", func(t *testing.T) {
		t.Parallel()
		putController := getNewConsumerController(consumerRepo)
		testRouter := createTestRouter(putController)
		testURI := putController.FormatAsRelativeLink(httprouter.Param{Key: channelIDPathParamKey, Value: consumerTestChannel.ChannelID}, httprouter.Param{Key: consumerIDPathParamKey, Value: deleteConsumerIDFailed})
		t.Log(testURI)
		req, _ := http.NewRequest("PUT", testURI, nil)
		req.PostForm = url.Values{}
		req.Header.Add(headerUnmodifiedSince, deleteFailedConsumer.GetLastUpdatedHTTPTimeString())
		req.Header.Add(headerContentType, formDataContentTypeHeaderValue)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})
	t.Run("400:InvalidURL", func(t *testing.T) {
		t.Parallel()
		putController := getNewConsumerController(consumerRepo)
		testRouter := createTestRouter(putController)
		testURI := putController.FormatAsRelativeLink(httprouter.Param{Key: channelIDPathParamKey, Value: consumerTestChannel.ChannelID}, httprouter.Param{Key: consumerIDPathParamKey, Value: deleteConsumerIDFailed})
		t.Log(testURI)
		req, _ := http.NewRequest("PUT", testURI, nil)
		req.PostForm = url.Values{}
		req.Header.Add(headerUnmodifiedSince, deleteFailedConsumer.GetLastUpdatedHTTPTimeString())
		req.Header.Add(headerContentType, formDataContentTypeHeaderValue)
		req.PostForm.Add("callbackUrl", "this is not a URL")
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})
	t.Run("400:RelativeURL", func(t *testing.T) {
		putController := getNewConsumerController(consumerRepo)
		testRouter := createTestRouter(putController)
		testURI := putController.FormatAsRelativeLink(httprouter.Param{Key: channelIDPathParamKey, Value: consumerTestChannel.ChannelID}, httprouter.Param{Key: consumerIDPathParamKey, Value: deleteConsumerIDFailed})
		t.Log(testURI)
		req, _ := http.NewRequest("PUT", testURI, nil)
		req.PostForm = url.Values{}
		req.Header.Add(headerUnmodifiedSince, deleteFailedConsumer.GetLastUpdatedHTTPTimeString())
		req.Header.Add(headerContentType, formDataContentTypeHeaderValue)
		req.PostForm.Add("callbackUrl", "./relative")
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})
	t.Run("400:NoUnmodifiedSinceHeader", func(t *testing.T) {
		t.Parallel()
		putController := getNewConsumerController(consumerRepo)
		testRouter := createTestRouter(putController)
		testURI := putController.FormatAsRelativeLink(httprouter.Param{Key: channelIDPathParamKey, Value: consumerTestChannel.ChannelID}, httprouter.Param{Key: consumerIDPathParamKey, Value: deleteConsumerIDFailed})
		t.Log(testURI)
		req, _ := http.NewRequest("PUT", testURI, nil)
		req.Header.Add(headerContentType, formDataContentTypeHeaderValue)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})
	t.Run("412", func(t *testing.T) {
		t.Parallel()
		putController := getNewConsumerController(consumerRepo)
		testRouter := createTestRouter(putController)
		testURI := putController.FormatAsRelativeLink(httprouter.Param{Key: channelIDPathParamKey, Value: consumerTestChannel.ChannelID}, httprouter.Param{Key: consumerIDPathParamKey, Value: deleteConsumerIDFailed})
		t.Log(testURI)
		req, _ := http.NewRequest("PUT", testURI, nil)
		req.Header.Add(headerUnmodifiedSince, time.Now().Add(-1*30*24*60*60*time.Second).Format(http.TimeFormat))
		req.Header.Add(headerContentType, formDataContentTypeHeaderValue)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusPreconditionFailed, rr.Code)
	})
	t.Run("500", func(t *testing.T) {
		t.Parallel()
		mockChannelRepo := new(storagemocks.ConsumerRepository)
		expectedErr := errors.New("error")
		mockChannelRepo.On("Get", mock.Anything, mock.Anything).Return(deleteFailedConsumer, nil)
		mockChannelRepo.On("Store", mock.Anything).Return(&data.Consumer{}, expectedErr)
		putController := getNewConsumerController(mockChannelRepo)
		testRouter := createTestRouter(putController)
		req, _ := http.NewRequest("PUT", "/channel/"+consumerTestChannel.ChannelID+"/consumer/"+time.Now().String(), nil)
		req.PostForm = url.Values{}
		req.Header.Add(headerUnmodifiedSince, deleteFailedConsumer.GetLastUpdatedHTTPTimeString())
		req.Header.Add(headerContentType, formDataContentTypeHeaderValue)
		req.PostForm.Add("callbackUrl", callbackURL.String())
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusInternalServerError, rr.Code)
	})
}
