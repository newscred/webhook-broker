//+build wireinject

package main

import (
	"github.com/google/wire"
)

// InitializeDBEngineName Creates DBEngineName
func InitializeDBEngineName() DBEngineName {
	wire.Build(NewDBEngineName)
	return "Test"
}

// InitializeJobQueue creates the Job queue for dispatching to workers
func InitializeJobQueue() chan Job {
	wire.Build(NewMaxQueuesConfig, NewJobQueue)
	return make(chan Job)
}

// InitializeDispatcher creates Dispatcher to handle messages
func InitializeDispatcher() *Dispatcher {
	wire.Build(NewDispatcher, NewMaxWorkersConfig, NewMaxQueuesConfig, NewJobQueue, NewJobPriorityQueue, NewPriorityDispatcherSwitch)
	return &Dispatcher{}
}
