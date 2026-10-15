package exec

import (
	"fmt"

	"github.com/sanke08/kite/internal/sql/ast"
	"github.com/sanke08/kite/internal/sql/eval"
	"github.com/sanke08/kite/internal/sql/value"
)

// WALKTHROUGH — SELECT a+1 FROM t over the row (5, 'x'):
//
//	NewProject(child, exprs, names)
//	  .Open()      opens the child
//	  .Next()      child.Next() -> Row{5, 'x'}
//	               evaluates a+1 against that row -> Integer(6)
//	               returns Row{6}
//	  .Close()     closes the child

// Project computes each output expression against every row its child
// produces.
//
// EG: the two jobs it does, on a table t(a, b) holding (5, 'x')
//
//	SELECT *      -> Row{5, 'x'}   every column, unchanged
//	SELECT a+1    -> Row{6}        one computed value
//	SELECT b, a   -> Row{'x', 5}   reordered
//
// WHY it is a separate operator rather than something SeqScan does: a scan
// reads storage and nothing else. Any operator can sit between them later — a
// filter, a sort, a join — and Project works over whatever produced the row,
// having no idea what that was.
type Project struct {
	child       Operator
	expressions []ast.Expr
	columns     []string

	// inputColumns names the columns of the rows arriving from the child, so
	// a Column reference can be turned into a position.
	//
	// TEMPORARY: this is a name lookup per column per row. Resolving names to
	// positions once per query, before any row is read, is what the binder
	// does — until then this is correct and wasteful.
	inputColumns []string
}

// NewProject returns a projection over child.
func NewProject(child Operator, expressions []ast.Expr, columns []string) *Project {
	return &Project{child: child, expressions: expressions, columns: columns}
}

func (project *Project) Open() error {
	if err := project.child.Open(); err != nil {
		return err
	}
	project.inputColumns = project.child.Columns()
	return nil
}

// Next pulls one row from the child and computes the output row from it.
func (project *Project) Next() (Row, error) {
	input, err := project.child.Next()
	if err != nil {
		return nil, err
	}
	if input == nil {
		return nil, nil
	}

	output := make(Row, 0, len(project.expressions))
	for _, expression := range project.expressions {
		// A Star expands to every input column, so one expression can
		// contribute several output values.
		if _, isStar := expression.(*ast.Star); isStar {
			output = append(output, input...)
			continue
		}

		computed, err := project.evaluate(expression, input)
		if err != nil {
			return nil, err
		}
		output = append(output, computed)
	}
	return output, nil
}

// evaluate computes one expression against one input row.
//
// WHY a Column is handled here rather than inside eval.Expr: eval knows how to
// combine values but has no idea a row exists — it evaluates 1+2 identically
// whether a table is involved or not. Reading a column out of a row is this
// operator's job, because this is the layer that HAS the row.
func (project *Project) evaluate(expression ast.Expr, input Row) (value.Value, error) {
	switch node := expression.(type) {
	case *ast.Column:
		index, ok := columnIndex(project.inputColumns, node.Name)
		if !ok {
			return value.Null(), fmt.Errorf("no such column: %s", node.Name)
		}
		return input[index], nil

	case *ast.Binary:
		left, err := project.evaluate(node.Left, input)
		if err != nil {
			return value.Null(), err
		}
		right, err := project.evaluate(node.Right, input)
		if err != nil {
			return value.Null(), err
		}
		// Hand the already-computed operands to eval as literals, so eval
		// stays the single place that knows SQL's arithmetic and comparison
		// rules rather than those rules being duplicated here.
		return eval.Expr(&ast.Binary{
			Operator: node.Operator,
			Left:     &ast.Literal{Value: left},
			Right:    &ast.Literal{Value: right},
		})

	case *ast.Unary:
		operand, err := project.evaluate(node.Operand, input)
		if err != nil {
			return value.Null(), err
		}
		return eval.Expr(&ast.Unary{
			Operator: node.Operator,
			Operand:  &ast.Literal{Value: operand},
		})

	case *ast.IsNull:
		operand, err := project.evaluate(node.Operand, input)
		if err != nil {
			return value.Null(), err
		}
		return eval.Expr(&ast.IsNull{
			Operand: &ast.Literal{Value: operand},
			Negated: node.Negated,
		})
	}

	// No column reference anywhere inside, so eval can take it whole.
	return eval.Expr(expression)
}

func (project *Project) Close() error {
	return project.child.Close()
}

func (project *Project) Columns() []string {
	return project.columns
}
