package dispatcher

import (
	"bytes"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/rs/xid"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	configmocks "github.com/newscred/webhook-broker/config/mocks"
	"github.com/newscred/webhook-broker/storage"
	"github.com/newscred/webhook-broker/storage/data"
	storagemocks "github.com/newscred/webhook-broker/storage/mocks"
)

// NOTE: dispatcher tests never create the GLOBAL pause row in the shared DB: dispatchers started by other tests
// may still be running and would park their jobs. Global behavior is covered with the fake registry below and in
// the storage package tests.

// fakePauseRegistry is a settable PauseRegistry for unit tests
type fakePauseRegistry struct {
	mutex  sync.Mutex
	global bool
	paused map[string]bool
}

func newFakePauseRegistry(pausedConsumerIDs ...string) *fakePauseRegistry {
	registry := &fakePauseRegistry{paused: map[string]bool{}}
	for _, id := range pausedConsumerIDs {
		registry.paused[id] = true
	}
	return registry
}

func (registry *fakePauseRegistry) IsGloballyPaused() bool {
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	return registry.global
}

func (registry *fakePauseRegistry) IsEffectivelyPaused(consumerInternalID string) bool {
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	return registry.global || registry.paused[consumerInternalID]
}

func (registry *fakePauseRegistry) Snapshot() []*data.DispatchPause { return nil }
func (registry *fakePauseRegistry) Apply(pause *data.DispatchPause)  {}
func (registry *fakePauseRegistry) Remove(scope string)              {}
func (registry *fakePauseRegistry) Refresh() error                   { return nil }
func (registry *fakePauseRegistry) Start()                           {}
func (registry *fakePauseRegistry) Stop()                            {}

func counterValue(t *testing.T, counter prometheus.Counter) float64 {
	t.Helper()
	metric := &dto.Metric{}
	assert.NoError(t, counter.Write(metric))
	return metric.GetCounter().GetValue()
}

// gaugeLabels returns label pairs ("channel|consumer") of the gauge vec with their values
func gaugeLabels(vec *prometheus.GaugeVec) map[string]float64 {
	result := map[string]float64{}
	ch := make(chan prometheus.Metric, 100)
	vec.Collect(ch)
	close(ch)
	for metric := range ch {
		m := &dto.Metric{}
		if metric.Write(m) == nil {
			key := ""
			for index, label := range m.GetLabel() {
				if index > 0 {
					key += "|"
				}
				key += label.GetValue()
			}
			result[key] = m.GetGauge().GetValue()
		}
	}
	return result
}

func newPauseConfigMock(interval time.Duration) *configmocks.DispatchPauseConfig {
	pauseConfig := new(configmocks.DispatchPauseConfig)
	pauseConfig.On("GetRegistryRefreshInterval").Return(interval)
	return pauseConfig
}

func TestNewPauseRegistry(t *testing.T) {
	t.Run("InitialRefresh", func(t *testing.T) {
		pauseRepo := new(storagemocks.DispatchPauseRepository)
		pauseRepo.On("GetAll").Return([]*data.DispatchPause{{Scope: "c1", ConsumerID: "c1", PausedAt: time.Now()}}, nil).Once()
		consumerRepo := new(storagemocks.ConsumerRepository)
		consumerRepo.On("GetByID", "c1").Return(nil, errors.New("not found"))
		registry := NewPauseRegistry(pauseRepo, consumerRepo, newPauseConfigMock(time.Second), counter)
		assert.True(t, registry.IsEffectivelyPaused("c1"))
		pauseRepo.AssertExpectations(t)
	})
	t.Run("InitialRefreshError", func(t *testing.T) {
		pauseRepo := new(storagemocks.DispatchPauseRepository)
		pauseRepo.On("GetAll").Return(nil, errors.New("db down"))
		registry := NewPauseRegistry(pauseRepo, new(storagemocks.ConsumerRepository), newPauseConfigMock(time.Second), counter)
		assert.False(t, registry.IsGloballyPaused())
		assert.Empty(t, registry.Snapshot())
	})
	t.Run("NilDependencies", func(t *testing.T) {
		pauseRepo := new(storagemocks.DispatchPauseRepository)
		consumerRepo := new(storagemocks.ConsumerRepository)
		pauseConfig := newPauseConfigMock(time.Second)
		assert.PanicsWithValue(t, panicString, func() { NewPauseRegistry(nil, consumerRepo, pauseConfig, counter) })
		assert.PanicsWithValue(t, panicString, func() { NewPauseRegistry(pauseRepo, nil, pauseConfig, counter) })
		assert.PanicsWithValue(t, panicString, func() { NewPauseRegistry(pauseRepo, consumerRepo, nil, counter) })
		assert.PanicsWithValue(t, panicString, func() { NewPauseRegistry(pauseRepo, consumerRepo, pauseConfig, nil) })
	})
}

func TestPauseRegistryEffectivePause(t *testing.T) {
	pauseRepo := new(storagemocks.DispatchPauseRepository)
	pauseRepo.On("GetAll").Return([]*data.DispatchPause{{Scope: "c1", ConsumerID: "c1", PausedAt: time.Now()}}, nil).Once()
	consumerRepo := new(storagemocks.ConsumerRepository)
	consumerRepo.On("GetByID", mock.Anything).Return(nil, errors.New("not found"))
	registry := NewPauseRegistry(pauseRepo, consumerRepo, newPauseConfigMock(time.Second), counter)
	assert.False(t, registry.IsGloballyPaused())
	assert.True(t, registry.IsEffectivelyPaused("c1"))
	assert.False(t, registry.IsEffectivelyPaused("c2"))
	pauseRepo.On("GetAll").Return([]*data.DispatchPause{{Scope: data.GlobalDispatchPauseScope, PausedAt: time.Now()}}, nil).Once()
	assert.NoError(t, registry.Refresh())
	assert.True(t, registry.IsGloballyPaused())
	assert.True(t, registry.IsEffectivelyPaused("c1"))
	assert.True(t, registry.IsEffectivelyPaused("c2"))
}

func TestPauseRegistryWriteThrough(t *testing.T) {
	pauseRepo := new(storagemocks.DispatchPauseRepository)
	pauseRepo.On("GetAll").Return([]*data.DispatchPause{}, nil)
	consumerRepo := new(storagemocks.ConsumerRepository)
	consumerRepo.On("GetByID", mock.Anything).Return(nil, errors.New("not found"))
	registry := NewPauseRegistry(pauseRepo, consumerRepo, newPauseConfigMock(time.Second), counter)
	registry.Apply(nil)
	assert.Empty(t, registry.Snapshot())
	registry.Apply(&data.DispatchPause{Scope: "c1", ConsumerID: "c1", Reason: "first"})
	assert.True(t, registry.IsEffectivelyPaused("c1"))
	registry.Apply(&data.DispatchPause{Scope: "c1", ConsumerID: "c1", Reason: "second"})
	snapshot := registry.Snapshot()
	if assert.Equal(t, 1, len(snapshot)) {
		assert.Equal(t, "second", snapshot[0].Reason)
	}
	registry.Apply(data.NewGlobalDispatchPause(""))
	assert.True(t, registry.IsGloballyPaused())
	registry.Remove(data.GlobalDispatchPauseScope)
	assert.False(t, registry.IsGloballyPaused())
	registry.Remove("c1")
	assert.False(t, registry.IsEffectivelyPaused("c1"))
	assert.Empty(t, registry.Snapshot())
}

func TestPauseRegistryRefreshFailureKeepsSnapshot(t *testing.T) {
	var buf bytes.Buffer
	oldLogger := log.Logger
	log.Logger = log.Output(&buf)
	defer func() { log.Logger = oldLogger }()
	pauseRepo := new(storagemocks.DispatchPauseRepository)
	pauseRepo.On("GetAll").Return([]*data.DispatchPause{{Scope: "c1", ConsumerID: "c1", PausedAt: time.Now()}}, nil).Once()
	pauseRepo.On("GetAll").Return(nil, errors.New("db down")).Once()
	consumerRepo := new(storagemocks.ConsumerRepository)
	consumerRepo.On("GetByID", mock.Anything).Return(nil, errors.New("not found"))
	registry := NewPauseRegistry(pauseRepo, consumerRepo, newPauseConfigMock(time.Second), counter)
	assert.Error(t, registry.Refresh())
	assert.True(t, registry.IsEffectivelyPaused("c1"), "a failed refresh must keep the previous snapshot")
	assert.Contains(t, buf.String(), "keeping the previous one")
}

func TestPauseRegistryStartStop(t *testing.T) {
	pauseRepo := new(storagemocks.DispatchPauseRepository)
	var calls int32
	pauseRepo.On("GetAll").Run(func(mock.Arguments) { atomic.AddInt32(&calls, 1) }).Return([]*data.DispatchPause{}, nil)
	registry := NewPauseRegistry(pauseRepo, new(storagemocks.ConsumerRepository), newPauseConfigMock(5*time.Millisecond), counter)
	registry.Start()
	registry.Start() // idempotent
	time.Sleep(30 * time.Millisecond)
	registry.Stop()
	registry.Stop() // idempotent
	stoppedAt := atomic.LoadInt32(&calls)
	assert.GreaterOrEqual(t, stoppedAt, int32(3), "initial refresh plus periodic refreshes")
	time.Sleep(15 * time.Millisecond)
	assert.Equal(t, stoppedAt, atomic.LoadInt32(&calls), "no refresh after Stop")
}

func TestPauseRegistryGauge(t *testing.T) {
	channelID := "gauge-channel-" + xid.New().String()
	consumer := &data.Consumer{ConsumerID: "gauge-consumer", ConsumingFrom: &data.Channel{ChannelID: channelID}}
	pauseRepo := new(storagemocks.DispatchPauseRepository)
	pauseRepo.On("GetAll").Return([]*data.DispatchPause{
		{Scope: "gauge-c1", ConsumerID: "gauge-c1", PausedAt: time.Now()},
		{Scope: data.GlobalDispatchPauseScope, PausedAt: time.Now()},
	}, nil).Once()
	consumerRepo := new(storagemocks.ConsumerRepository)
	consumerRepo.On("GetByID", "gauge-c1").Return(consumer, nil)
	registry := NewPauseRegistry(pauseRepo, consumerRepo, newPauseConfigMock(time.Second), counter)
	labels := gaugeLabels(counter.DispatchPaused)
	assert.Equal(t, float64(1), labels[channelID+"|gauge-consumer"])
	assert.Equal(t, float64(1), labels["*|*"])
	registry.Remove("gauge-c1")
	registry.Remove(data.GlobalDispatchPauseScope)
	labels = gaugeLabels(counter.DispatchPaused)
	_, consumerPresent := labels[channelID+"|gauge-consumer"]
	_, globalPresent := labels["*|*"]
	assert.False(t, consumerPresent)
	assert.False(t, globalPresent)
}

func TestPauseRegistry_Race(t *testing.T) {
	pauseRepo := new(storagemocks.DispatchPauseRepository)
	pauseRepo.On("GetAll").Return([]*data.DispatchPause{{Scope: "race-c1", ConsumerID: "race-c1", PausedAt: time.Now()}}, nil)
	consumerRepo := new(storagemocks.ConsumerRepository)
	consumerRepo.On("GetByID", mock.Anything).Return(nil, errors.New("not found"))
	registry := NewPauseRegistry(pauseRepo, consumerRepo, newPauseConfigMock(time.Millisecond), counter)
	registry.Start()
	defer registry.Stop()
	var wg sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				registry.IsEffectivelyPaused("race-c1")
				registry.IsGloballyPaused()
				registry.Snapshot()
			}
		}()
		go func(worker int) {
			defer wg.Done()
			scope := "race-w" + strconv.Itoa(worker)
			for i := 0; i < 100; i++ {
				registry.Apply(&data.DispatchPause{Scope: scope, ConsumerID: scope, PausedAt: time.Now()})
				registry.Remove(scope)
				if i%10 == 0 {
					registry.Refresh()
				}
			}
		}(worker)
	}
	wg.Wait()
	assert.True(t, registry.IsEffectivelyPaused("race-c1"))
}

// pauseTestFixture creates isolated consumers whose callbacks hit the shared test consumer server; handler keys are
// returned per consumer
func pauseTestFixture(t *testing.T, prefix string, push, pull int) (*storage.PauseFixture, []string) {
	t.Helper()
	unique := prefix + "-" + xid.New().String()
	keys := make([]string, push+pull)
	fixture, err := storage.SetupPauseFixture(dataAccessor, unique+"-", push, pull, func(i int) *url.URL {
		keys[i] = unique + "-cb-" + strconv.Itoa(i)
		callback, _ := url.Parse("http://localhost" + server.Addr + "/" + keys[i])
		return callback
	})
	if err != nil {
		t.Fatal(err)
	}
	return fixture, keys
}

func pauseInDB(t *testing.T, consumer *data.Consumer) {
	t.Helper()
	pause, _ := data.NewConsumerDispatchPause(consumer, "test")
	if _, err := dataAccessor.GetDispatchPauseRepository().Pause(pause); err != nil {
		t.Fatal(err)
	}
}

func resumeInDB(consumer *data.Consumer) {
	dataAccessor.GetDispatchPauseRepository().Resume(consumer.ID.String())
}

func placement(t *testing.T, msg *data.Message, consumer *data.Consumer) (int, int) {
	t.Helper()
	db := getTestDB(t)
	inJob, inParking, err := storage.JobPlacement(db, msg.ID.String(), consumer.ID.String())
	assert.NoError(t, err)
	return inJob, inParking
}

func TestDispatchParksPausedConsumers(t *testing.T) {
	fixture, keys := pauseTestFixture(t, "dispatch-parks", 3, 1)
	pausedPush, pausedPull := fixture.Push[0], fixture.Pull[0]
	pauseInDB(t, pausedPush)
	pauseInDB(t, pausedPull)
	defer resumeInDB(pausedPush)
	defer resumeInDB(pausedPull)
	var pausedCalls int32
	delivered := &sync.WaitGroup{}
	delivered.Add(2)
	consumerHandler[keys[0]] = func(string, http.ResponseWriter, *http.Request) { atomic.AddInt32(&pausedCalls, 1) }
	for _, key := range keys[1:3] {
		consumerHandler[key] = func(_ string, w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
			delivered.Done()
		}
	}
	defer func() {
		for _, key := range keys {
			delete(consumerHandler, key)
		}
	}()
	brokerConf := getMockedBrokerConfig()
	msgDispatcher := NewMessageDispatcher(getCompleteDispatcherConfiguration(dataAccessor.GetMessageRepository(), dataAccessor.GetDeliveryJobRepository(), dataAccessor.GetConsumerRepository(), brokerConf, configuration, dataAccessor.GetLockRepository()))
	defer msgDispatcher.Stop()
	parkedBefore := counterValue(t, counter.ParkedJobsTotal.WithLabelValues(ParkReasonDispatch))
	msg, _ := data.NewMessage(fixture.Channel, fixture.Producer, "payload", "text/plain", data.HeadersMap{})
	assert.NoError(t, dataAccessor.GetMessageRepository().Create(msg))
	msgDispatcher.Dispatch(msg)
	assert.False(t, waitTimeout(delivered, 3*time.Second), "active consumers must receive the message")
	time.Sleep(300 * time.Millisecond)
	assert.Equal(t, int32(0), atomic.LoadInt32(&pausedCalls), "a paused consumer must never be called")
	inJob, inParking := placement(t, msg, pausedPush)
	assert.Equal(t, 0, inJob)
	assert.Equal(t, 1, inParking)
	inJob, inParking = placement(t, msg, pausedPull)
	assert.Equal(t, 0, inJob)
	assert.Equal(t, 1, inParking)
	inJob, _ = placement(t, msg, fixture.Push[1])
	assert.Equal(t, 1, inJob)
	assert.Equal(t, float64(2), counterValue(t, counter.ParkedJobsTotal.WithLabelValues(ParkReasonDispatch))-parkedBefore)
}

func newBareDispatcher(registry PauseRegistry, parkingRepo storage.JobParkingRepository) *MessageDispatcherImpl {
	noLimit := func(channelID, consumerID string) uint { return 0 }
	return &MessageDispatcherImpl{jobQueue: make(chan *Job, 10), scheduler: newLaneScheduler(noLimit, counter),
		pauseRegistry: registry, jobParkingRepo: parkingRepo, metrics: counter}
}

func newPushJob(consumerType data.ConsumerType) *data.DeliveryJob {
	consumer := &data.Consumer{Type: consumerType}
	consumer.ID = xid.New()
	job := &data.DeliveryJob{Listener: consumer, Message: &data.Message{}}
	job.ID = xid.New()
	return job
}

func TestQueueJobReparksWhenSnapshotPaused(t *testing.T) {
	pausedJob := newPushJob(data.PushConsumer)
	activeJob := newPushJob(data.PushConsumer)
	pullJob := newPushJob(data.PullConsumer)
	parkingRepo := new(storagemocks.JobParkingRepository)
	parkingRepo.On("ReparkQueuedJob", pausedJob).Return(true, nil).Once()
	impl := newBareDispatcher(newFakePauseRegistry(pausedJob.Listener.ID.String()), parkingRepo)
	before := counterValue(t, counter.ParkedJobsTotal.WithLabelValues(ParkReasonRetry))
	queueJob(impl, pausedJob)
	queueJob(impl, pullJob)
	assert.Equal(t, 0, len(impl.jobQueue), "paused and pull jobs are never enqueued")
	queueJob(impl, activeJob)
	assert.Equal(t, 1, len(impl.jobQueue))
	parkingRepo.AssertExpectations(t)
	assert.Equal(t, float64(1), counterValue(t, counter.ParkedJobsTotal.WithLabelValues(ParkReasonRetry))-before)

	t.Run("ReparkErrorIsLogged", func(t *testing.T) {
		var buf bytes.Buffer
		oldLogger := log.Logger
		log.Logger = log.Output(&buf)
		defer func() { log.Logger = oldLogger }()
		failing := new(storagemocks.JobParkingRepository)
		failing.On("ReparkQueuedJob", pausedJob).Return(false, errors.New("db down"))
		impl := newBareDispatcher(newFakePauseRegistry(pausedJob.Listener.ID.String()), failing)
		queueJob(impl, pausedJob)
		assert.Equal(t, 0, len(impl.jobQueue))
		assert.Contains(t, buf.String(), "could not re-park job")
	})
}

func TestEnqueueJobsAndQueueDepth(t *testing.T) {
	activeJob := newPushJob(data.PushConsumer)
	pausedJob := newPushJob(data.PushConsumer)
	pullJob := newPushJob(data.PullConsumer)
	parkingRepo := new(storagemocks.JobParkingRepository)
	parkingRepo.On("ReparkQueuedJob", pausedJob).Return(true, nil).Once()
	impl := newBareDispatcher(newFakePauseRegistry(pausedJob.Listener.ID.String()), parkingRepo)
	before := counterValue(t, counter.ParkedJobsTotal.WithLabelValues(ParkReasonHandoff))
	enqueued := impl.EnqueueJobs([]*data.DeliveryJob{activeJob, pausedJob, pullJob, nil, {}})
	assert.Equal(t, 1, enqueued)
	assert.Equal(t, 1, impl.QueueDepth())
	impl.scheduler.enqueue(NewJob(activeJob))
	defer impl.scheduler.discardQueued()
	assert.Equal(t, 2, impl.QueueDepth())
	parkingRepo.AssertExpectations(t)
	assert.Equal(t, float64(1), counterValue(t, counter.ParkedJobsTotal.WithLabelValues(ParkReasonHandoff))-before)
}

func TestDeliverJobPauseGateReparks(t *testing.T) {
	fixture, _ := pauseTestFixture(t, "worker-gate", 1, 0)
	consumer := fixture.Push[0]
	job, err := setupTestJobFor(fixture, consumer)
	assert.NoError(t, err)
	// Pause behind the registry's back: only the DB knows; the worker must still refuse and park
	pauseInDB(t, consumer)
	defer resumeInDB(consumer)
	oldCallConsumer := callConsumer
	defer func() { callConsumer = oldCallConsumer }()
	callConsumer = func(worker *Worker, requestID string, logger zerolog.Logger, job *Job) error {
		t.Error("a paused consumer must never be called")
		return nil
	}
	worker := NewWorker(make(chan chan *Job, 1), configuration, getMockedBrokerConfig(), dataAccessor.GetDeliveryJobRepository(), dataAccessor.GetJobParkingRepository(), counter)
	before := counterValue(t, counter.ParkedJobsTotal.WithLabelValues(ParkReasonWorker))
	deliverJob(&worker, NewJob(job))
	inJob, inParking := placement(t, job.Message, consumer)
	assert.Equal(t, 0, inJob)
	assert.Equal(t, 1, inParking)
	assert.Equal(t, float64(1), counterValue(t, counter.ParkedJobsTotal.WithLabelValues(ParkReasonWorker))-before)
}

func TestDeliverJobInflightRaceNotParked(t *testing.T) {
	var buf bytes.Buffer
	oldLogger := log.Logger
	log.Logger = log.Output(&buf)
	defer func() { log.Logger = oldLogger }()
	fixture, _ := pauseTestFixture(t, "worker-race", 1, 0)
	consumer := fixture.Push[0]
	job, err := setupTestJobFor(fixture, consumer)
	assert.NoError(t, err)
	staleCopy := *job
	assert.NoError(t, dataAccessor.GetDeliveryJobRepository().MarkJobInflight(job))
	worker := NewWorker(make(chan chan *Job, 1), configuration, getMockedBrokerConfig(), dataAccessor.GetDeliveryJobRepository(), dataAccessor.GetJobParkingRepository(), counter)
	deliverJob(&worker, NewJob(&staleCopy))
	assert.Contains(t, buf.String(), "could not put job in flight")
	inJob, inParking := placement(t, job.Message, consumer)
	assert.Equal(t, 1, inJob)
	assert.Equal(t, 0, inParking)
}

func TestRetryQueuedJobsReparksPausedPush(t *testing.T) {
	fixture, _ := pauseTestFixture(t, "retry-repark", 1, 1)
	pushConsumer, pullConsumer := fixture.Push[0], fixture.Pull[0]
	pushJob, err := setupTestJobFor(fixture, pushConsumer)
	assert.NoError(t, err)
	pullJob, err := setupTestJobFor(fixture, pullConsumer)
	assert.NoError(t, err)
	db := getTestDB(t)
	// Make both jobs due for the retry worker; the pull job has exhausted its retries
	_, err = db.Exec("UPDATE job SET earliestNextAttemptAt = ? WHERE id IN (?, ?)", time.Now().Add(-1*time.Hour), pushJob.ID.String(), pullJob.ID.String())
	assert.NoError(t, err)
	_, err = db.Exec("UPDATE job SET retryAttemptCount = 5 WHERE id = ?", pullJob.ID.String())
	assert.NoError(t, err)
	pauseInDB(t, pushConsumer)
	pauseInDB(t, pullConsumer)
	defer resumeInDB(pushConsumer)
	defer resumeInDB(pullConsumer)
	brokerConf := getMockedBrokerConfig()
	msgDispatcher := NewMessageDispatcher(getCompleteDispatcherConfiguration(dataAccessor.GetMessageRepository(), dataAccessor.GetDeliveryJobRepository(), dataAccessor.GetConsumerRepository(), brokerConf, configuration, dataAccessor.GetLockRepository()))
	defer msgDispatcher.Stop()
	impl := msgDispatcher.(*MessageDispatcherImpl)
	retryQueuedJobs(impl)
	inJob, inParking := placement(t, pushJob.Message, pushConsumer)
	assert.Equal(t, 0, inJob)
	assert.Equal(t, 1, inParking, "a due push job of a paused consumer is parked instead of enqueued")
	reloaded, err := dataAccessor.GetDeliveryJobRepository().GetByID(pullJob.ID.String())
	assert.NoError(t, err)
	assert.Equal(t, data.JobDead, reloaded.Status, "exhausted pull jobs are still marked dead while paused")
	// The parked job is no longer visible to the retry scan
	for _, ready := range dataAccessor.GetDeliveryJobRepository().GetJobsReadyForInflightSince(0, 5) {
		assert.NotEqual(t, pushJob.ID, ready.ID)
	}
}

func TestRecoverMessagesNotYetDispatchedWithPause(t *testing.T) {
	fixture, _ := pauseTestFixture(t, "recover-pause", 1, 0)
	consumer := fixture.Push[0]
	pauseInDB(t, consumer)
	defer resumeInDB(consumer)
	msg, _ := data.NewMessage(fixture.Channel, fixture.Producer, "payload", "text/plain", data.HeadersMap{})
	msg.ReceivedAt = msg.ReceivedAt.Add(-1 * time.Minute)
	assert.NoError(t, dataAccessor.GetMessageRepository().Create(msg))
	brokerConf := getMockedBrokerConfig()
	msgDispatcher := NewMessageDispatcher(getCompleteDispatcherConfiguration(dataAccessor.GetMessageRepository(), dataAccessor.GetDeliveryJobRepository(), dataAccessor.GetConsumerRepository(), brokerConf, configuration, dataAccessor.GetLockRepository()))
	defer msgDispatcher.Stop()
	recoverMessagesNotYetDispatched(msgDispatcher.(*MessageDispatcherImpl))
	inJob, inParking := placement(t, msg, consumer)
	assert.Equal(t, 0, inJob)
	assert.Equal(t, 1, inParking)
}

func TestNewMessageDispatcherRequiresPauseDependencies(t *testing.T) {
	cfg := getCompleteDispatcherConfiguration(dataAccessor.GetMessageRepository(), dataAccessor.GetDeliveryJobRepository(), dataAccessor.GetConsumerRepository(), getMockedBrokerConfig(), configuration, dataAccessor.GetLockRepository())
	withoutRegistry := *cfg
	withoutRegistry.PauseRegistry = nil
	assert.PanicsWithValue(t, panicString, func() { NewMessageDispatcher(&withoutRegistry) })
	withoutParking := *cfg
	withoutParking.JobParkingRepo = nil
	assert.PanicsWithValue(t, panicString, func() { NewMessageDispatcher(&withoutParking) })
}

// Generated with assistance from Claude AI

var (
	testDBOnce sync.Once
	testDB     *sql.DB
)

// getTestDB opens a second handle on the dispatcher test database for direct assertions
func getTestDB(t *testing.T) *sql.DB {
	t.Helper()
	testDBOnce.Do(func() {
		var err error
		testDB, err = sql.Open(string(configuration.GetDBDialect()), configuration.GetDBConnectionURL())
		if err != nil {
			t.Fatal(err)
		}
	})
	return testDB
}

// setupTestJobFor dispatches a fresh message of the fixture to the consumer only and returns its QUEUED job
func setupTestJobFor(fixture *storage.PauseFixture, consumer *data.Consumer) (*data.DeliveryJob, error) {
	message, err := data.NewMessage(fixture.Channel, fixture.Producer, "payload", "text/plain", data.HeadersMap{})
	if err != nil {
		return nil, err
	}
	if err = dataAccessor.GetMessageRepository().Create(message); err != nil {
		return nil, err
	}
	job, err := data.NewDeliveryJob(message, consumer)
	if err != nil {
		return nil, err
	}
	return job, dataAccessor.GetDeliveryJobRepository().DispatchMessage(message, job)
}

// waitTimeout waits for the group; returns true if it timed out
func waitTimeout(wg *sync.WaitGroup, timeout time.Duration) bool {
	done := make(chan struct{})
	go func() {
		defer close(done)
		wg.Wait()
	}()
	select {
	case <-done:
		return false
	case <-time.After(timeout):
		return true
	}
}
