package storage

import (
	"database/sql"
	"errors"
	"time"

	"github.com/newscred/webhook-broker/storage/data"
)

var (
	// ErrNoLock is returned when no lock is passed to try or release function
	ErrNoLock = errors.New("no lock provided")
	// ErrAlreadyLocked is returned when lock already exists in repo
	ErrAlreadyLocked = errors.New("lock already attained by someone else")
	lockErrorMap     = map[uint16]error{
		1062: ErrAlreadyLocked,
	}
)

// LockDBRepository represents the RDBMS implementation of LockRepository
type LockDBRepository struct {
	db *sql.DB
}

// TryLock tries to achieve confirm the attainment of lock; returns error if it could not attain lock else nil
func (lockRepo *LockDBRepository) TryLock(lock *data.Lock) (err error) {
	if lock == nil {
		err = ErrNoLock
	} else {
		err = normalizeDBError(transactionalSingleRowWriteExec(lockRepo.db, emptyOps, "INSERT INTO `lock` (lockId, attainedAt) VALUES (?, ?)",
			args2SliceFnWrapper(lock.LockID, lock.AttainedAt)), lockErrorMap)
	}
	return err
}

// ReleaseLock tries to release the lock, will return error if no such lock or any error in releasing
func (lockRepo *LockDBRepository) ReleaseLock(lock *data.Lock) (err error) {
	if lock == nil {
		err = ErrNoLock
	} else {
		err = transactionalSingleRowWriteExec(lockRepo.db, emptyOps, "DELETE FROM `lock` WHERE lockId like ?",
			args2SliceFnWrapper(lock.LockID))
	}
	return err
}

// TimeoutLocks will force release locks that are older than the duration specified from now. Return error if DB called failed
func (lockRepo *LockDBRepository) TimeoutLocks(threshold time.Duration) (err error) {
	// Pass 0 expected row change else otherwise tx will fail due to row change check
	err = transactionalWrites(lockRepo.db, func(tx *sql.Tx) error {
		return inTransactionExec(tx, emptyOps, "DELETE FROM `lock` WHERE attainedAt < ?", args2SliceFnWrapper(time.Now().Add(-1*threshold)), int64(0))
	})
	return err
}

// NewLockRepository creates a new instance of LockRepository
func NewLockRepository(db *sql.DB) LockRepository {
	return &LockDBRepository{db: db}
}
