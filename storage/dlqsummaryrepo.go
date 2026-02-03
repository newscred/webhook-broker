package storage

import (
	"database/sql"
	"time"

	"github.com/newscred/webhook-broker/storage/data"
	"github.com/rs/zerolog/log"
)

// DLQSummaryDBRepository is the DLQSummaryRepository's RDBMS implementation
type DLQSummaryDBRepository struct {
	db *sql.DB
}

// GetAll retrieves all DLQ summary records
func (repo *DLQSummaryDBRepository) GetAll() ([]*data.DLQSummary, error) {
	summaries := make([]*data.DLQSummary, 0)
	scanArgs := func() []interface{} {
		summary := &data.DLQSummary{}
		summaries = append(summaries, summary)
		return []interface{}{&summary.ConsumerID, &summary.ConsumerName, &summary.ChannelID, &summary.ChannelName, &summary.DeadCount, &summary.LastCheckedAt}
	}
	err := queryRows(repo.db, "SELECT consumerId, consumerName, channelId, channelName, dead_count, last_checked_at FROM dlq_summary", nilArgs, scanArgs)
	return summaries, err
}

// GetLastCheckedAt returns the maximum last_checked_at from the dlq_summary table
func (repo *DLQSummaryDBRepository) GetLastCheckedAt() (time.Time, error) {
	var lastCheckedAt sql.NullString
	err := querySingleRow(repo.db, "SELECT MAX(last_checked_at) FROM dlq_summary", nilArgs, args2SliceFnWrapper(&lastCheckedAt))
	if err != nil || !lastCheckedAt.Valid {
		return time.Time{}, err
	}
	// Try parsing with T separator (MySQL ISO 8601 format) first, then space separator (SQLite)
	ts := lastCheckedAt.String
	if len(ts) >= 19 {
		ts = ts[:19]
	}
	t, err := time.Parse("2006-01-02T15:04:05", ts)
	if err != nil {
		t, err = time.Parse("2006-01-02 15:04:05", ts)
	}
	return t, err
}

// UpsertCounts inserts or updates dead job counts in the summary table
func (repo *DLQSummaryDBRepository) UpsertCounts(summaries []*data.DLQSummary) error {
	if len(summaries) == 0 {
		return nil
	}
	for _, summary := range summaries {
		err := transactionalWrites(repo.db, func(tx *sql.Tx) error {
			// Try to update first
			result, err := tx.Exec(
				"UPDATE dlq_summary SET dead_count = dead_count + ?, consumerName = ?, channelId = ?, channelName = ?, last_checked_at = ? WHERE consumerId = ?",
				summary.DeadCount, summary.ConsumerName, summary.ChannelID, summary.ChannelName, summary.LastCheckedAt, summary.ConsumerID,
			)
			if err != nil {
				return err
			}
			rowsAffected, err := result.RowsAffected()
			if err != nil {
				return err
			}
			if rowsAffected == 0 {
				// Insert new row
				_, err = tx.Exec(
					"INSERT INTO dlq_summary (consumerId, consumerName, channelId, channelName, dead_count, last_checked_at) VALUES (?, ?, ?, ?, ?, ?)",
					summary.ConsumerID, summary.ConsumerName, summary.ChannelID, summary.ChannelName, summary.DeadCount, summary.LastCheckedAt,
				)
				return err
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// DecrementCount decrements the dead_count for a consumer, not going below 0
func (repo *DLQSummaryDBRepository) DecrementCount(consumerID string, count int64) error {
	if count <= 0 {
		return nil
	}
	return transactionalWrites(repo.db, func(tx *sql.Tx) error {
		_, err := tx.Exec("UPDATE dlq_summary SET dead_count = CASE WHEN dead_count >= ? THEN dead_count - ? ELSE 0 END WHERE consumerId = ?",
			count, count, consumerID)
		return err
	})
}

// BootstrapCounts performs a full count of dead jobs and populates the summary table
func (repo *DLQSummaryDBRepository) BootstrapCounts(dbConn *sql.DB) error {
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
	err := queryRows(repo.db, "SELECT consumerId, COUNT(id) FROM job WHERE status = ? GROUP BY consumerId", args2SliceFnWrapper(data.JobDead), scanArgs)
	if err != nil {
		return err
	}
	now := time.Now()
	summaries := make([]*data.DLQSummary, 0, len(rows))
	for _, row := range rows {
		// Look up consumer and channel names
		var consumerName, channelID, channelName string
		err := querySingleRow(repo.db, "SELECT c.name, c.channelId, ch.name FROM consumer c JOIN channel ch ON c.channelId = ch.channelId WHERE c.id = ?",
			args2SliceFnWrapper(row.consumerID), args2SliceFnWrapper(&consumerName, &channelID, &channelName))
		if err != nil {
			log.Warn().Err(err).Str("consumerID", row.consumerID).Msg("could not look up consumer/channel for DLQ bootstrap")
			continue
		}
		summaries = append(summaries, &data.DLQSummary{
			ConsumerID:    row.consumerID,
			ConsumerName:  consumerName,
			ChannelID:     channelID,
			ChannelName:   channelName,
			DeadCount:     row.count,
			LastCheckedAt: now,
		})
	}
	// Clear existing data and insert fresh
	return transactionalWrites(repo.db, func(tx *sql.Tx) error {
		_, err := tx.Exec("DELETE FROM dlq_summary")
		if err != nil {
			return err
		}
		for _, s := range summaries {
			_, err = tx.Exec("INSERT INTO dlq_summary (consumerId, consumerName, channelId, channelName, dead_count, last_checked_at) VALUES (?, ?, ?, ?, ?, ?)",
				s.ConsumerID, s.ConsumerName, s.ChannelID, s.ChannelName, s.DeadCount, s.LastCheckedAt)
			if err != nil {
				return err
			}
		}
		return nil
	})
}

// NewDLQSummaryRepository creates a new instance of DLQSummaryRepository
func NewDLQSummaryRepository(db *sql.DB) DLQSummaryRepository {
	panicIfNoDBConnectionPool(db)
	return &DLQSummaryDBRepository{db: db}
}

// Generated with assistance from Claude AI
