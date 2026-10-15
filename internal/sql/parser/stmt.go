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
	case "INSERT":
		return parser.parseInsert()
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

// parseSelect handles SELECT columns [FROM table].
//
// EG: the three shapes that work
//
//	SELECT 1+2         Columns [Binary{+}],  From ""
//	SELECT * FROM t    Columns [Star{}],     From "t"
//	SELECT a, b FROM t Columns [Column, Column], From "t"
//
// TEMPORARY: FROM takes one bare table name. It becomes a join tree when
// there is a second table to read.
func (parser *Parser) parseSelect() (ast.Stmt, error) {
	if err := parser.advance(); err != nil { // consume SELECT
		return nil, err
	}

	var columns []ast.Expr
	for {
		expression, err := parser.parseSelectColumn()
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

	from, err := parser.parseFrom()
	if err != nil {
		return nil, err
	}
	if err := parser.expectEnd(); err != nil {
		return nil, err
	}
	return &ast.Select{Columns: columns, From: from}, nil
}

// parseSelectColumn reads one entry of the select list.
//
// WHY * is handled here and not in parseExpr: a bare * is only meaningful in a
// select list. Inside an expression the same token is multiplication, and
// parseExpr must keep reading it that way — otherwise "SELECT a*b" would
// parse the * as a wildcard and then fail on the b.
func (parser *Parser) parseSelectColumn() (ast.Expr, error) {
	if parser.current.Type == scanner.Star {
		if err := parser.advance(); err != nil {
			return nil, err
		}
		return &ast.Star{}, nil
	}
	return parser.parseExpr(0)
}

// parseFrom reads an optional FROM clause, returning "" when absent.
func (parser *Parser) parseFrom() (string, error) {
	if parser.current.Type != scanner.Keyword || parser.current.Text != "FROM" {
		return "", nil
	}
	if err := parser.advance(); err != nil {
		return "", err
	}
	return parser.parseName()
}

// parseInsert handles INSERT INTO name [(columns)] VALUES (...), (...).
//
// EG: INSERT INTO t VALUES (1, 'one'), (2, 'two')  -> two rows in one statement
func (parser *Parser) parseInsert() (ast.Stmt, error) {
	if err := parser.advance(); err != nil { // consume INSERT
		return nil, err
	}
	if err := parser.expectKeyword("INTO"); err != nil {
		return nil, err
	}

	table, err := parser.parseName()
	if err != nil {
		return nil, err
	}

	// An optional column list. Without it, values match the table's columns
	// in declaration order.
	var columns []string
	if parser.current.Type == scanner.LeftParen {
		columns, err = parser.parseNameList()
		if err != nil {
			return nil, err
		}
	}

	if err := parser.expectKeyword("VALUES"); err != nil {
		return nil, err
	}

	var rows [][]ast.Expr
	for {
		row, err := parser.parseValueRow()
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)

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
	return &ast.Insert{Table: table, Columns: columns, Rows: rows}, nil
}

// parseNameList reads a parenthesised list of names: (a, b, c).
func (parser *Parser) parseNameList() ([]string, error) {
	if err := parser.advance(); err != nil { // consume (
		return nil, err
	}

	var names []string
	for {
		name, err := parser.parseName()
		if err != nil {
			return nil, err
		}
		names = append(names, name)

		if parser.current.Type != scanner.Comma {
			break
		}
		if err := parser.advance(); err != nil {
			return nil, err
		}
	}

	if parser.current.Type != scanner.RightParen {
		return nil, parser.errorf("expected , or ) in column list, found %s", parser.current)
	}
	return names, parser.advance()
}

// parseValueRow reads one parenthesised row of VALUES: (1, 'one').
func (parser *Parser) parseValueRow() ([]ast.Expr, error) {
	if parser.current.Type != scanner.LeftParen {
		return nil, parser.errorf("expected ( to start a row of values, found %s", parser.current)
	}
	if err := parser.advance(); err != nil {
		return nil, err
	}

	var row []ast.Expr
	for {
		expression, err := parser.parseExpr(0)
		if err != nil {
			return nil, err
		}
		row = append(row, expression)

		if parser.current.Type != scanner.Comma {
			break
		}
		if err := parser.advance(); err != nil {
			return nil, err
		}
	}

	if parser.current.Type != scanner.RightParen {
		return nil, parser.errorf("expected , or ) in values, found %s", parser.current)
	}
	return row, parser.advance()
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
