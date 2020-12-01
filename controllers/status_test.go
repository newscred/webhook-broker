package controllers

import (
	"bytes"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/imyousuf/webhook-broker/config"
	"github.com/imyousuf/webhook-broker/storage/data"
	"github.com/julienschmidt/httprouter"
	"github.com/stretchr/testify/assert"
)

var (
	configuration *config.Config
	seedData      *config.SeedData
	defaultApp    *data.App
)

func TestMain(m *testing.M) {
	var err error
	configuration, err = config.GetAutoConfiguration()
	if err == nil {
		seedData = &configuration.SeedData
		defaultApp = data.NewApp(seedData, data.Initialized)
		m.Run()
	} else {
		log.Fatalln(err)
	}
}

func TestStatus(t *testing.T) {
	mAppRepo := new(AppRepositoryMockImpl)
	testRouter := httprouter.New()
	setupAPIRoutes(testRouter, NewStatusController(mAppRepo))
	mAppRepo.On("GetApp").Return(defaultApp, nil)
	req, _ := http.NewRequest("GET", "/_status", nil)
	rr := httptest.NewRecorder()
	testRouter.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)
	outAppData := &AppData{}
	body := rr.Body.String()
	t.Log(body)
	json.NewDecoder(strings.NewReader(body)).Decode(outAppData)
	assert.Equal(t, AppData{SeedData: defaultApp.GetSeedData(), AppStatus: defaultApp.GetStatus()}, *outAppData)
	mAppRepo.AssertExpectations(t)
}

func TestStatus_AppDataError(t *testing.T) {
	mAppRepo := new(AppRepositoryMockImpl)
	testRouter := httprouter.New()
	setupAPIRoutes(testRouter, NewStatusController(mAppRepo))
	err := errors.New("App could not be returned")
	mAppRepo.On("GetApp").Return(defaultApp, err)
	req, _ := http.NewRequest("GET", "/_status", nil)
	rr := httptest.NewRecorder()
	testRouter.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusInternalServerError, rr.Code)
	assert.Equal(t, err.Error(), rr.Body.String())
	mAppRepo.AssertExpectations(t)
}

func TestStatus_JSONMarshalError(t *testing.T) {
	mAppRepo := new(AppRepositoryMockImpl)
	testRouter := httprouter.New()
	setupAPIRoutes(testRouter, NewStatusController(mAppRepo))
	mAppRepo.On("GetApp").Return(defaultApp, nil)
	err := errors.New("App could not be returned")
	oldGetJSON := getJSON
	getJSON = func(buf *bytes.Buffer, app *data.App) error {
		return err
	}
	defer func() {
		getJSON = oldGetJSON
	}()
	req, _ := http.NewRequest("GET", "/_status", nil)
	rr := httptest.NewRecorder()
	testRouter.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusInternalServerError, rr.Code)
	assert.Equal(t, err.Error(), rr.Body.String())
	mAppRepo.AssertExpectations(t)
}
