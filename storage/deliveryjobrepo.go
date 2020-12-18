package storage

import (
	"context"
	"database/sql"
	"fmt"

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

// NewDeliveryJobRepository creates a new instance of DeliveryJobRepository
func NewDeliveryJobRepository(db *sql.DB, msgRepo MessageRepository, consumerRepo ConsumerRepository) DeliveryJobRepository {
	return &DeliveryJobDBRepository{db: db, mesageRepository: msgRepo, consumerRepository: consumerRepo}
}
