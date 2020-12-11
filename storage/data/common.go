package data

import (
	"errors"
	"net/http"
	"time"

	"github.com/rs/xid"
)

var (
	// ErrInsufficientInformationForCreating is returned when NewProducer is called with insufficient information
	ErrInsufficientInformationForCreating = errors.New("Necessary information missing for persistence")
)

// Cursor represents a string used for pagination
type Cursor string

// Updateable represents interface for objects that expose updated date
type Updateable interface {
	GetLastUpdatedHTTPTimeString() string
}

// BasePaginateable provides common functionalities around paginateable objects
type BasePaginateable struct {
	ID        xid.ID
	CreatedAt time.Time
	UpdatedAt time.Time
}

// GetLastUpdatedHTTPTimeString exposes the string rep of the last modified timestamp for the object
func (paginateable *BasePaginateable) GetLastUpdatedHTTPTimeString() string {
	return paginateable.UpdatedAt.Format(http.TimeFormat)
}

// GetCursor returns the cursor value for this producer
func (paginateable *BasePaginateable) GetCursor() (*Cursor, error) {
	text, err := paginateable.ID.Value()
	cursor := Cursor(text.(string))
	return &cursor, err
}

// QuickFix fixes base paginate-able model's attribute
func (paginateable *BasePaginateable) QuickFix() bool {
	madeChanges := false
	if paginateable.ID.IsNil() {
		paginateable.ID = xid.New()
		madeChanges = true
	}
	if paginateable.CreatedAt.IsZero() {
		paginateable.CreatedAt = time.Now()
		madeChanges = true
	}
	if paginateable.UpdatedAt.IsZero() {
		paginateable.UpdatedAt = time.Now()
		madeChanges = true
	}
	return madeChanges
}

// Pagination represents a data structure to determine how to traverse a list
type Pagination struct {
	Next     *Cursor
	Previous *Cursor
}

// Paginateable should be implemented by objects having xid.ID as field ID in DB and helps get cursor object
type Paginateable interface {
	GetCursor() (*Cursor, error)
}

// ValidateableModel model supporting this can be checked for valid state before write ops. Also allows for quick fix to be applied
type ValidateableModel interface {
	QuickFix() bool
	IsInValidState() bool
}

// NewPagination returns a new pagination wrapper
func NewPagination(after Paginateable, before Paginateable) *Pagination {
	var next, previous *Cursor
	var err error
	if after != nil {
		next, err = after.GetCursor()
		if err != nil {
			return &Pagination{}
		}
	}
	if before != nil {
		previous, err = before.GetCursor()
		if err != nil {
			return &Pagination{}
		}
	}
	return &Pagination{Next: next, Previous: previous}
}

func setValIfBothNotEmpty(src *string, fallback *string) bool {
	madeChanges := false
	if len(*src) <= 0 && len(*fallback) > 0 {
		*src = *fallback
		madeChanges = true
	}
	return madeChanges
}

func createMessageStakeholder(name string, token string) MessageStakeholder {
	return MessageStakeholder{BasePaginateable: BasePaginateable{ID: xid.New()}, Name: name, Token: token}
}
