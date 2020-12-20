package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/imyousuf/webhook-broker/storage/data"
)

const (
	jobPropertyCount  = 9
	commonSelectQuery = "SELECT id, messageId, consumerId, status, dispatchReceivedAt, retryAttemptCount, statusChangedAt, earliestNextAttemptAt, createdAt, updatedAt FROM job WHERE"
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
		for index, job := range deliveryJobs {
			if job == nil || !job.IsInValidState() || job.Message.ID != message.ID {
				err = ErrInvalidStateToSave
				break
			}
			args = append(args, job.ID, job.Message.ID, job.Listener.ID, job.DispatchReceivedAt, job.StatusChangedAt, job.EarliestNextAttemptAt, job.Status, job.CreatedAt, job.UpdatedAt)
			query = query + fmt.Sprintf(" ($%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d),", index*jobPropertyCount+1, index*jobPropertyCount+2, index*jobPropertyCount+3, index*jobPropertyCount+4, index*jobPropertyCount+5, index*jobPropertyCount+6, index*jobPropertyCount+7, index*jobPropertyCount+8, index*jobPropertyCount+9)
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
	err = transactionalSingleRowWriteExec(djRepo.db, emptyOps, "UPDATE job SET status = $1, statusChangedAt = $2, updatedAt = $3 WHERE id like $4 and status like $5", args2SliceFnWrapper(to, currentTime, currentTime, deliveryJob.ID, from))
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
	err = transactionalSingleRowWriteExec(djRepo.db, emptyOps, "UPDATE job SET status = $1, statusChangedAt = $2, updatedAt = $3, earliestNextAttemptAt = $4, retryAttemptCount = $5 WHERE id like $6 and status like $7", args2SliceFnWrapper(data.JobQueued, currentTime, currentTime, nextTime, deliveryJob.RetryAttemptCount+1, deliveryJob.ID, data.JobInflight))
	if err == nil {
		deliveryJob.Status = data.JobQueued
		deliveryJob.StatusChangedAt = currentTime
		deliveryJob.UpdatedAt = currentTime
		deliveryJob.EarliestNextAttemptAt = nextTime
		deliveryJob.RetryAttemptCount = deliveryJob.RetryAttemptCount + 1
	}
	return err
}

// GetJobsForMessage retrieves jobs created for a specific message
func (djRepo *DeliveryJobDBRepository) GetJobsForMessage(message *data.Message, page *data.Pagination) ([]*data.DeliveryJob, *data.Pagination, error) {
	jobs := make([]*data.DeliveryJob, 0)
	pagination := &data.Pagination{}
	if page == nil || (page.Next != nil && page.Previous != nil) {
		return jobs, pagination, ErrPaginationDeadlock
	}
	var err error
	baseQuery := commonSelectQuery + " messageId like $1" + getPaginationQueryFragmentWithConfigurablePageSize(page, true, largePageSizeWithOrder)
	scanArgs := func() []interface{} {
		job := &data.DeliveryJob{}
		job.Message = message
		job.Listener = &data.Consumer{}
		jobs = append(jobs, job)
		var messageID string
		return []interface{}{&job.ID, &messageID, &job.Listener.ID, &job.Status, &job.DispatchReceivedAt, &job.RetryAttemptCount, &job.StatusChangedAt, &job.EarliestNextAttemptAt, &job.CreatedAt, &job.UpdatedAt}
	}
	err = queryRows(djRepo.db, baseQuery, args2SliceFnWrapper(message.ID.String()), scanArgs)
	if err == nil {
		for _, job := range jobs {
			job.Listener, _ = djRepo.consumerRepository.GetByID(job.Listener.ID.String())
		}
		jobCount := len(jobs)
		if jobCount > 0 {
			pagination = data.NewPagination(jobs[jobCount-1], jobs[0])
		}
	}
	return jobs, pagination, err
}

// GetByID loads the delivery job with specified id if it exists, else returns an error
func (djRepo *DeliveryJobDBRepository) GetByID(id string) (job *data.DeliveryJob, err error) {
	job = &data.DeliveryJob{}
	var messageID string
	var consumerID string
	err = querySingleRow(djRepo.db, commonSelectQuery+" id like $1", args2SliceFnWrapper(id),
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
