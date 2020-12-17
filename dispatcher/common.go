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
	Data     *data.DeliveryJob
	Priority uint
}

// NewJob returns a new instance of Job. Only call this method if Job.IsInValidState() is true, else can result a panic
func NewJob(job *data.DeliveryJob) *Job {
	return &Job{Data: job, Priority: job.Message.Priority}
}
