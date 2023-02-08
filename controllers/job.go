package controllers

import (
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
	defaultPageSize     = 25
	maxPageSize         = 100
)

var (
	errInvalidQueryParam        = errors.New("invalid query parameter")
	errConsumerDoesNotExist     = errors.New("consumer could not be found")
	errConsumerTokenNotMatching = errors.New("consumer token does not match")
	errConsumerNotPullBased     = errors.New("consumer not pull based")
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
	ContentType string
}

func newQueuedMessageModel(message *data.Message) *QeuedMessageModel {
	return &QeuedMessageModel{
		MessageID:   message.MessageID,
		Payload:     message.Payload,
		ContentType: message.ContentType,
	}
}

type QueuedDeliveryJobModel struct {
	ID       xid.ID
	Priority uint
	Message  *QeuedMessageModel
}

func newQueuedDeliveryJobModel(job *data.DeliveryJob) *QueuedDeliveryJobModel {
	return &QueuedDeliveryJobModel{
		ID:       job.ID,
		Priority: job.Priority,
		Message:  newQueuedMessageModel(job.Message),
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
