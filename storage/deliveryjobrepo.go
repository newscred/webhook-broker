package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/imyousuf/webhook-broker/storage/data"
)

const (
	jobPropertyCount = 9
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

// MarkJobInflight sets the status of the job to Inflight if job's current state in the object and DB is Queued; else returns error
func (djRepo *DeliveryJobDBRepository) MarkJobInflight(deliveryJob *data.DeliveryJob) (err error) {
	return err
}

// MarkJobDelivered sets the status of the job to Delivered if the job's current status is Inflight in the object and DB; else returns error
func (djRepo *DeliveryJobDBRepository) MarkJobDelivered(deliveryJob *data.DeliveryJob) (err error) {
	return err
}

// MarkJobDead sets the status of the job to Dead if the job's current status is Inflight in the object and DB; else returns error
func (djRepo *DeliveryJobDBRepository) MarkJobDead(deliveryJob *data.DeliveryJob) (err error) {
	return err
}

// MarkJobRetry increases the retry attempt count and sets the status of the job to Queued if the job's current status is Inflight in the object and DB; else returns error
func (djRepo *DeliveryJobDBRepository) MarkJobRetry(deliveryJob *data.DeliveryJob, earliestDelta time.Duration) (err error) {
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
	baseQuery := "SELECT id, consumerId, status, dispatchReceivedAt, retryAttemptCount, statusChangedAt, earliestNextAttemptAt, createdAt, updatedAt FROM job WHERE messageId like $1" + getPaginationQueryFragmentWithConfigurablePageSize(page, true, largePageSizeWithOrder)
	scanArgs := func() []interface{} {
		job := &data.DeliveryJob{}
		job.Message = message
		job.Listener = &data.Consumer{}
		jobs = append(jobs, job)
		return []interface{}{&job.ID, &job.Listener.ID, &job.Status, &job.DispatchReceivedAt, &job.RetryAttemptCount, &job.StatusChangedAt, &job.EarliestNextAttemptAt, &job.CreatedAt, &job.UpdatedAt}
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

// NewDeliveryJobRepository creates a new instance of DeliveryJobRepository
func NewDeliveryJobRepository(db *sql.DB, msgRepo MessageRepository, consumerRepo ConsumerRepository) DeliveryJobRepository {
	return &DeliveryJobDBRepository{db: db, mesageRepository: msgRepo, consumerRepository: consumerRepo}
}
