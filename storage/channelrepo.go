package storage

import (
	"database/sql"
	"time"

	"github.com/newscred/webhook-broker/storage/data"
)

// NewChannelRepository retrieves new instance of channel repository
func NewChannelRepository(db *sql.DB) ChannelRepository {
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
