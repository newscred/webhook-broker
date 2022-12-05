//go:build wireinject
// +build wireinject

package main

import (
	"github.com/google/wire"
	"github.com/newscred/webhook-broker/config"
)

// GetAppVersion retrieves the app version
func GetAppVersion() config.AppVersion {
	wire.Build(config.GetVersion)

	return ""
}

// GetHTTPServer returns the server container with all adjacent data
func GetHTTPServer(cliConfig *config.CLIConfig) (*HTTPServiceContainer, error) {
	wire.Build(relationalDBWithControllerSet, configInjectorSet)

	return &HTTPServiceContainer{}, nil
}
