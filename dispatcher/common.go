package dispatcher

import "github.com/google/wire"

var (
	// DispatcherInjector is the injector for the dispatcher project
	DispatcherInjector = wire.NewSet(NewMessageDispatcher)
)
