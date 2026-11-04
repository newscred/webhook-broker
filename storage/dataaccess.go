package storage

import (
	"context"
	"database/sql"
	"time"

	"github.com/newscred/webhook-broker/config"
	"github.com/newscred/webhook-broker/storage/data"
)

// DataAccessor is the facade to all the data repository
type DataAccessor interface {
	GetAppRepository() AppRepository
	GetProducerRepository() ProducerRepository
	GetChannelRepository() ChannelRepository
	GetConsumerRepository() ConsumerRepository
	GetMessageRepository() MessageRepository
	GetDeliveryJobRepository() DeliveryJobRepository
	GetLockRepository() LockRepository
	GetScheduledMessageRepository() ScheduledMessageRepository
	GetDLQSummaryRepository() DLQSummaryRepository
	GetDispatchPauseRepository() DispatchPauseRepository
	GetJobParkingRepository() JobParkingRepository
	Close()
}

// AppRepository allows storage operation interaction for App
type AppRepository interface {
	GetApp() (*data.App, error)
	StartAppInit(data *config.SeedData) error
	CompleteAppInit() error
}

// ProducerRepository allows storage operation interaction for Producer
type ProducerRepository interface {
	Store(producer *data.Producer) (*data.Producer, error)
	Get(producerID string) (*data.Producer, error)
	GetList(page *data.Pagination) ([]*data.Producer, *data.Pagination, error)
}

// ChannelRepository allows storage operation interaction for Channel
type ChannelRepository interface {
	Store(channel *data.Channel) (*data.Channel, error)
	Get(channelID string) (*data.Channel, error)
	GetList(page *data.Pagination) ([]*data.Channel, *data.Pagination, error)
}

// ConsumerRepository allows storage operation interaction for Consumer
type ConsumerRepository interface {
	Store(consumer *data.Consumer) (*data.Consumer, error)
	Delete(consumer *data.Consumer) error
	Get(channelID string, consumerID string) (*data.Consumer, error)
	GetList(channelID string, page *data.Pagination) ([]*data.Consumer, *data.Pagination, error)
	GetByID(id string) (*data.Consumer, error)
}

// MessageRepository allows storage operations over Message. SetDispatched does not accept TX directly to keep the API storage class independent
type MessageRepository interface {
	Create(message *data.Message) error
	Get(channelID string, messageID string) (*data.Message, error)
	GetByID(id string) (*data.Message, error)
	GetByIDs(ids []string) ([]*data.Message, error)
	SetDispatched(txContext context.Context, message *data.Message) error
	GetMessagesNotDispatchedForCertainPeriod(delta time.Duration) []*data.Message
	GetMessagesForChannel(channelID string, page *data.Pagination, statusFilters ...data.MsgStatus) ([]*data.Message, *data.Pagination, error)
	GetMessagesFromBeforeDurationThatAreCompletelyDelivered(delta time.Duration, absoluteMaxMessages int) []*data.Message
	DeleteMessage(message *data.Message) error
	DeleteMessagesAndJobs(ctx context.Context, messageIDs []string) error
	GetMessageStatusCountsByChannel(channelID string) ([]*data.StatusCount[data.MsgStatus], error)
}

// DeliveryJobRepository allows storage operations over DeliveryJob
type DeliveryJobRepository interface {
	DispatchMessage(message *data.Message, deliveryJobs ...*data.DeliveryJob) error
	// DispatchMessageWithParking inserts jobs into job and parked jobs into job_parking and marks the message dispatched, all in one transaction
	DispatchMessageWithParking(message *data.Message, deliveryJobs []*data.DeliveryJob, parkedJobs []*data.DeliveryJob) error
	// MarkJobInflight moves the job from QUEUED to INFLIGHT only if its retry attempt count in the DB matches the object's, so an
	// out-of-date in-memory copy is refused, and only if its consumer is not effectively paused; else ErrNoRowsUpdated
	MarkJobInflight(deliveryJob *data.DeliveryJob) error
	MarkJobDelivered(deliveryJob *data.DeliveryJob) error
	MarkJobDead(deliveryJob *data.DeliveryJob) error
	MarkJobRetry(deliveryJob *data.DeliveryJob, earliestDelta time.Duration) error
	MarkQueuedJobAsDead(deliveryJob *data.DeliveryJob) error
	MarkDeadJobAsInflight(deliveryJob *data.DeliveryJob) error
	RequeueDeadJobsForConsumer(consumer *data.Consumer) (int64, error)
	GetJobsForMessage(message *data.Message, page *data.Pagination) ([]*data.DeliveryJob, *data.Pagination, error)
	GetJobsForMessages(messageIDs []string) (map[string][]*data.DeliveryJob, error)
	GetJobsForConsumer(consumer *data.Consumer, jobStatus data.JobStatus, page *data.Pagination) ([]*data.DeliveryJob, *data.Pagination, error)
	GetPrioritizedJobsForConsumer(consumer *data.Consumer, jobStatus data.JobStatus, pageSize int) ([]*data.DeliveryJob, error)
	GetByID(id string) (*data.DeliveryJob, error)
	GetJobsInflightSince(delta time.Duration) []*data.DeliveryJob
	GetJobsReadyForInflightSince(delta time.Duration, retryThreshold int) []*data.DeliveryJob
	DeleteJobsForMessage(message *data.Message) error
	GetJobStatusCountsGroupedByConsumer() (map[Channel_ID]map[Consumer_ID][]*data.StatusCount[data.JobStatus], error)
	RequeueDeadJob(job *data.DeliveryJob) (int64, error)
	DeleteDeadJobsForConsumer(consumer *data.Consumer, maxRetryCount uint) (int64, error)
	DeleteDeadJob(job *data.DeliveryJob, maxRetryCount uint) (int64, error)
	GetDeadJobCountsSinceCheckpoint(since time.Time) (map[string]int64, error)
	// GetQueuedJobsByIDs loads the jobs with the given ids that are currently QUEUED; unknown or non-QUEUED ids are skipped
	GetQueuedJobsByIDs(ids []string) ([]*data.DeliveryJob, error)
}

// DispatchPauseRepository allows storage operations over DispatchPause
type DispatchPauseRepository interface {
	// Pause stores the pause; it is idempotent and returns the existing pause if the scope is already paused
	Pause(pause *data.DispatchPause) (*data.DispatchPause, error)
	// Resume deletes the pause for the scope; returns true if a pause was deleted
	Resume(scope string) (bool, error)
	Get(scope string) (*data.DispatchPause, error)
	GetAll() ([]*data.DispatchPause, error)
	// IsEffectivelyPaused returns true if the consumer (internal id) or the whole broker is paused
	IsEffectivelyPaused(consumerInternalID string) (bool, error)
}

// JobParkingRepository allows storage operations over parked jobs (jobs of paused consumers)
type JobParkingRepository interface {
	// ReparkQueuedJob atomically moves a QUEUED job from job to job_parking only if its consumer is effectively paused; returns true if moved
	ReparkQueuedJob(job *data.DeliveryJob) (bool, error)
	// GetDrainableConsumerIDs returns internal ids of consumers that have parked jobs and are not effectively paused
	GetDrainableConsumerIDs() ([]string, error)
	// DrainPage atomically moves up to pageSize parked jobs of the consumer back into job as QUEUED, oldest first
	DrainPage(consumerInternalID string, pageSize int, earliestNextAttemptAt time.Time) ([]*data.DeliveryJob, error)
	// GetByID loads a parked job; its Status is data.JobParked
	GetByID(id string) (*data.DeliveryJob, error)
	GetForMessage(message *data.Message) ([]*data.DeliveryJob, error)
	GetCountsByConsumer() ([]*data.ParkedJobCount, error)
	// CountForConsumer returns the number of parked jobs of one consumer (internal id)
	CountForConsumer(consumerInternalID string) (int64, error)
}

// DLQSummaryRepository allows storage operations over DLQSummary
type DLQSummaryRepository interface {
	GetAll() ([]*data.DLQSummary, error)
	GetLastCheckedAt() (time.Time, error)
	UpsertCounts(summaries []*data.DLQSummary) error
	DecrementCount(consumerID string, count int64) error
	BootstrapCounts(db *sql.DB) error
}

// LockRepository allows storage operations over Lock
type LockRepository interface {
	TryLock(lock *data.Lock) error
	ReleaseLock(lock *data.Lock) error
	TimeoutLocks(threshold time.Duration) error
	// RenewLock refreshes attainedAt of a held lock; returns ErrNoRowsUpdated if the lock is no longer held
	RenewLock(lock *data.Lock) error
}

// ScheduledMessageRepository allows storage operations over ScheduledMessage
type ScheduledMessageRepository interface {
	Create(message *data.ScheduledMessage) error
	Get(channelID string, messageID string) (*data.ScheduledMessage, error)
	GetByID(id string) (*data.ScheduledMessage, error)
	MarkDispatched(message *data.ScheduledMessage) error
	GetMessagesReadyForDispatch(limit int) []*data.ScheduledMessage
	GetScheduledMessagesForChannel(channelID string, page *data.Pagination, statusFilters ...data.ScheduledMsgStatus) ([]*data.ScheduledMessage, *data.Pagination, error)
	GetScheduledMessageStatusCountsByChannel(channelID string) ([]*data.StatusCount[data.ScheduledMsgStatus], error)
	GetNextScheduledMessageTime(channelID string) (*time.Time, error)
}
