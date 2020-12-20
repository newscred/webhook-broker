package data

import (
	"github.com/imyousuf/webhook-broker/config"
)

// AppStatus represents the status of this App
type AppStatus int

const (
	// NotInitialized is when the App is just started and no initialization ever happened
	NotInitialized AppStatus = iota + 1
	// Initializing is when App has started to run the initializing process
	Initializing
	// Initialized is when init process is completed for the App
	Initialized
)

// App represents this application state for cross cluster use
type App struct {
	seedData *config.SeedData
	status   AppStatus
}

// GetStatus retrieves the current status of the App
func (app *App) GetStatus() AppStatus {
	return app.status
}

// GetSeedData retrieves the current seed data config of the App. In NonInitialized status it can be nil
func (app *App) GetSeedData() *config.SeedData {
	return app.seedData
}

// NewApp initializes a new App instance
func NewApp(seedData *config.SeedData, status AppStatus) *App {
	return &App{seedData: seedData, status: status}
}
