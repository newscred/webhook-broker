package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/imyousuf/webhook-broker/storage/data"
)

const (
	jobPropertyCount     = 9
	jobCommonSelectQuery = "SELECT id, messageId, consumerId, status, dispatchReceivedAt, retryAttemptCount, statusChangedAt, earliestNextAttemptAt, createdAt, updatedAt FROM job WHERE"
)

// DeliveryJobDBRepository is the DeliveryJobRepository's RDBMS implementation
type DeliveryJobDBRepository struct {
	db                 *sql.DB
	mesageRepository   MessageRepository
	consumerRepository ConsumerRepository
}

// DispatchMessage saves the delivery jobs and updates the message status in one atomic state
func (djRepo *DeliveryJobDBRepository) DispatchMessage(message *data.Message, deliveryJobs ...*data.DeliveryJob) (err error) {
	if message == nil || !message.IsInValidState() {
		err = ErrInvalidStateToSave
	}
	args := make([]interface{}, 0, len(deliveryJobs)*jobPropertyCount)
	query := "INSERT INTO job (id, messageId, consumerId, dispatchReceivedAt, statusChangedAt, earliestNextAttemptAt, status, createdAt, updatedAt) VALUES"
	if err == nil {
		for _, job := range deliveryJobs {
			if job == nil || !job.IsInValidState() || job.Message.ID != message.ID {
				err = ErrInvalidStateToSave
				break
			}
			args = append(args, job.ID, job.Message.ID, job.Listener.ID, job.DispatchReceivedAt, job.StatusChangedAt, job.EarliestNextAttemptAt, job.Status, job.CreatedAt, job.UpdatedAt)
			query = query + " (?, ?, ?, ?, ?, ?, ?, ?, ?),"
		}
	}
	if err == nil {
		query = query[:len(query)-1]
		err = transactionalWrites(djRepo.db, func(tx *sql.Tx) error {
			return inTransactionExec(tx, emptyOps, query, args2SliceFnWrapper(args...), int64(len(deliveryJobs)))
		}, func(tx *sql.Tx) error {
			return djRepo.mesageRepository.SetDispatched(context.WithValue(context.Background(), txContextKey, tx), message)
		})
	}
	return err
}

func (djRepo *DeliveryJobDBRepository) updateJobStatus(deliveryJob *data.DeliveryJob, from data.JobStatus, to data.JobStatus) (err error) {
	currentTime := time.Now()
	err = transactionalSingleRowWriteExec(djRepo.db, emptyOps, "UPDATE job SET status = ?, statusChangedAt = ?, updatedAt = ? WHERE id like ? and status = ?", args2SliceFnWrapper(to, currentTime, currentTime, deliveryJob.ID, from))
	if err == nil {
		deliveryJob.Status = to
		deliveryJob.StatusChangedAt = currentTime
		deliveryJob.UpdatedAt = currentTime
	}
	return err
}

// MarkJobInflight sets the status of the job to Inflight if job's current state in the object and DB is Queued; else returns error
func (djRepo *DeliveryJobDBRepository) MarkJobInflight(deliveryJob *data.DeliveryJob) error {
	return djRepo.updateJobStatus(deliveryJob, data.JobQueued, data.JobInflight)
}

// MarkJobDelivered sets the status of the job to Delivered if the job's current status is Inflight in the object and DB; else returns error
func (djRepo *DeliveryJobDBRepository) MarkJobDelivered(deliveryJob *data.DeliveryJob) error {
	return djRepo.updateJobStatus(deliveryJob, data.JobInflight, data.JobDelivered)
}

// MarkJobDead sets the status of the job to Dead if the job's current status is Inflight in the object and DB; else returns error
func (djRepo *DeliveryJobDBRepository) MarkJobDead(deliveryJob *data.DeliveryJob) error {
	return djRepo.updateJobStatus(deliveryJob, data.JobInflight, data.JobDead)
}

// MarkJobRetry increases the retry attempt count and sets the status of the job to Queued if the job's current status is Inflight in the object and DB; else returns error
func (djRepo *DeliveryJobDBRepository) MarkJobRetry(deliveryJob *data.DeliveryJob, earliestDelta time.Duration) (err error) {
	currentTime := time.Now()
	nextTime := currentTime.Add(earliestDelta)
	err = transactionalSingleRowWriteExec(djRepo.db, emptyOps, "UPDATE job SET status = ?, statusChangedAt = ?, updatedAt = ?, earliestNextAttemptAt = ?, retryAttemptCount = ? WHERE id like ? and status = ?", args2SliceFnWrapper(data.JobQueued, currentTime, currentTime, nextTime, deliveryJob.RetryAttemptCount+1, deliveryJob.ID, data.JobInflight))
	if err == nil {
		deliveryJob.Status = data.JobQueued
		deliveryJob.StatusChangedAt = currentTime
		deliveryJob.UpdatedAt = currentTime
		deliveryJob.EarliestNextAttemptAt = nextTime
		deliveryJob.RetryAttemptCount = deliveryJob.RetryAttemptCount + 1
	}
	return err
}

func (djRepo *DeliveryJobDBRepository) getJobs(baseQuery string, message *data.Message, consumer *data.Consumer, args []interface{}) (jobs []*data.DeliveryJob, pagination *data.Pagination, err error) {
	jobs = make([]*data.DeliveryJob, 0)
	pagination = &data.Pagination{}
	scanArgs := func() []interface{} {
		job := &data.DeliveryJob{}
		job.Message = &data.Message{}
		job.Listener = &data.Consumer{}
		jobs = append(jobs, job)
		return []interface{}{&job.ID, &job.Message.ID, &job.Listener.ID, &job.Status, &job.DispatchReceivedAt, &job.RetryAttemptCount, &job.StatusChangedAt, &job.EarliestNextAttemptAt, &job.CreatedAt, &job.UpdatedAt}
	}
	err = queryRows(djRepo.db, baseQuery, args2SliceFnWrapper(args...), scanArgs)
	if err == nil {
		for _, job := range jobs {
			if consumer == nil {
				job.Listener, _ = djRepo.consumerRepository.GetByID(job.Listener.ID.String())
			} else {
				job.Listener = consumer
			}
			if message == nil {
				job.Message, _ = djRepo.mesageRepository.GetByID(job.Message.ID.String())
			} else {
				job.Message = message
			}
		}
	}
	if err == nil {
		jobCount := len(jobs)
		if jobCount > 0 {
			pagination = data.NewPagination(jobs[jobCount-1], jobs[0])
		}
	}
	return jobs, pagination, err
}

func (djRepo *DeliveryJobDBRepository) getJobsForStatusAndDelta(status data.JobStatus, delta time.Duration, useStatusChangedAt bool) []*data.DeliveryJob {
	jobs := make([]*data.DeliveryJob, 0)
	page := data.NewPagination(nil, nil)
	if delta > 0 {
		delta = -1 * delta
	}
	more := true
	dateCol := "earliestNextAttemptAt"
	if useStatusChangedAt {
		dateCol = "statusChangedAt"
	}
	for more {
		baseQuery := jobCommonSelectQuery + " status like ? AND " + dateCol + " <= ?" + getPaginationQueryFragmentWithConfigurablePageSize(page, true, largePageSizeWithOrder)
		pageJobs, pagination, err := djRepo.getJobs(baseQuery, nil, nil, appendWithPaginationArgs(page, status, time.Now().Add(delta)))
		if err == nil {
			jobs = append(jobs, pageJobs...)
			jobCount := len(pageJobs)
			if jobCount > 0 {
				page.Next = pagination.Next
			} else {
				more = false
			}
		} else {
			log.Error().Err(err).Msg(fmt.Sprint("error - could get list jobs (status, use status changed at date field) ", status, " ", useStatusChangedAt))
			more = false
		}
	}
	return jobs
}

func getDefaultErrorResponseForJobs() ([]*data.DeliveryJob, *data.Pagination, error) {
	return make([]*data.DeliveryJob, 0), &data.Pagination{}, ErrPaginationDeadlock
}

// GetJobsForMessage retrieves jobs created for a specific message
func (djRepo *DeliveryJobDBRepository) GetJobsForMessage(message *data.Message, page *data.Pagination) ([]*data.DeliveryJob, *data.Pagination, error) {
	if page == nil || (page.Next != nil && page.Previous != nil) {
		return getDefaultErrorResponseForJobs()
	}
	baseQuery := jobCommonSelectQuery + " messageId like ?" + getPaginationQueryFragmentWithConfigurablePageSize(page, true, largePageSizeWithOrder)
	return djRepo.getJobs(baseQuery, message, nil, appendWithPaginationArgs(page, message.ID.String()))
}

// RequeueDeadJobsForConsumer queues up dead jobs for a specific consumer
func (djRepo *DeliveryJobDBRepository) RequeueDeadJobsForConsumer(consumer *data.Consumer) (err error) {
	currentTime := time.Now()
	err = transactionalWrites(djRepo.db, func(tx *sql.Tx) error {
		return inTransactionExec(tx, emptyOps, "UPDATE job SET status = ?, statusChangedAt = ?, updatedAt = ? WHERE consumerId like ? and status = ?", args2SliceFnWrapper(data.JobQueued, currentTime, currentTime, consumer.ID, data.JobDead), 0)
	})
	return err
}

// GetJobsForConsumer retrieves DeliveryJob created for delivery to a customer and it has to be filtered by a specific status
func (djRepo *DeliveryJobDBRepository) GetJobsForConsumer(consumer *data.Consumer, jobStatus data.JobStatus, page *data.Pagination) ([]*data.DeliveryJob, *data.Pagination, error) {
	if page == nil || (page.Next != nil && page.Previous != nil) {
		return getDefaultErrorResponseForJobs()
	}
	baseQuery := jobCommonSelectQuery + " consumerId like ? AND status = ?" + getPaginationQueryFragmentWithConfigurablePageSize(page, true, pageSizeWithOrder)
	return djRepo.getJobs(baseQuery, nil, consumer, appendWithPaginationArgs(page, consumer.ID.String(), jobStatus))
}

// GetJobsInflightSince retrieves jobs in inflight status since the delta duration
func (djRepo *DeliveryJobDBRepository) GetJobsInflightSince(delta time.Duration) []*data.DeliveryJob {
	return djRepo.getJobsForStatusAndDelta(data.JobInflight, delta, true)
}

// GetJobsReadyForInflightSince retrieves jobs in queued status and earliestNextAttemptAt < `now`-delta
func (djRepo *DeliveryJobDBRepository) GetJobsReadyForInflightSince(delta time.Duration) []*data.DeliveryJob {
	return djRepo.getJobsForStatusAndDelta(data.JobQueued, delta, false)
}

// GetByID loads the delivery job with specified id if it exists, else returns an error
func (djRepo *DeliveryJobDBRepository) GetByID(id string) (job *data.DeliveryJob, err error) {
	job = &data.DeliveryJob{}
	var messageID string
	var consumerID string
	err = querySingleRow(djRepo.db, jobCommonSelectQuery+" id like ?", args2SliceFnWrapper(id),
		args2SliceFnWrapper(&job.ID, &messageID, &consumerID, &job.Status, &job.DispatchReceivedAt, &job.RetryAttemptCount, &job.StatusChangedAt,
			&job.EarliestNextAttemptAt, &job.CreatedAt, &job.UpdatedAt))
	if err == nil {
		job.Message, err = djRepo.mesageRepository.GetByID(messageID)
	}
	if err == nil {
		job.Listener, err = djRepo.consumerRepository.GetByID(consumerID)
	}
	return job, err
}

// NewDeliveryJobRepository creates a new instance of DeliveryJobRepository
func NewDeliveryJobRepository(db *sql.DB, msgRepo MessageRepository, consumerRepo ConsumerRepository) DeliveryJobRepository {
	return &DeliveryJobDBRepository{db: db, mesageRepository: msgRepo, consumerRepository: consumerRepo}
}
