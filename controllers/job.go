package controllers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/julienschmidt/httprouter"
	"github.com/newscred/webhook-broker/storage"
	"github.com/newscred/webhook-broker/storage/data"
	"github.com/rs/zerolog/hlog"
)

const (
	headerConsumerToken = "X-Broker-Consumer-Token"
	jobIDPathParamKey   = "jobId"
	jobPath             = consumerPath + "/job/:" + jobIDPathParamKey
)

var (
	errConsumerDoesNotExist     = errors.New("consumer could not be found")
	errConsumerTokenNotMatching = errors.New("consumer token does not match")
	errJobDoesNotExist          = errors.New("job could not be found")
)

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

// Post implements the POST /channel/:channelId/consumer/:consumerId/job/:jobId endpoint
func (controller *JobController) Post(w http.ResponseWriter, r *http.Request, params httprouter.Params) {
	job, valid := controller.getJobWithChannelAndConsumerValidation(w, r, params)
	if !valid {
		return
	}

	updateData := struct{ NextState string }{}
	err := json.NewDecoder(r.Body).Decode(&updateData)
	if err != nil {
		writeErr(w, err)
		return
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
		writeBadRequest(w)
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
