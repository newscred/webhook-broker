package controllers

import (
	"net/http"

	"github.com/julienschmidt/httprouter"
	"github.com/newscred/webhook-broker/storage"
	"github.com/newscred/webhook-broker/storage/data"
)

const (
	channelsPath          = "/channels"
	channelIDPathParamKey = "channelId"
	channelPath           = "/channel/:" + channelIDPathParamKey
)

// ChannelController is for /channel/:prodId
type ChannelController struct {
	ChannelRepo            storage.ChannelRepository
	ConsumersEndpoint      EndpointController
	MessagesEndpoint       EndpointController
	BroadcastEndpoint      EndpointController
	MessagesStatusEndpoint EndpointController
}

// ChannelModel represents the Channel data
type ChannelModel struct {
	MsgStakeholder
	ConsumersURL      string
	MessagesURL       string
	BroadcastURL      string
	MessagesStatusURL string
}

// Get implements the /channel/:prodId GET endpoint
func (channelController *ChannelController) Get(w http.ResponseWriter, r *http.Request, param httprouter.Params) {
	channelID := param.ByName(channelIDPathParamKey)
	channelModel, err := channelController.ChannelRepo.Get(channelID)
	writeGetResult(err, writeNotFound, w, channelController.getChannelModel(channelModel))
}

// Put implements the /channel/:prodId PUT endpoint
func (channelController *ChannelController) Put(w http.ResponseWriter, r *http.Request, param httprouter.Params) {
	validRequest := checkFormContentType(r, w)
	channelID := param.ByName(channelIDPathParamKey)
	channelModel, err := channelController.ChannelRepo.Get(channelID)
	if err == nil && validRequest {
		validRequest = isConditionalUpdateCalled(w, r, channelModel)
	}
	if !validRequest {
		return
	}
	token, name := getUpdateData(r, channelID)
	channel, _ := data.NewChannel(channelID, token)
	channel.Name = name
	channel, err = channelController.ChannelRepo.Store(channel)
	writeGetResult(err, func(w http.ResponseWriter) { writeErr(w, err) }, w, channelController.getChannelModel(channel))
}

func (channelController *ChannelController) getChannelModel(channel *data.Channel) *ChannelModel {
	channelIDParam := httprouter.Param{Key: channelIDPathParamKey, Value: channel.ChannelID}
	return &ChannelModel{MsgStakeholder: *getMessageStakeholder(channel.ChannelID, &channel.MessageStakeholder),
		ConsumersURL:      channelController.ConsumersEndpoint.FormatAsRelativeLink(channelIDParam),
		MessagesURL:       channelController.MessagesEndpoint.FormatAsRelativeLink(channelIDParam),
		BroadcastURL:      channelController.BroadcastEndpoint.FormatAsRelativeLink(channelIDParam),
		MessagesStatusURL: channelController.MessagesStatusEndpoint.FormatAsRelativeLink(channelIDParam)}
}

// GetPath returns the endpoint's path
func (channelController *ChannelController) GetPath() string {
	return channelPath
}

// FormatAsRelativeLink Format as relative URL of this resource based on the params
func (channelController *ChannelController) FormatAsRelativeLink(params ...httprouter.Param) string {
	return formatURL(params, channelPath, channelIDPathParamKey)
}

// ChannelsController for handling `/channels` endpoint
type ChannelsController struct {
	ChannelRepo     storage.ChannelRepository
	ChannelEndpoint EndpointController
}

// Get implements the /channels endpoint
func (channelsController *ChannelsController) Get(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	channels, resultPagination, err := channelsController.ChannelRepo.GetList(getPagination(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	channelURLs := make([]string, len(channels))
	for index, channel := range channels {
		channelURLs[index] = channelsController.ChannelEndpoint.FormatAsRelativeLink(httprouter.Param{Key: channelIDPathParamKey, Value: channel.ChannelID})
	}
	data := ListResult{Result: channelURLs, Pages: getPaginationLinks(r, resultPagination)}
	writeJSON(w, data)
}

// GetPath returns the endpoint's path
func (channelsController *ChannelsController) GetPath() string {
	return channelsPath
}

// FormatAsRelativeLink Format as relative URL of this resource based on the params
func (channelsController *ChannelsController) FormatAsRelativeLink(params ...httprouter.Param) string {
	return channelsPath
}

// NewChannelController initialize new channels controller
func NewChannelController(consumersController *ConsumersController, messagesController *MessagesController, broadcastController *BroadcastController, messagesStatusController *MessagesStatusController, channelRepo storage.ChannelRepository) *ChannelController {
	return &ChannelController{ChannelRepo: channelRepo, ConsumersEndpoint: consumersController, MessagesEndpoint: messagesController, BroadcastEndpoint: broadcastController, MessagesStatusEndpoint: messagesStatusController}
}

// NewChannelsController initialize new channels controller
func NewChannelsController(channelRepo storage.ChannelRepository, channelController *ChannelController) *ChannelsController {
	return &ChannelsController{ChannelRepo: channelRepo, ChannelEndpoint: channelController}
}
