//+build wireinject

package main

import (
	"github.com/google/wire"
	"github.com/imyousuf/webhook-broker/config"
	"github.com/imyousuf/webhook-broker/controllers"
	"github.com/imyousuf/webhook-broker/storage"
)

var (
	configInjectorSet             = wire.NewSet(GetConfig, wire.Bind(new(config.SeedDataConfig), new(*config.Config)), wire.Bind(new(config.HTTPConfig), new(*config.Config)), wire.Bind(new(config.RelationalDatabaseConfig), new(*config.Config)))
	relationalDBWithControllerSet = wire.NewSet(NewHTTPServiceContainer, NewServerListener, configInjectorSet, GetMigrationConfig, wire.Bind(new(controllers.ServerLifecycleListener), new(*ServerLifecycleListenerImpl)), controllers.ConfigureAPI, storage.NewDataAccessor)
)

// GetAppVersion retrieves the app version
func GetAppVersion() config.AppVersion {
	wire.Build(config.GetVersion)

	return ""
}

func GetHTTPServer() (*HTTPServiceContainer, error) {
	wire.Build(relationalDBWithControllerSet)

	return &HTTPServiceContainer{}, nil
}
