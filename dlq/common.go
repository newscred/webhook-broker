package dlq

import (
	"github.com/google/wire"
)

var (
	// DLQInjector is the injector for the DLQ module
	DLQInjector = wire.NewSet(NewDLQSummaryUpdaterConfiguration, NewDLQSummaryUpdater)
)

// Generated with assistance from Claude AI
