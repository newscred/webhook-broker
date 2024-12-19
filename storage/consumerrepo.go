package storage

import (
	"database/sql"
	"time"
	"sync"

	"github.com/newscred/webhook-broker/storage/data"
)

const (
	consumerSelectRowCommonQuery = "SELECT id, consumerId, channelId, name, token, callbackUrl, type, createdAt, updatedAt FROM consumer WHERE"
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
	if consumer.Name != inConsumer.Name || consumer.Token != inConsumer.Token || consumer.CallbackURL != inConsumer.CallbackURL || consumer.Type != inConsumer.Type {
		if consumer.IsInValidState() {
			return consumerRepo.updateConsumer(inConsumer, consumer.Name, consumer.Token, consumer.CallbackURL, consumer.Type)
		}
		err = ErrInvalidStateToSave
	}
	return inConsumer, err
}

func (consumerRepo *ConsumerDBRepository) updateConsumer(consumer *data.Consumer, name, token, callbackURL string, ctype data.ConsumerType) (*data.Consumer, error) {
	err := transactionalSingleRowWriteExec(consumerRepo.db, func() {
		consumer.Name = name
		consumer.Token = token
		consumer.CallbackURL = callbackURL
		consumer.UpdatedAt = time.Now()
		consumer.Type = ctype
	}, "UPDATE consumer SET name = ?, token = ?, callbackUrl=?, type=?, updatedAt = ? WHERE consumerId = ? and channelId = ?",
		args2SliceFnWrapper(&consumer.Name, &consumer.Token, &consumer.CallbackURL, &consumer.Type, &consumer.UpdatedAt, consumer.ConsumerID, consumer.ConsumingFrom.ChannelID))
	return consumer, err
}

func (consumerRepo *ConsumerDBRepository) insertConsumer(consumer *data.Consumer) (*data.Consumer, error) {
	consumer.QuickFix()
	var err error
	if consumer.IsInValidState() {
		err = transactionalSingleRowWriteExec(consumerRepo.db, emptyOps, "INSERT INTO consumer (id, channelId, consumerId, name, token, callbackUrl, type, createdAt, updatedAt) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
			args2SliceFnWrapper(consumer.ID, consumer.ConsumingFrom.ChannelID, consumer.ConsumerID, consumer.Name, consumer.Token, consumer.CallbackURL, consumer.Type, consumer.CreatedAt, consumer.UpdatedAt))
	} else {
		err = ErrInvalidStateToSave
	}
	return consumer, err
}

// Delete deletes consumer from DB
func (consumerRepo *ConsumerDBRepository) Delete(consumer *data.Consumer) error {
	return transactionalSingleRowWriteExec(consumerRepo.db, emptyOps, "DELETE from consumer WHERE channelId = ? and consumerId = ?", args2SliceFnWrapper(consumer.GetChannelIDSafely(), consumer.ConsumerID))
}

// Get retrieves consumer for specific consumer, error if either consumer or channel does not exist
func (consumerRepo *ConsumerDBRepository) Get(channelID string, consumerID string) (consumer *data.Consumer, err error) {
	channel, err := consumerRepo.channelRepository.Get(channelID)
	if err == nil {
		consumer, err = consumerRepo.getSingleConsumer(consumerSelectRowCommonQuery+" channelId like ? and consumerId like ?", args2SliceFnWrapper(channelID, consumerID), false)
	}
	if err == nil {
		consumer.ConsumingFrom = channel
	}
	return consumer, err
}

func (consumerRepo *ConsumerDBRepository) getSingleConsumer(query string, queryArgs func() []interface{}, loadChannel bool) (consumer *data.Consumer, err error) {
	consumer = &data.Consumer{}
	consumer.ConsumingFrom = &data.Channel{}
	err = querySingleRow(consumerRepo.db, query, queryArgs,
		args2SliceFnWrapper(&consumer.ID, &consumer.ConsumerID, &consumer.ConsumingFrom.ChannelID, &consumer.Name, &consumer.Token, &consumer.CallbackURL, &consumer.Type, &consumer.CreatedAt, &consumer.UpdatedAt))
	if loadChannel && err == nil {
		consumer.ConsumingFrom, err = consumerRepo.channelRepository.Get(consumer.ConsumingFrom.ChannelID)
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
		baseQuery := "SELECT id, consumerId, name, token, callbackUrl, type, createdAt, updatedAt FROM consumer WHERE channelId like ?" + getPaginationQueryFragment(page, true)
		scanArgs := func() []interface{} {
			consumer := &data.Consumer{}
			consumer.ConsumingFrom = channel
			consumers = append(consumers, consumer)
			return []interface{}{&consumer.ID, &consumer.ConsumerID, &consumer.Name, &consumer.Token, &consumer.CallbackURL, &consumer.Type, &consumer.CreatedAt, &consumer.UpdatedAt}
		}
		var argsFunc func() []interface{} = args2SliceFnWrapper(channelID)
		times := getPaginationTimestampQueryArgs(page)
		if len(times) > 0 {
			argsFunc = args2SliceFnWrapper(channelID, times[0])
		}
		err = queryRows(consumerRepo.db, baseQuery, argsFunc, scanArgs)
	}
	if err == nil {
		consumerCount := len(consumers)
		if consumerCount > 0 {
			pagination = data.NewPagination(consumers[consumerCount-1], consumers[0])
		}
	}
	return consumers, pagination, err
}

// GetByID retrieves a consumer by its ID
func (consumerRepo *ConsumerDBRepository) GetByID(id string) (consumer *data.Consumer, err error) {
	return consumerRepo.getSingleConsumer(consumerSelectRowCommonQuery+" id like ?", args2SliceFnWrapper(id), true)
}

type PseudoConsumerRepository ConsumerRepository

// NewConsumerRepository initializes new consumer repository
func NewConsumerRepository(db *sql.DB, channelRepo ChannelRepository) PseudoConsumerRepository {
	panicIfNoDBConnectionPool(db)
	return &ConsumerDBRepository{db: db, channelRepository: channelRepo}
}

// CachedConsumerRepository is a decorator for ConsumerRepository that caches consumer data.
type CachedConsumerRepository struct {
	delegate ConsumerRepository
	cache    *MemoryCache[string, *data.Consumer]
	mutex    sync.RWMutex
}

// NewCachedConsumerRepository creates a new CachedConsumerRepository.
func NewCachedConsumerRepository(delegate PseudoConsumerRepository, ttl time.Duration) ConsumerRepository {
	return &CachedConsumerRepository{
		delegate: delegate,
		cache:    NewMemoryCache[string, *data.Consumer](ttl),
	}
}

// Get retrieves a consumer by ID, first checking the cache.
func (repo *CachedConsumerRepository) Get(channelID string, consumerID string) (*data.Consumer, error) {
	cacheKey := channelID + ":" + consumerID  // Create a composite key
	repo.mutex.RLock()
	if item, ok := repo.cache.Get(cacheKey); ok {
		repo.mutex.RUnlock()
		return item, nil // Cache hit
	}
	repo.mutex.RUnlock()

	// Cache miss; fetch from the underlying repository
	consumer, err := repo.delegate.Get(channelID, consumerID)
	if err != nil {
		return consumer, err
	}

	repo.mutex.Lock()
	repo.cache.Set(cacheKey, consumer) // Cache the consumer
	repo.mutex.Unlock()
	return consumer, nil
}


// GetByID retrieves a consumer by its ID from the cache or underlying repository
func (repo *CachedConsumerRepository) GetByID(id string) (*data.Consumer, error) {

	repo.mutex.RLock()
	if item, ok := repo.cache.Get(id); ok {
		repo.mutex.RUnlock()
		return item, nil //Cache Hit
	}
	repo.mutex.RUnlock()

	consumer, err := repo.delegate.GetByID(id)
	if err != nil {
		return nil, err
	}

	repo.mutex.Lock()
	repo.cache.Set(id, consumer)
	repo.mutex.Unlock()

	return consumer, nil
}


// Store delegates storing to the underlying repository and invalidates the cache.
func (repo *CachedConsumerRepository) Store(consumer *data.Consumer) (*data.Consumer, error) {
	consumer, err := repo.delegate.Store(consumer)
	if err == nil {
		repo.mutex.Lock()
		repo.cache.Delete(consumer.GetChannelIDSafely() + ":" + consumer.ConsumerID)
		repo.cache.Delete(consumer.ID.String())
		repo.mutex.Unlock()
	}
	return consumer, err
}

// Delete delegates deleting to the underlying repository and invalidates the cache.
func (repo *CachedConsumerRepository) Delete(consumer *data.Consumer) error {
	err := repo.delegate.Delete(consumer)
	if err == nil {
		repo.mutex.Lock()
		repo.cache.Delete(consumer.GetChannelIDSafely() + ":" + consumer.ConsumerID)
		repo.cache.Delete(consumer.ID.String())
		repo.mutex.Unlock()
	}
	return err
}

// GetList retrieves the list of consumers based on pagination params supplied. It will return an error if both after and before are present at the same time.
func (repo *CachedConsumerRepository) GetList(channelID string, page *data.Pagination) ([]*data.Consumer, *data.Pagination, error) {
	return repo.delegate.GetList(channelID, page) // Delegate to the underlying repository
}

func (repo *CachedConsumerRepository) Close() {
	repo.cache.Close()
}
