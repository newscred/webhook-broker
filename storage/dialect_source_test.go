package storage

import (
	"bytes"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestProcessTemplate_MySQLDialect(t *testing.T) {
	ds := &DialectSource{dialect: "mysql"}

	content := `CREATE INDEX idx ON tbl (col);
{{if eq .Dialect "mysql"}}
DROP INDEX old_idx ON tbl;
{{else}}
DROP INDEX old_idx;
{{end}}`

	r := io.NopCloser(bytes.NewReader([]byte(content)))
	result, identifier, err := ds.processTemplate(r, "test.sql")

	assert.NoError(t, err)
	assert.Equal(t, "test.sql", identifier)

	resultContent, _ := io.ReadAll(result)
	resultStr := string(resultContent)

	assert.Contains(t, resultStr, "CREATE INDEX idx ON tbl (col);")
	assert.Contains(t, resultStr, "DROP INDEX old_idx ON tbl;")
	assert.NotContains(t, resultStr, "DROP INDEX old_idx;")
}

func TestProcessTemplate_SQLiteDialect(t *testing.T) {
	ds := &DialectSource{dialect: "sqlite3"}

	content := `CREATE INDEX idx ON tbl (col);
{{if eq .Dialect "mysql"}}
DROP INDEX old_idx ON tbl;
{{else}}
DROP INDEX old_idx;
{{end}}`

	r := io.NopCloser(bytes.NewReader([]byte(content)))
	result, identifier, err := ds.processTemplate(r, "test.sql")

	assert.NoError(t, err)
	assert.Equal(t, "test.sql", identifier)

	resultContent, _ := io.ReadAll(result)
	resultStr := string(resultContent)

	assert.Contains(t, resultStr, "CREATE INDEX idx ON tbl (col);")
	assert.Contains(t, resultStr, "DROP INDEX old_idx;")
	assert.NotContains(t, resultStr, "DROP INDEX old_idx ON tbl;")
}

func TestProcessTemplate_NoTemplateDirectives(t *testing.T) {
	ds := &DialectSource{dialect: "mysql"}

	content := `CREATE INDEX idx ON tbl (col);
DROP INDEX old_idx ON tbl;`

	r := io.NopCloser(bytes.NewReader([]byte(content)))
	result, identifier, err := ds.processTemplate(r, "test.sql")

	assert.NoError(t, err)
	assert.Equal(t, "test.sql", identifier)

	resultContent, _ := io.ReadAll(result)
	assert.Equal(t, content, string(resultContent))
}

func TestProcessTemplate_CommonSQLRunsOnAllDialects(t *testing.T) {
	content := `-- Common SQL runs on all dialects
CREATE TABLE users (id INT);
{{if eq .Dialect "mysql"}}
ALTER TABLE users ENGINE=InnoDB;
{{end}}
INSERT INTO users VALUES (1);`

	// Test MySQL
	dsMySQL := &DialectSource{dialect: "mysql"}
	r := io.NopCloser(bytes.NewReader([]byte(content)))
	result, _, err := dsMySQL.processTemplate(r, "test.sql")
	assert.NoError(t, err)

	resultContent, _ := io.ReadAll(result)
	resultStr := string(resultContent)
	assert.Contains(t, resultStr, "CREATE TABLE users (id INT);")
	assert.Contains(t, resultStr, "ALTER TABLE users ENGINE=InnoDB;")
	assert.Contains(t, resultStr, "INSERT INTO users VALUES (1);")

	// Test SQLite
	dsSQLite := &DialectSource{dialect: "sqlite3"}
	r = io.NopCloser(bytes.NewReader([]byte(content)))
	result, _, err = dsSQLite.processTemplate(r, "test.sql")
	assert.NoError(t, err)

	resultContent, _ = io.ReadAll(result)
	resultStr = string(resultContent)
	assert.Contains(t, resultStr, "CREATE TABLE users (id INT);")
	assert.NotContains(t, resultStr, "ALTER TABLE users ENGINE=InnoDB;")
	assert.Contains(t, resultStr, "INSERT INTO users VALUES (1);")
}

func TestProcessTemplate_InvalidTemplateSyntax(t *testing.T) {
	ds := &DialectSource{dialect: "mysql"}

	// Invalid template syntax (unclosed action)
	content := `CREATE INDEX idx ON tbl (col);
{{if eq .Dialect "mysql"`

	r := io.NopCloser(bytes.NewReader([]byte(content)))
	_, _, err := ds.processTemplate(r, "test.sql")

	assert.Error(t, err)
}

func TestProcessTemplate_MultipleDialectBlocks(t *testing.T) {
	ds := &DialectSource{dialect: "mysql"}

	content := `CREATE INDEX idx1 ON tbl1 (col);
{{if eq .Dialect "mysql"}}
DROP INDEX old1 ON tbl1;
{{else}}
DROP INDEX old1;
{{end}}
CREATE INDEX idx2 ON tbl2 (col);
{{if eq .Dialect "mysql"}}
DROP INDEX old2 ON tbl2;
{{else}}
DROP INDEX old2;
{{end}}`

	r := io.NopCloser(bytes.NewReader([]byte(content)))
	result, _, err := ds.processTemplate(r, "test.sql")

	assert.NoError(t, err)

	resultContent, _ := io.ReadAll(result)
	resultStr := string(resultContent)

	assert.Contains(t, resultStr, "CREATE INDEX idx1 ON tbl1 (col);")
	assert.Contains(t, resultStr, "DROP INDEX old1 ON tbl1;")
	assert.Contains(t, resultStr, "CREATE INDEX idx2 ON tbl2 (col);")
	assert.Contains(t, resultStr, "DROP INDEX old2 ON tbl2;")
}

// Generated with assistance from Claude AI
