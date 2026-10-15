package exec

// WALKTHROUGH — scanning a two-row table:
//
//	NewSeqScan(source, cols)  holds the source, opens nothing yet
//	  .Open()                 asks the source for a cursor
//	  .Next()                 cursor.Next() true  -> row 1
//	  .Next()                 cursor.Next() true  -> row 2
//	  .Next()                 cursor.Next() false -> nil, no more rows
//	  .Close()                releases the cursor

// RowSource is the slice of a table that SeqScan needs: something that hands
// out a cursor.
//
// WHY declared here rather than imported: the table type lives in the root
// package, which imports this one — so importing it back would be a cycle and
// a compile error. Declaring the one method needed, at the point of use, is
// how Go inverts that dependency. The root package's table satisfies this
// without knowing the interface exists.
type RowSource interface {
	ScanRows() RowCursor
}

// RowCursor walks rows. Same shape as the storage cursor, restated here for
// the same reason as RowSource.
type RowCursor interface {
	Next() bool
	Row() Row
	Err() error
	Close() error
}

// SeqScan reads every row of a table in storage order.
//
// It is the bottom of every plan that touches a table — the only operator
// that talks to storage rather than to another operator.
type SeqScan struct {
	source  RowSource
	columns []string

	// cursor is nil until Open, and nil again after Close.
	cursor RowCursor
}

// NewSeqScan returns a scan over source, whose rows have the given columns.
func NewSeqScan(source RowSource, columns []string) *SeqScan {
	return &SeqScan{source: source, columns: columns}
}

func (scan *SeqScan) Open() error {
	scan.cursor = scan.source.ScanRows()
	return nil
}

// Next returns the next row from the table, or nil at the end.
//
// WHY the error check happens AFTER Next returns false rather than inside the
// loop: the cursor reports "stopped" one way for both reasons — end of table
// and read failure. Err is what separates them, and checking it only on the
// false branch is why a failed read becomes an error here instead of a
// silently truncated result.
func (scan *SeqScan) Next() (Row, error) {
	if !scan.cursor.Next() {
		if err := scan.cursor.Err(); err != nil {
			return nil, err
		}
		return nil, nil
	}
	return scan.cursor.Row(), nil
}

func (scan *SeqScan) Close() error {
	if scan.cursor == nil {
		return nil
	}
	cursor := scan.cursor
	scan.cursor = nil
	return cursor.Close()
}

func (scan *SeqScan) Columns() []string {
	return scan.columns
}
