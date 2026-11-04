package parking

import (
	"database/sql"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/rs/xid"
	"github.com/stretchr/testify/assert"

	"github.com/newscred/webhook-broker/config"
	configmocks "github.com/newscred/webhook-broker/config/mocks"
	"github.com/newscred/webhook-broker/dispatcher"
	dispatchermocks "github.com/newscred/webhook-broker/dispatcher/mocks"
	"github.com/newscred/webhook-broker/storage"
	"github.com/newscred/webhook-broker/storage/data"
)

// Component tests: real SQLite (through the real migrations), real repositories and lock, the hand-off sender talking
// to a recording HTTP server.

var (
	dataAccessor  storage.DataAccessor
	configuration *config.Config
	componentDB   *sql.DB
)

func TestMain(m *testing.M) {
	os.Remove("./webhook-broker.sqlite3")
	var err error
	configuration, err = config.GetAutoConfiguration()
	if err != nil {
		panic(err)
	}
	migrationLocation, _ := filepath.Abs("../migration/sqls/")
	dataAccessor, err = storage.GetNewDataAccessor(configuration, &storage.MigrationConfig{MigrationEnabled: true, MigrationSource: "file://" + migrationLocation}, configuration)
	if err != nil {
		panic(err)
	}
	componentDB, err = sql.Open(string(configuration.GetDBDialect()), configuration.GetDBConnectionURL())
	if err != nil {
		panic(err)
	}
	code := m.Run()
	componentDB.Close()
	dataAccessor.Close()
	os.Exit(code)
}

func newComponentDrainer(t *testing.T, pauseConfig *fakePauseConfig, recorder *handoffRecorder) *DrainerImpl {
	t.Helper()
	brokerConfig := new(configmocks.BrokerConfig)
	brokerConfig.On("GetRetriggerBaseEndpoint").Return(recorder.server.URL)
	cfg := NewDrainerConfiguration(dataAccessor.GetJobParkingRepository(), dataAccessor.GetLockRepository(), dataAccessor.GetConsumerRepository(),
		new(dispatchermocks.MessageDispatcher), pauseConfig, brokerConfig, &fakeGatewayAuthConfig{}, dispatcher.NewMetricsContainer())
	impl := NewDrainer(cfg).(*DrainerImpl)
	impl.lastCountRefresh = time.Now()
	return impl
}

// parkJobs pauses the consumers, dispatches messages so their jobs are parked and returns the parked job ids
func parkJobs(t *testing.T, prefix string, consumers, messages int) (*storage.PauseFixture, map[string][]string) {
	t.Helper()
	callback, _ := url.Parse("http://localhost:1/")
	fixture, err := storage.SetupPauseFixture(dataAccessor, prefix+"-"+xid.New().String()+"-", consumers, 0, func(int) *url.URL { return callback })
	if err != nil {
		t.Fatal(err)
	}
	pauseRepo := dataAccessor.GetDispatchPauseRepository()
	for _, consumer := range fixture.Consumers {
		pause, _ := data.NewConsumerDispatchPause(consumer, "component test")
		if _, err = pauseRepo.Pause(pause); err != nil {
			t.Fatal(err)
		}
	}
	parkedIDs := map[string][]string{}
	for i := 0; i < messages; i++ {
		msg, _ := data.NewMessage(fixture.Channel, fixture.Producer, "payload", "text/plain", data.HeadersMap{})
		if err = dataAccessor.GetMessageRepository().Create(msg); err != nil {
			t.Fatal(err)
		}
		parked := make([]*data.DeliveryJob, 0, len(fixture.Consumers))
		for _, consumer := range fixture.Consumers {
			job, _ := data.NewDeliveryJob(msg, consumer)
			parked = append(parked, job)
			parkedIDs[consumer.ID.String()] = append(parkedIDs[consumer.ID.String()], job.ID.String())
		}
		if err = dataAccessor.GetDeliveryJobRepository().DispatchMessageWithParking(msg, nil, parked); err != nil {
			t.Fatal(err)
		}
	}
	return fixture, parkedIDs
}

func resumeAll(t *testing.T, fixture *storage.PauseFixture) {
	t.Helper()
	for _, consumer := range fixture.Consumers {
		if _, err := dataAccessor.GetDispatchPauseRepository().Resume(consumer.ID.String()); err != nil {
			t.Fatal(err)
		}
	}
}

func parkedCount(t *testing.T, fixture *storage.PauseFixture) int {
	t.Helper()
	total := 0
	for _, consumer := range fixture.Consumers {
		count := 0
		assert.NoError(t, componentDB.QueryRow("SELECT COUNT(*) FROM job_parking WHERE consumerId = ?", consumer.ID.String()).Scan(&count))
		total += count
	}
	return total
}

func TestDrainerEndToEnd(t *testing.T) {
	recorder := newHandoffRecorder(http.StatusAccepted)
	defer recorder.server.Close()
	fixture, parkedIDs := parkJobs(t, "e2e", 3, 40)
	resumeAll(t, fixture)
	pauseConfig := newFakePauseConfig()
	pauseConfig.concurrency = 1 // keeps hand-off arrival order equal to drain order
	drainer := newComponentDrainer(t, pauseConfig, recorder)
	drainer.handoff.Start()
	for run := 0; run < 200 && parkedCount(t, fixture) > 0; run++ {
		drainer.runOnce()
	}
	drainer.handoff.Stop()
	assert.Equal(t, 0, parkedCount(t, fixture))
	received := recorder.receivedIDs()
	seen := map[string]int{}
	for _, id := range received {
		seen[id]++
	}
	expected := 0
	for _, ids := range parkedIDs {
		expected += len(ids)
		for _, id := range ids {
			assert.Equal(t, 1, seen[id], "every drained job is handed off exactly once")
		}
	}
	assert.Equal(t, expected, len(received))
	// Within a consumer, jobs arrive oldest first (they were parked in id order, one message at a time)
	for consumerID, ids := range parkedIDs {
		position := map[string]int{}
		for index, id := range received {
			position[id] = index
		}
		for i := 1; i < len(ids); i++ {
			assert.Less(t, position[ids[i-1]], position[ids[i]], "consumer %s jobs must be handed off oldest first", consumerID)
		}
	}
	for _, ids := range parkedIDs {
		for _, id := range ids {
			job, err := dataAccessor.GetDeliveryJobRepository().GetByID(id)
			if assert.NoError(t, err) {
				assert.Equal(t, data.JobQueued, job.Status)
			}
		}
	}
	assert.NoError(t, storage.CheckParkingInvariants(componentDB))
}

func TestDrainerRepauseMidDrain(t *testing.T) {
	recorder := newHandoffRecorder(http.StatusAccepted)
	defer recorder.server.Close()
	fixture, parkedIDs := parkJobs(t, "repause", 1, 10)
	consumer := fixture.Consumers[0]
	resumeAll(t, fixture)
	pauseConfig := newFakePauseConfig()
	pauseConfig.pageSize = 3
	pauseConfig.concurrency = 1 // sender not started: saturated after two pages
	drainer := newComponentDrainer(t, pauseConfig, recorder)
	drainer.runOnce()
	assert.Equal(t, 4, parkedCount(t, fixture), "two pages of three were moved before the hand-off saturated")
	pause, _ := data.NewConsumerDispatchPause(consumer, "paused again")
	_, err := dataAccessor.GetDispatchPauseRepository().Pause(pause)
	assert.NoError(t, err)
	drainer.handoff.pending = make(chan []*data.DeliveryJob, 2) // unblock the sender for the next run
	drainer.runOnce()
	assert.Equal(t, 4, parkedCount(t, fixture), "a paused consumer is not drained")
	// The six moved jobs are QUEUED; a receiving node re-parks them since the consumer is paused again
	reparked := 0
	for _, id := range parkedIDs[consumer.ID.String()] {
		job, err := dataAccessor.GetDeliveryJobRepository().GetByID(id)
		assert.NoError(t, err)
		if job.Status == data.JobQueued {
			moved, err := dataAccessor.GetJobParkingRepository().ReparkQueuedJob(job)
			assert.NoError(t, err)
			assert.True(t, moved)
			reparked++
		}
	}
	assert.Equal(t, 6, reparked)
	assert.Equal(t, 10, parkedCount(t, fixture))
}

// TestDrainerTwoInstances_Race runs two drainers on the same database while their lock is repeatedly forced to lapse,
// so both actively drain; no job may be handed off twice
func TestDrainerTwoInstances_Race(t *testing.T) {
	recorder := newHandoffRecorder(http.StatusAccepted)
	defer recorder.server.Close()
	fixture, parkedIDs := parkJobs(t, "two-drainers", 2, 25)
	resumeAll(t, fixture)
	drainers := []*DrainerImpl{newComponentDrainer(t, newFakePauseConfig(), recorder), newComponentDrainer(t, newFakePauseConfig(), recorder)}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				dataAccessor.GetLockRepository().TimeoutLocks(0)
				time.Sleep(time.Millisecond)
			}
		}
	}()
	for _, drainer := range drainers {
		drainer.handoff.Start()
		wg.Add(1)
		go func(drainer *DrainerImpl) {
			defer wg.Done()
			for run := 0; run < 500; run++ {
				select {
				case <-stop:
					return
				default:
				}
				drainer.runOnce()
			}
		}(drainer)
	}
	deadline := time.Now().Add(20 * time.Second)
	for parkedCount(t, fixture) > 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	close(stop)
	wg.Wait()
	for _, drainer := range drainers {
		drainer.handoff.Stop()
	}
	assert.Equal(t, 0, parkedCount(t, fixture))
	seen := map[string]int{}
	for _, id := range recorder.receivedIDs() {
		seen[id]++
	}
	for _, ids := range parkedIDs {
		for _, id := range ids {
			assert.Equal(t, 1, seen[id], "job handed off more than once or never: "+id)
		}
	}
	assert.NoError(t, storage.CheckParkingInvariants(componentDB))
}

func TestDrainerCrashAfterPageBeforeHandoff(t *testing.T) {
	defer noSleep(t)()
	recorder := newHandoffRecorder(http.StatusInternalServerError) // every hand-off is lost
	defer recorder.server.Close()
	fixture, parkedIDs := parkJobs(t, "lost-handoff", 1, 3)
	resumeAll(t, fixture)
	pauseConfig := newFakePauseConfig()
	pauseConfig.attempts = 1
	pauseConfig.fallback = false
	pauseConfig.lease = 300 * time.Millisecond
	drainer := newComponentDrainer(t, pauseConfig, recorder)
	drainer.handoff.Start()
	drainer.runOnce()
	drainer.handoff.Stop()
	assert.Equal(t, 0, parkedCount(t, fixture))
	ids := parkedIDs[fixture.Consumers[0].ID.String()]
	isReady := func() int {
		ready := 0
		for _, job := range dataAccessor.GetDeliveryJobRepository().GetJobsReadyForInflightSince(0, 5) {
			for _, id := range ids {
				if job.ID.String() == id {
					ready++
				}
			}
		}
		return ready
	}
	assert.Equal(t, 0, isReady(), "jobs are hidden from the retry worker during the hand-off lease")
	time.Sleep(400 * time.Millisecond)
	assert.Equal(t, len(ids), isReady(), "once the lease passes the retry worker picks the jobs up")
}

// Generated with assistance from Claude AI
