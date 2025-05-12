package scheduler

import (
	"github.com/google/wire"
)

var (
	// SchedulerInjector is the injector for the Scheduler module
	SchedulerInjector = wire.NewSet(NewSchedulerConfiguration, NewMessageScheduler)
)
