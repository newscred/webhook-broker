package storage

import (
	"github.com/go-sql-driver/mysql"
	sqlite "github.com/mattn/go-sqlite3"
)

func normalizeDBError(mysqlDriverErr error, mappedError map[uint16]error) (err error) {
	err = mysqlDriverErr
	if mysqlErr, ok := err.(*mysql.MySQLError); ok {
		err = lookup(mysqlErr.Number, mappedError, mysqlErr)
	} else if sqliteErr, ok := err.(*sqlite.ErrNoExtended); ok {
		switch *sqliteErr {
		case sqlite.ErrConstraintUnique:
			err = lookup(1062, mappedError, sqliteErr)
		}

	} else if sqliteErr, ok := err.(*sqlite.ErrNo); ok {
		switch *sqliteErr {
		case sqlite.ErrConstraint:
			err = lookup(1062, mappedError, sqliteErr)
		}
	}
	return err
}

func lookup(number uint16, mappedError map[uint16]error, defaultErr error) (err error) {
	err = defaultErr
	if mappedErr, ok := mappedError[number]; ok {
		err = mappedErr
	}
	return err
}
