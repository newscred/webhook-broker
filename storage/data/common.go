package data

// Cursor represents a string used for pagination
type Cursor string

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
