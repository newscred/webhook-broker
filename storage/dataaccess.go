package storage

import (
	"context"
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
	MarkJobInflight(deliveryJob *data.DeliveryJob) error
	MarkJobDelivered(deliveryJob *data.DeliveryJob) error
	MarkJobDead(deliveryJob *data.DeliveryJob) error
	MarkJobRetry(deliveryJob *data.DeliveryJob, earliestDelta time.Duration) error
	MarkQueuedJobAsDead(deliveryJob *data.DeliveryJob) error
	MarkDeadJobAsInflight(deliveryJob *data.DeliveryJob) error
	RequeueDeadJobsForConsumer(consumer *data.Consumer) error
	GetJobsForMessage(message *data.Message, page *data.Pagination) ([]*data.DeliveryJob, *data.Pagination, error)
	GetJobsForConsumer(consumer *data.Consumer, jobStatus data.JobStatus, page *data.Pagination) ([]*data.DeliveryJob, *data.Pagination, error)
	GetPrioritizedJobsForConsumer(consumer *data.Consumer, jobStatus data.JobStatus, pageSize int) ([]*data.DeliveryJob, error)
	GetByID(id string) (*data.DeliveryJob, error)
	GetJobsInflightSince(delta time.Duration) []*data.DeliveryJob
	GetJobsReadyForInflightSince(delta time.Duration) []*data.DeliveryJob
	DeleteJobsForMessage(message *data.Message) error
	GetJobStatusCountsGroupedByConsumer() (map[Channel_ID]map[Consumer_ID][]*data.StatusCount[data.JobStatus], error)
	RequeueDeadJob(job *data.DeliveryJob) (err error)
}

// LockRepository allows storage operations over Lock
type LockRepository interface {
	TryLock(lock *data.Lock) error
	ReleaseLock(lock *data.Lock) error
	TimeoutLocks(threshold time.Duration) error
}
