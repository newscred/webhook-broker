package storage

import (
	"errors"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/newscred/webhook-broker/storage/data"
	"github.com/stretchr/testify/assert"
)

const (
	successfulGetTestConsumerID      = "get-test"
	successfulGetByIDTestConsumerID  = "get-test-by-ID"
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
	channel1, channel2, channel3 *data.Channel // Used by messagerepo_test as well
	relativeURL                  *url.URL
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
	channel3 = createTestChannel("channel3-for-no-consumers", "sampletoken", channelRepo)
	if callbackURL == nil {
		callbackURL = parseTestURL("https://imytech.net/")
	}
	relativeURL = parseTestURL("./test/")
}

func parseTestURL(urlString string) *url.URL {
	url, err := url.Parse(urlString)
	if err != nil {
		log.Fatal().Err(err)
	}
	return url
}

func createTestChannel(channelID, token string, channelRepo ChannelRepository) *data.Channel {
	channel, err := data.NewChannel(channelID, token)
	if channel, err = channelRepo.Store(channel); err != nil {
		log.Fatal().Err(err)
	}
	return channel
}

func TestConsumerGet(t *testing.T) {
	t.Run("GetExistingPushConsumer", func(t *testing.T) {
		t.Parallel()
		repo := getConsumerRepo()
		sampleConsumer, err := data.NewConsumer(channel1, successfulGetTestConsumerID, successfulGetTestToken, callbackURL, data.PushConsumerStr)
		assert.Nil(t, err)
		assert.True(t, sampleConsumer.IsInValidState())
		resultConsumer, err := repo.Store(sampleConsumer)
		assert.Nil(t, err)
		assert.False(t, resultConsumer.ID.IsNil())
		consumer, err := repo.Get(channel1.ChannelID, successfulGetTestConsumerID)
		assert.Nil(t, err)
		assert.False(t, consumer.ID.IsNil())
		assert.Equal(t, resultConsumer.ID, consumer.ID)
		assert.Equal(t, successfulGetTestConsumerID, consumer.ConsumerID)
		assert.Equal(t, successfulGetTestConsumerID, consumer.Name)
		assert.Equal(t, successfulGetTestToken, consumer.Token)
		assert.Equal(t, data.PushConsumer, consumer.Type)
		assert.False(t, consumer.CreatedAt.IsZero())
		assert.False(t, consumer.UpdatedAt.IsZero())
		assert.True(t, consumer.CreatedAt.Before(time.Now()))
		assert.True(t, consumer.UpdatedAt.Before(time.Now()))
	})
	t.Run("GetExistingPullConsumer", func(t *testing.T) {
		t.Parallel()
		consumerID := successfulDeleteTestConsumerID + "-pull"
		repo := getConsumerRepo()
		sampleConsumer, err := data.NewConsumer(channel1, consumerID, successfulGetTestToken, callbackURL, data.PullConsumerStr)
		assert.Nil(t, err)
		assert.True(t, sampleConsumer.IsInValidState())
		resultConsumer, err := repo.Store(sampleConsumer)
		assert.Nil(t, err)
		assert.False(t, resultConsumer.ID.IsNil())
		consumer, err := repo.Get(channel1.ChannelID, consumerID)
		assert.Nil(t, err)
		assert.False(t, consumer.ID.IsNil())
		assert.Equal(t, resultConsumer.ID, consumer.ID)
		assert.Equal(t, consumerID, consumer.ConsumerID)
		assert.Equal(t, consumerID, consumer.Name)
		assert.Equal(t, successfulGetTestToken, consumer.Token)
		assert.Equal(t, data.PullConsumer, consumer.Type)
		assert.False(t, consumer.CreatedAt.IsZero())
		assert.False(t, consumer.UpdatedAt.IsZero())
		assert.True(t, consumer.CreatedAt.Before(time.Now()))
		assert.True(t, consumer.UpdatedAt.Before(time.Now()))
	})
	t.Run("GetMissing", func(t *testing.T) {
		t.Parallel()
		repo := getConsumerRepo()
		consumer, err := repo.Get(channel1.ChannelID, nonExistingGetTestConsumerID)
		assert.NotNil(t, err)
		assert.NotNil(t, consumer)
		assert.NotNil(t, consumer.ConsumingFrom)
	})
}

func TestConsumerGetByID(t *testing.T) {
	t.Run("GetExistingPushConsumer", func(t *testing.T) {
		t.Parallel()
		repo := getConsumerRepo()
		sampleConsumer, err := data.NewConsumer(channel1, successfulGetByIDTestConsumerID, successfulGetTestToken, callbackURL, data.PushConsumerStr)
		assert.Nil(t, err)
		assert.True(t, sampleConsumer.IsInValidState())
		resultConsumer, err := repo.Store(sampleConsumer)
		assert.Nil(t, err)
		assert.False(t, resultConsumer.ID.IsNil())
		consumer, err := repo.GetByID(resultConsumer.ID.String())
		assert.Nil(t, err)
		assert.False(t, consumer.ID.IsNil())
		assert.Equal(t, resultConsumer.ID, consumer.ID)
		assert.Equal(t, successfulGetByIDTestConsumerID, consumer.ConsumerID)
		assert.Equal(t, successfulGetByIDTestConsumerID, consumer.Name)
		assert.Equal(t, successfulGetTestToken, consumer.Token)
		assert.Equal(t, data.PushConsumer, consumer.Type)
		assert.False(t, consumer.CreatedAt.IsZero())
		assert.False(t, consumer.UpdatedAt.IsZero())
		assert.True(t, consumer.CreatedAt.Before(time.Now()))
		assert.True(t, consumer.UpdatedAt.Before(time.Now()))
	})
	t.Run("GetExistingPullConsumer", func(t *testing.T) {
		t.Parallel()
		repo := getConsumerRepo()
		consumerID := successfulGetByIDTestConsumerID + "-pull"
		sampleConsumer, err := data.NewConsumer(channel1, consumerID, successfulGetTestToken, callbackURL, data.PullConsumerStr)
		assert.Nil(t, err)
		assert.True(t, sampleConsumer.IsInValidState())
		resultConsumer, err := repo.Store(sampleConsumer)
		assert.Nil(t, err)
		assert.False(t, resultConsumer.ID.IsNil())
		consumer, err := repo.GetByID(resultConsumer.ID.String())
		assert.Nil(t, err)
		assert.False(t, consumer.ID.IsNil())
		assert.Equal(t, resultConsumer.ID, consumer.ID)
		assert.Equal(t, consumerID, consumer.ConsumerID)
		assert.Equal(t, consumerID, consumer.Name)
		assert.Equal(t, successfulGetTestToken, consumer.Token)
		assert.Equal(t, data.PullConsumer, consumer.Type)
		assert.False(t, consumer.CreatedAt.IsZero())
		assert.False(t, consumer.UpdatedAt.IsZero())
		assert.True(t, consumer.CreatedAt.Before(time.Now()))
		assert.True(t, consumer.UpdatedAt.Before(time.Now()))
	})
	t.Run("GetByIDMissing", func(t *testing.T) {
		t.Parallel()
		repo := getConsumerRepo()
		_, err := repo.GetByID(nonExistingGetTestConsumerID)
		assert.NotNil(t, err)
	})
}

func TestConsumerDelete(t *testing.T) {
	t.Run("DeleteExistingPullConsumer", func(t *testing.T) {
		t.Parallel()
		repo := getConsumerRepo()
		sampleConsumer, err := data.NewConsumer(channel1, successfulDeleteTestConsumerID, successfulGetTestToken, callbackURL, data.PushConsumerStr)
		assert.Nil(t, err)
		assert.True(t, sampleConsumer.IsInValidState())
		resultConsumer, err := repo.Store(sampleConsumer)
		assert.Nil(t, err)
		err = repo.Delete(resultConsumer)
		assert.Nil(t, err)

	})
	t.Run("DeleteExistingPushConsum", func(t *testing.T) {
		t.Parallel()
		repo := getConsumerRepo()
		consumerID := successfulDeleteTestConsumerID + "-pull"
		sampleConsumer, err := data.NewConsumer(channel1, consumerID, successfulGetTestToken, callbackURL, data.PullConsumerStr)
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
		sampleConsumer, err := data.NewConsumer(channel1, failedDeleteTestConsumerID, successfulGetTestToken, callbackURL, data.PushConsumerStr)
		assert.Nil(t, err)
		sampleConsumer.QuickFix()
		err = repo.Delete(sampleConsumer)
		assert.NotNil(t, err)
	})
}

func TestConsumerStore(t *testing.T) {
	t.Run("Create/Update:NilChannel", func(t *testing.T) {
		repo := getConsumerRepo()
		sampleConsumer, err := data.NewConsumer(channel1, failedDeleteTestConsumerID, successfulGetTestToken, callbackURL, data.PushConsumerStr)
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
		sampleConsumer, err := data.NewConsumer(sampleChannel, failedDeleteTestConsumerID, successfulGetTestToken, callbackURL, data.PushConsumerStr)
		assert.Nil(t, err)
		errConsumer, err := repo.Store(sampleConsumer)
		assert.NotNil(t, err)
		assert.NotNil(t, errConsumer)
	})
	t.Run("Create:InvalidState", func(t *testing.T) {
		t.Parallel()
		consumer, err := data.NewConsumer(channel1, successfulInsertTestConsumerID, successfulGetTestToken, callbackURL, data.PushConsumerStr)
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
		mock.ExpectExec("INSERT INTO").WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).WillReturnError(expectedErr)
		mock.ExpectRollback()
		mock.MatchExpectationsInOrder(true)
		repo := &ConsumerDBRepository{db: db, channelRepository: mockChannelRepo}
		consumer, err := data.NewConsumer(channel1, successfulInsertTestConsumerID, successfulGetTestToken, callbackURL, data.PushConsumerStr)
		assert.Nil(t, err)
		assert.Equal(t, channel1.ChannelID, consumer.GetChannelIDSafely())
		t.Log(consumer.GetChannelIDSafely())
		_, err = repo.Store(consumer)
		mockChannelRepo.AssertExpectations(t)
		assert.Equal(t, expectedErr, err)
		assert.Nil(t, mock.ExpectationsWereMet())
	})
	t.Run("Create:SuccessPushConsumer", func(t *testing.T) {
		t.Parallel()
		consumer, _ := data.NewConsumer(channel1, successfulInsertTestConsumerID, successfulGetTestToken, callbackURL, data.PushConsumerStr)
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
		assert.Equal(t, data.PushConsumer, newConsumer.Type)
	})
	t.Run("Create:SuccessPullConsumer", func(t *testing.T) {
		t.Parallel()
		consumerID := successfulInsertTestChannelID + "-pull"
		consumer, _ := data.NewConsumer(channel1, consumerID, successfulGetTestToken, callbackURL, data.PullConsumerStr)
		repo := getConsumerRepo()
		_, err := repo.Store(consumer)
		assert.Nil(t, err)
		newConsumer, err := repo.Get(channel1.ChannelID, consumerID)
		assert.Nil(t, err)
		assert.True(t, newConsumer.IsInValidState())
		assert.Equal(t, consumer.ID, newConsumer.ID)
		assert.Equal(t, consumer.Name, newConsumer.Name)
		assert.Equal(t, consumer.ConsumerID, newConsumer.ConsumerID)
		assert.Equal(t, consumer.Token, newConsumer.Token)
		assert.Equal(t, data.PullConsumer, newConsumer.Type)
	})
	t.Run("Update:NothingToChange", func(t *testing.T) {
		t.Parallel()
		consumer, _ := data.NewConsumer(channel2, noChangeUpdateTestConsumerID, successfulGetTestToken, callbackURL, data.PushConsumerStr)
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
		consumer, _ := data.NewConsumer(channel2, invalidStateUpdateTestConsumerID, successfulGetTestToken, callbackURL, data.PushConsumerStr)
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
		consumer, _ := data.NewConsumer(channel2, dbErrUpdateTestConsumerID, successfulGetTestToken, callbackURL, data.PushConsumerStr)
		consumer.QuickFix()
		rows := sqlmock.NewRows([]string{"id", "consumerId", "channelId", "name", "token", "type", "callbackUrl", "createdAt", "updatedAt"}).AddRow(consumer.ID, consumer.ConsumerID, channel2.ChannelID, consumer.Name, consumer.Token, consumer.CallbackURL, consumer.Type, consumer.CreatedAt, consumer.UpdatedAt)
		mock.ExpectQuery(consumerSelectRowCommonQuery+" channelId like").WithArgs(channel2.ChannelID, dbErrUpdateTestConsumerID).WillReturnRows(rows).WillReturnError(nil)
		mock.ExpectBegin()
		mock.ExpectExec("UPDATE consumer").WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), dbErrUpdateTestConsumerID, channel2.ChannelID).WillReturnError(expectedErr)
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
		consumer, _ := data.NewConsumer(channel2, dbErrUpdateTestConsumerID, successfulGetTestToken, callbackURL, data.PushConsumerStr)
		consumer.QuickFix()
		mockChannelRepo := new(MockChannelRepository)
		mockChannelRepo.On("Get", channel2.ChannelID).Return(channel2, nil)
		rows := sqlmock.NewRows([]string{"id", "consumerId", "channelId", "name", "token", "callbackUrl", "type", "createdAt", "updatedAt"}).AddRow(consumer.ID, consumer.ConsumerID, channel2.ChannelID, consumer.Name, consumer.Token, consumer.CallbackURL, consumer.Type, consumer.CreatedAt, consumer.UpdatedAt)
		mock.ExpectQuery(consumerSelectRowCommonQuery+" channelId like").WithArgs(channel2.ChannelID, dbErrUpdateTestConsumerID).WillReturnRows(rows).WillReturnError(nil)
		result := sqlmock.NewResult(1, 0)
		mock.ExpectBegin()
		mock.ExpectExec("UPDATE consumer").WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), dbErrUpdateTestConsumerID, channel2.ChannelID).WillReturnResult(result).WillReturnError(nil)
		mock.ExpectRollback()
		mock.MatchExpectationsInOrder(true)
		repo := &ConsumerDBRepository{db: db, channelRepository: mockChannelRepo}
		consumer.Token = "c"
		_, err := repo.Store(consumer)
		assert.Equal(t, ErrNoRowsUpdated, err)
		assert.Nil(t, mock.ExpectationsWereMet())
	})
	t.Run("Update:Success", func(t *testing.T) {
		t.Parallel()
		consumer, _ := data.NewConsumer(channel1, successfulUpdateTestConsumerID, "oldtoken", callbackURL, data.PushConsumerStr)
		repo := getConsumerRepo()
		repo.Store(consumer)
		consumer.Token = successfulGetTestToken
		consumer.Type = data.PullConsumer
		updatedConsumer, err := repo.Store(consumer)
		assert.Nil(t, err)
		assert.Equal(t, successfulGetTestToken, updatedConsumer.Token)
		assert.Equal(t, data.PullConsumer, updatedConsumer.Type)
		updatedConsumer, err = repo.Get(channel1.ChannelID, successfulUpdateTestConsumerID)
		assert.Nil(t, err)
		assert.Equal(t, successfulGetTestToken, updatedConsumer.Token)
		assert.Equal(t, data.PullConsumer, updatedConsumer.Type)
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
		consumerType := data.PullConsumerStr
		if index < 50 {
			consumerType = data.PushConsumerStr
		}
		consumer, err := data.NewConsumer(channel2, listTestConsumerIDPrefix+indexString, successfulGetTestToken+" - "+indexString, callbackURL, consumerType)
		assert.Nil(t, err)
		_, err = repo.Store(consumer)
		assert.Nil(t, err)
	}
	consumer, err := data.NewConsumer(channel1, listTestConsumerIDPrefix+"100", successfulGetTestToken+" - 100", callbackURL, data.PushConsumerStr)
	assert.Nil(t, err)
	_, err = repo.Store(consumer)
	t.Run("PaginationDeadlock", func(t *testing.T) {
		t.Parallel()
		consumer1, _ := data.NewConsumer(channel2, dbErrUpdateTestConsumerID, successfulGetTestToken, callbackURL, data.PushConsumerStr)
		consumer2, _ := data.NewConsumer(channel2, successfulGetTestConsumerID, successfulGetTestToken, callbackURL, data.PushConsumerStr)
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
		mock.ExpectQuery("SELECT id, consumerId, name, token, callbackUrl, type, createdAt, updatedAt FROM consumer").WillReturnError(expectedErr)
		mock.MatchExpectationsInOrder(true)
		repo := &ConsumerDBRepository{db: db, channelRepository: mockChannelRepo}
		_, _, err := repo.GetList(channel2.ChannelID, data.NewPagination(nil, nil))
		assert.Equal(t, expectedErr, err)
		assert.Nil(t, mock.ExpectationsWereMet())
	})
	t.Run("EmptyChannel", func(t *testing.T) {
		t.Parallel()
		consumers, page, err := repo.GetList(channel3.ChannelID, data.NewPagination(nil, nil))
		assert.Nil(t, err)
		assert.NotNil(t, consumers)
		assert.NotNil(t, page)
		assert.Equal(t, 0, len(consumers))
		assert.Nil(t, page.Next)
		assert.Nil(t, page.Previous)
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
			assert.Equal(t, consumer.Type, data.PushConsumer)
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
			assert.Equal(t, channel2.ChannelID, consumer.ConsumingFrom.ChannelID)
			if strings.Contains(consumer.ConsumerID, listTestConsumerIDPrefix) {
				count = count + 1
			}
		}
		assert.Equal(t, 100, count)
	})
	t.Run("TestPushAndPullCount", func(t *testing.T) {
		t.Parallel()
		firstList, page, err := repo.GetList(channel2.ChannelID, data.NewPagination(nil, nil))
		assert.Nil(t, err)

		allConsumers := make([]*data.Consumer, 0)
		pushConsumerCount := 0
		pullConsumserCount := 0

		allConsumers = append(allConsumers, firstList...)
		for page.Next != nil {
			var consumers []*data.Consumer
			consumers, page, _ = repo.GetList(channel2.ChannelID, &data.Pagination{Next: page.Next})
			allConsumers = append(allConsumers, consumers...)
		}
		for _, consumer := range allConsumers {
			if consumer.Type == data.PullConsumer {
				pullConsumserCount++
			} else {
				pushConsumerCount++
			}
		}
		assert.GreaterOrEqual(t, 50, pullConsumserCount)
		assert.GreaterOrEqual(t, 52, pushConsumerCount)
	})
}

func TestCachedConsumerRepository(t *testing.T) {
	mockRepo := new(MockConsumerRepository)
	channel, _ := data.NewChannel("test-channel", "test-token")
	consumer, _ := data.NewConsumer(channel, "test-consumer", "test-token", callbackURL, data.PushConsumerStr)

	t.Run("GetCacheHit", func(t *testing.T) {
		cachedRepo := NewCachedConsumerRepository(mockRepo, 5*time.Second)
		mockRepo.On("Get", channel.ChannelID, consumer.ConsumerID).Return(consumer, nil).Once()

		retrievedConsumer, err := cachedRepo.Get(channel.ChannelID, consumer.ConsumerID)
		assert.Nil(t, err)
		assert.Equal(t, consumer, retrievedConsumer)

		retrievedConsumer, err = cachedRepo.Get(channel.ChannelID, consumer.ConsumerID)
		assert.Nil(t, err)
		assert.Equal(t, consumer, retrievedConsumer)

		mockRepo.AssertExpectations(t) // Ensure delegate Get called only once
	})

	t.Run("GetCacheMiss", func(t *testing.T) {
		cachedRepo := NewCachedConsumerRepository(mockRepo, 5*time.Second)
		mockRepo.On("Get", channel.ChannelID, consumer.ConsumerID).Return(consumer, nil).Once()

		retrievedConsumer, err := cachedRepo.Get(channel.ChannelID, consumer.ConsumerID)
		assert.Nil(t, err)
		assert.Equal(t, consumer, retrievedConsumer)

		mockRepo.AssertExpectations(t)
	})

	t.Run("GetDelegateError", func(t *testing.T) {
		cachedRepo := NewCachedConsumerRepository(mockRepo, 5*time.Second)

		expectedError := assert.AnError
		mockRepo.On("Get", channel.ChannelID, consumer.ConsumerID).Return(nil, expectedError).Once()

		retrievedConsumer, err := cachedRepo.Get(channel.ChannelID, consumer.ConsumerID)

		assert.Nil(t, retrievedConsumer)
		assert.ErrorIs(t, err, expectedError)

		mockRepo.AssertExpectations(t)

	})

	t.Run("GetByIDCacheHit", func(t *testing.T) {
		cachedRepo := NewCachedConsumerRepository(mockRepo, 5*time.Second)
		mockRepo.On("GetByID", consumer.ID.String()).Return(consumer, nil).Once()

		retrievedConsumer, err := cachedRepo.GetByID(consumer.ID.String())

		assert.Nil(t, err)
		assert.Equal(t, consumer, retrievedConsumer)

		retrievedConsumer, err = cachedRepo.GetByID(consumer.ID.String())
		assert.Nil(t, err)
		assert.Equal(t, consumer, retrievedConsumer)

		mockRepo.AssertExpectations(t)
	})

	t.Run("GetByIDCacheMiss", func(t *testing.T) {
		cachedRepo := NewCachedConsumerRepository(mockRepo, 5*time.Second)
		mockRepo.On("GetByID", consumer.ID.String()).Return(consumer, nil).Once()
		retrievedConsumer, err := cachedRepo.GetByID(consumer.ID.String())
		assert.Nil(t, err)
		assert.Equal(t, consumer, retrievedConsumer)
		mockRepo.AssertExpectations(t)

	})

	t.Run("GetByIDDelegateError", func(t *testing.T) {
		cachedRepo := NewCachedConsumerRepository(mockRepo, 5*time.Second)
		expectedError := assert.AnError
		mockRepo.On("GetByID", consumer.ID.String()).Return(nil, expectedError).Once()
		retrievedConsumer, err := cachedRepo.GetByID(consumer.ID.String())
		assert.Nil(t, retrievedConsumer)
		assert.ErrorIs(t, err, expectedError)
		mockRepo.AssertExpectations(t)
	})

	t.Run("StoreInvalidatesCache", func(t *testing.T) {
		cachedRepo := NewCachedConsumerRepository(mockRepo, 5*time.Second)
		mockRepo.On("Get", channel.ChannelID, consumer.ConsumerID).Return(consumer, nil).Once()
		mockRepo.On("Store", consumer).Return(consumer, nil).Once()

		// Prime the cache
		_, err := cachedRepo.Get(channel.ChannelID, consumer.ConsumerID)
		assert.Nil(t, err)

		// Store should invalidate the cache
		_, err = cachedRepo.Store(consumer)
		assert.Nil(t, err)

		mockRepo.On("Get", channel.ChannelID, consumer.ConsumerID).Return(consumer, nil).Once()
		_, err = cachedRepo.Get(channel.ChannelID, consumer.ConsumerID)
		assert.Nil(t, err)

		mockRepo.On("GetByID", consumer.ID.String()).Return(consumer, nil).Once()
		_, err = cachedRepo.GetByID(consumer.ID.String())
		assert.Nil(t, err)
		mockRepo.AssertExpectations(t) // Delegate Get should have been called twice
	})

	t.Run("DeleteInvalidatesCache", func(t *testing.T) {
		cachedRepo := NewCachedConsumerRepository(mockRepo, 5*time.Second)

		mockRepo.On("Get", channel.ChannelID, consumer.ConsumerID).Return(consumer, nil).Once()
		mockRepo.On("Delete", consumer).Return(nil).Once()

		// Prime the cache
		_, err := cachedRepo.Get(channel.ChannelID, consumer.ConsumerID)
		assert.Nil(t, err)

		// Delete should invalidate the cache
		err = cachedRepo.Delete(consumer)
		assert.Nil(t, err)

		mockRepo.On("Get", channel.ChannelID, consumer.ConsumerID).Return(consumer, nil).Once()
		_, err = cachedRepo.Get(channel.ChannelID, consumer.ConsumerID)
		assert.Nil(t, err)

		mockRepo.On("GetByID", consumer.ID.String()).Return(consumer, nil).Once()
		_, err = cachedRepo.GetByID(consumer.ID.String())
		assert.Nil(t, err)

		mockRepo.AssertExpectations(t)
	})

	t.Run("GetListDelegates", func(t *testing.T) {
		cachedRepo := NewCachedConsumerRepository(mockRepo, 5*time.Second)

		page := &data.Pagination{}
		mockRepo.On("GetList", channel.ChannelID, page).Return([]*data.Consumer{consumer}, page, nil).Once()

		retrievedConsumers, retrievedPage, err := cachedRepo.GetList(channel.ChannelID, page)
		assert.Nil(t, err)
		assert.Equal(t, []*data.Consumer{consumer}, retrievedConsumers)
		assert.Equal(t, page, retrievedPage)

		mockRepo.AssertExpectations(t)
	})
}
