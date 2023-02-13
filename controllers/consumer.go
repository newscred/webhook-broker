package controllers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/julienschmidt/httprouter"
	"github.com/newscred/webhook-broker/storage"
	"github.com/newscred/webhook-broker/storage/data"
	"github.com/rs/zerolog/hlog"
)

const (
	headerConsumerToken    = "X-Broker-Consumer-Token"
	consumersPath          = channelPath + "/consumers"
	consumerIDPathParamKey = "consumerId"
	consumerPath           = channelPath + "/consumer/:" + consumerIDPathParamKey
	jobIDPathParamKey      = "jobId"
	jobPath                = consumerPath + "/job/:" + jobIDPathParamKey
)

var (
	errConsumerDoesNotExist     = errors.New("consumer could not be found")
	errConsumerTokenNotMatching = errors.New("consumer token does not match")
	errJobDoesNotExist          = errors.New("job could not be found")
)

// ConsumerModel represents the data communicated to HTTP clients
type ConsumerModel struct {
	MsgStakeholder
	CallbackURL        string
	DeadLetterQueueURL string
}

// ConsumerController represents all endpoints related to a single consumer for a channel
type ConsumerController struct {
	ConsumerRepo storage.ConsumerRepository
	ChannelRepo  storage.ChannelRepository
	DLQEndpoint  EndpointController
}

// NewConsumerController creates and returns a new instance of ConsumerController
func NewConsumerController(channelRepo storage.ChannelRepository, consumerRepo storage.ConsumerRepository, DLQController *DLQController) *ConsumerController {
	return &ConsumerController{ConsumerRepo: consumerRepo, ChannelRepo: channelRepo, DLQEndpoint: DLQController}
}

// Get implements the GET /channel/:channelId/consumer/:consumerId endpoint
func (controller *ConsumerController) Get(w http.ResponseWriter, r *http.Request, params httprouter.Params) {
	consumer, err := controller.ConsumerRepo.Get(findParam(params, channelIDPathParamKey), findParam(params, consumerIDPathParamKey))
	consumerModel := controller.getConsumerModel(consumer)
	writeGetResult(err, writeNotFound, w, consumerModel)
}

func (controller *ConsumerController) getConsumerModel(consumer *data.Consumer) *ConsumerModel {
	channelIDParam := httprouter.Param{Key: channelIDPathParamKey, Value: consumer.ConsumingFrom.ChannelID}
	consumerIDParam := httprouter.Param{Key: consumerIDPathParamKey, Value: consumer.ConsumerID}
	consumerModel := &ConsumerModel{
		MsgStakeholder:     *getMessageStakeholder(consumer.ConsumerID, &consumer.MessageStakeholder),
		CallbackURL:        consumer.CallbackURL,
		DeadLetterQueueURL: controller.DLQEndpoint.FormatAsRelativeLink(channelIDParam, consumerIDParam)}
	return consumerModel
}

// Put implements the PUT /channel/:channelId/consumer/:consumerId endpoint
func (controller *ConsumerController) Put(w http.ResponseWriter, r *http.Request, params httprouter.Params) {
	validRequest := checkFormContentType(r, w)
	var channel *data.Channel
	var err error
	channelID := findParam(params, channelIDPathParamKey)
	consumerID := findParam(params, consumerIDPathParamKey)
	if validRequest {
		channel, err = controller.ChannelRepo.Get(channelID)
		if err != nil {
			writeNotFound(w)
			validRequest = false
		}
	}
	if validRequest {
		consumer, err := controller.ConsumerRepo.Get(channelID, consumerID)
		if err == nil {
			validRequest = isConditionalUpdateCalled(w, r, consumer)
		}
	}
	if !validRequest {
		return
	}
	token, name := getUpdateData(r, consumerID)
	urlString := r.PostFormValue("callbackUrl")
	callbackURL, uErr := url.Parse(urlString)
	if len(urlString) < 1 || uErr != nil || !callbackURL.IsAbs() {
		writeBadRequest(w)
		return
	}
	inComingConsumer, _ := data.NewConsumer(channel, consumerID, token, callbackURL)
	inComingConsumer.Name = name
	consumer, updateErr := controller.ConsumerRepo.Store(inComingConsumer)
	writeGetResult(updateErr, func(w http.ResponseWriter) { writeErr(w, updateErr) }, w, controller.getConsumerModel(consumer))
}

// Delete implements the DELETE /channel/:channelId/consumer/:consumerId endpoint
func (controller *ConsumerController) Delete(w http.ResponseWriter, r *http.Request, params httprouter.Params) {
	consumer, err := controller.ConsumerRepo.Get(findParam(params, channelIDPathParamKey), findParam(params, consumerIDPathParamKey))
	switch err {
	case nil:
		if validRequest := isConditionalUpdateCalled(w, r, consumer); !validRequest {
			writePreconditionFailed(w)
		} else if delErr := controller.ConsumerRepo.Delete(consumer); delErr != nil {
			writeErr(w, delErr)
		} else {
			writeStatus(w, http.StatusNoContent, nil)
		}
	case sql.ErrNoRows:
		writeNotFound(w)
	default:
		writeErr(w, err)
	}
}

// GetPath returns the endpoint's path
func (controller *ConsumerController) GetPath() string {
	return consumerPath
}

// FormatAsRelativeLink formats this controllers URL with the parameters provided. Both `consumerId` and `channelId` params must be sent else it will return the templated URL
func (controller *ConsumerController) FormatAsRelativeLink(params ...httprouter.Param) (result string) {
	return formatURL(params, consumerPath, channelIDPathParamKey, consumerIDPathParamKey)
}

// ConsumersController represents all endpoints related to a consumers list for a channel
type ConsumersController struct {
	ConsumerRepo     storage.ConsumerRepository
	ConsumerEndpoint EndpointController
}

// NewConsumersController creates and returns a new instance of ConsumersController
func NewConsumersController(consumerEndpoint *ConsumerController, consumerRepo storage.ConsumerRepository) *ConsumersController {
	return &ConsumersController{ConsumerRepo: consumerRepo, ConsumerEndpoint: consumerEndpoint}
}

// Get implements the GET /channel/:channelId/consumers endpoint
func (controller *ConsumersController) Get(w http.ResponseWriter, r *http.Request, params httprouter.Params) {
	channelID := findParam(params, channelIDPathParamKey)
	consumers, resultPagination, err := controller.ConsumerRepo.GetList(channelID, getPagination(r))
	if err != nil {
		switch err {
		case sql.ErrNoRows:
			writeNotFound(w)
		default:
			writeErr(w, err)
		}
		return
	}
	consumerURLs := make([]string, len(consumers))
	channelIDParam := httprouter.Param{Key: channelIDPathParamKey, Value: channelID}
	for index, consumer := range consumers {
		consumerURLs[index] = controller.ConsumerEndpoint.FormatAsRelativeLink(channelIDParam, httprouter.Param{Key: consumerIDPathParamKey, Value: consumer.ConsumerID})
	}
	data := ListResult{Result: consumerURLs, Pages: getPaginationLinks(r, resultPagination)}
	writeJSON(w, data)
}

// GetPath returns the endpoint's path
func (controller *ConsumersController) GetPath() string {
	return consumersPath
}

// FormatAsRelativeLink formats this controllers URL with the parameters provided. Both `consumerId` and `channelId` params must be sent else it will return the templated URL
func (controller *ConsumersController) FormatAsRelativeLink(params ...httprouter.Param) string {
	return formatURL(params, consumersPath, channelIDPathParamKey)
}

// JobController represents all endpoints related to a single job for a consumer
type JobController struct {
	ChannelRepo     storage.ChannelRepository
	ConsumerRepo    storage.ConsumerRepository
	DeliveryJobRepo storage.DeliveryJobRepository
}

// NewJobController creates and returns a new instance of JobController
func NewJobController(channelRepo storage.ChannelRepository, consumerRepo storage.ConsumerRepository, deliveryJobRepo storage.DeliveryJobRepository) *JobController {
	return &JobController{ChannelRepo: channelRepo, ConsumerRepo: consumerRepo, DeliveryJobRepo: deliveryJobRepo}
}

type JobUpdateData struct {
	NextState string
}

// Post implements the POST /channel/:channelId/consumer/:consumerId/job/:jobId endpoint
func (controller *JobController) Post(w http.ResponseWriter, r *http.Request, params httprouter.Params) {
	job, valid := controller.getJobWithChannelAndConsumerValidation(w, r, params)
	if !valid {
		return
	}

	var updateData JobUpdateData
	err := json.NewDecoder(r.Body).Decode(&updateData)
	if err != nil {
		writeErr(w, err)
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
			writeBadRequest(w)
			return
		}
	case data.JobDelivered.String():
		err = controller.DeliveryJobRepo.MarkJobDelivered(job)
	case data.JobDead.String():
		err = controller.DeliveryJobRepo.MarkJobDead(job)
	default:
		writeBadRequest(w)
		return
	}

	if err != nil {
		writeErr(w, err)
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

func (controller *JobController) getJobWithChannelAndConsumerValidation(w http.ResponseWriter, r *http.Request, params httprouter.Params) (job *data.DeliveryJob, valid bool) {
	valid = true
	logger := hlog.FromRequest(r)
	channelID := params.ByName(channelIDPathParamKey)
	channelToken := r.Header.Get(headerChannelToken)
	consumerID := params.ByName(consumerIDPathParamKey)
	consumerToken := r.Header.Get(headerConsumerToken)
	jobID := params.ByName(jobIDPathParamKey)

	channel, err := controller.ChannelRepo.Get(channelID)
	if err != nil {
		logger.Error().Err(err).Msg("no channel found: " + channelID)
		writeNotFound(w)
		valid = false
	} else if channel.Token != channelToken {
		logger.Error().Msg(fmt.Sprintf("channel token did not match: %s vs %s", channel.Token, channelToken))
		writeStatus(w, http.StatusForbidden, errChannelTokenNotMatching)
		valid = false
	} else if consumer, err := controller.ConsumerRepo.Get(channelID, consumerID); err != nil {
		logger.Error().Err(err).Msg("no consumer found: " + consumerID)
		writeStatus(w, http.StatusUnauthorized, errConsumerDoesNotExist)
		valid = false
	} else if consumer.Token != consumerToken {
		logger.Error().Msg(fmt.Sprintf("consumer token did not match: %s vs %s", consumer.Token, consumerToken))
		writeStatus(w, http.StatusForbidden, errConsumerTokenNotMatching)
		valid = false
	} else if job, err = controller.DeliveryJobRepo.GetByID(jobID); err != nil {
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
