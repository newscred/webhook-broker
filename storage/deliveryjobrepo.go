package storage

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/newscred/webhook-broker/storage/data"
)

const (
	jobPropertyCount     = 11
	jobCommonProjection  = "SELECT id, messageId, consumerId, status, dispatchReceivedAt, retryAttemptCount, statusChangedAt, earliestNextAttemptAt, createdAt, updatedAt, priority, incrementalTimeout"
	jobCommonSelectQuery = jobCommonProjection + " FROM job WHERE"
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

func (djRepo *DeliveryJobDBRepository) DeleteJobsForMessage(message *data.Message) error {
	err := transactionalWrites(djRepo.db, func(tx *sql.Tx) error {
		return inTransactionExec(tx, emptyOps, "DELETE FROM job WHERE messageId like ?", args2SliceFnWrapper(message.ID), 0)
	})
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
	return djRepo.getJobsForStatusAndDeltaWithCustomQuery(status, delta, useStatusChangedAt, jobCommonSelectQuery, "")
}

func (djRepo *DeliveryJobDBRepository) getJobsForStatusAndDeltaWithCustomQuery(status data.JobStatus, delta time.Duration, useStatusChangedAt bool, jobQueryBase string, jobAlias string) []*data.DeliveryJob {
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
	orderBy := largePageSizeWithOrder
	aliasPrefix := ""
	if len(jobAlias) > 0 {
		orderBy = getOrderByClauseWithAlias(jobAlias, LIMIT_100_SUFFIX)
		aliasPrefix = jobAlias + "."
	}
	commonBaseQuery := jobQueryBase + fmt.Sprintf(" %sstatus = ? AND  %s%s <= ?", aliasPrefix, aliasPrefix, dateCol)
	log.Debug().Msgf("JobsForStatusAndDeltaWithCustomQuery for status %s common query: %s", status, commonBaseQuery)
	for more {
		baseQuery := commonBaseQuery + getPaginationQueryFragmentWithConfigurablePageSizeWithAlias(page, true, orderBy, jobAlias)
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

// RequeueDeadJobsForConsumer queues up dead jobs for a specific consumer; returns rows affected
func (djRepo *DeliveryJobDBRepository) RequeueDeadJobsForConsumer(consumer *data.Consumer) (int64, error) {
	currentTime := time.Now()
	var rowsAffected int64
	err := transactionalWrites(djRepo.db, func(tx *sql.Tx) error {
		result, execErr := tx.Exec("UPDATE job SET status = ?, statusChangedAt = ?, updatedAt = ?, retryAttemptCount = ? WHERE consumerId like ? and status = ?",
			data.JobQueued, currentTime, currentTime, 0, consumer.ID, data.JobDead)
		if execErr != nil {
			return execErr
		}
		rowsAffected, execErr = result.RowsAffected()
		return execErr
	})
	return rowsAffected, err
}

// RequeueDeadJob queues up a dead job; returns rows affected
func (djRepo *DeliveryJobDBRepository) RequeueDeadJob(job *data.DeliveryJob) (int64, error) {
	currentTime := time.Now()
	var rowsAffected int64
	err := transactionalWrites(djRepo.db, func(tx *sql.Tx) error {
		result, execErr := tx.Exec("UPDATE job SET status = ?, statusChangedAt = ?, updatedAt = ?, retryAttemptCount = ? WHERE id like ? and status = ?",
			data.JobQueued, currentTime, currentTime, 0, job.ID, data.JobDead)
		if execErr != nil {
			return execErr
		}
		rowsAffected, execErr = result.RowsAffected()
		if execErr == nil && rowsAffected == 0 {
			return ErrNoRowsUpdated
		}
		return execErr
	})
	return rowsAffected, err
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
func (djRepo *DeliveryJobDBRepository) GetJobsReadyForInflightSince(delta time.Duration, retryThreshold int) []*data.DeliveryJob {
	query := fmt.Sprintf(`%s (retryAttemptCount >= %d OR consumerId NOT IN (SELECT id FROM consumer WHERE type = %d)) AND`,
		jobCommonSelectQuery, retryThreshold, data.PullConsumer)
	return djRepo.getJobsForStatusAndDeltaWithCustomQuery(data.JobQueued, delta, false, query, "")
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

type Channel_ID string
type Consumer_ID string

func (djRepo *DeliveryJobDBRepository) GetJobStatusCountsGroupedByConsumer() (map[Channel_ID]map[Consumer_ID][]*data.StatusCount[data.JobStatus], error) {
	type statusRow struct {
		channelId  string
		consumerId string
		status     data.JobStatus
		count      int
		oldest     string
		newest     string
	}
	rows := make([]*statusRow, 0)
	query := `SELECT c.channelId, j.consumerId, j.status, count(j.id), min(j.statusChangedAt), max(j.statusChangedAt)
FROM job j JOIN consumer c on j.consumerId = c.id
GROUP BY c.channelId, j.consumerId, j.status`
	scanStatusCount := func() []interface{} {
		statusCount := &statusRow{}
		rows = append(rows, statusCount)
		return []interface{}{&statusCount.channelId, &statusCount.consumerId, &statusCount.status, &statusCount.count, &statusCount.oldest, &statusCount.newest}
	}
	err := queryRows(djRepo.db, query, nilArgs, scanStatusCount)
	log.Info().Msg("Query: " + query + "; Status rows: " + strconv.Itoa(len(rows)))
	result := make(map[Channel_ID]map[Consumer_ID][]*data.StatusCount[data.JobStatus])
	if err == nil {
		for _, row := range rows {
			channelID := Channel_ID(row.channelId)
			consumerID := Consumer_ID(row.consumerId)
			if _, ok := result[channelID]; !ok {
				result[channelID] = make(map[Consumer_ID][]*data.StatusCount[data.JobStatus])
			}
			if _, ok := result[channelID][consumerID]; !ok {
				result[channelID][consumerID] = make([]*data.StatusCount[data.JobStatus], 0)
			}
			result[channelID][consumerID] = append(result[channelID][consumerID], &data.StatusCount[data.JobStatus]{
				Status:              row.status,
				Count:               row.count,
				OldestItemTimestamp: row.oldest,
				NewestItemTimestamp: row.newest,
			})
		}
	}
	// The following log is for local dev/testing only
	// log.Info().Msg("Result: " + fmt.Sprint(result))
	return result, err
}

// DeleteDeadJobsForConsumer deletes dead jobs for a consumer where retry is exhausted; returns rows affected
func (djRepo *DeliveryJobDBRepository) DeleteDeadJobsForConsumer(consumer *data.Consumer, maxRetryCount uint) (int64, error) {
	var rowsAffected int64
	err := transactionalWrites(djRepo.db, func(tx *sql.Tx) error {
		result, execErr := tx.Exec("DELETE FROM job WHERE consumerId = ? AND status = ? AND retryAttemptCount >= ?",
			consumer.ID, data.JobDead, maxRetryCount)
		if execErr != nil {
			return execErr
		}
		rowsAffected, execErr = result.RowsAffected()
		return execErr
	})
	return rowsAffected, err
}

// DeleteDeadJob deletes a single dead job where retry is exhausted; returns rows affected (0 or 1)
func (djRepo *DeliveryJobDBRepository) DeleteDeadJob(job *data.DeliveryJob, maxRetryCount uint) (int64, error) {
	var rowsAffected int64
	err := transactionalWrites(djRepo.db, func(tx *sql.Tx) error {
		result, execErr := tx.Exec("DELETE FROM job WHERE id = ? AND status = ? AND retryAttemptCount >= ?",
			job.ID, data.JobDead, maxRetryCount)
		if execErr != nil {
			return execErr
		}
		rowsAffected, execErr = result.RowsAffected()
		return execErr
	})
	return rowsAffected, err
}

// GetDeadJobCountsSinceCheckpoint returns dead job counts per consumer since the given timestamp
func (djRepo *DeliveryJobDBRepository) GetDeadJobCountsSinceCheckpoint(since time.Time) (map[string]int64, error) {
	result := make(map[string]int64)
	type countRow struct {
		consumerID string
		count      int64
	}
	rows := make([]*countRow, 0)
	scanArgs := func() []interface{} {
		row := &countRow{}
		rows = append(rows, row)
		return []interface{}{&row.consumerID, &row.count}
	}
	err := queryRows(djRepo.db, "SELECT consumerId, COUNT(id) FROM job WHERE status = ? AND statusChangedAt > ? GROUP BY consumerId",
		args2SliceFnWrapper(data.JobDead, since), scanArgs)
	if err != nil {
		return result, err
	}
	for _, row := range rows {
		result[row.consumerID] = row.count
	}
	return result, nil
}

// NewDeliveryJobRepository creates a new instance of DeliveryJobRepository
func NewDeliveryJobRepository(db *sql.DB, msgRepo MessageRepository, consumerRepo ConsumerRepository) DeliveryJobRepository {
	return &DeliveryJobDBRepository{db: db, mesageRepository: msgRepo, consumerRepository: consumerRepo}
}
