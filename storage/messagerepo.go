package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/imyousuf/webhook-broker/storage/data"
)

// MessageDBRepository is the MessageRepository implementation
type MessageDBRepository struct {
	db                 *sql.DB
	channelRepository  ChannelRepository
	producerRepository ProducerRepository
}

var (
	// ErrDuplicateMessageIDForChannel represents when the a message with same ID already exists
	ErrDuplicateMessageIDForChannel = errors.New("duplicate message id for channel")
	// ErrNoTxInContext represents the case where transaction is not passed in the context
	ErrNoTxInContext = errors.New("no tx value in content")
)

// ContextKey represents context key
type ContextKey string

const (
	messageSelectRowCommonQuery            = "SELECT id, messageId, producerId, channelId, payload, contentType, priority, status, receivedAt, outboxedAt, createdAt, updatedAt FROM message WHERE"
	txContextKey                ContextKey = "tx"
)

// Create creates a new message if message.MessageID does not already exist; please ensure QuickFix is called before repo is called
func (msgRepo *MessageDBRepository) Create(message *data.Message) (err error) {
	if !message.IsInValidState() {
		err = data.ErrInsufficientInformationForCreating
	}
	if err == nil {
		_, msgErr := msgRepo.Get(message.GetChannelIDSafely(), message.MessageID)
		if msgErr == nil {
			err = ErrDuplicateMessageIDForChannel
		} else {
			err = transactionalSingleRowWriteExec(msgRepo.db, emptyOps, "INSERT INTO message (id, channelId, producerId, messageId, payload, contentType, priority, status, receivedAt, outboxedAt, createdAt, updatedAt) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
				args2SliceFnWrapper(message.ID, message.BroadcastedTo.ChannelID, message.ProducedBy.ProducerID, message.MessageID, message.Payload, message.ContentType, message.Priority, message.Status, message.ReceivedAt, message.OutboxedAt, message.CreatedAt, message.UpdatedAt))
		}
	}
	return err
}

// Get retrieves a message for a channel if it exists
func (msgRepo *MessageDBRepository) Get(channelID string, messageID string) (*data.Message, error) {
	channel, err := msgRepo.channelRepository.Get(channelID)
	message, err := msgRepo.getSingleMessage(messageSelectRowCommonQuery+" channelId like ? and messageId like ?", args2SliceFnWrapper(channelID, messageID), false)
	if err == nil {
		message.BroadcastedTo = channel
	}
	return message, err
}

func (msgRepo *MessageDBRepository) getSingleMessage(query string, queryArgs func() []interface{}, loadChannel bool) (message *data.Message, err error) {
	var producerID string
	var channelID string
	message = &data.Message{}
	if err == nil {
		err = querySingleRow(msgRepo.db, query, queryArgs,
			args2SliceFnWrapper(&message.ID, &message.MessageID, &producerID, &channelID, &message.Payload, &message.ContentType, &message.Priority, &message.Status, &message.ReceivedAt, &message.OutboxedAt, &message.CreatedAt, &message.UpdatedAt))
	}
	if err == nil {
		message.ProducedBy, err = msgRepo.producerRepository.Get(producerID)
	}
	if loadChannel && err == nil {
		message.BroadcastedTo, err = msgRepo.channelRepository.Get(channelID)
	}
	return message, err
}

// GetByID retrieves a message by its ID
func (msgRepo *MessageDBRepository) GetByID(id string) (*data.Message, error) {
	return msgRepo.getSingleMessage(messageSelectRowCommonQuery+" id like ?", args2SliceFnWrapper(id), true)
}

// SetDispatched sets the status of the message to dispatched within the transaction passed via txContext
func (msgRepo *MessageDBRepository) SetDispatched(txContext context.Context, message *data.Message) error {
	if message == nil || !message.IsInValidState() {
		return ErrInvalidStateToSave
	}
	tx, ok := txContext.Value(txContextKey).(*sql.Tx)
	if ok {
		currentTime := time.Now()
		err := inTransactionExec(tx, emptyOps, "UPDATE message SET status = ?, outboxedAt = ?, updatedAt = ? WHERE id like ? and status like ?", args2SliceFnWrapper(data.MsgStatusDispatched, currentTime, currentTime, message.ID, data.MsgStatusAcknowledged), int64(1))
		if err == nil {
			message.Status = data.MsgStatusDispatched
			message.OutboxedAt = currentTime
			message.UpdatedAt = currentTime
		}
		return err
	}
	return ErrNoTxInContext
}

// NewMessageRepository creates a new instance of MessageRepository
func NewMessageRepository(db *sql.DB, channelRepo ChannelRepository, producerRepo ProducerRepository) MessageRepository {
	panicIfNoDBConnectionPool(db)
	return &MessageDBRepository{db: db, channelRepository: channelRepo, producerRepository: producerRepo}
}
