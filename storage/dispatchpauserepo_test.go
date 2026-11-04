package storage

import (
	"database/sql"
	"errors"
	"net/url"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/rs/xid"
	"github.com/stretchr/testify/assert"

	"github.com/newscred/webhook-broker/storage/data"
)

// NOTE: tests that create the GLOBAL pause row must not run in parallel with other tests in this package
// because the global pause affects every job hand-out (MarkJobInflight) in the shared test DB. They are
// written as sequential top-level tests/subtests and always remove the global row before returning.

func getPauseTestAccessor() DataAccessor {
	channelRepo := NewChannelRepository(testDB)
	producerRepo := NewProducerRepository(testDB)
	consumerRepo := NewConsumerRepository(testDB, channelRepo)
	msgRepo := NewMessageRepository(testDB, channelRepo, producerRepo)
	return &RelationalDBDataAccessor{
		db:                      testDB,
		appRepository:           NewAppRepository(testDB),
		channelRepository:       channelRepo,
		producerRepository:      producerRepo,
		consumerRepository:      consumerRepo,
		messageRepository:       msgRepo,
		deliveryJobRepository:   NewDeliveryJobRepository(testDB, msgRepo, consumerRepo),
		lockRepository:          NewLockRepository(testDB),
		dispatchPauseRepository: NewDispatchPauseRepository(testDB),
		jobParkingRepository:    NewJobParkingRepository(testDB, msgRepo, consumerRepo),
	}
}

func newPauseFixture(t *testing.T, prefix string, push, pull int) *PauseFixture {
	t.Helper()
	callback, _ := url.Parse("https://imytech.net/")
	fixture, err := SetupPauseFixture(getPauseTestAccessor(), prefix+"-"+xid.New().String()+"-", push, pull, func(int) *url.URL { return callback })
	if err != nil {
		t.Fatal(err)
	}
	return fixture
}

func clearGlobalPause(t *testing.T) {
	t.Helper()
	assert.NoError(t, DeletePauseRow(testDB, data.GlobalDispatchPauseScope))
}

func TestDispatchPausePause(t *testing.T) {
	repo := NewDispatchPauseRepository(testDB)
	t.Run("Consumer", func(t *testing.T) {
		t.Parallel()
		fixture := newPauseFixture(t, "pause-consumer", 1, 0)
		pause, _ := data.NewConsumerDispatchPause(fixture.Push[0], "migration")
		stored, err := repo.Pause(pause)
		assert.NoError(t, err)
		assert.Equal(t, fixture.Push[0].ID.String(), stored.Scope)
		loaded, err := repo.Get(stored.Scope)
		assert.NoError(t, err)
		assert.Equal(t, fixture.Push[0].ID.String(), loaded.ConsumerID)
		assert.Equal(t, "migration", loaded.Reason)
		assert.False(t, loaded.IsGlobal())
	})
	t.Run("Idempotent", func(t *testing.T) {
		t.Parallel()
		fixture := newPauseFixture(t, "pause-idempotent", 1, 0)
		first, _ := data.NewConsumerDispatchPause(fixture.Push[0], "first")
		first.PausedAt = time.Now().Add(-1 * time.Hour).Truncate(time.Second)
		_, err := repo.Pause(first)
		assert.NoError(t, err)
		second, _ := data.NewConsumerDispatchPause(fixture.Push[0], "second")
		stored, err := repo.Pause(second)
		assert.NoError(t, err)
		assert.Equal(t, "first", stored.Reason)
		assert.True(t, stored.PausedAt.Equal(first.PausedAt), "original pausedAt must be kept")
	})
	t.Run("CascadeOnConsumerDelete", func(t *testing.T) {
		t.Parallel()
		fixture := newPauseFixture(t, "pause-cascade", 1, 0)
		pause, _ := data.NewConsumerDispatchPause(fixture.Push[0], "")
		_, err := repo.Pause(pause)
		assert.NoError(t, err)
		assert.NoError(t, getConsumerRepo().Delete(fixture.Push[0]))
		_, err = repo.Get(pause.Scope)
		assert.Equal(t, sql.ErrNoRows, err)
	})
	t.Run("UnknownConsumer", func(t *testing.T) {
		t.Parallel()
		_, err := repo.Pause(&data.DispatchPause{Scope: "unknown-consumer", ConsumerID: "unknown-consumer"})
		assert.Error(t, err)
	})
	t.Run("InvalidState", func(t *testing.T) {
		t.Parallel()
		_, err := repo.Pause(nil)
		assert.Equal(t, ErrInvalidStateToSave, err)
		_, err = repo.Pause(&data.DispatchPause{Scope: "c1", ConsumerID: "c2"})
		assert.Equal(t, ErrInvalidStateToSave, err)
	})
}

func TestDispatchPauseGlobal(t *testing.T) {
	repo := NewDispatchPauseRepository(testDB)
	defer clearGlobalPause(t)
	stored, err := repo.Pause(data.NewGlobalDispatchPause("incident"))
	assert.NoError(t, err)
	assert.True(t, stored.IsGlobal())
	loaded, err := repo.Get(data.GlobalDispatchPauseScope)
	assert.NoError(t, err)
	assert.Empty(t, loaded.ConsumerID)
	assert.Equal(t, "incident", loaded.Reason)
	all, err := repo.GetAll()
	assert.NoError(t, err)
	found := false
	for _, pause := range all {
		if pause.IsGlobal() {
			found = true
			assert.Empty(t, pause.ConsumerID)
		}
	}
	assert.True(t, found)
	resumed, err := repo.Resume(data.GlobalDispatchPauseScope)
	assert.NoError(t, err)
	assert.True(t, resumed)
}

func TestDispatchPauseResume(t *testing.T) {
	repo := NewDispatchPauseRepository(testDB)
	t.Run("Existing", func(t *testing.T) {
		t.Parallel()
		fixture := newPauseFixture(t, "resume-existing", 1, 0)
		pause, _ := data.NewConsumerDispatchPause(fixture.Push[0], "")
		_, err := repo.Pause(pause)
		assert.NoError(t, err)
		resumed, err := repo.Resume(pause.Scope)
		assert.NoError(t, err)
		assert.True(t, resumed)
		_, err = repo.Get(pause.Scope)
		assert.Equal(t, sql.ErrNoRows, err)
	})
	t.Run("Missing", func(t *testing.T) {
		t.Parallel()
		resumed, err := repo.Resume("not-paused-scope")
		assert.NoError(t, err)
		assert.False(t, resumed)
	})
}

func TestDispatchPauseIsEffectivelyPaused(t *testing.T) {
	repo := NewDispatchPauseRepository(testDB)
	fixture := newPauseFixture(t, "effective", 2, 0)
	target, other := fixture.Push[0], fixture.Push[1]
	check := func(expectTarget, expectOther bool) {
		t.Helper()
		paused, err := repo.IsEffectivelyPaused(target.ID.String())
		assert.NoError(t, err)
		assert.Equal(t, expectTarget, paused)
		paused, err = repo.IsEffectivelyPaused(other.ID.String())
		assert.NoError(t, err)
		assert.Equal(t, expectOther, paused)
	}
	defer clearGlobalPause(t)
	check(false, false)
	pause, _ := data.NewConsumerDispatchPause(target, "")
	_, err := repo.Pause(pause)
	assert.NoError(t, err)
	check(true, false)
	_, err = repo.Pause(data.NewGlobalDispatchPause(""))
	assert.NoError(t, err)
	check(true, true)
	_, err = repo.Resume(pause.Scope)
	assert.NoError(t, err)
	check(true, true)
	_, err = repo.Resume(data.GlobalDispatchPauseScope)
	assert.NoError(t, err)
	check(false, false)
}

func TestDispatchPauseDBErrors(t *testing.T) {
	expectedErr := errors.New("db failure")
	t.Run("PauseInsertError", func(t *testing.T) {
		db, mock, _ := sqlmock.New()
		defer db.Close()
		mock.ExpectBegin()
		mock.ExpectExec("INSERT INTO dispatch_pause").WillReturnError(expectedErr)
		mock.ExpectRollback()
		_, err := NewDispatchPauseRepository(db).Pause(data.NewGlobalDispatchPause(""))
		assert.Equal(t, expectedErr, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
	t.Run("ResumeError", func(t *testing.T) {
		db, mock, _ := sqlmock.New()
		defer db.Close()
		mock.ExpectBegin()
		mock.ExpectExec("DELETE FROM dispatch_pause").WillReturnError(expectedErr)
		mock.ExpectRollback()
		resumed, err := NewDispatchPauseRepository(db).Resume("x")
		assert.Equal(t, expectedErr, err)
		assert.False(t, resumed)
	})
	t.Run("GetAllError", func(t *testing.T) {
		db, mock, _ := sqlmock.New()
		defer db.Close()
		mock.ExpectQuery("SELECT scope").WillReturnError(expectedErr)
		_, err := NewDispatchPauseRepository(db).GetAll()
		assert.Equal(t, expectedErr, err)
	})
	t.Run("IsEffectivelyPausedError", func(t *testing.T) {
		db, mock, _ := sqlmock.New()
		defer db.Close()
		mock.ExpectQuery("SELECT COUNT").WillReturnError(expectedErr)
		_, err := NewDispatchPauseRepository(db).IsEffectivelyPaused("x")
		assert.Equal(t, expectedErr, err)
	})
	t.Run("NilDB", func(t *testing.T) {
		defer dbPanicDeferAssert(t)
		NewDispatchPauseRepository(nil)
	})
}

// Generated with assistance from Claude AI
