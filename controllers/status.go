package controllers

import (
	"bytes"
	"encoding/json"
	"net/http"

	"github.com/imyousuf/webhook-broker/config"
	"github.com/imyousuf/webhook-broker/storage"
	"github.com/imyousuf/webhook-broker/storage/data"
	"github.com/julienschmidt/httprouter"
)

const (
	statusPath = "/_status"
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
