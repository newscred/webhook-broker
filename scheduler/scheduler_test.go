package scheduler

import (
	"errors"
	dispatcherMocks "github.com/newscred/webhook-broker/dispatcher/mocks"
	"github.com/newscred/webhook-broker/storage"
	"github.com/newscred/webhook-broker/storage/data"
	"github.com/newscred/webhook-broker/storage/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"sync"
	"testing"
	"time"
)

type mockSchedulerConfig struct {
	schedulerIntervalMs time.Duration
	minScheduleDelay    time.Duration
	schedulerBatchSize  int
}

func (m *mockSchedulerConfig) GetSchedulerInterval() time.Duration {
	return m.schedulerIntervalMs
}

func (m *mockSchedulerConfig) GetMinScheduleDelay() time.Duration {
	return m.minScheduleDelay
}

func (m *mockSchedulerConfig) GetSchedulerBatchSize() int {
	return m.schedulerBatchSize
}

func TestNewMessageScheduler(t *testing.T) {
	t.Run("valid configuration", func(t *testing.T) {
		// Arrange
		scheduledMsgRepo := &mocks.ScheduledMessageRepository{}
		msgRepo := &mocks.MessageRepository{}
		dispatcherSvc := &dispatcherMocks.MessageDispatcher{}
		schedulerCfg := &mockSchedulerConfig{
			schedulerIntervalMs: 5 * time.Millisecond,
			minScheduleDelay:    2 * time.Minute,
			schedulerBatchSize:  10,
		}
		lockRepo := &mocks.LockRepository{}

		// Act
		scheduler := NewMessageScheduler(&SchedulerConfiguration{
			ScheduledMsgRepo: scheduledMsgRepo,
			MsgRepo:          msgRepo,
			DispatcherSvc:    dispatcherSvc,
			SchedulerCfg:     schedulerCfg,
			LockRepo:         lockRepo,
		})

		// Assert
		assert.NotNil(t, scheduler)
	})

	t.Run("nil configuration", func(t *testing.T) {
		// Arrange
		scheduledMsgRepo := &mocks.ScheduledMessageRepository{}
		msgRepo := &mocks.MessageRepository{}
		dispatcherSvc := &dispatcherMocks.MessageDispatcher{}
		schedulerCfg := &mockSchedulerConfig{}
		lockRepo := &mocks.LockRepository{}

		// Act & Assert
		assert.Panics(t, func() {
			NewMessageScheduler(&SchedulerConfiguration{
				ScheduledMsgRepo: nil,
				MsgRepo:          msgRepo,
				DispatcherSvc:    dispatcherSvc,
				SchedulerCfg:     schedulerCfg,
				LockRepo:         lockRepo,
			})
		})

		assert.Panics(t, func() {
			NewMessageScheduler(&SchedulerConfiguration{
				ScheduledMsgRepo: scheduledMsgRepo,
				MsgRepo:          nil,
				DispatcherSvc:    dispatcherSvc,
				SchedulerCfg:     schedulerCfg,
				LockRepo:         lockRepo,
			})
		})

		assert.Panics(t, func() {
			NewMessageScheduler(&SchedulerConfiguration{
				ScheduledMsgRepo: scheduledMsgRepo,
				MsgRepo:          msgRepo,
				DispatcherSvc:    nil,
				SchedulerCfg:     schedulerCfg,
				LockRepo:         lockRepo,
			})
		})

		assert.Panics(t, func() {
			NewMessageScheduler(&SchedulerConfiguration{
				ScheduledMsgRepo: scheduledMsgRepo,
				MsgRepo:          msgRepo,
				DispatcherSvc:    dispatcherSvc,
				SchedulerCfg:     nil,
				LockRepo:         lockRepo,
			})
		})

		assert.Panics(t, func() {
			NewMessageScheduler(&SchedulerConfiguration{
				ScheduledMsgRepo: scheduledMsgRepo,
				MsgRepo:          msgRepo,
				DispatcherSvc:    dispatcherSvc,
				SchedulerCfg:     schedulerCfg,
				LockRepo:         nil,
			})
		})
	})
}

func TestStartStop(t *testing.T) {
	// Arrange
	scheduledMsgRepo := &mocks.ScheduledMessageRepository{}
	msgRepo := &mocks.MessageRepository{}
	dispatcherSvc := &dispatcherMocks.MessageDispatcher{}
	schedulerCfg := &mockSchedulerConfig{
		schedulerIntervalMs: 5 * time.Millisecond,
		minScheduleDelay:    2 * time.Minute,
		schedulerBatchSize:  10,
	}
	lockRepo := &mocks.LockRepository{}

	// Need to mock the GetMessagesReadyForDispatch method to avoid panic
	scheduledMsgRepo.On("GetMessagesReadyForDispatch", 10).Return([]*data.ScheduledMessage{})

	scheduler := NewMessageScheduler(&SchedulerConfiguration{
		ScheduledMsgRepo: scheduledMsgRepo,
		MsgRepo:          msgRepo,
		DispatcherSvc:    dispatcherSvc,
		SchedulerCfg:     schedulerCfg,
		LockRepo:         lockRepo,
	})

	// Act
	scheduler.Start()
	time.Sleep(10 * time.Millisecond) // Give it time to start
	scheduler.Stop()

	// Assert - If it didn't hang, the test passes
	scheduledMsgRepo.AssertCalled(t, "GetMessagesReadyForDispatch", 10)
}

func createTestScheduledMessage(t *testing.T) *data.ScheduledMessage {
	channel, err := data.NewChannel("test-channel", "test-channel-token")
	assert.NoError(t, err)

	producer, err := data.NewProducer("test-producer", "test-producer-token")
	assert.NoError(t, err)

	scheduledTime := time.Now().Add(-5 * time.Minute)
	scheduledMsg, err := data.NewScheduledMessage(channel, producer, "test payload", "application/json", scheduledTime, nil)
	assert.NoError(t, err)

	scheduledMsg.MessageID = "test-message-id"
	return scheduledMsg
}

func TestProcessScheduledMessages(t *testing.T) {
	// Arrange
	scheduledMsgRepo := &mocks.ScheduledMessageRepository{}
	msgRepo := &mocks.MessageRepository{}
	dispatcherSvc := &dispatcherMocks.MessageDispatcher{}
	schedulerCfg := &mockSchedulerConfig{
		schedulerIntervalMs: 50 * time.Millisecond,
		minScheduleDelay:    2 * time.Minute,
		schedulerBatchSize:  10,
	}
	lockRepo := &mocks.LockRepository{}

	// Create test objects
	scheduledMsg := createTestScheduledMessage(t)

	// Set up expectations
	scheduledMsgRepo.On("GetMessagesReadyForDispatch", 10).Return([]*data.ScheduledMessage{scheduledMsg})

	// For locking
	lockRepo.On("TryLock", mock.Anything).Return(nil)
	lockRepo.On("ReleaseLock", mock.Anything).Return(nil)

	// For message creation and dispatching
	msgRepo.On("Create", mock.MatchedBy(func(msg *data.Message) bool {
		return msg.MessageID == "test-message-id"
	})).Return(nil)

	scheduledMsgRepo.On("MarkDispatched", mock.MatchedBy(func(msg *data.ScheduledMessage) bool {
		return msg.MessageID == "test-message-id" &&
			msg.Status == data.ScheduledMsgStatusDispatched
	})).Return(nil)

	// Add synchronization to ensure we can verify the call was made
	wg := sync.WaitGroup{}
	wg.Add(1)

	// Replace Dispatch with a function that signals when it's called
	dispatcherSvc.On("Dispatch", mock.MatchedBy(func(msg *data.Message) bool {
		return msg.MessageID == "test-message-id"
	})).Run(func(args mock.Arguments) {
		wg.Done()
	}).Return()

	// Create the scheduler
	scheduler := NewMessageScheduler(&SchedulerConfiguration{
		ScheduledMsgRepo: scheduledMsgRepo,
		MsgRepo:          msgRepo,
		DispatcherSvc:    dispatcherSvc,
		SchedulerCfg:     schedulerCfg,
		LockRepo:         lockRepo,
	})

	// Act - directly call process rather than using the scheduler goroutine
	impl := scheduler.(*MessageSchedulerImpl)
	impl.processScheduledMessages()

	// Wait for dispatch to be called or timeout
	timeoutChan := time.After(200 * time.Millisecond)
	doneChan := make(chan struct{})

	go func() {
		wg.Wait()
		close(doneChan)
	}()

	// Wait for either completion or timeout
	select {
	case <-doneChan:
		// Success!
	case <-timeoutChan:
		t.Fatal("Timed out waiting for dispatch to be called")
	}

	// Assert
	msgRepo.AssertCalled(t, "Create", mock.Anything)
	scheduledMsgRepo.AssertCalled(t, "MarkDispatched", mock.Anything)
	dispatcherSvc.AssertCalled(t, "Dispatch", mock.Anything)
}

func TestRaceConditionHandling(t *testing.T) {
	// Arrange
	scheduledMsgRepo := &mocks.ScheduledMessageRepository{}
	msgRepo := &mocks.MessageRepository{}
	dispatcherSvc := &dispatcherMocks.MessageDispatcher{}
	schedulerCfg := &mockSchedulerConfig{
		schedulerIntervalMs: 5 * time.Millisecond,
		minScheduleDelay:    2 * time.Minute,
		schedulerBatchSize:  10,
	}
	lockRepo := &mocks.LockRepository{}

	// Create test objects
	scheduledMsg := createTestScheduledMessage(t)

	// Set up expectations
	scheduledMsgRepo.On("GetMessagesReadyForDispatch", 10).Return([]*data.ScheduledMessage{scheduledMsg})

	// For locking
	lockRepo.On("TryLock", mock.Anything).Return(nil)
	lockRepo.On("ReleaseLock", mock.Anything).Return(nil)

	// Simulate duplicate message ID error
	msgRepo.On("Create", mock.MatchedBy(func(msg *data.Message) bool {
		return msg.MessageID == "test-message-id"
	})).Return(storage.ErrDuplicateMessageIDForChannel)

	// Return the message when checking status
	scheduledMsgRepo.On("GetByID", scheduledMsg.ID.String()).Return(scheduledMsg, nil)

	// Create the scheduler
	scheduler := NewMessageScheduler(&SchedulerConfiguration{
		ScheduledMsgRepo: scheduledMsgRepo,
		MsgRepo:          msgRepo,
		DispatcherSvc:    dispatcherSvc,
		SchedulerCfg:     schedulerCfg,
		LockRepo:         lockRepo,
	})

	// Act
	impl := scheduler.(*MessageSchedulerImpl)
	impl.processScheduledMessages()

	// Give time for goroutines to complete
	time.Sleep(200 * time.Millisecond)

	// Assert
	msgRepo.AssertCalled(t, "Create", mock.Anything)
	scheduledMsgRepo.AssertCalled(t, "GetByID", scheduledMsg.ID.String())

	// Dispatch should not be called due to error
	dispatcherSvc.AssertNotCalled(t, "Dispatch", mock.Anything)
}

func TestErrorHandling(t *testing.T) {
	// Arrange
	scheduledMsgRepo := &mocks.ScheduledMessageRepository{}
	msgRepo := &mocks.MessageRepository{}
	dispatcherSvc := &dispatcherMocks.MessageDispatcher{}
	schedulerCfg := &mockSchedulerConfig{
		schedulerIntervalMs: 5 * time.Millisecond,
		minScheduleDelay:    2 * time.Minute,
		schedulerBatchSize:  10,
	}
	lockRepo := &mocks.LockRepository{}

	// Create test objects
	scheduledMsg := createTestScheduledMessage(t)

	// Set up expectations
	scheduledMsgRepo.On("GetMessagesReadyForDispatch", 10).Return([]*data.ScheduledMessage{scheduledMsg})

	// For locking
	lockRepo.On("TryLock", mock.Anything).Return(nil)
	lockRepo.On("ReleaseLock", mock.Anything).Return(nil)

	// Test database error
	msgRepo.On("Create", mock.MatchedBy(func(msg *data.Message) bool {
		return msg.MessageID == "test-message-id"
	})).Return(errors.New("database error"))

	// Create the scheduler
	scheduler := NewMessageScheduler(&SchedulerConfiguration{
		ScheduledMsgRepo: scheduledMsgRepo,
		MsgRepo:          msgRepo,
		DispatcherSvc:    dispatcherSvc,
		SchedulerCfg:     schedulerCfg,
		LockRepo:         lockRepo,
	})

	// Act
	impl := scheduler.(*MessageSchedulerImpl)
	impl.processScheduledMessages()

	// Give time for goroutines to complete
	time.Sleep(200 * time.Millisecond)

	// Assert
	msgRepo.AssertCalled(t, "Create", mock.Anything)

	// These should not be called due to error
	scheduledMsgRepo.AssertNotCalled(t, "MarkDispatched", mock.Anything)
	dispatcherSvc.AssertNotCalled(t, "Dispatch", mock.Anything)

	// Metrics should have recorded an error
	assert.Greater(t, impl.metricsCollector.SchedulingErrors, uint64(0))
}

func TestMetrics(t *testing.T) {
	// Arrange
	m := NewMetricsContainer()

	// Act & Assert
	assert.Equal(t, uint64(0), m.ScheduledMessages)
	assert.Equal(t, uint64(1), m.IncreaseScheduledMessageCount())
	assert.Equal(t, uint64(1), m.ScheduledMessages)

	assert.Equal(t, uint64(0), m.DispatchedScheduledMessages)
	assert.Equal(t, uint64(1), m.IncreaseDispatchedScheduledMessageCount())
	assert.Equal(t, uint64(1), m.DispatchedScheduledMessages)

	assert.Equal(t, uint64(0), m.SchedulingErrors)
	assert.Equal(t, uint64(1), m.IncreaseSchedulingErrorCount())
	assert.Equal(t, uint64(1), m.SchedulingErrors)

	assert.Equal(t, time.Duration(0), m.GetLatestDispatchLag())
	testLag := 5 * time.Second
	m.SetLatestDispatchLag(testLag)
	assert.Equal(t, testLag, m.GetLatestDispatchLag())
}
