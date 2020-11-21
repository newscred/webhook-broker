//+build wireinject

package main

import (
	"github.com/google/wire"
	"github.com/imyousuf/webhook-broker/config"
)

// GetAppVersion retrieves the app version
func GetAppVersion() config.AppVersion {
	wire.Build(config.GetVersion)

	return ""
}
