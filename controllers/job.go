package controllers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/julienschmidt/httprouter"
	"github.com/newscred/webhook-broker/storage"
	"github.com/newscred/webhook-broker/storage/data"
	"github.com/rs/xid"
	"github.com/rs/zerolog/hlog"
)

const (
	headerConsumerToken = "X-Broker-Consumer-Token"
	jobsPath            = consumerPath + "/queued-jobs"
	jobIDPathParamKey   = "jobId"
	jobPath             = consumerPath + "/job/:" + jobIDPathParamKey
	jobRequeueSuffix    = "/requeue-dead-job"
	jobRequeuePath      = jobPath + jobRequeueSuffix
	defaultPageSize     = 25
	maxPageSize         = 100
)

var (
	errInvalidQueryParam        = errors.New("invalid query parameter")
	errInvalidTransitionRequest = errors.New("invalid transition request")
	errTimeoutWithInvalidState  = errors.New("timeout provided with invalid state")
	errConsumerDoesNotExist     = errors.New("consumer could not be found")
	errConsumerTokenNotMatching = errors.New("consumer token does not match")
	errConsumerNotPullBased     = errors.New("consumer not pull based")
	errJobDoesNotExist          = errors.New("job could not be found")
)

// JobsController represents all endpoints related to the queued jobs for a consumer of a channel
type JobsController struct {
	ChannelRepo     storage.ChannelRepository
	ConsumerRepo    storage.ConsumerRepository
	DeliveryJobRepo storage.DeliveryJobRepository
}

// NewJobsController creates and returns a new instance of JobsController
func NewJobsController(channelRepo storage.ChannelRepository, consumerRepo storage.ConsumerRepository, deliveryJobRepo storage.DeliveryJobRepository) *JobsController {
	return &JobsController{ChannelRepo: channelRepo, ConsumerRepo: consumerRepo, DeliveryJobRepo: deliveryJobRepo}
}

type QeuedMessageModel struct {
	MessageID   string
	Payload     string
	Headers     data.HeadersMap
	ContentType string
}

func newQueuedMessageModel(message *data.Message) *QeuedMessageModel {
	return &QeuedMessageModel{
		MessageID:   message.MessageID,
		Payload:     message.Payload,
		Headers:     message.Headers,
		ContentType: message.ContentType,
	}
}

type QueuedDeliveryJobModel struct {
	ID                 xid.ID
	Priority           uint
	IncrementalTimeout uint // in second
	Message            *QeuedMessageModel
}

func newQueuedDeliveryJobModel(job *data.DeliveryJob) *QueuedDeliveryJobModel {
	return &QueuedDeliveryJobModel{
		ID:                 job.ID,
		Priority:           job.Priority,
		IncrementalTimeout: job.IncrementalTimeout,
		Message:            newQueuedMessageModel(job.Message),
	}
}

type JobListResult struct {
	Result []*QueuedDeliveryJobModel
}

// Get implements the GET /channel/:channelId/consumer/:consumerId/queued-jobs endpoint
func (controller *JobsController) Get(w http.ResponseWriter, r *http.Request, params httprouter.Params) {
	consumer, valid := getConsumerWithValidation(w, r, params, controller.ChannelRepo, controller.ConsumerRepo)
	if !valid {
		return
	}

	pageSize, valid := getPageSizeFromURL(w, r)
	if !valid {
		return
	}

	jobs, err := controller.DeliveryJobRepo.GetPrioritizedJobsForConsumer(consumer, data.JobQueued, pageSize)
	if err != nil {
		writeErr(w, err)
		return
	}

	jobModels := make([]*QueuedDeliveryJobModel, len(jobs))
	for index, job := range jobs {
		jobModels[index] = newQueuedDeliveryJobModel(job)
	}

	data := JobListResult{Result: jobModels}
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

func getPageSizeFromURL(w http.ResponseWriter, r *http.Request) (pageSize int, valid bool) {
	limit := r.URL.Query().Get("limit")
	if limit == "" {
		return defaultPageSize, true
	}
	pageSize, err := strconv.Atoi(limit)
	if err != nil || pageSize < 0 {
		writeStatus(w, http.StatusBadRequest, errInvalidQueryParam)
		return 0, false
	}
	if pageSize > maxPageSize {
		pageSize = maxPageSize
	}
	return pageSize, true
}

func getConsumerWithValidation(w http.ResponseWriter, r *http.Request, params httprouter.Params, channelRepo storage.ChannelRepository, consumerRepo storage.ConsumerRepository) (consumer *data.Consumer, valid bool) {
	valid = true
	logger := hlog.FromRequest(r)
	channelID := params.ByName(channelIDPathParamKey)
	channelToken := r.Header.Get(headerChannelToken)
	consumerID := params.ByName(consumerIDPathParamKey)
	consumerToken := r.Header.Get(headerConsumerToken)

	channel, err := channelRepo.Get(channelID)
	if err != nil {
		logger.Error().Err(err).Msg("no channel found: " + channelID)
		writeNotFound(w)
		valid = false
	} else if channel.Token != channelToken {
		logger.Error().Msg(fmt.Sprintf("channel token did not match: %s vs %s", channel.Token, channelToken))
		writeStatus(w, http.StatusForbidden, errChannelTokenNotMatching)
		valid = false
	} else if consumer, err = consumerRepo.Get(channelID, consumerID); err != nil {
		logger.Error().Err(err).Msg("no consumer found: " + consumerID)
		writeStatus(w, http.StatusUnauthorized, errConsumerDoesNotExist)
		valid = false
	} else if consumer.Token != consumerToken {
		logger.Error().Msg(fmt.Sprintf("consumer token did not match: %s vs %s", consumer.Token, consumerToken))
		writeStatus(w, http.StatusForbidden, errConsumerTokenNotMatching)
		valid = false
	} else if consumer.Type != data.PullConsumer {
		logger.Error().Msg("consumer is not pull based")
		writeStatus(w, http.StatusPreconditionFailed, errConsumerNotPullBased)
		valid = false
	}
	return consumer, valid
}

type JobRequeueController struct {
	DeliveryJobRepo storage.DeliveryJobRepository
	ChannelRepo     storage.ChannelRepository
	ConsumerRepo    storage.ConsumerRepository
}

// NewJobRequeueController creates and returns a new instance of JobRequeueController
func NewJobRequeueController(deliveryJobRepo storage.DeliveryJobRepository, channelRepo storage.ChannelRepository, consumerRepo storage.ConsumerRepository) *JobRequeueController {
	return &JobRequeueController{DeliveryJobRepo: deliveryJobRepo, ChannelRepo: channelRepo, ConsumerRepo: consumerRepo}
}

// GetPath returns the endpoint's path
func (controller *JobRequeueController) GetPath() string {
	return jobRequeuePath
}

// FormatAsRelativeLink Format as relative URL of this resource based on the params
func (controller *JobRequeueController) FormatAsRelativeLink(params ...httprouter.Param) string {
	return formatURL(params, controller.GetPath(), channelIDPathParamKey, consumerIDPathParamKey, jobIDPathParamKey)
}

// Post implements the POST /channel/:channelId/consumer/:consumerId/job/:jobId/requeue-dead-job
func (controller *JobRequeueController) Post(w http.ResponseWriter, r *http.Request, params httprouter.Params) {
	job, valid := getJobWithValidation(w, r, params, controller.ChannelRepo, controller.ConsumerRepo, controller.DeliveryJobRepo)
	if !valid {
		return
	}
	if job.Status != data.JobDead {
		writeStatus(w, http.StatusBadRequest, errJobDoesNotExist)
		return
	}
	_, err := controller.DeliveryJobRepo.RequeueDeadJob(job)
	if err == nil {
		writeStatus(w, http.StatusAccepted, nil)
	} else {
		writeErr(w, err)
	}
}

// JobController represents all endpoints related to a single job for a consumer
type JobController struct {
	MsgController      EndpointController
	ChannelController  EndpointController
	ProducerController EndpointController
	ConsumerController EndpointController
	ChannelRepo        storage.ChannelRepository
	ConsumerRepo       storage.ConsumerRepository
	DeliveryJobRepo    storage.DeliveryJobRepository
}

// NewJobController creates and returns a new instance of JobController
func NewJobController(msgController *MessageController, channelController *ChannelController, producerController *ProducerController, consumerController *ConsumerController, channelRepo storage.ChannelRepository, consumerRepo storage.ConsumerRepository, deliveryJobRepo storage.DeliveryJobRepository) *JobController {
	return &JobController{MsgController: msgController, ChannelController: channelController, ProducerController: producerController, ConsumerController: consumerController, ChannelRepo: channelRepo, ConsumerRepo: consumerRepo, DeliveryJobRepo: deliveryJobRepo}
}

// Get implements the GET /channel/:channelId/consumer/:consumerId/job/:jobId endpoint
func (controller *JobController) Get(w http.ResponseWriter, r *http.Request, params httprouter.Params) {
	job, valid := getJobWithValidation(w, r, params, controller.ChannelRepo, controller.ConsumerRepo, controller.DeliveryJobRepo)
	if !valid {
		return
	}
	channelParam := httprouter.Param{Key: channelIDPathParamKey, Value: job.Message.BroadcastedTo.ChannelID}
	consumerParam := httprouter.Param{Key: consumerIDPathParamKey, Value: job.Listener.ConsumerID}
	linkedDJ := &HyperlinkedDeliveryJobModel{
		DeliveryJobModel: *newDeliveryJobModel(job),
		MessageURL:       controller.MsgController.FormatAsRelativeLink(channelParam, httprouter.Param{Key: messageIDParamKey, Value: job.Message.MessageID}),
		ConsumerURL:      controller.ConsumerController.FormatAsRelativeLink(channelParam, consumerParam),
		ProducerURL:      controller.ProducerController.FormatAsRelativeLink(httprouter.Param{Key: producerIDPathParamKey, Value: job.Message.ProducedBy.ProducerID}),
		ChannelURL:       controller.ChannelController.FormatAsRelativeLink(channelParam),
	}
	if job.Status == data.JobDead {
		linkedDJ.JobRequeueURL = controller.FormatAsRelativeLink(channelParam, consumerParam, httprouter.Param{Key: jobIDPathParamKey, Value: job.ID.String()}) + jobRequeueSuffix
	}
	writeJSON(w, linkedDJ)
}

// Post implements the POST /channel/:channelId/consumer/:consumerId/job/:jobId endpoint
func (controller *JobController) Post(w http.ResponseWriter, r *http.Request, params httprouter.Params) {
	job, valid := getJobWithValidation(w, r, params, controller.ChannelRepo, controller.ConsumerRepo, controller.DeliveryJobRepo)
	if !valid {
		return
	}

	updateData := struct {
		NextState          string
		IncrementalTimeout uint
	}{}
	err := json.NewDecoder(r.Body).Decode(&updateData)
	if err != nil {
		writeErr(w, err)
		return
	}
	// TODO: Guard Against Negative Number
	if updateData.IncrementalTimeout != 0 {
		if updateData.NextState != data.JobInflight.String() || job.Status == data.JobInflight {
			writeStatus(w, http.StatusBadRequest, errTimeoutWithInvalidState)
			return
		}
		job.IncrementalTimeout = updateData.IncrementalTimeout
	}

	if job.Status.String() == updateData.NextState {
		writeStatus(w, http.StatusAccepted, nil)
		return
	}

	switch updateData.NextState {
	case data.JobInflight.String():
		switch job.Status {
		case data.JobQueued:
			err = controller.DeliveryJobRepo.MarkJobInflight(job)
		case data.JobDead:
			err = controller.DeliveryJobRepo.MarkDeadJobAsInflight(job)
		default:
			writeStatus(w, http.StatusBadRequest, errInvalidTransitionRequest)
			return
		}
	case data.JobDelivered.String():
		err = controller.DeliveryJobRepo.MarkJobDelivered(job)
	case data.JobDead.String():
		err = controller.DeliveryJobRepo.MarkJobDead(job)
	default:
		writeStatus(w, http.StatusBadRequest, errInvalidTransitionRequest)
		return
	}

	if err != nil {
		writeStatus(w, http.StatusBadRequest, errInvalidTransitionRequest)
		return
	}

	writeStatus(w, http.StatusAccepted, nil)
}

// GetPath returns the endpoint's path
func (controller *JobController) GetPath() string {
	return jobPath
}

// FormatAsRelativeLink formats this controllers URL with the parameters provided. All of `consumerId`, `channelId` and `jobId` params must be sent else it will return the templated URL
func (controller *JobController) FormatAsRelativeLink(params ...httprouter.Param) (result string) {
	return formatURL(params, jobPath, channelIDPathParamKey, consumerIDPathParamKey, jobIDPathParamKey)
}

func getJobWithValidation(w http.ResponseWriter, r *http.Request, params httprouter.Params, channelRepo storage.ChannelRepository, consumerRepo storage.ConsumerRepository, deliveryJobRepo storage.DeliveryJobRepository) (job *data.DeliveryJob, valid bool) {
	logger := hlog.FromRequest(r)
	consumer, valid := getConsumerWithValidation(w, r, params, channelRepo, consumerRepo)
	if !valid {
		return nil, valid
	}

	jobID := params.ByName(jobIDPathParamKey)
	job, err := deliveryJobRepo.GetByID(jobID)
	if err != nil {
		logger.Error().Err(err).Msg("no job found: " + jobID)
		writeStatus(w, http.StatusNotFound, errJobDoesNotExist)
		valid = false
	} else if job.Listener.ConsumerID != consumer.ConsumerID {
		logger.Error().Msg(fmt.Sprintf("consumer id did not match: %s vs %s", job.Listener.ConsumerID, consumer.ConsumerID))
		writeStatus(w, http.StatusUnauthorized, errJobDoesNotExist)
		valid = false
	}
	return job, valid
}
