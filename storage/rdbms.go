package storage

import (
	"database/sql"
	"errors"
	"sync"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database"
	migrate_mysql "github.com/golang-migrate/migrate/v4/database/mysql"
	migrate_sqlite3 "github.com/golang-migrate/migrate/v4/database/sqlite3"
	"github.com/google/wire"
	"github.com/imyousuf/webhook-broker/config"
	"github.com/imyousuf/webhook-broker/storage/data"

	// MySQL DB Driver
	_ "github.com/go-sql-driver/mysql"
	// SQLite3 DB Driver
	_ "github.com/mattn/go-sqlite3"
	// File as a source for migration
	_ "github.com/golang-migrate/migrate/v4/source/file"
)

// MigrationConfig represents the DB migration config
type MigrationConfig struct {
	MigrationEnabled bool
	MigrationSource  string
}

// RelationalDBDataAccessor represents the DataAccessor implementation for RDBMS
type RelationalDBDataAccessor struct {
	appRepository AppRepository
	db            *sql.DB
}

// GetAppRepository returns the AppRepository to be used for App ops
func (rdbmsDataAccessor *RelationalDBDataAccessor) GetAppRepository() AppRepository {
	return rdbmsDataAccessor.appRepository
}

// Close closes the connection to DB
func (rdbmsDataAccessor *RelationalDBDataAccessor) Close() {
	db.Close()
}

const (
	insertStatement                 = "INSERT INTO app (id, seedData, appStatus) VALUES ($1, $2, $3)"
	selectStatement                 = "SELECT seedData, appStatus FROM app WHERE id = 1"
	startInitUpdateStatement        = `UPDATE app SET seedData = $1, appStatus = $2 WHERE id = 1 AND appStatus != $2`
	completeInitUpdateStatement     = `UPDATE app SET appStatus = $1 WHERE id = 1 AND appStatus == $2`
	optimisticLockInitAppErrMsg     = "Initializing began in another app in the meantime"
	optimisticLockCompleteAppErrMsg = "Initializing not started so can not complete"
)

var (
	// ErrOptimisticAppInit represents the Error when optimistically update fails to start app init
	ErrOptimisticAppInit = errors.New(optimisticLockInitAppErrMsg)
	// ErrOptimisticAppComplete represents the Error when app complete attempted from not initializing state
	ErrOptimisticAppComplete = errors.New(optimisticLockCompleteAppErrMsg)
	// ErrAppInitializing is returned when app is being initialized by another thread.
	ErrAppInitializing = errors.New("App is in initializing")
	// ErrNoDataChangeFromInitialized is returned when initialization is attempted without any seed data change while app has been initialized
	ErrNoDataChangeFromInitialized = errors.New("No data change on initialized App")
	// ErrCompleteWhileNotBeingInitialized is returned when complete is called without being initialized
	ErrCompleteWhileNotBeingInitialized = errors.New("App not initializing to complete initializing")
)

// AppDBRepository is the repository to access App data
type AppDBRepository struct {
	db *sql.DB
}

// InitAppData initializes only and only if none present in DB with status NotInitialized. Error if insertion fails.
func (appRep *AppDBRepository) InitAppData(seedData *config.SeedData) error {
	_, err := appRep.GetApp()
	if err == nil {
		return nil
	}
	// INSERT SQL
	initialState := data.NotInitialized
	tx, err := appRep.db.Begin()
	var appErr error
	if appErr = err; appErr == nil {
		_, err = appRep.db.Exec(insertStatement, 1, seedData, &initialState)
		if appErr = err; appErr == nil {
			tx.Commit()
		} else {
			tx.Rollback()
		}
	}
	return appErr
}

// GetApp retrieves the App from storage, it will never return nil
func (appRep *AppDBRepository) GetApp() (*data.App, error) {
	seedData := &config.SeedData{}
	appStatus := data.NotInitialized
	row := appRep.db.QueryRow(selectStatement)
	err := row.Scan(seedData, &appStatus)
	return data.NewApp(seedData, appStatus), err
}

// StartAppInit stores state that App initialization started. It will return error if App is in Initializing state or if data hash is equal and app in initialized state
func (appRep *AppDBRepository) StartAppInit(seedData *config.SeedData) error {
	var appErr error
	currentApp, err := appRep.GetApp()
	if err != nil {
		return err
	}
	if currentApp.GetStatus() == data.Initializing {
		return ErrAppInitializing
	}
	if currentApp.GetSeedData().DataHash == seedData.DataHash && currentApp.GetStatus() == data.Initialized {
		return ErrNoDataChangeFromInitialized
	}
	// UPDATE SQL with condition
	tx, err := appRep.db.Begin()
	appErr = err
	if appErr == nil {
		result, err := appRep.db.Exec(startInitUpdateStatement, *seedData, data.Initializing)
		appErr = err
		if appErr == nil {
			if rowsChanged, err := result.RowsAffected(); err == nil && rowsChanged <= 0 {
				appErr = ErrOptimisticAppInit
			}
			tx.Commit()
		} else {
			tx.Rollback()
		}
	}
	return appErr
}

// CompleteAppInit stores that App initialization completed; it will return error if app is not in initializing state before the update is made
func (appRep *AppDBRepository) CompleteAppInit() error {
	currentApp, err := appRep.GetApp()
	if err != nil {
		return err
	}
	if currentApp.GetStatus() != data.Initializing {
		return ErrCompleteWhileNotBeingInitialized
	}
	// UPDATE SQL with condition
	var appErr error
	tx, err := appRep.db.Begin()
	if appErr = err; appErr == nil {
		result, err := appRep.db.Exec(completeInitUpdateStatement, data.Initialized, data.Initializing)
		if appErr = err; appErr == nil {
			if rowsChanged, err := result.RowsAffected(); err == nil && rowsChanged <= 0 {
				appErr = ErrOptimisticAppComplete
			}
			tx.Commit()
		} else {
			tx.Rollback()
		}
	}
	return appErr
}

var (
	db                      *sql.DB
	dataAccessorInitializer sync.Once
	// ErrDBConnectionNeverInitialized is returned when same NewDataAccessor is called the first time and it failed to connec to DB; in all subsequent calls the accessor will remain nil
	ErrDBConnectionNeverInitialized = errors.New("DB Connection never initialized")
	// RDBMSStorageSet injector for data storage related implementation
	RDBMSStorageSet = wire.NewSet(GetConnectionPool, NewAppRepository, NewDataAccessor)
)

// NewDataAccessor retrieves the DB accessor
func NewDataAccessor(db *sql.DB, appRepo AppRepository) (DataAccessor, error) {
	var err error
	if db == nil {
		err = ErrDBConnectionNeverInitialized
	}
	dataAccessor := &RelationalDBDataAccessor{db: db, appRepository: appRepo}
	return dataAccessor, err
}

// NewAppRepository retrieves App Repository
func NewAppRepository(db *sql.DB) (AppRepository, error) {
	var err error
	if db == nil {
		err = ErrDBConnectionNeverInitialized
	}
	appRepo := &AppDBRepository{db: db}
	return appRepo, err
}

// GetConnectionPool Gets the DB Connection Pool for the App
func GetConnectionPool(dbConfig config.RelationalDatabaseConfig, migrationConf *MigrationConfig, seedDataConfig config.SeedDataConfig) (*sql.DB, error) {
	return getConnectionPoolImpl(dbConfig, migrationConf, seedDataConfig)
}

var (
	getConnectionPoolImpl = func(dbConfig config.RelationalDatabaseConfig, migrationConf *MigrationConfig, seedDataConfig config.SeedDataConfig) (*sql.DB, error) {
		var err error = nil
		dataAccessorInitializer.Do(func() {
			// Initialize DB Connection
			db, err = createDBConnectionPool(dbConfig)
			if err == nil {
				// Run Migration
				err = runMigration(db, dbConfig, migrationConf)
				if err == nil {
					appRepo := &AppDBRepository{db: db}
					seedData := seedDataConfig.GetSeedData()
					err = appRepo.InitAppData(&seedData)
				}
			}
		})
		if db == nil && err == nil {
			err = ErrDBConnectionNeverInitialized
		}
		return db, err
	}

	createDBConnectionPool = func(dbConfig config.RelationalDatabaseConfig) (*sql.DB, error) {
		db, err := getDB(string(dbConfig.GetDBDialect()), dbConfig.GetDBConnectionURL())
		if err == nil {
			db.SetConnMaxLifetime(dbConfig.GetDBConnectionMaxLifetime())
			db.SetMaxIdleConns(int(dbConfig.GetMaxIdleDBConnections()))
			db.SetMaxOpenConns(int(dbConfig.GetMaxOpenDBConnections()))
			db.SetConnMaxIdleTime(dbConfig.GetDBConnectionMaxIdleTime())
		}
		return db, err
	}

	getDB = func(dialect, connectionURL string) (*sql.DB, error) {
		return sql.Open(string(dialect), connectionURL)
	}
	runMigration = func(db *sql.DB, dbConfig config.RelationalDatabaseConfig, migrationConf *MigrationConfig) error {
		if migrationConf.MigrationEnabled {
			driver, err := getMigrationDriver(db, dbConfig)
			if err != nil {
				return err
			}
			migration, err := getMigration(migrationConf.MigrationSource, string(dbConfig.GetDBDialect()), driver)
			if err != nil {
				return err
			}
			migration.Steps(2)
		}
		return nil
	}

	getMigration = func(source, dialect string, driver database.Driver) (*migrate.Migrate, error) {
		return migrate.NewWithDatabaseInstance(source, dialect, driver)
	}

	getMigrationDriver = func(db *sql.DB, dbConfig config.RelationalDatabaseConfig) (database.Driver, error) {
		switch dbConfig.GetDBDialect() {
		case config.MySQLDialect:
			return migrate_mysql.WithInstance(db, &migrate_mysql.Config{})
		default:
			return migrate_sqlite3.WithInstance(db, &migrate_sqlite3.Config{})
		}
	}
)
