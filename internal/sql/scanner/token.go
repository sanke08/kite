// Package scanner turns SQL text into tokens.
//
// A token is one indivisible piece of a query: a keyword, a name, a number, a
// piece of punctuation. Scanning is the first step of running any SQL, and it
// answers only "what pieces is this text made of" — never "is this a valid
// query", which is the parser's job.
//
// EG: the six characters a<=5;
//
//	IDENT("a")  <=("<=")  INTEGER("5")  ;(";")
//
// Note what scanning already decided: <= is ONE token, not < followed by =.
// And note what it did not decide: whether a<=5 makes sense as a query.
package scanner

import "fmt"

// Type is which kind of token this is — the answer to "what did the scanner
// just read", independent of the characters it read.
//
// WHY: an int with named constants rather than storing the token's kind as a
// string. The parser branches on this constantly ("is the next token a
// keyword?"), and comparing an integer is both faster and impossible to
// misspell — token.Type == Keyword either compiles or does not, where
// token.Kind == "keyword" would silently never match "KEYWORD".
//
// EG: the same characters can produce different types, and different
// characters the same type:
//
//	'order'    -> String       the six-letter text
//	"order"    -> Identifier   a column named order
//	order      -> Identifier   also a column named order
//	SELECT     -> Keyword
//	users      -> Identifier   only because SELECT is in the keyword table
//
// The constants below are grouped by what the parser does with them:
// literals and names carry a value, operators and punctuation do not.
type Type int

const (
	// EOF means the input is exhausted. It is returned forever after that,
	// not just once.
	//
	// WHY: the parser hits the end of input in dozens of places — mid
	// expression, mid column list, after a keyword. A token that keeps
	// answering "nothing left" lets every one of those read the next token
	// unconditionally and check its type, instead of every one of them
	// checking a separate "is there more" flag first. One forgotten check
	// would be an index-out-of-range on a query missing its last character.
	//
	// It is deliberately the zero value, so a Token{} that was never filled
	// in reads as EOF rather than as an Integer.
	EOF Type = iota

	// Literals and names — the types whose Text carries information the
	// parser cannot reconstruct from the type alone.
	//
	// Integer and Float are separate because SQL stores them as different
	// things: 1 and 1.0 are not the same value, and collapsing them here
	// would lose that before the evaluator ever sees it.
	Integer    // 42
	Float      // 2.5
	String     // 'text'          quoting removed, '' already collapsed
	Identifier // users, "order"  a table or column name
	Keyword    // SELECT, FROM    upper-cased, so the parser compares one form

	// Operators. Text is fixed by the type, so the parser never reads it.
	Plus         // +
	Minus        // -
	Star         // *
	Slash        // /
	Percent      // %
	Concat       // ||
	Equal        // =
	NotEqual     // <> or !=
	Less         // <
	LessEqual    // <=
	Greater      // >
	GreaterEqual // >=

	// Punctuation. Structure rather than computation — these tell the parser
	// where an expression or a list begins and ends.
	LeftParen  // (
	RightParen // )
	Comma      // ,
	Semicolon  // ;
	Dot        // .
)

// Token is one piece of SQL text, together with what kind of piece it is and
// where it came from.
//
// EG: scanning WHERE x <= 5 produces four of these:
//
//	{Keyword,    "WHERE", line 1, column 1}
//	{Identifier, "x",     line 1, column 7}
//	{LessEqual,  "<=",    line 1, column 9}
//	{Integer,    "5",     line 1, column 12}
//
// Text holds "5" as a STRING, not the number 5. Converting to a number needs
// to know the column's type, which the scanner cannot know — so it hands the
// digits along unchanged and the evaluator decides later.
type Token struct {
	// Type is what kind of token this is. The parser reads this first and
	// only looks at Text when the type says there is a value worth reading —
	// Text on a LeftParen is always "(" and carries no information.
	Type Type

	// Text is the token's value with its quoting removed and escapes
	// resolved. For 'it''s' it is the five characters it's — the doubled
	// quote is already collapsed, so nothing downstream has to know how SQL
	// escapes anything.
	Text string

	// Line and Column are where the token started, counting from 1. The
	// parser needs them to report where an error is, and by then the source
	// text is gone.
	Line   int
	Column int
}

// String renders a token for error messages and the REPL's token dump.
//
// EG: {Keyword, "SELECT"} prints as KEYWORD("SELECT")
//
//	{Integer, "42"}     prints as INTEGER("42")
//
// The %q quoting is deliberate: it makes an empty or whitespace-only Text
// visible instead of printing as nothing.
func (token Token) String() string {
	return fmt.Sprintf("%s(%q)", token.Type, token.Text)
}

// typeNames gives each Type a printable name, indexed BY the Type value.
//
// WHY: an array indexed by the constant, not a map or a switch. Because Type
// values are consecutive integers starting at 0, typeNames[Keyword] is a
// single memory read. The indexed-literal form also puts the name next to the
// constant it belongs to, so adding a Type and forgetting its name here is
// visible rather than silent — the gap prints as Type(23).
//
// EG: typeNames[Integer] is "INTEGER", typeNames[LessEqual] is "<="
//
// Used only for error messages and debugging output. Nothing in the parser
// compares these strings — it compares Type values.
var typeNames = [...]string{
	EOF:          "EOF",
	Integer:      "INTEGER",
	Float:        "FLOAT",
	String:       "STRING",
	Identifier:   "IDENT",
	Keyword:      "KEYWORD",
	Plus:         "+",
	Minus:        "-",
	Star:         "*",
	Slash:        "/",
	Percent:      "%",
	Concat:       "||",
	Equal:        "=",
	NotEqual:     "<>",
	Less:         "<",
	LessEqual:    "<=",
	Greater:      ">",
	GreaterEqual: ">=",
	LeftParen:    "(",
	RightParen:   ")",
	Comma:        ",",
	Semicolon:    ";",
	Dot:          ".",
}

// String gives the Type's printable name.
//
// WHY: the bounds check and the empty-string check both matter. A Type added
// to the constant block but not to typeNames would otherwise either panic on
// an out-of-range index or print as an empty string — this prints Type(23),
// which names the problem.
func (tokenType Type) String() string {
	if int(tokenType) < len(typeNames) && typeNames[tokenType] != "" {
		return typeNames[tokenType]
	}
	return fmt.Sprintf("Type(%d)", int(tokenType))
}
