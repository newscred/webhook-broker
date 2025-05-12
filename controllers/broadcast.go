package controllers

import (
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog/hlog"

	"github.com/julienschmidt/httprouter"
	"github.com/newscred/webhook-broker/dispatcher"
	"github.com/newscred/webhook-broker/storage"
	"github.com/newscred/webhook-broker/storage/data"
)

const (
	broadcastPath             = channelPath + "/broadcast"
	headerPriority            = "X-Broker-Message-Priority"
	headerChannelToken        = "X-Broker-Channel-Token"
	headerProducerToken       = "X-Broker-Producer-Token"
	headerProducerID          = "X-Broker-Producer-ID"
	headerMessageID           = "X-Broker-Message-ID"
	headerMetadataHeaders     = "X-Broker-Metadata-Headers"
	headerScheduledFor        = "X-Broker-Scheduled-For"
	headerLocation            = "Location"
	defaultMessageContentType = "application/octet-stream"
	messageIDLogFieldKey      = "messageId"
	defaultMinScheduleDelay   = 2 * time.Minute
	iso8601Layout             = time.RFC3339
)

var (
	errChannelTokenNotMatching        = errors.New("channel token does not match")
	errProducerTokenNotMatching       = errors.New("producer token does not match")
	errProducerDoesNotExist           = errors.New("producer could not be found")
	errBodyCouldNotBeRead             = errors.New("body could not be read")
	errInvalidScheduledDateFormat     = errors.New("invalid date format for X-Broker-Scheduled-For, expected ISO-8601/RFC3339 format")
	errScheduledTimeTooCloseToPresent = errors.New("scheduled time must be at least 2 minutes in the future")
)

// BroadcastController receives new Message to broadcasted to a valid channel
type BroadcastController struct {
	MessageRepository          storage.MessageRepository
	ChannelRepository          storage.ChannelRepository
	ProducerRepository         storage.ProducerRepository
	ScheduledMessageRepository storage.ScheduledMessageRepository
	Dispatcher                 dispatcher.MessageDispatcher
}

// NewBroadcastController creates a new instance of the controller responsible for broadcasting a message
func NewBroadcastController(channelRepo storage.ChannelRepository, msgRepo storage.MessageRepository, producerRepo storage.ProducerRepository, scheduledMsgRepo storage.ScheduledMessageRepository, dispatcher dispatcher.MessageDispatcher) *BroadcastController {
	return &BroadcastController{
		ChannelRepository:          channelRepo,
		MessageRepository:          msgRepo,
		ProducerRepository:         producerRepo,
		ScheduledMessageRepository: scheduledMsgRepo,
		Dispatcher:                 dispatcher,
	}
}

// Post Receives message to be broadcasted to a channel
func (broadcastController *BroadcastController) Post(w http.ResponseWriter, r *http.Request, params httprouter.Params) {
	channel, producer, valid := broadcastController.getChannelAndProducerWithValidation(w, r, params)
	if !valid {
		return
	}

	headers := make(data.HeadersMap)
	metadataHeaders := getMetadataHeaders(r)
	if len(metadataHeaders) > 0 {
		for _, key := range metadataHeaders {
			if val := r.Header.Get(key); val != "" {
				headers[key] = val
			}
		}
	}

	logger := hlog.FromRequest(r)
	contentType := getContentType(r)
	priority := getPriority(r)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		logger.Error().Err(err).Msg("error reading body")
		writeErr(w, errBodyCouldNotBeRead)
		return
	}

	// Check if this is a scheduled message
	scheduledTime, err := parseAndValidateScheduledTime(r)
	if err != nil {
		if err == errInvalidScheduledDateFormat {
			logger.Error().Err(err).Msg("invalid scheduled date format")
			writeStatus(w, http.StatusBadRequest, err)
		} else if err == errScheduledTimeTooCloseToPresent {
			logger.Error().Err(err).Msg("scheduled time too close to present")
			writeStatus(w, http.StatusPreconditionFailed, err)
		} else {
			logger.Error().Err(err).Msg("unknown error parsing scheduled time")
			writeErr(w, err)
		}
		return
	}

	incomingMsgID := r.Header.Get(headerMessageID)
	priorityValue := uint(math.Abs(float64(priority)))

	if !scheduledTime.IsZero() {
		// This is a scheduled message
		scheduledMessage, _ := data.NewScheduledMessage(channel, producer, string(body), contentType, scheduledTime, headers)
		if len(incomingMsgID) > 0 {
			scheduledMessage.MessageID = incomingMsgID
		}
		scheduledMessage.Priority = priorityValue

		if err = broadcastController.ScheduledMessageRepository.Create(scheduledMessage); err == nil {
			logger.Info().
				Str(messageIDLogFieldKey, scheduledMessage.ID.String()).
				Time("scheduledFor", scheduledTime).
				Msg("Scheduled message accepted for future broadcast")

			w.Header().Add(headerLocation, "/channel/"+channel.ChannelID+"/scheduled-message/"+scheduledMessage.MessageID)
			writeStatus(w, http.StatusCreated, nil)
		} else if err == storage.ErrDuplicateScheduledMessageIDForChannel {
			logger.Error().
				Err(err).
				Str(messageIDLogFieldKey, scheduledMessage.ID.String()).
				Msg("scheduled message rejected because its duplicate id in channel")
			writeStatus(w, http.StatusConflict, err)
		} else {
			logger.Error().Err(err).Msg("error creating scheduled message")
			writeErr(w, err)
		}
	} else {
		// This is a regular message (not scheduled)
		message, _ := data.NewMessage(channel, producer, string(body), contentType, headers)
		if len(incomingMsgID) > 0 {
			message.MessageID = incomingMsgID
		}
		message.Priority = priorityValue

		if err = broadcastController.MessageRepository.Create(message); err == nil {
			logger.Info().Str(messageIDLogFieldKey, message.ID.String()).Msg("Message accepted for broadcast")
			go broadcastController.Dispatcher.Dispatch(message)
			w.Header().Add(headerLocation, "/channel/"+channel.ChannelID+"/message/"+message.MessageID)
			writeStatus(w, http.StatusCreated, nil)
		} else if err == storage.ErrDuplicateMessageIDForChannel {
			logger.Error().Err(err).Str(messageIDLogFieldKey, message.ID.String()).Msg("message rejected because its duplicate id in channel")
			writeStatus(w, http.StatusConflict, err)
		} else {
			logger.Error().Err(err).Msg("error creating message")
			writeErr(w, err)
		}
	}
}

func (broadcastController *BroadcastController) getChannelAndProducerWithValidation(w http.ResponseWriter, r *http.Request, params httprouter.Params) (channel *data.Channel, producer *data.Producer, valid bool) {
	var err error
	valid = true
	logger := hlog.FromRequest(r)
	channelID := params.ByName(channelIDPathParamKey)
	channelToken := r.Header.Get(headerChannelToken)
	producerID := r.Header.Get(headerProducerID)
	producerToken := r.Header.Get(headerProducerToken)
	channel, err = broadcastController.ChannelRepository.Get(channelID)
	if err != nil {
		logger.Error().Err(err).Msg("no channel found: " + channelID)
		writeNotFound(w)
		valid = false
	} else if channel.Token != channelToken {
		logger.Error().Msg(fmt.Sprintf("channel token did not match: %s vs %s", channel.Token, channelToken))
		writeStatus(w, http.StatusForbidden, errChannelTokenNotMatching)
		valid = false
	} else if producer, err = broadcastController.ProducerRepository.Get(producerID); err != nil {
		logger.Error().Err(err).Msg("no producer found: " + producerID)
		writeStatus(w, http.StatusUnauthorized, errProducerDoesNotExist)
		valid = false
	} else if producer.Token != producerToken {
		logger.Error().Msg(fmt.Sprintf("producer token did not match: %s vs %s", producer.Token, producerToken))
		writeStatus(w, http.StatusForbidden, errProducerTokenNotMatching)
		valid = false
	}
	return channel, producer, valid
}

func getPriority(r *http.Request) int {
	priority, err := strconv.Atoi(r.Header.Get(headerPriority))
	if err != nil {
		priority = 0
	}
	return priority
}

func getContentType(r *http.Request) string {
	contentType := r.Header.Get(headerContentType)
	if len(contentType) < 1 {
		contentType = defaultMessageContentType
	}
	return contentType
}

func getMetadataHeaders(r *http.Request) []string {
	metadataHeadersValue := r.Header.Get(headerMetadataHeaders)
	if metadataHeadersValue == "" {
		return []string{}
	}
	return strings.Split(metadataHeadersValue, ",")
}

func parseAndValidateScheduledTime(r *http.Request) (time.Time, error) {
	var emptyTime time.Time
	scheduledForValue := r.Header.Get(headerScheduledFor)
	if scheduledForValue == "" {
		return emptyTime, nil // No scheduled time specified, not an error
	}

	// Parse the scheduled time
	scheduledTime, err := time.Parse(iso8601Layout, scheduledForValue)
	if err != nil {
		return emptyTime, errInvalidScheduledDateFormat
	}

	// Check if scheduled time is far enough in the future
	if scheduledTime.Before(time.Now().Add(defaultMinScheduleDelay)) {
		return emptyTime, errScheduledTimeTooCloseToPresent
	}

	return scheduledTime, nil
}

// GetPath returns the endpoint's path
func (broadcastController *BroadcastController) GetPath() string {
	return broadcastPath
}

// FormatAsRelativeLink Format as relative URL of this resource based on the params
func (broadcastController *BroadcastController) FormatAsRelativeLink(params ...httprouter.Param) string {
	return formatURL(params, broadcastPath, channelIDPathParamKey)
}
