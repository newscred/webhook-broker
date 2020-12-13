package storage

import (
	"database/sql"
	"time"

	"github.com/imyousuf/webhook-broker/storage/data"
)

// ConsumerDBRepository is the RDBMS implementation for ConsumerRepository
type ConsumerDBRepository struct {
	db                *sql.DB
	channelRepository ChannelRepository
}

// Store stores consumer with either update or insert
func (consumerRepo *ConsumerDBRepository) Store(consumer *data.Consumer) (*data.Consumer, error) {
	inChannel, err := consumerRepo.channelRepository.Get(consumer.GetChannelIDSafely())
	if err != nil {
		return &data.Consumer{}, err
	}
	inConsumer, err := consumerRepo.Get(inChannel.ChannelID, consumer.ConsumerID)
	if err != nil {
		return consumerRepo.insertConsumer(consumer)
	}
	if consumer.Name != inConsumer.Name || consumer.Token != inConsumer.Token || consumer.CallbackURL != inConsumer.CallbackURL {
		if consumer.IsInValidState() {
			return consumerRepo.updateConsumer(inConsumer, consumer.Name, consumer.Token, consumer.CallbackURL)
		}
		err = ErrInvalidStateToSave
	}
	return inConsumer, err
}

func (consumerRepo *ConsumerDBRepository) updateConsumer(consumer *data.Consumer, name, token, callbackURL string) (*data.Consumer, error) {
	err := transactionalSingleRowWriteExec(consumerRepo.db, func() {
		consumer.Name = name
		consumer.Token = token
		consumer.CallbackURL = callbackURL
		consumer.UpdatedAt = time.Now()
	}, "UPDATE consumer SET name = $1, token = $2, callbackUrl=$3, updatedAt = $4 WHERE consumerId = $5 and channelId = $6",
		args2SliceFnWrapper(consumer.Name, consumer.Token, consumer.CallbackURL, consumer.UpdatedAt, consumer.ConsumerID, consumer.ConsumingFrom.ChannelID))
	return consumer, err
}

func (consumerRepo *ConsumerDBRepository) insertConsumer(consumer *data.Consumer) (*data.Consumer, error) {
	consumer.QuickFix()
	var err error
	if consumer.IsInValidState() {
		err = transactionalSingleRowWriteExec(consumerRepo.db, emptyOps, "INSERT INTO consumer (id, channelId, consumerId, name, token, callbackUrl, createdAt, updatedAt) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)",
			args2SliceFnWrapper(consumer.ID, consumer.ConsumingFrom.ChannelID, consumer.ConsumerID, consumer.Name, consumer.Token, consumer.CallbackURL, consumer.CreatedAt, consumer.UpdatedAt))
	} else {
		err = ErrInvalidStateToSave
	}
	return consumer, err
}

// Delete deletes consumer from DB
func (consumerRepo *ConsumerDBRepository) Delete(consumer *data.Consumer) error {
	return transactionalSingleRowWriteExec(consumerRepo.db, emptyOps, "DELETE from consumer WHERE channelId = $1 and consumerId = $2", args2SliceFnWrapper(consumer.GetChannelIDSafely(), consumer.ConsumerID))
}

// Get retrieves consumer for specific consumer, error if either consumer or channel does not exist
func (consumerRepo *ConsumerDBRepository) Get(channelID string, consumerID string) (*data.Consumer, error) {
	consumer := &data.Consumer{}
	channel, err := consumerRepo.channelRepository.Get(channelID)
	if err == nil {
		err = querySingleRow(consumerRepo.db, "SELECT id, consumerId, name, token, callbackUrl, createdAt, updatedAt FROM consumer WHERE channelId like $1 and consumerId like $2",
			args2SliceFnWrapper(channelID, consumerID),
			args2SliceFnWrapper(&consumer.ID, &consumer.ConsumerID, &consumer.Name, &consumer.Token, &consumer.CallbackURL, &consumer.CreatedAt, &consumer.UpdatedAt))
	}
	if err == nil {
		consumer.ConsumingFrom = channel
	}
	return consumer, err
}

// GetList retrieves consumers for specific consumer; return error if channel does not exist
func (consumerRepo *ConsumerDBRepository) GetList(channelID string, page *data.Pagination) ([]*data.Consumer, *data.Pagination, error) {
	consumers := make([]*data.Consumer, 0)
	pagination := &data.Pagination{}
	if page == nil || (page.Next != nil && page.Previous != nil) {
		return consumers, pagination, ErrPaginationDeadlock
	}
	channel, err := consumerRepo.channelRepository.Get(channelID)
	if err == nil {
		baseQuery := "SELECT id, consumerId, name, token, callbackUrl, createdAt, updatedAt FROM consumer" + getPaginationQueryFragment(page, false)
		scanArgs := func() []interface{} {
			consumer := &data.Consumer{}
			consumer.ConsumingFrom = channel
			consumers = append(consumers, consumer)
			return []interface{}{&consumer.ID, &consumer.ConsumerID, &consumer.Name, &consumer.Token, &consumer.CallbackURL, &consumer.CreatedAt, &consumer.UpdatedAt}
		}
		err = queryRows(consumerRepo.db, baseQuery, nilArgs, scanArgs)
	}
	if err == nil {
		consumerCount := len(consumers)
		if consumerCount > 0 {
			pagination = data.NewPagination(consumers[consumerCount-1], consumers[0])
		}
	}
	return consumers, pagination, err
}

// NewConsumerRepository initializes new consumer repository
func NewConsumerRepository(db *sql.DB, channelRepo ChannelRepository) ConsumerRepository {
	panicIfNoDBConnectionPool(db)
	return &ConsumerDBRepository{db: db, channelRepository: channelRepo}
}
