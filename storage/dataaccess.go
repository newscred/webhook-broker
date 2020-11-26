package storage

import (
	"github.com/imyousuf/webhook-broker/config"
	"github.com/imyousuf/webhook-broker/storage/data"
)

// DataAccessor is the facade to all the data repository
type DataAccessor interface {
	GetAppRepository() AppRepository
	Close()
}

// AppRepository allows storage operation interaction for App
type AppRepository interface {
	GetApp() (*data.App, error)
	StartAppInit(data *config.SeedData) error
	CompleteAppInit() error
}
