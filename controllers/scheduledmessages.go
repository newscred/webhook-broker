package controllers

import (
	"net/http"
	"strconv"

	"github.com/julienschmidt/httprouter"
	"github.com/newscred/webhook-broker/storage"
	"github.com/newscred/webhook-broker/storage/data"
	"github.com/rs/zerolog/hlog"
)

const (
	scheduledMessagesPath = channelPath + "/scheduled-messages"
	scheduledStatusParam  = "status"
)

// ScheduledMessagesController handles listing scheduled messages for a channel
type ScheduledMessagesController struct {
	ScheduledMessageRepository storage.ScheduledMessageRepository
	ChannelRepository          storage.ChannelRepository
}

// NewScheduledMessagesController creates a new instance of the controller for listing scheduled messages
func NewScheduledMessagesController(scheduledMsgRepo storage.ScheduledMessageRepository, channelRepo storage.ChannelRepository) *ScheduledMessagesController {
	return &ScheduledMessagesController{ScheduledMessageRepository: scheduledMsgRepo, ChannelRepository: channelRepo}
}

// Get retrieves scheduled messages for a channel with optional filtering
func (controller *ScheduledMessagesController) Get(w http.ResponseWriter, r *http.Request, params httprouter.Params) {
	logger := hlog.FromRequest(r)
	channelID := params.ByName(channelIDPathParamKey)

	// Check if channel exists
	_, err := controller.ChannelRepository.Get(channelID)
	if err != nil {
		logger.Error().Err(err).Str("channelID", channelID).Msg("error retrieving channel")
		writeNotFound(w)
		return
	}

	// Get pagination parameters
	pagination := getPagination(r)

	// Get status filter parameter
	statusFilters := make([]data.ScheduledMsgStatus, 0)
	statusParam := r.URL.Query().Get(scheduledStatusParam)
	if statusParam != "" {
		statusInt, err := strconv.Atoi(statusParam)
		if err == nil {
			statusFilters = append(statusFilters, data.ScheduledMsgStatus(statusInt))
		}
	}

	// Retrieve scheduled messages
	messages, resultPagination, err := controller.ScheduledMessageRepository.GetScheduledMessagesForChannel(channelID, pagination, statusFilters...)
	if err != nil {
		logger.Error().Err(err).Str("channelID", channelID).Msg("error retrieving scheduled messages")
		writeErr(w, err)
		return
	}

	// Format response
	resultURIs := make([]string, 0, len(messages))
	for _, msg := range messages {
		resultURIs = append(resultURIs, "/channel/"+channelID+"/scheduled-message/"+msg.MessageID)
	}

	responseData := map[string]interface{}{
		"result": resultURIs,
		"pages":  getPaginationLinks(r, resultPagination),
	}

	writeJSON(w, responseData)
}

// GetPath returns the endpoint's path
func (controller *ScheduledMessagesController) GetPath() string {
	return scheduledMessagesPath
}

// FormatAsRelativeLink formats as relative URL of this resource based on the params
func (controller *ScheduledMessagesController) FormatAsRelativeLink(params ...httprouter.Param) string {
	return formatURL(params, scheduledMessagesPath, channelIDPathParamKey)
}
