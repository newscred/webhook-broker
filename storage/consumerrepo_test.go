package storage

import (
	"errors"
	"log"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/imyousuf/webhook-broker/storage/data"
	"github.com/stretchr/testify/assert"
)

const (
	successfulGetTestConsumerID      = "get-test"
	nonExistingGetTestConsumerID     = "get-test-ne"
	successfulInsertTestConsumerID   = "s-insert-test"
	invalidStateUpdateTestConsumerID = "i-update-test"
	successfulUpdateTestConsumerID   = "s-update-test"
	dbErrUpdateTestConsumerID        = "db-update-test"
	noChangeUpdateTestConsumerID     = "nc-update-test"
	successfulDeleteTestConsumerID   = "s-delete-test"
	failedDeleteTestConsumerID       = "s-delete-ne-test"
	listTestConsumerIDPrefix         = "get-list-"
)

var (
	channel1, channel2       *data.Channel
	callbackURL, relativeURL *url.URL
)

func getConsumerRepo() ConsumerRepository {
	channelRepo := NewChannelRepository(testDB)
	consumerRepo := NewConsumerRepository(testDB, channelRepo)
	return consumerRepo
}

func SetupForConsumerTests() {
	channelRepo := NewChannelRepository(testDB)
	channel1 = createTestChannel("channel1-for-consumer", "sampletoken", channelRepo)
	channel2 = createTestChannel("channel2-for-consumer", "sampletoken", channelRepo)
	callbackURL = parseTestURL("https://imytech.net/")
	relativeURL = parseTestURL("./test/")
}

func parseTestURL(urlString string) *url.URL {
	url, err := url.Parse(urlString)
	if err != nil {
		log.Fatalln(err)
	}
	return url
}

func createTestChannel(channelID, token string, channelRepo ChannelRepository) *data.Channel {
	channel, err := data.NewChannel(channelID, token)
	if channel, err = channelRepo.Store(channel); err != nil {
		log.Fatalln(err)
	}
	return channel
}

func TestConsumerGet(t *testing.T) {
	t.Run("GetExisting", func(t *testing.T) {
		t.Parallel()
		repo := getConsumerRepo()
		sampleConsumer, err := data.NewConsumer(channel1, successfulGetTestConsumerID, successfulGetTestToken, callbackURL)
		assert.Nil(t, err)
		assert.True(t, sampleConsumer.IsInValidState())
		resultConsumer, err := repo.Store(sampleConsumer)
		assert.Nil(t, err)
		assert.False(t, resultConsumer.ID.IsNil())
		consumer, err := repo.Get(channel1.ChannelID, successfulGetTestConsumerID)
		assert.Nil(t, err)
		assert.False(t, consumer.ID.IsNil())
		assert.Equal(t, sampleConsumer.ID, consumer.ID)
		assert.Equal(t, successfulGetTestConsumerID, consumer.ConsumerID)
		assert.Equal(t, successfulGetTestConsumerID, consumer.Name)
		assert.Equal(t, successfulGetTestToken, consumer.Token)
		assert.False(t, consumer.CreatedAt.IsZero())
		assert.False(t, consumer.UpdatedAt.IsZero())
		assert.True(t, consumer.CreatedAt.Before(time.Now()))
		assert.True(t, consumer.UpdatedAt.Before(time.Now()))
	})
	t.Run("GetMissing", func(t *testing.T) {
		t.Parallel()
		repo := getConsumerRepo()
		_, err := repo.Get(channel1.ChannelID, nonExistingGetTestConsumerID)
		assert.NotNil(t, err)
	})
}

func TestConsumerDelete(t *testing.T) {
	t.Run("DeleteExisting", func(t *testing.T) {
		t.Parallel()
		repo := getConsumerRepo()
		sampleConsumer, err := data.NewConsumer(channel1, successfulDeleteTestConsumerID, successfulGetTestToken, callbackURL)
		assert.Nil(t, err)
		assert.True(t, sampleConsumer.IsInValidState())
		resultConsumer, err := repo.Store(sampleConsumer)
		assert.Nil(t, err)
		err = repo.Delete(resultConsumer)
		assert.Nil(t, err)

	})
	t.Run("DeleteMissing", func(t *testing.T) {
		t.Parallel()
		repo := getConsumerRepo()
		sampleConsumer, err := data.NewConsumer(channel1, failedDeleteTestConsumerID, successfulGetTestToken, callbackURL)
		assert.Nil(t, err)
		sampleConsumer.QuickFix()
		err = repo.Delete(sampleConsumer)
		assert.NotNil(t, err)
	})
}

func TestConsumerStore(t *testing.T) {
	t.Run("Create/Update:NilChannel", func(t *testing.T) {
		repo := getConsumerRepo()
		sampleConsumer, err := data.NewConsumer(channel1, failedDeleteTestConsumerID, successfulGetTestToken, callbackURL)
		assert.Nil(t, err)
		sampleConsumer.ConsumingFrom = nil
		sampleConsumer.QuickFix()
		_, err = repo.Store(sampleConsumer)
		assert.NotNil(t, err)
	})
	t.Run("Create/Update:NonExistingChannel", func(t *testing.T) {
		repo := getConsumerRepo()
		sampleChannel, err := data.NewChannel(nonExistingGetTestChannelID, successfulGetTestToken)
		assert.Nil(t, err)
		sampleChannel.QuickFix()
		sampleConsumer, err := data.NewConsumer(sampleChannel, failedDeleteTestConsumerID, successfulGetTestToken, callbackURL)
		assert.Nil(t, err)
		errConsumer, err := repo.Store(sampleConsumer)
		assert.NotNil(t, err)
		assert.NotNil(t, errConsumer)
	})
	t.Run("Create:InvalidState", func(t *testing.T) {
		t.Parallel()
		consumer, err := data.NewConsumer(channel1, successfulInsertTestConsumerID, successfulGetTestToken, callbackURL)
		assert.Nil(t, err)
		assert.True(t, consumer.IsInValidState())
		consumer.Token = ""
		assert.False(t, consumer.IsInValidState())
		repo := getConsumerRepo()
		_, err = repo.Store(consumer)
		assert.Equal(t, ErrInvalidStateToSave, err)
	})
	t.Run("Create:InsertionFailed", func(t *testing.T) {
		t.Parallel()
		db, mock, _ := sqlmock.New()
		expectedErr := errors.New("Insertion failed")
		mockChannelRepo := new(MockChannelRepository)
		mockChannelRepo.On("Get", channel1.ChannelID).Return(channel1, nil)
		mock.ExpectBegin()
		mock.ExpectExec("INSERT INTO").WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).WillReturnError(expectedErr)
		mock.ExpectRollback()
		mock.MatchExpectationsInOrder(true)
		repo := &ConsumerDBRepository{db: db, channelRepository: mockChannelRepo}
		consumer, err := data.NewConsumer(channel1, successfulInsertTestConsumerID, successfulGetTestToken, callbackURL)
		assert.Nil(t, err)
		assert.Equal(t, channel1.ChannelID, consumer.GetChannelIDSafely())
		t.Log(consumer.GetChannelIDSafely())
		_, err = repo.Store(consumer)
		mockChannelRepo.AssertExpectations(t)
		assert.Equal(t, expectedErr, err)
		assert.Nil(t, mock.ExpectationsWereMet())
	})
	t.Run("Create:Success", func(t *testing.T) {
		t.Parallel()
		consumer, _ := data.NewConsumer(channel1, successfulInsertTestConsumerID, successfulGetTestToken, callbackURL)
		repo := getConsumerRepo()
		_, err := repo.Store(consumer)
		assert.Nil(t, err)
		newConsumer, err := repo.Get(channel1.ChannelID, successfulInsertTestConsumerID)
		assert.Nil(t, err)
		assert.True(t, newConsumer.IsInValidState())
		assert.Equal(t, consumer.ID, newConsumer.ID)
		assert.Equal(t, consumer.Name, newConsumer.Name)
		assert.Equal(t, consumer.ConsumerID, newConsumer.ConsumerID)
		assert.Equal(t, consumer.Token, newConsumer.Token)
	})
	t.Run("Update:NothingToChange", func(t *testing.T) {
		t.Parallel()
		consumer, _ := data.NewConsumer(channel2, noChangeUpdateTestConsumerID, successfulGetTestToken, callbackURL)
		repo := getConsumerRepo()
		_, err := repo.Store(consumer)
		assert.Nil(t, err)
		failedUpdate, err := repo.Store(consumer)
		assert.Nil(t, err)
		assert.True(t, consumer.CreatedAt.Equal(failedUpdate.CreatedAt))
		assert.True(t, consumer.UpdatedAt.Equal(failedUpdate.UpdatedAt))
	})
	t.Run("Update:InvalidState", func(t *testing.T) {
		t.Parallel()
		consumer, _ := data.NewConsumer(channel2, invalidStateUpdateTestConsumerID, successfulGetTestToken, callbackURL)
		repo := getConsumerRepo()
		_, err := repo.Store(consumer)
		assert.Nil(t, err)
		consumer.Token = ""
		_, err = repo.Store(consumer)
		assert.NotNil(t, err)
		assert.Equal(t, ErrInvalidStateToSave, err)
	})
	t.Run("Update:UpdateFailed", func(t *testing.T) {
		t.Parallel()
		db, mock, _ := sqlmock.New()
		expectedErr := errors.New("Update failed")
		mockChannelRepo := new(MockChannelRepository)
		mockChannelRepo.On("Get", channel2.ChannelID).Return(channel2, nil)
		consumer, _ := data.NewConsumer(channel2, dbErrUpdateTestConsumerID, successfulGetTestToken, callbackURL)
		consumer.QuickFix()
		rows := sqlmock.NewRows([]string{"id", "consumerId", "name", "token", "callbackUrl", "createdAt", "updatedAt"}).AddRow(consumer.ID, consumer.ConsumerID, consumer.Name, consumer.Token, consumer.CallbackURL, consumer.CreatedAt, consumer.UpdatedAt)
		mock.ExpectQuery("SELECT id, consumerId, name, token, callbackUrl, createdAt, updatedAt FROM consumer WHERE channelId like").WithArgs(channel2.ChannelID, dbErrUpdateTestConsumerID).WillReturnRows(rows).WillReturnError(nil)
		mock.ExpectBegin()
		mock.ExpectExec("UPDATE consumer").WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), dbErrUpdateTestConsumerID, channel2.ChannelID).WillReturnError(expectedErr)
		mock.ExpectRollback()
		mock.MatchExpectationsInOrder(true)
		repo := &ConsumerDBRepository{db: db, channelRepository: mockChannelRepo}
		consumer.Token = "c"
		_, err := repo.Store(consumer)
		assert.Equal(t, expectedErr, err)
		assert.Nil(t, mock.ExpectationsWereMet())
	})
	t.Run("Update:NoRowChanged", func(t *testing.T) {
		t.Parallel()
		// uses mock
		db, mock, _ := sqlmock.New()
		consumer, _ := data.NewConsumer(channel2, dbErrUpdateTestConsumerID, successfulGetTestToken, callbackURL)
		consumer.QuickFix()
		mockChannelRepo := new(MockChannelRepository)
		mockChannelRepo.On("Get", channel2.ChannelID).Return(channel2, nil)
		rows := sqlmock.NewRows([]string{"id", "consumerId", "name", "token", "callbackUrl", "createdAt", "updatedAt"}).AddRow(consumer.ID, consumer.ConsumerID, consumer.Name, consumer.Token, consumer.CallbackURL, consumer.CreatedAt, consumer.UpdatedAt)
		mock.ExpectQuery("SELECT id, consumerId, name, token, callbackUrl, createdAt, updatedAt FROM consumer WHERE channelId like").WithArgs(channel2.ChannelID, dbErrUpdateTestConsumerID).WillReturnRows(rows).WillReturnError(nil)
		result := sqlmock.NewResult(1, 0)
		mock.ExpectBegin()
		mock.ExpectExec("UPDATE consumer").WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), dbErrUpdateTestConsumerID, channel2.ChannelID).WillReturnResult(result).WillReturnError(nil)
		mock.ExpectCommit()
		mock.MatchExpectationsInOrder(true)
		repo := &ConsumerDBRepository{db: db, channelRepository: mockChannelRepo}
		consumer.Token = "c"
		_, err := repo.Store(consumer)
		assert.Equal(t, ErrNoRowsUpdated, err)
		assert.Nil(t, mock.ExpectationsWereMet())
	})
	t.Run("Update:Success", func(t *testing.T) {
		t.Parallel()
		consumer, _ := data.NewConsumer(channel1, successfulUpdateTestConsumerID, "oldtoken", callbackURL)
		repo := getConsumerRepo()
		repo.Store(consumer)
		consumer.Token = successfulGetTestToken
		updatedConsumer, err := repo.Store(consumer)
		assert.Nil(t, err)
		assert.Equal(t, successfulGetTestToken, updatedConsumer.Token)
		assert.True(t, consumer.UpdatedAt.Before(updatedConsumer.UpdatedAt))
	})
}

func TestNewConsumerRepository(t *testing.T) {
	defer dbPanicDeferAssert(t)
	NewConsumerRepository(nil, nil)
}

func TestConsumerGetList(t *testing.T) {
	repo := getConsumerRepo()
	for index := 99; index > -1; index = index - 1 {
		indexString := strconv.Itoa(index)
		consumer, err := data.NewConsumer(channel2, listTestConsumerIDPrefix+indexString, successfulGetTestToken+" - "+indexString, callbackURL)
		assert.Nil(t, err)
		_, err = repo.Store(consumer)
		assert.Nil(t, err)
	}
	t.Run("PaginationDeadlock", func(t *testing.T) {
		t.Parallel()
		consumer1, _ := data.NewConsumer(channel2, dbErrUpdateTestConsumerID, successfulGetTestToken, callbackURL)
		consumer2, _ := data.NewConsumer(channel2, successfulGetTestConsumerID, successfulGetTestToken, callbackURL)
		_, _, err := repo.GetList(channel2.ChannelID, data.NewPagination(consumer1, consumer2))
		assert.Equal(t, ErrPaginationDeadlock, err)
		_, _, err = repo.GetList(channel2.ChannelID, nil)
		assert.Equal(t, ErrPaginationDeadlock, err)
	})
	t.Run("QueryError", func(t *testing.T) {
		t.Parallel()
		mockChannelRepo := new(MockChannelRepository)
		mockChannelRepo.On("Get", channel2.ChannelID).Return(channel2, nil)
		db, mock, _ := sqlmock.New()
		expectedErr := errors.New("DB Query Error")
		mock.ExpectQuery("SELECT id, consumerId, name, token, callbackUrl, createdAt, updatedAt FROM consumer").WillReturnError(expectedErr)
		mock.MatchExpectationsInOrder(true)
		repo := &ConsumerDBRepository{db: db, channelRepository: mockChannelRepo}
		_, _, err := repo.GetList(channel2.ChannelID, data.NewPagination(nil, nil))
		assert.Equal(t, expectedErr, err)
		assert.Nil(t, mock.ExpectationsWereMet())
	})
	t.Run("Root", func(t *testing.T) {
		t.Parallel()
		consumers, page, err := repo.GetList(channel2.ChannelID, data.NewPagination(nil, nil))
		assert.Nil(t, err)
		assert.NotNil(t, consumers)
		assert.NotNil(t, page)
		assert.Equal(t, 25, len(consumers))
		for _, consumer := range consumers {
			assert.False(t, consumer.ID.IsNil())
			assert.Contains(t, consumer.ConsumerID, listTestConsumerIDPrefix)
			assert.Contains(t, consumer.Name, listTestConsumerIDPrefix)
			assert.Contains(t, consumer.Token, successfulGetTestToken)
		}
		assert.NotNil(t, page.Next)
		assert.NotNil(t, page.Previous)
	})
	t.Run("Previous", func(t *testing.T) {
		t.Parallel()
		firstList, page, _ := repo.GetList(channel2.ChannelID, data.NewPagination(nil, nil))
		_, sPage, _ := repo.GetList(channel2.ChannelID, &data.Pagination{Next: page.Next})
		lastList, _, _ := repo.GetList(channel2.ChannelID, &data.Pagination{Previous: sPage.Previous})
		for _, firstListConsumer := range firstList {
			found := false
			for _, secondListConsumer := range lastList {
				if firstListConsumer.ID == secondListConsumer.ID {
					found = true
				}
			}
			if !found {
				t.Log(firstListConsumer.ConsumerID)
			}
			assert.True(t, found)
		}
	})
	t.Run("Next", func(t *testing.T) {
		t.Parallel()
		firstList, page, _ := repo.GetList(channel2.ChannelID, data.NewPagination(nil, nil))
		lastList, _, _ := repo.GetList(channel2.ChannelID, &data.Pagination{Next: page.Next})
		for _, firstListConsumer := range firstList {
			found := false
			for _, secondListConsumer := range lastList {
				if firstListConsumer.ID == secondListConsumer.ID {
					found = true
				}
			}
			if found {
				t.Log(firstListConsumer.ConsumerID)
			}
			assert.False(t, found)
		}
	})
	t.Run("TestReadThrough", func(t *testing.T) {
		t.Parallel()
		firstList, page, err := repo.GetList(channel2.ChannelID, data.NewPagination(nil, nil))
		assert.Nil(t, err)
		result := make([]*data.Consumer, 0)
		result = append(result, firstList...)
		for page.Next != nil {
			var consumers []*data.Consumer
			consumers, page, _ = repo.GetList(channel2.ChannelID, &data.Pagination{Next: page.Next})
			result = append(result, consumers...)
		}
		assert.GreaterOrEqual(t, len(result), 100)
		count := 0
		for _, consumer := range result {
			if strings.Contains(consumer.ConsumerID, listTestConsumerIDPrefix) {
				count = count + 1
			}
		}
		assert.Equal(t, 100, count)
	})
}
