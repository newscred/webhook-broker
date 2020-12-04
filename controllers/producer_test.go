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
	"time"

	"github.com/imyousuf/webhook-broker/storage"
	"github.com/imyousuf/webhook-broker/storage/data"
	"github.com/julienschmidt/httprouter"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

var producerRepo storage.ProducerRepository

const (
	successfulGetTestToken      = "sometokenforget"
	listTestProducerIDPrefix    = "controller-get-list-"
	createProducerIDWithData    = "put-producer-id"
	createProducerIDWithoutData = "put-producer-id-without-data"
)

// ProducerTestSetup is called from TestMain for the package
func ProducerTestSetup() {
	producerRepo = storage.NewProducerRepository(db)
	for index := 49; index > -1; index = index - 1 {
		indexString := strconv.Itoa(index)
		producer, err := data.NewProducer(listTestProducerIDPrefix+indexString, successfulGetTestToken+" - "+indexString)
		if err == nil {
			_, err = producerRepo.Store(producer)
		}
		if err != nil {
			log.Fatalln(err)
		}
	}
}

func TestProducersControllerGet(t *testing.T) {
	testRouter := httprouter.New()
	listController := NewProducersController(producerRepo, NewProducerController(producerRepo))
	setupAPIRoutes(testRouter, listController)
	req, _ := http.NewRequest("GET", "/producers", nil)
	rr := httptest.NewRecorder()
	testRouter.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)
	body := rr.Body.String()
	t.Log(body)
	bodyProducers := &Producers{}
	json.NewDecoder(strings.NewReader(body)).Decode(bodyProducers)
	assert.Equal(t, 25, len(bodyProducers.Producers))

	nextURL := bodyProducers.Pages[nextPaginationQueryParamKey]
	previousURL := bodyProducers.Pages[previousPaginationQueryParamKey]

	// Previous of first page should be empty
	preq, _ := http.NewRequest("GET", previousURL, nil)
	pr := httptest.NewRecorder()
	testRouter.ServeHTTP(pr, preq)
	assert.Equal(t, http.StatusOK, pr.Code)
	json.NewDecoder(strings.NewReader(pr.Body.String())).Decode(bodyProducers)
	assert.Equal(t, 0, len(bodyProducers.Producers))

	// Next of first page should have 25
	nreq, _ := http.NewRequest("GET", nextURL, nil)
	nr := httptest.NewRecorder()
	testRouter.ServeHTTP(nr, nreq)
	assert.Equal(t, http.StatusOK, nr.Code)
	json.NewDecoder(strings.NewReader(nr.Body.String())).Decode(bodyProducers)
	assert.Equal(t, 25, len(bodyProducers.Producers))
	nextURL = bodyProducers.Pages[nextPaginationQueryParamKey]
	previousURL = bodyProducers.Pages[previousPaginationQueryParamKey]

	// Previous of second page should be 25 or first page
	preq, _ = http.NewRequest("GET", previousURL, nil)
	pr = httptest.NewRecorder()
	testRouter.ServeHTTP(pr, preq)
	assert.Equal(t, http.StatusOK, pr.Code)
	json.NewDecoder(strings.NewReader(pr.Body.String())).Decode(bodyProducers)
	assert.Equal(t, 25, len(bodyProducers.Producers))

	// Next of second page should be empty
	nreq, _ = http.NewRequest("GET", nextURL, nil)
	nr = httptest.NewRecorder()
	testRouter.ServeHTTP(nr, nreq)
	assert.Equal(t, http.StatusOK, nr.Code)
	json.NewDecoder(strings.NewReader(nr.Body.String())).Decode(bodyProducers)
	assert.Equal(t, 0, len(bodyProducers.Producers))
}

func TestProducersControllerGet_Error(t *testing.T) {
	testRouter := httprouter.New()
	mockProducerRepo := new(ProducerRepositoryMockImpl)
	expectedErr := errors.New("GetList error")
	mockProducerRepo.On("GetList", mock.Anything).Return(nil, nil, expectedErr)
	listController := NewProducersController(mockProducerRepo, NewProducerController(mockProducerRepo))
	setupAPIRoutes(testRouter, listController)
	req, _ := http.NewRequest("GET", "/producers", nil)
	rr := httptest.NewRecorder()
	testRouter.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusInternalServerError, rr.Code)
}

func TestProducersFormatAsRelativeLink(t *testing.T) {
	listController := NewProducersController(producerRepo, NewProducerController(producerRepo))
	assert.Equal(t, "/producers", listController.FormatAsRelativeLink())
}

func TestProducerControllerFormatAsRelativeLink_NoParam(t *testing.T) {
	assert.Equal(t, "/producer/:producerId", NewProducerController(producerRepo).FormatAsRelativeLink())
}

func TestProducerGet(t *testing.T) {
	t.Run("SuccessfulGet", func(t *testing.T) {
		t.Parallel()
		testRouter := httprouter.New()
		getController := NewProducerController(producerRepo)
		setupAPIRoutes(testRouter, getController)
		req, _ := http.NewRequest("GET", "/producer/"+listTestProducerIDPrefix+"0", nil)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code)
		bodyProducer := &Producer{}
		json.NewDecoder(strings.NewReader(rr.Body.String())).Decode(bodyProducer)
		assert.Contains(t, bodyProducer.ID, listTestProducerIDPrefix)
		assert.Contains(t, bodyProducer.Name, listTestProducerIDPrefix)
		assert.Contains(t, bodyProducer.Token, successfulGetTestToken)
		assert.NotNil(t, bodyProducer.ChangedAt)
		assert.Equal(t, bodyProducer.ChangedAt.Format(http.TimeFormat), rr.HeaderMap.Get(headerLastModified))
	})
	t.Run("NotFound", func(t *testing.T) {
		t.Parallel()
		testRouter := httprouter.New()
		getController := NewProducerController(producerRepo)
		setupAPIRoutes(testRouter, getController)
		req, _ := http.NewRequest("GET", "/producer/"+time.Now().String(), nil)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusNotFound, rr.Code)
	})
}

func TestProducerPut(t *testing.T) {
	t.Run("SuccessfulPutCreateWithNameToken", func(t *testing.T) {
		t.Parallel()
		testRouter := httprouter.New()
		putController := NewProducerController(producerRepo)
		setupAPIRoutes(testRouter, putController)
		req, _ := http.NewRequest("PUT", "/producer/"+createProducerIDWithData, nil)
		req.Header.Add(headerContentType, formDataContentTypeHeaderValue)
		req.PostForm = url.Values{}
		req.PostForm.Add("token", successfulGetTestToken)
		req.PostForm.Add("name", "CREATE NAME")
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code)
		bodyProducer := &Producer{}
		json.NewDecoder(strings.NewReader(rr.Body.String())).Decode(bodyProducer)
		assert.Equal(t, createProducerIDWithData, bodyProducer.ID)
		assert.Equal(t, "CREATE NAME", bodyProducer.Name)
		assert.Equal(t, successfulGetTestToken, bodyProducer.Token)
	})
	t.Run("SuccessfulPutCreateWithoutNameToken", func(t *testing.T) {
		t.Parallel()
		testRouter := httprouter.New()
		putController := NewProducerController(producerRepo)
		setupAPIRoutes(testRouter, putController)
		req, _ := http.NewRequest("PUT", "/producer/"+createProducerIDWithoutData, nil)
		req.Header.Add(headerContentType, formDataContentTypeHeaderValue)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code)
		bodyProducer := &Producer{}
		json.NewDecoder(strings.NewReader(rr.Body.String())).Decode(bodyProducer)
		assert.Equal(t, createProducerIDWithoutData, bodyProducer.ID)
		assert.Equal(t, createProducerIDWithoutData, bodyProducer.Name)
		assert.Equal(t, 12, len(bodyProducer.Token))
	})
	t.Run("SuccessfulPutUpdate", func(t *testing.T) {
		t.Parallel()
		testRouter := httprouter.New()
		putController := NewProducerController(producerRepo)
		setupAPIRoutes(testRouter, putController)
		greq, _ := http.NewRequest("GET", "/producer/"+listTestProducerIDPrefix+"0", nil)
		grr := httptest.NewRecorder()
		testRouter.ServeHTTP(grr, greq)
		assert.Equal(t, http.StatusOK, grr.Code)
		bodyProducer := &Producer{}
		json.NewDecoder(strings.NewReader(grr.Body.String())).Decode(bodyProducer)
		req, _ := http.NewRequest("PUT", "/producer/"+listTestProducerIDPrefix+"0", nil)
		req.Header.Add(headerContentType, formDataContentTypeHeaderValue)
		req.Header.Add(headerUnmodifiedSince, bodyProducer.ChangedAt.Format(http.TimeFormat))
		req.PostForm = url.Values{}
		req.PostForm.Add("token", successfulGetTestToken+" - 0 Updated")
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code)
		updatedBodyProducer := &Producer{}
		json.NewDecoder(strings.NewReader(rr.Body.String())).Decode(updatedBodyProducer)
		assert.Contains(t, updatedBodyProducer.Token, "Updated")
		assert.True(t, bodyProducer.ChangedAt.Before(updatedBodyProducer.ChangedAt))
	})
	t.Run("415", func(t *testing.T) {
		t.Parallel()
		testRouter := httprouter.New()
		putController := NewProducerController(producerRepo)
		setupAPIRoutes(testRouter, putController)
		req, _ := http.NewRequest("PUT", "/producer/"+listTestProducerIDPrefix+"0", nil)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusUnsupportedMediaType, rr.Code)
	})
	t.Run("400", func(t *testing.T) {
		t.Parallel()
		testRouter := httprouter.New()
		putController := NewProducerController(producerRepo)
		setupAPIRoutes(testRouter, putController)
		req, _ := http.NewRequest("PUT", "/producer/"+listTestProducerIDPrefix+"0", nil)
		req.Header.Add(headerContentType, formDataContentTypeHeaderValue)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})
	t.Run("412", func(t *testing.T) {
		t.Parallel()
		testRouter := httprouter.New()
		putController := NewProducerController(producerRepo)
		setupAPIRoutes(testRouter, putController)
		req, _ := http.NewRequest("PUT", "/producer/"+listTestProducerIDPrefix+"0", nil)
		req.Header.Add(headerContentType, formDataContentTypeHeaderValue)
		req.Header.Add(headerUnmodifiedSince, time.Now().Add(-1*time.Duration(10)*time.Hour).Format(http.TimeFormat))
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusPreconditionFailed, rr.Code)
	})
	t.Run("500", func(t *testing.T) {
		t.Parallel()
		mockProducerRepo := new(ProducerRepositoryMockImpl)
		expectedErr := errors.New("error")
		mockProducerRepo.On("Get", mock.Anything).Return(nil, expectedErr)
		mockProducerRepo.On("Store", mock.Anything).Return(nil, expectedErr)
		testRouter := httprouter.New()
		putController := NewProducerController(mockProducerRepo)
		setupAPIRoutes(testRouter, putController)
		req, _ := http.NewRequest("PUT", "/producer/"+listTestProducerIDPrefix+"0", nil)
		req.Header.Add(headerContentType, formDataContentTypeHeaderValue)
		rr := httptest.NewRecorder()
		testRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusInternalServerError, rr.Code)
	})
}
