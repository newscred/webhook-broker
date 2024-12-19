package storage

import (
	"database/sql"
	"sync"
	"time"

	"github.com/newscred/webhook-broker/storage/data"
)

// ProducerDBRepository is the producer repository implementation for RDBMS
type ProducerDBRepository struct {
	db *sql.DB
}

// Store either creates or updates the producer information
func (repo *ProducerDBRepository) Store(producer *data.Producer) (*data.Producer, error) {
	inProducer, err := repo.Get(producer.ProducerID)
	if err != nil {
		return repo.insertProducer(producer)
	}
	if producer.Name != inProducer.Name || producer.Token != inProducer.Token {
		if !producer.IsInValidState() {
			return &data.Producer{}, ErrInvalidStateToSave
		}
		return repo.updateProducer(inProducer, producer.Name, producer.Token)
	}
	return inProducer, err
}

func (repo *ProducerDBRepository) updateProducer(producer *data.Producer, name, token string) (*data.Producer, error) {
	err := transactionalSingleRowWriteExec(repo.db, func() {
		producer.Name = name
		producer.Token = token
		producer.UpdatedAt = time.Now()
	}, "UPDATE producer SET name = ?, token = ?, updatedAt = ? WHERE producerId = ?",
		args2SliceFnWrapper(&producer.Name, &producer.Token, &producer.UpdatedAt, producer.ProducerID))
	return producer, err
}

func (repo *ProducerDBRepository) insertProducer(producer *data.Producer) (*data.Producer, error) {
	producer.QuickFix()
	if !producer.IsInValidState() {
		return producer, ErrInvalidStateToSave
	}
	err := transactionalSingleRowWriteExec(repo.db, emptyOps, "INSERT INTO producer (id, producerId, name, token, createdAt, updatedAt) VALUES (?, ?, ?, ?, ?, ?)",
		args2SliceFnWrapper(producer.ID, producer.ProducerID, producer.Name, producer.Token, producer.CreatedAt, producer.UpdatedAt))
	return producer, err
}

// Get retrieves the producer with matching producer id
func (repo *ProducerDBRepository) Get(producerID string) (*data.Producer, error) {
	producer := &data.Producer{}
	err := querySingleRow(repo.db, "SELECT id, producerId, name, token, createdAt, updatedAt FROM producer WHERE producerId like ?", args2SliceFnWrapper(producerID),
		args2SliceFnWrapper(&producer.ID, &producer.ProducerID, &producer.Name, &producer.Token, &producer.CreatedAt, &producer.UpdatedAt))
	return producer, err
}

// GetList retrieves the list of producer based on pagination params supplied. It will return a error if both after and before is present at the same time
func (repo *ProducerDBRepository) GetList(page *data.Pagination) ([]*data.Producer, *data.Pagination, error) {
	producers := make([]*data.Producer, 0)
	pagination := &data.Pagination{}
	if page == nil || (page.Next != nil && page.Previous != nil) {
		return producers, pagination, ErrPaginationDeadlock
	}
	baseQuery := "SELECT id, producerId, name, token, createdAt, updatedAt FROM producer" + getPaginationQueryFragment(page, false)
	scanArgs := func() []interface{} {
		producer := &data.Producer{}
		producers = append(producers, producer)
		return []interface{}{&producer.ID, &producer.ProducerID, &producer.Name, &producer.Token, &producer.CreatedAt, &producer.UpdatedAt}
	}
	err := queryRows(repo.db, baseQuery, args2SliceFnWrapper(getPaginationTimestampQueryArgs(page)...), scanArgs)
	if err == nil {
		producerCount := len(producers)
		if producerCount > 0 {
			pagination = data.NewPagination(producers[producerCount-1], producers[0])
		}
	}
	return producers, pagination, err
}

type PseudoProducerRepository ProducerRepository

// NewProducerRepository returns a new producer repository
func NewProducerRepository(db *sql.DB) PseudoProducerRepository {
	panicIfNoDBConnectionPool(db)
	return &ProducerDBRepository{db: db}
}

// CachedProducerRepository is a decorator for ProducerRepository that caches producer data.
type CachedProducerRepository struct {
	delegate ProducerRepository
	cache    *MemoryCache[string, *data.Producer]
	mutex    sync.RWMutex
}

// NewCachedProducerRepository creates a new CachedProducerRepository.
func NewCachedProducerRepository(delegate PseudoProducerRepository, ttl time.Duration) ProducerRepository {
	return &CachedProducerRepository{
		delegate: delegate,
		cache:    NewMemoryCache[string, *data.Producer](ttl),
	}
}

// Get retrieves a producer by ID, first checking the cache.
func (repo *CachedProducerRepository) Get(producerID string) (*data.Producer, error) {
	repo.mutex.RLock()
	if item, ok := repo.cache.Get(producerID); ok {
		repo.mutex.RUnlock()
		return item, nil // Cache hit
	}
	repo.mutex.RUnlock()

	// Cache miss; fetch from the underlying repository
	producer, err := repo.delegate.Get(producerID)
	if err != nil {
		return producer, err
	}

	repo.mutex.Lock()
	repo.cache.Set(producerID, producer) // Cache the producer
	repo.mutex.Unlock()

	return producer, nil
}

// Store delegates storing to the underlying repository and invalidates the cache.
func (repo *CachedProducerRepository) Store(producer *data.Producer) (*data.Producer, error) {
	producer, err := repo.delegate.Store(producer)
	if err == nil {
		repo.mutex.Lock()
		repo.cache.Delete(producer.ProducerID)
		repo.mutex.Unlock()
	}
	return producer, err
}

// GetList retrieves the list of producers based on pagination params supplied.
// It delegates directly to the underlying repository as caching lists is more complex
// and requires invalidation strategies that are beyond the scope of this simple example.
func (repo *CachedProducerRepository) GetList(page *data.Pagination) ([]*data.Producer, *data.Pagination, error) {
	return repo.delegate.GetList(page)
}

// Close closes the underlying cache.
func (repo *CachedProducerRepository) Close() {
	repo.cache.Close()
}
