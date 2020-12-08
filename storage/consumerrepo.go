package storage

import (
	"database/sql"

	"github.com/imyousuf/webhook-broker/storage/data"
)

// ConsumerDBRepository is the RDBMS implementation for ConsumerRepository
type ConsumerDBRepository struct {
	db                *sql.DB
	channelRepository ChannelRepository
}

// Store stores consumer with either update or insert
func (consumerRepo *ConsumerDBRepository) Store(consumer *data.Consumer) (*data.Consumer, error) {
	return nil, nil
}

// Delete deletes consumer from DB
func (consumerRepo *ConsumerDBRepository) Delete(consumer *data.Consumer) error {
	return nil
}

// Get retrieves consumer for specific channel, error if either channel or consumer id does not exist
func (consumerRepo *ConsumerDBRepository) Get(channelID string, consumerID string) (*data.Consumer, error) {
	return nil, nil
}

// GetList retrieves consumers for specific channel; return error if channel does not exist
func (consumerRepo *ConsumerDBRepository) GetList(channelID string, page *data.Pagination) ([]*data.Consumer, *data.Pagination, error) {
	return nil, nil, nil
}

// NewConsumerRepository initializes new consumer repository
func NewConsumerRepository(db *sql.DB, channelRepo ChannelRepository) ConsumerRepository {
	panicIfNoDBConnectionPool(db)
	return &ConsumerDBRepository{db: db, channelRepository: channelRepo}
}
