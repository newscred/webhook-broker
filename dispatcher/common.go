package dispatcher

import (
	"github.com/google/wire"
	"github.com/imyousuf/webhook-broker/storage/data"
)

var (
	// DispatcherInjector is the injector for the dispatcher project
	DispatcherInjector = wire.NewSet(NewMessageDispatcher)
)

// Job represents the job to be run
type Job struct {
	Data     *data.Message
	Priority int
}
