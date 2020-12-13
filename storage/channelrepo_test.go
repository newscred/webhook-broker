package storage

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/imyousuf/webhook-broker/storage/data"
	"github.com/stretchr/testify/assert"
)

const (
	successfulGetTestChannelID      = "get-test"
	nonExistingGetTestChannelID     = "get-test-ne"
	successfulInsertTestChannelID   = "s-insert-test"
	invalidStateUpdateTestChannelID = "i-update-test"
	successfulUpdateTestChannelID   = "s-update-test"
	dbErrUpdateTestChannelID        = "db-update-test"
	noChangeUpdateTestChannelID     = "nc-update-test"
	listTestChannelIDPrefix         = "get-list-"
)

func getChannelRepo() ChannelRepository {
	channelRepo := NewChannelRepository(testDB)
	return channelRepo
}

func TestChannelGet(t *testing.T) {
	t.Run("GetExisting", func(t *testing.T) {
		t.Parallel()
		repo := getChannelRepo()
		sampleChannel, err := data.NewChannel(successfulGetTestChannelID, successfulGetTestToken)
		assert.Nil(t, err)
		assert.True(t, sampleChannel.IsInValidState())
		resultChannel, err := repo.Store(sampleChannel)
		assert.Nil(t, err)
		assert.False(t, resultChannel.ID.IsNil())
		channel, err := repo.Get(successfulGetTestChannelID)
		assert.Nil(t, err)
		assert.False(t, channel.ID.IsNil())
		assert.Equal(t, sampleChannel.ID, channel.ID)
		assert.Equal(t, successfulGetTestChannelID, channel.ChannelID)
		assert.Equal(t, successfulGetTestChannelID, channel.Name)
		assert.Equal(t, successfulGetTestToken, channel.Token)
		assert.False(t, channel.CreatedAt.IsZero())
		assert.False(t, channel.UpdatedAt.IsZero())
		assert.True(t, channel.CreatedAt.Before(time.Now()))
		assert.True(t, channel.UpdatedAt.Before(time.Now()))
	})
	t.Run("GetMissing", func(t *testing.T) {
		t.Parallel()
		repo := getChannelRepo()
		_, err := repo.Get(nonExistingGetTestChannelID)
		assert.NotNil(t, err)
	})
}

func TestChannelStore(t *testing.T) {
	t.Run("Create:InvalidState", func(t *testing.T) {
		t.Parallel()
		channel, err := data.NewChannel(successfulInsertTestChannelID, successfulGetTestToken)
		assert.Nil(t, err)
		assert.True(t, channel.IsInValidState())
		channel.Token = ""
		assert.False(t, channel.IsInValidState())
		repo := getChannelRepo()
		_, err = repo.Store(channel)
		assert.Equal(t, ErrInvalidStateToSave, err)
	})
	t.Run("Create:InsertionFailed", func(t *testing.T) {
		t.Parallel()
		db, mock, _ := sqlmock.New()
		expectedErr := errors.New("Insertion failed")
		mock.ExpectBegin()
		mock.ExpectExec("INSERT INTO").WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).WillReturnError(expectedErr)
		mock.ExpectRollback()
		mock.MatchExpectationsInOrder(true)
		repo := &ChannelDBRepository{db: db}
		channel, _ := data.NewChannel(successfulInsertTestChannelID, successfulGetTestToken)
		_, err := repo.Store(channel)
		assert.Equal(t, expectedErr, err)
		assert.Nil(t, mock.ExpectationsWereMet())
	})
	t.Run("Create:Success", func(t *testing.T) {
		t.Parallel()
		channel, _ := data.NewChannel(successfulInsertTestChannelID, successfulGetTestToken)
		repo := getChannelRepo()
		_, err := repo.Store(channel)
		assert.Nil(t, err)
		newChannel, err := repo.Get(successfulInsertTestChannelID)
		assert.Nil(t, err)
		assert.True(t, newChannel.IsInValidState())
		assert.Equal(t, channel.ID, newChannel.ID)
		assert.Equal(t, channel.Name, newChannel.Name)
		assert.Equal(t, channel.ChannelID, newChannel.ChannelID)
		assert.Equal(t, channel.Token, newChannel.Token)
	})
	t.Run("Update:NothingToChange", func(t *testing.T) {
		t.Parallel()
		channel, _ := data.NewChannel(noChangeUpdateTestChannelID, successfulGetTestToken)
		repo := getChannelRepo()
		_, err := repo.Store(channel)
		assert.Nil(t, err)
		failedUpdate, err := repo.Store(channel)
		assert.Nil(t, err)
		assert.True(t, channel.CreatedAt.Equal(failedUpdate.CreatedAt))
		assert.True(t, channel.UpdatedAt.Equal(failedUpdate.UpdatedAt))
	})
	t.Run("Update:InvalidState", func(t *testing.T) {
		t.Parallel()
		channel, _ := data.NewChannel(invalidStateUpdateTestChannelID, successfulGetTestToken)
		repo := getChannelRepo()
		_, err := repo.Store(channel)
		assert.Nil(t, err)
		channel.Token = ""
		_, err = repo.Store(channel)
		assert.NotNil(t, err)
		assert.Equal(t, ErrInvalidStateToSave, err)
	})
	t.Run("Update:UpdateFailed", func(t *testing.T) {
		t.Parallel()
		db, mock, _ := sqlmock.New()
		expectedErr := errors.New("Update failed")
		channel, _ := data.NewChannel(dbErrUpdateTestChannelID, successfulGetTestToken)
		channel.QuickFix()
		rows := sqlmock.NewRows([]string{"id", "channelId", "name", "token", "createdAt", "updatedAt"}).AddRow(channel.ID, channel.ChannelID, channel.Name, channel.Token, channel.CreatedAt, channel.UpdatedAt)
		mock.ExpectQuery("SELECT id, channelId, name, token, createdAt, updatedAt FROM channel WHERE channelId like").WithArgs(dbErrUpdateTestChannelID).WillReturnRows(rows).WillReturnError(nil)
		mock.ExpectBegin()
		mock.ExpectExec("UPDATE channel").WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), dbErrUpdateTestChannelID).WillReturnError(expectedErr)
		mock.ExpectRollback()
		mock.MatchExpectationsInOrder(true)
		repo := &ChannelDBRepository{db: db}
		channel.Token = "c"
		_, err := repo.Store(channel)
		assert.Equal(t, expectedErr, err)
		assert.Nil(t, mock.ExpectationsWereMet())
	})
	t.Run("Update:NoRowChanged", func(t *testing.T) {
		t.Parallel()
		// uses mock
		db, mock, _ := sqlmock.New()
		channel, _ := data.NewChannel(dbErrUpdateTestChannelID, successfulGetTestToken)
		channel.QuickFix()
		rows := sqlmock.NewRows([]string{"id", "channelId", "name", "token", "createdAt", "updatedAt"}).AddRow(channel.ID, channel.ChannelID, channel.Name, channel.Token, channel.CreatedAt, channel.UpdatedAt)
		result := sqlmock.NewResult(1, 0)
		mock.ExpectQuery("SELECT id, channelId, name, token, createdAt, updatedAt FROM channel WHERE channelId like").WithArgs(dbErrUpdateTestChannelID).WillReturnRows(rows).WillReturnError(nil)
		mock.ExpectBegin()
		mock.ExpectExec("UPDATE channel").WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), dbErrUpdateTestChannelID).WillReturnResult(result).WillReturnError(nil)
		mock.ExpectRollback()
		mock.MatchExpectationsInOrder(true)
		repo := &ChannelDBRepository{db: db}
		channel.Token = "c"
		_, err := repo.Store(channel)
		assert.Equal(t, ErrNoRowsUpdated, err)
		assert.Nil(t, mock.ExpectationsWereMet())
	})
	t.Run("Update:Success", func(t *testing.T) {
		t.Parallel()
		channel, _ := data.NewChannel(successfulUpdateTestChannelID, "oldtoken")
		repo := getChannelRepo()
		repo.Store(channel)
		channel.Token = successfulGetTestToken
		updatedChannel, err := repo.Store(channel)
		assert.Nil(t, err)
		assert.Equal(t, successfulGetTestToken, updatedChannel.Token)
		assert.True(t, channel.UpdatedAt.Before(updatedChannel.UpdatedAt))
	})
}

func TestNewChannelRepository(t *testing.T) {
	defer dbPanicDeferAssert(t)
	NewChannelRepository(nil)
}

func TestChannelGetList(t *testing.T) {
	repo := getChannelRepo()
	for index := 99; index > -1; index = index - 1 {
		indexString := strconv.Itoa(index)
		channel, err := data.NewChannel(listTestChannelIDPrefix+indexString, successfulGetTestToken+" - "+indexString)
		assert.Nil(t, err)
		_, err = repo.Store(channel)
		assert.Nil(t, err)
	}
	t.Run("PaginationDeadlock", func(t *testing.T) {
		t.Parallel()
		channel1, _ := data.NewChannel(dbErrUpdateTestChannelID, successfulGetTestToken)
		channel2, _ := data.NewChannel(successfulGetTestChannelID, successfulGetTestToken)
		_, _, err := repo.GetList(data.NewPagination(channel1, channel2))
		assert.Equal(t, ErrPaginationDeadlock, err)
		_, _, err = repo.GetList(nil)
		assert.Equal(t, ErrPaginationDeadlock, err)
	})
	t.Run("QueryError", func(t *testing.T) {
		t.Parallel()
		db, mock, _ := sqlmock.New()
		expectedErr := errors.New("DB Query Error")
		mock.ExpectQuery("SELECT id, channelId, name, token, createdAt, updatedAt FROM channel").WillReturnError(expectedErr)
		mock.MatchExpectationsInOrder(true)
		repo := &ChannelDBRepository{db: db}
		_, _, err := repo.GetList(data.NewPagination(nil, nil))
		assert.Equal(t, expectedErr, err)
		assert.Nil(t, mock.ExpectationsWereMet())
	})
	t.Run("Root", func(t *testing.T) {
		t.Parallel()
		channels, page, err := repo.GetList(data.NewPagination(nil, nil))
		assert.Nil(t, err)
		assert.NotNil(t, channels)
		assert.NotNil(t, page)
		assert.Equal(t, 25, len(channels))
		for _, channel := range channels {
			assert.False(t, channel.ID.IsNil())
			assert.Contains(t, channel.ChannelID, listTestChannelIDPrefix)
			assert.Contains(t, channel.Name, listTestChannelIDPrefix)
			assert.Contains(t, channel.Token, successfulGetTestToken)
		}
		assert.NotNil(t, page.Next)
		assert.NotNil(t, page.Previous)
	})
	t.Run("Previous", func(t *testing.T) {
		t.Parallel()
		firstList, page, _ := repo.GetList(data.NewPagination(nil, nil))
		_, sPage, _ := repo.GetList(&data.Pagination{Next: page.Next})
		lastList, _, _ := repo.GetList(&data.Pagination{Previous: sPage.Previous})
		for _, firstListChannel := range firstList {
			found := false
			for _, secondListChannel := range lastList {
				if firstListChannel.ID == secondListChannel.ID {
					found = true
				}
			}
			if !found {
				t.Log(firstListChannel.ChannelID)
			}
			assert.True(t, found)
		}
	})
	t.Run("Next", func(t *testing.T) {
		t.Parallel()
		firstList, page, _ := repo.GetList(data.NewPagination(nil, nil))
		lastList, _, _ := repo.GetList(&data.Pagination{Next: page.Next})
		for _, firstListChannel := range firstList {
			found := false
			for _, secondListChannel := range lastList {
				if firstListChannel.ID == secondListChannel.ID {
					found = true
				}
			}
			if found {
				t.Log(firstListChannel.ChannelID)
			}
			assert.False(t, found)
		}
	})
	t.Run("TestReadThrough", func(t *testing.T) {
		t.Parallel()
		firstList, page, err := repo.GetList(data.NewPagination(nil, nil))
		assert.Nil(t, err)
		result := make([]*data.Channel, 0)
		result = append(result, firstList...)
		for page.Next != nil {
			var channels []*data.Channel
			channels, page, _ = repo.GetList(&data.Pagination{Next: page.Next})
			result = append(result, channels...)
		}
		assert.GreaterOrEqual(t, len(result), 100)
		count := 0
		for _, channel := range result {
			if strings.Contains(channel.ChannelID, listTestChannelIDPrefix) {
				count = count + 1
			}
		}
		assert.Equal(t, 100, count)
	})
}
