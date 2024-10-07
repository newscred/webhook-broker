package data

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/rs/xid"
)

// MsgStatus represents the state of a Msg.
type MsgStatus int

func (status MsgStatus) String() string {
	switch status {
	case MsgStatusAcknowledged:
		return MsgStatusAcknowledgedStr
	case MsgStatusDispatched:
		return MsgStatusDispatchedStr
	default:
		return strconv.Itoa(int(status))
	}
}

const (
	messageLockPrefix = "msg-"
	// MsgStatusAcknowledged represents the state after receiving the message but before it is dispatched
	MsgStatusAcknowledged MsgStatus = iota + 100
	// MsgStatusDispatched represents the fact that the dispatch jobs have been created for the message
	MsgStatusDispatched
	// MsgStatusAcknowledgedStr is the string representation of message's acknowledged status
	MsgStatusAcknowledgedStr = "ACKNOWLEDGED"
	// MsgStatusDispatchedStr is the string representation of message's dispatched status
	MsgStatusDispatchedStr = "DISPATCHED"
)

type HeadersMap map[string]string

func (hmap *HeadersMap) Scan(value interface{}) error {
	if value == nil {
		*hmap = HeadersMap{}
		return nil
	}
	bytes, ok := value.([]byte)
	if !ok {
		return errors.New("HeadersMap must be a byte array")
	}
	if err := json.Unmarshal(bytes, hmap); err != nil {
		return err
	}
	return nil
}

func (hmap HeadersMap) Value() (driver.Value, error) {
	return json.Marshal(hmap)
}

// Message represents the main payload of the application to be delivered
type Message struct {
	BasePaginateable
	MessageID     string
	Payload       string
	ContentType   string
	Priority      uint
	Status        MsgStatus
	BroadcastedTo *Channel
	ProducedBy    *Producer
	ReceivedAt    time.Time
	OutboxedAt    time.Time
	Headers       HeadersMap
}

// QuickFix fixes the object state automatically as much as possible
func (message *Message) QuickFix() bool {
	madeChanges := message.BasePaginateable.QuickFix()
	if message.ReceivedAt.IsZero() {
		message.ReceivedAt = time.Now()
		madeChanges = true
	}
	if message.OutboxedAt.IsZero() {
		message.OutboxedAt = time.Now()
		madeChanges = true
	}
	switch message.Status {
	case MsgStatusAcknowledged:
	case MsgStatusDispatched:
	default:
		message.Status = MsgStatusAcknowledged
		madeChanges = true
	}
	if len(message.MessageID) <= 0 {
		message.MessageID = xid.New().String()
		madeChanges = true
	}
	return madeChanges
}

// IsInValidState returns false if any of message id or payload or content type is empty, channel is nil, callback URL is not url or not absolute URL,
// status not recognized, received at and outboxed at not set properly. Call QuickFix before IsInValidState is called.
func (message *Message) IsInValidState() bool {
	valid := true
	if len(message.MessageID) <= 0 || len(message.Payload) <= 0 || len(message.ContentType) <= 0 {
		valid = false
	}
	if message.BroadcastedTo == nil || !message.BroadcastedTo.IsInValidState() || message.ProducedBy == nil || !message.ProducedBy.IsInValidState() {
		valid = false
	}
	if valid && message.Status != MsgStatusAcknowledged && message.Status != MsgStatusDispatched {
		valid = false
	}
	if valid {
		switch message.Status {
		case MsgStatusAcknowledged:
			if message.ReceivedAt.IsZero() {
				valid = false
			}
		case MsgStatusDispatched:
			if message.OutboxedAt.IsZero() {
				valid = false
			}
		}
	}
	return valid
}

// GetChannelIDSafely retrieves channel id account for the fact that BroadcastedTo may be null
func (message *Message) GetChannelIDSafely() (channelID string) {
	if message.BroadcastedTo != nil {
		channelID = message.BroadcastedTo.ChannelID
	}
	return channelID
}

// GetLockID retrieves lock ID for the current instance of message
func (message *Message) GetLockID() string {
	return messageLockPrefix + message.ID.String()
}

// NewMessage creates and returns new instance of message
func NewMessage(channel *Channel, producedBy *Producer, payload, contentType string, headers HeadersMap) (*Message, error) {
	msg := &Message{Payload: payload, ContentType: contentType, BroadcastedTo: channel, ProducedBy: producedBy, Headers: headers}
	msg.QuickFix()
	var err error
	if !msg.IsInValidState() {
		err = ErrInsufficientInformationForCreating
	}
	return msg, err
}
