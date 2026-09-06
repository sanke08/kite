package parser

import (
	"github.com/sanke08/kite/internal/sql/ast"
	"github.com/sanke08/kite/internal/sql/scanner"
)

// WALKTHROUGH — parsing CREATE TABLE t(a INTEGER):
//
//	ParseStmt          looks at the first keyword to pick a branch
//	  parseCreate      consumes CREATE, then expects TABLE
//	    parseColumnDefs  consumes ( then reads name/type pairs until )
//	  -> &ast.CreateTable{Name: "t", Columns: [{a, INTEGER}]}

// ParseStmt parses one complete statement.
//
// WHY the FIRST keyword decides everything: SQL statements are prefix-
// distinguished — no two start with the same word. So one token of lookahead
// picks the branch, with no backtracking, exactly like the scanner's dispatch
// on one byte.
func (parser *Parser) ParseStmt() (ast.Stmt, error) {
	if parser.current.Type != scanner.Keyword {
		return nil, parser.errorf("expected a statement, found %s", parser.current)
	}

	switch parser.current.Text {
	case "CREATE":
		return parser.parseCreate()
	case "DROP":
		return parser.parseDrop()
	case "SELECT":
		return parser.parseSelect()
	}
	return nil, parser.errorf("unsupported statement %s", parser.current.Text)
}

// parseCreate handles CREATE TABLE name (col type, ...).
func (parser *Parser) parseCreate() (ast.Stmt, error) {
	if err := parser.advance(); err != nil { // consume CREATE
		return nil, err
	}
	if err := parser.expectKeyword("TABLE"); err != nil {
		return nil, err
	}

	name, err := parser.parseName()
	if err != nil {
		return nil, err
	}

	columns, err := parser.parseColumnDefs()
	if err != nil {
		return nil, err
	}
	if err := parser.expectEnd(); err != nil {
		return nil, err
	}
	return &ast.CreateTable{Name: name, Columns: columns}, nil
}

// parseColumnDefs reads the parenthesised column list.
//
// EG: (a INTEGER, b VARCHAR(255), c)
//
//	-> [{a, "INTEGER"}, {b, "VARCHAR(255)"}, {c, ""}]
//
// The third column has no declared type at all, which SQL permits. An empty
// type is not an error — the catalog gives it Blob affinity, meaning values
// are stored exactly as supplied.
func (parser *Parser) parseColumnDefs() ([]ast.ColumnDef, error) {
	if parser.current.Type != scanner.LeftParen {
		return nil, parser.errorf("expected ( after table name, found %s", parser.current)
	}
	if err := parser.advance(); err != nil {
		return nil, err
	}

	var columns []ast.ColumnDef
	for {
		name, err := parser.parseName()
		if err != nil {
			return nil, err
		}

		declaredType, err := parser.parseTypeName()
		if err != nil {
			return nil, err
		}
		columns = append(columns, ast.ColumnDef{Name: name, Type: declaredType})

		if parser.current.Type == scanner.Comma {
			if err := parser.advance(); err != nil {
				return nil, err
			}
			continue
		}
		break
	}

	if parser.current.Type != scanner.RightParen {
		return nil, parser.errorf("expected , or ) in column list, found %s", parser.current)
	}
	if err := parser.advance(); err != nil {
		return nil, err
	}
	return columns, nil
}

// parseTypeName reads a column's declared type, which may be absent, several
// words long, and may carry a parenthesised size.
//
// EG: what counts as one type name
//
//	INTEGER              one word
//	DOUBLE PRECISION     two words, both part of the type
//	VARCHAR(255)         a word and a size
//	DECIMAL(10, 5)       a word and two sizes
//	(nothing)            allowed — returns ""
//
// WHY the size is kept in the text rather than parsed into numbers: nothing in
// this engine enforces a length. VARCHAR(255) and VARCHAR(9) both have Text
// affinity and both store a string of any length. The digits are preserved
// only so .schema can print back what was typed.
func (parser *Parser) parseTypeName() (string, error) {
	var typeName string

	// Type words run until a comma, a closing paren, or the end.
	for parser.current.Type == scanner.Identifier || parser.current.Type == scanner.Keyword {
		if typeName != "" {
			typeName += " "
		}
		typeName += parser.current.Text
		if err := parser.advance(); err != nil {
			return "", err
		}
	}

	if parser.current.Type != scanner.LeftParen {
		return typeName, nil
	}

	// A size: copy through to the matching close paren without interpreting it.
	typeName += "("
	if err := parser.advance(); err != nil {
		return "", err
	}
	for parser.current.Type != scanner.RightParen {
		if parser.current.Type == scanner.EOF {
			return "", parser.errorf("unclosed ( in column type")
		}
		if parser.current.Type == scanner.Comma {
			typeName += ","
		} else {
			typeName += parser.current.Text
		}
		if err := parser.advance(); err != nil {
			return "", err
		}
	}
	typeName += ")"
	if err := parser.advance(); err != nil {
		return "", err
	}
	return typeName, nil
}

// parseDrop handles DROP TABLE name.
func (parser *Parser) parseDrop() (ast.Stmt, error) {
	if err := parser.advance(); err != nil { // consume DROP
		return nil, err
	}
	if err := parser.expectKeyword("TABLE"); err != nil {
		return nil, err
	}

	name, err := parser.parseName()
	if err != nil {
		return nil, err
	}
	if err := parser.expectEnd(); err != nil {
		return nil, err
	}
	return &ast.DropTable{Name: name}, nil
}

// parseSelect handles SELECT with a list of expressions and no FROM.
//
// TEMPORARY: no FROM clause, because there is nothing to select from until
// tables can hold rows. The expression list is real, so SELECT 1+2, 'x' works.
func (parser *Parser) parseSelect() (ast.Stmt, error) {
	if err := parser.advance(); err != nil { // consume SELECT
		return nil, err
	}

	var columns []ast.Expr
	for {
		expression, err := parser.parseExpr(0)
		if err != nil {
			return nil, err
		}
		columns = append(columns, expression)

		if parser.current.Type != scanner.Comma {
			break
		}
		if err := parser.advance(); err != nil {
			return nil, err
		}
	}
	if err := parser.expectEnd(); err != nil {
		return nil, err
	}
	return &ast.Select{Columns: columns}, nil
}

// parseName reads a table or column name.
//
// WHY a keyword is rejected here rather than accepted: CREATE TABLE select(...)
// would otherwise parse, and then every later SELECT against it would be
// ambiguous. The double-quoted form "select" still works, because the scanner
// already classified it as an identifier.
func (parser *Parser) parseName() (string, error) {
	if parser.current.Type != scanner.Identifier {
		return "", parser.errorf("expected a name, found %s", parser.current)
	}
	name := parser.current.Text
	if err := parser.advance(); err != nil {
		return "", err
	}
	return name, nil
}

// expectKeyword consumes the named keyword or reports what was there instead.
func (parser *Parser) expectKeyword(word string) error {
	if parser.current.Type != scanner.Keyword || parser.current.Text != word {
		return parser.errorf("expected %s, found %s", word, parser.current)
	}
	return parser.advance()
}

// expectEnd checks the statement is over: an optional semicolon, then EOF.
//
// WHY this is an error rather than ignoring the rest: "CREATE TABLE t(a) junk"
// silently succeeding would leave the user thinking junk meant something.
func (parser *Parser) expectEnd() error {
	if parser.current.Type == scanner.Semicolon {
		if err := parser.advance(); err != nil {
			return err
		}
	}
	if parser.current.Type != scanner.EOF {
		return parser.errorf("unexpected %s after statement", parser.current)
	}
	return nil
}
