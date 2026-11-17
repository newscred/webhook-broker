package scheduler

import (
	"errors"
	"testing"
	"time"

	dispatcherMocks "github.com/newscred/webhook-broker/dispatcher/mocks"
	"github.com/newscred/webhook-broker/storage"
	"github.com/newscred/webhook-broker/storage/data"
	"github.com/newscred/webhook-broker/storage/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
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

func defaultMockSchedulerConfig() *mockSchedulerConfig {
	return &mockSchedulerConfig{
		schedulerIntervalMs: 5 * time.Millisecond,
		minScheduleDelay:    2 * time.Minute,
		schedulerBatchSize:  10,
	}
}

func TestNewMessageScheduler(t *testing.T) {
	scheduledMsgRepo := &mocks.ScheduledMessageRepository{}
	dispatcherSvc := &dispatcherMocks.MessageDispatcher{}
	schedulerCfg := defaultMockSchedulerConfig()
	lockRepo := &mocks.LockRepository{}

	t.Run("valid configuration", func(t *testing.T) {
		assert.NotNil(t, NewMessageScheduler(NewSchedulerConfiguration(scheduledMsgRepo, dispatcherSvc, lockRepo, schedulerCfg)))
	})

	t.Run("nil configuration", func(t *testing.T) {
		assert.Panics(t, func() {
			NewMessageScheduler(&SchedulerConfiguration{DispatcherSvc: dispatcherSvc, SchedulerCfg: schedulerCfg, LockRepo: lockRepo})
		})
		assert.Panics(t, func() {
			NewMessageScheduler(&SchedulerConfiguration{ScheduledMsgRepo: scheduledMsgRepo, SchedulerCfg: schedulerCfg, LockRepo: lockRepo})
		})
		assert.Panics(t, func() {
			NewMessageScheduler(&SchedulerConfiguration{ScheduledMsgRepo: scheduledMsgRepo, DispatcherSvc: dispatcherSvc, LockRepo: lockRepo})
		})
		assert.Panics(t, func() {
			NewMessageScheduler(&SchedulerConfiguration{ScheduledMsgRepo: scheduledMsgRepo, DispatcherSvc: dispatcherSvc, SchedulerCfg: schedulerCfg})
		})
	})
}

func TestStartStop(t *testing.T) {
	scheduledMsgRepo := &mocks.ScheduledMessageRepository{}
	scheduledMsgRepo.On("GetMessagesReadyForDispatch", 10).Return([]*data.ScheduledMessage{})

	scheduler := NewMessageScheduler(NewSchedulerConfiguration(scheduledMsgRepo, &dispatcherMocks.MessageDispatcher{}, &mocks.LockRepository{}, defaultMockSchedulerConfig()))

	scheduler.Start()
	time.Sleep(10 * time.Millisecond)
	scheduler.Stop()

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

// newMockedScheduler returns a scheduler whose lock is always granted and whose re-read under the lock
// reports currentStatus for the scheduled message.
func newMockedScheduler(t *testing.T, scheduledMsg *data.ScheduledMessage, currentStatus data.ScheduledMsgStatus) (*MessageSchedulerImpl, *mocks.ScheduledMessageRepository, *dispatcherMocks.MessageDispatcher) {
	scheduledMsgRepo := &mocks.ScheduledMessageRepository{}
	dispatcherSvc := &dispatcherMocks.MessageDispatcher{}
	lockRepo := &mocks.LockRepository{}
	lockRepo.On("TryLock", mock.Anything).Return(nil)
	lockRepo.On("ReleaseLock", mock.Anything).Return(nil)

	current := *scheduledMsg
	current.Status = currentStatus
	scheduledMsgRepo.On("GetByID", scheduledMsg.ID.String()).Return(&current, nil)

	scheduler := NewMessageScheduler(NewSchedulerConfiguration(scheduledMsgRepo, dispatcherSvc, lockRepo, defaultMockSchedulerConfig()))
	return scheduler.(*MessageSchedulerImpl), scheduledMsgRepo, dispatcherSvc
}

func TestProcessScheduledMessages(t *testing.T) {
	scheduledMsg := createTestScheduledMessage(t)
	impl, scheduledMsgRepo, dispatcherSvc := newMockedScheduler(t, scheduledMsg, data.ScheduledMsgStatusScheduled)
	scheduledMsgRepo.On("GetMessagesReadyForDispatch", 10).Return([]*data.ScheduledMessage{scheduledMsg})
	scheduledMsgRepo.On("DispatchAsMessage", scheduledMsg, mock.MatchedBy(func(msg *data.Message) bool {
		return msg.MessageID == "test-message-id"
	})).Return(nil)

	dispatched := make(chan struct{})
	dispatcherSvc.On("Dispatch", mock.MatchedBy(func(msg *data.Message) bool {
		return msg.MessageID == "test-message-id"
	})).Run(func(args mock.Arguments) { close(dispatched) }).Return()

	impl.processScheduledMessages()

	select {
	case <-dispatched:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Timed out waiting for dispatch to be called")
	}
	assert.Equal(t, data.ScheduledMsgStatusDispatched, scheduledMsg.Status)
}

func TestAlreadyDispatchedMessageIsSkipped(t *testing.T) {
	scheduledMsg := createTestScheduledMessage(t)
	impl, scheduledMsgRepo, dispatcherSvc := newMockedScheduler(t, scheduledMsg, data.ScheduledMsgStatusDispatched)

	impl.dispatchMessage(scheduledMsg)

	scheduledMsgRepo.AssertNotCalled(t, "DispatchAsMessage", mock.Anything, mock.Anything)
	dispatcherSvc.AssertNotCalled(t, "Dispatch", mock.Anything)
	assert.Equal(t, uint64(0), impl.metricsCollector.SchedulingErrors)
}

func TestDispatchedByAnotherReplicaAfterReRead(t *testing.T) {
	scheduledMsg := createTestScheduledMessage(t)
	impl, scheduledMsgRepo, dispatcherSvc := newMockedScheduler(t, scheduledMsg, data.ScheduledMsgStatusScheduled)
	scheduledMsgRepo.On("DispatchAsMessage", scheduledMsg, mock.Anything).Return(storage.ErrNoRowsUpdated)

	impl.dispatchMessage(scheduledMsg)

	dispatcherSvc.AssertNotCalled(t, "Dispatch", mock.Anything)
	assert.Equal(t, uint64(0), impl.metricsCollector.SchedulingErrors)
}

func TestErrorHandling(t *testing.T) {
	t.Run("ReReadFails", func(t *testing.T) {
		scheduledMsg := createTestScheduledMessage(t)
		scheduledMsgRepo := &mocks.ScheduledMessageRepository{}
		dispatcherSvc := &dispatcherMocks.MessageDispatcher{}
		lockRepo := &mocks.LockRepository{}
		lockRepo.On("TryLock", mock.Anything).Return(nil)
		lockRepo.On("ReleaseLock", mock.Anything).Return(nil)
		scheduledMsgRepo.On("GetByID", scheduledMsg.ID.String()).Return(nil, errors.New("database error"))
		impl := NewMessageScheduler(NewSchedulerConfiguration(scheduledMsgRepo, dispatcherSvc, lockRepo, defaultMockSchedulerConfig())).(*MessageSchedulerImpl)

		impl.dispatchMessage(scheduledMsg)

		scheduledMsgRepo.AssertNotCalled(t, "DispatchAsMessage", mock.Anything, mock.Anything)
		dispatcherSvc.AssertNotCalled(t, "Dispatch", mock.Anything)
		assert.Equal(t, uint64(1), impl.metricsCollector.SchedulingErrors)
	})

	t.Run("WriteFails", func(t *testing.T) {
		scheduledMsg := createTestScheduledMessage(t)
		impl, scheduledMsgRepo, dispatcherSvc := newMockedScheduler(t, scheduledMsg, data.ScheduledMsgStatusScheduled)
		scheduledMsgRepo.On("DispatchAsMessage", scheduledMsg, mock.Anything).Return(errors.New("database error"))

		impl.dispatchMessage(scheduledMsg)

		dispatcherSvc.AssertNotCalled(t, "Dispatch", mock.Anything)
		assert.Equal(t, uint64(1), impl.metricsCollector.SchedulingErrors)
	})
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
