// Package ast holds the parsed shape of a SQL statement, before any names have
// been resolved against a catalog.
package ast

import (
	"fmt"
	"strings"

	"github.com/sanke08/kite/internal/sql/value"
)

// Expr is any expression: something that produces one value when evaluated.
//
// WHY an interface here, when most of this project avoids them: there are
// genuinely many kinds of expression and they are walked polymorphically. The
// evaluator does not care whether it has a literal or a nested binary
// operation — it asks the node what it is and recurses.
//
// EG: 1+2*3 parses into a tree of three of these
//
//	Binary{+}
//	  Literal{1}
//	  Binary{*}
//	    Literal{2}
//	    Literal{3}
//
// The tree shape IS the precedence. Nothing later re-reads the operators to
// work out what binds tighter — the parser already decided by building * as a
// child of +.
type Expr interface {
	// String renders the expression back into readable form, for EXPLAIN
	// output and error messages.
	String() string

	// exprNode makes this interface closed: only types in this package can
	// satisfy it, because only this package can name an unexported method.
	//
	// WHY closed: the evaluator switches on the concrete type. A node from
	// outside would fall through every case and be silently ignored.
	exprNode()
}

// Literal is a constant written directly in the query.
//
// EG: the four literal forms
//
//	42       Literal{Integer(42)}
//	2.5      Literal{Real(2.5)}
//	'hi'     Literal{Text("hi")}
//	NULL     Literal{Null()}
type Literal struct {
	Value value.Value
}

func (literal *Literal) String() string {
	if literal.Value.Kind == value.KindText {
		return "'" + strings.ReplaceAll(literal.Value.Text(), "'", "''") + "'"
	}
	return literal.Value.String()
}

func (literal *Literal) exprNode() {}

// Operator identifies a binary operator.
type Operator int

const (
	OpAdd Operator = iota
	OpSubtract
	OpMultiply
	OpDivide
	OpModulo
	OpConcat
	OpEqual
	OpNotEqual
	OpLess
	OpLessEqual
	OpGreater
	OpGreaterEqual
	OpAnd
	OpOr
)

var operatorText = [...]string{
	OpAdd:          "+",
	OpSubtract:     "-",
	OpMultiply:     "*",
	OpDivide:       "/",
	OpModulo:       "%",
	OpConcat:       "||",
	OpEqual:        "=",
	OpNotEqual:     "<>",
	OpLess:         "<",
	OpLessEqual:    "<=",
	OpGreater:      ">",
	OpGreaterEqual: ">=",
	OpAnd:          "AND",
	OpOr:           "OR",
}

func (operator Operator) String() string {
	if int(operator) < len(operatorText) && operatorText[operator] != "" {
		return operatorText[operator]
	}
	return fmt.Sprintf("Operator(%d)", int(operator))
}

// Binary is an operator with a left and a right operand.
//
// EG: 1 < 2 AND 3 = 3 parses with AND at the ROOT, because AND binds more
// loosely than a comparison:
//
//	Binary{AND}
//	  Binary{<}  1, 2
//	  Binary{=}  3, 3
type Binary struct {
	Operator Operator
	Left     Expr
	Right    Expr
}

func (binary *Binary) String() string {
	return "(" + binary.Left.String() + " " + binary.Operator.String() + " " + binary.Right.String() + ")"
}

func (binary *Binary) exprNode() {}

// UnaryOperator identifies a prefix operator.
type UnaryOperator int

const (
	// OpNegate is the minus in -5, as opposed to the minus in 3-5.
	OpNegate UnaryOperator = iota
	OpNot
)

// Unary is a prefix operator applied to one operand.
//
// EG: -2+3 parses with the negation bound TIGHTLY, to the 2 only
//
//	Binary{+}
//	  Unary{-}  Literal{2}
//	  Literal{3}
//
// giving 1. If negation bound loosely it would parse as -(2+3) = -5, which is
// the classic Pratt mistake this shape exists to prevent.
type Unary struct {
	Operator UnaryOperator
	Operand  Expr
}

func (unary *Unary) String() string {
	if unary.Operator == OpNot {
		return "(NOT " + unary.Operand.String() + ")"
	}
	return "(-" + unary.Operand.String() + ")"
}

func (unary *Unary) exprNode() {}

// IsNull is the IS NULL and IS NOT NULL test.
//
// WHY it is a node rather than a comparison against a NULL literal: x = NULL
// is always Unknown, so it can never be used to find NULLs. IS NULL is the
// only test that returns a definite answer, which makes it a different
// operation rather than a special case of equality.
//
// EG: x IS NULL      -> IsNull{Negated: false}
//
//	x IS NOT NULL  -> IsNull{Negated: true}
type IsNull struct {
	Operand Expr
	Negated bool
}

func (isNull *IsNull) String() string {
	if isNull.Negated {
		return "(" + isNull.Operand.String() + " IS NOT NULL)"
	}
	return "(" + isNull.Operand.String() + " IS NULL)"
}

func (isNull *IsNull) exprNode() {}

// Star is the * in SELECT * FROM t.
//
// WHY it is an expression node rather than being expanded during parsing: the
// parser does not know what columns a table has — only the catalog does, and
// the catalog is not reachable from here. So * survives as a node and gets
// replaced by the real column list at execution time, where the table's shape
// is known.
//
// TRAP: expanding it in the parser would need the parser to hold a catalog,
// which puts schema knowledge into a component whose only job is grammar. It
// also breaks the moment a query names a table the parser has not read yet.
type Star struct{}

func (star *Star) String() string {
	return "*"
}

func (star *Star) exprNode() {}

// Column is a bare column reference: the a in SELECT a FROM t.
//
// EG: SELECT a FROM t   ->  Column{Name: "a"}
//
// TEMPORARY: it holds a name, not a resolved position. Turning a name into a
// column index needs the table's shape, and doing that lookup once per query
// instead of once per row is what the binder is for. Until then the executor
// resolves it by name on every row, which is correct and wasteful.
type Column struct {
	Name string
}

func (column *Column) String() string {
	return column.Name
}

func (column *Column) exprNode() {}
