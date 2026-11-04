package controllers

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/julienschmidt/httprouter"

	"github.com/newscred/webhook-broker/config"
	"github.com/newscred/webhook-broker/dispatcher"
	"github.com/newscred/webhook-broker/parking"
	"github.com/newscred/webhook-broker/storage"
)

var (
	errAdminTokenMismatch = errors.New("admin token missing or not matching")
	errTooManyJobIDs      = errors.New("too many job ids in one request")
	errNodeBusy           = errors.New("node queue above high-water mark")
)

// isAdminAuthorized compares the X-Broker-Admin-Token header with the configured token in constant time
func isAdminAuthorized(r *http.Request, pauseConfig config.DispatchPauseConfig) bool {
	expected := []byte(pauseConfig.GetAdminToken())
	actual := []byte(r.Header.Get(parking.AdminTokenHeader))
	return len(expected) > 0 && subtle.ConstantTimeCompare(expected, actual) == 1
}

// writeJSONWithStatus writes data as JSON with the given status code
func writeJSONWithStatus(w http.ResponseWriter, code int, data interface{}) {
	var buf bytes.Buffer
	if err := getJSON(&buf, data); err != nil {
		writeErr(w, err)
		return
	}
	w.Header().Add(headerContentType, "application/json")
	w.WriteHeader(code)
	w.Write(buf.Bytes())
}

// InternalJobEnqueueController receives job ids handed off by the drainer of another (or the same) node through the
// load-balanced broker endpoint, and puts those jobs in this node's in-memory queue
type InternalJobEnqueueController struct {
	DeliveryJobRepo storage.DeliveryJobRepository
	Dispatcher      dispatcher.MessageDispatcher
	PauseConfig     config.DispatchPauseConfig
}

// NewInternalJobEnqueueController creates the internal enqueue controller
func NewInternalJobEnqueueController(deliveryJobRepo storage.DeliveryJobRepository, messageDispatcher dispatcher.MessageDispatcher, pauseConfig config.DispatchPauseConfig) *InternalJobEnqueueController {
	return &InternalJobEnqueueController{DeliveryJobRepo: deliveryJobRepo, Dispatcher: messageDispatcher, PauseConfig: pauseConfig}
}

// GetPath returns the endpoint's path
func (controller *InternalJobEnqueueController) GetPath() string {
	return parking.InternalEnqueuePath
}

// FormatAsRelativeLink returns the endpoint's path
func (controller *InternalJobEnqueueController) FormatAsRelativeLink(params ...httprouter.Param) string {
	return parking.InternalEnqueuePath
}

// Post implements POST /_internal/jobs/enqueue
func (controller *InternalJobEnqueueController) Post(w http.ResponseWriter, r *http.Request, params httprouter.Params) {
	if !controller.PauseConfig.IsAdminTokenConfigured() {
		writeNotFound(w)
		return
	}
	if !isAdminAuthorized(r, controller.PauseConfig) {
		writeStatus(w, http.StatusUnauthorized, errAdminTokenMismatch)
		return
	}
	request := parking.EnqueueRequest{}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeBadRequest(w)
		return
	}
	if len(request.JobIDs) > controller.PauseConfig.GetDrainPageSize() {
		writeStatus(w, http.StatusBadRequest, errTooManyJobIDs)
		return
	}
	if controller.Dispatcher.QueueDepth()+len(request.JobIDs) > controller.PauseConfig.GetHandoffHighWaterMark() {
		w.Header().Set("Retry-After", "1")
		writeStatus(w, http.StatusServiceUnavailable, errNodeBusy)
		return
	}
	jobs, err := controller.DeliveryJobRepo.GetQueuedJobsByIDs(request.JobIDs)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSONWithStatus(w, http.StatusAccepted, parking.EnqueueResponse{Accepted: controller.Dispatcher.EnqueueJobs(jobs)})
}

// Generated with assistance from Claude AI
