package ast

import "strings"

// Stmt is any complete SQL statement.
//
// WHY a separate interface from Expr: an expression produces a VALUE, a
// statement produces an EFFECT or a result set. SELECT 1+2 is a statement
// whose body happens to be an expression; CREATE TABLE has no value at all.
// Sharing one interface would mean every evaluator switch has to handle both.
type Stmt interface {
	stmtNode()
}

// ColumnDef is one column as written in a CREATE TABLE.
//
// EG: in CREATE TABLE t(a INTEGER, b VARCHAR(255))
//
//	{Name: "a", Type: "INTEGER"}
//	{Name: "b", Type: "VARCHAR(255)"}
//
// Type is the raw text the user wrote, not an affinity. Turning it into an
// affinity is the catalog's job — the parser records what was typed and makes
// no judgement about it, which is why "CREATE TABLE t(a BANANA)" parses fine.
type ColumnDef struct {
	Name string
	Type string
}

// CreateTable is CREATE TABLE name (columns...).
type CreateTable struct {
	Name    string
	Columns []ColumnDef
}

func (createTable *CreateTable) stmtNode() {}

func (createTable *CreateTable) String() string {
	var builder strings.Builder
	builder.WriteString("CREATE TABLE ")
	builder.WriteString(createTable.Name)
	builder.WriteString("(")
	for index, column := range createTable.Columns {
		if index > 0 {
			builder.WriteString(", ")
		}
		builder.WriteString(column.Name)
		if column.Type != "" {
			builder.WriteString(" ")
			builder.WriteString(column.Type)
		}
	}
	builder.WriteString(")")
	return builder.String()
}

// DropTable is DROP TABLE name.
type DropTable struct {
	Name string
}

func (dropTable *DropTable) stmtNode() {}

// Insert is INSERT INTO name VALUES (...), (...).
//
// EG: INSERT INTO t VALUES (1, 'one'), (2, 'two')
//
//	Table: "t"
//	Rows:  [[Literal{1}, Literal{'one'}],
//	        [Literal{2}, Literal{'two'}]]
//
// WHY Rows is a slice of slices rather than one flat list: a single INSERT can
// carry several rows, and the row boundaries matter — three values could mean
// one 3-column row or three 1-column rows, and only the grouping says which.
type Insert struct {
	Table string

	// Columns names the target columns, or is empty for "all of them, in
	// declaration order".
	//
	// EG: INSERT INTO t(b, a) VALUES ('x', 1)  -> Columns ["b", "a"]
	//     INSERT INTO t VALUES (1, 'x')        -> Columns nil
	//
	// The order given here is the order the VALUES match, which is why "b, a"
	// puts 'x' in b and 1 in a.
	Columns []string

	Rows [][]Expr
}

func (insert *Insert) stmtNode() {}

// Select is a SELECT statement.
//
// EG: SELECT * FROM t
//
//	Columns: [Star{}]
//	From:    "t"
//
// TEMPORARY: From is a bare table name, not a join tree. It becomes a real
// FROM clause when there is more than one table to read from.
type Select struct {
	Columns []Expr

	// From is the table to read, or "" for a SELECT with no FROM at all —
	// which produces exactly one row from the expressions alone.
	From string
}

func (selectStmt *Select) stmtNode() {}
