package controllers

import (
	"bytes"
	"encoding/json"
	"net/http"

	"github.com/julienschmidt/httprouter"
	"github.com/newscred/webhook-broker/config"
	"github.com/newscred/webhook-broker/storage"
	"github.com/newscred/webhook-broker/storage/data"
)

const (
	statusPath    = "/_status"
	jobStatusPath = "/job-status"
)

// AppData to deserialize in status endpoint
type AppData struct {
	SeedData  *config.SeedData
	AppStatus data.AppStatus
}

var getJSON = func(buf *bytes.Buffer, data interface{}) error {
	return json.NewEncoder(buf).Encode(data)
}

// NewStatusController Factory for new StatusController
func NewStatusController(appRepo storage.AppRepository) *StatusController {
	statusController := &StatusController{appRepository: appRepo}
	return statusController
}

// StatusController is the controller for `/_status` endpoint
type StatusController struct {
	appRepository storage.AppRepository
}

// GetPath returns the endpoint path
func (cont *StatusController) GetPath() string {
	return statusPath
}

// FormatAsRelativeLink Format as relative URL of this resource based on the params
func (cont *StatusController) FormatAsRelativeLink(params ...httprouter.Param) string {
	return statusPath
}

// Get is the GET /_status endpoint controller
func (cont *StatusController) Get(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	app, err := cont.appRepository.GetApp()
	if err != nil {
		// return error
		writeErr(w, err)
		return
	}
	data := AppData{SeedData: app.GetSeedData(), AppStatus: app.GetStatus()}
	writeJSON(w, data)
}

type JobStatusController struct {
	DeliveryJobRepo storage.DeliveryJobRepository
}

// NewJobStatusController Factory for new JobStatusController
func NewJobStatusController(deliveryJobRepo storage.DeliveryJobRepository) *JobStatusController {
	statusController := &JobStatusController{DeliveryJobRepo: deliveryJobRepo}
	return statusController
}

// GetPath returns the endpoint path
func (cont *JobStatusController) GetPath() string {
	return jobStatusPath
}

// FormatAsRelativeLink Format as relative URL of this resource based on the params
func (cont *JobStatusController) FormatAsRelativeLink(params ...httprouter.Param) string {
	return cont.GetPath()
}

// Get is the GET /job-status endpoint controller
func (cont *JobStatusController) Get(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	status, err := cont.DeliveryJobRepo.GetJobStatusCountsGroupedByConsumer()
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, status)
}
