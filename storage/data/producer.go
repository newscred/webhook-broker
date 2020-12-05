package data

import (
	"errors"
	"time"

	"github.com/rs/xid"
)

// BasePaginateable provides common functionalities around paginateable objects
type BasePaginateable struct {
	ID        xid.ID
	CreatedAt time.Time
	UpdatedAt time.Time
}

// GetCursor returns the cursor value for this producer
func (paginateable *BasePaginateable) GetCursor() (*Cursor, error) {
	text, err := paginateable.ID.Value()
	cursor := Cursor(text.(string))
	return &cursor, err
}

// QuickFix fixes base paginatable model's attribute
func (paginateable *BasePaginateable) QuickFix() bool {
	madeChanges := false
	if paginateable.ID.IsNil() {
		paginateable.ID = xid.New()
		madeChanges = true
	}
	if paginateable.CreatedAt.IsZero() {
		paginateable.CreatedAt = time.Now()
		madeChanges = true
	}
	if paginateable.UpdatedAt.IsZero() {
		paginateable.UpdatedAt = time.Now()
		madeChanges = true
	}
	return madeChanges
}

// MessageStakeholder represents all objects around a message, for example, Producer, Channel, Consumer
type MessageStakeholder struct {
	BasePaginateable
	Name  string
	Token string
}

// Producer represents generator of messages
type Producer struct {
	MessageStakeholder
	ProducerID string
}

// QuickFix fixes the model to set default ID, name same as producer id, created and updated at to current time.
func (prod *Producer) QuickFix() bool {
	madeChanges := prod.BasePaginateable.QuickFix()
	if len(prod.Name) <= 0 && len(prod.ProducerID) > 0 {
		prod.Name = prod.ProducerID
		madeChanges = true
	}
	return madeChanges
}

// IsInValidState returns false if any of producer id or name or token is empty
func (prod *Producer) IsInValidState() bool {
	if len(prod.ProducerID) <= 0 || len(prod.Name) <= 0 || len(prod.Token) <= 0 {
		return false
	}
	return true
}

var (
	// ErrInsufficientInformationForCreating is returned when NewProducer is called with insufficient information
	ErrInsufficientInformationForCreating = errors.New("ID and Token is must for creating")
)

// NewProducer creates new Producer
func NewProducer(producerID string, token string) (*Producer, error) {
	if len(producerID) <= 0 || len(token) <= 0 {
		return nil, ErrInsufficientInformationForCreating
	}
	producer := Producer{ProducerID: producerID, MessageStakeholder: createMessageStakeholder(producerID, token)}
	return &producer, nil
}

func createMessageStakeholder(name string, token string) MessageStakeholder {
	return MessageStakeholder{BasePaginateable: BasePaginateable{ID: xid.New()}, Name: name, Token: token}
}
