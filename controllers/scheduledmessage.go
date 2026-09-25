package controllers

import (
	"net/http"
	"time"

	"github.com/julienschmidt/httprouter"
	"github.com/newscred/webhook-broker/storage"
	"github.com/newscred/webhook-broker/storage/data"
	"github.com/rs/zerolog/hlog"
)

const (
	scheduledMessagePath = channelPath + "/scheduled-message/:messageId"
)

// ScheduledMessageModel represents the response for a scheduled message
type ScheduledMessageModel struct {
	ID               string         `json:"id"`
	MessageID        string         `json:"messageId"`
	ContentType      string         `json:"contentType"`
	Priority         uint           `json:"priority"`
	ProducedBy       string         `json:"producedBy"`
	DispatchSchedule time.Time      `json:"dispatchSchedule"`
	DispatchedAt     *time.Time     `json:"dispatchedAt"`
	Status           string         `json:"status"`
	Payload          string         `json:"payload"`
	Headers          data.HeadersMap `json:"headers"`
}

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
// @Summary Get Scheduled Message Details
// @Description Retrieves details of a specific scheduled message.
// @Tags Scheduled Messages
// @Produce json
// @Param channelId path string true "Channel ID"
// @Param messageId path string true "Message ID"
// @Success 200 {object} ScheduledMessageModel
// @Failure 404
// @Failure 500
// @Router /channel/{channelId}/scheduled-message/{messageId} [get]
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

	model := &ScheduledMessageModel{
		ID:               scheduledMessage.ID.String(),
		MessageID:        scheduledMessage.MessageID,
		ContentType:      scheduledMessage.ContentType,
		Priority:         scheduledMessage.Priority,
		ProducedBy:       scheduledMessage.ProducedBy.ProducerID,
		DispatchSchedule: scheduledMessage.DispatchSchedule,
		Status:           scheduledMessage.Status.String(),
		Payload:          scheduledMessage.Payload,
		Headers:          scheduledMessage.Headers,
	}

	if !scheduledMessage.DispatchedAt.IsZero() {
		model.DispatchedAt = &scheduledMessage.DispatchedAt
	}

	writeJSON(w, model)
}

// GetPath returns the endpoint's path
func (controller *ScheduledMessageController) GetPath() string {
	return scheduledMessagePath
}

// FormatAsRelativeLink formats as relative URL of this resource based on the params
func (controller *ScheduledMessageController) FormatAsRelativeLink(params ...httprouter.Param) string {
	return formatURL(params, scheduledMessagePath, channelIDPathParamKey, "messageId")
}
