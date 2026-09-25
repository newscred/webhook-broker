package controllers

import (
	"database/sql"
	"net/http"
	"net/url"

	"github.com/julienschmidt/httprouter"
	"github.com/newscred/webhook-broker/storage"
	"github.com/newscred/webhook-broker/storage/data"
)

const (
	consumersPath          = channelPath + "/consumers"
	consumerIDPathParamKey = "consumerId"
	consumerPath           = channelPath + "/consumer/:" + consumerIDPathParamKey
)

// ConsumerModel represents the data communicated to HTTP clients
type ConsumerModel struct {
	MsgStakeholder
	CallbackURL        string
	DeadLetterQueueURL string
	Type               string
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
// @Summary Get Consumer Details
// @Description Retrieves details for a specific consumer.
// @Tags Consumers
// @Produce json
// @Param channelId path string true "Channel ID"
// @Param consumerId path string true "Consumer ID"
// @Success 200 {object} ConsumerModel
// @Failure 404
// @Failure 500
// @Router /channel/{channelId}/consumer/{consumerId} [get]
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
		DeadLetterQueueURL: controller.DLQEndpoint.FormatAsRelativeLink(channelIDParam, consumerIDParam),
		Type:               consumer.Type.String(),
	}
	return consumerModel
}

// Put implements the PUT /channel/:channelId/consumer/:consumerId endpoint
// @Summary Update Consumer
// @Description Creates or updates a consumer for a channel.
// @Tags Consumers
// @Accept x-www-form-urlencoded
// @Produce json
// @Param channelId path string true "Channel ID"
// @Param consumerId path string true "Consumer ID"
// @Param If-Unmodified-Since header string false "Only update if the resource has not been modified since this timestamp"
// @Param token formData string false "Token"
// @Param name formData string false "Name"
// @Param callbackUrl formData string false "Callback URL"
// @Param type formData string false "Consumer type"
// @Success 200 {object} ConsumerModel
// @Failure 400
// @Failure 404
// @Failure 412
// @Failure 415
// @Failure 500
// @Router /channel/{channelId}/consumer/{consumerId} [put]
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
	consumerType := r.PostFormValue("type")
	inComingConsumer, err := data.NewConsumer(channel, consumerID, token, callbackURL, consumerType)
	if err != nil {
		writeBadRequest(w)
		return
	}
	inComingConsumer.Name = name
	consumer, updateErr := controller.ConsumerRepo.Store(inComingConsumer)
	writeGetResult(updateErr, func(w http.ResponseWriter) { writeErr(w, updateErr) }, w, controller.getConsumerModel(consumer))
}

// Delete implements the DELETE /channel/:channelId/consumer/:consumerId endpoint
// @Summary Delete Consumer
// @Description Deletes a consumer from a channel.
// @Tags Consumers
// @Param channelId path string true "Channel ID"
// @Param consumerId path string true "Consumer ID"
// @Param If-Unmodified-Since header string false "Only delete if the resource has not been modified since this timestamp"
// @Success 204
// @Failure 404
// @Failure 412
// @Failure 500
// @Router /channel/{channelId}/consumer/{consumerId} [delete]
func (controller *ConsumerController) Delete(w http.ResponseWriter, r *http.Request, params httprouter.Params) {
	consumer, err := controller.ConsumerRepo.Get(findParam(params, channelIDPathParamKey), findParam(params, consumerIDPathParamKey))
	switch err {
	case nil:
		if validRequest := isConditionalUpdateCalled(w, r, consumer); !validRequest {
			return
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
// @Summary List Consumers for a Channel
// @Description Retrieves a paginated list of all consumers for a specific channel.
// @Tags Consumers
// @Produce json
// @Param channelId path string true "Channel ID"
// @Success 200 {object} ListResult
// @Failure 404
// @Failure 500
// @Router /channel/{channelId}/consumers [get]
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
