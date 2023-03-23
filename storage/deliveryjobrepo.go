package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/newscred/webhook-broker/storage/data"
)

const (
	jobPropertyCount     = 11
	jobCommonSelectQuery = "SELECT id, messageId, consumerId, status, dispatchReceivedAt, retryAttemptCount, statusChangedAt, earliestNextAttemptAt, createdAt, updatedAt, priority, incrementalTimeout FROM job WHERE"
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
	query := "INSERT INTO job (id, messageId, consumerId, dispatchReceivedAt, statusChangedAt, earliestNextAttemptAt, status, createdAt, updatedAt, priority, incrementalTimeout) VALUES"
	if err == nil {
		for _, job := range deliveryJobs {
			if job == nil || !job.IsInValidState() || job.Message.ID != message.ID {
				err = ErrInvalidStateToSave
				break
			}
			args = append(args, job.ID, job.Message.ID, job.Listener.ID, job.DispatchReceivedAt, job.StatusChangedAt, job.EarliestNextAttemptAt, job.Status, job.CreatedAt, job.UpdatedAt, job.Priority, job.IncrementalTimeout)
			query = query + " (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?),"
		}
	}
	if err == nil {
		query = query[:len(query)-1]
		txs := make([]func(tx *sql.Tx) error, 0, 2)
		if len(deliveryJobs) > 0 {
			txs = append(txs, func(tx *sql.Tx) error {
				return inTransactionExec(tx, emptyOps, query, args2SliceFnWrapper(args...), int64(len(deliveryJobs)))
			})
		}
		txs = append(txs, func(tx *sql.Tx) error {
			return djRepo.mesageRepository.SetDispatched(context.WithValue(context.Background(), txContextKey, tx), message)
		})
		err = transactionalWrites(djRepo.db, txs...)
	}
	return err
}

func (djRepo *DeliveryJobDBRepository) updateJobStatus(deliveryJob *data.DeliveryJob, from data.JobStatus, to data.JobStatus) (err error) {
	currentTime := time.Now()
	err = transactionalSingleRowWriteExec(djRepo.db, emptyOps, "UPDATE job SET status = ?, incrementalTimeout = ?, statusChangedAt = ?, updatedAt = ? WHERE id like ? and status = ?", args2SliceFnWrapper(to, deliveryJob.IncrementalTimeout, currentTime, currentTime, deliveryJob.ID, from))
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

func (djRepo *DeliveryJobDBRepository) MarkQueuedJobAsDead(deliveryJob *data.DeliveryJob) error {
	return djRepo.updateJobStatus(deliveryJob, data.JobQueued, data.JobDead)
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

// MarkDeadJobAsInflight increases the retry attempt count and sets the status of the job to Queued if the job's current status is Dead in the object and DB; else returns error
func (djRepo *DeliveryJobDBRepository) MarkDeadJobAsInflight(deliveryJob *data.DeliveryJob) (err error) {
	currentTime := time.Now()
	err = transactionalSingleRowWriteExec(djRepo.db, emptyOps, "UPDATE job SET status = ?, incrementalTimeout = ?, statusChangedAt = ?, updatedAt = ?, earliestNextAttemptAt = ?, retryAttemptCount = ? WHERE id like ? and status = ?", args2SliceFnWrapper(data.JobInflight, deliveryJob.IncrementalTimeout, currentTime, currentTime, currentTime, deliveryJob.RetryAttemptCount+1, deliveryJob.ID, data.JobDead))
	if err == nil {
		deliveryJob.Status = data.JobInflight
		deliveryJob.StatusChangedAt = currentTime
		deliveryJob.UpdatedAt = currentTime
		deliveryJob.EarliestNextAttemptAt = currentTime
		deliveryJob.RetryAttemptCount = deliveryJob.RetryAttemptCount + 1
	}
	return err
}

func (djRepo *DeliveryJobDBRepository) populateJobsWithMessage(jobs []*data.DeliveryJob) error {
	messageIDs := make([]string, 0, len(jobs))
	for _, job := range jobs {
		messageIDs = append(messageIDs, job.Message.ID.String())
	}
	messages, err := djRepo.mesageRepository.GetByIDs(messageIDs)
	if err == nil {
		messageMap := make(map[string]*data.Message)
		for _, message := range messages {
			messageMap[message.ID.String()] = message
		}
		for _, job := range jobs {
			job.Message = messageMap[job.Message.ID.String()]
		}
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
		return []interface{}{&job.ID, &job.Message.ID, &job.Listener.ID, &job.Status, &job.DispatchReceivedAt, &job.RetryAttemptCount, &job.StatusChangedAt, &job.EarliestNextAttemptAt, &job.CreatedAt, &job.UpdatedAt, &job.Priority, &job.IncrementalTimeout}
	}
	err = queryRows(djRepo.db, baseQuery, args2SliceFnWrapper(args...), scanArgs)
	if err == nil {
		for _, job := range jobs {
			if consumer == nil {
				job.Listener, _ = djRepo.consumerRepository.GetByID(job.Listener.ID.String())
			} else {
				job.Listener = consumer
			}
			if message != nil {
				job.Message = message
			}
		}
		if message == nil {
			err = djRepo.populateJobsWithMessage(jobs)
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
		baseQuery := jobCommonSelectQuery + " status = ? AND " + dateCol + " <= ?" + getPaginationQueryFragmentWithConfigurablePageSize(page, true, largePageSizeWithOrder)
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
		return inTransactionExec(tx, emptyOps, "UPDATE job SET status = ?, statusChangedAt = ?, updatedAt = ?, retryAttemptCount = ? WHERE consumerId like ? and status = ?", args2SliceFnWrapper(data.JobQueued, currentTime, currentTime, 0, consumer.ID, data.JobDead), 0)
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

// GetPrioritizedJobsForConsumer retrieves DeliveryJob created for delivery to a customer and it has to be filtered by a specific status and ordered by message priority
func (djRepo *DeliveryJobDBRepository) GetPrioritizedJobsForConsumer(consumer *data.Consumer, jobStatus data.JobStatus, pageSize int) ([]*data.DeliveryJob, error) {
	orderClause := "ORDER BY priority DESC, createdAt DESC, id DESC"
	baseQuery := jobCommonSelectQuery + " consumerId like ? AND status = ? " + orderClause + " LIMIT ?"
	jobs, _, err := djRepo.getJobs(baseQuery, nil, consumer, args2SliceFnWrapper(consumer.ID.String(), jobStatus, pageSize)())
	return jobs, err
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
			&job.EarliestNextAttemptAt, &job.CreatedAt, &job.UpdatedAt, &job.Priority, &job.IncrementalTimeout))
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
