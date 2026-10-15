// Package exec holds the query operators: the things that actually produce rows.
package exec

import (
	"strings"

	"github.com/sanke08/kite/internal/sql/value"
)

// Row is one row flowing between operators.
//
// WHY exec has its own Row rather than importing the storage one: exec sits
// below the root package that defines storage, so importing it would be an
// import cycle — a hard compile error. Both are a slice of Value and convert
// with a plain copy at the seam.
type Row []value.Value

// Operator produces rows, one at a time, on request.
//
// This is the contract every operator in the engine implements, and the shape
// it fixes matters more than the two operators using it today.
//
// EG: the caller's loop, which never changes no matter what is underneath
//
//	operator.Open()
//	defer operator.Close()
//	for {
//		row, err := operator.Next()
//		if err != nil {
//			return err
//		}
//		if row == nil {
//			break
//		}
//	}
//
// WHY PULL and not push: a parent asks its child for exactly one row and owns
// the loop. In a push model the child drives and hands rows up through a
// callback — which makes LIMIT 1 awkward, because there is no way to tell a
// producer already mid-flight to stop. Pulling means Limit simply stops
// calling Next, and the scan underneath never reads another row.
//
// WHY a nil Row means "no more rows" rather than a separate bool or an EOF
// error: an operator returns exactly one thing per call, and a caller that
// forgets to check gets a nil it will visibly panic on, rather than an empty
// row it might silently use.
type Operator interface {
	// Open prepares the operator. Nothing produces rows before it is called.
	//
	// WHY separate from the constructor: building the operator tree and
	// starting to read from it are different moments. A plan can be built,
	// inspected, and printed by EXPLAIN without touching a table.
	Open() error

	// Next returns the next row, or nil when there are no more.
	Next() (Row, error)

	// Close releases whatever Open acquired. Safe to call after an error, and
	// safe to call twice.
	Close() error

	// Columns names the output columns, in order. It is valid after Open and
	// is what the REPL prints as a header.
	Columns() []string
}

// columnIndex finds a column by name, case-insensitively.
//
// EG: for ["a", "b", "c"] — columnIndex("B") is 1, true.
//
// SQL identifiers are case-insensitive, so a column declared "Name" must be
// found by NAME, name, or nAmE.
func columnIndex(columns []string, name string) (int, bool) {
	for index, column := range columns {
		if strings.EqualFold(column, name) {
			return index, true
		}
	}
	return -1, false
}
