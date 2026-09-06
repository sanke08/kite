package kite

import (
	"fmt"
	"strings"

	"github.com/sanke08/kite/internal/sql/ast"
	"github.com/sanke08/kite/internal/sql/catalog"
	"github.com/sanke08/kite/internal/sql/eval"
	"github.com/sanke08/kite/internal/sql/parser"
	"github.com/sanke08/kite/internal/sql/value"
)

// WALKTHROUGH — CREATE TABLE t(a INTEGER) then SELECT 1+1:
//
//	Open()                     an empty catalog, no tables
//	  .Exec("CREATE TABLE...")
//	    parser.ParseStmt       -> *ast.CreateTable
//	    execCreateTable        registers it in the catalog, makes a MemTable
//	  .Exec("SELECT 1+1")
//	    parser.ParseStmt       -> *ast.Select
//	    execSelect             evaluates each expression -> one Row

// DB is an open database: the tables that exist and the rows in them.
//
// TEMPORARY: everything lives in memory and dies with the process, so a
// CREATE TABLE has to be re-run each session. Persistence arrives when there
// is a file underneath.
type DB struct {
	catalog *catalog.Catalog

	// tables maps a lower-cased table name to its rows.
	//
	// WHY the rows are here rather than on catalog.Table: the catalog
	// describes SHAPE — what columns exist — and knows nothing about storage.
	// Keeping rows out of it is what lets the storage implementation be
	// swapped without the catalog changing at all.
	tables map[string]Table
}

// Result is what one statement produced.
//
// EG: SELECT 1+1, 'x' gives Columns ["1+1", "'x'"] and one Row.
// CREATE TABLE gives both empty — it has an effect, not a result.
type Result struct {
	Columns []string
	Rows    []Row
}

// Open returns an empty database.
func Open() *DB {
	return &DB{catalog: catalog.New(), tables: make(map[string]Table)}
}

// Exec runs one SQL statement.
func (db *DB) Exec(sql string) (*Result, error) {
	statementParser, err := parser.New(sql)
	if err != nil {
		return nil, err
	}
	statement, err := statementParser.ParseStmt()
	if err != nil {
		return nil, err
	}

	// WHY a type switch in one function rather than an Exec method on each
	// statement node: the node set is closed and owned by the ast package, so
	// there is nothing outside to plug in. A switch keeps the whole execution
	// story in one readable place, and adding a statement type produces a
	// missing case here rather than a forgotten method elsewhere.
	switch node := statement.(type) {
	case *ast.CreateTable:
		return db.execCreateTable(node)
	case *ast.DropTable:
		return db.execDropTable(node)
	case *ast.Select:
		return db.execSelect(node)
	}
	return nil, fmt.Errorf("unsupported statement %T", statement)
}

// execCreateTable registers a table and gives it somewhere to keep rows.
func (db *DB) execCreateTable(node *ast.CreateTable) (*Result, error) {
	columns := make([]catalog.Column, len(node.Columns))
	for index, definition := range node.Columns {
		columns[index] = catalog.Column{
			Name:         definition.Name,
			DeclaredType: definition.Type,

			// Affinity is computed once here, at CREATE time, rather than on
			// every value that later arrives.
			Affinity: catalog.AffinityOf(definition.Type),
		}
	}

	table := &catalog.Table{Name: node.Name, Columns: columns}
	if err := db.catalog.Create(table); err != nil {
		return nil, err
	}
	db.tables[strings.ToLower(node.Name)] = NewMemTable()
	return &Result{}, nil
}

func (db *DB) execDropTable(node *ast.DropTable) (*Result, error) {
	if !db.catalog.Drop(node.Name) {
		return nil, fmt.Errorf("no such table: %s", node.Name)
	}
	delete(db.tables, strings.ToLower(node.Name))
	return &Result{}, nil
}

// execSelect evaluates each expression in the select list.
//
// TEMPORARY: no FROM, so this always produces exactly one row. It becomes a
// scan over a real table once tables can hold rows.
func (db *DB) execSelect(node *ast.Select) (*Result, error) {
	result := &Result{}
	row := make(Row, len(node.Columns))

	for index, expression := range node.Columns {
		computed, err := eval.Expr(expression)
		if err != nil {
			return nil, err
		}
		row[index] = computed

		// The column's name is the expression's own text, which is what
		// SQLite does for an expression with no AS alias.
		result.Columns = append(result.Columns, expression.String())
	}

	result.Rows = append(result.Rows, row)
	return result, nil
}

// Tables lists every table name, sorted. Backs the .tables dot-command.
func (db *DB) Tables() []string {
	return db.catalog.Names()
}

// Schema renders a table's definition the way it was declared.
//
// EG: after CREATE TABLE t(a INTEGER, b VARCHAR(255)) —
//
//	CREATE TABLE t(a INTEGER, b VARCHAR(255));
func (db *DB) Schema(name string) (string, bool) {
	table, ok := db.catalog.Lookup(name)
	if !ok {
		return "", false
	}

	var builder strings.Builder
	builder.WriteString("CREATE TABLE ")
	builder.WriteString(table.Name)
	builder.WriteString("(")
	for index, column := range table.Columns {
		if index > 0 {
			builder.WriteString(", ")
		}
		builder.WriteString(column.Name)
		if column.DeclaredType != "" {
			builder.WriteString(" ")
			builder.WriteString(column.DeclaredType)
		}
	}
	builder.WriteString(");")
	return builder.String(), true
}

// Render turns a value into the text the REPL prints.
func Render(v value.Value) string {
	return v.String()
}
