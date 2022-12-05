package storage

import (
	"bytes"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog/log"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database"
	"github.com/newscred/webhook-broker/config"
	configmocks "github.com/newscred/webhook-broker/config/mocks"
	"github.com/newscred/webhook-broker/storage/data"
	"github.com/stretchr/testify/assert"
)

var (
	configuration *config.Config
	testDB        *sql.DB
)

func TestMain(m *testing.M) {
	// Setup DB and migration
	os.Remove("./webhook-broker.sqlite3")
	configuration, _ = config.GetAutoConfiguration()
	configuration.RationalDelay = 20 * time.Millisecond
	var dbErr error
	testDB, dbErr = GetConnectionPool(configuration, defaultMigrationConf, configuration)
	if dbErr == nil {
		SetupForConsumerTests()
		SetupForMessageTests()
		SetupForDeliveryJobTests()
		m.Run()
	}
	testDB.Close()
}

func dbPanicDeferAssert(t *testing.T) {
	r := recover()
	assert.Equal(t, ErrDBConnectionNeverInitialized, r)
}

var (
	migrationLocation, _ = filepath.Abs("../migration/sqls/")
	defaultMigrationConf = &MigrationConfig{MigrationEnabled: true, MigrationSource: "file://" + migrationLocation}
)

func TestGetNewDataAccessor(t *testing.T) {
	// Clear DB before starting test
	os.Remove("./webhook-broker.sqlite3")
	configuration, _ := config.GetAutoConfiguration()
	t.Run("DBConnectionErr", func(t *testing.T) {
		dataAccessorInitializer = sync.Once{}
		oldGetDB := getDB
		defer func() { getDB = oldGetDB }()
		dbConnectionErr := errors.New("DB Connection Error")
		getDB = func(dialect, connectionURL string) (*sql.DB, error) {
			return nil, dbConnectionErr
		}
		_, err := GetNewDataAccessor(configuration, defaultMigrationConf, configuration)
		assert.Equal(t, dbConnectionErr, err)
		t.Run("RetryingAfterConnectionErr", func(t *testing.T) {
			_, err := GetNewDataAccessor(configuration, defaultMigrationConf, configuration)
			assert.Equal(t, ErrDBConnectionNeverInitialized, err)
		})
	})
	t.Run("MigrationDisabled", func(t *testing.T) {
		dataAccessorInitializer = sync.Once{}
		migrationConf := &MigrationConfig{MigrationEnabled: false, MigrationSource: "file://" + migrationLocation}
		_, err := GetNewDataAccessor(configuration, migrationConf, configuration)
		assert.NotNil(t, err)
	})
	t.Run("MigrationDriverErr", func(t *testing.T) {
		dataAccessorInitializer = sync.Once{}
		oldGetMigrationDriver := getMigrationDriver
		defer func() { getMigrationDriver = oldGetMigrationDriver }()
		migrationErr := errors.New("Migration Driver Error")
		getMigrationDriver = func(db *sql.DB, dbConfig config.RelationalDatabaseConfig) (database.Driver, error) {
			return nil, migrationErr
		}
		_, err := GetNewDataAccessor(configuration, defaultMigrationConf, configuration)
		assert.Equal(t, migrationErr, err)
	})
	t.Run("MigrationRunErr", func(t *testing.T) {
		dataAccessorInitializer = sync.Once{}
		dataAccessorInitializer = sync.Once{}
		oldGetMigration := getMigration
		defer func() { getMigration = oldGetMigration }()
		migrationErr := errors.New("Migration Error")
		getMigration = func(source, dialect string, driver database.Driver) (*migrate.Migrate, error) {
			return nil, migrationErr
		}
		_, err := GetNewDataAccessor(configuration, defaultMigrationConf, configuration)
		assert.Equal(t, migrationErr, err)
	})
	t.Run("MigrationDriverMySQL", func(t *testing.T) {
		oldGetDB := getDB
		db, mock, _ := sqlmock.New()
		defer func() {
			getDB = oldGetDB
			db.Close()
		}()
		getDB = func(dialect, connectionURL string) (*sql.DB, error) {
			return db, nil
		}
		mock.ExpectPing()
		row := mock.NewRows([]string{"databaseName"}).FromCSVString("sample_database")
		mock.ExpectQuery("SELECT DATABASE()").WillReturnRows(row).WillReturnError(nil)
		dbConfig := new(configmocks.RelationalDatabaseConfig)
		dbConfig.On("GetDBDialect").Return(config.MySQLDialect)
		mock.MatchExpectationsInOrder(true)
		_, err := getMigrationDriver(db, dbConfig)
		mErr := mock.ExpectationsWereMet()
		assert.Nil(t, mErr)
		dbConfig.AssertExpectations(t)
		// Err is expected since there is no way to mock db.conn.querycontext used by mysql driver
		assert.NotNil(t, err)
	})
	t.Run("SuccessRun", func(t *testing.T) {
		dataAccessorInitializer = sync.Once{}
		dataAccessor, err := GetNewDataAccessor(configuration, defaultMigrationConf, configuration)
		assert.Nil(t, err)
		assert.NotNil(t, dataAccessor)
		assert.NotNil(t, dataAccessor.GetAppRepository())
		assert.NotNil(t, dataAccessor.GetProducerRepository())
		assert.NotNil(t, dataAccessor.GetChannelRepository())
		assert.NotNil(t, dataAccessor.GetConsumerRepository())
		assert.NotNil(t, dataAccessor.GetMessageRepository())
		assert.NotNil(t, dataAccessor.GetDeliveryJobRepository())
		assert.NotNil(t, dataAccessor.GetLockRepository())
		// Does nothing
		dataAccessor.Close()
		t.Run("InitAppSkip", func(t *testing.T) {
			dataAccessorInitializer = sync.Once{}
			dataAccessor, err := GetNewDataAccessor(configuration, defaultMigrationConf, configuration)
			assert.Nil(t, err)
			assert.NotNil(t, dataAccessor)
		})
	})
	t.Run("NewAppRepositoryWithNilDB", func(t *testing.T) {
		t.Parallel()
		defer dbPanicDeferAssert(t)
		NewAppRepository(nil)
	})
}

func TestAppDBRepositoryStartAppInit(t *testing.T) {
	t.Parallel()
	configuration, _ := config.GetAutoConfiguration()
	seedData := configuration.GetSeedData()
	getMockSelectAppRow := func(mock sqlmock.Sqlmock, seedData *config.SeedData, appStatus data.AppStatus) *sqlmock.Rows {
		seedDataDriverVal, _ := seedData.Value()
		return mock.NewRows([]string{"seedData", "appStatus"}).AddRow(seedDataDriverVal, appStatus)
	}
	seedDataVal, _ := seedData.Value()
	t.Run("NoApp", func(t *testing.T) {
		t.Parallel()
		db, mock, _ := sqlmock.New()
		appRepo := &AppDBRepository{db: db}
		mock.MatchExpectationsInOrder(true)
		err := errors.New("No App")
		mock.ExpectQuery(selectStatement).WillReturnError(err)
		// Main Call
		aErr := appRepo.StartAppInit(&seedData)
		mErr := mock.ExpectationsWereMet()
		assert.Nil(t, mErr)
		assert.Equal(t, err, aErr)
	})
	t.Run("AppAlreadyInitializing", func(t *testing.T) {
		t.Parallel()
		db, mock, _ := sqlmock.New()
		appRepo := &AppDBRepository{db: db}
		mock.MatchExpectationsInOrder(true)
		mock.ExpectQuery(selectStatement).WillReturnRows(getMockSelectAppRow(mock, &seedData, data.Initializing))
		// Main Call
		aErr := appRepo.StartAppInit(&seedData)
		mErr := mock.ExpectationsWereMet()
		assert.Nil(t, mErr)
		assert.Equal(t, ErrAppInitializing, aErr)
	})
	t.Run("NoDataChangeOnInitializedState", func(t *testing.T) {
		t.Parallel()
		db, mock, _ := sqlmock.New()
		appRepo := &AppDBRepository{db: db}
		mock.MatchExpectationsInOrder(true)
		mock.ExpectQuery(selectStatement).WillReturnRows(getMockSelectAppRow(mock, &seedData, data.Initialized))
		// Main Call
		aErr := appRepo.StartAppInit(&seedData)
		mErr := mock.ExpectationsWereMet()
		assert.Nil(t, mErr)
		assert.Equal(t, ErrNoDataChangeFromInitialized, aErr)
	})
	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		db, mock, _ := sqlmock.New()
		appRepo := &AppDBRepository{db: db}
		mock.MatchExpectationsInOrder(true)
		mock.ExpectQuery(selectStatement).WillReturnRows(getMockSelectAppRow(mock, &seedData, data.NotInitialized))
		mock.ExpectBegin()
		mock.ExpectExec("UPDATE app").WithArgs(seedDataVal, data.Initializing, data.Initializing).WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectCommit()
		// Main Call
		aErr := appRepo.StartAppInit(&seedData)
		mErr := mock.ExpectationsWereMet()
		assert.Nil(t, mErr)
		assert.Nil(t, aErr)
	})
	t.Run("UpdateFailed", func(t *testing.T) {
		t.Parallel()
		db, mock, _ := sqlmock.New()
		appRepo := &AppDBRepository{db: db}
		mock.MatchExpectationsInOrder(true)
		mock.ExpectQuery(selectStatement).WillReturnRows(getMockSelectAppRow(mock, &seedData, data.NotInitialized))
		mock.ExpectBegin()
		err := errors.New("Update failed")
		mock.ExpectExec("UPDATE app").WithArgs(seedDataVal, data.Initializing, data.Initializing).WillReturnError(err)
		mock.ExpectRollback()
		// Main Call
		aErr := appRepo.StartAppInit(&seedData)
		mErr := mock.ExpectationsWereMet()
		assert.Nil(t, mErr)
		assert.Equal(t, err, aErr)
	})
	t.Run("OptimisticWriteFailure", func(t *testing.T) {
		t.Parallel()
		db, mock, _ := sqlmock.New()
		appRepo := &AppDBRepository{db: db}
		mock.MatchExpectationsInOrder(true)
		mock.ExpectQuery(selectStatement).WillReturnRows(getMockSelectAppRow(mock, &seedData, data.NotInitialized))
		mock.ExpectBegin()
		mock.ExpectExec("UPDATE app").WithArgs(seedDataVal, data.Initializing, data.Initializing).WillReturnResult(sqlmock.NewResult(1, 0))
		mock.ExpectRollback()
		// Main Call
		aErr := appRepo.StartAppInit(&seedData)
		mErr := mock.ExpectationsWereMet()
		assert.Nil(t, mErr)
		assert.Equal(t, ErrOptimisticAppInit, aErr)
	})
}

func TestTransactionalCommitRollbackErrors(t *testing.T) {
	var buf bytes.Buffer
	configuration, _ := config.GetAutoConfiguration()
	seedData := configuration.GetSeedData()
	getMockSelectAppRow := func(mock sqlmock.Sqlmock, seedData *config.SeedData, appStatus data.AppStatus) *sqlmock.Rows {
		seedDataDriverVal, _ := seedData.Value()
		return mock.NewRows([]string{"seedData", "appStatus"}).AddRow(seedDataDriverVal, appStatus)
	}
	seedDataVal, _ := seedData.Value()
	t.Run("RollbackFailed", func(t *testing.T) {
		var buf bytes.Buffer
		oldLogger := log.Logger
		log.Logger = log.Output(&buf)
		defer func() {
			log.Logger = oldLogger
		}()
		db, mock, _ := sqlmock.New()
		appRepo := &AppDBRepository{db: db}
		mock.MatchExpectationsInOrder(true)
		mock.ExpectQuery(selectStatement).WillReturnRows(getMockSelectAppRow(mock, &seedData, data.NotInitialized))
		mock.ExpectBegin()
		err := errors.New("Update failed")
		mock.ExpectExec("UPDATE app").WithArgs(seedDataVal, data.Initializing, data.Initializing).WillReturnError(err)
		mock.ExpectRollback().WillReturnError(err)
		// Main Call
		aErr := appRepo.StartAppInit(&seedData)
		mErr := mock.ExpectationsWereMet()
		assert.Nil(t, mErr)
		assert.Equal(t, err, aErr)
		assert.Contains(t, buf.String(), "tx rollback error")
	})
	t.Run("CommitFailed", func(t *testing.T) {
		oldLogger := log.Logger
		log.Logger = log.Output(&buf)
		defer func() {
			log.Logger = oldLogger
		}()
		db, mock, _ := sqlmock.New()
		appRepo := &AppDBRepository{db: db}
		mock.MatchExpectationsInOrder(true)
		mock.ExpectQuery(selectStatement).WillReturnRows(getMockSelectAppRow(mock, &seedData, data.NotInitialized))
		mock.ExpectBegin()
		mock.ExpectExec("UPDATE app").WithArgs(seedDataVal, data.Initializing, data.Initializing).WillReturnResult(sqlmock.NewResult(1, 1))
		err := errors.New("Update failed")
		mock.ExpectCommit().WillReturnError(err)
		// Main Call
		aErr := appRepo.StartAppInit(&seedData)
		mErr := mock.ExpectationsWereMet()
		assert.Nil(t, mErr)
		assert.Equal(t, err, aErr)
		assert.Contains(t, buf.String(), "tx commit error")
	})
	t.Run("PanicRollbackFailed", func(t *testing.T) {
		oldLogger := log.Logger
		log.Logger = log.Output(&buf)
		defer func() {
			log.Logger = oldLogger
		}()
		db, mock, _ := sqlmock.New()
		mock.MatchExpectationsInOrder(true)
		mock.ExpectBegin()
		transactionalSingleRowWriteExec(db, func() {
			panic(1)
		}, "", func() []interface{} { return nil })
		assert.Contains(t, buf.String(), "tx rollback error")
		assert.Contains(t, buf.String(), "recovered from in-tx panic")
	})
}

func TestAppDBRepositoryCompleteAppInit(t *testing.T) {
	t.Parallel()
	configuration, _ := config.GetAutoConfiguration()
	seedData := configuration.GetSeedData()
	getMockSelectAppRow := func(mock sqlmock.Sqlmock, seedData *config.SeedData, appStatus data.AppStatus) *sqlmock.Rows {
		seedDataDriverVal, _ := seedData.Value()
		return mock.NewRows([]string{"seedData", "appStatus"}).AddRow(seedDataDriverVal, appStatus)

	}
	t.Run("NoApp", func(t *testing.T) {
		t.Parallel()
		db, mock, _ := sqlmock.New()
		appRepo := &AppDBRepository{db: db}
		mock.MatchExpectationsInOrder(true)
		err := errors.New("No App")
		mock.ExpectQuery(selectStatement).WillReturnError(err)
		// Main Call
		aErr := appRepo.CompleteAppInit()
		mErr := mock.ExpectationsWereMet()
		assert.Nil(t, mErr)
		assert.Equal(t, err, aErr)
	})
	t.Run("AppNotInitializing", func(t *testing.T) {
		t.Parallel()
		db, mock, _ := sqlmock.New()
		appRepo := &AppDBRepository{db: db}
		mock.MatchExpectationsInOrder(true)
		mock.ExpectQuery(selectStatement).WillReturnRows(getMockSelectAppRow(mock, &seedData, data.Initialized))
		// Main Call
		aErr := appRepo.CompleteAppInit()
		mErr := mock.ExpectationsWereMet()
		assert.Nil(t, mErr)
		assert.Equal(t, ErrCompleteWhileNotBeingInitialized, aErr)
	})
	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		db, mock, _ := sqlmock.New()
		appRepo := &AppDBRepository{db: db}
		mock.MatchExpectationsInOrder(true)
		mock.ExpectQuery(selectStatement).WillReturnRows(getMockSelectAppRow(mock, &seedData, data.Initializing))
		mock.ExpectBegin()
		mock.ExpectExec("UPDATE app").WithArgs(data.Initialized, data.Initializing).WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectCommit()
		// Main Call
		aErr := appRepo.CompleteAppInit()
		mErr := mock.ExpectationsWereMet()
		assert.Nil(t, mErr)
		assert.Nil(t, aErr)
	})
	t.Run("UpdateFailed", func(t *testing.T) {
		t.Parallel()
		db, mock, _ := sqlmock.New()
		appRepo := &AppDBRepository{db: db}
		err := errors.New("Update App Error")
		mock.MatchExpectationsInOrder(true)
		mock.ExpectQuery(selectStatement).WillReturnRows(getMockSelectAppRow(mock, &seedData, data.Initializing))
		mock.ExpectBegin()
		mock.ExpectExec("UPDATE app").WithArgs(data.Initialized, data.Initializing).WillReturnError(err)
		mock.ExpectRollback()
		// Main Call
		aErr := appRepo.CompleteAppInit()
		mErr := mock.ExpectationsWereMet()
		assert.Nil(t, mErr)
		assert.Equal(t, err, aErr)
	})
	t.Run("OptimisticWriteFailure", func(t *testing.T) {
		t.Parallel()
		db, mock, _ := sqlmock.New()
		appRepo := &AppDBRepository{db: db}
		mock.MatchExpectationsInOrder(true)
		mock.ExpectQuery(selectStatement).WillReturnRows(getMockSelectAppRow(mock, &seedData, data.Initializing))
		mock.ExpectBegin()
		mock.ExpectExec("UPDATE app").WithArgs(data.Initialized, data.Initializing).WillReturnResult(sqlmock.NewResult(1, 0))
		mock.ExpectRollback()
		// Main Call
		aErr := appRepo.CompleteAppInit()
		mErr := mock.ExpectationsWereMet()
		assert.Nil(t, mErr)
		assert.Equal(t, ErrOptimisticAppComplete, aErr)
	})
}
