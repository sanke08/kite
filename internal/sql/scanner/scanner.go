package scanner

import (
	"fmt"
	"strings"
)

// WALKTHROUGH — scanning SELECT 'hi' from the caller's first Next() call:
//
//	Next()                  skips leading space, looks at the first byte
//	  skipSpaceAndComments  nothing to skip here
//	  isWordStart('S')      yes, so it is a word
//	  scanWord              reads SELECT, asks lookupWord -> Keyword
//	Next()                  called again
//	  peek() is '\''         so it is a string literal
//	  scanString            reads to the closing quote -> String("hi")
//	Next()                  atEnd() -> EOF, forever after
//
// Every scanX method below is reached from that one switch in Next, and the
// file is ordered to match: Next first, then the scanX methods in the order
// the switch tries them, then the byte-level helpers they all share.

// Scanner reads SQL text one token at a time.
//
// WHY: one token per call, not a []Token built up front. The parser needs one
// token of lookahead and never more, so a full slice would be work nobody
// asked for. And a query that fails to scan halfway through reports the
// failure at that point rather than after assembling a list that gets thrown
// away — which matters because the error carries the line and column of the
// byte that broke.
//
// EG: all four fields, scanning "SELECT 1" and having just read SELECT
//
//	input     "SELECT 1"      the whole text, unchanged, start to finish
//	           012345 6       byte indexes
//	position   6              next byte to read: the space
//	line       1              still on the first line
//	column     7              7th character, because columns count from 1
//
// Note position is 6 while column is 7 for the same byte. position indexes
// into a Go string, which starts at 0. column is what a person counts, which
// starts at 1. They differ by one on line 1 and diverge completely after a
// newline, which is why both exist.
type Scanner struct {
	// input is the entire SQL text, never modified and never consumed. The
	// scanner only reads it; tokens take substrings of it or build new
	// strings, so it looks the same at EOF as it did at the start.
	//
	// EG: one line typed at the REPL
	//
	//	input = "SELECT a FROM t"
	//
	// or several lines from a file, newlines included:
	//
	//	input = "SELECT a\nFROM t"
	input string

	// position is the index of the next byte to read — the cursor into input.
	// Everything before it is already a token; everything from it on has not
	// been looked at.
	//
	// EG: reading "SELECT 1" one token at a time
	//
	//	position 0   at the start, nothing read
	//	position 6   SELECT is a token, next byte is the space
	//	position 7   space skipped, next byte is the 1
	//	position 8   == len(input), so atEnd() is true
	position int

	// line is which line of input position sits on, counting from 1.
	//
	// EG: input "SELECT a\nFROM t"
	//
	//	while reading SELECT and a   line == 1
	//	after advance() eats the \n  line == 2
	//	while reading FROM and t     line == 2
	//
	// It only ever increases, and only inside advance().
	line int

	// column is how far along the current line position sits, counting from 1
	// so that it matches what an editor shows. It RESETS to 1 on every
	// newline, which is what makes it a column rather than a byte offset.
	//
	// EG: input "SELECT a\nFROM t", the second line
	//
	//	byte index 9 is the F     column == 1, not 10
	//	byte index 13 is the t    column == 5
	//
	// WHY line and column are maintained here rather than derived when
	// needed: computing the line of byte 4,000 means counting newlines from
	// the start of the input, every single time an error is raised. advance()
	// keeps both current for the cost of one increment per byte.
	column int
}

// New returns a Scanner positioned at the start of input.
//
// WHY a constructor rather than a usable zero value: line and column count
// from 1, so a zero-valued Scanner would report every token as being on line
// 0 — off by one in every error message the parser ever prints.
func New(input string) *Scanner {
	return &Scanner{input: input, line: 1, column: 1}
}

// Next returns the next token, or EOF once the input is exhausted — and EOF
// on every call after that, not just the first.
//
// The switch is the whole design: ONE byte decides which kind of token
// follows, with no backtracking anywhere. A digit starts a number, a quote
// starts a literal, a letter starts a word, anything else is punctuation.
//
// EG: the first byte of each of these settles it
//
//	42       '4' is a digit        -> scanNumber
//	'hi'     '\'' is a quote       -> scanString
//	"order"  '"' is a double quote -> scanQuotedIdentifier
//	users    'u' is a letter       -> scanWord
//	<=       none of the above     -> scanOperator
//
// WHY startLine and startColumn are captured before dispatching: the scanX
// methods advance position, so by the time one of them builds its token the
// scanner's own line and column point at the character AFTER the token. An
// error about an unterminated string has to name where the string opened,
// not where the input ran out.
func (scanner *Scanner) Next() (Token, error) {
	scanner.skipSpaceAndComments()

	if scanner.atEnd() {
		return scanner.make(EOF, ""), nil
	}

	startLine, startColumn := scanner.line, scanner.column
	character := scanner.peek()

	switch {
	case isDigit(character):
		return scanner.scanNumber(startLine, startColumn)
	case character == '\'':
		return scanner.scanString(startLine, startColumn)
	case character == '"':
		return scanner.scanQuotedIdentifier(startLine, startColumn)
	case isWordStart(character):
		return scanner.scanWord(startLine, startColumn), nil
	}
	return scanner.scanOperator(startLine, startColumn)
}

// scanNumber reads an integer or a float.
//
// WHY: the token type is decided by whether a dot appeared, not by the value.
// SQL treats 1 and 1.0 as different storage classes, and collapsing them here
// would lose that before the evaluator ever sees it.
//
// EG: "42" -> Integer("42"),  "2.5" -> Float("2.5"),  "1." -> Float("1.")
func (scanner *Scanner) scanNumber(line, column int) (Token, error) {
	start := scanner.position
	tokenType := Integer

	for !scanner.atEnd() && isDigit(scanner.peek()) {
		scanner.advance()
	}
	if !scanner.atEnd() && scanner.peek() == '.' {
		tokenType = Float
		scanner.advance()
		for !scanner.atEnd() && isDigit(scanner.peek()) {
			scanner.advance()
		}
	}
	return Token{tokenType, scanner.input[start:scanner.position], line, column}, nil
}

// scanString reads a single-quoted string literal.
//
// WHY: SQL escapes a quote by DOUBLING it, and has no backslash escaping at
// all. Two quotes inside a literal mean one literal quote character.
//
// EG: what the scanner reads, and the one token it produces
//
//	input   'it''s'          6 characters on the line
//	token   String("it's")   4 characters of text, the pair collapsed
//
//	input   'a\nb'           SQL has no escapes: 5 characters
//	token   String("a\\nb")   backslash and n, NOT a newline
//
// TRAP: the obvious loop stops at the first closing quote and returns
// String("it"), leaving s' unread. The parser then sees a stray s and
// reports a syntax error three characters past the real cause.
func (scanner *Scanner) scanString(line, column int) (Token, error) {
	scanner.advance() // opening quote

	var text strings.Builder
	for {
		if scanner.atEnd() {
			return Token{}, fmt.Errorf("line %d, column %d: unterminated string", line, column)
		}

		character := scanner.advance()
		if character != '\'' {
			text.WriteByte(character)
			continue
		}

		// A quote: either the end of the string, or the first half of an
		// escaped quote.
		if !scanner.atEnd() && scanner.peek() == '\'' {
			scanner.advance()
			text.WriteByte('\'')
			continue
		}
		return Token{String, text.String(), line, column}, nil
	}
}

// scanQuotedIdentifier reads a double-quoted name.
//
// WHY: double quotes mean an IDENTIFIER in SQL, not a string. "order" is a
// column named order; 'order' is the six-character text. Treating them the
// same is a silent wrong-answer bug, never a crash.
//
// EG: SELECT "order" FROM t     -> Identifier("order")
//
//	SELECT 'order' FROM t     -> String("order")
func (scanner *Scanner) scanQuotedIdentifier(line, column int) (Token, error) {
	scanner.advance() // opening quote

	var text strings.Builder
	for {
		if scanner.atEnd() {
			return Token{}, fmt.Errorf("line %d, column %d: unterminated quoted identifier", line, column)
		}

		character := scanner.advance()
		if character != '"' {
			text.WriteByte(character)
			continue
		}
		// A doubled double-quote is one literal quote, same rule as strings.
		if !scanner.atEnd() && scanner.peek() == '"' {
			scanner.advance()
			text.WriteByte('"')
			continue
		}
		return Token{Identifier, text.String(), line, column}, nil
	}
}

// scanWord reads an unquoted word and lets lookupWord classify it.
//
// It returns no error because there is no way to fail: the caller only
// dispatches here when the first byte is already a valid word start, and the
// loop simply stops at the first byte that is not part of a word.
//
// EG: "users WHERE" from position 0 reads users and stops at the space.
//
//	"a1_b" reads all four bytes — digits and underscores are word parts,
//	just not word starts.
func (scanner *Scanner) scanWord(line, column int) Token {
	start := scanner.position
	for !scanner.atEnd() && isWordPart(scanner.peek()) {
		scanner.advance()
	}
	tokenType, text := lookupWord(scanner.input[start:scanner.position])
	return Token{tokenType, text, line, column}
}

// scanOperator reads punctuation and operators.
//
// EG: the same first byte can start two different tokens
//
//	<      Less          nothing useful follows
//	<=     LessEqual     one token, not Less then Equal
//	<>     NotEqual      SQL's older spelling of !=
//	|      an error      SQL has no single-pipe operator
//	||     Concat        string concatenation
//
// TRAP: the two-character forms must be tested BEFORE the one-character ones.
// Checking '<' first turns <= into Less followed by Equal, and the parser
// then sees "a < = 5" — a syntax error one character from a valid query.
func (scanner *Scanner) scanOperator(line, column int) (Token, error) {
	character := scanner.advance()

	switch character {
	case '+':
		return Token{Plus, "+", line, column}, nil
	case '-':
		return Token{Minus, "-", line, column}, nil
	case '*':
		return Token{Star, "*", line, column}, nil
	case '/':
		return Token{Slash, "/", line, column}, nil
	case '%':
		return Token{Percent, "%", line, column}, nil
	case '(':
		return Token{LeftParen, "(", line, column}, nil
	case ')':
		return Token{RightParen, ")", line, column}, nil
	case ',':
		return Token{Comma, ",", line, column}, nil
	case ';':
		return Token{Semicolon, ";", line, column}, nil
	case '.':
		return Token{Dot, ".", line, column}, nil
	case '=':
		return Token{Equal, "=", line, column}, nil

	case '|':
		if !scanner.atEnd() && scanner.peek() == '|' {
			scanner.advance()
			return Token{Concat, "||", line, column}, nil
		}
		return Token{}, fmt.Errorf("line %d, column %d: single | is not an operator, did you mean ||", line, column)

	case '<':
		if !scanner.atEnd() && scanner.peek() == '=' {
			scanner.advance()
			return Token{LessEqual, "<=", line, column}, nil
		}
		if !scanner.atEnd() && scanner.peek() == '>' {
			scanner.advance()
			return Token{NotEqual, "<>", line, column}, nil
		}
		return Token{Less, "<", line, column}, nil

	case '>':
		if !scanner.atEnd() && scanner.peek() == '=' {
			scanner.advance()
			return Token{GreaterEqual, ">=", line, column}, nil
		}
		return Token{Greater, ">", line, column}, nil

	case '!':
		if !scanner.atEnd() && scanner.peek() == '=' {
			scanner.advance()
			return Token{NotEqual, "!=", line, column}, nil
		}
		return Token{}, fmt.Errorf("line %d, column %d: single ! is not an operator, did you mean !=", line, column)
	}
	return Token{}, fmt.Errorf("line %d, column %d: unexpected character %q", line, column, character)
}

// skipSpaceAndComments advances past anything that is not a token.
//
// EG: everything between SELECT and 1 here is skipped in one call
//
//	input   "SELECT  -- note\n  /* and */  1"
//	                 ^--------------------^  all of this vanishes
//	result  position lands on the 1
//
// WHY one loop for both rather than two passes: a comment can be followed by
// whitespace followed by another comment. Handling them separately would mean
// the caller asks "skip space", then "skip comments", then has to ask again —
// and the loop is what makes one call enough.
func (scanner *Scanner) skipSpaceAndComments() {
	for !scanner.atEnd() {
		character := scanner.peek()

		// The four bytes SQL treats as nothing. Each is a single invisible
		// character, written with a backslash because it cannot be typed
		// between quotes:
		//
		//	' '     space             0x20   the spacebar
		//	'\t'    tab               0x09   the Tab key
		//	'\n'    newline           0x0A   the Enter key
		//	'\r'    carriage return   0x0D   moves to start of line
		if character == ' ' || character == '\t' || character == '\n' || character == '\r' {
			scanner.advance()
			continue
		}

		// A line comment: -- and everything after it, up to the newline.
		//
		// The newline itself is deliberately NOT consumed here — the outer
		// loop comes back round and the whitespace branch above takes it,
		// which is what keeps advance() the only thing incrementing line.
		//
		// EG: "SELECT 1 -- pick one\nFROM t"
		//              ^-----------^  skipped, newline left for the loop
		//
		// TRAP: a single - is subtraction. peekAhead(1) is what separates
		// "a--b" (a comment) from "a - -b" (subtract negative b), and
		// checking only the first byte would silently swallow the rest of
		// any line containing a minus sign.
		if character == '-' && scanner.peekAhead(1) == '-' {
			for !scanner.atEnd() && scanner.peek() != '\n' {
				scanner.advance()
			}
			continue
		}

		// A block comment: /* through */, which may span lines.
		//
		// It does NOT nest — /* a /* b */ ends at the first */, leaving a
		// stray "b */" for the scanner to choke on. That matches SQL, and
		// supporting nesting would mean a depth counter for a case the
		// corpus never exercises.
		//
		// EG: one string containing a newline inside the comment
		//
		//	input   "SELECT 1 /* two\nlines */ + 2"
		//	tokens  SELECT 1 + 2
		//
		// The newline inside the comment still goes through advance(), so
		// line becomes 2 and any later error reports the right one. Note the
		// REPL cannot show this: it hands over one line at a time, so a
		// comment spanning lines only reaches the scanner whole when the SQL
		// comes from a file.
		//
		// An unterminated /* runs to end of input and is not an error here.
		// The tokens simply stop, and the parser reports the incomplete
		// statement — which is a better message than the scanner could give.
		if character == '/' && scanner.peekAhead(1) == '*' {
			scanner.advance()
			scanner.advance()
			for !scanner.atEnd() {
				if scanner.peek() == '*' && scanner.peekAhead(1) == '/' {
					scanner.advance()
					scanner.advance()
					break
				}
				scanner.advance()
			}
			continue
		}
		return
	}
}

// atEnd reports whether position is at or past the end of input.
func (scanner *Scanner) atEnd() bool {
	return scanner.position >= len(scanner.input)
}

// peek returns the byte at the current position without advancing.
func (scanner *Scanner) peek() byte {
	return scanner.input[scanner.position]
}

// peekAhead returns the byte n positions ahead, or 0 past the end.
//
// EG: input "a<", position 1 (on the <)
//
//	peekAhead(0)   '<'   the byte at position
//	peekAhead(1)   0     nothing there, so not <= or <>
//
// WHY 0 rather than an error: every caller is asking "is the next character
// part of a two-character token". Past the end the answer is simply no, and 0
// is never a byte SQL contains — so the caller needs no special case.
func (scanner *Scanner) peekAhead(n int) byte {
	if scanner.position+n >= len(scanner.input) {
		return 0
	}
	return scanner.input[scanner.position+n]
}

// advance consumes one byte, returns it, and keeps line and column current.
//
// This is the only place position moves, which is what makes the line and
// column trustworthy — a scanX method that stepped position directly would
// leave them silently stale, and every error message after that point in the
// input would name the wrong place.
//
// EG: advancing over the newline in "a\nb" takes line from 1 to 2 and resets
// column to 1, so the b is reported as line 2 column 1.
func (scanner *Scanner) advance() byte {
	character := scanner.input[scanner.position]
	scanner.position++

	if character == '\n' {
		scanner.line++
		scanner.column = 1
	} else {
		scanner.column++
	}
	return character
}

// make builds a token at the scanner's CURRENT position, for tokens whose
// position is not captured beforehand. Only EOF uses it — every other token
// is built with the line and column saved before scanning started.
func (scanner *Scanner) make(tokenType Type, text string) Token {
	return Token{tokenType, text, scanner.line, scanner.column}
}

func isDigit(character byte) bool {
	return character >= '0' && character <= '9'
}

// isWordStart reports whether a character can begin an unquoted name.
//
// EG: what may and may not begin a name
//
//	users    'u'   yes, a letter
//	_hidden  '_'   yes, underscore is deliberate
//	1abc     '1'   NO — a digit means scanNumber runs instead
//
// WHY the digit is excluded: it is the only thing keeping 1 a number. Allow
// digits to start a word and "1" would scan as an identifier named 1 rather
// than an integer literal. As written, 1abc scans as INTEGER("1") followed by
// IDENT("abc") — two tokens the parser rejects, which is the right failure.
//
// isWordPart is the looser rule for every byte AFTER the first, which is why
// a1_b is a valid name while 1a_b is not.
func isWordStart(character byte) bool {
	return character == '_' ||
		(character >= 'a' && character <= 'z') ||
		(character >= 'A' && character <= 'Z')
}

func isWordPart(character byte) bool {
	return isWordStart(character) || isDigit(character)
}
