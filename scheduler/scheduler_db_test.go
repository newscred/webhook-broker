package scheduler

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/newscred/webhook-broker/config"
	dispatcherMocks "github.com/newscred/webhook-broker/dispatcher/mocks"
	"github.com/newscred/webhook-broker/storage"
	"github.com/newscred/webhook-broker/storage/data"
	"github.com/rs/xid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

var dataAccessor storage.DataAccessor

func TestMain(m *testing.M) {
	if err := os.Remove("./webhook-broker.sqlite3"); err != nil && !os.IsNotExist(err) {
		panic(err)
	}
	configuration, err := config.GetAutoConfiguration()
	if err != nil {
		panic(err)
	}
	migrationLocation, _ := filepath.Abs("../migration/sqls/")
	dataAccessor, err = storage.GetNewDataAccessor(configuration, &storage.MigrationConfig{MigrationEnabled: true, MigrationSource: "file://" + migrationLocation}, configuration)
	if err != nil {
		panic(err)
	}
	code := m.Run()
	dataAccessor.Close()
	os.Exit(code)
}

// countingScheduledMessageRepository counts write attempts so a test can prove a replica never tried one.
type countingScheduledMessageRepository struct {
	storage.ScheduledMessageRepository
	dispatchAttempts *int32
}

func (repo countingScheduledMessageRepository) DispatchAsMessage(scheduledMsg *data.ScheduledMessage, message *data.Message) error {
	atomic.AddInt32(repo.dispatchAttempts, 1)
	return repo.ScheduledMessageRepository.DispatchAsMessage(scheduledMsg, message)
}

func createDueScheduledMessage(t *testing.T) *data.ScheduledMessage {
	suffix := xid.New().String()
	channel, err := data.NewChannel("scheduler-db-channel-"+suffix, "token")
	require.NoError(t, err)
	channel, err = dataAccessor.GetChannelRepository().Store(channel)
	require.NoError(t, err)
	producer, err := data.NewProducer("scheduler-db-producer-"+suffix, "token")
	require.NoError(t, err)
	producer, err = dataAccessor.GetProducerRepository().Store(producer)
	require.NoError(t, err)
	scheduledMsg, err := data.NewScheduledMessage(channel, producer, "payload", "application/json", time.Now().Add(-time.Minute), data.HeadersMap{})
	require.NoError(t, err)
	require.NoError(t, dataAccessor.GetScheduledMessageRepository().Create(scheduledMsg))
	return scheduledMsg
}

func newDBScheduler(dispatcherSvc *dispatcherMocks.MessageDispatcher, dispatchAttempts *int32) *MessageSchedulerImpl {
	repo := countingScheduledMessageRepository{ScheduledMessageRepository: dataAccessor.GetScheduledMessageRepository(), dispatchAttempts: dispatchAttempts}
	return NewMessageScheduler(NewSchedulerConfiguration(repo, dispatcherSvc, dataAccessor.GetLockRepository(), defaultMockSchedulerConfig())).(*MessageSchedulerImpl)
}

func findReady(t *testing.T, scheduledMsg *data.ScheduledMessage) *data.ScheduledMessage {
	for _, ready := range dataAccessor.GetScheduledMessageRepository().GetMessagesReadyForDispatch(1000) {
		if ready.ID == scheduledMsg.ID {
			return ready
		}
	}
	t.Fatalf("scheduled message %s not returned as ready for dispatch", scheduledMsg.ID)
	return nil
}

func TestSchedulerTickDispatchesAgainstDatabase(t *testing.T) {
	scheduledMsg := createDueScheduledMessage(t)
	dispatched := make(chan struct{})
	dispatcherSvc := &dispatcherMocks.MessageDispatcher{}
	dispatcherSvc.On("Dispatch", mock.Anything).Run(func(args mock.Arguments) {
		if args.Get(0).(*data.Message).MessageID == scheduledMsg.MessageID {
			close(dispatched)
		}
	}).Return()
	var attempts int32
	impl := newDBScheduler(dispatcherSvc, &attempts)

	impl.processScheduledMessages()

	select {
	case <-dispatched:
	case <-time.After(5 * time.Second):
		t.Fatal("scheduler tick did not dispatch the due scheduled message")
	}
	stored, err := dataAccessor.GetScheduledMessageRepository().GetByID(scheduledMsg.ID.String())
	require.NoError(t, err)
	assert.Equal(t, data.ScheduledMsgStatusDispatched, stored.Status)
	_, err = dataAccessor.GetMessageRepository().Get(scheduledMsg.BroadcastedTo.ChannelID, scheduledMsg.MessageID)
	assert.NoError(t, err)
}

// Two replicas read the same batch; the second acquires the per-message lock only after the first released it.
func TestSecondReplicaWithStaleBatchDoesNotRedispatch(t *testing.T) {
	scheduledMsg := createDueScheduledMessage(t)
	firstSnapshot := findReady(t, scheduledMsg)
	secondSnapshot := findReady(t, scheduledMsg)
	var attempts int32

	firstDispatcher := &dispatcherMocks.MessageDispatcher{}
	firstDispatcher.On("Dispatch", mock.Anything).Return()
	first := newDBScheduler(firstDispatcher, &attempts)
	first.dispatchMessage(firstSnapshot)

	secondDispatcher := &dispatcherMocks.MessageDispatcher{}
	second := newDBScheduler(secondDispatcher, &attempts)
	second.dispatchMessage(secondSnapshot)

	firstDispatcher.AssertNumberOfCalls(t, "Dispatch", 1)
	secondDispatcher.AssertNotCalled(t, "Dispatch", mock.Anything)
	assert.Equal(t, int32(1), atomic.LoadInt32(&attempts))
	assert.Equal(t, uint64(0), second.metricsCollector.SchedulingErrors)
}
