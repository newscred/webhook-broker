package parking

import (
	"bytes"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/rs/xid"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/newscred/webhook-broker/config"
	configmocks "github.com/newscred/webhook-broker/config/mocks"
	"github.com/newscred/webhook-broker/dispatcher"
	dispatchermocks "github.com/newscred/webhook-broker/dispatcher/mocks"
	"github.com/newscred/webhook-broker/storage"
	"github.com/newscred/webhook-broker/storage/data"
	storagemocks "github.com/newscred/webhook-broker/storage/mocks"
)

// fakePauseConfig is a settable config.DispatchPauseConfig
type fakePauseConfig struct {
	token         string
	mode          string
	drainInterval time.Duration
	lease         time.Duration
	timeout       time.Duration
	pageSize      int
	attempts      int
	concurrency   int
	highWaterMark int
	fallback      bool
}

func newFakePauseConfig() *fakePauseConfig {
	return &fakePauseConfig{token: "admin-token", mode: config.HandoffModeHTTP, drainInterval: 5 * time.Millisecond, lease: time.Minute,
		timeout: time.Second, pageSize: 5, attempts: 3, concurrency: 2, highWaterMark: 100, fallback: true}
}

func (c *fakePauseConfig) GetAdminToken() string                     { return c.token }
func (c *fakePauseConfig) IsAdminTokenConfigured() bool              { return c.token != "" }
func (c *fakePauseConfig) GetRegistryRefreshInterval() time.Duration { return time.Second }
func (c *fakePauseConfig) GetDrainInterval() time.Duration           { return c.drainInterval }
func (c *fakePauseConfig) GetDrainPageSize() int                     { return c.pageSize }
func (c *fakePauseConfig) GetHandoffMode() string                    { return c.mode }
func (c *fakePauseConfig) IsHTTPHandoffEnabled() bool {
	return c.mode == config.HandoffModeHTTP && c.IsAdminTokenConfigured()
}
func (c *fakePauseConfig) GetHandoffLease() time.Duration      { return c.lease }
func (c *fakePauseConfig) GetHandoffTimeout() time.Duration    { return c.timeout }
func (c *fakePauseConfig) GetHandoffMaxAttempts() int          { return c.attempts }
func (c *fakePauseConfig) GetHandoffConcurrency() int          { return c.concurrency }
func (c *fakePauseConfig) GetHandoffHighWaterMark() int        { return c.highWaterMark }
func (c *fakePauseConfig) IsHandoffLocalFallbackEnabled() bool { return c.fallback }

// fakeGatewayAuthConfig is a settable config.GatewayAuthConfig
type fakeGatewayAuthConfig struct {
	enabled bool
	secret  []byte
}

func (c *fakeGatewayAuthConfig) GetGatewayAuthMode() string {
	if c.enabled {
		return "hmac"
	}
	return "none"
}
func (c *fakeGatewayAuthConfig) GetHMACSharedSecret() []byte              { return c.secret }
func (c *fakeGatewayAuthConfig) GetHMACTimestampTolerance() time.Duration { return time.Minute }
func (c *fakeGatewayAuthConfig) GetAuthExemptPaths() []string             { return nil }
func (c *fakeGatewayAuthConfig) IsGatewayAuthEnabled() bool               { return c.enabled }

type drainerMocks struct {
	parking    *storagemocks.JobParkingRepository
	lock       *storagemocks.LockRepository
	consumer   *storagemocks.ConsumerRepository
	dispatcher *dispatchermocks.MessageDispatcher
}

func newMockedDrainerConfiguration(pauseConfig config.DispatchPauseConfig, retrigger string) (*DrainerConfiguration, *drainerMocks) {
	mocks := &drainerMocks{parking: new(storagemocks.JobParkingRepository), lock: new(storagemocks.LockRepository),
		consumer: new(storagemocks.ConsumerRepository), dispatcher: new(dispatchermocks.MessageDispatcher)}
	brokerConfig := new(configmocks.BrokerConfig)
	brokerConfig.On("GetRetriggerBaseEndpoint").Return(retrigger)
	return NewDrainerConfiguration(mocks.parking, mocks.lock, mocks.consumer, mocks.dispatcher, pauseConfig, brokerConfig,
		&fakeGatewayAuthConfig{}, dispatcher.NewMetricsContainer()), mocks
}

func newConsumerOfType(consumerType data.ConsumerType) *data.Consumer {
	consumer := &data.Consumer{ConsumerID: "public-" + xid.New().String(), Type: consumerType, ConsumingFrom: &data.Channel{ChannelID: "channel"}}
	consumer.ID = xid.New()
	return consumer
}

func jobsOf(n int) []*data.DeliveryJob {
	jobs := make([]*data.DeliveryJob, n)
	for i := range jobs {
		jobs[i] = &data.DeliveryJob{}
		jobs[i].ID = xid.New()
	}
	return jobs
}

func counterValue(t *testing.T, counter prometheus.Counter) float64 {
	t.Helper()
	metric := &dto.Metric{}
	assert.NoError(t, counter.Write(metric))
	return metric.GetCounter().GetValue()
}

// skipCountRefresh marks the parked count gauge as fresh so runOnce only drains
func skipCountRefresh(drainer Drainer) *DrainerImpl {
	impl := drainer.(*DrainerImpl)
	impl.lastCountRefresh = time.Now()
	return impl
}

func TestNewDrainer(t *testing.T) {
	valid := func() *DrainerConfiguration {
		cfg, _ := newMockedDrainerConfiguration(newFakePauseConfig(), "http://localhost:8080")
		return cfg
	}
	assert.NotNil(t, NewDrainer(valid()))
	assert.PanicsWithValue(t, panicString, func() { NewDrainer(nil) })
	mutations := map[string]func(*DrainerConfiguration){
		"JobParkingRepo":    func(c *DrainerConfiguration) { c.JobParkingRepo = nil },
		"LockRepo":          func(c *DrainerConfiguration) { c.LockRepo = nil },
		"ConsumerRepo":      func(c *DrainerConfiguration) { c.ConsumerRepo = nil },
		"Dispatcher":        func(c *DrainerConfiguration) { c.Dispatcher = nil },
		"PauseConfig":       func(c *DrainerConfiguration) { c.PauseConfig = nil },
		"BrokerConfig":      func(c *DrainerConfiguration) { c.BrokerConfig = nil },
		"GatewayAuthConfig": func(c *DrainerConfiguration) { c.GatewayAuthConfig = nil },
		"Metrics":           func(c *DrainerConfiguration) { c.Metrics = nil },
	}
	for name, mutate := range mutations {
		cfg := valid()
		mutate(cfg)
		assert.PanicsWithValue(t, panicString, func() { NewDrainer(cfg) }, name)
	}
}

func TestNewDrainerHandoffConfiguration(t *testing.T) {
	t.Run("HMACSecretWhenGatewayAuthEnabled", func(t *testing.T) {
		cfg, _ := newMockedDrainerConfiguration(newFakePauseConfig(), "http://lb:8080/")
		cfg.GatewayAuthConfig = &fakeGatewayAuthConfig{enabled: true, secret: []byte("secret")}
		impl := NewDrainer(cfg).(*DrainerImpl)
		assert.Equal(t, []byte("secret"), impl.handoff.cfg.HMACSecret)
		assert.True(t, impl.handoff.cfg.HTTPEnabled)
		assert.Equal(t, "http://lb:8080"+InternalEnqueuePath, impl.handoff.cfg.Endpoint)
	})
	t.Run("LocalWithoutAdminToken", func(t *testing.T) {
		var buf bytes.Buffer
		oldLogger := log.Logger
		log.Logger = log.Output(&buf)
		defer func() { log.Logger = oldLogger }()
		pauseConfig := newFakePauseConfig()
		pauseConfig.token = ""
		cfg, _ := newMockedDrainerConfiguration(pauseConfig, "http://lb:8080")
		impl := NewDrainer(cfg).(*DrainerImpl)
		assert.False(t, impl.handoff.cfg.HTTPEnabled)
		assert.Nil(t, impl.handoff.cfg.HMACSecret)
		assert.Contains(t, buf.String(), "enqueued on the draining node only")
	})
}

func TestDrainerStartStop(t *testing.T) {
	cfg, mocks := newMockedDrainerConfiguration(newFakePauseConfig(), "http://localhost:8080")
	mocks.parking.On("GetDrainableConsumerIDs").Return([]string{}, nil)
	mocks.parking.On("GetCountsByConsumer").Return([]*data.ParkedJobCount{}, nil)
	mocks.lock.On("TryLock", mock.Anything).Return(nil)
	mocks.lock.On("ReleaseLock", mock.Anything).Return(nil)
	drainer := NewDrainer(cfg)
	drainer.Start()
	drainer.Start()
	time.Sleep(30 * time.Millisecond)
	drainer.Stop()
	drainer.Stop()
	mocks.parking.AssertCalled(t, "GetDrainableConsumerIDs")
}

func TestDrainRunNoDrainableSkipsLock(t *testing.T) {
	cfg, mocks := newMockedDrainerConfiguration(newFakePauseConfig(), "http://localhost:8080")
	mocks.parking.On("GetDrainableConsumerIDs").Return([]string{}, nil)
	skipCountRefresh(NewDrainer(cfg)).runOnce()
	mocks.lock.AssertNotCalled(t, "TryLock", mock.Anything)
}

func TestDrainRunListError(t *testing.T) {
	cfg, mocks := newMockedDrainerConfiguration(newFakePauseConfig(), "http://localhost:8080")
	mocks.parking.On("GetDrainableConsumerIDs").Return(nil, errors.New("db down"))
	NewDrainer(cfg).(*DrainerImpl).runOnce()
	mocks.lock.AssertNotCalled(t, "TryLock", mock.Anything)
}

func TestDrainRunLockContention(t *testing.T) {
	cfg, mocks := newMockedDrainerConfiguration(newFakePauseConfig(), "http://localhost:8080")
	mocks.parking.On("GetDrainableConsumerIDs").Return([]string{"c1"}, nil)
	mocks.lock.On("TryLock", mock.Anything).Return(storage.ErrAlreadyLocked)
	skipCountRefresh(NewDrainer(cfg)).runOnce()
	mocks.parking.AssertNotCalled(t, "DrainPage", mock.Anything, mock.Anything, mock.Anything)
	mocks.lock.AssertNotCalled(t, "ReleaseLock", mock.Anything)
}

// pagedDrain returns a DrainPage implementation serving the given number of single-job pages per consumer and
// recording the call order
func pagedDrain(pages map[string]int, calls *[]string, mutex *sync.Mutex) func(string, int, time.Time) ([]*data.DeliveryJob, error) {
	return func(consumerID string, _ int, _ time.Time) ([]*data.DeliveryJob, error) {
		mutex.Lock()
		defer mutex.Unlock()
		*calls = append(*calls, consumerID)
		if pages[consumerID] == 0 {
			return []*data.DeliveryJob{}, nil
		}
		pages[consumerID]--
		return jobsOf(1), nil
	}
}

func TestDrainRunRoundRobin(t *testing.T) {
	cfg, mocks := newMockedDrainerConfiguration(newFakePauseConfig(), "http://localhost:8080")
	a, b, c := newConsumerOfType(data.PullConsumer), newConsumerOfType(data.PullConsumer), newConsumerOfType(data.PullConsumer)
	ids := []string{a.ID.String(), b.ID.String(), c.ID.String()}
	for _, consumer := range []*data.Consumer{a, b, c} {
		mocks.consumer.On("GetByID", consumer.ID.String()).Return(consumer, nil)
	}
	var calls []string
	var mutex sync.Mutex
	mocks.parking.On("GetDrainableConsumerIDs").Return(ids, nil)
	mocks.parking.On("DrainPage", mock.Anything, 5, mock.Anything).Return(pagedDrain(map[string]int{ids[0]: 3, ids[1]: 1, ids[2]: 2}, &calls, &mutex))
	mocks.lock.On("TryLock", mock.Anything).Return(nil)
	mocks.lock.On("RenewLock", mock.Anything).Return(nil)
	mocks.lock.On("ReleaseLock", mock.Anything).Return(nil)
	skipCountRefresh(NewDrainer(cfg)).runOnce()
	assert.Equal(t, []string{ids[0], ids[1], ids[2], ids[0], ids[1], ids[2], ids[0], ids[2], ids[0]}, calls)
	mocks.lock.AssertNumberOfCalls(t, "RenewLock", 6)
	mocks.lock.AssertNumberOfCalls(t, "ReleaseLock", 1)
	mocks.dispatcher.AssertNotCalled(t, "EnqueueJobs", mock.Anything)
}

func TestDrainRunRenewFailureStops(t *testing.T) {
	var buf bytes.Buffer
	oldLogger := log.Logger
	log.Logger = log.Output(&buf)
	defer func() { log.Logger = oldLogger }()
	cfg, mocks := newMockedDrainerConfiguration(newFakePauseConfig(), "http://localhost:8080")
	consumer := newConsumerOfType(data.PullConsumer)
	id := consumer.ID.String()
	var calls []string
	var mutex sync.Mutex
	mocks.consumer.On("GetByID", id).Return(consumer, nil)
	mocks.parking.On("GetDrainableConsumerIDs").Return([]string{id}, nil)
	mocks.parking.On("DrainPage", id, 5, mock.Anything).Return(pagedDrain(map[string]int{id: 10}, &calls, &mutex))
	mocks.lock.On("TryLock", mock.Anything).Return(nil)
	mocks.lock.On("RenewLock", mock.Anything).Return(nil).Once()
	mocks.lock.On("RenewLock", mock.Anything).Return(storage.ErrNoRowsUpdated).Once()
	skipCountRefresh(NewDrainer(cfg)).runOnce()
	assert.Equal(t, 2, len(calls), "no page after the lock was lost")
	mocks.lock.AssertNotCalled(t, "ReleaseLock", mock.Anything)
	assert.Contains(t, buf.String(), "lost the drain lock")
}

func TestDrainRunBackpressureEndsRun(t *testing.T) {
	cfg, mocks := newMockedDrainerConfiguration(newFakePauseConfig(), "http://localhost:8080")
	consumer := newConsumerOfType(data.PushConsumer)
	id := consumer.ID.String()
	var calls []string
	var mutex sync.Mutex
	mocks.consumer.On("GetByID", id).Return(consumer, nil)
	mocks.parking.On("GetDrainableConsumerIDs").Return([]string{id}, nil)
	mocks.parking.On("DrainPage", id, 5, mock.Anything).Return(pagedDrain(map[string]int{id: 100}, &calls, &mutex))
	mocks.lock.On("TryLock", mock.Anything).Return(nil)
	mocks.lock.On("RenewLock", mock.Anything).Return(nil)
	mocks.lock.On("ReleaseLock", mock.Anything).Return(nil)
	impl := skipCountRefresh(NewDrainer(cfg))
	// The sender is not started, so its buffer (2 x concurrency = 4) fills up and the run must end
	impl.runOnce()
	assert.Equal(t, 4, len(calls))
	assert.True(t, impl.handoff.Saturated())
	mocks.lock.AssertNumberOfCalls(t, "ReleaseLock", 1)
}

func TestDrainRunEarliestNextAttemptAt(t *testing.T) {
	fixedNow := time.Now().Truncate(time.Second)
	oldNow := drainerNow
	drainerNow = func() time.Time { return fixedNow }
	defer func() { drainerNow = oldNow }()
	pauseConfig := newFakePauseConfig()
	pauseConfig.lease = 90 * time.Second
	for name, testCase := range map[string]struct {
		consumerType data.ConsumerType
		expected     time.Time
		handedOff    int
	}{
		"PushUsesLease": {data.PushConsumer, fixedNow.Add(90 * time.Second), 1},
		"PullNoHandoff": {data.PullConsumer, fixedNow, 0},
	} {
		t.Run(name, func(t *testing.T) {
			cfg, mocks := newMockedDrainerConfiguration(pauseConfig, "http://localhost:8080")
			consumer := newConsumerOfType(testCase.consumerType)
			id := consumer.ID.String()
			mocks.consumer.On("GetByID", id).Return(consumer, nil)
			mocks.parking.On("GetDrainableConsumerIDs").Return([]string{id}, nil)
			mocks.parking.On("DrainPage", id, 5, testCase.expected).Return(jobsOf(2), nil).Once()
			mocks.parking.On("DrainPage", id, 5, testCase.expected).Return([]*data.DeliveryJob{}, nil).Once()
			mocks.lock.On("TryLock", mock.Anything).Return(nil)
			mocks.lock.On("RenewLock", mock.Anything).Return(nil)
			mocks.lock.On("ReleaseLock", mock.Anything).Return(nil)
			impl := skipCountRefresh(NewDrainer(cfg))
			impl.runOnce()
			mocks.parking.AssertExpectations(t)
			assert.Equal(t, testCase.handedOff, len(impl.handoff.pending))
		})
	}
}

func TestDrainRunErrorsSkipConsumer(t *testing.T) {
	cfg, mocks := newMockedDrainerConfiguration(newFakePauseConfig(), "http://localhost:8080")
	failingPage, failingLookup, healthy := newConsumerOfType(data.PullConsumer), newConsumerOfType(data.PullConsumer), newConsumerOfType(data.PullConsumer)
	mocks.consumer.On("GetByID", failingPage.ID.String()).Return(failingPage, nil)
	mocks.consumer.On("GetByID", failingLookup.ID.String()).Return(nil, errors.New("not found"))
	mocks.consumer.On("GetByID", healthy.ID.String()).Return(healthy, nil)
	mocks.parking.On("GetDrainableConsumerIDs").Return([]string{failingPage.ID.String(), failingLookup.ID.String(), healthy.ID.String()}, nil)
	mocks.parking.On("DrainPage", failingPage.ID.String(), 5, mock.Anything).Return(nil, errors.New("db busy"))
	mocks.parking.On("DrainPage", healthy.ID.String(), 5, mock.Anything).Return(jobsOf(1), nil).Once()
	mocks.parking.On("DrainPage", healthy.ID.String(), 5, mock.Anything).Return([]*data.DeliveryJob{}, nil).Once()
	mocks.lock.On("TryLock", mock.Anything).Return(nil)
	mocks.lock.On("RenewLock", mock.Anything).Return(nil)
	mocks.lock.On("ReleaseLock", mock.Anything).Return(nil)
	skipCountRefresh(NewDrainer(cfg)).runOnce()
	mocks.parking.AssertNumberOfCalls(t, "DrainPage", 3)
	mocks.parking.AssertExpectations(t)
}

func TestRefreshParkedCounts(t *testing.T) {
	cfg, mocks := newMockedDrainerConfiguration(newFakePauseConfig(), "http://localhost:8080")
	known := newConsumerOfType(data.PushConsumer)
	mocks.consumer.On("GetByID", known.ID.String()).Return(known, nil)
	mocks.consumer.On("GetByID", "unknown-internal").Return(nil, errors.New("not found"))
	mocks.parking.On("GetDrainableConsumerIDs").Return([]string{}, nil)
	mocks.parking.On("GetCountsByConsumer").Return([]*data.ParkedJobCount{
		{ChannelID: "count-channel", ConsumerID: known.ID.String(), Count: 7},
		{ChannelID: "count-channel", ConsumerID: "unknown-internal", Count: 3},
	}, nil).Once()
	mocks.lock.On("TryLock", mock.Anything).Return(nil)
	mocks.lock.On("ReleaseLock", mock.Anything).Return(nil)
	impl := NewDrainer(cfg).(*DrainerImpl)
	impl.runOnce()
	metric := &dto.Metric{}
	assert.NoError(t, cfg.Metrics.ParkedJobCount.WithLabelValues("count-channel", known.ConsumerID).Write(metric))
	assert.Equal(t, float64(7), metric.GetGauge().GetValue())
	assert.NoError(t, cfg.Metrics.ParkedJobCount.WithLabelValues("count-channel", "unknown-internal").Write(metric))
	assert.Equal(t, float64(3), metric.GetGauge().GetValue())
	// Fresh counts and nothing to drain: the next run does not take the lock
	impl.runOnce()
	mocks.lock.AssertNumberOfCalls(t, "TryLock", 1)
	mocks.parking.AssertNumberOfCalls(t, "GetCountsByConsumer", 1)
}

// Generated with assistance from Claude AI
