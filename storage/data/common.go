package data

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/rs/xid"
)

var (
	// ErrInsufficientInformationForCreating is returned when NewProducer is called with insufficient information
	ErrInsufficientInformationForCreating = errors.New("Necessary information missing for persistence")
)

const (
	cursorSeparator = "|"
)

// Cursor represents a string used for pagination
type Cursor struct {
	ID        string
	Timestamp time.Time
}

func (c *Cursor) String() string {
	cursorString := c.ID + cursorSeparator + c.Timestamp.Format(time.RFC3339Nano)
	return base64.StdEncoding.EncodeToString([]byte(cursorString))
}

// ParseCursor creates Cursor from its string representation
func ParseCursor(encodedCursorString string) (cursor *Cursor, err error) {
	cursor = &Cursor{}
	cursorString, err := base64.StdEncoding.DecodeString(encodedCursorString)
	var splits []string
	if err == nil {
		splits = strings.Split(string(cursorString), cursorSeparator)
		if len(splits) != 2 {
			err = ErrInsufficientInformationForCreating
		}
	}
	if err == nil {
		cursor.ID = splits[0]
	}
	if err == nil {
		cursor.Timestamp, err = time.Parse(time.RFC3339Nano, splits[1])
	}
	return cursor, err
}

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
func (paginateable *BasePaginateable) GetCursor() (cursor *Cursor, err error) {
	cursor = &Cursor{ID: paginateable.ID.String(), Timestamp: paginateable.CreatedAt}
	return cursor, err
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
