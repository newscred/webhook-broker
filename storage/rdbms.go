package storage

import (
	"database/sql"
	"errors"
	"sync"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database"
	migrate_mysql "github.com/golang-migrate/migrate/v4/database/mysql"
	migrate_sqlite3 "github.com/golang-migrate/migrate/v4/database/sqlite3"
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
	appRepository *AppDBRepository
}

// GetAppRepository returns the AppRepository to be used for App ops
func (rdbmsDataAccessor *RelationalDBDataAccessor) GetAppRepository() AppRepository {
	return rdbmsDataAccessor.appRepository
}

// Close closes the connection to DB
func (rdbmsDataAccessor *RelationalDBDataAccessor) Close() {

}

const (
	insertStatement                 = "INSERT INTO app (id, seedData, appStatus) VALUES ($1, $2, $3)"
	selectStatement                 = "SELECT seedData, appStatus FROM app WHERE id = 1"
	optimisticLockInitAppErrMsg     = "Initializing began in another app in the meantime"
	optimisticLockCompleteAppErrMsg = "Initializing not started so can not complete"
)

var (
	// ErrOptimisticAppInit represents the Error when optimistically update fails to start app init
	ErrOptimisticAppInit = errors.New(optimisticLockInitAppErrMsg)
	// ErrOptimisticAppComplete represents the Error when app complete attempted from not initializing state
	ErrOptimisticAppComplete = errors.New(optimisticLockCompleteAppErrMsg)
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
	_, err = appRep.db.Exec(insertStatement, 1, seedData, &initialState)
	return err
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
	currentApp, err := appRep.GetApp()
	if err != nil {
		return err
	}
	if currentApp.GetStatus() == data.Initializing {
		return errors.New("App is in initialized")
	}
	if currentApp.GetSeedData().DataHash == seedData.DataHash && currentApp.GetStatus() == data.Initialized {
		return errors.New("No data change on initialized App")
	}
	// UPDATE SQL with condition
	updateStatement := `UPDATE app
	SET seedData = $1, appStatus = $2
	WHERE id = 1 AND appStatus != $2`
	result, err := appRep.db.Exec(updateStatement, seedData, data.Initializing)
	if err == nil {
		if rowsChanged, err := result.RowsAffected(); err == nil && rowsChanged <= 0 {
			err = ErrOptimisticAppInit
		}
	}
	return err
}

// CompleteAppInit stores that App initialization completed; it will return error if app is not in initializing state before the update is made
func (appRep *AppDBRepository) CompleteAppInit() error {
	currentApp, err := appRep.GetApp()
	if err != nil {
		return err
	}
	if currentApp.GetStatus() != data.Initializing {
		return errors.New("App not initializing to complete initializing")
	}
	// UPDATE SQL with condition
	updateStatement := `UPDATE app
	SET appStatus = $1
	WHERE id = 1 AND appStatus != $2`
	result, err := appRep.db.Exec(updateStatement, data.Initialized, data.Initializing)
	if err == nil {
		if rowsChanged, err := result.RowsAffected(); err == nil && rowsChanged <= 0 {
			err = ErrOptimisticAppComplete
		}
	}
	return err
}

var (
	dataAccessor            *RelationalDBDataAccessor
	dataAccessorInitializer sync.Once
)

// NewDataAccessor retrieves the DB accessor
func NewDataAccessor(dbConfig config.RelationalDatabaseConfig, migrationConf *MigrationConfig, seedDataConfig config.SeedDataConfig) (DataAccessor, error) {
	var err error = nil
	dataAccessorInitializer.Do(func() {
		var db *sql.DB
		// Initialize DB Connection
		db, err = sql.Open(string(dbConfig.GetDBDialect()), dbConfig.GetDBConnectionURL())
		if err == nil {
			db.SetConnMaxLifetime(dbConfig.GetDBConnectionMaxLifetime())
			db.SetMaxIdleConns(int(dbConfig.GetMaxIdleDBConnections()))
			db.SetMaxOpenConns(int(dbConfig.GetMaxOpenDBConnections()))
			db.SetConnMaxIdleTime(dbConfig.GetDBConnectionMaxIdleTime())
			// Run Migration
			err = runMigration(db, dbConfig, migrationConf)
			if err == nil {
				appRepo := &AppDBRepository{db: db}
				seedData := seedDataConfig.GetSeedData()
				err = appRepo.InitAppData(&seedData)
				// Set Data accessor
				dataAccessor = &RelationalDBDataAccessor{appRepository: appRepo}
			}
		}
	})
	return dataAccessor, err
}

func runMigration(db *sql.DB, dbConfig config.RelationalDatabaseConfig, migrationConf *MigrationConfig) error {
	if migrationConf.MigrationEnabled {
		driver, err := getMigrationDriver(db, dbConfig)
		if err != nil {
			return err
		}
		migration, err := migrate.NewWithDatabaseInstance(migrationConf.MigrationSource, string(dbConfig.GetDBDialect()), driver)
		if err != nil {
			return err
		}
		migration.Steps(2)
	}
	return nil
}

func getMigrationDriver(db *sql.DB, dbConfig config.RelationalDatabaseConfig) (database.Driver, error) {
	switch dbConfig.GetDBDialect() {
	case config.MySQLDialect:
		return migrate_mysql.WithInstance(db, &migrate_mysql.Config{})
	default:
		return migrate_sqlite3.WithInstance(db, &migrate_sqlite3.Config{})
	}
}
