package storage

import (
	"database/sql"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/imyousuf/webhook-broker/config"
	"github.com/imyousuf/webhook-broker/storage/data"
	"github.com/stretchr/testify/assert"
)

var testDB *sql.DB

const (
	successfulGetTestProducerID      = "get-test"
	successfulGetTestToken           = "sometokenforget"
	nonExistingGetTestProducerID     = "get-test-ne"
	successfulInsertTestProducerID   = "s-insert-test"
	invalidStateUpdateTestProducerID = "i-update-test"
	successfulUpdateTestProducerID   = "s-update-test"
	dbErrUpdateTestProducerID        = "db-update-test"
	noChangeUpdateTestProducerID     = "nc-update-test"
	listTestProducerIDPrefix         = "get-list-"
)

func TestMain(m *testing.M) {
	// Setup DB and migration
	os.Remove("./webhook-broker.sqlite3")
	configuration, _ := config.GetAutoConfiguration()
	var dbErr error
	testDB, dbErr = GetConnectionPool(configuration, defaultMigrationConf, configuration)
	if dbErr == nil {
		m.Run()
	}
	testDB.Close()
}

func getProducerRepo() ProducerRepository {
	producerRepo := NewProducerRepository(testDB)
	return producerRepo
}

func TestProducerGet(t *testing.T) {
	t.Run("GetExisting", func(t *testing.T) {
		t.Parallel()
		repo := getProducerRepo()
		sampleProducer, err := data.NewProducer(successfulGetTestProducerID, successfulGetTestToken)
		assert.Nil(t, err)
		assert.True(t, sampleProducer.IsInValidState())
		resultProducer, err := repo.Store(sampleProducer)
		assert.Nil(t, err)
		assert.False(t, resultProducer.ID.IsNil())
		producer, err := repo.Get(successfulGetTestProducerID)
		assert.Nil(t, err)
		assert.False(t, producer.ID.IsNil())
		assert.Equal(t, sampleProducer.ID, producer.ID)
		assert.Equal(t, successfulGetTestProducerID, producer.ProducerID)
		assert.Equal(t, successfulGetTestProducerID, producer.Name)
		assert.Equal(t, successfulGetTestToken, producer.Token)
		assert.False(t, producer.CreatedAt.IsZero())
		assert.False(t, producer.UpdatedAt.IsZero())
		assert.True(t, producer.CreatedAt.Before(time.Now()))
		assert.True(t, producer.UpdatedAt.Before(time.Now()))
	})
	t.Run("GetMissing", func(t *testing.T) {
		t.Parallel()
		repo := getProducerRepo()
		_, err := repo.Get(nonExistingGetTestProducerID)
		assert.NotNil(t, err)
	})
}

func TestProducerStore(t *testing.T) {
	t.Run("Create:InvalidState", func(t *testing.T) {
		t.Parallel()
		producer, err := data.NewProducer(successfulInsertTestProducerID, successfulGetTestToken)
		assert.Nil(t, err)
		assert.True(t, producer.IsInValidState())
		producer.Token = ""
		assert.False(t, producer.IsInValidState())
		repo := getProducerRepo()
		_, err = repo.Store(producer)
		assert.Equal(t, ErrInvalidStateToSave, err)
	})
	t.Run("Create:InsertionFailed", func(t *testing.T) {
		t.Parallel()
		db, mock, _ := sqlmock.New()
		expectedErr := errors.New("Insertion failed")
		mock.ExpectBegin()
		mock.ExpectExec("INSERT INTO").WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).WillReturnError(expectedErr)
		mock.ExpectRollback()
		mock.MatchExpectationsInOrder(true)
		repo := &ProducerDBRepository{db: db}
		producer, _ := data.NewProducer(successfulInsertTestProducerID, successfulGetTestToken)
		_, err := repo.Store(producer)
		assert.Equal(t, expectedErr, err)
		assert.Nil(t, mock.ExpectationsWereMet())
	})
	t.Run("Create:Success", func(t *testing.T) {
		t.Parallel()
		producer, _ := data.NewProducer(successfulInsertTestProducerID, successfulGetTestToken)
		repo := getProducerRepo()
		_, err := repo.Store(producer)
		assert.Nil(t, err)
		newProducer, err := repo.Get(successfulInsertTestProducerID)
		assert.Nil(t, err)
		assert.True(t, newProducer.IsInValidState())
		assert.Equal(t, producer.ID, newProducer.ID)
		assert.Equal(t, producer.Name, newProducer.Name)
		assert.Equal(t, producer.ProducerID, newProducer.ProducerID)
		assert.Equal(t, producer.Token, newProducer.Token)
	})
	t.Run("Update:NothingToChange", func(t *testing.T) {
		t.Parallel()
		producer, _ := data.NewProducer(noChangeUpdateTestProducerID, successfulGetTestToken)
		repo := getProducerRepo()
		_, err := repo.Store(producer)
		assert.Nil(t, err)
		failedUpdate, err := repo.Store(producer)
		assert.Nil(t, err)
		assert.True(t, producer.CreatedAt.Equal(failedUpdate.CreatedAt))
		assert.True(t, producer.UpdatedAt.Equal(failedUpdate.UpdatedAt))
	})
	t.Run("Update:InvalidState", func(t *testing.T) {
		t.Parallel()
		producer, _ := data.NewProducer(invalidStateUpdateTestProducerID, successfulGetTestToken)
		repo := getProducerRepo()
		_, err := repo.Store(producer)
		assert.Nil(t, err)
		producer.Token = ""
		_, err = repo.Store(producer)
		assert.NotNil(t, err)
		assert.Equal(t, ErrInvalidStateToSave, err)
	})
	t.Run("Update:UpdateFailed", func(t *testing.T) {
		t.Parallel()
		db, mock, _ := sqlmock.New()
		expectedErr := errors.New("Update failed")
		producer, _ := data.NewProducer(dbErrUpdateTestProducerID, successfulGetTestToken)
		producer.QuickFix()
		rows := sqlmock.NewRows([]string{"ID", "producerID", "name", "token", "createdAt", "updatedAt"}).AddRow(producer.ID, producer.ProducerID, producer.Name, producer.Token, producer.CreatedAt, producer.UpdatedAt)
		mock.ExpectQuery("SELECT ID, producerID, name, token, createdAt, updatedAt FROM producer WHERE producerID like").WithArgs(dbErrUpdateTestProducerID).WillReturnRows(rows).WillReturnError(nil)
		mock.ExpectBegin()
		mock.ExpectExec("UPDATE producer").WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), dbErrUpdateTestProducerID).WillReturnError(expectedErr)
		mock.ExpectRollback()
		mock.MatchExpectationsInOrder(true)
		repo := &ProducerDBRepository{db: db}
		producer.Token = "c"
		_, err := repo.Store(producer)
		assert.Equal(t, expectedErr, err)
		assert.Nil(t, mock.ExpectationsWereMet())
	})
	t.Run("Update:NoRowChanged", func(t *testing.T) {
		t.Parallel()
		// uses mock
		db, mock, _ := sqlmock.New()
		producer, _ := data.NewProducer(dbErrUpdateTestProducerID, successfulGetTestToken)
		producer.QuickFix()
		rows := sqlmock.NewRows([]string{"ID", "producerID", "name", "token", "createdAt", "updatedAt"}).AddRow(producer.ID, producer.ProducerID, producer.Name, producer.Token, producer.CreatedAt, producer.UpdatedAt)
		result := sqlmock.NewResult(1, 0)
		mock.ExpectQuery("SELECT ID, producerID, name, token, createdAt, updatedAt FROM producer WHERE producerID like").WithArgs(dbErrUpdateTestProducerID).WillReturnRows(rows).WillReturnError(nil)
		mock.ExpectBegin()
		mock.ExpectExec("UPDATE producer").WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), dbErrUpdateTestProducerID).WillReturnResult(result).WillReturnError(nil)
		mock.ExpectCommit()
		mock.MatchExpectationsInOrder(true)
		repo := &ProducerDBRepository{db: db}
		producer.Token = "c"
		_, err := repo.Store(producer)
		assert.Equal(t, ErrNoRowsUpdated, err)
		assert.Nil(t, mock.ExpectationsWereMet())
	})
	t.Run("Update:Success", func(t *testing.T) {
		t.Parallel()
		producer, _ := data.NewProducer(successfulUpdateTestProducerID, "oldtoken")
		repo := getProducerRepo()
		repo.Store(producer)
		producer.Token = successfulGetTestToken
		updatedProducer, err := repo.Store(producer)
		assert.Nil(t, err)
		assert.Equal(t, successfulGetTestToken, updatedProducer.Token)
		assert.True(t, producer.UpdatedAt.Before(updatedProducer.UpdatedAt))
	})
}

func TestNewProducerRepository(t *testing.T) {
	defer dbPanicDeferAssert(t)
	NewProducerRepository(nil)
}

func TestProducerGetList(t *testing.T) {
	repo := getProducerRepo()
	for index := 99; index > -1; index = index - 1 {
		indexString := strconv.Itoa(index)
		producer, err := data.NewProducer(listTestProducerIDPrefix+indexString, successfulGetTestToken+" - "+indexString)
		assert.Nil(t, err)
		_, err = repo.Store(producer)
		assert.Nil(t, err)
	}
	t.Run("PaginationDeadlock", func(t *testing.T) {
		t.Parallel()
		producer1, _ := data.NewProducer(dbErrUpdateTestProducerID, successfulGetTestToken)
		producer2, _ := data.NewProducer(successfulGetTestProducerID, successfulGetTestToken)
		_, _, err := repo.GetList(data.NewPagination(producer1, producer2))
		assert.Equal(t, ErrPaginationDeadlock, err)
		_, _, err = repo.GetList(nil)
		assert.Equal(t, ErrPaginationDeadlock, err)
	})
	t.Run("QueryError", func(t *testing.T) {
		t.Parallel()
		db, mock, _ := sqlmock.New()
		expectedErr := errors.New("DB Query Error")
		mock.ExpectQuery("SELECT ID, producerID, name, token, createdAt, updatedAt FROM producer").WillReturnError(expectedErr)
		mock.MatchExpectationsInOrder(true)
		repo := &ProducerDBRepository{db: db}
		_, _, err := repo.GetList(data.NewPagination(nil, nil))
		assert.Equal(t, expectedErr, err)
		assert.Nil(t, mock.ExpectationsWereMet())
	})
	t.Run("Root", func(t *testing.T) {
		t.Parallel()
		producers, page, err := repo.GetList(data.NewPagination(nil, nil))
		assert.Nil(t, err)
		assert.NotNil(t, producers)
		assert.NotNil(t, page)
		assert.Equal(t, 25, len(producers))
		for _, producer := range producers {
			assert.False(t, producer.ID.IsNil())
			assert.Contains(t, producer.ProducerID, listTestProducerIDPrefix)
			assert.Contains(t, producer.Name, listTestProducerIDPrefix)
			assert.Contains(t, producer.Token, successfulGetTestToken)
		}
		assert.NotNil(t, page.Next)
		assert.NotNil(t, page.Previous)
	})
	t.Run("Previous", func(t *testing.T) {
		t.Parallel()
		firstList, page, _ := repo.GetList(data.NewPagination(nil, nil))
		_, sPage, _ := repo.GetList(&data.Pagination{Next: page.Next})
		lastList, _, _ := repo.GetList(&data.Pagination{Previous: sPage.Previous})
		for _, firstListProducer := range firstList {
			found := false
			for _, secondListProducer := range lastList {
				if firstListProducer.ID == secondListProducer.ID {
					found = true
				}
			}
			if !found {
				t.Log(firstListProducer.ProducerID)
			}
			assert.True(t, found)
		}
	})
	t.Run("Next", func(t *testing.T) {
		t.Parallel()
		firstList, page, _ := repo.GetList(data.NewPagination(nil, nil))
		lastList, _, _ := repo.GetList(&data.Pagination{Next: page.Next})
		for _, firstListProducer := range firstList {
			found := false
			for _, secondListProducer := range lastList {
				if firstListProducer.ID == secondListProducer.ID {
					found = true
				}
			}
			if found {
				t.Log(firstListProducer.ProducerID)
			}
			assert.False(t, found)
		}
	})
	t.Run("TestReadThrough", func(t *testing.T) {
		t.Parallel()
		firstList, page, err := repo.GetList(data.NewPagination(nil, nil))
		assert.Nil(t, err)
		result := make([]*data.Producer, 0)
		result = append(result, firstList...)
		for page.Next != nil {
			var producers []*data.Producer
			producers, page, _ = repo.GetList(&data.Pagination{Next: page.Next})
			result = append(result, producers...)
		}
		assert.GreaterOrEqual(t, len(result), 100)
		count := 0
		for _, producer := range result {
			if strings.Contains(producer.ProducerID, listTestProducerIDPrefix) {
				count = count + 1
			}
		}
		assert.Equal(t, 100, count)
	})
}
