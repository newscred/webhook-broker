package dlq

import (
	"sync"
	"time"

	"github.com/newscred/webhook-broker/config"
	"github.com/newscred/webhook-broker/dispatcher"
	"github.com/newscred/webhook-broker/storage"
	"github.com/newscred/webhook-broker/storage/data"
	"github.com/rs/zerolog/log"
)

const (
	panicString = "parameters null"
)

// DLQSummaryUpdater is the contract for the DLQ summary updater service
type DLQSummaryUpdater interface {
	Start()
	Stop()
}

// DLQSummaryUpdaterConfiguration holds dependencies for the updater
type DLQSummaryUpdaterConfiguration struct {
	DLQSummaryRepo  storage.DLQSummaryRepository
	DeliveryJobRepo storage.DeliveryJobRepository
	ConsumerRepo    storage.ConsumerRepository
	ChannelRepo     storage.ChannelRepository
	LockRepo        storage.LockRepository
	DLQConfig       config.DLQConfig
	Metrics         *dispatcher.MetricsContainer
}

// NewDLQSummaryUpdaterConfiguration creates a new configuration for the updater
func NewDLQSummaryUpdaterConfiguration(
	dlqSummaryRepo storage.DLQSummaryRepository,
	deliveryJobRepo storage.DeliveryJobRepository,
	consumerRepo storage.ConsumerRepository,
	channelRepo storage.ChannelRepository,
	lockRepo storage.LockRepository,
	dlqConfig config.DLQConfig,
	metrics *dispatcher.MetricsContainer,
) *DLQSummaryUpdaterConfiguration {
	return &DLQSummaryUpdaterConfiguration{
		DLQSummaryRepo:  dlqSummaryRepo,
		DeliveryJobRepo: deliveryJobRepo,
		ConsumerRepo:    consumerRepo,
		ChannelRepo:     channelRepo,
		LockRepo:        lockRepo,
		DLQConfig:       dlqConfig,
		Metrics:         metrics,
	}
}

// DLQSummaryUpdaterImpl implements DLQSummaryUpdater
type DLQSummaryUpdaterImpl struct {
	dlqSummaryRepo  storage.DLQSummaryRepository
	deliveryJobRepo storage.DeliveryJobRepository
	consumerRepo    storage.ConsumerRepository
	channelRepo     storage.ChannelRepository
	lockRepo        storage.LockRepository
	dlqConfig       config.DLQConfig
	metrics         *dispatcher.MetricsContainer
	stopChan        chan struct{}
	wg              sync.WaitGroup
}

// NewDLQSummaryUpdater creates a new DLQ summary updater
func NewDLQSummaryUpdater(configuration *DLQSummaryUpdaterConfiguration) DLQSummaryUpdater {
	if configuration.DLQSummaryRepo == nil || configuration.DeliveryJobRepo == nil ||
		configuration.ConsumerRepo == nil || configuration.ChannelRepo == nil ||
		configuration.LockRepo == nil || configuration.DLQConfig == nil ||
		configuration.Metrics == nil {
		panic(panicString)
	}
	return &DLQSummaryUpdaterImpl{
		dlqSummaryRepo:  configuration.DLQSummaryRepo,
		deliveryJobRepo: configuration.DeliveryJobRepo,
		consumerRepo:    configuration.ConsumerRepo,
		channelRepo:     configuration.ChannelRepo,
		lockRepo:        configuration.LockRepo,
		dlqConfig:       configuration.DLQConfig,
		metrics:         configuration.Metrics,
		stopChan:        make(chan struct{}),
	}
}

// Start begins the updater processing loop
func (u *DLQSummaryUpdaterImpl) Start() {
	u.wg.Add(1)
	go func() {
		defer u.wg.Done()
		ticker := time.NewTicker(u.dlqConfig.GetDLQSummaryUpdateInterval())
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				u.processUpdate()
			case <-u.stopChan:
				return
			}
		}
	}()
}

// Stop halts the updater processing loop
func (u *DLQSummaryUpdaterImpl) Stop() {
	close(u.stopChan)
	u.wg.Wait()
}

// processUpdate runs one update cycle: lock, check interval, count, upsert, update metrics
func (u *DLQSummaryUpdaterImpl) processUpdate() {
	lockable := &data.DLQSummaryLock{}
	lock, err := data.NewLock(lockable)
	if err != nil {
		log.Error().Err(err).Msg("failed to create DLQ summary lock")
		return
	}
	err = u.lockRepo.TryLock(lock)
	if err != nil {
		if err == storage.ErrAlreadyLocked {
			log.Debug().Msg("DLQ summary update skipped: lock held by another instance")
		} else {
			log.Error().Err(err).Msg("failed to acquire DLQ summary lock")
		}
		return
	}
	defer u.lockRepo.ReleaseLock(lock)

	// Check if an update has already been performed recently
	lastChecked, err := u.dlqSummaryRepo.GetLastCheckedAt()
	if err != nil {
		log.Error().Err(err).Msg("failed to get last checked at for DLQ summary")
		return
	}

	now := time.Now()
	interval := u.dlqConfig.GetDLQSummaryUpdateInterval()

	if lastChecked.IsZero() {
		// Bootstrap: first run or empty table
		log.Info().Msg("bootstrapping DLQ summary counts")
		err = u.dlqSummaryRepo.BootstrapCounts(nil)
		if err != nil {
			log.Error().Err(err).Msg("failed to bootstrap DLQ summary counts")
			return
		}
	} else if now.Sub(lastChecked) < interval {
		// Another instance already updated recently
		log.Debug().Msg("DLQ summary update skipped: already updated recently")
		return
	} else {
		// Incremental update
		counts, err := u.deliveryJobRepo.GetDeadJobCountsSinceCheckpoint(lastChecked)
		if err != nil {
			log.Error().Err(err).Msg("failed to get dead job counts since checkpoint")
			return
		}
		if len(counts) > 0 {
			summaries := make([]*data.DLQSummary, 0, len(counts))
			for consumerID, count := range counts {
				consumer, consErr := u.consumerRepo.GetByID(consumerID)
				if consErr != nil {
					log.Warn().Err(consErr).Str("consumerID", consumerID).Msg("could not look up consumer for DLQ incremental update")
					continue
				}
				summaries = append(summaries, &data.DLQSummary{
					ConsumerID:    consumerID,
					ConsumerName:  consumer.Name,
					ChannelID:     consumer.ConsumingFrom.ChannelID,
					ChannelName:   consumer.ConsumingFrom.Name,
					DeadCount:     count,
					LastCheckedAt: now,
				})
			}
			err = u.dlqSummaryRepo.UpsertCounts(summaries)
			if err != nil {
				log.Error().Err(err).Msg("failed to upsert DLQ summary counts")
				return
			}
		}
	}

	// Update Prometheus gauges from summary
	u.updatePrometheusGauges()
}

func (u *DLQSummaryUpdaterImpl) updatePrometheusGauges() {
	summaries, err := u.dlqSummaryRepo.GetAll()
	if err != nil {
		log.Error().Err(err).Msg("failed to read DLQ summaries for Prometheus update")
		return
	}
	// Reset the gauge vec to remove stale labels
	u.metrics.DeadJobCount.Reset()
	for _, s := range summaries {
		u.metrics.DeadJobCount.WithLabelValues(s.ChannelID, s.ConsumerID).Set(float64(s.DeadCount))
	}
}

// Generated with assistance from Claude AI
