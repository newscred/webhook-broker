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

// Producer represents generator of messages
type Producer struct {
	BasePaginateable
	ProducerID string
	Name       string
	Token      string
}

// QuickFix fixes the model to set default ID, name same as producer id, created and updated at to current time.
func (prod *Producer) QuickFix() bool {
	madeChanges := false
	if prod.ID.IsNil() {
		prod.ID = xid.New()
		madeChanges = true
	}
	if len(prod.Name) <= 0 && len(prod.ProducerID) > 0 {
		prod.Name = prod.ProducerID
		madeChanges = true
	}
	if prod.CreatedAt.IsZero() {
		prod.CreatedAt = time.Now()
		madeChanges = true
	}
	if prod.UpdatedAt.IsZero() {
		prod.UpdatedAt = time.Now()
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
	// ErrInsufficientInformationForCreatingProducer is returned when NewProducer is called with insufficient information
	ErrInsufficientInformationForCreatingProducer = errors.New("Producer ID and Token is must for creating a Producer")
)

// NewProducer creates new Producer
func NewProducer(producerID string, token string) (*Producer, error) {
	if len(producerID) <= 0 || len(token) <= 0 {
		return nil, ErrInsufficientInformationForCreatingProducer
	}
	producer := Producer{BasePaginateable: BasePaginateable{ID: xid.New()}, ProducerID: producerID, Name: producerID, Token: token}
	return &producer, nil
}
