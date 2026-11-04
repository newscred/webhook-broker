package storage

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/newscred/webhook-broker/storage/data"
)

const (
	parkedJobColumns     = "seq, id, messageId, consumerId, dispatchReceivedAt, retryAttemptCount, earliestNextAttemptAt, priority, incrementalTimeout, createdAt, parkedAt"
	parkedJobSelectQuery = "SELECT " + parkedJobColumns + " FROM job_parking"
	parkedJobInsertQuery = "INSERT INTO job_parking (id, messageId, consumerId, dispatchReceivedAt, retryAttemptCount, earliestNextAttemptAt, priority, incrementalTimeout, createdAt, parkedAt) VALUES"
	parkedJobRowArgCount = 10
	// notEffectivelyPausedFmt is a correlated predicate that is true when neither the consumer referenced by
	// the given column nor the broker as a whole is paused
	notEffectivelyPausedFmt = "NOT EXISTS (SELECT 1 FROM dispatch_pause d WHERE d.scope IN (%s, '" + data.GlobalDispatchPauseScope + "'))"
	effectivelyPausedFmt    = "EXISTS (SELECT 1 FROM dispatch_pause d WHERE d.scope IN (%s, '" + data.GlobalDispatchPauseScope + "'))"

	// reparkInsertQuery copies a QUEUED job into job_parking only if its consumer is effectively paused
	reparkInsertQuery = "INSERT INTO job_parking (id, messageId, consumerId, dispatchReceivedAt, retryAttemptCount, earliestNextAttemptAt, priority, incrementalTimeout, createdAt, parkedAt) " +
		"SELECT j.id, j.messageId, j.consumerId, j.dispatchReceivedAt, j.retryAttemptCount, j.earliestNextAttemptAt, j.priority, j.incrementalTimeout, j.createdAt, ? " +
		"FROM job j WHERE j.id = ? AND j.status = ? AND "
	reparkDeleteQuery = "DELETE FROM job WHERE id = ? AND status = ?"

	// drainableConsumersQuery lists consumers that have parked jobs and are not effectively paused
	drainableConsumersQuery = "SELECT DISTINCT p.consumerId FROM job_parking p WHERE "
	// drainPageSelectQuery picks the oldest parked jobs of a consumer that is not effectively paused
	drainPageSelectQuery = "SELECT " + parkedJobColumns + " FROM job_parking p WHERE p.consumerId = ? AND "
	drainPageOrderBy     = " ORDER BY p.createdAt, p.seq LIMIT ?"
	drainJobInsertQuery  = "INSERT INTO job (id, messageId, consumerId, dispatchReceivedAt, statusChangedAt, earliestNextAttemptAt, status, createdAt, updatedAt, priority, incrementalTimeout, retryAttemptCount) VALUES"
	drainJobRowArgCount  = 12

	parkedCountsQuery = "SELECT c.channelId, p.consumerId, COUNT(p.id), MIN(p.parkedAt), MAX(p.parkedAt) FROM job_parking p JOIN consumer c ON p.consumerId = c.id GROUP BY c.channelId, p.consumerId"
)

var (
	reparkQuery                 = reparkInsertQuery + fmt.Sprintf(effectivelyPausedFmt, "j.consumerId")
	drainableConsumersFullQuery = drainableConsumersQuery + fmt.Sprintf(notEffectivelyPausedFmt, "p.consumerId")
	drainPageQuery              = drainPageSelectQuery + fmt.Sprintf(notEffectivelyPausedFmt, "p.consumerId") + drainPageOrderBy
)

// parkedJobRow is a scanned job_parking row
type parkedJobRow struct {
	seq      int64
	job      *data.DeliveryJob
	parkedAt time.Time
}

func newParkedJobRow() *parkedJobRow {
	job := &data.DeliveryJob{Message: &data.Message{}, Listener: &data.Consumer{}, Status: data.JobParked}
	return &parkedJobRow{job: job}
}

func (row *parkedJobRow) scanArgs() []interface{} {
	job := row.job
	return []interface{}{&row.seq, &job.ID, &job.Message.ID, &job.Listener.ID, &job.DispatchReceivedAt, &job.RetryAttemptCount,
		&job.EarliestNextAttemptAt, &job.Priority, &job.IncrementalTimeout, &job.CreatedAt, &row.parkedAt}
}

// asParked finalizes the job as a parked job: status PARKED and status-changed/updated at parkedAt
func (row *parkedJobRow) asParked() *data.DeliveryJob {
	row.job.Status = data.JobParked
	row.job.StatusChangedAt = row.parkedAt
	row.job.UpdatedAt = row.parkedAt
	return row.job
}

// insertParkedJobsInTx writes jobs into job_parking within the transaction; expects every row to be inserted
func insertParkedJobsInTx(tx *sql.Tx, jobs []*data.DeliveryJob, parkedAt time.Time) error {
	if len(jobs) == 0 {
		return nil
	}
	query := parkedJobInsertQuery
	args := make([]interface{}, 0, len(jobs)*parkedJobRowArgCount)
	for index, job := range jobs {
		if index > 0 {
			query += ","
		}
		query += " (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)"
		args = append(args, job.ID, job.Message.ID, job.Listener.ID, job.DispatchReceivedAt, job.RetryAttemptCount,
			job.EarliestNextAttemptAt, job.Priority, job.IncrementalTimeout, job.CreatedAt, parkedAt)
	}
	return inTransactionExec(tx, emptyOps, query, args2SliceFnWrapper(args...), int64(len(jobs)))
}

// loadParkedJobByID loads a parked job with its message and listener
func loadParkedJobByID(db *sql.DB, msgRepo MessageRepository, consumerRepo ConsumerRepository, id string) (*data.DeliveryJob, error) {
	row := newParkedJobRow()
	err := querySingleRow(db, parkedJobSelectQuery+" WHERE id = ?", args2SliceFnWrapper(id), row.scanArgs)
	if err != nil {
		return nil, err
	}
	job := row.asParked()
	if job.Message, err = msgRepo.GetByID(job.Message.ID.String()); err != nil {
		return nil, err
	}
	if job.Listener, err = consumerRepo.GetByID(job.Listener.ID.String()); err != nil {
		return nil, err
	}
	return job, nil
}

// JobParkingDBRepository is the RDBMS implementation of JobParkingRepository
type JobParkingDBRepository struct {
	db                 *sql.DB
	messageRepository  MessageRepository
	consumerRepository ConsumerRepository
}

// ReparkQueuedJob atomically moves a QUEUED job into job_parking if, and only if, its consumer is
// effectively paused. Returns false (and changes nothing) if the job is not QUEUED or not paused.
func (repo *JobParkingDBRepository) ReparkQueuedJob(job *data.DeliveryJob) (bool, error) {
	if job == nil {
		return false, ErrInvalidStateToSave
	}
	moved := false
	err := transactionalWrites(repo.db, func(tx *sql.Tx) error {
		result, execErr := tx.Exec(reparkQuery, time.Now(), job.ID, data.JobQueued)
		if execErr != nil {
			return execErr
		}
		inserted, execErr := result.RowsAffected()
		if execErr != nil || inserted == 0 {
			return execErr
		}
		if execErr = inTransactionExec(tx, emptyOps, reparkDeleteQuery, args2SliceFnWrapper(job.ID, data.JobQueued), int64(1)); execErr != nil {
			return execErr
		}
		moved = true
		return nil
	})
	if err != nil {
		return false, err
	}
	if moved {
		job.Status = data.JobParked
	}
	return moved, nil
}

// GetDrainableConsumerIDs returns internal ids of consumers that have parked jobs and are not effectively paused
func (repo *JobParkingDBRepository) GetDrainableConsumerIDs() ([]string, error) {
	consumerIDs := make([]string, 0)
	err := queryRows(repo.db, drainableConsumersFullQuery, nilArgs, func() []interface{} {
		consumerIDs = append(consumerIDs, "")
		return []interface{}{&consumerIDs[len(consumerIDs)-1]}
	})
	return consumerIDs, err
}

// DrainPage atomically moves up to pageSize of the consumer's parked jobs back into job as QUEUED,
// oldest (createdAt, seq) first. Parked rows are deleted before the jobs are inserted and the delete must
// remove exactly the selected rows, so two concurrent drainers can never move the same job twice.
func (repo *JobParkingDBRepository) DrainPage(consumerInternalID string, pageSize int, earliestNextAttemptAt time.Time) ([]*data.DeliveryJob, error) {
	if pageSize <= 0 {
		return []*data.DeliveryJob{}, nil
	}
	rows := make([]*parkedJobRow, 0, pageSize)
	now := time.Now()
	err := transactionalWrites(repo.db, func(tx *sql.Tx) error {
		resultRows, queryErr := tx.Query(drainPageQuery, consumerInternalID, pageSize)
		if queryErr != nil {
			return queryErr
		}
		for resultRows.Next() {
			row := newParkedJobRow()
			if scanErr := resultRows.Scan(row.scanArgs()...); scanErr != nil {
				resultRows.Close()
				return scanErr
			}
			rows = append(rows, row)
		}
		resultRows.Close()
		if iterErr := resultRows.Err(); iterErr != nil || len(rows) == 0 {
			return iterErr
		}
		seqs := make([]interface{}, len(rows))
		for index, row := range rows {
			seqs[index] = row.seq
		}
		deleteQuery := "DELETE FROM job_parking WHERE seq IN (" + placeholders(len(seqs)) + ")"
		if execErr := inTransactionExec(tx, emptyOps, deleteQuery, args2SliceFnWrapper(seqs...), int64(len(rows))); execErr != nil {
			return execErr
		}
		insertQuery := drainJobInsertQuery
		args := make([]interface{}, 0, len(rows)*drainJobRowArgCount)
		for index, row := range rows {
			if index > 0 {
				insertQuery += ","
			}
			insertQuery += " (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)"
			job := row.job
			args = append(args, job.ID, job.Message.ID, job.Listener.ID, job.DispatchReceivedAt, now, earliestNextAttemptAt,
				data.JobQueued, job.CreatedAt, now, job.Priority, job.IncrementalTimeout, job.RetryAttemptCount)
		}
		return inTransactionExec(tx, emptyOps, insertQuery, args2SliceFnWrapper(args...), int64(len(rows)))
	})
	if err != nil {
		return nil, err
	}
	jobs := make([]*data.DeliveryJob, 0, len(rows))
	for _, row := range rows {
		job := row.job
		job.Status = data.JobQueued
		job.StatusChangedAt = now
		job.UpdatedAt = now
		job.EarliestNextAttemptAt = earliestNextAttemptAt
		jobs = append(jobs, job)
	}
	return jobs, repo.populateJobs(jobs)
}

// populateJobs loads messages (batched) and listeners (cached) for jobs whose Message/Listener only carry ids
func (repo *JobParkingDBRepository) populateJobs(jobs []*data.DeliveryJob) error {
	if len(jobs) == 0 {
		return nil
	}
	messageIDs := make([]string, 0, len(jobs))
	for _, job := range jobs {
		messageIDs = append(messageIDs, job.Message.ID.String())
	}
	messages, err := repo.messageRepository.GetByIDs(messageIDs)
	if err != nil {
		return err
	}
	messageByID := make(map[string]*data.Message, len(messages))
	for _, message := range messages {
		messageByID[message.ID.String()] = message
	}
	for _, job := range jobs {
		if message, ok := messageByID[job.Message.ID.String()]; ok {
			job.Message = message
		}
		listener, listenerErr := repo.consumerRepository.GetByID(job.Listener.ID.String())
		if listenerErr != nil {
			log.Error().Err(listenerErr).Str("jobId", job.ID.String()).Msg("could not load listener for parked job")
			return listenerErr
		}
		job.Listener = listener
	}
	return nil
}

// GetByID loads a parked job; its Status is data.JobParked
func (repo *JobParkingDBRepository) GetByID(id string) (*data.DeliveryJob, error) {
	return loadParkedJobByID(repo.db, repo.messageRepository, repo.consumerRepository, id)
}

// GetForMessage lists the parked jobs of a message, oldest first
func (repo *JobParkingDBRepository) GetForMessage(message *data.Message) ([]*data.DeliveryJob, error) {
	rows := make([]*parkedJobRow, 0)
	err := queryRows(repo.db, parkedJobSelectQuery+" WHERE messageId = ? ORDER BY createdAt, seq", args2SliceFnWrapper(message.ID), func() []interface{} {
		row := newParkedJobRow()
		rows = append(rows, row)
		return row.scanArgs()
	})
	if err != nil {
		return nil, err
	}
	jobs := make([]*data.DeliveryJob, 0, len(rows))
	for _, row := range rows {
		job := row.asParked()
		job.Message = message
		if job.Listener, err = repo.consumerRepository.GetByID(job.Listener.ID.String()); err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, nil
}

// GetCountsByConsumer returns the number of parked jobs per consumer (internal id) with its channel id
func (repo *JobParkingDBRepository) GetCountsByConsumer() ([]*data.ParkedJobCount, error) {
	counts := make([]*data.ParkedJobCount, 0)
	err := queryRows(repo.db, parkedCountsQuery, nilArgs, func() []interface{} {
		count := &data.ParkedJobCount{}
		counts = append(counts, count)
		return []interface{}{&count.ChannelID, &count.ConsumerID, &count.Count, &count.OldestParkedAt, &count.NewestParkedAt}
	})
	return counts, err
}

// NewJobParkingRepository creates a new JobParkingRepository
func NewJobParkingRepository(db *sql.DB, msgRepo MessageRepository, consumerRepo ConsumerRepository) JobParkingRepository {
	panicIfNoDBConnectionPool(db)
	return &JobParkingDBRepository{db: db, messageRepository: msgRepo, consumerRepository: consumerRepo}
}

// Generated with assistance from Claude AI
