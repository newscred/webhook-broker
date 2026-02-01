package controllers

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/julienschmidt/httprouter"
	"github.com/newscred/webhook-broker/storage"
	"github.com/newscred/webhook-broker/storage/data"
	"github.com/rs/zerolog/log"
)

const (
	messageIDParamKey    = "messageId"
	messagePath          = channelPath + "/message/:" + messageIDParamKey
	messagesPath         = channelPath + "/messages"
	messagesStatusPath   = channelPath + "/messages-status"
	dlqPath              = consumerPath + "/dlq"
	requeueFormParamName = "requeue"
)

// DeliveryJobModel represents a delivery job of a message
type DeliveryJobModel struct {
	ListenerEndpoint string
	ListenerName     string
	Status           string
	StatusChangedAt  time.Time
}

type HyperlinkedDeliveryJobModel struct {
	DeliveryJobModel
	MessageURL    string
	ChannelURL    string
	ConsumerURL   string
	ProducerURL   string
	JobRequeueURL string
}

// DeadDeliveryJobModel is a DeliveryJobModel with reference to its message and to be used for DLQ
type DeadDeliveryJobModel struct {
	DeliveryJobModel
	MessageURL    string
	JobRequeueURL string
}

// DLQList represents the list of jobs that are dead
type DLQList struct {
	DeadJobs []*DeadDeliveryJobModel
	Pages    map[string]string
}

// MessageModel represents a single message
type MessageModel struct {
	ID           string
	Payload      string
	ContentType  string
	ProducedBy   string
	ReceivedAt   time.Time
	DispatchedAt time.Time
	Status       string
	Jobs         []*DeliveryJobModel
	Headers      data.HeadersMap
}

type TheCount struct {
	Count int
	Links map[string]string
}

type StatusCount struct {
	Counts map[string]TheCount
}

func newMessageModel(message *data.Message, jobs ...*data.DeliveryJob) *MessageModel {
	messageModel := &MessageModel{
		ID:           message.MessageID,
		Payload:      message.Payload,
		ContentType:  message.ContentType,
		ReceivedAt:   message.ReceivedAt,
		DispatchedAt: message.OutboxedAt,
		Status:       message.Status.String(),
		ProducedBy:   message.ProducedBy.Name,
		Headers:      message.Headers,
		Jobs:         make([]*DeliveryJobModel, 0, len(jobs)),
	}
	for _, job := range jobs {
		messageModel.Jobs = append(messageModel.Jobs, newDeliveryJobModel(job))
	}
	return messageModel
}

func newDeliveryJobModel(job *data.DeliveryJob) *DeliveryJobModel {
	return &DeliveryJobModel{
		ListenerName:     job.Listener.Name,
		ListenerEndpoint: job.Listener.CallbackURL,
		Status:           job.Status.String(),
		StatusChangedAt:  job.StatusChangedAt,
	}
}

func newDeadDeliveryJobs(msgController EndpointController, jobRequeueController EndpointController, jobs ...*data.DeliveryJob) []*DeadDeliveryJobModel {
	result := make([]*DeadDeliveryJobModel, 0, len(jobs))
	for _, job := range jobs {
		channelIDParam := httprouter.Param{Key: channelIDPathParamKey, Value: job.Message.BroadcastedTo.ChannelID}
		messageURL := msgController.FormatAsRelativeLink(channelIDParam,
			httprouter.Param{Key: messageIDParamKey, Value: job.Message.MessageID})
		jobRequeueURL := jobRequeueController.FormatAsRelativeLink(channelIDParam,
			httprouter.Param{Key: consumerIDPathParamKey, Value: job.Listener.ConsumerID},
			httprouter.Param{Key: jobIDPathParamKey, Value: job.ID.String()})
		result = append(result, &DeadDeliveryJobModel{DeliveryJobModel: *newDeliveryJobModel(job), MessageURL: messageURL, JobRequeueURL: jobRequeueURL})
	}
	return result
}

// MessageController represents the GET endpoint for a single message broadcasted to a channel
type MessageController struct {
	MessageRepo     storage.MessageRepository
	DeliveryJobRepo storage.DeliveryJobRepository
}

// NewMessageController initializes the message controller
func NewMessageController(msgRepo storage.MessageRepository, djRepo storage.DeliveryJobRepository) *MessageController {
	return &MessageController{MessageRepo: msgRepo, DeliveryJobRepo: djRepo}
}

// GetPath returns the endpoint's path
func (messageController *MessageController) GetPath() string {
	return messagePath
}

// FormatAsRelativeLink Format as relative URL of this resource based on the params
func (messageController *MessageController) FormatAsRelativeLink(params ...httprouter.Param) string {
	return formatURL(params, messagePath, channelIDPathParamKey, messageIDParamKey)
}

// Get implements GET /channel/:channelId/message/:messageId
func (messageController *MessageController) Get(w http.ResponseWriter, r *http.Request, param httprouter.Params) {
	channelID := param.ByName(channelIDPathParamKey)
	messageID := param.ByName(messageIDParamKey)
	message, err := messageController.MessageRepo.Get(channelID, messageID)
	if err != nil {
		writeNotFound(w)
	} else {
		page := data.NewPagination(nil, nil)
		jobs := make([]*data.DeliveryJob, 0, 100)
		more := true
		for more {
			var singlePageJobs []*data.DeliveryJob
			singlePageJobs, page, err = messageController.DeliveryJobRepo.GetJobsForMessage(message, page)
			if err != nil {
				writeErr(w, err)
				more = false
			} else if len(singlePageJobs) > 0 {
				jobs = append(jobs, singlePageJobs...)
				page.Previous = nil
			} else {
				more = false
			}
		}
		if err == nil {
			writeJSON(w, newMessageModel(message, jobs...))
		}
	}
}

// MessagesController represents the GET endpoint for listing all messages broadcasted to a channel
type MessagesController struct {
	MessageController EndpointController
	MessageRepo       storage.MessageRepository
}

// NewMessagesController initializes the controller for messages in a channel
func NewMessagesController(msgController *MessageController, msgRepo storage.MessageRepository) *MessagesController {
	return &MessagesController{MessageController: msgController, MessageRepo: msgRepo}
}

// GetPath returns the endpoint's path
func (messagesController *MessagesController) GetPath() string {
	return messagesPath
}

// FormatAsRelativeLink Format as relative URL of this resource based on the params
func (messagesController *MessagesController) FormatAsRelativeLink(params ...httprouter.Param) string {
	return formatURL(params, messagesPath, channelIDPathParamKey)
}

// Get implements GET /channel/:channelId/messages
func (messagesController *MessagesController) Get(w http.ResponseWriter, r *http.Request, param httprouter.Params) {
	channelID := param.ByName(channelIDPathParamKey)
	statusFilters := extractMsgStatusFilters(r)
	messages, resultPagination, err := messagesController.MessageRepo.GetMessagesForChannel(channelID, getPagination(r), statusFilters...)
	if err != nil {
		switch err {
		case sql.ErrNoRows:
			writeNotFound(w)
		default:
			writeErr(w, err)
		}
		return
	}
	msgURLs := make([]string, len(messages))
	channelIDParam := httprouter.Param{Key: channelIDPathParamKey, Value: channelID}
	for index, msg := range messages {
		msgURLs[index] = messagesController.MessageController.FormatAsRelativeLink(channelIDParam, httprouter.Param{Key: messageIDParamKey, Value: msg.MessageID})
	}
	data := ListResult{Result: msgURLs, Pages: getPaginationLinks(r, resultPagination)}
	writeJSON(w, data)
}

func extractMsgStatusFilters(r *http.Request) []data.MsgStatus {
	statusFilterStrings := []string{}
	temp, ok := r.URL.Query()["status"]
	if ok {
		statusFilterStrings = temp
	}
	statusFilters := make([]data.MsgStatus, 0, len(statusFilterStrings))
	for _, statusString := range statusFilterStrings {
		status, err := strconv.Atoi(statusString)
		if err == nil {
			statusFilters = append(statusFilters, data.MsgStatus(status))
		}
	}
	log.Info().Msg(strconv.Itoa(len(statusFilters)))
	return statusFilters
}

type MessagesStatusController struct {
	MessagesController          EndpointController
	ScheduledMessagesController EndpointController
	MessageRepo                 storage.MessageRepository
	ScheduledMessageRepo        storage.ScheduledMessageRepository
}

// NewMessagesStatusController initializes the controller for messages in a channel
func NewMessagesStatusController(msgsController *MessagesController, scheduledMsgsController *ScheduledMessagesController,
	msgRepo storage.MessageRepository, scheduledMsgRepo storage.ScheduledMessageRepository) *MessagesStatusController {
	return &MessagesStatusController{
		MessagesController:          msgsController,
		ScheduledMessagesController: scheduledMsgsController,
		MessageRepo:                 msgRepo,
		ScheduledMessageRepo:        scheduledMsgRepo,
	}
}

// GetPath returns the endpoint's path
func (messagesStatusController *MessagesStatusController) GetPath() string {
	return messagesStatusPath
}

// FormatAsRelativeLink Format as relative URL of this resource based on the params
func (messagesStatusController *MessagesStatusController) FormatAsRelativeLink(params ...httprouter.Param) string {
	return formatURL(params, messagesStatusPath, channelIDPathParamKey)
}

// Get implements GET /channel/:channelId/messages-status
func (messagesStatusController *MessagesStatusController) Get(w http.ResponseWriter, r *http.Request, param httprouter.Params) {
	channelID := param.ByName(channelIDPathParamKey)
	log.Debug().Msgf("Channel ID: %s", channelID)

	// Get regular message counts
	statusCount, err := messagesStatusController.MessageRepo.GetMessageStatusCountsByChannel(channelID)
	log.Debug().Msgf("Status Count: %v, %v", statusCount, err)
	if err != nil {
		log.Error().Err(err).Msg("Error getting message status counts")
		writeErr(w, err)
		return
	}

	// Get scheduled message counts
	scheduledStatusCount, err := messagesStatusController.ScheduledMessageRepo.GetScheduledMessageStatusCountsByChannel(channelID)
	log.Debug().Msgf("Scheduled Status Count: %v, %v", scheduledStatusCount, err)
	if err != nil {
		log.Error().Err(err).Msg("Error getting scheduled message status counts")
		writeErr(w, err)
		return
	}

	// Get next scheduled message time
	nextScheduledTime, err := messagesStatusController.ScheduledMessageRepo.GetNextScheduledMessageTime(channelID)
	if err != nil {
		log.Error().Err(err).Msg("Error getting next scheduled message time")
		writeErr(w, err)
		return
	}

	// Build response with both regular and scheduled message counts
	statusCountOutput := &StatusCount{}
	statusCountOutput.Counts = make(map[string]TheCount)

	// Add regular message counts
	for _, count := range statusCount {
		statusString := count.Status.String()
		statusCountOutput.Counts[statusString] = TheCount{
			Count: count.Count,
			Links: map[string]string{
				"messages": fmt.Sprintf("%s?status=%d", messagesStatusController.MessagesController.FormatAsRelativeLink(httprouter.Param{Key: channelIDPathParamKey, Value: channelID}), count.Status.GetValue()),
			},
		}
	}

	// Add scheduled message counts with distinct keys
	for _, count := range scheduledStatusCount {
		statusString := "SCHEDULED_" + count.Status.String()
		statusCountOutput.Counts[statusString] = TheCount{
			Count: count.Count,
			Links: map[string]string{
				"messages": fmt.Sprintf("%s?status=%d", messagesStatusController.ScheduledMessagesController.FormatAsRelativeLink(httprouter.Param{Key: channelIDPathParamKey, Value: channelID}), count.Status.GetValue()),
			},
		}
	}

	// Add next scheduled message time if available
	responseData := map[string]interface{}{
		"counts": statusCountOutput.Counts,
	}

	if nextScheduledTime != nil {
		responseData["next_scheduled_message_at"] = nextScheduledTime
	}

	writeJSON(w, responseData)
}

// DLQController represents the GET and POST endpoint for reading dead and requeuing all dead messages for delivery.
type DLQController struct {
	MessageController    EndpointController
	DeliveryJobRepo      storage.DeliveryJobRepository
	ConsumerRepo         storage.ConsumerRepository
	JobRequeueController EndpointController
}

// NewDLQController retrieves the controller for DLQ list and requeue endpoints
func NewDLQController(msgController *MessageController, jobRequeueController *JobRequeueController, djRepo storage.DeliveryJobRepository, consumerRepo storage.ConsumerRepository) *DLQController {
	return &DLQController{MessageController: msgController, JobRequeueController: jobRequeueController, DeliveryJobRepo: djRepo, ConsumerRepo: consumerRepo}
}

// GetPath returns the endpoint's path
func (controller *DLQController) GetPath() string {
	return dlqPath
}

// FormatAsRelativeLink formats this controllers URL with the parameters provided. Both `consumerId` and `channelId` params must be sent else it will return the templated URL
func (controller *DLQController) FormatAsRelativeLink(params ...httprouter.Param) (result string) {
	return formatURL(params, dlqPath, channelIDPathParamKey, consumerIDPathParamKey)
}

// Get Retrieves dead jobs for a specific consumer
func (controller *DLQController) Get(w http.ResponseWriter, r *http.Request, params httprouter.Params) {
	consumer := controller.getConsumer(w, params)
	if consumer != nil {
		deadJobs, resultPagination, err := controller.DeliveryJobRepo.GetJobsForConsumer(consumer, data.JobDead, getPagination(r))
		if err == nil {
			data := &DLQList{DeadJobs: newDeadDeliveryJobs(controller.MessageController, controller.JobRequeueController, deadJobs...), Pages: getPaginationLinks(r, resultPagination)}
			writeJSON(w, data)
		} else {
			writeErr(w, err)
		}
	}
}

func (controller *DLQController) getConsumer(w http.ResponseWriter, params httprouter.Params) *data.Consumer {
	consumer, err := controller.ConsumerRepo.Get(params.ByName(channelIDPathParamKey), params.ByName(consumerIDPathParamKey))
	if err != nil {
		switch err {
		case sql.ErrNoRows:
			writeNotFound(w)
		default:
			writeErr(w, err)
		}
		return nil
	}
	return consumer
}

// Post Requeue dead jobs for another single delivery attempt
func (controller *DLQController) Post(w http.ResponseWriter, r *http.Request, params httprouter.Params) {
	validRequest := checkFormContentType(r, w)
	if !validRequest {
		return
	}
	consumer := controller.getConsumer(w, params)
	if consumer != nil {
		if r.PostFormValue(requeueFormParamName) != consumer.Token {
			writeStatus(w, http.StatusBadRequest, ErrBadRequestForRequeue)
			return
		}
		_, err := controller.DeliveryJobRepo.RequeueDeadJobsForConsumer(consumer)
		if err == nil {
			writeStatus(w, http.StatusAccepted, nil)
		} else {
			writeErr(w, err)
		}
	}
}
