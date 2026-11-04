package parking

import (
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/newscred/webhook-broker/config"
	"github.com/newscred/webhook-broker/dispatcher"
	"github.com/newscred/webhook-broker/storage"
	"github.com/newscred/webhook-broker/storage/data"
)

const (
	parkedCountRefreshInterval = 60 * time.Second
)

var (
	// drainerNow is the clock used for drain timestamps; replaceable in tests
	drainerNow = time.Now
)

// Drainer moves parked jobs of consumers that are no longer paused back into the job queue
type Drainer interface {
	Start()
	Stop()
}

// DrainerConfiguration holds the drainer dependencies
type DrainerConfiguration struct {
	JobParkingRepo    storage.JobParkingRepository
	LockRepo          storage.LockRepository
	ConsumerRepo      storage.ConsumerRepository
	Dispatcher        dispatcher.MessageDispatcher
	PauseConfig       config.DispatchPauseConfig
	BrokerConfig      config.BrokerConfig
	GatewayAuthConfig config.GatewayAuthConfig
	Metrics           *dispatcher.MetricsContainer
}

// NewDrainerConfiguration creates the drainer configuration
func NewDrainerConfiguration(jobParkingRepo storage.JobParkingRepository, lockRepo storage.LockRepository, consumerRepo storage.ConsumerRepository,
	messageDispatcher dispatcher.MessageDispatcher, pauseConfig config.DispatchPauseConfig, brokerConfig config.BrokerConfig,
	gatewayAuthConfig config.GatewayAuthConfig, metrics *dispatcher.MetricsContainer) *DrainerConfiguration {
	return &DrainerConfiguration{JobParkingRepo: jobParkingRepo, LockRepo: lockRepo, ConsumerRepo: consumerRepo, Dispatcher: messageDispatcher,
		PauseConfig: pauseConfig, BrokerConfig: brokerConfig, GatewayAuthConfig: gatewayAuthConfig, Metrics: metrics}
}

// DrainerImpl is the default Drainer. Every node runs one; the renewable "job-parking-drainer" lock makes sure only
// one node moves pages at a time. Correctness never depends on the lock: DrainPage deletes parked rows first and
// requires the exact count, so two drainers can not move a job twice.
type DrainerImpl struct {
	cfg              *DrainerConfiguration
	handoff          *HandoffSender
	stopChan         chan struct{}
	startOnce        sync.Once
	stopOnce         sync.Once
	wg               sync.WaitGroup
	lastCountRefresh time.Time
}

// NewDrainer creates the drainer
func NewDrainer(cfg *DrainerConfiguration) Drainer {
	if cfg == nil || cfg.JobParkingRepo == nil || cfg.LockRepo == nil || cfg.ConsumerRepo == nil || cfg.Dispatcher == nil ||
		cfg.PauseConfig == nil || cfg.BrokerConfig == nil || cfg.GatewayAuthConfig == nil || cfg.Metrics == nil {
		panic(panicString)
	}
	pauseConfig := cfg.PauseConfig
	httpEnabled := pauseConfig.IsHTTPHandoffEnabled()
	if !httpEnabled {
		log.Warn().Str("handoffMode", pauseConfig.GetHandoffMode()).Bool("adminTokenConfigured", pauseConfig.IsAdminTokenConfigured()).
			Msg("drained jobs will be enqueued on the draining node only; set [dispatch-pause] admin-token and handoff-mode=http to spread them across the cluster")
	}
	var hmacSecret []byte
	if cfg.GatewayAuthConfig.IsGatewayAuthEnabled() {
		hmacSecret = cfg.GatewayAuthConfig.GetHMACSharedSecret()
	}
	handoff := NewHandoffSender(HandoffSenderConfiguration{
		Dispatcher:    cfg.Dispatcher,
		Metrics:       cfg.Metrics,
		HTTPEnabled:   httpEnabled,
		Endpoint:      BuildEnqueueEndpoint(cfg.BrokerConfig.GetRetriggerBaseEndpoint()),
		AdminToken:    pauseConfig.GetAdminToken(),
		HMACSecret:    hmacSecret,
		Timeout:       pauseConfig.GetHandoffTimeout(),
		MaxAttempts:   pauseConfig.GetHandoffMaxAttempts(),
		Concurrency:   pauseConfig.GetHandoffConcurrency(),
		LocalFallback: pauseConfig.IsHandoffLocalFallbackEnabled(),
	})
	return &DrainerImpl{cfg: cfg, handoff: handoff, stopChan: make(chan struct{})}
}

// Start begins the drain loop
func (drainer *DrainerImpl) Start() {
	drainer.startOnce.Do(func() {
		drainer.handoff.Start()
		drainer.wg.Add(1)
		go func() {
			defer drainer.wg.Done()
			ticker := time.NewTicker(drainer.cfg.PauseConfig.GetDrainInterval())
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					drainer.runOnce()
				case <-drainer.stopChan:
					return
				}
			}
		}()
	})
}

// Stop ends the drain loop and waits for pending hand-offs
func (drainer *DrainerImpl) Stop() {
	drainer.stopOnce.Do(func() {
		close(drainer.stopChan)
		drainer.wg.Wait()
		drainer.handoff.Stop()
	})
}

func (drainer *DrainerImpl) stopping() bool {
	select {
	case <-drainer.stopChan:
		return true
	default:
		return false
	}
}

func (drainer *DrainerImpl) countRefreshDue() bool {
	return drainerNow().Sub(drainer.lastCountRefresh) >= parkedCountRefreshInterval
}

// runOnce performs one drain run: find drainable consumers, take the lock and move pages round-robin
func (drainer *DrainerImpl) runOnce() {
	consumerIDs, err := drainer.cfg.JobParkingRepo.GetDrainableConsumerIDs()
	if err != nil {
		log.Error().Err(err).Msg("could not list drainable consumers")
		return
	}
	if len(consumerIDs) == 0 && !drainer.countRefreshDue() {
		return
	}
	lock, err := data.NewLock(&data.DrainLock{})
	if err != nil {
		log.Error().Err(err).Msg("could not create drain lock")
		return
	}
	if err = drainer.cfg.LockRepo.TryLock(lock); err != nil {
		if err != storage.ErrAlreadyLocked {
			log.Error().Err(err).Msg("could not acquire drain lock")
		}
		return
	}
	lockHeld := true
	defer func() {
		if lockHeld {
			drainer.cfg.LockRepo.ReleaseLock(lock)
		}
	}()
	if drainer.countRefreshDue() {
		drainer.refreshParkedCounts()
	}
	lockHeld = drainer.drainRoundRobin(consumerIDs, lock)
}

// drainRoundRobin moves one page per consumer per round until every consumer is drained, the run is stopped, the
// hand-off is saturated or the lock is lost. Returns false if the lock was lost.
func (drainer *DrainerImpl) drainRoundRobin(consumerIDs []string, lock *data.Lock) bool {
	pageSize := drainer.cfg.PauseConfig.GetDrainPageSize()
	lease := drainer.cfg.PauseConfig.GetHandoffLease()
	drainedByConsumer := make(map[string]int, len(consumerIDs))
	active := consumerIDs
	for len(active) > 0 {
		remaining := make([]string, 0, len(active))
		for _, consumerID := range active {
			if drainer.stopping() || drainer.handoff.Saturated() {
				return true
			}
			consumer, err := drainer.cfg.ConsumerRepo.GetByID(consumerID)
			if err != nil {
				log.Error().Err(err).Str("consumerId", consumerID).Msg("could not load consumer to drain")
				continue
			}
			now := drainerNow()
			earliestNextAttemptAt := now
			if consumer.Type == data.PushConsumer {
				earliestNextAttemptAt = now.Add(lease)
			}
			jobs, err := drainer.cfg.JobParkingRepo.DrainPage(consumerID, pageSize, earliestNextAttemptAt)
			if err != nil {
				log.Error().Err(err).Str("consumerId", consumer.ConsumerID).Msg("could not drain parked jobs")
				continue
			}
			if len(jobs) == 0 {
				if drainedByConsumer[consumerID] > 0 {
					log.Info().Str("consumerId", consumer.ConsumerID).Int("drained", drainedByConsumer[consumerID]).Msg("finished draining parked jobs")
				}
				continue
			}
			if drainedByConsumer[consumerID] == 0 {
				log.Info().Str("consumerId", consumer.ConsumerID).Str("channelId", consumer.GetChannelIDSafely()).Msg("draining parked jobs")
			}
			drainedByConsumer[consumerID] += len(jobs)
			drainer.cfg.Metrics.DrainedJobsTotal.WithLabelValues(consumer.ConsumerID).Add(float64(len(jobs)))
			if consumer.Type == data.PushConsumer && !drainer.handoff.Submit(jobs) {
				// Jobs are durable as QUEUED; the retry worker delivers them once the lease passes
				log.Warn().Str("consumerId", consumer.ConsumerID).Int("jobs", len(jobs)).Msg("hand-off saturated; jobs will be retried after the lease")
			}
			if err = drainer.cfg.LockRepo.RenewLock(lock); err != nil {
				log.Warn().Err(err).Msg("lost the drain lock; stopping this drain run")
				return false
			}
			remaining = append(remaining, consumerID)
		}
		active = remaining
	}
	return true
}

// refreshParkedCounts rebuilds the parked_job_count gauge; only the drain lock holder does this
func (drainer *DrainerImpl) refreshParkedCounts() {
	counts, err := drainer.cfg.JobParkingRepo.GetCountsByConsumer()
	if err != nil {
		log.Error().Err(err).Msg("could not count parked jobs")
		return
	}
	drainer.lastCountRefresh = drainerNow()
	drainer.cfg.Metrics.ParkedJobCount.Reset()
	for _, count := range counts {
		consumerLabel := count.ConsumerID
		if consumer, lookupErr := drainer.cfg.ConsumerRepo.GetByID(count.ConsumerID); lookupErr == nil {
			consumerLabel = consumer.ConsumerID
		}
		drainer.cfg.Metrics.ParkedJobCount.WithLabelValues(count.ChannelID, consumerLabel).Set(float64(count.Count))
	}
}

// Generated with assistance from Claude AI
