package kite

import "github.com/sanke08/kite/internal/sql/value"

// Row is one row of a table: its column values, in the order the table
// declares them.
//
// EG: for CREATE TABLE t(a INTEGER, b TEXT), the row (1, 'one') is
//
//	Row{value.Integer(1), value.Text("one")}
//
// WHY a slice of Value, not a struct and not a map: column types are not known
// until a CREATE TABLE runs at runtime, so no static struct can describe a
// row. A map would work but loses column order, which SELECT * needs. And the
// element type is value.Value rather than any, so nothing downstream has to
// type-assert before it can compare or add two of them.
type Row []value.Value

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
