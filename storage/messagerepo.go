package storage

import (
	"database/sql"
	"errors"

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
			err = transactionalSingleRowWriteExec(msgRepo.db, emptyOps, "INSERT INTO message (id, channelId, producerId, messageId, payload, contentType, priority, status, receivedAt, outboxedAt, createdAt, updatedAt) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)",
				args2SliceFnWrapper(message.ID, message.BroadcastedTo.ChannelID, message.ProducedBy.ProducerID, message.MessageID, message.Payload, message.ContentType, message.Priority, message.Status, message.ReceivedAt, message.OutboxedAt, message.CreatedAt, message.UpdatedAt))
		}
	}
	return err
}

// Get retrieves a message for a channel if it exists
func (msgRepo *MessageDBRepository) Get(channelID string, messageID string) (*data.Message, error) {
	message := &data.Message{}
	channel, err := msgRepo.channelRepository.Get(channelID)
	var producerID string
	if err == nil {
		err = querySingleRow(msgRepo.db, "SELECT id, producerId, messageId, payload, contentType, priority, status, receivedAt, outboxedAt, createdAt, updatedAt FROM message WHERE channelId like $1 and messageId like $2",
			args2SliceFnWrapper(channelID, messageID),
			args2SliceFnWrapper(&message.ID, &producerID, &message.MessageID, &message.Payload, &message.ContentType, &message.Priority, &message.Status, &message.ReceivedAt, &message.OutboxedAt, &message.CreatedAt, &message.UpdatedAt))
	}
	if err == nil {
		message.BroadcastedTo = channel
		producer, pErr := msgRepo.producerRepository.Get(producerID)
		if pErr == nil {
			message.ProducedBy = producer
		} else {
			err = pErr
		}
	}
	return message, err
}

// NewMessageRepository creates a new instance of MessageRepository
func NewMessageRepository(db *sql.DB, channelRepo ChannelRepository, producerRepo ProducerRepository) MessageRepository {
	panicIfNoDBConnectionPool(db)
	return &MessageDBRepository{db: db, channelRepository: channelRepo, producerRepository: producerRepo}
}
