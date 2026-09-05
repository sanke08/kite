// Package parser turns a token stream into an ast.Expr tree.
package parser

import (
	"fmt"
	"strconv"

	"github.com/sanke08/kite/internal/sql/ast"
	"github.com/sanke08/kite/internal/sql/scanner"
	"github.com/sanke08/kite/internal/sql/value"
)

// WALKTHROUGH — parsing 1+2*3:
//
//	ParseExpr           calls parseExpr(0), the lowest binding power
//	  parsePrefix       reads 1 -> Literal{1}
//	  loop sees +       power 4 > 0, so + binds: consume it
//	    parseExpr(5)    parse the right side at + power + 1
//	      parsePrefix   reads 2 -> Literal{2}
//	      loop sees *   power 6 > 5, so * binds tighter: consume it
//	        parseExpr(7)  reads 3 -> Literal{3}
//	      returns       Binary{* , 2, 3}
//	    returns         Binary{+ , 1, Binary{*, 2, 3}}
//
// The whole algorithm is that one comparison — "does the operator I can see
// bind tighter than the caller's threshold". Everything else is bookkeeping.

// Parser reads tokens and builds expression trees.
type Parser struct {
	scanner *scanner.Scanner

	// current is the token being looked at. Pratt parsing needs exactly one
	// token of lookahead: enough to ask "what is the next operator and how
	// tightly does it bind" without consuming it.
	current scanner.Token
}

// New returns a parser over the given SQL text.
func New(input string) (*Parser, error) {
	parser := &Parser{scanner: scanner.New(input)}

	// Prime current, so the first parse step already has a token to look at.
	if err := parser.advance(); err != nil {
		return nil, err
	}
	return parser, nil
}

// ParseExpr parses one complete expression and checks nothing is left over.
//
// EG: "1+2" succeeds. "1+2 junk" fails, because the expression ended at 2 and
// junk is unaccounted for — reporting that is better than silently ignoring
// the rest of what the user typed.
func (parser *Parser) ParseExpr() (ast.Expr, error) {
	expression, err := parser.parseExpr(0)
	if err != nil {
		return nil, err
	}

	// A trailing semicolon is fine; anything else is not.
	if parser.current.Type == scanner.Semicolon {
		if err := parser.advance(); err != nil {
			return nil, err
		}
	}
	if parser.current.Type != scanner.EOF {
		return nil, parser.errorf("unexpected %s after expression", parser.current)
	}
	return expression, nil
}

// bindingPower returns how tightly an operator holds its operands, and the
// operator it maps to. A power of 0 means "not a binary operator".
//
// WHY numbers rather than a table of precedence levels with names: the parser
// only ever compares two of them. The gaps between values are deliberate — a
// right-associative operator would use power-1 on its right side, and leaving
// space means adding one later does not renumber everything.
//
// EG: SQL's precedence, loosest first
//
//	OR              1     binds least: splits the whole expression
//	AND             2
//	= <> < <= > >=  3
//	+ -             4
//	* / %           6
//	||              7     binds tightest of the binaries
//
// so 1+2*3 groups as 1+(2*3) because 6 > 4, and
// a=1 AND b=2 groups as (a=1) AND (b=2) because 3 > 2.
func bindingPower(token scanner.Token) (int, ast.Operator) {
	switch token.Type {
	case scanner.Keyword:
		switch token.Text {
		case "OR":
			return 1, ast.OpOr
		case "AND":
			return 2, ast.OpAnd
		}
	case scanner.Equal:
		return 3, ast.OpEqual
	case scanner.NotEqual:
		return 3, ast.OpNotEqual
	case scanner.Less:
		return 3, ast.OpLess
	case scanner.LessEqual:
		return 3, ast.OpLessEqual
	case scanner.Greater:
		return 3, ast.OpGreater
	case scanner.GreaterEqual:
		return 3, ast.OpGreaterEqual
	case scanner.Plus:
		return 4, ast.OpAdd
	case scanner.Minus:
		return 4, ast.OpSubtract
	case scanner.Star:
		return 6, ast.OpMultiply
	case scanner.Slash:
		return 6, ast.OpDivide
	case scanner.Percent:
		return 6, ast.OpModulo
	case scanner.Concat:
		return 7, ast.OpConcat
	}
	return 0, 0
}

// unaryPower is how tightly a prefix operator binds its operand.
//
// WHY 5 for negation — between + - (4) and * / (6): it has to beat addition so
// -2+3 is (-2)+3 rather than -(2+3). It must NOT beat multiplication, because
// -2*3 and -(2*3) both give -6 and SQL treats them as the same. Sitting in the
// gap satisfies both.
//
// TRAP: give negation a power below + and "-2+3" parses as -(2+3) = -5. The
// query runs, returns a number, and the number is wrong. This is the classic
// Pratt off-by-one.
const (
	unaryPowerNegate = 5

	// NOT binds looser than any comparison, so NOT a = b is NOT (a = b)
	// rather than (NOT a) = b.
	unaryPowerNot = 2
)

// parseExpr is the Pratt loop: parse one operand, then keep absorbing
// operators that bind at least as tightly as minPower.
//
// EG: why the loop condition is `power <= minPower -> stop`
//
//	parseExpr(0) on 1+2+3
//	  reads 1, sees + at power 4. 4 > 0, so absorb.
//	  right side parsed at power 5 — so the second + (power 4) does NOT
//	  bind there, and control returns to this loop, which absorbs it.
//	  result: ((1+2)+3), left-associative, which is what SQL wants.
//
// Calling with power+1 on the right is what makes operators left-associative.
// Calling with power alone would give ((1+(2+3))) — right-associative, and
// wrong for subtraction: 10-3-2 would be 9 instead of 5.
func (parser *Parser) parseExpr(minPower int) (ast.Expr, error) {
	left, err := parser.parsePrefix()
	if err != nil {
		return nil, err
	}

	for {
		// IS NULL / IS NOT NULL is postfix, so it is handled here rather
		// than in the binding-power table.
		if parser.current.Type == scanner.Keyword && parser.current.Text == "IS" {
			left, err = parser.parseIsNull(left)
			if err != nil {
				return nil, err
			}
			continue
		}

		power, operator := bindingPower(parser.current)
		if power == 0 || power <= minPower {
			return left, nil
		}

		if err := parser.advance(); err != nil {
			return nil, err
		}
		right, err := parser.parseExpr(power)
		if err != nil {
			return nil, err
		}
		left = &ast.Binary{Operator: operator, Left: left, Right: right}
	}
}

// parsePrefix parses whatever can START an expression: a literal, a prefix
// operator, or a parenthesised expression.
//
// WHY this is separate from the operator loop: at the start of an expression a
// minus can only be negation, because there is no left operand for it to
// subtract from. That positional fact is what tells unary and binary minus
// apart, and it is why the scanner correctly refused to guess.
//
// EG: the same '-' token, two meanings, decided by where it appears
//
//	-2      parsePrefix sees it first     -> Unary{negate}
//	3-2     the loop sees it after 3      -> Binary{subtract}
func (parser *Parser) parsePrefix() (ast.Expr, error) {
	token := parser.current

	switch token.Type {
	case scanner.Integer:
		number, err := strconv.ParseInt(token.Text, 10, 64)
		if err != nil {
			// Too large for int64. SQL falls back to a real rather than
			// failing, which is what SQLite does.
			realNumber, realErr := strconv.ParseFloat(token.Text, 64)
			if realErr != nil {
				return nil, parser.errorf("bad integer %q", token.Text)
			}
			if err := parser.advance(); err != nil {
				return nil, err
			}
			return &ast.Literal{Value: value.Real(realNumber)}, nil
		}
		if err := parser.advance(); err != nil {
			return nil, err
		}
		return &ast.Literal{Value: value.Integer(number)}, nil

	case scanner.Float:
		number, err := strconv.ParseFloat(token.Text, 64)
		if err != nil {
			return nil, parser.errorf("bad number %q", token.Text)
		}
		if err := parser.advance(); err != nil {
			return nil, err
		}
		return &ast.Literal{Value: value.Real(number)}, nil

	case scanner.String:
		if err := parser.advance(); err != nil {
			return nil, err
		}
		return &ast.Literal{Value: value.Text(token.Text)}, nil

	case scanner.Minus:
		if err := parser.advance(); err != nil {
			return nil, err
		}
		operand, err := parser.parseExpr(unaryPowerNegate)
		if err != nil {
			return nil, err
		}
		return &ast.Unary{Operator: ast.OpNegate, Operand: operand}, nil

	case scanner.Plus:
		// Unary plus is a no-op, so it is consumed and discarded rather than
		// given a node nothing would ever do anything with.
		if err := parser.advance(); err != nil {
			return nil, err
		}
		return parser.parseExpr(unaryPowerNegate)

	case scanner.LeftParen:
		if err := parser.advance(); err != nil {
			return nil, err
		}
		// Inside parentheses the threshold drops back to 0, which is what
		// makes (1+2)*3 group the way the brackets say.
		inner, err := parser.parseExpr(0)
		if err != nil {
			return nil, err
		}
		if parser.current.Type != scanner.RightParen {
			return nil, parser.errorf("expected ) but found %s", parser.current)
		}
		if err := parser.advance(); err != nil {
			return nil, err
		}
		return inner, nil

	case scanner.Keyword:
		switch token.Text {
		case "NULL":
			if err := parser.advance(); err != nil {
				return nil, err
			}
			return &ast.Literal{Value: value.Null()}, nil

		case "NOT":
			if err := parser.advance(); err != nil {
				return nil, err
			}
			operand, err := parser.parseExpr(unaryPowerNot)
			if err != nil {
				return nil, err
			}
			return &ast.Unary{Operator: ast.OpNot, Operand: operand}, nil
		}
	}
	return nil, parser.errorf("unexpected %s at start of expression", token)
}

// parseIsNull handles the postfix IS NULL and IS NOT NULL, given the
// expression already parsed to its left.
func (parser *Parser) parseIsNull(operand ast.Expr) (ast.Expr, error) {
	if err := parser.advance(); err != nil { // consume IS
		return nil, err
	}

	negated := false
	if parser.current.Type == scanner.Keyword && parser.current.Text == "NOT" {
		negated = true
		if err := parser.advance(); err != nil {
			return nil, err
		}
	}

	if parser.current.Type != scanner.Keyword || parser.current.Text != "NULL" {
		return nil, parser.errorf("expected NULL after IS but found %s", parser.current)
	}
	if err := parser.advance(); err != nil {
		return nil, err
	}
	return &ast.IsNull{Operand: operand, Negated: negated}, nil
}

// advance moves current to the next token.
func (parser *Parser) advance() error {
	token, err := parser.scanner.Next()
	if err != nil {
		return err
	}
	parser.current = token
	return nil
}

// errorf builds an error naming where in the input it happened.
func (parser *Parser) errorf(format string, args ...any) error {
	return fmt.Errorf("line %d, column %d: %s",
		parser.current.Line, parser.current.Column, fmt.Sprintf(format, args...))
}
