package storage

import (
	"database/sql"
	"errors"
	"net/url"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/rs/xid"
	"github.com/stretchr/testify/assert"

	"github.com/newscred/webhook-broker/storage/data"
)

func newParkingMessage(t *testing.T, fixture *PauseFixture) *data.Message {
	t.Helper()
	msg, err := data.NewMessage(fixture.Channel, fixture.Producer, samplePayload, sampleContentType, data.HeadersMap{})
	if err != nil {
		t.Fatal(err)
	}
	if err = getMessageRepository().Create(msg); err != nil {
		t.Fatal(err)
	}
	return msg
}

// dispatchSplit dispatches msg to consumers, parking the ones whose internal id is in parked
func dispatchSplit(t *testing.T, msg *data.Message, consumers []*data.Consumer, parked map[string]bool) (jobs, parkedJobs []*data.DeliveryJob) {
	t.Helper()
	for _, consumer := range consumers {
		job, err := data.NewDeliveryJob(msg, consumer)
		if err != nil {
			t.Fatal(err)
		}
		if parked[consumer.ID.String()] {
			parkedJobs = append(parkedJobs, job)
		} else {
			jobs = append(jobs, job)
		}
	}
	if err := getDeliverJobRepository().DispatchMessageWithParking(msg, jobs, parkedJobs); err != nil {
		t.Fatal(err)
	}
	return jobs, parkedJobs
}

func pauseConsumerForTest(t *testing.T, consumer *data.Consumer) {
	t.Helper()
	pause, _ := data.NewConsumerDispatchPause(consumer, "test")
	if _, err := NewDispatchPauseRepository(testDB).Pause(pause); err != nil {
		t.Fatal(err)
	}
}

func resumeConsumerForTest(t *testing.T, consumer *data.Consumer) {
	t.Helper()
	if _, err := NewDispatchPauseRepository(testDB).Resume(consumer.ID.String()); err != nil {
		t.Fatal(err)
	}
}

func assertPlacement(t *testing.T, msg *data.Message, consumer *data.Consumer, expectedInJob, expectedInParking int) {
	t.Helper()
	inJob, inParking, err := JobPlacement(testDB, msg.ID.String(), consumer.ID.String())
	assert.NoError(t, err)
	assert.Equal(t, expectedInJob, inJob, "rows in job")
	assert.Equal(t, expectedInParking, inParking, "rows in job_parking")
}

func getJobParkingRepository() JobParkingRepository {
	return NewJobParkingRepository(testDB, getMessageRepository(), getConsumerRepo())
}

func parkedCountFor(t *testing.T, consumer *data.Consumer) int {
	t.Helper()
	count := 0
	assert.NoError(t, testDB.QueryRow("SELECT COUNT(*) FROM job_parking WHERE consumerId = ?", consumer.ID.String()).Scan(&count))
	return count
}

func TestDispatchMessageWithParking(t *testing.T) {
	t.Run("SplitAcrossTables", func(t *testing.T) {
		t.Parallel()
		fixture := newPauseFixture(t, "dmwp-split", 3, 0)
		msg := newParkingMessage(t, fixture)
		_, parkedJobs := dispatchSplit(t, msg, fixture.Consumers, map[string]bool{fixture.Push[0].ID.String(): true})
		assert.Equal(t, data.MsgStatusDispatched, msg.Status)
		assert.Equal(t, data.JobParked, parkedJobs[0].Status)
		assertPlacement(t, msg, fixture.Push[0], 0, 1)
		assertPlacement(t, msg, fixture.Push[1], 1, 0)
		assertPlacement(t, msg, fixture.Push[2], 1, 0)
		assert.NoError(t, CheckParkingInvariants(testDB, fixture.Push[1].ID.String(), fixture.Push[2].ID.String()))
		assert.NoError(t, CheckMessageJobCount(testDB, msg.ID.String(), 3))
	})
	t.Run("AllParked", func(t *testing.T) {
		t.Parallel()
		fixture := newPauseFixture(t, "dmwp-all", 2, 1)
		msg := newParkingMessage(t, fixture)
		parked := map[string]bool{}
		for _, consumer := range fixture.Consumers {
			parked[consumer.ID.String()] = true
		}
		dispatchSplit(t, msg, fixture.Consumers, parked)
		assert.Equal(t, data.MsgStatusDispatched, msg.Status)
		for _, consumer := range fixture.Consumers {
			assertPlacement(t, msg, consumer, 0, 1)
		}
	})
	t.Run("NoneParked", func(t *testing.T) {
		t.Parallel()
		fixture := newPauseFixture(t, "dmwp-none", 2, 0)
		msg := newParkingMessage(t, fixture)
		dispatchSplit(t, msg, fixture.Consumers, nil)
		for _, consumer := range fixture.Consumers {
			assertPlacement(t, msg, consumer, 1, 0)
		}
	})
	t.Run("AtomicRollback", func(t *testing.T) {
		t.Parallel()
		fixture := newPauseFixture(t, "dmwp-rollback", 3, 0)
		msg := newParkingMessage(t, fixture)
		// The message stays ACKNOWLEDGED after the rollback; remove it so tests counting undispatched messages are unaffected
		defer getMessageRepository().DeleteMessage(msg)
		// A pre-existing parked row for (msg, consumer-0) makes the parking insert violate the unique key
		conflictingParkedID := xid.New().String()
		_, err := testDB.Exec("INSERT INTO job_parking (id, messageId, consumerId, dispatchReceivedAt, retryAttemptCount, earliestNextAttemptAt, priority, incrementalTimeout, createdAt, parkedAt) VALUES (?, ?, ?, ?, 0, ?, 0, 0, ?, ?)",
			conflictingParkedID, msg.ID.String(), fixture.Push[0].ID.String(), time.Now(), time.Now(), time.Now(), time.Now())
		assert.NoError(t, err)
		defer testDB.Exec("DELETE FROM job_parking WHERE id = ?", conflictingParkedID)
		jobs := make([]*data.DeliveryJob, 0, 2)
		for _, consumer := range fixture.Push[1:] {
			job, _ := data.NewDeliveryJob(msg, consumer)
			jobs = append(jobs, job)
		}
		parkedJob, _ := data.NewDeliveryJob(msg, fixture.Push[0])
		err = getDeliverJobRepository().DispatchMessageWithParking(msg, jobs, []*data.DeliveryJob{parkedJob})
		assert.Error(t, err)
		assertPlacement(t, msg, fixture.Push[1], 0, 0)
		assertPlacement(t, msg, fixture.Push[2], 0, 0)
		reloaded, err := getMessageRepository().GetByID(msg.ID.String())
		assert.NoError(t, err)
		assert.Equal(t, data.MsgStatusAcknowledged, reloaded.Status, "message must not be marked dispatched when the transaction rolls back")
	})
	t.Run("InvalidParkedJob", func(t *testing.T) {
		t.Parallel()
		fixture := newPauseFixture(t, "dmwp-invalid", 1, 0)
		msg := newParkingMessage(t, fixture)
		otherMsg := newParkingMessage(t, fixture)
		defer getMessageRepository().DeleteMessage(msg)
		defer getMessageRepository().DeleteMessage(otherMsg)
		parkedJob, _ := data.NewDeliveryJob(otherMsg, fixture.Push[0])
		err := getDeliverJobRepository().DispatchMessageWithParking(msg, nil, []*data.DeliveryJob{parkedJob})
		assert.Equal(t, ErrInvalidStateToSave, err)
		err = getDeliverJobRepository().DispatchMessageWithParking(msg, nil, []*data.DeliveryJob{nil})
		assert.Equal(t, ErrInvalidStateToSave, err)
	})
}

func TestReparkQueuedJob(t *testing.T) {
	parkingRepo := getJobParkingRepository()
	djRepo := getDeliverJobRepository()
	t.Run("PausedConsumerPreservesFields", func(t *testing.T) {
		t.Parallel()
		fixture := newPauseFixture(t, "repark-paused", 1, 0)
		msg := newParkingMessage(t, fixture)
		jobs, _ := dispatchSplit(t, msg, fixture.Consumers, nil)
		job := jobs[0]
		assert.NoError(t, djRepo.MarkJobInflight(job))
		assert.NoError(t, djRepo.MarkJobRetry(job, -1*time.Second))
		assert.Equal(t, uint(1), job.RetryAttemptCount)
		pauseConsumerForTest(t, fixture.Push[0])
		moved, err := parkingRepo.ReparkQueuedJob(job)
		assert.NoError(t, err)
		assert.True(t, moved)
		assert.Equal(t, data.JobParked, job.Status)
		assertPlacement(t, msg, fixture.Push[0], 0, 1)
		parked, err := parkingRepo.GetByID(job.ID.String())
		assert.NoError(t, err)
		assert.Equal(t, job.ID, parked.ID)
		assert.Equal(t, uint(1), parked.RetryAttemptCount)
		assert.Equal(t, job.Priority, parked.Priority)
		assert.Equal(t, job.IncrementalTimeout, parked.IncrementalTimeout)
		assert.True(t, job.CreatedAt.Equal(parked.CreatedAt))
		assert.Equal(t, data.JobParked, parked.Status)
		assert.Equal(t, msg.ID, parked.Message.ID)
		assert.Equal(t, fixture.Push[0].ConsumerID, parked.Listener.ConsumerID)
	})
	t.Run("NotPaused", func(t *testing.T) {
		t.Parallel()
		fixture := newPauseFixture(t, "repark-notpaused", 1, 0)
		msg := newParkingMessage(t, fixture)
		jobs, _ := dispatchSplit(t, msg, fixture.Consumers, nil)
		moved, err := parkingRepo.ReparkQueuedJob(jobs[0])
		assert.NoError(t, err)
		assert.False(t, moved)
		assert.Equal(t, data.JobQueued, jobs[0].Status)
		assertPlacement(t, msg, fixture.Push[0], 1, 0)
	})
	t.Run("NotQueued", func(t *testing.T) {
		t.Parallel()
		fixture := newPauseFixture(t, "repark-notqueued", 1, 0)
		msg := newParkingMessage(t, fixture)
		jobs, _ := dispatchSplit(t, msg, fixture.Consumers, nil)
		assert.NoError(t, djRepo.MarkJobInflight(jobs[0]))
		pauseConsumerForTest(t, fixture.Push[0])
		moved, err := parkingRepo.ReparkQueuedJob(jobs[0])
		assert.NoError(t, err)
		assert.False(t, moved)
		assertPlacement(t, msg, fixture.Push[0], 1, 0)
	})
	t.Run("AlreadyParked", func(t *testing.T) {
		t.Parallel()
		fixture := newPauseFixture(t, "repark-twice", 1, 0)
		msg := newParkingMessage(t, fixture)
		jobs, _ := dispatchSplit(t, msg, fixture.Consumers, nil)
		pauseConsumerForTest(t, fixture.Push[0])
		moved, err := parkingRepo.ReparkQueuedJob(jobs[0])
		assert.NoError(t, err)
		assert.True(t, moved)
		moved, err = parkingRepo.ReparkQueuedJob(jobs[0])
		assert.NoError(t, err)
		assert.False(t, moved)
		assertPlacement(t, msg, fixture.Push[0], 0, 1)
	})
	t.Run("Nil", func(t *testing.T) {
		t.Parallel()
		_, err := parkingRepo.ReparkQueuedJob(nil)
		assert.Equal(t, ErrInvalidStateToSave, err)
	})
	t.Run("DeleteMismatchRollsBack", func(t *testing.T) {
		t.Parallel()
		db, mock, _ := sqlmock.New()
		defer db.Close()
		mock.ExpectBegin()
		mock.ExpectExec("INSERT INTO job_parking").WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec("DELETE FROM job").WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectRollback()
		job := &data.DeliveryJob{}
		job.ID = xid.New()
		moved, err := NewJobParkingRepository(db, nil, nil).ReparkQueuedJob(job)
		assert.Equal(t, ErrNoRowsUpdated, err)
		assert.False(t, moved)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

// Global pause tests are sequential (see note in dispatchpauserepo_test.go)
func TestReparkQueuedJobGlobalPause(t *testing.T) {
	defer clearGlobalPause(t)
	fixture := newPauseFixture(t, "repark-global", 1, 0)
	msg := newParkingMessage(t, fixture)
	jobs, _ := dispatchSplit(t, msg, fixture.Consumers, nil)
	assert.NoError(t, InsertPauseRow(testDB, data.GlobalDispatchPauseScope, ""))
	moved, err := getJobParkingRepository().ReparkQueuedJob(jobs[0])
	assert.NoError(t, err)
	assert.True(t, moved)
	assertPlacement(t, msg, fixture.Push[0], 0, 1)
}

func TestGetDrainableConsumerIDs(t *testing.T) {
	parkingRepo := getJobParkingRepository()
	fixture := newPauseFixture(t, "drainable", 3, 0)
	parked := map[string]bool{}
	for _, consumer := range fixture.Consumers {
		pauseConsumerForTest(t, consumer)
		parked[consumer.ID.String()] = true
	}
	dispatchSplit(t, newParkingMessage(t, fixture), fixture.Consumers, parked)
	ids, err := parkingRepo.GetDrainableConsumerIDs()
	assert.NoError(t, err)
	for _, consumer := range fixture.Consumers {
		assert.NotContains(t, ids, consumer.ID.String(), "paused consumers are not drainable")
	}
	resumeConsumerForTest(t, fixture.Push[1])
	ids, err = parkingRepo.GetDrainableConsumerIDs()
	assert.NoError(t, err)
	assert.Contains(t, ids, fixture.Push[1].ID.String())
	assert.NotContains(t, ids, fixture.Push[0].ID.String())
	assert.NotContains(t, ids, fixture.Push[2].ID.String())

	t.Run("GlobalPauseBlocksAll", func(t *testing.T) {
		defer clearGlobalPause(t)
		assert.NoError(t, InsertPauseRow(testDB, data.GlobalDispatchPauseScope, ""))
		ids, err := parkingRepo.GetDrainableConsumerIDs()
		assert.NoError(t, err)
		assert.NotContains(t, ids, fixture.Push[1].ID.String())
	})
}

func TestDrainPage(t *testing.T) {
	parkingRepo := getJobParkingRepository()
	setupParked := func(t *testing.T, prefix string, messages int) (*PauseFixture, []*data.DeliveryJob) {
		fixture := newPauseFixture(t, prefix, 1, 0)
		pauseConsumerForTest(t, fixture.Push[0])
		parkedJobs := make([]*data.DeliveryJob, 0, messages)
		for i := 0; i < messages; i++ {
			_, parked := dispatchSplit(t, newParkingMessage(t, fixture), fixture.Consumers, map[string]bool{fixture.Push[0].ID.String(): true})
			parkedJobs = append(parkedJobs, parked...)
		}
		return fixture, parkedJobs
	}
	t.Run("OrderByCreatedAtThenSeq", func(t *testing.T) {
		t.Parallel()
		fixture, parked := setupParked(t, "drain-order", 5)
		base := time.Now().Add(-1 * time.Hour).Truncate(time.Second)
		offsets := []int{3, 1, 1, 0, 2} // j1 and j2 tie on createdAt; seq decides
		for index, job := range parked {
			_, err := testDB.Exec("UPDATE job_parking SET createdAt = ? WHERE id = ?", base.Add(time.Duration(offsets[index])*time.Minute), job.ID.String())
			assert.NoError(t, err)
		}
		resumeConsumerForTest(t, fixture.Push[0])
		drained, err := parkingRepo.DrainPage(fixture.Push[0].ID.String(), 10, time.Now())
		assert.NoError(t, err)
		expected := []xid.ID{parked[3].ID, parked[1].ID, parked[2].ID, parked[4].ID, parked[0].ID}
		actual := make([]xid.ID, 0, len(drained))
		for _, job := range drained {
			actual = append(actual, job.ID)
		}
		assert.Equal(t, expected, actual)
	})
	t.Run("PageSizeLimit", func(t *testing.T) {
		t.Parallel()
		fixture, _ := setupParked(t, "drain-pagesize", 5)
		resumeConsumerForTest(t, fixture.Push[0])
		consumerID := fixture.Push[0].ID.String()
		for _, expected := range []int{2, 2, 1, 0} {
			drained, err := parkingRepo.DrainPage(consumerID, 2, time.Now())
			assert.NoError(t, err)
			assert.Equal(t, expected, len(drained))
		}
		assert.Equal(t, 0, parkedCountFor(t, fixture.Push[0]))
	})
	t.Run("RestoredFields", func(t *testing.T) {
		t.Parallel()
		fixture, parked := setupParked(t, "drain-fields", 1)
		_, err := testDB.Exec("UPDATE job_parking SET retryAttemptCount = 3 WHERE id = ?", parked[0].ID.String())
		assert.NoError(t, err)
		resumeConsumerForTest(t, fixture.Push[0])
		earliest := time.Now().Add(1 * time.Minute)
		drained, err := parkingRepo.DrainPage(fixture.Push[0].ID.String(), 10, earliest)
		assert.NoError(t, err)
		if assert.Equal(t, 1, len(drained)) {
			job := drained[0]
			assert.Equal(t, parked[0].ID, job.ID)
			assert.Equal(t, data.JobQueued, job.Status)
			assert.Equal(t, uint(3), job.RetryAttemptCount)
			assert.True(t, earliest.Equal(job.EarliestNextAttemptAt))
			assert.Equal(t, samplePayload, job.Message.Payload)
			assert.Equal(t, fixture.Push[0].ConsumerID, job.Listener.ConsumerID)
		}
		reloaded, err := getDeliverJobRepository().GetByID(parked[0].ID.String())
		assert.NoError(t, err)
		assert.Equal(t, data.JobQueued, reloaded.Status)
		assert.Equal(t, uint(3), reloaded.RetryAttemptCount)
		assert.True(t, parked[0].CreatedAt.Equal(reloaded.CreatedAt), "createdAt must be preserved")
		assert.WithinDuration(t, earliest, reloaded.EarliestNextAttemptAt, time.Second)
	})
	t.Run("PausedSinceSelection", func(t *testing.T) {
		t.Parallel()
		fixture, _ := setupParked(t, "drain-stillpaused", 2)
		drained, err := parkingRepo.DrainPage(fixture.Push[0].ID.String(), 10, time.Now())
		assert.NoError(t, err)
		assert.Empty(t, drained)
		assert.Equal(t, 2, parkedCountFor(t, fixture.Push[0]))
	})
	t.Run("EmptyAndZeroPageSize", func(t *testing.T) {
		t.Parallel()
		fixture := newPauseFixture(t, "drain-empty", 1, 0)
		drained, err := parkingRepo.DrainPage(fixture.Push[0].ID.String(), 10, time.Now())
		assert.NoError(t, err)
		assert.Empty(t, drained)
		drained, err = parkingRepo.DrainPage(fixture.Push[0].ID.String(), 0, time.Now())
		assert.NoError(t, err)
		assert.Empty(t, drained)
	})
	t.Run("InsertConflictRollsBack", func(t *testing.T) {
		t.Parallel()
		fixture, parked := setupParked(t, "drain-conflict", 1)
		conflictingJobID := xid.New().String()
		_, err := testDB.Exec("INSERT INTO job (id, messageId, consumerId, dispatchReceivedAt, statusChangedAt, earliestNextAttemptAt, status, createdAt, updatedAt, priority, incrementalTimeout) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0, 0)",
			conflictingJobID, parked[0].Message.ID.String(), fixture.Push[0].ID.String(), time.Now(), time.Now(), time.Now(), data.JobQueued, time.Now(), time.Now())
		assert.NoError(t, err)
		// The conflicting row deliberately breaks the parking invariant; remove both rows so later tests that
		// check invariants across the whole shared DB are not affected
		defer testDB.Exec("DELETE FROM job WHERE id = ?", conflictingJobID)
		defer testDB.Exec("DELETE FROM job_parking WHERE id = ?", parked[0].ID.String())
		resumeConsumerForTest(t, fixture.Push[0])
		_, err = parkingRepo.DrainPage(fixture.Push[0].ID.String(), 10, time.Now())
		assert.Error(t, err)
		assert.Equal(t, 1, parkedCountFor(t, fixture.Push[0]), "parked row must survive a rolled back drain")
	})
	t.Run("DeleteShortRollsBack", func(t *testing.T) {
		t.Parallel()
		db, mock, _ := sqlmock.New()
		defer db.Close()
		now := time.Now()
		rows := sqlmock.NewRows(strings.Split(strings.ReplaceAll(parkedJobColumns, " ", ""), ","))
		rows.AddRow(1, xid.New().String(), xid.New().String(), xid.New().String(), now, 0, now, 0, 0, now, now)
		rows.AddRow(2, xid.New().String(), xid.New().String(), xid.New().String(), now, 0, now, 0, 0, now, now)
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT seq").WillReturnRows(rows)
		mock.ExpectExec("DELETE FROM job_parking").WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectRollback()
		drained, err := NewJobParkingRepository(db, nil, nil).DrainPage("c1", 10, now)
		assert.Equal(t, ErrNoRowsUpdated, err)
		assert.Nil(t, drained)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

// TestDrainPage_ConcurrentDrainers_Race drains the same consumer from two goroutines; every parked job must be
// moved exactly once
func TestDrainPage_ConcurrentDrainers_Race(t *testing.T) {
	parkingRepo := getJobParkingRepository()
	fixture := newPauseFixture(t, "drain-concurrent", 1, 0)
	consumer := fixture.Push[0]
	pauseConsumerForTest(t, consumer)
	expected := map[string]bool{}
	for i := 0; i < 40; i++ {
		_, parked := dispatchSplit(t, newParkingMessage(t, fixture), fixture.Consumers, map[string]bool{consumer.ID.String(): true})
		expected[parked[0].ID.String()] = true
	}
	resumeConsumerForTest(t, consumer)
	var mutex sync.Mutex
	seen := map[string]int{}
	var wg sync.WaitGroup
	for worker := 0; worker < 2; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for attempt := 0; attempt < 1000; attempt++ {
				drained, err := parkingRepo.DrainPage(consumer.ID.String(), 5, time.Now())
				if err != nil {
					// e.g. SQLite "database is locked" or the delete-first guard rolling back; retry
					time.Sleep(time.Millisecond)
					continue
				}
				mutex.Lock()
				for _, job := range drained {
					seen[job.ID.String()]++
				}
				mutex.Unlock()
				if len(drained) == 0 {
					remaining := 0
					if testDB.QueryRow("SELECT COUNT(*) FROM job_parking WHERE consumerId = ?", consumer.ID.String()).Scan(&remaining) == nil && remaining == 0 {
						return
					}
				}
			}
		}()
	}
	wg.Wait()
	assert.Equal(t, len(expected), len(seen))
	for id, count := range seen {
		assert.True(t, expected[id], "unexpected job drained "+id)
		assert.Equal(t, 1, count, "job drained more than once "+id)
	}
	assert.NoError(t, CheckParkingInvariants(testDB, consumer.ID.String()))
}

// TestReparkVsMarkJobInflight_Race races re-park against hand-out for every job of a paused consumer; the DB gate
// must never hand a job out and each job must end up in exactly one table
func TestReparkVsMarkJobInflight_Race(t *testing.T) {
	parkingRepo := getJobParkingRepository()
	djRepo := getDeliverJobRepository()
	fixture := newPauseFixture(t, "repark-race", 1, 0)
	consumer := fixture.Push[0]
	messages := make([]*data.Message, 0, 30)
	jobs := make([]*data.DeliveryJob, 0, 30)
	for i := 0; i < 30; i++ {
		msg := newParkingMessage(t, fixture)
		dispatched, _ := dispatchSplit(t, msg, fixture.Consumers, nil)
		messages = append(messages, msg)
		jobs = append(jobs, dispatched[0])
	}
	pauseConsumerForTest(t, consumer)
	var wg sync.WaitGroup
	for _, job := range jobs {
		reparkCopy, inflightCopy := *job, *job
		wg.Add(2)
		go func() {
			defer wg.Done()
			for attempt := 0; attempt < 200; attempt++ {
				if _, err := parkingRepo.ReparkQueuedJob(&reparkCopy); err == nil {
					return
				}
				time.Sleep(time.Millisecond)
			}
		}()
		go func() {
			defer wg.Done()
			assert.Error(t, djRepo.MarkJobInflight(&inflightCopy), "a paused consumer's job must never be handed out")
		}()
	}
	wg.Wait()
	inflight := 0
	assert.NoError(t, testDB.QueryRow("SELECT COUNT(*) FROM job WHERE consumerId = ? AND status = ?", consumer.ID.String(), data.JobInflight).Scan(&inflight))
	assert.Equal(t, 0, inflight)
	for _, msg := range messages {
		assertPlacement(t, msg, consumer, 0, 1)
	}
	assert.NoError(t, CheckParkingInvariants(testDB))
}

func TestMarkJobInflight_PauseGate(t *testing.T) {
	djRepo := getDeliverJobRepository()
	fixture := newPauseFixture(t, "gate-inflight", 1, 0)
	msg := newParkingMessage(t, fixture)
	jobs, _ := dispatchSplit(t, msg, fixture.Consumers, nil)
	job := jobs[0]
	pauseConsumerForTest(t, fixture.Push[0])
	assert.Equal(t, ErrNoRowsUpdated, djRepo.MarkJobInflight(job))
	reloaded, err := djRepo.GetByID(job.ID.String())
	assert.NoError(t, err)
	assert.Equal(t, data.JobQueued, reloaded.Status)

	t.Run("GlobalPause", func(t *testing.T) {
		resumeConsumerForTest(t, fixture.Push[0])
		defer clearGlobalPause(t)
		assert.NoError(t, InsertPauseRow(testDB, data.GlobalDispatchPauseScope, ""))
		assert.Equal(t, ErrNoRowsUpdated, djRepo.MarkJobInflight(job))
	})

	assert.NoError(t, djRepo.MarkJobInflight(job), "hand-out must work again after resume")
	assert.Equal(t, data.JobInflight, job.Status)
}

func TestMarkDeadJobAsInflight_PauseGate(t *testing.T) {
	djRepo := getDeliverJobRepository()
	fixture := newPauseFixture(t, "gate-dead", 0, 1)
	msg := newParkingMessage(t, fixture)
	jobs, _ := dispatchSplit(t, msg, fixture.Consumers, nil)
	job := jobs[0]
	assert.NoError(t, djRepo.MarkJobInflight(job))
	assert.NoError(t, djRepo.MarkJobDead(job))
	pauseConsumerForTest(t, fixture.Pull[0])
	assert.Equal(t, ErrNoRowsUpdated, djRepo.MarkDeadJobAsInflight(job))
	resumeConsumerForTest(t, fixture.Pull[0])
	assert.NoError(t, djRepo.MarkDeadJobAsInflight(job))
	assert.Equal(t, data.JobInflight, job.Status)
}

func TestGetByID_ParkedFallback(t *testing.T) {
	djRepo := getDeliverJobRepository()
	fixture := newPauseFixture(t, "getbyid-parked", 1, 0)
	msg := newParkingMessage(t, fixture)
	_, parked := dispatchSplit(t, msg, fixture.Consumers, map[string]bool{fixture.Push[0].ID.String(): true})
	job, err := djRepo.GetByID(parked[0].ID.String())
	assert.NoError(t, err)
	assert.Equal(t, data.JobParked, job.Status)
	assert.Equal(t, msg.ID, job.Message.ID)
	assert.Equal(t, samplePayload, job.Message.Payload)
	assert.Equal(t, fixture.Push[0].ConsumerID, job.Listener.ConsumerID)
	assert.False(t, job.StatusChangedAt.IsZero())
	_, err = djRepo.GetByID(xid.New().String())
	assert.Equal(t, sql.ErrNoRows, err)
}

func TestGetQueuedJobsByIDs(t *testing.T) {
	djRepo := getDeliverJobRepository()
	fixture := newPauseFixture(t, "queued-by-ids", 10, 0)
	ids := make([]string, 0, 452)
	allJobs := make([]*data.DeliveryJob, 0, 450)
	for i := 0; i < 45; i++ {
		jobs, _ := dispatchSplit(t, newParkingMessage(t, fixture), fixture.Consumers, nil)
		for _, job := range jobs {
			ids = append(ids, job.ID.String())
			allJobs = append(allJobs, job)
		}
	}
	for _, job := range allJobs[:5] {
		assert.NoError(t, djRepo.MarkJobInflight(job))
	}
	ids = append(ids, xid.New().String(), xid.New().String())
	jobs, err := djRepo.GetQueuedJobsByIDs(ids)
	assert.NoError(t, err)
	assert.Equal(t, 445, len(jobs))
	for _, job := range jobs {
		assert.Equal(t, data.JobQueued, job.Status)
		assert.Equal(t, samplePayload, job.Message.Payload)
		assert.NotEmpty(t, job.Listener.ConsumerID)
	}
	empty, err := djRepo.GetQueuedJobsByIDs(nil)
	assert.NoError(t, err)
	assert.Empty(t, empty)
}

func TestJobParkingGetForMessageAndCounts(t *testing.T) {
	parkingRepo := getJobParkingRepository()
	fixture := newPauseFixture(t, "parking-counts", 3, 0)
	parked := map[string]bool{fixture.Push[0].ID.String(): true, fixture.Push[1].ID.String(): true}
	var lastMsg *data.Message
	for i := 0; i < 3; i++ {
		lastMsg = newParkingMessage(t, fixture)
		dispatchSplit(t, lastMsg, fixture.Consumers, parked)
	}
	forMessage, err := parkingRepo.GetForMessage(lastMsg)
	assert.NoError(t, err)
	assert.Equal(t, 2, len(forMessage))
	for _, job := range forMessage {
		assert.Equal(t, data.JobParked, job.Status)
		assert.Equal(t, lastMsg, job.Message)
		assert.NotEmpty(t, job.Listener.ConsumerID)
	}
	single, err := parkingRepo.CountForConsumer(fixture.Push[0].ID.String())
	assert.NoError(t, err)
	assert.Equal(t, int64(3), single)
	none, err := parkingRepo.CountForConsumer(fixture.Push[2].ID.String())
	assert.NoError(t, err)
	assert.Equal(t, int64(0), none)
	counts, err := parkingRepo.GetCountsByConsumer()
	assert.NoError(t, err)
	found := 0
	for _, count := range counts {
		if parked[count.ConsumerID] {
			found++
			assert.Equal(t, int64(3), count.Count)
			assert.Equal(t, fixture.Channel.ChannelID, count.ChannelID)
			assert.NotEmpty(t, count.OldestParkedAt)
			assert.NotEmpty(t, count.NewestParkedAt)
		}
		assert.NotEqual(t, fixture.Push[2].ID.String(), count.ConsumerID)
	}
	assert.Equal(t, 2, found)

	statusCounts, err := getDeliverJobRepository().GetJobStatusCountsGroupedByConsumer()
	assert.NoError(t, err)
	consumerCounts := statusCounts[Channel_ID(fixture.Channel.ChannelID)][Consumer_ID(fixture.Push[0].ID.String())]
	parkedFound := false
	for _, statusCount := range consumerCounts {
		if statusCount.Status == data.JobParked {
			parkedFound = true
			assert.Equal(t, 3, statusCount.Count)
		}
	}
	assert.True(t, parkedFound, "job status counts must include PARKED")
}

func explainPlan(t *testing.T, query string, args ...interface{}) string {
	t.Helper()
	rows, err := testDB.Query("EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan strings.Builder
	for rows.Next() {
		var id, parent, notused int
		var detail string
		assert.NoError(t, rows.Scan(&id, &parent, &notused, &detail))
		plan.WriteString(detail)
		plan.WriteString("\n")
	}
	assert.NoError(t, rows.Err())
	return plan.String()
}

// TestJobParkingQueryPlans guards the indexes the parking queries depend on (SQLite reports filesorts as
// "USE TEMP B-TREE FOR ORDER BY")
func TestJobParkingQueryPlans(t *testing.T) {
	t.Run("DrainPage", func(t *testing.T) {
		plan := explainPlan(t, drainPageQuery, "consumer", 50)
		assert.Contains(t, plan, "job_parking_drain", "plan was:\n"+plan)
		assert.NotContains(t, plan, "USE TEMP B-TREE FOR ORDER BY", "plan was:\n"+plan)
	})
	t.Run("DrainableConsumers", func(t *testing.T) {
		plan := explainPlan(t, drainableConsumersFullQuery)
		assert.Contains(t, plan, "job_parking_drain", "plan was:\n"+plan)
	})
	t.Run("PruneNotExists", func(t *testing.T) {
		plan := explainPlan(t, pruneMessageQuery, data.MsgStatusDispatched, time.Now(), data.JobDelivered)
		assert.Contains(t, plan, "job_parking_message", "plan was:\n"+plan)
	})
}

// TestParkingInvariants_Randomized is a model-based test: a seeded random walk over pause/resume, dispatch,
// re-park, drain and hand-out operations, checking the parking invariants after every step.
// Replay a failure with PARKING_INVARIANT_SEED=<seed>.
func TestParkingInvariants_Randomized(t *testing.T) {
	seed := time.Now().UnixNano()
	if fromEnv, err := strconv.ParseInt(os.Getenv("PARKING_INVARIANT_SEED"), 10, 64); err == nil {
		seed = fromEnv
	}
	t.Logf("seed=%d", seed)
	random := rand.New(rand.NewSource(seed))
	steps := 500
	if testing.Short() {
		steps = 50
	}
	defer clearGlobalPause(t)
	pauseRepo := NewDispatchPauseRepository(testDB)
	parkingRepo := getJobParkingRepository()
	djRepo := getDeliverJobRepository()
	fixture := newPauseFixture(t, "invariants", 3, 1)
	consumerIDs := make([]interface{}, 0, len(fixture.Consumers))
	for _, consumer := range fixture.Consumers {
		consumerIDs = append(consumerIDs, consumer.ID.String())
	}
	inFixture := " consumerId IN (" + placeholders(len(consumerIDs)) + ")"
	expectedRetries := map[string]uint{}
	messages := make([]*data.Message, 0)

	pickJobID := func(table, extra string, args ...interface{}) string {
		ids := make([]string, 0)
		queryArgs := append(append([]interface{}{}, consumerIDs...), args...)
		rows, err := testDB.Query("SELECT id FROM "+table+" WHERE"+inFixture+extra, queryArgs...)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var id string
			if rows.Scan(&id) == nil {
				ids = append(ids, id)
			}
		}
		rows.Close()
		if len(ids) == 0 {
			return ""
		}
		return ids[random.Intn(len(ids))]
	}
	effectivelyPaused := func(consumerID string) bool {
		paused, err := pauseRepo.IsEffectivelyPaused(consumerID)
		if err != nil {
			t.Fatal(err)
		}
		return paused
	}
	storedRetries := func(jobID string) uint {
		var retries uint
		if err := testDB.QueryRow("SELECT retryAttemptCount FROM job WHERE id = ? UNION ALL SELECT retryAttemptCount FROM job_parking WHERE id = ?", jobID, jobID).Scan(&retries); err != nil {
			t.Fatal(err)
		}
		return retries
	}

	for step := 0; step < steps; step++ {
		switch op := random.Intn(7); op {
		case 0: // toggle a consumer pause
			consumer := fixture.Consumers[random.Intn(len(fixture.Consumers))]
			if _, err := pauseRepo.Get(consumer.ID.String()); err == nil {
				resumeConsumerForTest(t, consumer)
			} else {
				pauseConsumerForTest(t, consumer)
			}
		case 1: // toggle the global pause (less often than consumer pauses)
			if random.Intn(3) == 0 {
				if _, err := pauseRepo.Get(data.GlobalDispatchPauseScope); err == nil {
					clearGlobalPause(t)
				} else {
					assert.NoError(t, InsertPauseRow(testDB, data.GlobalDispatchPauseScope, ""))
				}
			}
		case 2: // dispatch a message, parking jobs of effectively paused consumers
			msg := newParkingMessage(t, fixture)
			parked := map[string]bool{}
			for _, consumer := range fixture.Consumers {
				parked[consumer.ID.String()] = effectivelyPaused(consumer.ID.String())
			}
			jobs, parkedJobs := dispatchSplit(t, msg, fixture.Consumers, parked)
			for _, job := range append(jobs, parkedJobs...) {
				expectedRetries[job.ID.String()] = 0
			}
			messages = append(messages, msg)
		case 3: // re-park a random QUEUED job
			if id := pickJobID("job", " AND status = ?", data.JobQueued); id != "" {
				job, err := djRepo.GetByID(id)
				assert.NoError(t, err)
				wasPaused := effectivelyPaused(job.Listener.ID.String())
				moved, err := parkingRepo.ReparkQueuedJob(job)
				assert.NoError(t, err)
				assert.Equal(t, wasPaused, moved, "re-park must move a QUEUED job iff its consumer is effectively paused")
			}
		case 4: // drain a page for a random drainable fixture consumer
			consumer := fixture.Consumers[random.Intn(len(fixture.Consumers))]
			drained, err := parkingRepo.DrainPage(consumer.ID.String(), 3, time.Now())
			assert.NoError(t, err)
			if effectivelyPaused(consumer.ID.String()) {
				assert.Empty(t, drained, "a paused consumer must never be drained")
			}
		case 5: // hand out a random QUEUED job
			if id := pickJobID("job", " AND status = ?", data.JobQueued); id != "" {
				job, err := djRepo.GetByID(id)
				assert.NoError(t, err)
				wasPaused := effectivelyPaused(job.Listener.ID.String())
				err = djRepo.MarkJobInflight(job)
				if wasPaused {
					assert.Error(t, err, "hand-out to an effectively paused consumer must be refused")
				} else {
					assert.NoError(t, err)
				}
			}
		case 6: // finish a random INFLIGHT job: delivered or retried
			if id := pickJobID("job", " AND status = ?", data.JobInflight); id != "" {
				job, err := djRepo.GetByID(id)
				assert.NoError(t, err)
				if random.Intn(2) == 0 {
					assert.NoError(t, djRepo.MarkJobDelivered(job))
				} else {
					assert.NoError(t, djRepo.MarkJobRetry(job, -1*time.Second))
					expectedRetries[id]++
				}
			}
		}
		if err := CheckParkingInvariants(testDB); err != nil {
			t.Fatalf("step %d: %v (seed=%d)", step, err, seed)
		}
		if len(messages) > 0 {
			msg := messages[random.Intn(len(messages))]
			if err := CheckMessageJobCount(testDB, msg.ID.String(), len(fixture.Consumers)); err != nil {
				t.Fatalf("step %d: %v (seed=%d)", step, err, seed)
			}
		}
		if len(expectedRetries) > 0 {
			for id, expected := range expectedRetries {
				assert.Equal(t, expected, storedRetries(id), "park/drain must never change retryAttemptCount (seed=%d)", seed)
				break // one random-ish job per step; all are checked at the end
			}
		}
	}
	for id, expected := range expectedRetries {
		assert.Equal(t, expected, storedRetries(id), "park/drain must never change retryAttemptCount (seed=%d)", seed)
	}
	for _, msg := range messages {
		assert.NoError(t, CheckMessageJobCount(testDB, msg.ID.String(), len(fixture.Consumers)))
	}
}


// Generated with assistance from Claude AI

func failingDB(t *testing.T) *sql.DB {
	t.Helper()
	db, mock, _ := sqlmock.New()
	mock.MatchExpectationsInOrder(false)
	for i := 0; i < 10; i++ {
		mock.ExpectQuery(".*").WillReturnError(errors.New("db down"))
	}
	return db
}

// Lookups of a parked job's message or consumer can fail independently of the parking row itself
func TestJobParkingLookupFailures(t *testing.T) {
	fixture := newPauseFixture(t, "lookup-failures", 1, 0)
	consumer := fixture.Push[0]
	pauseConsumerForTest(t, consumer)
	msg := newParkingMessage(t, fixture)
	_, parked := dispatchSplit(t, msg, fixture.Consumers, map[string]bool{consumer.ID.String(): true})
	badDB := failingDB(t)
	defer badDB.Close()
	failingMsgRepo := NewMessageRepository(badDB, NewChannelRepository(testDB), NewProducerRepository(testDB))
	failingConsumerRepo := NewConsumerRepository(badDB, NewChannelRepository(testDB))

	_, err := NewJobParkingRepository(testDB, failingMsgRepo, getConsumerRepo()).GetByID(parked[0].ID.String())
	assert.Error(t, err, "message lookup failure")
	_, err = NewJobParkingRepository(testDB, getMessageRepository(), failingConsumerRepo).GetByID(parked[0].ID.String())
	assert.Error(t, err, "consumer lookup failure")
	_, err = NewJobParkingRepository(testDB, getMessageRepository(), failingConsumerRepo).GetForMessage(msg)
	assert.Error(t, err, "listener lookup failure while listing a message's parked jobs")

	resumeConsumerForTest(t, consumer)
	// The page is committed before messages are loaded; a load failure is reported but the jobs are already QUEUED
	// (the retry worker delivers them after the hand-off lease)
	_, err = NewJobParkingRepository(testDB, failingMsgRepo, getConsumerRepo()).DrainPage(consumer.ID.String(), 10, time.Now())
	assert.Error(t, err)
	assertPlacement(t, msg, consumer, 1, 0)
	reloaded, err := getDeliverJobRepository().GetByID(parked[0].ID.String())
	assert.NoError(t, err)
	assert.Equal(t, data.JobQueued, reloaded.Status)
}

func TestDrainPageListenerLookupFailure(t *testing.T) {
	fixture := newPauseFixture(t, "drain-listener-failure", 1, 0)
	consumer := fixture.Push[0]
	pauseConsumerForTest(t, consumer)
	dispatchSplit(t, newParkingMessage(t, fixture), fixture.Consumers, map[string]bool{consumer.ID.String(): true})
	resumeConsumerForTest(t, consumer)
	badDB := failingDB(t)
	defer badDB.Close()
	_, err := NewJobParkingRepository(testDB, getMessageRepository(), NewConsumerRepository(badDB, NewChannelRepository(testDB))).DrainPage(consumer.ID.String(), 10, time.Now())
	assert.Error(t, err)
}

func TestInsertParkedJobsInTxEmpty(t *testing.T) {
	assert.NoError(t, transactionalWrites(testDB, func(tx *sql.Tx) error { return insertParkedJobsInTx(tx, nil, time.Now()) }))
}

func TestGetQueuedJobsByIDsError(t *testing.T) {
	badDB := failingDB(t)
	defer badDB.Close()
	_, err := NewDeliveryJobRepository(badDB, getMessageRepository(), getConsumerRepo()).GetQueuedJobsByIDs([]string{"a"})
	assert.Error(t, err)
}

func TestPauseTestHelpers(t *testing.T) {
	badDB := failingDB(t)
	defer badDB.Close()
	_, _, err := JobPlacement(badDB, "m", "c")
	assert.Error(t, err)
	assert.Error(t, CheckParkingInvariants(badDB))
	assert.Error(t, CheckMessageJobCount(badDB, "m", 1))
	assert.Error(t, InsertPauseRow(badDB, "s", ""))

	fixture := newPauseFixture(t, "helpers", 2, 0)
	never, parkedConsumer := fixture.Push[0], fixture.Push[1]
	msg := newParkingMessage(t, fixture)
	dispatchSplit(t, msg, fixture.Consumers, map[string]bool{parkedConsumer.ID.String(): true})
	assert.NoError(t, CheckParkingInvariants(testDB, never.ID.String()))
	assert.Error(t, CheckParkingInvariants(testDB, parkedConsumer.ID.String()), "a consumer declared never-paused must not have parked jobs")
	assert.NoError(t, CheckMessageJobCount(testDB, msg.ID.String(), 2))
	assert.Error(t, CheckMessageJobCount(testDB, msg.ID.String(), 3))

	relative, _ := url.Parse("relative/callback")
	_, err = SetupPauseFixture(getPauseTestAccessor(), "helpers-invalid-"+xid.New().String()+"-", 1, 0, func(int) *url.URL { return relative })
	assert.Error(t, err, "an invalid consumer callback fails the fixture")
}
