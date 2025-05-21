package storage

import (
	"database/sql"
	"testing"
	"time"

	"github.com/newscred/webhook-broker/storage/data"
	"github.com/rs/xid"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/assert"
)

const (
	duplicateScheduledMessageID = "a-duplicate-scheduled-message-id"
	scheduledTestChannelID      = "test-channel-for-scheduled"
	scheduledTestProducerID     = "test-producer-for-scheduled"
	scheduledTestToken          = "scheduled-test-token"
)

var (
	scheduledTestChannel  *data.Channel
	scheduledTestProducer *data.Producer
)

// SetupForScheduledMessageTests is called by TestMain to set up the test data
func SetupForScheduledMessageTests() {
	channelRepo := NewChannelRepository(testDB)
	producerRepo := NewProducerRepository(testDB)

	// Create channel if it doesn't exist
	scheduledTestChannel, _ = data.NewChannel(scheduledTestChannelID, scheduledTestToken)
	scheduledTestChannel.Name = "Scheduled Message Test Channel"
	scheduledTestChannel.QuickFix()
	existingChannel, err := channelRepo.Get(scheduledTestChannelID)
	if err != nil {
		log.Info().Caller().Msg("Creating test channel for scheduled message")
		scheduledTestChannel, err = channelRepo.Store(scheduledTestChannel)
		if err != nil {
			log.Info().Caller().Err(err)
		} else {
			log.Info().Caller().Msg("Created test channel for scheduled message")
		}
	} else {
		log.Info().Caller().Msg("Reading test channel for scheduled message")
		scheduledTestChannel = existingChannel
	}

	// Create producer if it doesn't exist
	scheduledTestProducer, _ = data.NewProducer(scheduledTestProducerID, scheduledTestToken)
	scheduledTestProducer.Name = "Scheduled Message Test Producer"
	scheduledTestProducer.QuickFix()
	existingProducer, err := producerRepo.Get(scheduledTestProducerID)
	if err != nil {
		log.Info().Caller().Msg("Creating test producer for scheduled message")
		scheduledTestProducer, err = producerRepo.Store(scheduledTestProducer)
		if err != nil {
			log.Info().Caller().Err(err)
		} else {
			log.Info().Caller().Msg("Created test producer for scheduled message")
		}
	} else {
		log.Info().Caller().Msg("Reading test producer for scheduled message")
		scheduledTestProducer = existingProducer
	}
}

// Helper function to execute SQL update queries for test cleanup
func executeUpdateQuery(db *sql.DB, query string, args []interface{}) (sql.Result, error) {
	var result sql.Result
	var err error
	err = transactionalWrites(db, func(tx *sql.Tx) error {
		result, err = tx.Exec(query, args...)
		return err
	})
	return result, err
}

func getScheduledMessageRepository() ScheduledMessageRepository {
	return NewScheduledMessageRepository(testDB, NewChannelRepository(testDB), NewProducerRepository(testDB))
}

func init() {
	// Run setup if we're not in TestMain (for development only)
	if testDB != nil && scheduledTestChannel == nil {
		SetupForScheduledMessageTests()
	}
}

func TestScheduledMessageCreate(t *testing.T) {
	// Assuming testDB is a *sql.DB instance from your test setup

	// 1. Create a Test Table (if it doesn't exist)
	_, err := testDB.Exec(`
		CREATE TABLE IF NOT EXISTS test_writable (
			id INTEGER PRIMARY KEY,
			value TEXT
		);
	`)
	assert.NoError(t, err, "Failed to create test table")

	// 2. Attempt a Write
	testValue := "test_value"
	result, err := testDB.Exec(`
		INSERT INTO test_writable (value) VALUES (?);
	`, testValue)
	assert.NoError(t, err, "Failed to insert test data")

	// 3. Verify Success
	rowsAffected, err := result.RowsAffected()
	assert.NoError(t, err, "Failed to get rows affected")
	assert.Equal(t, int64(1), rowsAffected, "Incorrect number of rows affected")

	t.Run("Success", func(t *testing.T) {
		msgRepo := getScheduledMessageRepository()
		dispatchTime := time.Now().Add(5 * time.Minute)
		message, err := data.NewScheduledMessage(scheduledTestChannel, scheduledTestProducer, "test-payload", "test/content-type", dispatchTime, data.HeadersMap{})
		assert.Nil(t, err)
		assert.NotNil(t, message)

		t.Logf("Database connection: %+v", testDB)

		err = msgRepo.Create(message)
		assert.Nil(t, err)

		// Cleanup
		_, err = executeUpdateQuery(testDB, "DELETE FROM scheduled_message WHERE id = ?", []interface{}{message.ID})
		assert.Nil(t, err)
	})

	t.Run("DuplicateMessageID", func(t *testing.T) {
		msgRepo := getScheduledMessageRepository()
		dispatchTime := time.Now().Add(5 * time.Minute)
		message, err := data.NewScheduledMessage(scheduledTestChannel, scheduledTestProducer, "test-payload", "test/content-type", dispatchTime, data.HeadersMap{})
		message.MessageID = duplicateScheduledMessageID
		assert.Nil(t, err)
		assert.NotNil(t, message)

		err = msgRepo.Create(message)
		assert.Nil(t, err)

		// Try to create another message with the same ID
		message2, err := data.NewScheduledMessage(scheduledTestChannel, scheduledTestProducer, "test-payload-2", "test/content-type", dispatchTime, data.HeadersMap{})
		message2.MessageID = duplicateScheduledMessageID
		assert.Nil(t, err)
		assert.NotNil(t, message2)

		err = msgRepo.Create(message2)
		assert.Equal(t, ErrDuplicateScheduledMessageIDForChannel, err)

		// Cleanup
		_, err = executeUpdateQuery(testDB, "DELETE FROM scheduled_message WHERE id = ?", []interface{}{message.ID})
		assert.Nil(t, err)
	})

	t.Run("InvalidMessage", func(t *testing.T) {
		msgRepo := getScheduledMessageRepository()
		message := &data.ScheduledMessage{}
		err := msgRepo.Create(message)
		assert.Equal(t, data.ErrInsufficientInformationForCreating, err)
	})
}

func TestScheduledMessageGet(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		msgRepo := getScheduledMessageRepository()
		dispatchTime := time.Now().Add(5 * time.Minute)
		message, err := data.NewScheduledMessage(scheduledTestChannel, scheduledTestProducer, "test-payload", "test/content-type", dispatchTime, data.HeadersMap{})
		assert.Nil(t, err)
		assert.NotNil(t, message)

		t.Logf("Database connection: %+v", testDB)

		err = msgRepo.Create(message)
		assert.Nil(t, err)

		// Retrieve the message
		retrievedMessage, err := msgRepo.Get(scheduledTestChannel.ChannelID, message.MessageID)
		assert.Nil(t, err)
		assert.Equal(t, message.MessageID, retrievedMessage.MessageID)
		assert.Equal(t, message.Payload, retrievedMessage.Payload)
		assert.Equal(t, message.ContentType, retrievedMessage.ContentType)
		assert.Equal(t, scheduledTestChannel.ChannelID, retrievedMessage.BroadcastedTo.ChannelID)
		assert.Equal(t, scheduledTestProducer.ProducerID, retrievedMessage.ProducedBy.ProducerID)

		// Cleanup
		_, err = executeUpdateQuery(testDB, "DELETE FROM scheduled_message WHERE id = ?", []interface{}{message.ID})
		assert.Nil(t, err)
	})

	t.Run("NotFound", func(t *testing.T) {
		msgRepo := getScheduledMessageRepository()
		_, err := msgRepo.Get(scheduledTestChannel.ChannelID, "non-existent-id")
		assert.NotNil(t, err)
	})

	t.Run("InvalidChannel", func(t *testing.T) {
		msgRepo := getScheduledMessageRepository()
		_, err := msgRepo.Get("non-existent-channel", "some-id")
		assert.NotNil(t, err)
	})
}

func TestScheduledMessageGetByID(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		msgRepo := getScheduledMessageRepository()
		dispatchTime := time.Now().Add(5 * time.Minute)
		message, err := data.NewScheduledMessage(scheduledTestChannel, scheduledTestProducer, "test-payload", "test/content-type", dispatchTime, data.HeadersMap{})
		assert.Nil(t, err)
		assert.NotNil(t, message)

		err = msgRepo.Create(message)
		assert.Nil(t, err)

		// Retrieve the message by ID
		retrievedMessage, err := msgRepo.GetByID(message.ID.String())
		assert.Nil(t, err)
		assert.Equal(t, message.MessageID, retrievedMessage.MessageID)
		assert.Equal(t, message.Payload, retrievedMessage.Payload)
		assert.Equal(t, message.ContentType, retrievedMessage.ContentType)
		assert.Equal(t, scheduledTestChannel.ChannelID, retrievedMessage.BroadcastedTo.ChannelID)
		assert.Equal(t, scheduledTestProducer.ProducerID, retrievedMessage.ProducedBy.ProducerID)

		// Cleanup
		_, err = executeUpdateQuery(testDB, "DELETE FROM scheduled_message WHERE id = ?", []interface{}{message.ID})
		assert.Nil(t, err)
	})

	t.Run("NotFound", func(t *testing.T) {
		msgRepo := getScheduledMessageRepository()
		_, err := msgRepo.GetByID("non-existent-id")
		assert.NotNil(t, err)
	})
}

func TestScheduledMessageMarkDispatched(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		msgRepo := getScheduledMessageRepository()
		dispatchTime := time.Now().Add(5 * time.Minute)
		message, err := data.NewScheduledMessage(scheduledTestChannel, scheduledTestProducer, "test-payload", "test/content-type", dispatchTime, data.HeadersMap{})
		assert.Nil(t, err)
		assert.NotNil(t, message)

		err = msgRepo.Create(message)
		assert.Nil(t, err)

		// Mark message as dispatched
		message.Status = data.ScheduledMsgStatusDispatched
		message.DispatchedAt = time.Now()
		err = msgRepo.MarkDispatched(message)
		assert.Nil(t, err)

		// Verify status change
		retrievedMessage, err := msgRepo.GetByID(message.ID.String())
		assert.Nil(t, err)
		assert.Equal(t, data.ScheduledMsgStatusDispatched, retrievedMessage.Status)
		assert.False(t, retrievedMessage.DispatchedAt.IsZero())

		// Cleanup
		_, err = executeUpdateQuery(testDB, "DELETE FROM scheduled_message WHERE id = ?", []interface{}{message.ID})
		assert.Nil(t, err)
	})

	t.Run("InvalidStatus", func(t *testing.T) {
		msgRepo := getScheduledMessageRepository()
		dispatchTime := time.Now().Add(5 * time.Minute)
		message, err := data.NewScheduledMessage(scheduledTestChannel, scheduledTestProducer, "test-payload", "test/content-type", dispatchTime, data.HeadersMap{})
		assert.Nil(t, err)
		assert.NotNil(t, message)

		err = msgRepo.Create(message)
		assert.Nil(t, err)

		// Try to mark as dispatched without changing status
		err = msgRepo.MarkDispatched(message)
		assert.NotNil(t, err)

		// Cleanup
		_, err = executeUpdateQuery(testDB, "DELETE FROM scheduled_message WHERE id = ?", []interface{}{message.ID})
		assert.Nil(t, err)
	})
}

func TestScheduledMessageGetMessagesReadyForDispatch(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		msgRepo := getScheduledMessageRepository()

		// Create a message scheduled in the past
		pastDispatchTime := time.Now().Add(-5 * time.Minute)
		pastMessage, err := data.NewScheduledMessage(scheduledTestChannel, scheduledTestProducer, "past-payload", "test/content-type", pastDispatchTime, data.HeadersMap{})
		assert.Nil(t, err)
		assert.NotNil(t, pastMessage)

		// Force the dispatch schedule to be in the past (since QuickFix enforces minimum 2 min)
		pastMessage.DispatchSchedule = pastDispatchTime
		err = msgRepo.Create(pastMessage)
		assert.Nil(t, err)

		// Create a message scheduled in the future
		futureDispatchTime := time.Now().Add(15 * time.Minute)
		futureMessage, err := data.NewScheduledMessage(scheduledTestChannel, scheduledTestProducer, "future-payload", "test/content-type", futureDispatchTime, data.HeadersMap{})
		assert.Nil(t, err)
		assert.NotNil(t, futureMessage)

		err = msgRepo.Create(futureMessage)
		assert.Nil(t, err)

		// Get messages ready for dispatch
		readyMessages := msgRepo.GetMessagesReadyForDispatch(10)
		assert.GreaterOrEqual(t, len(readyMessages), 1)
		found := false
		for _, msg := range readyMessages {
			if msg.ID.String() == pastMessage.ID.String() {
				found = true
				break
			}
		}
		assert.True(t, found, "Past message should be in ready messages")

		// Cleanup
		_, err = executeUpdateQuery(testDB, "DELETE FROM scheduled_message WHERE id IN (?, ?)",
			[]interface{}{pastMessage.ID, futureMessage.ID})
		assert.Nil(t, err)
	})

	t.Run("LimitRespected", func(t *testing.T) {
		msgRepo := getScheduledMessageRepository()

		var messageIDs []interface{}

		// Create multiple past messages
		for i := 0; i < 5; i++ {
			pastDispatchTime := time.Now().Add(time.Duration(-5-i) * time.Minute)
			pastMessage, err := data.NewScheduledMessage(scheduledTestChannel, scheduledTestProducer, "past-payload", "test/content-type", pastDispatchTime, data.HeadersMap{})
			assert.Nil(t, err)
			assert.NotNil(t, pastMessage)

			// Force the dispatch schedule to be in the past
			pastMessage.DispatchSchedule = pastDispatchTime
			err = msgRepo.Create(pastMessage)
			assert.Nil(t, err)

			messageIDs = append(messageIDs, pastMessage.ID)
		}

		// Get messages with limit of 3
		readyMessages := msgRepo.GetMessagesReadyForDispatch(3)
		assert.LessOrEqual(t, len(readyMessages), 3)

		// Build DELETE query with placeholders
		placeholders := ""
		for i := range messageIDs {
			if i > 0 {
				placeholders += ", "
			}
			placeholders += "?"
		}
		_, err := executeUpdateQuery(testDB, "DELETE FROM scheduled_message WHERE id IN ("+placeholders+")", messageIDs)
		assert.Nil(t, err)
	})
}

func TestScheduledMessageGetScheduledMessagesForChannel(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		msgRepo := getScheduledMessageRepository()

		var messageIDs []interface{}

		// Create a mix of scheduled and dispatched messages
		for i := 0; i < 3; i++ {
			dispatchTime := time.Now().Add(time.Duration(i) * time.Minute)
			message, err := data.NewScheduledMessage(scheduledTestChannel, scheduledTestProducer, "test-payload", "test/content-type", dispatchTime, data.HeadersMap{})
			assert.Nil(t, err)
			assert.NotNil(t, message)

			// Force the dispatch schedule
			message.DispatchSchedule = dispatchTime
			err = msgRepo.Create(message)
			assert.Nil(t, err)

			messageIDs = append(messageIDs, message.ID)
		}

		// Mark one message as dispatched
		msgID := messageIDs[0].(xid.ID).String()
		firstMsg, err := msgRepo.GetByID(msgID)
		assert.Nil(t, err)
		firstMsg.Status = data.ScheduledMsgStatusDispatched
		firstMsg.DispatchedAt = time.Now()
		err = msgRepo.MarkDispatched(firstMsg)
		assert.Nil(t, err)

		// Get all messages
		pagination := data.NewPagination(nil, nil)

		messages, _, err := msgRepo.GetScheduledMessagesForChannel(scheduledTestChannel.ChannelID, pagination)
		assert.Nil(t, err)
		assert.GreaterOrEqual(t, len(messages), 3)

		// Get only scheduled messages
		pagination = data.NewPagination(nil, nil)

		messages, _, err = msgRepo.GetScheduledMessagesForChannel(scheduledTestChannel.ChannelID, pagination, data.ScheduledMsgStatusScheduled)
		assert.Nil(t, err)
		for _, msg := range messages {
			assert.Equal(t, data.ScheduledMsgStatusScheduled, msg.Status)
		}

		// Get only dispatched messages
		pagination = data.NewPagination(nil, nil)

		messages, _, err = msgRepo.GetScheduledMessagesForChannel(scheduledTestChannel.ChannelID, pagination, data.ScheduledMsgStatusDispatched)
		assert.Nil(t, err)
		dispatchedCount := 0
		for _, msg := range messages {
			if msg.Status == data.ScheduledMsgStatusDispatched {
				dispatchedCount++
			}
		}
		assert.GreaterOrEqual(t, dispatchedCount, 1)

		// Cleanup
		placeholders := ""
		for i := range messageIDs {
			if i > 0 {
				placeholders += ", "
			}
			placeholders += "?"
		}
		_, err = executeUpdateQuery(testDB, "DELETE FROM scheduled_message WHERE id IN ("+placeholders+")", messageIDs)
		assert.Nil(t, err)
	})

	t.Run("Pagination", func(t *testing.T) {
		msgRepo := getScheduledMessageRepository()

		var messageIDs []interface{}

		// Create multiple scheduled messages
		for i := 0; i < 5; i++ {
			dispatchTime := time.Now().Add(time.Duration(i) * time.Minute)
			message, err := data.NewScheduledMessage(scheduledTestChannel, scheduledTestProducer, "test-payload", "test/content-type", dispatchTime, data.HeadersMap{})
			assert.Nil(t, err)
			assert.NotNil(t, message)

			// Force the dispatch schedule
			message.DispatchSchedule = dispatchTime
			err = msgRepo.Create(message)
			assert.Nil(t, err)

			messageIDs = append(messageIDs, message.ID)
		}

		// Get first page - should get all messages without pagination
		pagination := data.NewPagination(nil, nil)
		messages, _, err := msgRepo.GetScheduledMessagesForChannel(scheduledTestChannel.ChannelID, pagination)
		assert.Nil(t, err)
		assert.GreaterOrEqual(t, len(messages), 1)

		// If we have messages, test pagination with a cursor
		if len(messages) > 0 {
			// Create pagination for next page
			firstPagePagination := data.NewPagination(messages[0], nil)

			// Get the next page
			messages2, _, err := msgRepo.GetScheduledMessagesForChannel(scheduledTestChannel.ChannelID, firstPagePagination)
			assert.Nil(t, err)

			log.Info().Int("messages1", len(messages)).Int("messages2", len(messages2)).Msg("Pagination test")
		}

		// Cleanup
		placeholders := ""
		for i := range messageIDs {
			if i > 0 {
				placeholders += ", "
			}
			placeholders += "?"
		}
		_, err = executeUpdateQuery(testDB, "DELETE FROM scheduled_message WHERE id IN ("+placeholders+")", messageIDs)
		assert.Nil(t, err)
	})
}

func TestScheduledMessageGetScheduledMessageStatusCountsByChannel(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		msgRepo := getScheduledMessageRepository()

		var messageIDs []interface{}

		// Create a mix of scheduled and dispatched messages
		for i := 0; i < 3; i++ {
			dispatchTime := time.Now().Add(time.Duration(i) * time.Minute)
			message, err := data.NewScheduledMessage(scheduledTestChannel, scheduledTestProducer, "test-payload", "test/content-type", dispatchTime, data.HeadersMap{})
			assert.Nil(t, err)
			assert.NotNil(t, message)

			// Force the dispatch schedule
			message.DispatchSchedule = dispatchTime
			err = msgRepo.Create(message)
			assert.Nil(t, err)

			messageIDs = append(messageIDs, message.ID)
		}

		// Mark one message as dispatched
		msgID := messageIDs[0].(xid.ID).String()
		firstMsg, err := msgRepo.GetByID(msgID)
		assert.Nil(t, err)
		firstMsg.Status = data.ScheduledMsgStatusDispatched
		firstMsg.DispatchedAt = time.Now()
		err = msgRepo.MarkDispatched(firstMsg)
		assert.Nil(t, err)

		// Get status counts
		counts, err := msgRepo.GetScheduledMessageStatusCountsByChannel(scheduledTestChannel.ChannelID)
		assert.Nil(t, err)

		var scheduledCount, dispatchedCount int
		for _, count := range counts {
			if count.Status == data.ScheduledMsgStatusScheduled {
				scheduledCount = count.Count
			} else if count.Status == data.ScheduledMsgStatusDispatched {
				dispatchedCount = count.Count
			}
		}

		assert.GreaterOrEqual(t, scheduledCount, 2)
		assert.GreaterOrEqual(t, dispatchedCount, 1)

		// Cleanup
		placeholders := ""
		for i := range messageIDs {
			if i > 0 {
				placeholders += ", "
			}
			placeholders += "?"
		}
		_, err = executeUpdateQuery(testDB, "DELETE FROM scheduled_message WHERE id IN ("+placeholders+")", messageIDs)
		assert.Nil(t, err)
	})

	t.Run("EmptyChannel", func(t *testing.T) {
		msgRepo := getScheduledMessageRepository()
		_, err := msgRepo.GetScheduledMessageStatusCountsByChannel("non-existent-channel")
		assert.Nil(t, err)
	})
}

func TestScheduledMessageGetNextScheduledMessageTime(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		msgRepo := getScheduledMessageRepository()

		var messageIDs []interface{}

		// Create multiple scheduled messages with different times
		for i := 0; i < 3; i++ {
			dispatchTime := time.Now().Add(time.Duration(i+1) * time.Minute)
			message, err := data.NewScheduledMessage(scheduledTestChannel, scheduledTestProducer, "test-payload", "test/content-type", dispatchTime, data.HeadersMap{})
			assert.Nil(t, err)
			assert.NotNil(t, message)

			// Force the dispatch schedule
			message.DispatchSchedule = dispatchTime
			err = msgRepo.Create(message)
			assert.Nil(t, err)

			messageIDs = append(messageIDs, message.ID)
		}

		// Get next scheduled time
		nextTime, err := msgRepo.GetNextScheduledMessageTime(scheduledTestChannel.ChannelID)
		assert.Nil(t, err)
		assert.NotNil(t, nextTime)

		// Verify it's within the expected time range
		expectedTime := time.Now().Add(1 * time.Minute)
		timeDiff := nextTime.Sub(expectedTime)
		assert.True(t, timeDiff.Seconds() < 60, "Next scheduled time should be close to expected time")

		// Cleanup
		placeholders := ""
		for i := range messageIDs {
			if i > 0 {
				placeholders += ", "
			}
			placeholders += "?"
		}
		_, err = executeUpdateQuery(testDB, "DELETE FROM scheduled_message WHERE id IN ("+placeholders+")", messageIDs)
		assert.Nil(t, err)
	})

	t.Run("NoMessages", func(t *testing.T) {
		msgRepo := getScheduledMessageRepository()
		nextTime, err := msgRepo.GetNextScheduledMessageTime("non-existent-channel")
		assert.Nil(t, err)
		assert.Nil(t, nextTime)
	})
}
