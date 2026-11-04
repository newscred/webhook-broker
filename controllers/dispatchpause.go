package controllers

import (
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/julienschmidt/httprouter"
	"github.com/rs/zerolog/hlog"

	"github.com/newscred/webhook-broker/config"
	"github.com/newscred/webhook-broker/dispatcher"
	"github.com/newscred/webhook-broker/storage"
	"github.com/newscred/webhook-broker/storage/data"
)

const (
	consumerDispatchPausePath = consumerPath + "/dispatch-pause"
	globalDispatchPausePath   = "/dispatch-pause"
	dispatchPausesPath        = "/dispatch-pauses"
	pauseReasonFormParamName  = "reason"
	pauseScopeConsumer        = "consumer"
	pauseScopeGlobal          = "global"
	// headerDispatchPaused is set on pull responses while the consumer is effectively paused
	headerDispatchPaused = "X-Broker-Dispatch-Paused"
)

var (
	errDispatchPaused  = errors.New("dispatch paused")
	errPauseReasonLong = errors.New("pause reason too long")
	errNotPaused       = errors.New("not paused")
)

// DispatchPauseModel represents a dispatch pause to HTTP clients
type DispatchPauseModel struct {
	Scope          string    `json:"scope"`
	ChannelID      string    `json:"channelId,omitempty"`
	ConsumerID     string    `json:"consumerId,omitempty"`
	Reason         string    `json:"reason"`
	PausedAt       time.Time `json:"pausedAt"`
	GloballyPaused bool      `json:"globallyPaused"`
	ParkedJobCount int64     `json:"parkedJobCount"`
	// InflightJobCount is the number of deliveries still in flight for the consumer; only set on the consumer endpoint.
	// Wait for 0 before treating the consumer as quiet.
	InflightJobCount *int64 `json:"inflightJobCount,omitempty"`
}

// ParkedJobsModel represents the parked jobs of one consumer
type ParkedJobsModel struct {
	ChannelID      string `json:"channelId"`
	ConsumerID     string `json:"consumerId"`
	Count          int64  `json:"count"`
	OldestParkedAt string `json:"oldestParkedAt"`
	Paused         bool   `json:"paused"`
}

// DispatchPausesModel lists every pause and every consumer with parked jobs (including resumed consumers still draining)
type DispatchPausesModel struct {
	GloballyPaused bool                  `json:"globallyPaused"`
	Pauses         []*DispatchPauseModel `json:"pauses"`
	Parked         []*ParkedJobsModel    `json:"parked"`
}

func isGloballyPausedInDB(pauseRepo storage.DispatchPauseRepository) bool {
	_, err := pauseRepo.Get(data.GlobalDispatchPauseScope)
	return err == nil
}

func newDispatchPauseModel(pause *data.DispatchPause, consumer *data.Consumer, globallyPaused bool, parkedJobCount int64) *DispatchPauseModel {
	model := &DispatchPauseModel{Scope: pauseScopeConsumer, Reason: pause.Reason, PausedAt: pause.PausedAt, GloballyPaused: globallyPaused, ParkedJobCount: parkedJobCount}
	if pause.IsGlobal() {
		model.Scope = pauseScopeGlobal
		model.GloballyPaused = true
	} else if consumer != nil {
		model.ChannelID = consumer.GetChannelIDSafely()
		model.ConsumerID = consumer.ConsumerID
	} else {
		model.ConsumerID = pause.ConsumerID
	}
	return model
}

// readPauseReason reads the optional reason form field; returns false (after writing 400) if it is too long
func readPauseReason(w http.ResponseWriter, r *http.Request) (string, bool) {
	reason := r.PostFormValue(pauseReasonFormParamName)
	if len(reason) > data.MaxDispatchPauseReasonLength {
		writeStatus(w, http.StatusBadRequest, errPauseReasonLong)
		return "", false
	}
	return reason, true
}

// checkAdminToken writes 404 when no admin token is configured and 401 when the request's token does not match
func checkAdminToken(w http.ResponseWriter, r *http.Request, pauseConfig config.DispatchPauseConfig) bool {
	if !pauseConfig.IsAdminTokenConfigured() {
		writeNotFound(w)
		return false
	}
	if !isAdminAuthorized(r, pauseConfig) {
		writeStatus(w, http.StatusUnauthorized, errAdminTokenMismatch)
		return false
	}
	return true
}

// ConsumerDispatchPauseController handles PUT/GET/DELETE /channel/:channelId/consumer/:consumerId/dispatch-pause
type ConsumerDispatchPauseController struct {
	ChannelRepo     storage.ChannelRepository
	ConsumerRepo    storage.ConsumerRepository
	PauseRepo       storage.DispatchPauseRepository
	JobParkingRepo  storage.JobParkingRepository
	DeliveryJobRepo storage.DeliveryJobRepository
	PauseRegistry   dispatcher.PauseRegistry
}

// NewConsumerDispatchPauseController creates the consumer dispatch pause controller
func NewConsumerDispatchPauseController(channelRepo storage.ChannelRepository, consumerRepo storage.ConsumerRepository, pauseRepo storage.DispatchPauseRepository,
	jobParkingRepo storage.JobParkingRepository, deliveryJobRepo storage.DeliveryJobRepository, pauseRegistry dispatcher.PauseRegistry) *ConsumerDispatchPauseController {
	return &ConsumerDispatchPauseController{ChannelRepo: channelRepo, ConsumerRepo: consumerRepo, PauseRepo: pauseRepo, JobParkingRepo: jobParkingRepo,
		DeliveryJobRepo: deliveryJobRepo, PauseRegistry: pauseRegistry}
}

// GetPath returns the endpoint's path
func (controller *ConsumerDispatchPauseController) GetPath() string {
	return consumerDispatchPausePath
}

// FormatAsRelativeLink formats the endpoint's path with the channel and consumer ids
func (controller *ConsumerDispatchPauseController) FormatAsRelativeLink(params ...httprouter.Param) string {
	return formatURL(params, consumerDispatchPausePath, channelIDPathParamKey, consumerIDPathParamKey)
}

func (controller *ConsumerDispatchPauseController) writeModel(w http.ResponseWriter, pause *data.DispatchPause, consumer *data.Consumer) {
	parked, err := controller.JobParkingRepo.CountForConsumer(consumer.ID.String())
	if err != nil {
		writeErr(w, err)
		return
	}
	inflight, err := controller.DeliveryJobRepo.CountJobsForConsumer(consumer.ID.String(), data.JobInflight)
	if err != nil {
		writeErr(w, err)
		return
	}
	model := newDispatchPauseModel(pause, consumer, isGloballyPausedInDB(controller.PauseRepo), parked)
	model.InflightJobCount = &inflight
	writeJSON(w, model)
}

// Put pauses dispatching for the consumer; idempotent
func (controller *ConsumerDispatchPauseController) Put(w http.ResponseWriter, r *http.Request, params httprouter.Params) {
	consumer, valid := getChannelConsumerWithAuth(w, r, params, controller.ChannelRepo, controller.ConsumerRepo)
	if !valid {
		return
	}
	reason, valid := readPauseReason(w, r)
	if !valid {
		return
	}
	pause, err := data.NewConsumerDispatchPause(consumer, reason)
	if err == nil {
		pause, err = controller.PauseRepo.Pause(pause)
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	controller.PauseRegistry.Apply(pause)
	hlog.FromRequest(r).Info().Str("scope", pauseScopeConsumer).Str("channelId", consumer.GetChannelIDSafely()).Str("consumerId", consumer.ConsumerID).
		Str("reason", pause.Reason).Msg("dispatch paused")
	controller.writeModel(w, pause, consumer)
}

// Get returns the consumer's pause or 404 if it is not paused
func (controller *ConsumerDispatchPauseController) Get(w http.ResponseWriter, r *http.Request, params httprouter.Params) {
	consumer, valid := getChannelConsumerWithAuth(w, r, params, controller.ChannelRepo, controller.ConsumerRepo)
	if !valid {
		return
	}
	pause, err := controller.PauseRepo.Get(consumer.ID.String())
	switch {
	case err == sql.ErrNoRows:
		writeStatus(w, http.StatusNotFound, errNotPaused)
	case err != nil:
		writeErr(w, err)
	default:
		controller.writeModel(w, pause, consumer)
	}
}

// Delete resumes dispatching for the consumer; the drainer moves its parked jobs back on its next run
func (controller *ConsumerDispatchPauseController) Delete(w http.ResponseWriter, r *http.Request, params httprouter.Params) {
	consumer, valid := getChannelConsumerWithAuth(w, r, params, controller.ChannelRepo, controller.ConsumerRepo)
	if !valid {
		return
	}
	resumed, err := controller.PauseRepo.Resume(consumer.ID.String())
	if err != nil {
		writeErr(w, err)
		return
	}
	if !resumed {
		writeStatus(w, http.StatusNotFound, errNotPaused)
		return
	}
	controller.PauseRegistry.Remove(consumer.ID.String())
	hlog.FromRequest(r).Info().Str("scope", pauseScopeConsumer).Str("channelId", consumer.GetChannelIDSafely()).Str("consumerId", consumer.ConsumerID).Msg("dispatch resumed")
	writeStatus(w, http.StatusNoContent, nil)
}

// GlobalDispatchPauseController handles PUT/DELETE /dispatch-pause; requires the admin token
type GlobalDispatchPauseController struct {
	PauseRepo      storage.DispatchPauseRepository
	JobParkingRepo storage.JobParkingRepository
	PauseRegistry  dispatcher.PauseRegistry
	PauseConfig    config.DispatchPauseConfig
}

// NewGlobalDispatchPauseController creates the global dispatch pause controller
func NewGlobalDispatchPauseController(pauseRepo storage.DispatchPauseRepository, jobParkingRepo storage.JobParkingRepository, pauseRegistry dispatcher.PauseRegistry,
	pauseConfig config.DispatchPauseConfig) *GlobalDispatchPauseController {
	return &GlobalDispatchPauseController{PauseRepo: pauseRepo, JobParkingRepo: jobParkingRepo, PauseRegistry: pauseRegistry, PauseConfig: pauseConfig}
}

// GetPath returns the endpoint's path
func (controller *GlobalDispatchPauseController) GetPath() string {
	return globalDispatchPausePath
}

// FormatAsRelativeLink returns the endpoint's path
func (controller *GlobalDispatchPauseController) FormatAsRelativeLink(params ...httprouter.Param) string {
	return globalDispatchPausePath
}

// Put pauses dispatching for every consumer; idempotent
func (controller *GlobalDispatchPauseController) Put(w http.ResponseWriter, r *http.Request, params httprouter.Params) {
	if !checkAdminToken(w, r, controller.PauseConfig) {
		return
	}
	reason, valid := readPauseReason(w, r)
	if !valid {
		return
	}
	pause, err := controller.PauseRepo.Pause(data.NewGlobalDispatchPause(reason))
	if err != nil {
		writeErr(w, err)
		return
	}
	controller.PauseRegistry.Apply(pause)
	hlog.FromRequest(r).Info().Str("scope", pauseScopeGlobal).Str("reason", pause.Reason).Msg("dispatch paused")
	counts, err := controller.JobParkingRepo.GetCountsByConsumer()
	if err != nil {
		writeErr(w, err)
		return
	}
	var total int64
	for _, count := range counts {
		total += count.Count
	}
	writeJSON(w, newDispatchPauseModel(pause, nil, true, total))
}

// Delete resumes dispatching globally; consumers with their own pause stay paused
func (controller *GlobalDispatchPauseController) Delete(w http.ResponseWriter, r *http.Request, params httprouter.Params) {
	if !checkAdminToken(w, r, controller.PauseConfig) {
		return
	}
	resumed, err := controller.PauseRepo.Resume(data.GlobalDispatchPauseScope)
	if err != nil {
		writeErr(w, err)
		return
	}
	if !resumed {
		writeStatus(w, http.StatusNotFound, errNotPaused)
		return
	}
	controller.PauseRegistry.Remove(data.GlobalDispatchPauseScope)
	hlog.FromRequest(r).Info().Str("scope", pauseScopeGlobal).Msg("dispatch resumed")
	writeStatus(w, http.StatusNoContent, nil)
}

// DispatchPausesController handles GET /dispatch-pauses; open unless an admin token is configured
type DispatchPausesController struct {
	PauseRepo      storage.DispatchPauseRepository
	JobParkingRepo storage.JobParkingRepository
	ConsumerRepo   storage.ConsumerRepository
	PauseConfig    config.DispatchPauseConfig
}

// NewDispatchPausesController creates the dispatch pauses listing controller
func NewDispatchPausesController(pauseRepo storage.DispatchPauseRepository, jobParkingRepo storage.JobParkingRepository, consumerRepo storage.ConsumerRepository,
	pauseConfig config.DispatchPauseConfig) *DispatchPausesController {
	return &DispatchPausesController{PauseRepo: pauseRepo, JobParkingRepo: jobParkingRepo, ConsumerRepo: consumerRepo, PauseConfig: pauseConfig}
}

// GetPath returns the endpoint's path
func (controller *DispatchPausesController) GetPath() string {
	return dispatchPausesPath
}

// FormatAsRelativeLink returns the endpoint's path
func (controller *DispatchPausesController) FormatAsRelativeLink(params ...httprouter.Param) string {
	return dispatchPausesPath
}

// Get lists every pause (from the database) and the parked job counts per consumer
func (controller *DispatchPausesController) Get(w http.ResponseWriter, r *http.Request, params httprouter.Params) {
	if !checkAdminToken(w, r, controller.PauseConfig) {
		return
	}
	pauses, err := controller.PauseRepo.GetAll()
	if err != nil {
		writeErr(w, err)
		return
	}
	counts, err := controller.JobParkingRepo.GetCountsByConsumer()
	if err != nil {
		writeErr(w, err)
		return
	}
	result := &DispatchPausesModel{Pauses: make([]*DispatchPauseModel, 0, len(pauses)), Parked: make([]*ParkedJobsModel, 0, len(counts))}
	pausedConsumers := make(map[string]bool, len(pauses))
	for _, pause := range pauses {
		if pause.IsGlobal() {
			result.GloballyPaused = true
		} else {
			pausedConsumers[pause.ConsumerID] = true
		}
	}
	parkedByConsumer := make(map[string]int64, len(counts))
	for _, count := range counts {
		parkedByConsumer[count.ConsumerID] = count.Count
		model := &ParkedJobsModel{ChannelID: count.ChannelID, ConsumerID: count.ConsumerID, Count: count.Count, OldestParkedAt: count.OldestParkedAt,
			Paused: result.GloballyPaused || pausedConsumers[count.ConsumerID]}
		if consumer, lookupErr := controller.ConsumerRepo.GetByID(count.ConsumerID); lookupErr == nil {
			model.ConsumerID = consumer.ConsumerID
		}
		result.Parked = append(result.Parked, model)
	}
	for _, pause := range pauses {
		var consumer *data.Consumer
		if !pause.IsGlobal() {
			consumer, _ = controller.ConsumerRepo.GetByID(pause.ConsumerID)
		}
		result.Pauses = append(result.Pauses, newDispatchPauseModel(pause, consumer, result.GloballyPaused, parkedByConsumer[pause.ConsumerID]))
	}
	writeJSON(w, result)
}

// isConsumerEffectivelyPaused checks the database; nil repository means the feature is not wired (always false)
func isConsumerEffectivelyPaused(pauseRepo storage.DispatchPauseRepository, consumer *data.Consumer) bool {
	if pauseRepo == nil || consumer == nil {
		return false
	}
	paused, err := pauseRepo.IsEffectivelyPaused(consumer.ID.String())
	return err == nil && paused
}

// Generated with assistance from Claude AI
