package controllers

import (
	"database/sql"
	"net/http"

	"github.com/julienschmidt/httprouter"
	"github.com/newscred/webhook-broker/storage"
	"github.com/newscred/webhook-broker/storage/data"
	"github.com/rs/xid"
)

const (
	jobsPath = consumerPath + "/queued-jobs"
)

// JobsController represents all endpoints related to the queued jobs for a consumer of a channel
type JobsController struct {
	ConsumerRepo    storage.ConsumerRepository
	DeliveryJobRepo storage.DeliveryJobRepository
}

// NewJobsController creates and returns a new instance of JobsController
func NewJobsController(consumerRepo storage.ConsumerRepository, deliveryJobRepo storage.DeliveryJobRepository) *JobsController {
	return &JobsController{ConsumerRepo: consumerRepo, DeliveryJobRepo: deliveryJobRepo}
}

type QeuedMessageModel struct {
	MessageID   string
	Payload     string
	ContentType string
	Priority    uint
}

func newQueuedMessageModel(message *data.Message) *QeuedMessageModel {
	return &QeuedMessageModel{
		MessageID:   message.MessageID,
		Payload:     message.Payload,
		ContentType: message.ContentType,
		Priority:    message.Priority,
	}
}

type QueuedDeliveryJobModel struct {
	ID      xid.ID
	Message *QeuedMessageModel
}

func newQueuedDeliveryJobModel(job *data.DeliveryJob) *QueuedDeliveryJobModel {
	return &QueuedDeliveryJobModel{
		ID:      job.ID,
		Message: newQueuedMessageModel(job.Message),
	}
}

type JobListResult struct {
	Result []*QueuedDeliveryJobModel
	Pages  map[string]string
	Links  map[string]string
}

// Get implements the GET /channel/:channelId/consumer/:consumerId/queued-jobs endpoint
func (controller *JobsController) Get(w http.ResponseWriter, r *http.Request, params httprouter.Params) {
	channelID := findParam(params, channelIDPathParamKey)
	consumerID := findParam(params, consumerIDPathParamKey)
	consumer, err := controller.ConsumerRepo.Get(channelID, consumerID)

	if err != nil {
		switch err {
		case sql.ErrNoRows:
			writeNotFound(w)
		default:
			writeErr(w, err)
		}
		return
	}

	jobs, resultPagination, err := controller.DeliveryJobRepo.GetPrioritizedJobsForConsumer(consumer, data.JobQueued, getPagination(r))

	if err != nil {
		switch err {
		case sql.ErrNoRows:
			writeNotFound(w)
		default:
			writeErr(w, err)
		}
		return
	}

	jobModels := make([]*QueuedDeliveryJobModel, len(jobs))
	for index, job := range jobs {
		jobModels[index] = newQueuedDeliveryJobModel(job)
	}

	data := JobListResult{Result: jobModels, Pages: getPaginationLinks(r, resultPagination), Links: make(map[string]string)}
	writeJSON(w, data)
}

// GetPath returns the endpoint's path
func (controller *JobsController) GetPath() string {
	return jobsPath
}

// FormatAsRelativeLink formats this controllers URL with the parameters provided. Both `consumerId` and `channelId` params must be sent else it will return the templated URL
func (controller *JobsController) FormatAsRelativeLink(params ...httprouter.Param) string {
	return formatURL(params, jobsPath, channelIDPathParamKey, consumerIDPathParamKey)
}
