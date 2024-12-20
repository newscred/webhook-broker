package storage

import (
	"database/sql"
	"sync"
	"time"

	"github.com/newscred/webhook-broker/storage/data"
)

type PseudoChannelRepository ChannelRepository

// CachedChannelRepository is a decorator for ChannelRepository that caches channel data.
type CachedChannelRepository struct {
	delegate ChannelRepository
	cache    *MemoryCache[string, *data.Channel]
	mutex    sync.RWMutex
}

// NewCachedChannelRepository creates a new CachedChannelRepository.
func NewCachedChannelRepository(delegate PseudoChannelRepository, ttl time.Duration) ChannelRepository {
	return &CachedChannelRepository{
		delegate: delegate,
		cache:    NewMemoryCache[string, *data.Channel](ttl),
	}
}

// Get retrieves a channel by ID, first checking the cache.
func (repo *CachedChannelRepository) Get(channelID string) (*data.Channel, error) {
	repo.mutex.RLock()
	if item, ok := repo.cache.Get(channelID); ok {
		repo.mutex.RUnlock()
		return item, nil // Cache hit
	}
	repo.mutex.RUnlock()

	// Cache miss; fetch from the underlying repository
	channel, err := repo.delegate.Get(channelID)
	if err != nil {
		return channel, err
	}

	repo.mutex.Lock()
	repo.cache.Set(channelID, channel) // Cache the channel
	repo.mutex.Unlock()

	return channel, nil
}

// Store delegates storing to the underlying repository and invalidates the cache.
func (repo *CachedChannelRepository) Store(channel *data.Channel) (*data.Channel, error) {

	channel, err := repo.delegate.Store(channel)
	if err == nil {
		repo.mutex.Lock()
		repo.cache.Delete(channel.ChannelID)
		repo.mutex.Unlock()
	}
	return channel, err
}

// GetList retrieves the list of channel based on pagination params supplied. It will return a error if both after and before is present at the same time
func (repo *CachedChannelRepository) GetList(page *data.Pagination) ([]*data.Channel, *data.Pagination, error) {
	return repo.delegate.GetList(page) // Delegate to the underlying repository
}

// Close closes the underlying cache
func (repo *CachedChannelRepository) Close() {
	repo.cache.Close()
}

// NewChannelRepository retrieves new instance of channel repository
func NewChannelRepository(db *sql.DB) PseudoChannelRepository {
	panicIfNoDBConnectionPool(db)
	return &ChannelDBRepository{db: db}
}

// ChannelDBRepository channel repository implementation for RDBMS
type ChannelDBRepository struct {
	db *sql.DB
}

// Store either creates or updates the channel information
func (repo *ChannelDBRepository) Store(channel *data.Channel) (*data.Channel, error) {
	inChannel, err := repo.Get(channel.ChannelID)
	if err != nil {
		return repo.insertChannel(channel)
	}
	if channel.Name != inChannel.Name || channel.Token != inChannel.Token {
		if !channel.IsInValidState() {
			return &data.Channel{}, ErrInvalidStateToSave
		}
		return repo.updateChannel(inChannel, channel.Name, channel.Token)
	}
	return inChannel, err
}

func (repo *ChannelDBRepository) updateChannel(channel *data.Channel, name, token string) (*data.Channel, error) {
	err := transactionalSingleRowWriteExec(repo.db, func() {
		channel.Name = name
		channel.Token = token
		channel.UpdatedAt = time.Now()
	}, "UPDATE channel SET name = ?, token = ?, updatedAt = ? WHERE channelId = ?",
		args2SliceFnWrapper(&channel.Name, &channel.Token, &channel.UpdatedAt, &channel.ChannelID))
	return channel, err
}

func (repo *ChannelDBRepository) insertChannel(channel *data.Channel) (*data.Channel, error) {
	channel.QuickFix()
	if !channel.IsInValidState() {
		return channel, ErrInvalidStateToSave
	}
	err := transactionalSingleRowWriteExec(repo.db, emptyOps, "INSERT INTO channel (id, channelId, name, token, createdAt, updatedAt) VALUES (?, ?, ?, ?, ?, ?)",
		args2SliceFnWrapper(channel.ID, channel.ChannelID, channel.Name, channel.Token, channel.CreatedAt, channel.UpdatedAt))
	return channel, err
}

// Get retrieves the channel with matching channel id
func (repo *ChannelDBRepository) Get(channelID string) (*data.Channel, error) {
	channel := &data.Channel{}
	err := querySingleRow(repo.db, "SELECT id, channelId, name, token, createdAt, updatedAt FROM channel WHERE channelId like ?", args2SliceFnWrapper(channelID),
		args2SliceFnWrapper(&channel.ID, &channel.ChannelID, &channel.Name, &channel.Token, &channel.CreatedAt, &channel.UpdatedAt))
	return channel, err
}

// GetList retrieves the list of channel based on pagination params supplied. It will return a error if both after and before is present at the same time
func (repo *ChannelDBRepository) GetList(page *data.Pagination) ([]*data.Channel, *data.Pagination, error) {
	channels := make([]*data.Channel, 0)
	pagination := &data.Pagination{}
	if page == nil || (page.Next != nil && page.Previous != nil) {
		return channels, pagination, ErrPaginationDeadlock
	}
	baseQuery := "SELECT id, channelId, name, token, createdAt, updatedAt FROM channel" + getPaginationQueryFragment(page, false)
	scanArgs := func() []interface{} {
		channel := &data.Channel{}
		channels = append(channels, channel)
		return []interface{}{&channel.ID, &channel.ChannelID, &channel.Name, &channel.Token, &channel.CreatedAt, &channel.UpdatedAt}
	}
	err := queryRows(repo.db, baseQuery, args2SliceFnWrapper(getPaginationTimestampQueryArgs(page)...), scanArgs)
	if err == nil {
		channelCount := len(channels)
		if channelCount > 0 {
			pagination = data.NewPagination(channels[channelCount-1], channels[0])
		}
	}
	return channels, pagination, err
}
