//go:build wireinject
// +build wireinject

package storage

import (
	"github.com/google/wire"
	"github.com/newscred/webhook-broker/config"
)

// GetNewDataAccessor provides the facade for accessing all the object repositories
func GetNewDataAccessor(dbConfig config.RelationalDatabaseConfig, migrationConf *MigrationConfig, seedDataConfig config.SeedDataConfig) (DataAccessor, error) {
	wire.Build(RDBMSStorageInternalInjector)

	return nil, nil
}
