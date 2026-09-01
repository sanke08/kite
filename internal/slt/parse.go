// Package slt reads and runs sqllogictest files.
//
// A .slt file is a sequence of records separated by blank lines. Each record
// either runs a statement whose success or failure is checked, or runs a query
// whose results are compared against expected values:
//
//	statement ok
//	CREATE TABLE t(a INTEGER)
//
//	query I nosort
//	SELECT a FROM t
//	----
//	1
//	2
package slt // SQL logic test"

import (
	"bufio"
	"fmt"
	"io"
	"slices"
	"strings"
)

// EngineLabel is the name kite answers to in skipif/onlyif lines. The corpus
// is engine-conditional, and its expected values are SQLite's — so claiming
// any other name would compare kite against answers it is not trying to
// produce.
const EngineLabel = "sqlite"

// Kind says whether a record checks that SQL ran, or checks what it returned.
type Kind int

const (
	// KindStatement: only success or failure is checked, not any output.
	// Written "statement ok" or "statement error".
	KindStatement Kind = iota

	// KindQuery: the returned rows are compared against expected values.
	// Written "query I nosort" and followed by ---- and those values.
	KindQuery
)

// SortMode says how the engine's rows are ordered before being compared, so
// that a query with no ORDER BY can still have a fixed expected answer.
type SortMode int

const (
	// NoSort compares in the order the engine returned. The query either has
	// an ORDER BY or its natural order is the answer being tested.
	NoSort SortMode = iota

	// RowSort sorts whole rows, keeping each row's values together.
	RowSort

	// ValueSort flattens every value into one flat list and sorts that, so
	// values can move between rows.
	ValueSort
)

// Record is one test from a .slt file.
//
// EG: these six lines
//
//	skipif mysql
//	query IT rowsort
//	SELECT a, b FROM t
//	----
//	1
//	alpha
//
// parse into
//
//	Kind         KindQuery
//	SQL          "SELECT a, b FROM t"
//	ColumnTypes  "IT"
//	SortMode     RowSort
//	Expected     ["1", "alpha"]
//	SkipIf       ["mysql"]
type Record struct {
	Kind Kind

	// SQL is the statement or query text, with no trailing semicolon. A
	// record spanning several lines keeps its newlines.
	SQL string

	// Line is where the record's header sits in the file, so a failure can
	// name the place to look.
	Line int

	// ExpectError inverts what counts as passing: the record passes only if
	// the SQL fails. Set by "statement error".
	ExpectError bool

	// ColumnTypes is one letter per column the query returns —
	// I integer, R real, T text. "IT" means two columns, an integer then
	// text. It decides how a value is rendered before comparison: 3 as "3"
	// under I, but "3.000" under R.
	ColumnTypes string

	// SortMode is how rows are ordered before comparison. Zero value NoSort
	// is correct when the record's header names no mode.
	SortMode SortMode

	// Expected holds the lines after ----, ONE VALUE PER LINE, not one row.
	// A two-column query returning one row has two entries here.
	Expected []string

	// SkipIf lists engines this record does not apply to; OnlyIf lists the
	// only engines it does. Both come from lines above the record's header,
	// and an empty OnlyIf means every engine.
	SkipIf []string
	OnlyIf []string
}

// Parse reads every record from a .slt file.
func Parse(input io.Reader) ([]Record, error) {
	scanner := bufio.NewScanner(input)

	// WHY: the default 64KB line limit is too small — some generated corpus
	// files hold very long single-line queries, and the scanner would fail
	// with a confusing "token too long" instead of running them.
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	var (
		// records accumulates every parsed record, in file order.
		records []Record

		// skipIf and onlyIf hold conditionals seen since the last record.
		// They sit on their own lines ABOVE the record they guard, so they
		// are collected here and moved onto the next record parsed.
		skipIf []string
		onlyIf []string

		// line is the current line number, reported on failures.
		line int
	)

	for scanner.Scan() {
		line++
		// text is the current line, with trailing whitespace removed.
		text := strings.TrimRight(scanner.Text(), " \t")

		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}

		// Conditionals attach to the next record, so collect and continue.
		// TRAP: a conditional can carry a trailing comment —
		// "onlyif sqlite # empty RHS". Keeping the whole rest of the line as
		// the label means it never matches the engine name.
		if label, ok := strings.CutPrefix(text, "skipif "); ok {
			skipIf = append(skipIf, engineLabel(label))
			continue
		}
		if label, ok := strings.CutPrefix(text, "onlyif "); ok {
			onlyIf = append(onlyIf, engineLabel(label))
			continue
		}

		// halt stops reading the file.
		//
		// TRAP: it obeys the skipif/onlyif above it. Corpus files open with
		// "onlyif mssql / halt" to exclude themselves from one engine, so
		// treating halt as unconditional would stop after two lines and
		// silently parse nothing.
		if text == "halt" {
			if shouldRun(skipIf, onlyIf) {
				break
			}
			skipIf, onlyIf = nil, nil
			continue
		}

		if text == "statement" || strings.HasPrefix(text, "statement ") ||
			text == "query" || strings.HasPrefix(text, "query ") {
			record, err := parseRecord(scanner, text, line, &line)
			if err != nil {
				return nil, err
			}
			record.SkipIf, record.OnlyIf = skipIf, onlyIf
			skipIf, onlyIf = nil, nil
			records = append(records, record)
			continue
		}

		// Anything else is a directive this runner does not implement —
		// hash-threshold, control, and so on. Ignoring them is safe; they
		// change how results are compared, never what the answer is.
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("slt: read: %w", err)
	}
	return records, nil
}

// parseRecord reads one record: its header, the SQL under it, and the
// expected values after any ---- separator.
//
// It is given the header line already read by Parse, and reads the rest.
// lineCounter is a pointer to Parse's line counter, so the lines consumed here
// still advance the number Parse reports.
//
// EG: header "query IT rowsort", then the scanner produces
//
//	SELECT a, b FROM t     -> collected as SQL
//	----                   -> switches to reading expected values
//	1                      -> Expected[0]
//	alpha                  -> Expected[1]
//	<blank>                -> record ends
func parseRecord(scanner *bufio.Scanner, header string, startLine int, lineCounter *int) (Record, error) {
	record := Record{Line: startLine}

	// headerWords splits "query IT rowsort" into its three parts: the kind,
	// then whatever that kind takes.
	headerWords := strings.Fields(header)

	switch headerWords[0] {
	case "statement":
		record.Kind = KindStatement
		if len(headerWords) < 2 {
			return record, fmt.Errorf("slt: line %d: statement needs ok or error", startLine)
		}
		record.ExpectError = headerWords[1] == "error"

	case "query":
		record.Kind = KindQuery
		if len(headerWords) < 2 {
			return record, fmt.Errorf("slt: line %d: query needs a type string", startLine)
		}
		record.ColumnTypes = headerWords[1]

		// The sort mode is optional; without it NoSort already applies.
		if len(headerWords) >= 3 {
			record.SortMode = parseSortMode(headerWords[2])
		}
	}

	// Read the SQL. It ends at a ---- separator, or at a blank line for a
	// record with no expected values.
	var sqlLines []string
	for scanner.Scan() {
		*lineCounter++
		text := scanner.Text()

		if strings.TrimSpace(text) == "" {
			record.SQL = strings.Join(sqlLines, "\n")
			return record, nil
		}
		if strings.HasPrefix(text, "----") {
			break
		}
		sqlLines = append(sqlLines, text)
	}
	record.SQL = strings.Join(sqlLines, "\n")

	// Past the ----, every line is one expected value until a blank line.
	for scanner.Scan() {
		*lineCounter++
		text := scanner.Text()
		if strings.TrimSpace(text) == "" {
			break
		}
		record.Expected = append(record.Expected, text)
	}
	return record, nil
}

// engineLabel pulls the engine name out of what follows "skipif "/"onlyif ".
//
// EG: "sqlite # empty RHS" -> "sqlite"
func engineLabel(afterKeyword string) string {
	if comment := strings.Index(afterKeyword, "#"); comment >= 0 {
		afterKeyword = afterKeyword[:comment]
	}
	return strings.TrimSpace(afterKeyword)
}

// shouldRun reports whether a record guarded by these conditionals applies to
// kite.
//
// EG:
//
//	skipIf ["mysql"], onlyIf []      -> true, kite is not mysql
//	skipIf [], onlyIf ["mssql"]      -> false, kite is not mssql
//	skipIf [], onlyIf []             -> true, applies everywhere
func shouldRun(skipIf, onlyIf []string) bool {
	if slices.Contains(skipIf, EngineLabel) {
		return false
	}
	if len(onlyIf) == 0 {
		return true
	}
	return slices.Contains(onlyIf, EngineLabel)
}

// parseSortMode reads the third word of a query header.
//
// An unrecognised word is not an error: the corpus also puts an arbitrary
// label in this position to name a record, and a label does not affect
// comparison, so NoSort stands.
func parseSortMode(word string) SortMode {
	switch word {
	case "rowsort":
		return RowSort
	case "valuesort":
		return ValueSort
	default:
		return NoSort
	}
}
