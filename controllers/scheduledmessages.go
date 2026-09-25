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

// ScheduledMessagesListResponse represents the paginated response for listing scheduled messages
type ScheduledMessagesListResponse struct {
	Result []string          `json:"result"`
	Pages  map[string]string `json:"pages"`
}

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
// @Summary List Scheduled Messages for a Channel
// @Description Retrieves a paginated list of scheduled messages for a channel with optional status filtering.
// @Tags Scheduled Messages
// @Produce json
// @Param channelId path string true "Channel ID"
// @Param status query int false "Filter by scheduled message status"
// @Param previous query string false "Pagination cursor for previous page"
// @Param next query string false "Pagination cursor for next page"
// @Success 200 {object} ScheduledMessagesListResponse
// @Failure 404
// @Failure 500
// @Router /channel/{channelId}/scheduled-messages [get]
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

	responseData := ScheduledMessagesListResponse{
		Result: resultURIs,
		Pages:  getPaginationLinks(r, resultPagination),
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
