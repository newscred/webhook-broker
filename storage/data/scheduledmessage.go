package data

import (
	"strconv"
	"time"

	"github.com/rs/xid"
)

// ScheduledMsgStatus represents the state of a ScheduledMessage
type ScheduledMsgStatus int

func (status ScheduledMsgStatus) String() string {
	switch status {
	case ScheduledMsgStatusScheduled:
		return ScheduledMsgStatusScheduledStr
	case ScheduledMsgStatusDispatched:
		return ScheduledMsgStatusDispatchedStr
	default:
		return strconv.Itoa(int(status))
	}
}

func (status ScheduledMsgStatus) GetValue() int {
	return int(status)
}

const (
	scheduledMessageLockPrefix = "sch-msg-"
	// ScheduledMsgStatusScheduled represents the state where message is waiting to be dispatched
	ScheduledMsgStatusScheduled ScheduledMsgStatus = iota + 100
	// ScheduledMsgStatusDispatched represents when message has been dispatched
	ScheduledMsgStatusDispatched
	// ScheduledMsgStatusScheduledStr is the string representation of message's scheduled status
	ScheduledMsgStatusScheduledStr = "SCHEDULED"
	// ScheduledMsgStatusDispatchedStr is the string representation of message's dispatched status
	ScheduledMsgStatusDispatchedStr = "DISPATCHED"
)

// ScheduledMessage represents a message scheduled for future delivery
type ScheduledMessage struct {
	BasePaginateable
	MessageID        string
	Payload          string
	ContentType      string
	Priority         uint
	Status           ScheduledMsgStatus
	BroadcastedTo    *Channel
	ProducedBy       *Producer
	DispatchSchedule time.Time
	DispatchedAt     time.Time
	Headers          HeadersMap
}

// QuickFix fixes the object state automatically as much as possible
func (message *ScheduledMessage) QuickFix() bool {
	madeChanges := message.BasePaginateable.QuickFix()
	if message.DispatchSchedule.IsZero() {
		message.DispatchSchedule = time.Now().Add(2 * time.Minute)
		madeChanges = true
	}
	switch message.Status {
	case ScheduledMsgStatusScheduled:
	case ScheduledMsgStatusDispatched:
	default:
		message.Status = ScheduledMsgStatusScheduled
		madeChanges = true
	}
	if len(message.MessageID) <= 0 {
		message.MessageID = xid.New().String()
		madeChanges = true
	}
	return madeChanges
}

// IsInValidState returns false if any of message id or payload or content type is empty, channel is nil,
// status not recognized, dispatch schedule not set properly. Call QuickFix before IsInValidState is called.
func (message *ScheduledMessage) IsInValidState() bool {
	valid := true
	if len(message.MessageID) <= 0 || len(message.Payload) <= 0 || len(message.ContentType) <= 0 {
		valid = false
	}
	if message.BroadcastedTo == nil || !message.BroadcastedTo.IsInValidState() || message.ProducedBy == nil || !message.ProducedBy.IsInValidState() {
		valid = false
	}
	if valid && message.Status != ScheduledMsgStatusScheduled && message.Status != ScheduledMsgStatusDispatched {
		valid = false
	}
	if valid {
		switch message.Status {
		case ScheduledMsgStatusScheduled:
			if message.DispatchSchedule.IsZero() {
				valid = false
			}
		case ScheduledMsgStatusDispatched:
			if message.DispatchedAt.IsZero() {
				valid = false
			}
		}
	}
	return valid
}

// GetChannelIDSafely retrieves channel id account for the fact that BroadcastedTo may be null
func (message *ScheduledMessage) GetChannelIDSafely() (channelID string) {
	if message.BroadcastedTo != nil {
		channelID = message.BroadcastedTo.ChannelID
	}
	return channelID
}

// GetLockID retrieves lock ID for the current instance of scheduled message
func (message *ScheduledMessage) GetLockID() string {
	return scheduledMessageLockPrefix + message.ID.String()
}

// NewScheduledMessage creates and returns new instance of scheduled message
func NewScheduledMessage(channel *Channel, producedBy *Producer, payload, contentType string, dispatchSchedule time.Time, headers HeadersMap) (*ScheduledMessage, error) {
	msg := &ScheduledMessage{
		Payload:          payload,
		ContentType:      contentType,
		BroadcastedTo:    channel,
		ProducedBy:       producedBy,
		DispatchSchedule: dispatchSchedule,
		Headers:          headers,
	}
	msg.QuickFix()
	var err error
	if !msg.IsInValidState() {
		err = ErrInsufficientInformationForCreating
	}
	return msg, err
}