package storage

import (
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database"
	migrate_mysql "github.com/golang-migrate/migrate/v4/database/mysql"
	migrate_sqlite3 "github.com/golang-migrate/migrate/v4/database/sqlite3"
	"github.com/google/wire"
	"github.com/newscred/webhook-broker/config"
	"github.com/newscred/webhook-broker/storage/data"

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
	appRepository              AppRepository
	producerRepository         ProducerRepository
	channelRepository          ChannelRepository
	consumerRepository         ConsumerRepository
	messageRepository          MessageRepository
	deliveryJobRepository      DeliveryJobRepository
	lockRepository             LockRepository
	scheduledMessageRepository ScheduledMessageRepository
	db                         *sql.DB
}

// GetAppRepository returns the AppRepository to be used for App ops
func (rdbmsDataAccessor *RelationalDBDataAccessor) GetAppRepository() AppRepository {
	return rdbmsDataAccessor.appRepository
}

// GetProducerRepository returns the ProducerRepository to be used for Producer ops
func (rdbmsDataAccessor *RelationalDBDataAccessor) GetProducerRepository() ProducerRepository {
	return rdbmsDataAccessor.producerRepository
}

// GetChannelRepository returns the ProducerRepository to be used for Producer ops
func (rdbmsDataAccessor *RelationalDBDataAccessor) GetChannelRepository() ChannelRepository {
	return rdbmsDataAccessor.channelRepository
}

// GetConsumerRepository returns the ProducerRepository to be used for Producer ops
func (rdbmsDataAccessor *RelationalDBDataAccessor) GetConsumerRepository() ConsumerRepository {
	return rdbmsDataAccessor.consumerRepository
}

// GetMessageRepository retrieves the MessageRepository to be used for Message ops
func (rdbmsDataAccessor *RelationalDBDataAccessor) GetMessageRepository() MessageRepository {
	return rdbmsDataAccessor.messageRepository
}

// GetDeliveryJobRepository retrieves the DeliveryJobRepository to be used for DeliverJob ops
func (rdbmsDataAccessor *RelationalDBDataAccessor) GetDeliveryJobRepository() DeliveryJobRepository {
	return rdbmsDataAccessor.deliveryJobRepository
}

// GetLockRepository retrieves the LockRepository to be used for Lock ops
func (rdbmsDataAccessor *RelationalDBDataAccessor) GetLockRepository() LockRepository {
	return rdbmsDataAccessor.lockRepository
}

// GetScheduledMessageRepository retrieves the ScheduledMessageRepository to be used for ScheduledMessage ops
func (rdbmsDataAccessor *RelationalDBDataAccessor) GetScheduledMessageRepository() ScheduledMessageRepository {
	return rdbmsDataAccessor.scheduledMessageRepository
}

// Close closes the connection to DB
func (rdbmsDataAccessor *RelationalDBDataAccessor) Close() {
	db.Close()
}

type orderByClause string
type limitOption string

const (
	insertStatement                               = "INSERT INTO app (id, seedData, appStatus) VALUES (?, ?, ?)"
	selectStatement                               = "SELECT seedData, appStatus FROM app WHERE id = 1"
	startInitUpdateStatement                      = `UPDATE app SET seedData = ?, appStatus = ? WHERE id = 1 AND appStatus != ?`
	completeInitUpdateStatement                   = `UPDATE app SET appStatus = ? WHERE id = 1 AND appStatus = ?`
	optimisticLockInitAppErrMsg                   = "initializing began in another app in the meantime"
	optimisticLockCompleteAppErrMsg               = "initializing not started so can not complete"
	LIMIT_25_SUFFIX                 limitOption   = " LIMIT 25"
	LIMIT_50_SUFFIX                 limitOption   = " LIMIT 50"
	LIMIT_100_SUFFIX                limitOption   = " LIMIT 100"
	LIMIT_500_SUFFIX                limitOption   = " LIMIT 500"
	baseOrderByWithPrefixFmtClause  orderByClause = "ORDER BY %s.createdAt desc, %s.id desc %s"
	baseOrderByClause               orderByClause = "ORDER BY createdAt desc, id desc"
	pageSizeWithOrder               orderByClause = baseOrderByClause + orderByClause(LIMIT_25_SUFFIX)
	mediumPageSizeWithOrder         orderByClause = baseOrderByClause + orderByClause(LIMIT_50_SUFFIX)
	largePageSizeWithOrder          orderByClause = baseOrderByClause + orderByClause(LIMIT_100_SUFFIX)
	extraLargePageSizeWithOrder     orderByClause = baseOrderByClause + orderByClause(LIMIT_500_SUFFIX)
)

var (
	// ErrOptimisticAppInit represents the Error when optimistically update fails to start app init
	ErrOptimisticAppInit = errors.New(optimisticLockInitAppErrMsg)
	// ErrOptimisticAppComplete represents the Error when app complete attempted from not initializing state
	ErrOptimisticAppComplete = errors.New(optimisticLockCompleteAppErrMsg)
	// ErrAppInitializing is returned when app is being initialized by another thread.
	ErrAppInitializing = errors.New("app is in initializing")
	// ErrNoDataChangeFromInitialized is returned when initialization is attempted without any seed data change while app has been initialized
	ErrNoDataChangeFromInitialized = errors.New("no data change on initialized App")
	// ErrCompleteWhileNotBeingInitialized is returned when complete is called without being initialized
	ErrCompleteWhileNotBeingInitialized = errors.New("app not initializing to complete initializing")
	// ErrNoRowsUpdated is returned when a UPDATE query does not change any row which is unexpected
	ErrNoRowsUpdated = errors.New("no rows updated on UPDATE query")
	// ErrInvalidStateToSave is returned when a data is not in a state we can send it to the repo as
	ErrInvalidStateToSave = errors.New("data model in invalid state to be stored")
	// ErrPaginationDeadlock is returned if both after and before is provided in pagination
	ErrPaginationDeadlock = errors.New("can not decide on pagination direction! Both after and before provided or pagination is nil")
)

// GetOrderByClauseWithAlias Returns order by clause with choice of limit option and alias
func getOrderByClauseWithAlias(alias string, limitPhrase limitOption) orderByClause {
	return orderByClause(fmt.Sprintf(string(baseOrderByWithPrefixFmtClause), alias, alias, string(limitPhrase)))
}

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
	var appErr error = transactionalSingleRowWriteExec(appRep.db, emptyOps, insertStatement, args2SliceFnWrapper(1, seedData, &initialState))
	return appErr
}

// GetApp retrieves the App from storage, it will never return nil
func (appRep *AppDBRepository) GetApp() (*data.App, error) {
	seedData := &config.SeedData{}
	appStatus := data.NotInitialized
	err := querySingleRow(appRep.db, selectStatement, nilArgs, args2SliceFnWrapper(seedData, &appStatus))
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
		appErr = ErrAppInitializing
	}
	if currentApp.GetSeedData().DataHash == seedData.DataHash && currentApp.GetStatus() == data.Initialized {
		appErr = ErrNoDataChangeFromInitialized
	}
	if appErr == nil {
		appErr = transactionalSingleRowWriteExec(appRep.db, emptyOps, startInitUpdateStatement, args2SliceFnWrapper(*seedData, data.Initializing, data.Initializing))
		if appErr == ErrNoRowsUpdated {
			appErr = ErrOptimisticAppInit
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
	var appErr error = transactionalSingleRowWriteExec(appRep.db, emptyOps, completeInitUpdateStatement, args2SliceFnWrapper(data.Initialized, data.Initializing))
	if appErr == ErrNoRowsUpdated {
		appErr = ErrOptimisticAppComplete
	}
	return appErr
}

var (
	db                      *sql.DB
	dataAccessorInitializer sync.Once
	// ErrDBConnectionNeverInitialized is returned when same NewDataAccessor is called the first time and it failed to connec to DB; in all subsequent calls the accessor will remain nil
	ErrDBConnectionNeverInitialized = errors.New("DB Connection never initialized")
	// RDBMSStorageInternalInjector injector for data storage related implementation
	RDBMSStorageInternalInjector = wire.NewSet(GetConnectionPool, GetDefaultCacheTTLDuration, NewLockRepository, NewAppRepository, NewProducerRepository, NewCachedProducerRepository, NewChannelRepository, NewCachedChannelRepository, NewConsumerRepository, NewCachedConsumerRepository, NewMessageRepository, NewDeliveryJobRepository, NewScheduledMessageRepository, wire.Struct(new(RelationalDBDataAccessor), "db", "appRepository", "producerRepository", "channelRepository", "consumerRepository", "messageRepository", "deliveryJobRepository", "lockRepository", "scheduledMessageRepository"), wire.Bind(new(DataAccessor), new(*RelationalDBDataAccessor)))
)

func GetDefaultCacheTTLDuration() time.Duration {
	return 4 * time.Hour
}

func panicIfNoDBConnectionPool(db *sql.DB) {
	if db == nil {
		panic(ErrDBConnectionNeverInitialized)
	}
}

// NewAppRepository retrieves App Repository
func NewAppRepository(db *sql.DB) AppRepository {
	panicIfNoDBConnectionPool(db)
	appRepo := &AppDBRepository{db: db}
	return appRepo
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
			dbDriver, err := getMigrationDriver(db, dbConfig)
			if err != nil {
				return err
			}
			dialect := string(dbConfig.GetDBDialect())
			sourceDriver, err := NewDialectSource(migrationConf.MigrationSource, dialect)
			if err != nil {
				return err
			}
			migration, err := getMigration(sourceDriver, dialect, dbDriver)
			if err != nil {
				return err
			}
			err = migration.Up()
			if err != nil && err != migrate.ErrNoChange {
				return err
			}
		}
		return nil
	}

	getMigration = func(sourceDriver *DialectSource, dialect string, dbDriver database.Driver) (*migrate.Migrate, error) {
		return migrate.NewWithInstance("dialect", sourceDriver, dialect, dbDriver)
	}

	getMigrationDriver = func(db *sql.DB, dbConfig config.RelationalDatabaseConfig) (database.Driver, error) {
		switch dbConfig.GetDBDialect() {
		case config.MySQLDialect:
			return migrate_mysql.WithInstance(db, &migrate_mysql.Config{})
		default:
			return migrate_sqlite3.WithInstance(db, &migrate_sqlite3.Config{})
		}
	}

	rollback = func(tx *sql.Tx) {
		txErr := tx.Rollback()
		if txErr != nil {
			log.Error().Err(txErr).Msg("tx rollback error")
		}
	}

	transactionalOperations = func(db *sql.DB, txOps func(tx *sql.Tx) error) (err error) {
		var tx *sql.Tx
		tx, err = db.Begin()
		defer func() {
			if r := recover(); r != nil {
				log.Error().Msg(fmt.Sprint("recovered from in-tx panic", r))
				rollback(tx)
			}
		}()
		if err == nil {
			err = txOps(tx)
			if err == nil {
				txErr := tx.Commit()
				if txErr != nil {
					log.Error().Err(txErr).Msg("tx commit error")
					err = txErr
				}
			} else {
				rollback(tx)
			}
		}
		return err
	}

	inTransactionExec = func(tx *sql.Tx, prequeryOps func(), query string, arguments func() []interface{}, expectedRowEffected int64) (err error) {
		prequeryOps()
		var result sql.Result
		result, err = tx.Exec(query, arguments()...)
		if err == nil {
			var rowsAffected int64
			if rowsAffected, err = result.RowsAffected(); expectedRowEffected > 0 && rowsAffected != expectedRowEffected && err == nil {
				err = ErrNoRowsUpdated
			}
		}
		return err
	}

	getTxWrapperForSingleWriteQuery = func(prequeryOps func(), query string, arguments func() []interface{}) func(tx *sql.Tx) error {
		return func(tx *sql.Tx) error {
			return inTransactionExec(tx, prequeryOps, query, arguments, int64(1))
		}
	}

	transactionalSingleRowWriteExec = func(db *sql.DB, prequeryOps func(), query string, arguments func() []interface{}) error {
		return transactionalWrites(db, getTxWrapperForSingleWriteQuery(prequeryOps, query, arguments))
	}

	transactionalWrites = func(db *sql.DB, ops ...func(tx *sql.Tx) error) error {
		return transactionalOperations(db, func(tx *sql.Tx) (err error) {
			for _, op := range ops {
				err = op(tx)
				if err != nil {
					break
				}
			}
			return err
		})
	}

	getPaginationQueryFragmentWithConfigurablePageSize = func(page *data.Pagination, append bool, orderByQueryClause orderByClause) string {
		return getPaginationQueryFragmentWithConfigurablePageSizeWithAlias(page, append, orderByQueryClause, "")
	}

	getPaginationQueryFragmentWithConfigurablePageSizeWithAlias = func(page *data.Pagination, append bool, orderByQueryClause orderByClause, alias string) string {
		query := " "
		aliasPrefix := ""
		if len(alias) > 0 {
			aliasPrefix = alias + "."
		}
		if page.Next != nil {
			if append {
				query = query + "AND "
			} else {
				query = query + "WHERE "
			}
			query = query + fmt.Sprintf("%sid < '", aliasPrefix) + page.Next.ID + "' "
			query = query + fmt.Sprintf("AND %screatedAt <= ? ", aliasPrefix)
		}
		if page.Previous != nil {
			if append {
				query = query + "AND "
			} else {
				query = query + "WHERE "
			}
			query = query + fmt.Sprintf("%sid > '", aliasPrefix) + page.Previous.ID + "' "
			query = query + fmt.Sprintf("AND %screatedAt >= ? ", aliasPrefix)
		}
		query = query + string(orderByQueryClause)
		return query
	}

	getPaginationQueryFragment = func(page *data.Pagination, append bool) string {
		return getPaginationQueryFragmentWithConfigurablePageSize(page, append, pageSizeWithOrder)
	}

	getPaginationTimestampQueryArgs = func(page *data.Pagination) []interface{} {
		times := make([]interface{}, 0, 2)
		if page.Next != nil {
			times = append(times, page.Next.Timestamp)
		}
		if page.Previous != nil {
			times = append(times, page.Previous.Timestamp)
		}
		return times
	}

	querySingleRow = func(db *sql.DB, query string, queryArgs func() []interface{}, scanArgs func() []interface{}) error {
		row := db.QueryRow(query, queryArgs()...)
		return row.Scan(scanArgs()...)
	}

	queryRows = func(db *sql.DB, query string, queryArgs func() []interface{}, scanArgs func() []interface{}) error {
		rows, err := db.Query(query, queryArgs()...)
		if err != nil {
			return err
		}
		defer func() { rows.Close() }()
		for rows.Next() {
			err = rows.Scan(scanArgs()...)
			if err != nil {
				return err
			}
		}
		return err
	}

	appendWithPaginationArgs = func(page *data.Pagination, args ...interface{}) []interface{} {
		return append(args, getPaginationTimestampQueryArgs(page)...)
	}

	nilArgs             = func() []interface{} { return nil }
	emptyOps            = func() {}
	args2SliceFnWrapper = func(args ...interface{}) func() []interface{} {
		return func() []interface{} { return args }
	}
)
