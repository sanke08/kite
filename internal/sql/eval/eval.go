// Package eval computes the value of an expression tree.
package eval

import (
	"fmt"
	"math"

	"github.com/sanke08/kite/internal/sql/ast"
	"github.com/sanke08/kite/internal/sql/value"
)

// Expr evaluates an expression to a single value.
//
// WHY a type switch in one recursive function rather than an Eval method on
// each node: the node set is closed and owned by the ast package, so there is
// nothing outside to plug in. A switch keeps the whole evaluation story in one
// readable place, and adding a node type produces a missing case here rather
// than a forgotten method somewhere.
//
// EG: evaluating the tree for 1+2*3
//
//	Expr(Binary{+})        recurses into both sides
//	  Expr(Literal{1})     -> Integer(1)
//	  Expr(Binary{*})      recurses again
//	    -> Integer(6)
//	  -> Integer(7)
func Expr(expression ast.Expr) (value.Value, error) {
	switch node := expression.(type) {
	case *ast.Literal:
		return node.Value, nil

	case *ast.Unary:
		return evalUnary(node)

	case *ast.Binary:
		return evalBinary(node)

	case *ast.IsNull:
		operand, err := Expr(node.Operand)
		if err != nil {
			return value.Null(), err
		}
		// This is the one place a NULL produces a definite answer rather
		// than propagating, which is the whole reason IS NULL exists.
		isNull := operand.IsNull()
		if node.Negated {
			isNull = !isNull
		}
		return value.Bool(isNull).Value(), nil
	}
	return value.Null(), fmt.Errorf("eval: unsupported expression %T", expression)
}

func evalUnary(node *ast.Unary) (value.Value, error) {
	operand, err := Expr(node.Operand)
	if err != nil {
		return value.Null(), err
	}

	switch node.Operator {
	case ast.OpNegate:
		if operand.IsNull() {
			return value.Null(), nil
		}
		if operand.Kind == value.KindInteger {
			return value.Integer(-operand.Integer()), nil
		}
		return value.Real(-operand.Real()), nil

	case ast.OpNot:
		return value.Truthy(operand).Not().Value(), nil
	}
	return value.Null(), fmt.Errorf("eval: unsupported unary operator %d", node.Operator)
}

// evalBinary computes a binary operation.
//
// WHY AND and OR are handled before the operands are evaluated: they are the
// only operators whose result can be settled by one side alone. FALSE AND
// anything is FALSE even if the other side is NULL or would error.
func evalBinary(node *ast.Binary) (value.Value, error) {
	if node.Operator == ast.OpAnd || node.Operator == ast.OpOr {
		return evalLogical(node)
	}

	left, err := Expr(node.Left)
	if err != nil {
		return value.Null(), err
	}
	right, err := Expr(node.Right)
	if err != nil {
		return value.Null(), err
	}

	switch node.Operator {
	case ast.OpEqual:
		return value.Equal(left, right).Value(), nil
	case ast.OpNotEqual:
		return value.Equal(left, right).Not().Value(), nil
	case ast.OpLess:
		return value.Less(left, right).Value(), nil
	case ast.OpGreaterEqual:
		return value.Less(left, right).Not().Value(), nil
	case ast.OpGreater:
		return value.Less(right, left).Value(), nil
	case ast.OpLessEqual:
		return value.Less(right, left).Not().Value(), nil

	case ast.OpConcat:
		// NULL || anything is NULL, so a missing value cannot silently
		// become an empty string in the middle of a concatenation.
		if left.IsNull() || right.IsNull() {
			return value.Null(), nil
		}
		return value.Text(left.String() + right.String()), nil
	}
	return evalArithmetic(node.Operator, left, right)
}

// evalLogical computes AND and OR with three-valued logic and short-circuiting.
//
// EG: SELECT 0 AND NULL
//
//	left  -> Integer(0) -> False
//	False AND anything is False, so the right side is never evaluated
//	result Integer(0)
//
// and the case that shows why: SELECT 1 AND NULL is NULL, because a true left
// side leaves the answer resting on an unknown.
func evalLogical(node *ast.Binary) (value.Value, error) {
	left, err := Expr(node.Left)
	if err != nil {
		return value.Null(), err
	}
	leftTruth := value.Truthy(left)

	if node.Operator == ast.OpAnd && leftTruth == value.False {
		return value.False.Value(), nil
	}
	if node.Operator == ast.OpOr && leftTruth == value.True {
		return value.True.Value(), nil
	}

	right, err := Expr(node.Right)
	if err != nil {
		return value.Null(), err
	}
	rightTruth := value.Truthy(right)

	if node.Operator == ast.OpAnd {
		return leftTruth.And(rightTruth).Value(), nil
	}
	return leftTruth.Or(rightTruth).Value(), nil
}

// evalArithmetic computes + - * / %.
//
// EG: the integer/real rule
//
//	1 + 2      Integer(3)    both integers, so integer arithmetic
//	1 + 2.5    Real(3.5)     one real, so the whole thing is real
//	7 / 2      Integer(3)    integer division TRUNCATES
//	7 / 2.0    Real(3.5)
//
// TRAP: 7/2 giving 3 looks like a bug and is correct SQL. Integer division
// truncates; the way to get 3.5 is for one operand to be real.
func evalArithmetic(operator ast.Operator, left, right value.Value) (value.Value, error) {
	if left.IsNull() || right.IsNull() {
		return value.Null(), nil
	}

	leftNumber, leftIsInteger := numeric(left)
	rightNumber, rightIsInteger := numeric(right)
	bothIntegers := leftIsInteger && rightIsInteger

	switch operator {
	case ast.OpAdd:
		return numberValue(leftNumber+rightNumber, bothIntegers), nil
	case ast.OpSubtract:
		return numberValue(leftNumber-rightNumber, bothIntegers), nil
	case ast.OpMultiply:
		return numberValue(leftNumber*rightNumber, bothIntegers), nil

	case ast.OpDivide:
		// Division by zero is NULL in SQL, not an error and not infinity.
		if rightNumber == 0 {
			return value.Null(), nil
		}
		if bothIntegers {
			return value.Integer(int64(leftNumber) / int64(rightNumber)), nil
		}
		return value.Real(leftNumber / rightNumber), nil

	case ast.OpModulo:
		if int64(rightNumber) == 0 {
			return value.Null(), nil
		}
		return value.Integer(int64(leftNumber) % int64(rightNumber)), nil
	}
	return value.Null(), fmt.Errorf("eval: unsupported operator %s", operator)
}

// numeric coerces a value to a float and reports whether it was an integer.
//
// EG: Integer(3) -> 3.0, true.  Real(2.5) -> 2.5, false.
//
//	Text("12abc") -> 12.0, false.  Text("abc") -> 0.0, false.
//
// WHY text coerces rather than erroring: SQL reads a number off the front of
// text and stops. '12abc' + 1 is 13, which looks wrong and is the rule.
func numeric(operand value.Value) (float64, bool) {
	switch operand.Kind {
	case value.KindInteger:
		return float64(operand.Integer()), true
	case value.KindReal:
		return operand.Real(), false
	}
	return value.NumericPrefix(operand.Text())
}

// numberValue packages an arithmetic result back into a Value, keeping it an
// integer when both operands were integers and the result fits.
func numberValue(result float64, keepInteger bool) value.Value {
	if keepInteger && result == math.Trunc(result) &&
		result >= math.MinInt64 && result <= math.MaxInt64 {
		return value.Integer(int64(result))
	}
	return value.Real(result)
}
