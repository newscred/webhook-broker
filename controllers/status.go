package controllers

import (
	"bytes"
	"encoding/json"
	"net/http"

	"github.com/imyousuf/webhook-broker/config"
	"github.com/imyousuf/webhook-broker/storage/data"
	"github.com/julienschmidt/httprouter"
)

// AppData to deserialize in status endpoint
type AppData struct {
	SeedData  *config.SeedData
	AppStatus data.AppStatus
}

var getJSON = func(buf *bytes.Buffer, app *data.App) error {
	return json.NewEncoder(buf).Encode(AppData{SeedData: app.GetSeedData(), AppStatus: app.GetStatus()})
}

// Status represents the endpoint for /_status
func Status(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	app, err := dataAccessor.GetAppRepository().GetApp()
	if err != nil {
		// return error
		w.WriteHeader(500)
		w.Write([]byte(err.Error()))
		return
	}
	// Write Config JSON
	var buf bytes.Buffer
	err = getJSON(&buf, app)
	if err != nil {
		// return error
		w.WriteHeader(500)
		w.Write([]byte(err.Error()))
		return
	}
	w.WriteHeader(200)
	w.Header().Add("Content-Type", "application/json")
	w.Write(buf.Bytes())
}
