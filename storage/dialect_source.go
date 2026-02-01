package storage

import (
	"bytes"
	"io"
	"text/template"

	"github.com/golang-migrate/migrate/v4/source"
	_ "github.com/golang-migrate/migrate/v4/source/file"
)

// DialectSource wraps a file source and processes migrations as Go templates.
// This allows migration files to contain dialect-specific SQL using Go template syntax.
//
// Example migration file:
//
//	CREATE INDEX `my_index` ON `my_table` (`col1`, `col2`);
//	{{if eq .Dialect "mysql"}}
//	DROP INDEX `old_index` ON `my_table`;
//	{{else}}
//	DROP INDEX `old_index`;
//	{{end}}
//
// Unmarked SQL (outside template blocks) runs on all dialects.
type DialectSource struct {
	wrapped source.Driver
	dialect string
}

// TemplateData is passed to migration templates during processing
type TemplateData struct {
	Dialect string // "mysql" or "sqlite3"
}

// NewDialectSource creates a dialect-aware source wrapping a file source.
// The fileURL should be in the format "file://path/to/migrations".
// The dialect should be "mysql" or "sqlite3".
func NewDialectSource(fileURL string, dialect string) (*DialectSource, error) {
	wrapped, err := source.Open(fileURL)
	if err != nil {
		return nil, err
	}
	return &DialectSource{wrapped: wrapped, dialect: dialect}, nil
}

// Open returns a new driver instance. This is required by the source.Driver interface
// but we use NewDialectSource instead for initialization.
func (d *DialectSource) Open(url string) (source.Driver, error) {
	// This method is required by the interface but not used in our case
	// since we initialize via NewDialectSource
	return nil, nil
}

// Close closes the underlying source instance.
func (d *DialectSource) Close() error {
	return d.wrapped.Close()
}

// First returns the very first migration version available.
func (d *DialectSource) First() (version uint, err error) {
	return d.wrapped.First()
}

// Prev returns the previous version for a given version.
func (d *DialectSource) Prev(version uint) (prevVersion uint, err error) {
	return d.wrapped.Prev(version)
}

// Next returns the next version for a given version.
func (d *DialectSource) Next(version uint) (nextVersion uint, err error) {
	return d.wrapped.Next(version)
}

// ReadUp returns the UP migration body after processing it as a Go template.
func (d *DialectSource) ReadUp(version uint) (io.ReadCloser, string, error) {
	r, identifier, err := d.wrapped.ReadUp(version)
	if err != nil {
		return nil, "", err
	}
	return d.processTemplate(r, identifier)
}

// ReadDown returns the DOWN migration body after processing it as a Go template.
func (d *DialectSource) ReadDown(version uint) (io.ReadCloser, string, error) {
	r, identifier, err := d.wrapped.ReadDown(version)
	if err != nil {
		return nil, "", err
	}
	return d.processTemplate(r, identifier)
}

// processTemplate reads the migration content, processes it as a Go template,
// and returns the result. If the content is not a valid template or contains
// no template directives, it returns the original content unchanged.
func (d *DialectSource) processTemplate(r io.ReadCloser, identifier string) (io.ReadCloser, string, error) {
	content, err := io.ReadAll(r)
	r.Close()
	if err != nil {
		return nil, "", err
	}

	// Check if content contains template directives
	if !bytes.Contains(content, []byte("{{")) {
		// No template directives, return as-is for backwards compatibility
		return io.NopCloser(bytes.NewReader(content)), identifier, nil
	}

	tmpl, err := template.New("migration").Parse(string(content))
	if err != nil {
		// Template parse error - this is a real error that should be reported
		return nil, "", err
	}

	var buf bytes.Buffer
	err = tmpl.Execute(&buf, TemplateData{Dialect: d.dialect})
	if err != nil {
		return nil, "", err
	}

	return io.NopCloser(&buf), identifier, nil
}

// Generated with assistance from Claude AI
