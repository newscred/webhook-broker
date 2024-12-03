package storage

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
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
	producer1       *data.Producer
	channelForPrune *data.Channel
	pruneConsumers  []*data.Consumer
)

func SetupForMessageTests() {
	producerRepo := NewProducerRepository(testDB)
	channelRepo := NewChannelRepository(testDB)
	consumerRepo := NewConsumerRepository(testDB, channelRepo)
	producer1, channelForPrune, pruneConsumers = SetupMessageDependencyFixture(producerRepo, channelRepo, consumerRepo, consumerIDPrefixForPrune)
}

func getMessageRepository() MessageRepository {
	return NewMessageRepository(testDB, NewChannelRepository(testDB), NewProducerRepository(testDB))
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

func TestGetMessagesFromBeforeDurationThatAreCompletelyDelivered(t *testing.T) {
	msgRepo := getMessageRepository()

	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		dataAccessor := new(MockedDataAccessor)
		dataAccessor.On("GetMessageRepository").Return(msgRepo)
		dataAccessor.On("GetDeliveryJobRepository").Return(getDeliverJobRepository())
		msg, _ := SetupPruneableMessageFixture(dataAccessor, channelForPrune, producer1, pruneConsumers, 50)
		pruneAbleMessages := msgRepo.GetMessagesFromBeforeDurationThatAreCompletelyDelivered(40*time.Second, 1000)
		assert.Equal(t, 1, len(pruneAbleMessages))
		assert.Equal(t, msg.MessageID, pruneAbleMessages[0].MessageID)
		// create such that pagination query gets triggered
		iterLength := 110
		msgIds := make([]string, iterLength+1)
		for i := 0; i < iterLength; i++ {
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
