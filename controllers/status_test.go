package controllers

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/julienschmidt/httprouter"
	"github.com/newscred/webhook-broker/config"
	"github.com/newscred/webhook-broker/storage"
	"github.com/newscred/webhook-broker/storage/data"
	storagemocks "github.com/newscred/webhook-broker/storage/mocks"
	"github.com/stretchr/testify/assert"
)

var (
	configuration *config.Config
	seedData      *config.SeedData
	defaultApp    *data.App
	db            *sql.DB
)

func TestMain(m *testing.M) {
	os.Remove("./webhook-broker.sqlite3")
	var err error
	configuration, err = config.GetConfiguration("./controller-test-config.cfg")
	if err == nil {
		seedData = &configuration.SeedData
		defaultApp = data.NewApp(seedData, data.Initialized)
		migrationLocation, _ := filepath.Abs("../migration/sqls/")
		os.Remove("./webhook-broker.sqlite3")
		db, err = storage.GetConnectionPool(configuration, &storage.MigrationConfig{MigrationEnabled: true, MigrationSource: "file://" + migrationLocation}, configuration)
		if err == nil {
			// ORDER needs to be maintained
			ProducerTestSetup()
			ChannelTestSetup()
			ConsumerTestSetup()
			BroadcastTestSetup()
			MessageTestSetup()
			JobTestSetup()
			m.Run()
			db.Close()
		}
	}
	if err != nil {
		panic(err)
	}
}

func createTestRouter(endpoints ...EndpointController) http.Handler {
	testRouter := httprouter.New()
	setupAPIRoutes(testRouter, endpoints...)
	return getHandler(testRouter)
}

func TestStatus(t *testing.T) {
	mAppRepo := new(storagemocks.AppRepository)
	statusController := NewStatusController(mAppRepo)
	testRouter := createTestRouter(statusController)
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
	assert.Equal(t, statusPath, statusController.FormatAsRelativeLink())
}

func TestStatus_AppDataError(t *testing.T) {
	mAppRepo := new(storagemocks.AppRepository)
	testRouter := createTestRouter(NewStatusController(mAppRepo))
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
	mAppRepo := new(storagemocks.AppRepository)
	testRouter := createTestRouter(NewStatusController(mAppRepo))
	mAppRepo.On("GetApp").Return(defaultApp, nil)
	err := errors.New("App could not be returned")
	oldGetJSON := getJSON
	getJSON = func(buf *bytes.Buffer, app interface{}) error {
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

func TestJobStatusController_Get(t *testing.T) {
	repo := new(storagemocks.DeliveryJobRepository)
	controller := NewJobStatusController(repo)
	router := createTestRouter(controller)

	t.Run("GetJobStatusCountsGroupedByConsumer returns error", func(t *testing.T) {
		mockCall := repo.On("GetJobStatusCountsGroupedByConsumer").Return(nil, errors.New("some-error"))
		req, _ := http.NewRequest("GET", "/job-status", nil)
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusInternalServerError, rr.Code)
		mockCall.Unset()
	})

	t.Run("GetJobStatusCountsGroupedByConsumer returns empty result", func(t *testing.T) {
		mockCall := repo.On("GetJobStatusCountsGroupedByConsumer").Return(map[storage.Channel_ID]map[storage.Consumer_ID][]*data.StatusCount[data.JobStatus]{}, nil) // Return empty map
		req, _ := http.NewRequest("GET", "/job-status", nil)
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code)
		assert.JSONEq(t, "{}", rr.Body.String()) // Empty JSON object expected
		mockCall.Unset()
	})

	t.Run("GetJobStatusCountsGroupedByConsumer returns results", func(t *testing.T) {
		jobStatus := map[storage.Channel_ID]map[storage.Consumer_ID][]*data.StatusCount[data.JobStatus]{
			storage.Channel_ID("channel1"): {
				storage.Consumer_ID("consumer1"): []*data.StatusCount[data.JobStatus]{
					{Status: data.JobQueued, Count: 2},
					{Status: data.JobDead, Count: 5},
				},
			},
			storage.Channel_ID("channel2"): {
				storage.Consumer_ID("consumer2"): []*data.StatusCount[data.JobStatus]{
					{Status: data.JobInflight, Count: 1},
					{Status: data.JobDelivered, Count: 3},
				},
			},
		}

		mockCall := repo.On("GetJobStatusCountsGroupedByConsumer").Return(jobStatus, nil)
		req, _ := http.NewRequest("GET", "/job-status", nil)
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code)

		// Marshal and unmarshal to handle map comparison and ordering
		var actual interface{}
		err := json.Unmarshal(rr.Body.Bytes(), &actual)
		assert.NoError(t, err)

		expectedJSON, err := json.Marshal(jobStatus)
		assert.NoError(t, err)
		var expected interface{}
		err = json.Unmarshal(expectedJSON, &expected)
		assert.NoError(t, err)

		assert.Equal(t, expected, actual)

		mockCall.Unset()
	})
}
