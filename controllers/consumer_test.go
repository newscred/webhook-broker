package controllers

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/imyousuf/webhook-broker/storage"
	"github.com/imyousuf/webhook-broker/storage/data"
	storagemocks "github.com/imyousuf/webhook-broker/storage/mocks"
	"github.com/julienschmidt/httprouter"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

var (
	consumerRepo        storage.ConsumerRepository
	callbackURL         *url.URL
	consumerTestChannel *data.Channel
)

const (
	listTestConsumerIDPrefix    = "consumer-get-list-"
	channelTestConsumerID       = "consumer-channel-some-id"
	createConsumerIDWithData    = "put-consumer-id"
	createConsumerIDWithoutData = "put-consumer-id-without-data"
)

// ChannelTestSetup is called from TestMain for the package
func ConsumerTestSetup() {
	consumerRepo = storage.NewConsumerRepository(db, channelRepo)
	channel, err := data.NewChannel(channelTestConsumerID, successfulGetTestToken)
	if err != nil {
		log.Fatalln(err)
	}
	consumerTestChannel, err = channelRepo.Store(channel)
	if err != nil {
		log.Fatalln(err)
	}
	callbackURL, err = url.Parse("https://imytech.net/")
	if err != nil {
		log.Fatalln(err)
	}
	for index := 49; index > -1; index = index - 1 {
		indexString := strconv.Itoa(index)
		consumer, err := data.NewConsumer(consumerTestChannel, listTestConsumerIDPrefix+indexString, successfulGetTestToken+" - "+indexString, callbackURL)
		if err == nil {
			_, err = consumerRepo.Store(consumer)
		}
		if err != nil {
			log.Fatalln(err)
		}
	}
}

func TestConsumerFormatAsRelativeLink(t *testing.T) {
	t.Parallel()
	controller := NewConsumerController(NewChannelController(nil), nil, nil)
	assert.Equal(t, consumerPath, controller.GetPath())
	assert.Equal(t, "/channel/someChannelId/consumer/someConsumerId", controller.FormatAsRelativeLink(httprouter.Param{Key: channelIDPathParamKey, Value: "someChannelId"},
		httprouter.Param{Key: consumerIDPathParamKey, Value: "someConsumerId"}))
}

func TestConsumersFormatAsRelativeLink(t *testing.T) {
	t.Parallel()
	channelEndpoint := NewChannelController(nil)
	controller := NewConsumersController(channelEndpoint, NewConsumerController(channelEndpoint, nil, nil), nil, nil)
	assert.Equal(t, consumersPath, controller.GetPath())
	assert.Equal(t, "/channel/someChannelId/consumers", controller.FormatAsRelativeLink(httprouter.Param{Key: channelIDPathParamKey, Value: "someChannelId"}))
}

func TestConsumersControllerGet(t *testing.T) {
	testRouter := httprouter.New()
	channelController := NewChannelController(channelRepo)
	listController := NewConsumersController(channelController, NewConsumerController(channelController, channelRepo, consumerRepo), channelRepo, consumerRepo)
	setupAPIRoutes(testRouter, listController)
	testURI := listController.FormatAsRelativeLink(httprouter.Param{Key: channelIDPathParamKey, Value: consumerTestChannel.ChannelID})
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
	preq, _ := http.NewRequest("GET", previousURL, nil)
	pr := httptest.NewRecorder()
	testRouter.ServeHTTP(pr, preq)
	assert.Equal(t, http.StatusOK, pr.Code)
	json.NewDecoder(strings.NewReader(pr.Body.String())).Decode(bodyChannels)
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
		testRouter := httprouter.New()
		mockConsumerRepo := new(storagemocks.ConsumerRepository)
		expectedErr := errors.New("GetList error")
		mockConsumerRepo.On("GetList", consumerTestChannel.ChannelID, mock.Anything).Return(nil, nil, expectedErr)
		channelController := NewChannelController(channelRepo)
		listController := NewConsumersController(channelController, NewConsumerController(channelController, channelRepo, mockConsumerRepo), channelRepo, mockConsumerRepo)
		setupAPIRoutes(testRouter, listController)
		testURI := listController.FormatAsRelativeLink(httprouter.Param{Key: channelIDPathParamKey, Value: consumerTestChannel.ChannelID})
		req, _ := http.NewRequest("GET", testURI, nil)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusInternalServerError, rr.Code)
	})
	t.Run("GetList:NoChannel", func(t *testing.T) {
		t.Parallel()
		testRouter := httprouter.New()
		channelController := NewChannelController(channelRepo)
		listController := NewConsumersController(channelController, NewConsumerController(channelController, channelRepo, consumerRepo), channelRepo, consumerRepo)
		setupAPIRoutes(testRouter, listController)
		testURI := listController.FormatAsRelativeLink(httprouter.Param{Key: channelIDPathParamKey, Value: "no-such-channel-for-get-consumers"})
		req, _ := http.NewRequest("GET", testURI, nil)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusNotFound, rr.Code)
	})
}
