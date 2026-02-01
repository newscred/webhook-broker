package storage

import (
	"testing"
	"time"

	"github.com/newscred/webhook-broker/storage/data"
	"github.com/stretchr/testify/assert"
)

var (
	dlqSummaryRepo DLQSummaryRepository
	dlqChannel     *data.Channel
	dlqProducer    *data.Producer
	dlqConsumers   []*data.Consumer
)

func SetupForDLQSummaryTests() {
	dlqSummaryRepo = NewDLQSummaryRepository(testDB)
	channelRepo := NewChannelRepository(testDB)
	producerRepo := NewProducerRepository(testDB)
	consumerRepo := NewConsumerRepository(testDB, channelRepo)
	var err error
	dlqChannel, _ = data.NewChannel("dlq-test-channel", successfulGetTestToken)
	dlqChannel, err = channelRepo.Store(dlqChannel)
	if err != nil {
		panic(err)
	}
	dlqProducer, _ = data.NewProducer("dlq-test-producer", successfulGetTestToken)
	dlqProducer, err = producerRepo.Store(dlqProducer)
	if err != nil {
		panic(err)
	}
	dlqConsumers = SetupForDeliveryJobTestsWithOptions(&DeliveryJobSetupOptions{
		ConsumerCount:    2,
		ConsumerIDPrefix: "dlq-summary-consumer-",
		ConsumerRepo:     consumerRepo,
		ConsumerChannel:  dlqChannel,
	})
}

func TestDLQSummaryUpsertAndGetAll(t *testing.T) {
	now := time.Now()
	summaries := []*data.DLQSummary{
		{
			ConsumerID:    dlqConsumers[0].ID.String(),
			ConsumerName:  dlqConsumers[0].Name,
			ChannelID:     dlqChannel.ChannelID,
			ChannelName:   dlqChannel.Name,
			DeadCount:     5,
			LastCheckedAt: now,
		},
	}

	// Insert new
	err := dlqSummaryRepo.UpsertCounts(summaries)
	assert.NoError(t, err)

	all, err := dlqSummaryRepo.GetAll()
	assert.NoError(t, err)
	found := false
	for _, s := range all {
		if s.ConsumerID == dlqConsumers[0].ID.String() {
			assert.Equal(t, int64(5), s.DeadCount)
			assert.Equal(t, dlqChannel.ChannelID, s.ChannelID)
			found = true
		}
	}
	assert.True(t, found)

	// Increment existing
	summaries[0].DeadCount = 3
	summaries[0].LastCheckedAt = time.Now()
	err = dlqSummaryRepo.UpsertCounts(summaries)
	assert.NoError(t, err)

	all, err = dlqSummaryRepo.GetAll()
	assert.NoError(t, err)
	for _, s := range all {
		if s.ConsumerID == dlqConsumers[0].ID.String() {
			assert.Equal(t, int64(8), s.DeadCount)
		}
	}
}

func TestDLQSummaryGetLastCheckedAt(t *testing.T) {
	t.Run("PopulatedTable", func(t *testing.T) {
		lastChecked, err := dlqSummaryRepo.GetLastCheckedAt()
		assert.NoError(t, err)
		assert.False(t, lastChecked.IsZero())
	})
}

func TestDLQSummaryDecrementCount(t *testing.T) {
	t.Run("DecrementExisting", func(t *testing.T) {
		err := dlqSummaryRepo.DecrementCount(dlqConsumers[0].ID.String(), 2)
		assert.NoError(t, err)

		all, err := dlqSummaryRepo.GetAll()
		assert.NoError(t, err)
		for _, s := range all {
			if s.ConsumerID == dlqConsumers[0].ID.String() {
				assert.Equal(t, int64(6), s.DeadCount)
			}
		}
	})

	t.Run("DecrementDoesNotGoBelowZero", func(t *testing.T) {
		err := dlqSummaryRepo.DecrementCount(dlqConsumers[0].ID.String(), 100)
		assert.NoError(t, err)

		all, err := dlqSummaryRepo.GetAll()
		assert.NoError(t, err)
		for _, s := range all {
			if s.ConsumerID == dlqConsumers[0].ID.String() {
				assert.Equal(t, int64(0), s.DeadCount)
			}
		}
	})

	t.Run("DecrementNonExistentConsumer", func(t *testing.T) {
		err := dlqSummaryRepo.DecrementCount("non-existent-consumer", 5)
		assert.NoError(t, err)
	})

	t.Run("DecrementZeroCount", func(t *testing.T) {
		err := dlqSummaryRepo.DecrementCount(dlqConsumers[0].ID.String(), 0)
		assert.NoError(t, err)
	})
}

func TestDLQSummaryUpsertEmpty(t *testing.T) {
	err := dlqSummaryRepo.UpsertCounts([]*data.DLQSummary{})
	assert.NoError(t, err)

	err = dlqSummaryRepo.UpsertCounts(nil)
	assert.NoError(t, err)
}

func TestDLQSummaryBootstrapCounts(t *testing.T) {
	// Create messages and mark jobs dead to have something to count
	msgRepo := NewMessageRepository(testDB, NewChannelRepository(testDB), NewProducerRepository(testDB))
	consumerRepo := NewConsumerRepository(testDB, NewChannelRepository(testDB))
	djRepo := NewDeliveryJobRepository(testDB, msgRepo, consumerRepo)

	msg, _ := data.NewMessage(dlqChannel, dlqProducer, "dlq-bootstrap-payload", sampleContentType, data.HeadersMap{})
	msg.QuickFix()
	err := msgRepo.Create(msg)
	assert.NoError(t, err)

	jobs := make([]*data.DeliveryJob, 0, len(dlqConsumers))
	for _, consumer := range dlqConsumers {
		job, _ := data.NewDeliveryJob(msg, consumer)
		jobs = append(jobs, job)
	}
	err = djRepo.DispatchMessage(msg, jobs...)
	assert.NoError(t, err)

	// Mark all jobs dead
	for _, job := range jobs {
		err = djRepo.MarkJobInflight(job)
		assert.NoError(t, err)
		err = djRepo.MarkJobDead(job)
		assert.NoError(t, err)
	}

	// Bootstrap
	err = dlqSummaryRepo.BootstrapCounts(testDB)
	assert.NoError(t, err)

	all, err := dlqSummaryRepo.GetAll()
	assert.NoError(t, err)
	foundCount := 0
	for _, s := range all {
		for _, c := range dlqConsumers {
			if s.ConsumerID == c.ID.String() {
				assert.GreaterOrEqual(t, s.DeadCount, int64(1))
				foundCount++
			}
		}
	}
	assert.Equal(t, len(dlqConsumers), foundCount)
}

// Generated with assistance from Claude AI
