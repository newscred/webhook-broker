package storage

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/newscred/webhook-broker/storage/data"
	"github.com/newscred/webhook-broker/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

const (
	duplicateMessageID       = "a-duplicate-message-id"
	consumerIDPrefixForPrune = "test-consumer-for-prune-"
)

var (
	producer1             *data.Producer
	channelForPrune       *data.Channel
	channelForStatusCount *data.Channel
	pruneConsumers        []*data.Consumer
)

func SetupForMessageTests() {
	producerRepo := NewProducerRepository(testDB)
	channelRepo := NewChannelRepository(testDB)
	consumerRepo := NewConsumerRepository(testDB, channelRepo)
	producer1, channelForPrune, pruneConsumers = SetupMessageDependencyFixture(producerRepo, channelRepo, consumerRepo, consumerIDPrefixForPrune)
}

func setupMsgStatusCount(channelRepo PseudoChannelRepository) (*data.Message, *data.Message) {
	msgRepo := getMessageRepository()
	channelForStatusCount, _ = data.NewChannel("channel-for-status-count", "sampletoken")
	var err error
	if channelForStatusCount, err = channelRepo.Store(channelForStatusCount); err != nil {
		log.Fatal().Err(err)
	}
	msg, err := data.NewMessage(channelForStatusCount, producer1, samplePayload, sampleContentType, data.HeadersMap{})
	if err != nil {
		log.Fatal().Err(err)
	}
	err = msgRepo.Create(msg)
	if err != nil {
		log.Fatal().Err(err)
	}
	firstMsg := msg
	tx, _ := testDB.Begin()
	txCompleted := false
	defer func() {
		if !txCompleted {
			tx.Rollback()
		}
	}()
	dispatchContext := context.WithValue(context.Background(), txContextKey, tx)
	err = msgRepo.SetDispatched(dispatchContext, msg)
	if err != nil {
		log.Fatal().Err(err)
	} else {
		tx.Commit()
		txCompleted = true
	}
	msg, err = data.NewMessage(channelForStatusCount, producer1, samplePayload, sampleContentType, data.HeadersMap{})
	if err != nil {
		log.Fatal().Err(err)
	}
	err = msgRepo.Create(msg)
	if err != nil {
		log.Fatal().Err(err)
	}
	return firstMsg, msg
}

func getMessageRepository() MessageRepository {
	return NewMessageRepository(testDB, NewChannelRepository(testDB), NewProducerRepository(testDB))
}

func TestGetMessageStatusCountsByChannel(t *testing.T) {
	msg1, msg2 := setupMsgStatusCount(NewChannelRepository(testDB))
	msgRepo := getMessageRepository()
	defer func() {
		msgRepo.DeleteMessage(msg1)
		msgRepo.DeleteMessage(msg2)
	}()
	counts, err := msgRepo.GetMessageStatusCountsByChannel(channelForStatusCount.ChannelID)
	assert.Nil(t, err)
	assert.Equal(t, 2, len(counts))
	assert.Equal(t, 1, counts[0].Count)
	assert.NotNil(t, counts[0].Status)
	log.Debug().Msg(counts[0].OldestItemTimestamp)
	currentTime := time.Now()
	formattedTime := time.Date(currentTime.Year(), currentTime.Month(), currentTime.Day(), 0, 0, 0, 0, currentTime.Location()).Format("2006-01-02 15:04:05.1231234Z07:00")
	assert.NotNil(t, counts[0].OldestItemTimestamp)
	assert.True(t, len(counts[0].OldestItemTimestamp) > 10)
	assert.Less(t, formattedTime, counts[0].OldestItemTimestamp)
	assert.NotNil(t, counts[0].NewestItemTimestamp)
	assert.True(t, len(counts[0].NewestItemTimestamp) > 10)
	assert.Less(t, formattedTime, counts[0].NewestItemTimestamp)
	assert.LessOrEqual(t, counts[0].OldestItemTimestamp, counts[0].NewestItemTimestamp)
	assert.Equal(t, 1, counts[1].Count)
}

func TestMessageGetByID(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		repo := getMessageRepository()
		msg, err := data.NewMessage(channel1, producer1, samplePayload, sampleContentType, data.HeadersMap{})
		assert.Nil(t, err)
		assert.Nil(t, repo.Create(msg))
		rMsg, err := repo.GetByID(msg.ID.String())
		assert.Nil(t, err)
		assert.NotNil(t, rMsg)
		assert.Equal(t, channel1.ID, msg.BroadcastedTo.ID)
		assert.Equal(t, producer1.ID, msg.ProducedBy.ID)
		assert.Equal(t, samplePayload, msg.Payload)
		assert.Equal(t, sampleContentType, msg.ContentType)
	})
	t.Run("Fail", func(t *testing.T) {
		t.Parallel()
		repo := getMessageRepository()
		_, err := repo.GetByID("non-existing-id")
		assert.NotNil(t, err)
	})
}

func TestMessageGetByIDs(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		repo := getMessageRepository()
		payload1, payload2 := samplePayload+"1", samplePayload+"2"
		msg1, err := data.NewMessage(channel1, producer1, payload1, sampleContentType, data.HeadersMap{})
		assert.NoError(t, err)
		assert.Nil(t, repo.Create(msg1))
		msg2, err := data.NewMessage(channel2, producer1, payload2, sampleContentType, data.HeadersMap{})
		assert.NoError(t, err)
		assert.Nil(t, repo.Create(msg2))

		rMsgs, err := repo.GetByIDs([]string{msg1.ID.String(), msg2.ID.String(), msg1.ID.String(), "non-existing-id"})
		assert.NoError(t, err)
		assert.Equal(t, 2, len(rMsgs))
		if rMsgs[0].ID != msg1.ID {
			rMsgs[0], rMsgs[1] = rMsgs[1], rMsgs[0]
		}

		assert.Equal(t, channel1.ChannelID, rMsgs[0].BroadcastedTo.ChannelID)
		assert.Equal(t, producer1.ProducerID, rMsgs[0].ProducedBy.ProducerID)
		assert.Equal(t, payload1, rMsgs[0].Payload)
		assert.Equal(t, sampleContentType, rMsgs[0].ContentType)

		assert.Equal(t, channel2.ChannelID, rMsgs[1].BroadcastedTo.ChannelID)
		assert.Equal(t, producer1.ProducerID, rMsgs[1].ProducedBy.ProducerID)
		assert.Equal(t, payload2, rMsgs[1].Payload)
		assert.Equal(t, sampleContentType, rMsgs[1].ContentType)
	})
	t.Run("Success Empty", func(t *testing.T) {
		t.Parallel()
		repo := getMessageRepository()
		rMsgs, err := repo.GetByIDs([]string{})
		assert.NoError(t, err)
		assert.Equal(t, 0, len(rMsgs))
	})
	t.Run("Fail", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		oldLogger := log.Logger
		log.Logger = log.Output(&buf)
		defer func() { log.Logger = oldLogger }()
		errString := "sample select error"
		expectedErr := errors.New(errString)
		db, mock, _ := sqlmock.New()
		msgRepo := NewMessageRepository(db, NewChannelRepository(testDB), NewProducerRepository(testDB))
		mock.ExpectQuery(messageSelectRowCommonQuery).WillReturnError(expectedErr)
		mock.MatchExpectationsInOrder(true)
		msgs, err := msgRepo.GetByIDs([]string{""})
		assert.Equal(t, 0, len(msgs))
		assert.NoError(t, mock.ExpectationsWereMet())
		assert.Equal(t, err, expectedErr)
		assert.Contains(t, buf.String(), errString)
	})
}

func TestMessageGetCreate(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		repo := getMessageRepository()
		msg, err := data.NewMessage(channel1, producer1, samplePayload, sampleContentType, data.HeadersMap{})
		assert.Nil(t, err)
		_, err = repo.Get(channel1.ChannelID, msg.MessageID)
		assert.NotNil(t, err)
		assert.Nil(t, repo.Create(msg))
		var readMessage *data.Message
		readMessage, err = repo.Get(channel1.ChannelID, msg.MessageID)
		assert.Nil(t, err)
		assert.Equal(t, msg.MessageID, readMessage.MessageID)
		assert.Equal(t, msg.ID, readMessage.ID)
		assert.Equal(t, channel1.ChannelID, readMessage.BroadcastedTo.ChannelID)
		assert.Equal(t, producer1.ProducerID, readMessage.ProducedBy.ProducerID)
		assert.Equal(t, msg.ContentType, readMessage.ContentType)
		assert.Equal(t, msg.Payload, readMessage.Payload)
		assert.Equal(t, msg.Priority, readMessage.Priority)
		assert.Equal(t, msg.Status, readMessage.Status)
		assert.True(t, msg.ReceivedAt.Equal(readMessage.ReceivedAt))
		assert.True(t, msg.OutboxedAt.Equal(readMessage.OutboxedAt))
		assert.True(t, msg.CreatedAt.Equal(readMessage.CreatedAt))
		assert.True(t, msg.UpdatedAt.Equal(readMessage.UpdatedAt))
	})
	t.Run("InvalidMsgState", func(t *testing.T) {
		t.Parallel()
		msg, err := data.NewMessage(channel1, producer1, samplePayload, sampleContentType, data.HeadersMap{})
		assert.Nil(t, err)
		msg.MessageID = ""
		repo := getMessageRepository()
		assert.NotNil(t, repo.Create(msg))
	})
	t.Run("NonExistingChannel", func(t *testing.T) {
		t.Parallel()
		channel, _ := data.NewChannel("testchannel4msgtest", "token")
		channel.QuickFix()
		msg, err := data.NewMessage(channel, producer1, samplePayload, sampleContentType, data.HeadersMap{})
		assert.Nil(t, err)
		repo := getMessageRepository()
		err = repo.Create(msg)
		assert.NotNil(t, err)
		_, err = repo.Get(channel.ChannelID, msg.MessageID)
		assert.NotNil(t, err)
		assert.Equal(t, sql.ErrNoRows, err)
	})
	t.Run("NonExistingProducer", func(t *testing.T) {
		t.Parallel()
		producer, _ := data.NewProducer("testproducer4invalidprodinmsgtest", "testtoken")
		producer.QuickFix()
		msg, err := data.NewMessage(channel1, producer, samplePayload, sampleContentType, data.HeadersMap{})
		assert.Nil(t, err)
		repo := getMessageRepository()
		err = repo.Create(msg)
		assert.NotNil(t, err)
		_, err = repo.Get(channel1.ChannelID, msg.MessageID)
		assert.NotNil(t, err)
		assert.Equal(t, sql.ErrNoRows, err)
	})
	t.Run("DuplicateMessage", func(t *testing.T) {
		t.Parallel()
		msg, err := data.NewMessage(channel1, producer1, samplePayload, sampleContentType, data.HeadersMap{})
		assert.Nil(t, err)
		repo := getMessageRepository()
		assert.Nil(t, repo.Create(msg))
		err = repo.Create(msg)
		assert.NotNil(t, err)
		assert.Equal(t, ErrDuplicateMessageIDForChannel, err)
	})
	t.Run("ProducerReadErr", func(t *testing.T) {
		t.Parallel()
		expectedErr := errors.New("producer could not be read")
		msg, err := data.NewMessage(channel1, producer1, samplePayload, sampleContentType, data.HeadersMap{})
		assert.Nil(t, err)
		mockProducerRepository := new(MockProducerRepository)
		repo := NewMessageRepository(testDB, NewChannelRepository(testDB), mockProducerRepository)
		mockProducerRepository.On("Get", mock.Anything).Return(nil, expectedErr)
		assert.Nil(t, repo.Create(msg))
		_, err = repo.Get(channel1.ChannelID, msg.MessageID)
		assert.NotNil(t, err)
		assert.Equal(t, expectedErr, err)
	})
}

func TestMessageSetDispatched(t *testing.T) {
	// Success tested along in TestDispatchMessage/Success
	t.Run("MessageNil", func(t *testing.T) {
		t.Parallel()
		msgRepo := getMessageRepository()
		tx, _ := testDB.Begin()
		assert.Equal(t, ErrInvalidStateToSave, msgRepo.SetDispatched(context.WithValue(context.Background(), txContextKey, tx), nil))
	})
	t.Run("MessageInvalid", func(t *testing.T) {
		t.Parallel()
		msgRepo := getMessageRepository()
		message := getMessageForJob()
		message.ReceivedAt = time.Time{}
		tx, _ := testDB.Begin()
		assert.Equal(t, ErrInvalidStateToSave, msgRepo.SetDispatched(context.WithValue(context.Background(), txContextKey, tx), message))
	})
	t.Run("NoTX", func(t *testing.T) {
		t.Parallel()
		msgRepo := getMessageRepository()
		message := getMessageForJob()
		tx, _ := testDB.Begin()
		assert.Equal(t, ErrNoTxInContext, msgRepo.SetDispatched(context.WithValue(context.Background(), ContextKey("hello"), tx), message))
	})
}

func TestGetMessagesNotDispatchedForCertainPeriod(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		msgRepo := getMessageRepository()
		msg, err := data.NewMessage(channel1, producer1, samplePayload, sampleContentType, data.HeadersMap{})
		assert.Nil(t, err)
		msg.ReceivedAt = msg.ReceivedAt.Add(-5 * time.Second)
		msg2, err := data.NewMessage(channel1, producer1, samplePayload, sampleContentType, data.HeadersMap{})
		err = msgRepo.Create(msg)
		assert.Nil(t, err)
		err = msgRepo.Create(msg2)
		assert.Nil(t, err)
		msgs := msgRepo.GetMessagesNotDispatchedForCertainPeriod(2 * time.Second)
		assert.Equal(t, 1, len(msgs))
		assert.Equal(t, msg.MessageID, msgs[0].MessageID)
	})
	t.Run("QueryError", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		oldLogger := log.Logger
		log.Logger = log.Output(&buf)
		defer func() { log.Logger = oldLogger }()
		errString := "sample select error"
		expectedErr := errors.New(errString)
		db, mock, _ := sqlmock.New()
		msgRepo := NewMessageRepository(db, NewChannelRepository(testDB), NewProducerRepository(testDB))
		mock.ExpectQuery(messageSelectRowCommonQuery).WillReturnError(expectedErr)
		mock.MatchExpectationsInOrder(true)
		msgs := msgRepo.GetMessagesNotDispatchedForCertainPeriod(2 * time.Second)
		assert.Equal(t, 0, len(msgs))
		assert.Nil(t, mock.ExpectationsWereMet())
		assert.Contains(t, buf.String(), errString)
	})
}

func getPruneDeliveryJobsInFixture(msg *data.Message) []*data.DeliveryJob {
	jobs := make([]*data.DeliveryJob, 0, len(pruneConsumers))
	for _, consumer := range pruneConsumers {
		job, _ := data.NewDeliveryJob(msg, consumer)
		jobs = append(jobs, job)
	}
	return jobs
}

// MockedDataAccessor is a mock implementation of DataAccessor for testing purposes.
type MockedDataAccessor struct {
	mock.Mock
}

func (m *MockedDataAccessor) GetLockRepository() LockRepository {
	args := m.Called()
	return args.Get(0).(LockRepository)
}

func (m *MockedDataAccessor) GetAppRepository() AppRepository {
	args := m.Called()
	return args.Get(0).(AppRepository)
}

// GetMessageRepository mocks the GetMessageRepository method.
func (m *MockedDataAccessor) GetMessageRepository() MessageRepository {
	args := m.Called()
	return args.Get(0).(MessageRepository)
}

// GetDeliveryJobRepository mocks the GetDeliveryJobRepository method.
func (m *MockedDataAccessor) GetDeliveryJobRepository() DeliveryJobRepository {
	args := m.Called()
	return args.Get(0).(DeliveryJobRepository)
}

// GetProducerRepository mocks the GetProducerRepository method.
func (m *MockedDataAccessor) GetProducerRepository() ProducerRepository {
	args := m.Called()
	return args.Get(0).(ProducerRepository)
}

// GetChannelRepository mocks the GetChannelRepository method.
func (m *MockedDataAccessor) GetChannelRepository() ChannelRepository {
	args := m.Called()
	return args.Get(0).(ChannelRepository)
}

// GetConsumerRepository mocks the GetConsumerRepository method.
func (m *MockedDataAccessor) GetConsumerRepository() ConsumerRepository {
	args := m.Called()
	return args.Get(0).(ConsumerRepository)
}

// Close mocks the Close method.
func (m *MockedDataAccessor) Close() {
	m.Called()
}

// GetScheduledMessageRepository mocks the GetScheduledMessageRepository method.
func (m *MockedDataAccessor) GetScheduledMessageRepository() ScheduledMessageRepository {
	args := m.Called()
	return args.Get(0).(ScheduledMessageRepository)
}

func TestGetMessagesFromBeforeDurationThatAreCompletelyDelivered(t *testing.T) {
	msgRepo := getMessageRepository()

	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		dataAccessor := new(MockedDataAccessor)
		dataAccessor.On("GetMessageRepository").Return(msgRepo)
		dataAccessor.On("GetDeliveryJobRepository").Return(getDeliverJobRepository())
		dataAccessor.On("GetScheduledMessageRepository").Return(getScheduledMessageRepository())
		msg, _ := SetupPruneableMessageFixture(dataAccessor, channelForPrune, producer1, pruneConsumers, 50)
		pruneAbleMessages := msgRepo.GetMessagesFromBeforeDurationThatAreCompletelyDelivered(40*time.Second, 1000)
		assert.Equal(t, 1, len(pruneAbleMessages))
		assert.Equal(t, msg.MessageID, pruneAbleMessages[0].MessageID)
		// create such that pagination query gets triggered
		iterLength := 110
		msgIds := make([]string, iterLength+1)
		for i := 0; i < iterLength; i++ {
			dataAccessor.On("GetScheduledMessageRepository").Return(getScheduledMessageRepository())
			msg, _ := SetupPruneableMessageFixture(dataAccessor, channelForPrune, producer1, pruneConsumers, 50)
			msgIds[i] = msg.MessageID
		}
		msgIds[iterLength] = pruneAbleMessages[0].MessageID
		pruneAbleMessages = msgRepo.GetMessagesFromBeforeDurationThatAreCompletelyDelivered(40*time.Second, 1000)
		// make sure every msg is returned
		assert.Equal(t, iterLength+1, len(pruneAbleMessages))
		for index := range pruneAbleMessages {
			assert.Contains(t, msgIds, pruneAbleMessages[index].MessageID)
			msgIds = utils.DeleteFromSlice(msgIds, utils.FindIndex(msgIds, pruneAbleMessages[index].MessageID))
		}
		assert.Equal(t, 0, len(msgIds))
		pruneAbleMessages = msgRepo.GetMessagesFromBeforeDurationThatAreCompletelyDelivered(40*time.Second, 10)
		// First page size is 100, so absolute max of 10 should return 100
		assert.Equal(t, 100, len(pruneAbleMessages))

	})
	t.Run("QueryError", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		oldLogger := log.Logger
		log.Logger = log.Output(&buf)
		defer func() { log.Logger = oldLogger }()
		errString := "sample select error"
		expectedErr := errors.New(errString)
		db, mock, _ := sqlmock.New()
		msgRepo := NewMessageRepository(db, NewChannelRepository(testDB), NewProducerRepository(testDB))
		mock.ExpectQuery("SELECT").WillReturnError(expectedErr)
		mock.MatchExpectationsInOrder(true)
		msgs := msgRepo.GetMessagesFromBeforeDurationThatAreCompletelyDelivered(2*time.Second, 1000)
		assert.Equal(t, 0, len(msgs))
		assert.Contains(t, buf.String(), errString)
		assert.Nil(t, mock.ExpectationsWereMet())
	})
}

func TestDeleteMessageJobs(t *testing.T) {
	deliverJobRepo := getDeliverJobRepository()
	msgRepo := getMessageRepository()
	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		msg, _ := data.NewMessage(channelForPrune, producer1, samplePayload, sampleContentType, data.HeadersMap{})
		msg.ReceivedAt = msg.ReceivedAt.Add(-50 * time.Second)
		msgRepo.Create(msg)
		jobs := getPruneDeliveryJobsInFixture(msg)
		deliverJobRepo.DispatchMessage(msg, jobs...)
		for index := range jobs {
			markJobDelivered(deliverJobRepo, jobs[index])
		}
		pruneAbleMessages := msgRepo.GetMessagesFromBeforeDurationThatAreCompletelyDelivered(40*time.Second, 1000)
		assert.GreaterOrEqual(t, len(pruneAbleMessages), 1)
		for index := range pruneAbleMessages {
			d_jobs, _, err := deliverJobRepo.GetJobsForMessage(pruneAbleMessages[index], &data.Pagination{})
			assert.Nil(t, err)
			assert.Equal(t, 1, len(d_jobs))
			assert.Nil(t, deliverJobRepo.DeleteJobsForMessage(pruneAbleMessages[index]))
			assert.Nil(t, msgRepo.DeleteMessage(pruneAbleMessages[index]))
		}
	})
}

func TestGetMessagesByChannel(t *testing.T) {
	t.Run("PaginationDeadlock", func(t *testing.T) {
		t.Parallel()
		msgRepo := getMessageRepository()
		_, _, err := msgRepo.GetMessagesForChannel(channel2.ChannelID, data.NewPagination(channel1, channel2))
		assert.NotNil(t, err)
		assert.Equal(t, ErrPaginationDeadlock, err)
	})
	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		msgRepo := getMessageRepository()
		msg, err := data.NewMessage(channel2, producer1, samplePayload, sampleContentType, data.HeadersMap{})
		assert.Nil(t, err)
		msg2, err := data.NewMessage(channel1, producer1, samplePayload, sampleContentType, data.HeadersMap{})
		err = msgRepo.Create(msg)
		assert.Nil(t, err)
		err = msgRepo.Create(msg2)
		assert.Nil(t, err)
		msgs, page, err := msgRepo.GetMessagesForChannel(channel2.ChannelID, data.NewPagination(nil, nil))
		assert.Nil(t, err)
		assert.NotNil(t, page)
		assert.NotNil(t, page.Next)
		assert.NotNil(t, page.Previous)
		assert.Equal(t, 2, len(msgs))
		assert.Equal(t, msg.ID, msgs[0].ID)
		msgs, page, err = msgRepo.GetMessagesForChannel(channel2.ChannelID, data.NewPagination(nil, nil), msgs[0].Status)
		assert.Nil(t, err)
		assert.NotNil(t, page)
		assert.NotNil(t, page.Next)
		assert.NotNil(t, page.Previous)
		assert.Equal(t, 2, len(msgs))
		assert.Equal(t, msg.ID, msgs[0].ID)
		msgs, page3, err := msgRepo.GetMessagesForChannel(channel2.ChannelID, &data.Pagination{Previous: page.Previous})
		assert.Nil(t, err)
		assert.NotNil(t, page3)
		assert.Nil(t, page3.Next)
		assert.Nil(t, page3.Previous)
		assert.Equal(t, 0, len(msgs))
		msgs, page2, err := msgRepo.GetMessagesForChannel(channel2.ChannelID, &data.Pagination{Next: page.Next})
		assert.Nil(t, err)
		assert.NotNil(t, page2)
		assert.Nil(t, page2.Next)
		assert.Nil(t, page2.Previous)
		assert.Equal(t, 0, len(msgs))
	})
	t.Run("NonExistingChannel", func(t *testing.T) {
		t.Parallel()
		msgRepo := getMessageRepository()
		_, _, err := msgRepo.GetMessagesForChannel(channel2.ChannelID+"NONE", data.NewPagination(nil, nil))
		assert.Equal(t, sql.ErrNoRows, err)
	})
}

func TestMessageDBRepository_DeleteMessagesAndJobs(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("an error '%s' was not expected when opening a stub database connection", err)
	}
	defer db.Close()

	// Mock Channel and Producer Repositories (needed for MessageDBRepository creation)
	mockChannelRepo := new(MockChannelRepository)
	mockProducerRepo := new(MockProducerRepository)

	msgRepo := NewMessageRepository(db, mockChannelRepo, mockProducerRepo).(*MessageDBRepository)

	t.Run("Success", func(t *testing.T) {
		messageIDs := []string{"messageID1", "messageID2", "messageID3"}
		mock.ExpectBegin()
		// Mock the delete queries
		mock.ExpectExec("DELETE FROM job WHERE messageId IN \\(\\?,\\?,\\?\\)").
			WithArgs(messageIDs[0], messageIDs[1], messageIDs[2]).
			WillReturnResult(sqlmock.NewResult(3, 3)) // 3 rows affected

		mock.ExpectExec("DELETE FROM message WHERE id IN \\(\\?,\\?,\\?\\)").
			WithArgs(messageIDs[0], messageIDs[1], messageIDs[2]).
			WillReturnResult(sqlmock.NewResult(3, 3)) // 3 rows affected
		mock.ExpectCommit()

		err := msgRepo.DeleteMessagesAndJobs(context.Background(), messageIDs)
		assert.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("Empty IDs", func(t *testing.T) {
		err := msgRepo.DeleteMessagesAndJobs(context.Background(), []string{})
		assert.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("Error deleting jobs", func(t *testing.T) {
		messageIDs := []string{"messageID1", "messageID2", "messageID3"}
		// Mock the delete queries to return an error
		mock.ExpectBegin()
		mock.ExpectExec("DELETE FROM job WHERE messageId IN \\(\\?,\\?,\\?\\)").
			WithArgs(messageIDs[0], messageIDs[1], messageIDs[2]).
			WillReturnError(errors.New("failed to delete jobs"))
		mock.ExpectRollback()

		err := msgRepo.DeleteMessagesAndJobs(context.Background(), messageIDs)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to delete jobs")
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("Error deleting messages", func(t *testing.T) {
		messageIDs := []string{"messageID1", "messageID2", "messageID3"}
		// Mock successful job deletion, then message deletion error
		mock.ExpectBegin()
		mock.ExpectExec("DELETE FROM job WHERE messageId IN \\(\\?,\\?,\\?\\)").
			WithArgs(messageIDs[0], messageIDs[1], messageIDs[2]).
			WillReturnResult(sqlmock.NewResult(3, 3))

		mock.ExpectExec("DELETE FROM message WHERE id IN \\(\\?,\\?,\\?\\)").
			WithArgs(messageIDs[0], messageIDs[1], messageIDs[2]).
			WillReturnError(errors.New("failed to delete messages"))
		mock.ExpectRollback()

		err := msgRepo.DeleteMessagesAndJobs(context.Background(), messageIDs)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to delete messages")
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("Batching", func(t *testing.T) {
		generateMessageIDs := func(count int) []string {
			messageIDs := make([]string, count)
			for i := 0; i < count; i++ {
				messageIDs[i] = fmt.Sprintf("messageID%d", i+1)
			}
			return messageIDs
		}
		messageIDs := generateMessageIDs(110)
		// Simulate batching by mocking multiple delete executions

		// Mock the delete queries for the first batch (100 messages)
		mock.ExpectBegin()
		mock.ExpectExec("DELETE FROM job WHERE messageId IN"). //Explicitly list placeholders for 100 args
									WithArgs(messageIDs[0], messageIDs[1], messageIDs[2], messageIDs[3], messageIDs[4], messageIDs[5], messageIDs[6], messageIDs[7], messageIDs[8], messageIDs[9], messageIDs[10], messageIDs[11], messageIDs[12], messageIDs[13], messageIDs[14], messageIDs[15], messageIDs[16], messageIDs[17], messageIDs[18], messageIDs[19], messageIDs[20], messageIDs[21], messageIDs[22], messageIDs[23], messageIDs[24], messageIDs[25], messageIDs[26], messageIDs[27], messageIDs[28], messageIDs[29], messageIDs[30], messageIDs[31], messageIDs[32], messageIDs[33], messageIDs[34], messageIDs[35], messageIDs[36], messageIDs[37], messageIDs[38], messageIDs[39], messageIDs[40], messageIDs[41], messageIDs[42], messageIDs[43], messageIDs[44], messageIDs[45], messageIDs[46], messageIDs[47], messageIDs[48], messageIDs[49], messageIDs[50], messageIDs[51], messageIDs[52], messageIDs[53], messageIDs[54], messageIDs[55], messageIDs[56], messageIDs[57], messageIDs[58], messageIDs[59], messageIDs[60], messageIDs[61], messageIDs[62], messageIDs[63], messageIDs[64], messageIDs[65], messageIDs[66], messageIDs[67], messageIDs[68], messageIDs[69], messageIDs[70], messageIDs[71], messageIDs[72], messageIDs[73], messageIDs[74], messageIDs[75], messageIDs[76], messageIDs[77], messageIDs[78], messageIDs[79], messageIDs[80], messageIDs[81], messageIDs[82], messageIDs[83], messageIDs[84], messageIDs[85], messageIDs[86], messageIDs[87], messageIDs[88], messageIDs[89], messageIDs[90], messageIDs[91], messageIDs[92], messageIDs[93], messageIDs[94], messageIDs[95], messageIDs[96], messageIDs[97], messageIDs[98], messageIDs[99]).
									WillReturnResult(sqlmock.NewResult(100, 100))

		mock.ExpectExec("DELETE FROM message WHERE id IN"). //Explicitly list placeholders for 100 args
									WithArgs(messageIDs[0], messageIDs[1], messageIDs[2], messageIDs[3], messageIDs[4], messageIDs[5], messageIDs[6], messageIDs[7], messageIDs[8], messageIDs[9], messageIDs[10], messageIDs[11], messageIDs[12], messageIDs[13], messageIDs[14], messageIDs[15], messageIDs[16], messageIDs[17], messageIDs[18], messageIDs[19], messageIDs[20], messageIDs[21], messageIDs[22], messageIDs[23], messageIDs[24], messageIDs[25], messageIDs[26], messageIDs[27], messageIDs[28], messageIDs[29], messageIDs[30], messageIDs[31], messageIDs[32], messageIDs[33], messageIDs[34], messageIDs[35], messageIDs[36], messageIDs[37], messageIDs[38], messageIDs[39], messageIDs[40], messageIDs[41], messageIDs[42], messageIDs[43], messageIDs[44], messageIDs[45], messageIDs[46], messageIDs[47], messageIDs[48], messageIDs[49], messageIDs[50], messageIDs[51], messageIDs[52], messageIDs[53], messageIDs[54], messageIDs[55], messageIDs[56], messageIDs[57], messageIDs[58], messageIDs[59], messageIDs[60], messageIDs[61], messageIDs[62], messageIDs[63], messageIDs[64], messageIDs[65], messageIDs[66], messageIDs[67], messageIDs[68], messageIDs[69], messageIDs[70], messageIDs[71], messageIDs[72], messageIDs[73], messageIDs[74], messageIDs[75], messageIDs[76], messageIDs[77], messageIDs[78], messageIDs[79], messageIDs[80], messageIDs[81], messageIDs[82], messageIDs[83], messageIDs[84], messageIDs[85], messageIDs[86], messageIDs[87], messageIDs[88], messageIDs[89], messageIDs[90], messageIDs[91], messageIDs[92], messageIDs[93], messageIDs[94], messageIDs[95], messageIDs[96], messageIDs[97], messageIDs[98], messageIDs[99]).
									WillReturnResult(sqlmock.NewResult(100, 100))

		mock.ExpectExec("DELETE FROM job WHERE messageId IN"). //Explicitly list placeholders for 10 args
									WithArgs(messageIDs[100], messageIDs[101], messageIDs[102], messageIDs[103], messageIDs[104], messageIDs[105], messageIDs[106], messageIDs[107], messageIDs[108], messageIDs[109]). // Pass string slice directly
									WillReturnResult(sqlmock.NewResult(10, 10))

		mock.ExpectExec("DELETE FROM message WHERE id IN"). //Explicitly list placeholders for 10 args
									WithArgs(messageIDs[100], messageIDs[101], messageIDs[102], messageIDs[103], messageIDs[104], messageIDs[105], messageIDs[106], messageIDs[107], messageIDs[108], messageIDs[109]). // Pass string slice directly
									WillReturnResult(sqlmock.NewResult(10, 10))
		mock.ExpectCommit()

		err := msgRepo.DeleteMessagesAndJobs(context.Background(), messageIDs)
		assert.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())

	})
}
