package storage

import (
	"database/sql"
	"errors"

	"github.com/newscred/webhook-broker/storage/data"
)

const (
	dispatchPauseSelectQuery = "SELECT scope, consumerId, reason, pausedAt FROM dispatch_pause"
	// effectivePauseQuery checks for either the consumer's own pause row or the global pause row
	effectivePauseQuery = "SELECT COUNT(*) FROM dispatch_pause WHERE scope IN (?, '" + data.GlobalDispatchPauseScope + "')"
)

var (
	errDuplicateDispatchPause = errors.New("dispatch pause already exists for scope")
	dispatchPauseErrorMap     = map[uint16]error{
		1062: errDuplicateDispatchPause,
	}
)

// DispatchPauseDBRepository is the RDBMS implementation of DispatchPauseRepository
type DispatchPauseDBRepository struct {
	db *sql.DB
}

// Pause stores the pause. It is idempotent: if the scope is already paused the existing pause
// (with its original pausedAt and reason) is returned.
func (repo *DispatchPauseDBRepository) Pause(pause *data.DispatchPause) (*data.DispatchPause, error) {
	if pause == nil {
		return nil, ErrInvalidStateToSave
	}
	pause.QuickFix()
	if !pause.IsInValidState() {
		return nil, ErrInvalidStateToSave
	}
	var consumerID sql.NullString
	if !pause.IsGlobal() {
		consumerID = sql.NullString{String: pause.ConsumerID, Valid: true}
	}
	err := transactionalSingleRowWriteExec(repo.db, emptyOps, "INSERT INTO dispatch_pause (scope, consumerId, reason, pausedAt) VALUES (?, ?, ?, ?)",
		args2SliceFnWrapper(pause.Scope, consumerID, pause.Reason, pause.PausedAt))
	if err != nil {
		if normalizeDBError(err, dispatchPauseErrorMap) == errDuplicateDispatchPause {
			// Either already paused (idempotent) or a constraint failure; only the former has a row to return
			if existing, getErr := repo.Get(pause.Scope); getErr == nil {
				return existing, nil
			}
		}
		return nil, err
	}
	return pause, nil
}

// Resume deletes the pause for the scope; returns true if a pause existed
func (repo *DispatchPauseDBRepository) Resume(scope string) (bool, error) {
	var rowsAffected int64
	err := transactionalWrites(repo.db, func(tx *sql.Tx) error {
		result, execErr := tx.Exec("DELETE FROM dispatch_pause WHERE scope = ?", scope)
		if execErr != nil {
			return execErr
		}
		rowsAffected, execErr = result.RowsAffected()
		return execErr
	})
	return rowsAffected > 0, err
}

// Get retrieves the pause for the scope; returns sql.ErrNoRows if the scope is not paused
func (repo *DispatchPauseDBRepository) Get(scope string) (*data.DispatchPause, error) {
	pause := &data.DispatchPause{}
	var consumerID sql.NullString
	err := querySingleRow(repo.db, dispatchPauseSelectQuery+" WHERE scope = ?", args2SliceFnWrapper(scope),
		args2SliceFnWrapper(&pause.Scope, &consumerID, &pause.Reason, &pause.PausedAt))
	if err != nil {
		return nil, err
	}
	pause.ConsumerID = consumerID.String
	return pause, nil
}

// GetAll retrieves every pause
func (repo *DispatchPauseDBRepository) GetAll() ([]*data.DispatchPause, error) {
	pauses := make([]*data.DispatchPause, 0)
	consumerIDs := make([]*sql.NullString, 0)
	err := queryRows(repo.db, dispatchPauseSelectQuery+" ORDER BY pausedAt", nilArgs, func() []interface{} {
		pause := &data.DispatchPause{}
		consumerID := &sql.NullString{}
		pauses = append(pauses, pause)
		consumerIDs = append(consumerIDs, consumerID)
		return []interface{}{&pause.Scope, consumerID, &pause.Reason, &pause.PausedAt}
	})
	if err != nil {
		return nil, err
	}
	for index, pause := range pauses {
		pause.ConsumerID = consumerIDs[index].String
	}
	return pauses, nil
}

// IsEffectivelyPaused returns true if the consumer (internal id) or the whole broker is paused
func (repo *DispatchPauseDBRepository) IsEffectivelyPaused(consumerInternalID string) (bool, error) {
	count := 0
	err := querySingleRow(repo.db, effectivePauseQuery, args2SliceFnWrapper(consumerInternalID), args2SliceFnWrapper(&count))
	return count > 0, err
}

// NewDispatchPauseRepository creates a new DispatchPauseRepository
func NewDispatchPauseRepository(db *sql.DB) DispatchPauseRepository {
	panicIfNoDBConnectionPool(db)
	return &DispatchPauseDBRepository{db: db}
}

// Generated with assistance from Claude AI
