package dispatcher

import (
	"github.com/google/wire"
	"github.com/newscred/webhook-broker/storage/data"
)

var (
	// DispatcherInjector is the injector for the Dispatcher module
	DispatcherInjector = wire.NewSet(NewMessageDispatcher, NewPauseRegistry, wire.Struct(new(Configuration), "DeliveryJobRepo", "ConsumerRepo", "LockRepo", "BrokerConfig", "ConsumerConnectionConfig", "MsgRepo", "MetricsCollector", "PauseRegistry", "JobParkingRepo"))
)

// Job represents the job to be run
type Job struct {
	Data     *data.DeliveryJob
	Priority uint
	// seq is the order the job entered the dispatcher; it breaks ties between equal priorities.
	seq uint64
}

// NewJob returns a new instance of Job. Only call this method if Job.IsInValidState() is true, else can result a panic
func NewJob(job *data.DeliveryJob) *Job {
	return &Job{Data: job, Priority: job.Priority}
}
