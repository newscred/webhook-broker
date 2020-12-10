package controllers

import (
	"database/sql"
	"net/http"

	"github.com/imyousuf/webhook-broker/storage"
	"github.com/julienschmidt/httprouter"
)

const (
	consumersPath          = channelPath + "/consumers"
	consumerIDPathParamKey = "consumerId"
	consumerPath           = channelPath + "/consumer/:" + consumerIDPathParamKey
)

// ConsumerController represents all endpoints related to a single consumer for a channel
type ConsumerController struct {
	ChannelRepo     storage.ChannelRepository
	ConsumerRepo    storage.ConsumerRepository
	ChannelEndpoint EndpointController
}

// NewConsumerController creates and returns a new instance of ConsumerController
func NewConsumerController(channelEndpoint *ChannelController, channelRepo storage.ChannelRepository, consumerRepo storage.ConsumerRepository) *ConsumerController {
	return &ConsumerController{ChannelRepo: channelRepo, ConsumerRepo: consumerRepo, ChannelEndpoint: channelEndpoint}
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
	ChannelRepo      storage.ChannelRepository
	ConsumerRepo     storage.ConsumerRepository
	ChannelEndpoint  EndpointController
	ConsumerEndpoint EndpointController
}

// NewConsumersController creates and returns a new instance of ConsumersController
func NewConsumersController(channelEndpoint *ChannelController, consumerEndpoint *ConsumerController, channelRepo storage.ChannelRepository, consumerRepo storage.ConsumerRepository) *ConsumersController {
	return &ConsumersController{ChannelRepo: channelRepo, ConsumerRepo: consumerRepo, ChannelEndpoint: channelEndpoint, ConsumerEndpoint: consumerEndpoint}
}

// Get implements the /channels endpoint
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
	links := make(map[string]string)
	links["channel"] = controller.ChannelEndpoint.FormatAsRelativeLink(channelIDParam)
	data := ListResult{Result: consumerURLs, Pages: getPaginationLinks(r, resultPagination), Links: links}
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
