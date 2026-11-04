package dispatcher

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/newscred/webhook-broker/config"
	"github.com/newscred/webhook-broker/storage"
	"github.com/newscred/webhook-broker/storage/data"
)

const (
	globalPauseMetricLabel = "*"
)

// PauseRegistry is a node's in-memory snapshot of dispatch pauses. It is an optimisation only: every hand-out is
// checked against the database (see storage DeliveryJobRepository.MarkJobInflight), so a stale snapshot can cost
// wasted work but never a delivery to a paused consumer.
type PauseRegistry interface {
	// IsGloballyPaused returns true if the broker-wide pause is active in the snapshot
	IsGloballyPaused() bool
	// IsEffectivelyPaused returns true if the consumer (internal id) or the whole broker is paused in the snapshot
	IsEffectivelyPaused(consumerInternalID string) bool
	// Snapshot returns the pauses currently known to this node
	Snapshot() []*data.DispatchPause
	// Apply records a pause immediately (write-through after a local pause API call)
	Apply(pause *data.DispatchPause)
	// Remove forgets a pause immediately (write-through after a local resume API call)
	Remove(scope string)
	// Refresh reloads the snapshot from the database; on error the previous snapshot is kept
	Refresh() error
	// Start begins periodic refreshes
	Start()
	// Stop ends periodic refreshes
	Stop()
}

// pauseSnapshot is immutable once published
type pauseSnapshot struct {
	global    *data.DispatchPause
	consumers map[string]*data.DispatchPause
}

func newPauseSnapshot(pauses []*data.DispatchPause) *pauseSnapshot {
	snapshot := &pauseSnapshot{consumers: make(map[string]*data.DispatchPause, len(pauses))}
	for _, pause := range pauses {
		if pause.IsGlobal() {
			snapshot.global = pause
		} else {
			snapshot.consumers[pause.Scope] = pause
		}
	}
	return snapshot
}

func (snapshot *pauseSnapshot) all() []*data.DispatchPause {
	pauses := make([]*data.DispatchPause, 0, len(snapshot.consumers)+1)
	if snapshot.global != nil {
		pauses = append(pauses, snapshot.global)
	}
	for _, pause := range snapshot.consumers {
		pauses = append(pauses, pause)
	}
	return pauses
}

// PauseRegistryImpl is the default PauseRegistry
type PauseRegistryImpl struct {
	pauseRepo       storage.DispatchPauseRepository
	consumerRepo    storage.ConsumerRepository
	refreshInterval time.Duration
	metrics         *MetricsContainer
	state           atomic.Pointer[pauseSnapshot]
	writeMutex      sync.Mutex
	stopChan        chan struct{}
	startOnce       sync.Once
	stopOnce        sync.Once
	wg              sync.WaitGroup
}

// NewPauseRegistry creates the registry and loads the snapshot synchronously so the state is known before any
// dispatching starts. A failed initial load is logged and leaves the snapshot empty; the database checks still
// protect paused consumers.
func NewPauseRegistry(pauseRepo storage.DispatchPauseRepository, consumerRepo storage.ConsumerRepository, pauseConfig config.DispatchPauseConfig, metrics *MetricsContainer) PauseRegistry {
	if pauseRepo == nil || consumerRepo == nil || pauseConfig == nil || metrics == nil {
		panic(panicString)
	}
	registry := &PauseRegistryImpl{pauseRepo: pauseRepo, consumerRepo: consumerRepo, refreshInterval: pauseConfig.GetRegistryRefreshInterval(),
		metrics: metrics, stopChan: make(chan struct{})}
	registry.state.Store(newPauseSnapshot(nil))
	if err := registry.Refresh(); err != nil {
		log.Error().Err(err).Msg("could not load initial dispatch pause snapshot")
	}
	return registry
}

// IsGloballyPaused returns true if the broker-wide pause is active in the snapshot
func (registry *PauseRegistryImpl) IsGloballyPaused() bool {
	return registry.state.Load().global != nil
}

// IsEffectivelyPaused returns true if the consumer or the whole broker is paused in the snapshot
func (registry *PauseRegistryImpl) IsEffectivelyPaused(consumerInternalID string) bool {
	snapshot := registry.state.Load()
	if snapshot.global != nil {
		return true
	}
	_, paused := snapshot.consumers[consumerInternalID]
	return paused
}

// Snapshot returns the pauses currently known to this node
func (registry *PauseRegistryImpl) Snapshot() []*data.DispatchPause {
	return registry.state.Load().all()
}

// Apply records a pause immediately
func (registry *PauseRegistryImpl) Apply(pause *data.DispatchPause) {
	if pause == nil {
		return
	}
	registry.writeMutex.Lock()
	defer registry.writeMutex.Unlock()
	pauses := registry.state.Load().all()
	replaced := false
	for index, existing := range pauses {
		if existing.Scope == pause.Scope {
			pauses[index] = pause
			replaced = true
		}
	}
	if !replaced {
		pauses = append(pauses, pause)
	}
	registry.publish(newPauseSnapshot(pauses))
}

// Remove forgets a pause immediately
func (registry *PauseRegistryImpl) Remove(scope string) {
	registry.writeMutex.Lock()
	defer registry.writeMutex.Unlock()
	pauses := registry.state.Load().all()
	remaining := make([]*data.DispatchPause, 0, len(pauses))
	for _, existing := range pauses {
		if existing.Scope != scope {
			remaining = append(remaining, existing)
		}
	}
	registry.publish(newPauseSnapshot(remaining))
}

// Refresh reloads the snapshot from the database; on error the previous snapshot is kept
func (registry *PauseRegistryImpl) Refresh() error {
	pauses, err := registry.pauseRepo.GetAll()
	if err != nil {
		log.Error().Err(err).Msg("could not refresh dispatch pause snapshot; keeping the previous one")
		return err
	}
	registry.writeMutex.Lock()
	defer registry.writeMutex.Unlock()
	registry.publish(newPauseSnapshot(pauses))
	return nil
}

// publish swaps the snapshot and rebuilds the dispatch_paused gauge; callers hold writeMutex
func (registry *PauseRegistryImpl) publish(snapshot *pauseSnapshot) {
	registry.state.Store(snapshot)
	registry.metrics.DispatchPaused.Reset()
	if snapshot.global != nil {
		registry.metrics.DispatchPaused.WithLabelValues(globalPauseMetricLabel, globalPauseMetricLabel).Set(1)
	}
	for consumerInternalID := range snapshot.consumers {
		channelLabel, consumerLabel := consumerInternalID, consumerInternalID
		if consumer, err := registry.consumerRepo.GetByID(consumerInternalID); err == nil {
			channelLabel, consumerLabel = consumer.GetChannelIDSafely(), consumer.ConsumerID
		}
		registry.metrics.DispatchPaused.WithLabelValues(channelLabel, consumerLabel).Set(1)
	}
}

// Start begins periodic refreshes
func (registry *PauseRegistryImpl) Start() {
	registry.startOnce.Do(func() {
		registry.wg.Add(1)
		go func() {
			defer registry.wg.Done()
			ticker := time.NewTicker(registry.refreshInterval)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					registry.Refresh()
				case <-registry.stopChan:
					return
				}
			}
		}()
	})
}

// Stop ends periodic refreshes
func (registry *PauseRegistryImpl) Stop() {
	registry.stopOnce.Do(func() {
		close(registry.stopChan)
		registry.wg.Wait()
	})
}

// Generated with assistance from Claude AI
