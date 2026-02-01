package controllers

import (
	"errors"
	"net/http"

	"github.com/julienschmidt/httprouter"
	"github.com/newscred/webhook-broker/config"
	"github.com/newscred/webhook-broker/dispatcher"
	"github.com/newscred/webhook-broker/storage"
	"github.com/newscred/webhook-broker/storage/data"
	"github.com/rs/zerolog/hlog"
)

const (
	dlqPurgePath    = consumerPath + "/dlq"
	deadJobPath     = consumerPath + "/job/:" + jobIDPathParamKey
)

var (
	errJobNotDeadOrRetryNotExhausted = errors.New("job is not dead or retry not exhausted")
)

// DLQPurgeController handles DELETE /channel/:channelId/consumer/:consumerId/dlq
type DLQPurgeController struct {
	ChannelRepo     storage.ChannelRepository
	ConsumerRepo    storage.ConsumerRepository
	DeliveryJobRepo storage.DeliveryJobRepository
	DLQSummaryRepo  storage.DLQSummaryRepository
	BrokerConfig    config.BrokerConfig
	Metrics         *dispatcher.MetricsContainer
}

// NewDLQPurgeController creates a new DLQ purge controller
func NewDLQPurgeController(
	channelRepo storage.ChannelRepository,
	consumerRepo storage.ConsumerRepository,
	deliveryJobRepo storage.DeliveryJobRepository,
	dlqSummaryRepo storage.DLQSummaryRepository,
	brokerConfig config.BrokerConfig,
	metrics *dispatcher.MetricsContainer,
) *DLQPurgeController {
	return &DLQPurgeController{
		ChannelRepo:     channelRepo,
		ConsumerRepo:    consumerRepo,
		DeliveryJobRepo: deliveryJobRepo,
		DLQSummaryRepo:  dlqSummaryRepo,
		BrokerConfig:    brokerConfig,
		Metrics:         metrics,
	}
}

// GetPath returns the endpoint's path
func (controller *DLQPurgeController) GetPath() string {
	return dlqPurgePath
}

// FormatAsRelativeLink formats this controller's URL
func (controller *DLQPurgeController) FormatAsRelativeLink(params ...httprouter.Param) string {
	return formatURL(params, dlqPurgePath, channelIDPathParamKey, consumerIDPathParamKey)
}

// Delete implements DELETE /channel/:channelId/consumer/:consumerId/dlq
func (controller *DLQPurgeController) Delete(w http.ResponseWriter, r *http.Request, params httprouter.Params) {
	consumer, valid := getChannelConsumerWithAuth(w, r, params, controller.ChannelRepo, controller.ConsumerRepo)
	if !valid {
		return
	}

	rowsAffected, err := controller.DeliveryJobRepo.DeleteDeadJobsForConsumer(consumer, uint(controller.BrokerConfig.GetMaxRetry()))
	if err != nil {
		writeErr(w, err)
		return
	}

	if rowsAffected > 0 {
		controller.DLQSummaryRepo.DecrementCount(consumer.ID.String(), rowsAffected)
		controller.Metrics.DeadJobCount.WithLabelValues(consumer.ConsumingFrom.ChannelID, consumer.ID.String()).Sub(float64(rowsAffected))
	}

	writeJSON(w, map[string]int64{"deletedCount": rowsAffected})
}

// DeadJobDeleteController handles DELETE /channel/:channelId/consumer/:consumerId/job/:jobId
type DeadJobDeleteController struct {
	ChannelRepo     storage.ChannelRepository
	ConsumerRepo    storage.ConsumerRepository
	DeliveryJobRepo storage.DeliveryJobRepository
	DLQSummaryRepo  storage.DLQSummaryRepository
	BrokerConfig    config.BrokerConfig
	Metrics         *dispatcher.MetricsContainer
}

// NewDeadJobDeleteController creates a new dead job delete controller
func NewDeadJobDeleteController(
	channelRepo storage.ChannelRepository,
	consumerRepo storage.ConsumerRepository,
	deliveryJobRepo storage.DeliveryJobRepository,
	dlqSummaryRepo storage.DLQSummaryRepository,
	brokerConfig config.BrokerConfig,
	metrics *dispatcher.MetricsContainer,
) *DeadJobDeleteController {
	return &DeadJobDeleteController{
		ChannelRepo:     channelRepo,
		ConsumerRepo:    consumerRepo,
		DeliveryJobRepo: deliveryJobRepo,
		DLQSummaryRepo:  dlqSummaryRepo,
		BrokerConfig:    brokerConfig,
		Metrics:         metrics,
	}
}

// GetPath returns the endpoint's path
func (controller *DeadJobDeleteController) GetPath() string {
	return deadJobPath
}

// FormatAsRelativeLink formats this controller's URL
func (controller *DeadJobDeleteController) FormatAsRelativeLink(params ...httprouter.Param) string {
	return formatURL(params, deadJobPath, channelIDPathParamKey, consumerIDPathParamKey, jobIDPathParamKey)
}

// Delete implements DELETE /channel/:channelId/consumer/:consumerId/job/:jobId
func (controller *DeadJobDeleteController) Delete(w http.ResponseWriter, r *http.Request, params httprouter.Params) {
	logger := hlog.FromRequest(r)
	consumer, valid := getChannelConsumerWithAuth(w, r, params, controller.ChannelRepo, controller.ConsumerRepo)
	if !valid {
		return
	}

	jobID := params.ByName(jobIDPathParamKey)
	job, err := controller.DeliveryJobRepo.GetByID(jobID)
	if err != nil {
		logger.Error().Err(err).Msg("no job found: " + jobID)
		writeNotFound(w)
		return
	}

	if job.Listener.ConsumerID != consumer.ConsumerID {
		writeNotFound(w)
		return
	}

	if job.Status != data.JobDead {
		writeNotFound(w)
		return
	}

	rowsAffected, err := controller.DeliveryJobRepo.DeleteDeadJob(job, uint(controller.BrokerConfig.GetMaxRetry()))
	if err != nil {
		writeErr(w, err)
		return
	}

	if rowsAffected == 0 {
		writeStatus(w, http.StatusNotFound, errJobNotDeadOrRetryNotExhausted)
		return
	}

	controller.DLQSummaryRepo.DecrementCount(consumer.ID.String(), rowsAffected)
	controller.Metrics.DeadJobCount.WithLabelValues(consumer.ConsumingFrom.ChannelID, consumer.ID.String()).Sub(float64(rowsAffected))

	writeStatus(w, http.StatusNoContent, nil)
}

// getChannelConsumerWithAuth validates channel token and consumer token without requiring pull consumer type
func getChannelConsumerWithAuth(w http.ResponseWriter, r *http.Request, params httprouter.Params, channelRepo storage.ChannelRepository, consumerRepo storage.ConsumerRepository) (consumer *data.Consumer, valid bool) {
	logger := hlog.FromRequest(r)
	channelID := params.ByName(channelIDPathParamKey)
	channelToken := r.Header.Get(headerChannelToken)
	consumerID := params.ByName(consumerIDPathParamKey)
	consumerToken := r.Header.Get(headerConsumerToken)

	channel, err := channelRepo.Get(channelID)
	if err != nil {
		logger.Error().Err(err).Msg("no channel found: " + channelID)
		writeNotFound(w)
		return nil, false
	}
	if channel.Token != channelToken {
		writeStatus(w, http.StatusForbidden, errChannelTokenNotMatching)
		return nil, false
	}

	consumer, err = consumerRepo.Get(channelID, consumerID)
	if err != nil {
		logger.Error().Err(err).Msg("no consumer found: " + consumerID)
		writeNotFound(w)
		return nil, false
	}
	if consumer.Token != consumerToken {
		writeStatus(w, http.StatusForbidden, errConsumerTokenNotMatching)
		return nil, false
	}

	return consumer, true
}

// Generated with assistance from Claude AI
