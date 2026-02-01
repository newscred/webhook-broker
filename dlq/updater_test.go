package dlq

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/newscred/webhook-broker/dispatcher"
	"github.com/newscred/webhook-broker/storage"
	"github.com/newscred/webhook-broker/storage/data"
	"github.com/newscred/webhook-broker/storage/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

type mockDLQConfig struct {
	interval time.Duration
}

func (m *mockDLQConfig) GetDLQSummaryUpdateInterval() time.Duration {
	return m.interval
}

func newTestConfig() *DLQSummaryUpdaterConfiguration {
	return &DLQSummaryUpdaterConfiguration{
		DLQSummaryRepo:  new(mocks.DLQSummaryRepository),
		DeliveryJobRepo: new(mocks.DeliveryJobRepository),
		ConsumerRepo:    new(mocks.ConsumerRepository),
		ChannelRepo:     new(mocks.ChannelRepository),
		LockRepo:        new(mocks.LockRepository),
		DLQConfig:       &mockDLQConfig{interval: 5 * time.Millisecond},
		Metrics:         dispatcher.NewMetricsContainer(),
	}
}

func TestNewDLQSummaryUpdater(t *testing.T) {
	t.Run("ValidConfiguration", func(t *testing.T) {
		cfg := newTestConfig()
		updater := NewDLQSummaryUpdater(cfg)
		assert.NotNil(t, updater)
	})

	t.Run("NilDLQSummaryRepo", func(t *testing.T) {
		cfg := newTestConfig()
		cfg.DLQSummaryRepo = nil
		assert.Panics(t, func() { NewDLQSummaryUpdater(cfg) })
	})

	t.Run("NilDeliveryJobRepo", func(t *testing.T) {
		cfg := newTestConfig()
		cfg.DeliveryJobRepo = nil
		assert.Panics(t, func() { NewDLQSummaryUpdater(cfg) })
	})

	t.Run("NilConsumerRepo", func(t *testing.T) {
		cfg := newTestConfig()
		cfg.ConsumerRepo = nil
		assert.Panics(t, func() { NewDLQSummaryUpdater(cfg) })
	})

	t.Run("NilChannelRepo", func(t *testing.T) {
		cfg := newTestConfig()
		cfg.ChannelRepo = nil
		assert.Panics(t, func() { NewDLQSummaryUpdater(cfg) })
	})

	t.Run("NilLockRepo", func(t *testing.T) {
		cfg := newTestConfig()
		cfg.LockRepo = nil
		assert.Panics(t, func() { NewDLQSummaryUpdater(cfg) })
	})

	t.Run("NilDLQConfig", func(t *testing.T) {
		cfg := newTestConfig()
		cfg.DLQConfig = nil
		assert.Panics(t, func() { NewDLQSummaryUpdater(cfg) })
	})

	t.Run("NilMetrics", func(t *testing.T) {
		cfg := newTestConfig()
		cfg.Metrics = nil
		assert.Panics(t, func() { NewDLQSummaryUpdater(cfg) })
	})
}

func TestStartStop(t *testing.T) {
	cfg := newTestConfig()
	lockRepo := cfg.LockRepo.(*mocks.LockRepository)
	lockRepo.On("TryLock", mock.Anything).Return(storage.ErrAlreadyLocked)

	updater := NewDLQSummaryUpdater(cfg)
	updater.Start()
	time.Sleep(15 * time.Millisecond)
	updater.Stop()
	// If it didn't hang, the test passes
}

func TestProcessUpdateBootstrap(t *testing.T) {
	cfg := newTestConfig()
	lockRepo := cfg.LockRepo.(*mocks.LockRepository)
	summaryRepo := cfg.DLQSummaryRepo.(*mocks.DLQSummaryRepository)

	lockRepo.On("TryLock", mock.Anything).Return(nil)
	lockRepo.On("ReleaseLock", mock.Anything).Return(nil)
	summaryRepo.On("GetLastCheckedAt").Return(time.Time{}, nil)
	summaryRepo.On("BootstrapCounts", (*sql.DB)(nil)).Return(nil)
	summaryRepo.On("GetAll").Return([]*data.DLQSummary{
		{ConsumerID: "c1", ChannelID: "ch1", DeadCount: 5},
	}, nil)

	updater := NewDLQSummaryUpdater(cfg)
	impl := updater.(*DLQSummaryUpdaterImpl)
	impl.processUpdate()

	summaryRepo.AssertCalled(t, "BootstrapCounts", (*sql.DB)(nil))
	summaryRepo.AssertCalled(t, "GetAll")
}

func TestProcessUpdateIncremental(t *testing.T) {
	cfg := newTestConfig()
	cfg.DLQConfig = &mockDLQConfig{interval: 1 * time.Millisecond}
	lockRepo := cfg.LockRepo.(*mocks.LockRepository)
	summaryRepo := cfg.DLQSummaryRepo.(*mocks.DLQSummaryRepository)
	djRepo := cfg.DeliveryJobRepo.(*mocks.DeliveryJobRepository)
	consumerRepo := cfg.ConsumerRepo.(*mocks.ConsumerRepository)

	lockRepo.On("TryLock", mock.Anything).Return(nil)
	lockRepo.On("ReleaseLock", mock.Anything).Return(nil)
	// Return a time far enough in the past
	summaryRepo.On("GetLastCheckedAt").Return(time.Now().Add(-1*time.Hour), nil)
	djRepo.On("GetDeadJobCountsSinceCheckpoint", mock.AnythingOfType("time.Time")).Return(map[string]int64{"c1": 3}, nil)

	consumer := &data.Consumer{}
	consumer.Name = "consumer-1"
	consumer.ConsumingFrom = &data.Channel{ChannelID: "ch1"}
	consumer.ConsumingFrom.Name = "channel-1"
	consumerRepo.On("GetByID", "c1").Return(consumer, nil)
	summaryRepo.On("UpsertCounts", mock.AnythingOfType("[]*data.DLQSummary")).Return(nil)
	summaryRepo.On("GetAll").Return([]*data.DLQSummary{}, nil)

	updater := NewDLQSummaryUpdater(cfg)
	impl := updater.(*DLQSummaryUpdaterImpl)
	impl.processUpdate()

	djRepo.AssertCalled(t, "GetDeadJobCountsSinceCheckpoint", mock.Anything)
	summaryRepo.AssertCalled(t, "UpsertCounts", mock.Anything)
}

func TestProcessUpdateSkipRecentUpdate(t *testing.T) {
	cfg := newTestConfig()
	cfg.DLQConfig = &mockDLQConfig{interval: 10 * time.Minute}
	lockRepo := cfg.LockRepo.(*mocks.LockRepository)
	summaryRepo := cfg.DLQSummaryRepo.(*mocks.DLQSummaryRepository)
	djRepo := cfg.DeliveryJobRepo.(*mocks.DeliveryJobRepository)

	lockRepo.On("TryLock", mock.Anything).Return(nil)
	lockRepo.On("ReleaseLock", mock.Anything).Return(nil)
	// Return a recent time — within the interval
	summaryRepo.On("GetLastCheckedAt").Return(time.Now().Add(-1*time.Second), nil)

	updater := NewDLQSummaryUpdater(cfg)
	impl := updater.(*DLQSummaryUpdaterImpl)
	impl.processUpdate()

	// Should NOT call GetDeadJobCountsSinceCheckpoint or BootstrapCounts
	djRepo.AssertNotCalled(t, "GetDeadJobCountsSinceCheckpoint", mock.Anything)
	summaryRepo.AssertNotCalled(t, "BootstrapCounts", mock.Anything)
	summaryRepo.AssertNotCalled(t, "GetAll")
}

func TestProcessUpdateLockContention(t *testing.T) {
	cfg := newTestConfig()
	lockRepo := cfg.LockRepo.(*mocks.LockRepository)
	summaryRepo := cfg.DLQSummaryRepo.(*mocks.DLQSummaryRepository)

	lockRepo.On("TryLock", mock.Anything).Return(storage.ErrAlreadyLocked)

	updater := NewDLQSummaryUpdater(cfg)
	impl := updater.(*DLQSummaryUpdaterImpl)
	impl.processUpdate()

	summaryRepo.AssertNotCalled(t, "GetLastCheckedAt")
}

func TestProcessUpdateLockError(t *testing.T) {
	cfg := newTestConfig()
	lockRepo := cfg.LockRepo.(*mocks.LockRepository)
	summaryRepo := cfg.DLQSummaryRepo.(*mocks.DLQSummaryRepository)

	lockRepo.On("TryLock", mock.Anything).Return(errors.New("lock error"))

	updater := NewDLQSummaryUpdater(cfg)
	impl := updater.(*DLQSummaryUpdaterImpl)
	impl.processUpdate()

	summaryRepo.AssertNotCalled(t, "GetLastCheckedAt")
}

func TestProcessUpdateGetLastCheckedAtError(t *testing.T) {
	cfg := newTestConfig()
	lockRepo := cfg.LockRepo.(*mocks.LockRepository)
	summaryRepo := cfg.DLQSummaryRepo.(*mocks.DLQSummaryRepository)

	lockRepo.On("TryLock", mock.Anything).Return(nil)
	lockRepo.On("ReleaseLock", mock.Anything).Return(nil)
	summaryRepo.On("GetLastCheckedAt").Return(time.Time{}, errors.New("db error"))

	updater := NewDLQSummaryUpdater(cfg)
	impl := updater.(*DLQSummaryUpdaterImpl)
	impl.processUpdate()

	summaryRepo.AssertNotCalled(t, "BootstrapCounts", mock.Anything)
}

func TestProcessUpdateIncrementalConsumerLookupError(t *testing.T) {
	cfg := newTestConfig()
	cfg.DLQConfig = &mockDLQConfig{interval: 1 * time.Millisecond}
	lockRepo := cfg.LockRepo.(*mocks.LockRepository)
	summaryRepo := cfg.DLQSummaryRepo.(*mocks.DLQSummaryRepository)
	djRepo := cfg.DeliveryJobRepo.(*mocks.DeliveryJobRepository)
	consumerRepo := cfg.ConsumerRepo.(*mocks.ConsumerRepository)

	lockRepo.On("TryLock", mock.Anything).Return(nil)
	lockRepo.On("ReleaseLock", mock.Anything).Return(nil)
	summaryRepo.On("GetLastCheckedAt").Return(time.Now().Add(-1*time.Hour), nil)
	djRepo.On("GetDeadJobCountsSinceCheckpoint", mock.AnythingOfType("time.Time")).Return(map[string]int64{"c1": 3}, nil)
	consumerRepo.On("GetByID", "c1").Return(nil, errors.New("not found"))
	// Empty summaries — skipped consumer means empty upsert, but UpsertCounts may still be called with empty slice
	// Actually looking at the code: if len(counts) > 0 but all consumers fail lookup, summaries is empty, so UpsertCounts with empty slice is called
	summaryRepo.On("UpsertCounts", mock.AnythingOfType("[]*data.DLQSummary")).Return(nil)
	summaryRepo.On("GetAll").Return([]*data.DLQSummary{}, nil)

	updater := NewDLQSummaryUpdater(cfg)
	impl := updater.(*DLQSummaryUpdaterImpl)
	impl.processUpdate()

	consumerRepo.AssertCalled(t, "GetByID", "c1")
}

// Generated with assistance from Claude AI
