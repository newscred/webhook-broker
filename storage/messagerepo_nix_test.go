//go:build linux || darwin

package storage

import (
	"testing"

	"github.com/go-sql-driver/mysql"
	sqlite "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
)

func TestNormalizeMySQLError(t *testing.T) {
	assert.Equal(t, ErrDuplicateMessageIDForChannel, normalizeDBError(&mysql.MySQLError{Number: 1062}, mysqlErrorMap))
	assert.Nil(t, normalizeDBError(nil, mysqlErrorMap))
	assert.Equal(t, ErrDuplicateMessageIDForChannel, normalizeDBError(&sqlite.ErrConstraint, mysqlErrorMap))
	assert.Equal(t, ErrDuplicateMessageIDForChannel, normalizeDBError(&sqlite.ErrConstraintUnique, mysqlErrorMap))
}
