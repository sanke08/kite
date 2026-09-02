package scanner

import "strings"

// keywords is the set of words SQL reserves. The key is the word upper-cased;
// the bool is always true and carries no information — Go has no set type, so
// a map to bool is how membership gets expressed.
//
// WHY: a lookup table, not a decision made while reading. A word is read the
// same way whether it turns out to be SELECT or users: letters until they
// stop. Only then does the scanner ask this table which it was.
//
// TRAP: the alternative is matching keywords character by character while
// scanning — spot S, then E, then L. That needs backtracking the moment the
// word turns out to be "selected", because by then four characters have been
// consumed on the assumption of SELECT. Read the whole word first, classify
// second, and there is nothing to back out of.
//
// EG: what a lookup does and does not do
//
//	"SELECT"    in the table  -> Keyword
//	"select"    upper-cases to SELECT, in the table -> Keyword
//	"users"     not in the table -> Identifier
//	"selected"  not in the table -> Identifier, no partial match on SELECT
//
// The list holds only the words the parser understands today, and grows one
// entry at a time. A word missing from it becomes an identifier, which fails
// usefully: "no such column: LIMIT" points at the real problem, where a
// scanner that panicked on an unknown word would not.
var keywords = map[string]bool{
	"SELECT": true,
	"FROM":   true,
	"WHERE":  true,
	"INSERT": true,
	"INTO":   true,
	"VALUES": true,
	"CREATE": true,
	"TABLE":  true,
	"DROP":   true,
	"UPDATE": true,
	"SET":    true,
	"DELETE": true,
	"AND":    true,
	"OR":     true,
	"NOT":    true,
	"NULL":   true,
	"AS":     true,
	"IS":     true,
}

// lookupWord classifies a bare word — one the scanner read without quotes —
// as either a keyword or an identifier, and returns the text to store with it.
//
// EG: the same call site handles both, and the case handling differs
//
//	lookupWord("select")   -> Keyword,    "SELECT"    upper-cased
//	lookupWord("SeLeCt")   -> Keyword,    "SELECT"    same result
//	lookupWord("MyTable")  -> Identifier, "MyTable"   case preserved
//
// WHY the two are treated differently: a keyword is upper-cased so the parser
// compares against exactly one form — otherwise every check would need its
// own case-insensitive comparison, and one of them would eventually be
// written with ==. An identifier keeps the case typed, because that is the
// name the user sees back in a result header or an error message; folding
// MyTable to MYTABLE would be a visible wrong answer.
//
// Quoted names never reach here. "select" in double quotes is an identifier
// by virtue of its quoting, so the scanner handles it separately — which is
// the only way to have a column actually named select.
func lookupWord(word string) (Type, string) {
	upper := strings.ToUpper(word)
	if keywords[upper] {
		return Keyword, upper
	}
	return Identifier, word
}
