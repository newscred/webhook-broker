package controllers

import (
	"net/http"

	"github.com/julienschmidt/httprouter"
	"github.com/newscred/webhook-broker/storage"
	"github.com/rs/zerolog/hlog"
)

const (
	scheduledMessagePath = channelPath + "/scheduled-message/:messageId"
)

// ScheduledMessageController handles retrieving a single scheduled message
type ScheduledMessageController struct {
	ScheduledMessageRepository storage.ScheduledMessageRepository
	ChannelRepository          storage.ChannelRepository
}

// NewScheduledMessageController creates a new instance of the controller for managing scheduled messages
func NewScheduledMessageController(scheduledMsgRepo storage.ScheduledMessageRepository, channelRepo storage.ChannelRepository) *ScheduledMessageController {
	return &ScheduledMessageController{ScheduledMessageRepository: scheduledMsgRepo, ChannelRepository: channelRepo}
}

// Get retrieves a specific scheduled message by channel ID and message ID
func (controller *ScheduledMessageController) Get(w http.ResponseWriter, r *http.Request, params httprouter.Params) {
	logger := hlog.FromRequest(r)
	channelID := params.ByName(channelIDPathParamKey)
	messageID := params.ByName("messageId")

	scheduledMessage, err := controller.ScheduledMessageRepository.Get(channelID, messageID)
	if err != nil {
		logger.Error().Err(err).Str("channelID", channelID).Str("messageID", messageID).Msg("error retrieving scheduled message")
		writeNotFound(w)
		return
	}

	responseData := map[string]interface{}{
		"id":               scheduledMessage.ID.String(),
		"messageId":        scheduledMessage.MessageID,
		"contentType":      scheduledMessage.ContentType,
		"priority":         scheduledMessage.Priority,
		"producedBy":       scheduledMessage.ProducedBy.ProducerID,
		"dispatchSchedule": scheduledMessage.DispatchSchedule,
		"dispatchedAt":   nil,
		"status":           scheduledMessage.Status.String(),
		"payload":          scheduledMessage.Payload,
		"headers":          scheduledMessage.Headers,
	}

	// Only include dispatched date if it's set (not zero value)
	if !scheduledMessage.DispatchedAt.IsZero() {
		responseData["dispatchedAt"] = scheduledMessage.DispatchedAt
	}

	writeJSON(w, responseData)
}

// GetPath returns the endpoint's path
func (controller *ScheduledMessageController) GetPath() string {
	return scheduledMessagePath
}

// FormatAsRelativeLink formats as relative URL of this resource based on the params
func (controller *ScheduledMessageController) FormatAsRelativeLink(params ...httprouter.Param) string {
	return formatURL(params, scheduledMessagePath, channelIDPathParamKey, "messageId")
}
