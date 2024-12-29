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

	"github.com/newscred/webhook-broker/storage"
	"github.com/newscred/webhook-broker/storage/data"
	storagemocks "github.com/newscred/webhook-broker/storage/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

var channelRepo storage.ChannelRepository
var cachedChannelRepo storage.PseudoChannelRepository

const (
	listTestChannelIDPrefix    = "controller-get-list-"
	createChannelIDWithData    = "put-channel-id"
	createChannelIDWithoutData = "put-channel-id-without-data"
)

// ChannelTestSetup is called from TestMain for the package
func ChannelTestSetup() {
	channelRepo = storage.NewChannelRepository(db)
	cachedChannelRepo = storage.NewCachedChannelRepository(channelRepo, 2*time.Second)
	for index := 49; index > -1; index = index - 1 {
		indexString := strconv.Itoa(index)
		channel, err := data.NewChannel(listTestChannelIDPrefix+indexString, successfulGetTestToken+" - "+indexString)
		if err == nil {
			_, err = channelRepo.Store(channel)
		}
		if err != nil {
			log.Fatal().Err(err)
		}
	}
}

func TestChannelsControllerGet(t *testing.T) {
	testRouter := createTestRouter(NewChannelsController(channelRepo, getNewChannelController(channelRepo)))
	req, _ := http.NewRequest("GET", "/channels", nil)
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

func TestChannelsControllerGet_Error(t *testing.T) {
	mockChannelRepo := new(storagemocks.ChannelRepository)
	expectedErr := errors.New("GetList error")
	mockChannelRepo.On("GetList", mock.Anything).Return(nil, nil, expectedErr)
	testRouter := createTestRouter(NewChannelsController(mockChannelRepo, getNewChannelController(mockChannelRepo)))
	req, _ := http.NewRequest("GET", "/channels", nil)
	rr := httptest.NewRecorder()
	testRouter.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusInternalServerError, rr.Code)
}

func TestChannelsFormatAsRelativeLink(t *testing.T) {
	listController := NewChannelsController(channelRepo, getNewChannelController(channelRepo))
	assert.Equal(t, "/channels", listController.FormatAsRelativeLink())
}

func TestChannelControllerFormatAsRelativeLink_NoParam(t *testing.T) {
	assert.Equal(t, "/channel/:channelId", getNewChannelController(channelRepo).FormatAsRelativeLink())
}

func TestChannelGet(t *testing.T) {
	t.Run("SuccessfulGet", func(t *testing.T) {
		t.Parallel()
		testRouter := createTestRouter(getNewChannelController(channelRepo))
		req, _ := http.NewRequest("GET", "/channel/"+listTestChannelIDPrefix+"0", nil)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code)
		bodyChannel := &ChannelModel{}
		json.NewDecoder(strings.NewReader(rr.Body.String())).Decode(bodyChannel)
		assert.Contains(t, bodyChannel.ID, listTestChannelIDPrefix)
		assert.Contains(t, bodyChannel.Name, listTestChannelIDPrefix)
		assert.Contains(t, bodyChannel.Token, successfulGetTestToken)
		assert.Equal(t, "/channel/"+listTestChannelIDPrefix+"0/consumers", bodyChannel.ConsumersURL)
		assert.Equal(t, "/channel/"+listTestChannelIDPrefix+"0/messages", bodyChannel.MessagesURL)
		assert.Equal(t, "/channel/"+listTestChannelIDPrefix+"0/broadcast", bodyChannel.BroadcastURL)
		assert.NotNil(t, bodyChannel.ChangedAt)
		assert.Equal(t, bodyChannel.ChangedAt.Format(http.TimeFormat), rr.HeaderMap.Get(headerLastModified))
	})
	t.Run("NotFound", func(t *testing.T) {
		t.Parallel()
		testRouter := createTestRouter(getNewChannelController(channelRepo))
		req, _ := http.NewRequest("GET", "/channel/"+time.Now().String(), nil)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusNotFound, rr.Code)
	})
}

func TestCachedChannelGet(t *testing.T) {
	t.Run("NotFound", func(t *testing.T) {
		t.Parallel()
		testRouter := createTestRouter(getNewChannelController(cachedChannelRepo))
		req, _ := http.NewRequest("GET", "/channel/"+time.Now().String(), nil)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusNotFound, rr.Code)
	})
}

func getNewChannelController(channelRepo storage.ChannelRepository) *ChannelController {
	bc, _ := getNewBroadcastController(messageRepo)
	return NewChannelController(NewConsumersController(NewConsumerController(nil, nil, getDLQControllerWithMockedRepo()), nil), getMessagesController(), bc, channelRepo)
}

func TestChannelPut(t *testing.T) {
	t.Run("SuccessfulPutCreateWithNameToken", func(t *testing.T) {
		t.Parallel()
		testRouter := createTestRouter(getNewChannelController(channelRepo))
		req, _ := http.NewRequest("PUT", "/channel/"+createChannelIDWithData, nil)
		req.Header.Add(headerContentType, formDataContentTypeHeaderValue)
		req.PostForm = url.Values{}
		req.PostForm.Add("token", successfulGetTestToken)
		req.PostForm.Add("name", "CREATE NAME")
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code)
		bodyChannel := &MsgStakeholder{}
		json.NewDecoder(strings.NewReader(rr.Body.String())).Decode(bodyChannel)
		assert.Equal(t, createChannelIDWithData, bodyChannel.ID)
		assert.Equal(t, "CREATE NAME", bodyChannel.Name)
		assert.Equal(t, successfulGetTestToken, bodyChannel.Token)
	})
	t.Run("SuccessfulPutCreateWithoutNameToken", func(t *testing.T) {
		t.Parallel()
		testRouter := createTestRouter(getNewChannelController(channelRepo))
		req, _ := http.NewRequest("PUT", "/channel/"+createChannelIDWithoutData, nil)
		req.Header.Add(headerContentType, formDataContentTypeHeaderValue)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code)
		bodyChannel := &MsgStakeholder{}
		json.NewDecoder(strings.NewReader(rr.Body.String())).Decode(bodyChannel)
		assert.Equal(t, createChannelIDWithoutData, bodyChannel.ID)
		assert.Equal(t, createChannelIDWithoutData, bodyChannel.Name)
		assert.Equal(t, 12, len(bodyChannel.Token))
	})
	t.Run("SuccessfulPutUpdate", func(t *testing.T) {
		t.Parallel()
		testRouter := createTestRouter(getNewChannelController(channelRepo))
		greq, _ := http.NewRequest("GET", "/channel/"+listTestChannelIDPrefix+"0", nil)
		grr := httptest.NewRecorder()
		testRouter.ServeHTTP(grr, greq)
		assert.Equal(t, http.StatusOK, grr.Code)
		bodyChannel := &MsgStakeholder{}
		json.NewDecoder(strings.NewReader(grr.Body.String())).Decode(bodyChannel)
		req, _ := http.NewRequest("PUT", "/channel/"+listTestChannelIDPrefix+"0", nil)
		req.Header.Add(headerContentType, formDataContentTypeHeaderValue)
		req.Header.Add(headerUnmodifiedSince, bodyChannel.ChangedAt.Format(http.TimeFormat))
		req.PostForm = url.Values{}
		req.PostForm.Add("token", successfulGetTestToken+" - 0 Updated")
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code)
		updatedBodyChannel := &MsgStakeholder{}
		json.NewDecoder(strings.NewReader(rr.Body.String())).Decode(updatedBodyChannel)
		assert.Contains(t, updatedBodyChannel.Token, "Updated")
		assert.True(t, bodyChannel.ChangedAt.Before(updatedBodyChannel.ChangedAt))
	})
	t.Run("415", func(t *testing.T) {
		t.Parallel()
		testRouter := createTestRouter(getNewChannelController(channelRepo))
		req, _ := http.NewRequest("PUT", "/channel/"+listTestChannelIDPrefix+"0", nil)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusUnsupportedMediaType, rr.Code)
	})
	t.Run("400", func(t *testing.T) {
		t.Parallel()
		testRouter := createTestRouter(getNewChannelController(channelRepo))
		req, _ := http.NewRequest("PUT", "/channel/"+listTestChannelIDPrefix+"0", nil)
		req.Header.Add(headerContentType, formDataContentTypeHeaderValue)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})
	t.Run("412", func(t *testing.T) {
		t.Parallel()
		testRouter := createTestRouter(getNewChannelController(channelRepo))
		req, _ := http.NewRequest("PUT", "/channel/"+listTestChannelIDPrefix+"0", nil)
		req.Header.Add(headerContentType, formDataContentTypeHeaderValue)
		req.Header.Add(headerUnmodifiedSince, time.Now().Add(-1*time.Duration(10)*time.Hour).Format(http.TimeFormat))
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusPreconditionFailed, rr.Code)
	})
	t.Run("500", func(t *testing.T) {
		t.Parallel()
		mockChannelRepo := new(storagemocks.ChannelRepository)
		expectedErr := errors.New("error")
		mockChannelRepo.On("Get", mock.Anything).Return(&data.Channel{}, expectedErr)
		mockChannelRepo.On("Store", mock.Anything).Return(&data.Channel{}, expectedErr)
		testRouter := createTestRouter(getNewChannelController(mockChannelRepo))
		req, _ := http.NewRequest("PUT", "/channel/"+listTestChannelIDPrefix+"0", nil)
		req.Header.Add(headerContentType, formDataContentTypeHeaderValue)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusInternalServerError, rr.Code)
	})
}
