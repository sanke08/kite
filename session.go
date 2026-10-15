package kite

import (
	"fmt"
	"strings"

	"github.com/sanke08/kite/internal/sql/ast"
	"github.com/sanke08/kite/internal/sql/catalog"
	"github.com/sanke08/kite/internal/sql/eval"
	"github.com/sanke08/kite/internal/sql/exec"
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
	case *ast.Insert:
		return db.execInsert(node)
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

// execInsert evaluates each row of VALUES and stores it.
//
// EG: INSERT INTO t(b, a) VALUES ('x', 1) on t(a INTEGER, b TEXT)
//
//	the column list says value 'x' goes to b and 1 goes to a,
//	so the stored row is Row{Integer(1), Text("x")} — in TABLE order,
//	not in the order the values were written
func (db *DB) execInsert(node *ast.Insert) (*Result, error) {
	schema, ok := db.catalog.Lookup(node.Table)
	if !ok {
		return nil, fmt.Errorf("no such table: %s", node.Table)
	}
	table := db.tables[strings.ToLower(node.Table)]

	// targets[i] is the table column that value i of each row belongs to.
	targets, err := insertTargets(schema, node.Columns)
	if err != nil {
		return nil, err
	}

	for _, valueRow := range node.Rows {
		if len(valueRow) != len(targets) {
			return nil, fmt.Errorf("table %s has %d columns but %d values were supplied",
				schema.Name, len(targets), len(valueRow))
		}

		// A row starts as all NULL, so a column the INSERT did not name
		// stays NULL rather than being absent — every stored row has one
		// value per table column, which is what lets a scan read positionally.
		row := make(Row, len(schema.Columns))
		for index := range row {
			row[index] = value.Null()
		}

		for position, expression := range valueRow {
			computed, err := eval.Expr(expression)
			if err != nil {
				return nil, err
			}
			row[targets[position]] = computed
		}

		if _, err := table.Insert(row); err != nil {
			return nil, err
		}
	}
	return &Result{}, nil
}

// insertTargets maps each supplied value position to a table column index.
//
// EG: on t(a, b, c)
//
//	INSERT INTO t VALUES (...)          -> [0, 1, 2]  all columns, in order
//	INSERT INTO t(c, a) VALUES (...)    -> [2, 0]     value 1 -> c, value 2 -> a
func insertTargets(schema *catalog.Table, named []string) ([]int, error) {
	if len(named) == 0 {
		targets := make([]int, len(schema.Columns))
		for index := range targets {
			targets[index] = index
		}
		return targets, nil
	}

	targets := make([]int, len(named))
	for position, name := range named {
		index, ok := schema.ColumnIndex(name)
		if !ok {
			return nil, fmt.Errorf("no such column: %s", name)
		}
		targets[position] = index
	}
	return targets, nil
}

// execSelect plans the query into an operator tree, then drains it.
//
// EG: SELECT * FROM t builds
//
//	Project{[Star]}
//	  SeqScan{t}
//
// and SELECT 1+2 with no FROM builds a Project over a single-row source,
// because an expression with nothing to read from still produces one row.
func (db *DB) execSelect(node *ast.Select) (*Result, error) {
	plan, err := db.plan(node)
	if err != nil {
		return nil, err
	}
	return drain(plan)
}

// plan turns a parsed SELECT into an operator tree.
//
// WHY planning is separate from running: the tree can be built and printed
// without reading a single row, and the same tree is what a later optimiser
// rewrites. Mixing the two would mean any change to plan shape touches
// execution too.
func (db *DB) plan(node *ast.Select) (exec.Operator, error) {
	var source exec.Operator
	var inputColumns []string

	if node.From == "" {
		// No table: one empty row, so the expressions evaluate exactly once.
		source = exec.NewSingleRow()
	} else {
		schema, ok := db.catalog.Lookup(node.From)
		if !ok {
			return nil, fmt.Errorf("no such table: %s", node.From)
		}
		for _, column := range schema.Columns {
			inputColumns = append(inputColumns, column.Name)
		}
		source = exec.NewSeqScan(rowSource{db.tables[strings.ToLower(node.From)]}, inputColumns)
	}

	outputColumns, err := outputNames(node.Columns, inputColumns)
	if err != nil {
		return nil, err
	}
	return exec.NewProject(source, node.Columns, outputColumns), nil
}

// outputNames works out the result's column headers.
//
// EG: on t(a, b)
//
//	SELECT *      -> ["a", "b"]     the star expands to the table's columns
//	SELECT a+1    -> ["a+1"]        the expression's own text, as SQLite does
func outputNames(expressions []ast.Expr, inputColumns []string) ([]string, error) {
	var names []string
	for _, expression := range expressions {
		if _, isStar := expression.(*ast.Star); isStar {
			if len(inputColumns) == 0 {
				return nil, fmt.Errorf("no tables specified")
			}
			names = append(names, inputColumns...)
			continue
		}
		names = append(names, expression.String())
	}
	return names, nil
}

// drain pulls every row out of a plan.
//
// TEMPORARY: it materialises the whole result before returning, which is fine
// for a REPL printing to a terminal and wrong for a million-row table. The
// operator tree already streams; only this caller does not.
func drain(plan exec.Operator) (*Result, error) {
	if err := plan.Open(); err != nil {
		return nil, err
	}
	defer plan.Close()

	result := &Result{Columns: plan.Columns()}
	for {
		row, err := plan.Next()
		if err != nil {
			return nil, err
		}
		if row == nil {
			return result, nil
		}

		// Copy: the operator may reuse its output buffer for the next row.
		stored := make(Row, len(row))
		for index, cell := range row {
			stored[index] = cell
		}
		result.Rows = append(result.Rows, stored)
	}
}

// rowSource adapts a storage Table to what SeqScan asks for.
//
// WHY an adapter rather than making Table satisfy exec.RowSource directly:
// exec sits below this package, so it cannot name Table, and Table cannot name
// exec.Row without a cycle. Four lines here keep both sides ignorant of each
// other.
type rowSource struct {
	table Table
}

func (source rowSource) ScanRows() exec.RowCursor {
	return rowCursor{source.table.Scan()}
}

type rowCursor struct {
	cursor Cursor
}

func (wrapper rowCursor) Next() bool { return wrapper.cursor.Next() }
func (wrapper rowCursor) Err() error { return wrapper.cursor.Err() }

func (wrapper rowCursor) Close() error { return wrapper.cursor.Close() }

func (wrapper rowCursor) Row() exec.Row {
	return exec.Row(wrapper.cursor.Row())
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
