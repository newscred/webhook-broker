package storage

import (
	"github.com/go-sql-driver/mysql"
)

func normalizeDBError(mysqlDriverErr error, mappedError map[uint16]error) (err error) {
	err = mysqlDriverErr
	if mysqlErr, ok := mysqlDriverErr.(*mysql.MySQLError); ok {
		err = mysqlErr
		if mappedErr, ok := mappedError[mysqlErr.Number]; ok {
			err = mappedErr
		}
	}
	return err
}
