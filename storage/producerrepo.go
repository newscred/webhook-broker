package storage

import (
	"database/sql"
	"time"

	"github.com/imyousuf/webhook-broker/storage/data"
)

// ProducerDBRepository is the producer repository implementation for RDBMS
type ProducerDBRepository struct {
	db *sql.DB
}

// Store either creates or updates the producer information
func (repo *ProducerDBRepository) Store(producer *data.Producer) (*data.Producer, error) {
	inProducer, err := repo.Get(producer.ProducerID)
	if err != nil {
		return repo.insertProducer(producer)
	}
	if producer.Name != inProducer.Name || producer.Token != inProducer.Token {
		if !producer.IsInValidState() {
			return nil, ErrInvalidStateToSave
		}
		return repo.updateProducer(inProducer, producer.Name, producer.Token)
	}
	return inProducer, err
}

func (repo *ProducerDBRepository) updateProducer(producer *data.Producer, name, token string) (*data.Producer, error) {
	var tx *sql.Tx
	var err error
	tx, err = repo.db.Begin()
	if err == nil {
		producer.Name = name
		producer.Token = token
		producer.UpdatedAt = time.Now()
		var result sql.Result
		result, err = repo.db.Exec("UPDATE producer SET name = $1, token = $2, updatedAt = $3 WHERE producerId = $4", producer.Name, producer.Token, producer.UpdatedAt, producer.ProducerID)
		if err == nil {
			var rowsAffected int64
			if rowsAffected, err = result.RowsAffected(); rowsAffected <= 0 && err == nil {
				err = ErrNoRowsUpdated
			}
			tx.Commit()
		} else {
			tx.Rollback()
		}
	}
	return producer, err
}

func (repo *ProducerDBRepository) insertProducer(producer *data.Producer) (*data.Producer, error) {
	producer.QuickFix()
	if !producer.IsInValidState() {
		return nil, ErrInvalidStateToSave
	}
	var tx *sql.Tx
	var err error
	tx, err = repo.db.Begin()
	if err == nil {
		_, err = repo.db.Exec("INSERT INTO producer (id, producerId, name, token, createdAt, updatedAt) VALUES ($1, $2, $3, $4, $5, $6)", producer.ID, producer.ProducerID, producer.Name, producer.Token, producer.CreatedAt, producer.UpdatedAt)
		if err == nil {
			tx.Commit()
		} else {
			tx.Rollback()
		}
	}
	return producer, err
}

// Get retrieves the producer with matching producer id
func (repo *ProducerDBRepository) Get(producerID string) (*data.Producer, error) {
	producer := &data.Producer{}
	row := repo.db.QueryRow("SELECT ID, producerID, name, token, createdAt, updatedAt FROM producer WHERE producerID like $1", producerID)
	err := row.Scan(&producer.ID, &producer.ProducerID, &producer.Name, &producer.Token, &producer.CreatedAt, &producer.UpdatedAt)
	return producer, err
}

// GetList retrieves the list of producer based on pagination params supplied. It will return a error if both after and before is present at the same time
func (repo *ProducerDBRepository) GetList(page *data.Pagination) ([]*data.Producer, *data.Pagination, error) {
	if page == nil || (page.Next != nil && page.Previous != nil) {
		return nil, nil, ErrPaginationDeadlock
	}
	baseQuery := "SELECT ID, producerID, name, token, createdAt, updatedAt FROM producer" + getPaginationQueryFragment(page, false)
	rows, err := repo.db.Query(baseQuery)
	if err != nil {
		return nil, nil, err
	}
	defer func() { rows.Close() }()
	producers := make([]*data.Producer, 0)
	for rows.Next() {
		producer := &data.Producer{}
		err = rows.Scan(&producer.ID, &producer.ProducerID, &producer.Name, &producer.Token, &producer.CreatedAt, &producer.UpdatedAt)
		if err != nil {
			return nil, nil, err
		}
		producers = append(producers, producer)
	}
	producerCount := len(producers)
	var pagination *data.Pagination = &data.Pagination{}
	if producerCount > 0 {
		pagination = data.NewPagination(producers[producerCount-1], producers[0])
	}
	return producers, pagination, nil
}

func getPaginationQueryFragment(page *data.Pagination, append bool) string {
	query := " "
	if page.Next != nil {
		if append {
			query = query + "AND "
		} else {
			query = query + "WHERE "
		}
		query = query + "ID < '" + string(*page.Next) + "' "
	}
	if page.Previous != nil {
		if append {
			query = query + "AND "
		} else {
			query = query + "WHERE "
		}
		query = query + "ID > '" + string(*page.Previous) + "' "
	}
	query = query + pageSizeWithOrder
	return query
}

// NewProducerRepository returns a new producer repository
func NewProducerRepository(db *sql.DB) ProducerRepository {
	panicIfNoDBConnectionPool(db)
	return &ProducerDBRepository{db: db}
}
