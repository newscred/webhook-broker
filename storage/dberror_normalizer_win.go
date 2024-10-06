//go:build windows

package storage

import (
	"github.com/go-sql-driver/mysql"
)

func normalizeDBError(mysqlDriverErr error, mappedError map[uint16]error) (err error) {
	err = mysqlDriverErr
	if mysqlErr, ok := err.(*mysql.MySQLError); ok {
		err = lookup(mysqlErr.Number, mappedError, mysqlErr)
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
