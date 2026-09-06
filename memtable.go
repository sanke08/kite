package kite

import "fmt"

// WALKTHROUGH — three rows in, one scan out:
//
//	NewMemTable()      an empty table
//	  .Insert(row)     appends, hands back RowID 1
//	  .Insert(row)     appends, hands back RowID 2
//	  .Scan()          a cursor sitting BEFORE the first row
//	    .Next()        moves to row 1, reports true
//	    .Row()         the row it landed on
//	    .Next()        row 2
//	    .Next()        past the end, reports false

// MemTable is a Table that keeps its rows in a slice. It is the simplest
// thing that can honour the interface, and it exists so the SQL engine has
// somewhere to put rows before any disk code is written.
//
// TEMPORARY: everything vanishes when the process exits. A disk-backed Table
// replaces it later behind this same interface, and the whole test suite
// passing unchanged is how that swap gets verified.
type MemTable struct {
	// rows holds every row ever inserted, at its RowID's index minus one.
	//
	// WHY deletion leaves a hole rather than shifting the slice: a RowID must
	// keep meaning the same row for as long as it exists. Shifting on delete
	// would silently renumber every row after it, so an UPDATE holding a
	// RowID from a scan would rewrite the wrong row.
	rows []Row

	// deleted marks which slots are holes. A slot in rows with deleted[i]
	// true is skipped by scans and rejected by Update.
	//
	// EG: insert 3 rows, delete row 2 —
	//
	//	rows     [rowA, rowB, rowC]
	//	deleted  [false, true, false]
	//	a scan yields rowA then rowC, and rowC keeps RowID 3
	deleted []bool
}

// NewMemTable returns an empty in-memory table.
//
// WHY a constructor at all when the zero value works: it does not, quite —
// the zero MemTable is usable, but returning a pointer here makes it obvious
// at every call site that the table is shared and mutated rather than copied.
func NewMemTable() *MemTable {
	return &MemTable{}
}

// Insert appends a row and returns its RowID.
//
// WHY RowIDs start at 1 and never get reused: 1-based matches SQLite, and
// leaves 0 available as "no row", which is what an uninitialised RowID reads
// as. Never reusing means a RowID handed out by a scan is still valid after
// other rows are deleted.
func (table *MemTable) Insert(row Row) (RowID, error) {
	// Copy the row before storing it.
	//
	// WHY: the caller owns the slice it passed in and may reuse it for the
	// next row — an INSERT loop that fills one scratch row and calls Insert
	// each time would otherwise end up with every stored row aliasing the
	// same memory, all holding the last values written.
	stored := make(Row, len(row))
	copy(stored, row)

	table.rows = append(table.rows, stored)
	table.deleted = append(table.deleted, false)
	return RowID(len(table.rows)), nil
}

// Update replaces the row at id.
func (table *MemTable) Update(id RowID, row Row) error {
	index, err := table.indexOf(id)
	if err != nil {
		return err
	}

	stored := make(Row, len(row))
	copy(stored, row)
	table.rows[index] = stored
	return nil
}

// Delete marks the row at id as gone.
//
// WHY a flag rather than removing the element: see the note on rows. Removing
// would renumber every later row, and a cursor mid-scan would then skip one.
func (table *MemTable) Delete(id RowID) error {
	index, err := table.indexOf(id)
	if err != nil {
		return err
	}
	table.deleted[index] = true
	table.rows[index] = nil
	return nil
}

// indexOf turns a RowID into a slice index, rejecting ones that never existed
// or have been deleted.
func (table *MemTable) indexOf(id RowID) (int, error) {
	index := int(id) - 1
	if index < 0 || index >= len(table.rows) || table.deleted[index] {
		return 0, fmt.Errorf("no such row: %d", id)
	}
	return index, nil
}

// Scan returns a cursor positioned before the first row.
func (table *MemTable) Scan() Cursor {
	return &memCursor{table: table, position: -1}
}

// memCursor walks a MemTable.
type memCursor struct {
	table *MemTable

	// position is the index of the row the cursor is ON, or -1 before the
	// first row.
	//
	// WHY -1 rather than 0: the caller's loop is `for cursor.Next()`, so the
	// first Next has to land on index 0. Starting at 0 would make the first
	// Next skip the first row — a bug that only shows up as a missing row,
	// never as an error.
	position int
}

// Next advances to the next row that has not been deleted.
//
// EG: rows [A, B, C] with B deleted —
//
//	position -1 -> 0   true,  row A
//	position  0 -> 2   true,  row C   (1 is a hole, skipped)
//	position  2 -> 3   false, past the end
func (cursor *memCursor) Next() bool {
	for {
		cursor.position++
		if cursor.position >= len(cursor.table.rows) {
			return false
		}
		if !cursor.table.deleted[cursor.position] {
			return true
		}
	}
}

// Row returns the row the cursor is on. Only valid after Next returned true.
func (cursor *memCursor) Row() Row {
	return cursor.table.rows[cursor.position]
}

// RowID returns the identity of the current row, so an UPDATE or DELETE can
// name the row a scan just found.
func (cursor *memCursor) RowID() RowID {
	return RowID(cursor.position + 1)
}

// Err always returns nil: reading a slice cannot fail. It exists because a
// tree cursor CAN fail mid-scan, and every caller must already be written to
// check for that before the tree arrives.
func (cursor *memCursor) Err() error {
	return nil
}

// Close does nothing here — there are no pages to unpin. Callers still write
// `defer cursor.Close()`, so the call is already in place when a cursor that
// holds real resources takes over.
func (cursor *memCursor) Close() error {
	return nil
}
