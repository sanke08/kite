package kite

// Row is one row of a table: the column values, in the order the table
// declares them.
//
// WHY: []any rather than a struct or a map. Column types are not known until
// runtime, and SQL values carry their own type — so a static struct cannot
// describe a row and a map would lose column order.
type Row []any

// RowID identifies a row within one table. Every table row has one, assigned
// by the storage layer, and it is what a secondary index points at.
type RowID int64

// Table is how the SQL engine reads and writes one table's rows. Everything
// above this interface works the same whether the rows live in memory or in a
// B+tree on disk.
//
// WHY: this is deliberately small and cursor-shaped. A method like
// Rows() []Row would be one line for an in-memory table and a memory bomb for
// a tree holding a million rows, which would only be discovered after every
// query operator had been written against it.
type Table interface {
	// Scan returns a cursor positioned before the first row.
	Scan() Cursor

	// Insert stores a row and returns the RowID assigned to it.
	Insert(row Row) (RowID, error)

	// Update replaces the row at id.
	Update(id RowID, row Row) error

	// Delete removes the row at id.
	Delete(id RowID) error
}

// Cursor walks the rows of a table, one at a time.
//
// WHY: Next reports only whether a row is available, and errors are collected
// for Err. A single Next() (Row, error) would force every caller to
// distinguish "no more rows" from "the disk failed" on every iteration, and
// that check gets forgotten.
//
//	cursor := table.Scan()
//	defer cursor.Close()
//	for cursor.Next() {
//		row := cursor.Row()
//	}
//	if err := cursor.Err(); err != nil {
//	}
type Cursor interface {
	// Next advances to the next row, reporting whether one exists. It returns
	// false at the end of the table and also when a read fails, so Err must
	// be checked after the loop.
	Next() bool

	// Row returns the row at the cursor's current position. It is only valid
	// after Next has returned true.
	Row() Row

	// RowID returns the identity of the current row, so an UPDATE or DELETE
	// can name the row a scan just found.
	RowID() RowID

	// Err reports the first error that stopped the scan, or nil if the cursor
	// simply reached the end.
	Err() error

	// Close releases the cursor. Reading a tree holds pages pinned in memory,
	// and Close is what lets them go.
	Close() error
}
