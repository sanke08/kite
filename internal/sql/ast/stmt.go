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

// Select is a SELECT statement.
//
// TEMPORARY: it holds only a list of expressions, because there is nowhere to
// select FROM yet. A From field and a Where field arrive with the operators
// that can use them.
type Select struct {
	Columns []Expr
}

func (selectStmt *Select) stmtNode() {}
