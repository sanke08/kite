// Package catalog remembers what tables exist and what columns they have.
package catalog

import (
	"fmt"
	"slices"
	"strings"
)

// Column is one column of a table.
//
// EG: CREATE TABLE t(a INTEGER, b VARCHAR(255)) gives
//
//	{Name: "a", DeclaredType: "INTEGER",       Affinity: Integer}
//	{Name: "b", DeclaredType: "VARCHAR(255)",  Affinity: Text}
type Column struct {
	Name string

	// DeclaredType is what the user wrote, kept verbatim so .schema can print
	// the original text back. Affinity is derived from it once, at CREATE
	// time, rather than being recomputed on every value that arrives.
	DeclaredType string

	Affinity Affinity
}

// Table is a table's shape: its name and its columns, in declaration order.
//
// WHY order matters enough to use a slice: SELECT * expands to the columns in
// exactly this order, and INSERT INTO t VALUES (1, 'x') matches values to
// columns positionally. A map would lose that.
type Table struct {
	Name    string
	Columns []Column
}

// ColumnIndex finds a column by name, returning its position and whether it
// was found.
//
// EG: for t(a, b, c) — ColumnIndex("b") is 1, true. ColumnIndex("z") is -1,
// false.
//
// WHY a linear scan rather than a map from name to index: tables have a
// handful of columns, and this runs once per query at bind time, not once per
// row. A map would be more code for no measurable difference.
//
// Comparison is case-insensitive because SQL identifiers are: a column
// declared "Name" is found by NAME, name, or nAmE.
func (table *Table) ColumnIndex(name string) (int, bool) {
	for index, column := range table.Columns {
		if strings.EqualFold(column.Name, name) {
			return index, true
		}
	}
	return -1, false
}

// Catalog holds every table the database knows about.
//
// WHY a plain map with no locking and no persistence: this engine has one
// writer and no concurrency, so a mutex would guard against nothing. The map
// is rebuilt from disk at open time once storage is durable — for now it dies
// with the process, which is why a CREATE TABLE has to be re-run each session.
type Catalog struct {
	// tables is keyed by the LOWER-CASED table name, because SQL identifiers
	// are case-insensitive: CREATE TABLE Users then SELECT * FROM USERS must
	// find the same table. The Table inside keeps the original spelling for
	// .schema output.
	tables map[string]*Table
}

// New returns an empty catalog.
func New() *Catalog {
	return &Catalog{tables: make(map[string]*Table)}
}

// Create adds a table.
//
// EG: Create("t", []Column{{Name: "a", ...}}) then Lookup("T") finds it.
//
// It fails if a table of that name already exists, which is what makes
// "CREATE TABLE t" twice an error rather than a silent overwrite that would
// discard the first table's rows.
func (catalog *Catalog) Create(table *Table) error {
	key := strings.ToLower(table.Name)
	if _, exists := catalog.tables[key]; exists {
		return fmt.Errorf("table %s already exists", table.Name)
	}
	catalog.tables[key] = table
	return nil
}

// Lookup finds a table by name, case-insensitively.
func (catalog *Catalog) Lookup(name string) (*Table, bool) {
	table, ok := catalog.tables[strings.ToLower(name)]
	return table, ok
}

// Drop removes a table. It reports whether the table existed.
func (catalog *Catalog) Drop(name string) bool {
	key := strings.ToLower(name)
	if _, exists := catalog.tables[key]; !exists {
		return false
	}
	delete(catalog.tables, key)
	return true
}

// Names returns every table name, sorted.
//
// WHY sorted rather than in creation order: Go randomises map iteration
// deliberately, so without sorting, .tables would print a different order
// every run and no test could pin its output.
func (catalog *Catalog) Names() []string {
	names := make([]string, 0, len(catalog.tables))
	for _, table := range catalog.tables {
		names = append(names, table.Name)
	}
	slices.Sort(names)
	return names
}
