package value

import (
	"strconv"
	"strings"
)

// Compare orders two values the way SQL does, returning -1, 0 or 1.
//
// It must not be called with NULL on either side — comparing against NULL
// yields Unknown, which an int cannot express. Equal and Less handle that
// before getting here.
//
// EG: the sort order across kinds, which is fixed by SQL and not by value
//
//	NULL  <  numbers  <  text  <  blobs
//
// So Integer(999) sorts BEFORE Text("a"), because a number is a number and
// text is text — the comparison never asks which "looks bigger".
//
// WHY kind order comes first: without it, comparing 1 against 'a' has no
// defined answer, and a sort would produce a different order depending on
// which elements happened to be compared. SQLite fixes the order, and since
// the corpus's expected values come from SQLite, matching it is not optional.
func Compare(left, right Value) int {
	leftRank, rightRank := kindRank(left.Kind), kindRank(right.Kind)
	// Are these even the same species? If not, the species decides.
	// left  = Integer(999)   → kindRank = 1
	// right = Text("a")      → kindRank = 2
	// 1 != 2   →  different species!
	if leftRank != rightRank {
		return compareInt(leftRank, rightRank)
	}

	switch left.Kind {
	case KindInteger:
		if right.Kind == KindInteger {
			return compareInt64(left.integer, right.integer)
		}
		return compareFloat(left.Real(), right.Real())

	case KindReal:
		return compareFloat(left.Real(), right.Real())

	case KindText, KindBlob:
		return strings.Compare(left.text, right.text)
	}
	return 0
}

// kindRank gives each kind its position in SQL's cross-kind ordering.
//
// EG: kindRank(KindNull) is 0, kindRank(KindText) is 2
//
// Integer and Real share rank 1 so that 1 and 1.0 compare as numbers rather
// than by kind — otherwise every integer would sort before every real.
func kindRank(kind Kind) int {
	switch kind {
	case KindNull:
		return 0
	case KindInteger, KindReal:
		return 1
	case KindText:
		return 2
	}
	return 3
}

// Equal compares two values for equality, in three-valued logic.
//
// EG: the case that makes this return Truth instead of bool
//
//	Equal(Integer(1), Integer(1))    True
//	Equal(Integer(1), Integer(2))    False
//	Equal(Integer(1), Null())        Unknown   <- not False
//	Equal(Null(), Null())            Unknown   <- NULL is not equal to itself
//
// WHY NULL = NULL is Unknown rather than True: NULL means "unknown value".
// Two unknown values might be the same or might not, so the comparison cannot
// claim they match. That is why IS NULL exists as separate syntax — it is the
// only way to test for NULL at all.
//
// TRAP: GROUP BY and DISTINCT do the opposite and treat all NULLs as one
// group. That asymmetry is real SQL, not an inconsistency to smooth over.
func Equal(left, right Value) Truth {
	if left.IsNull() || right.IsNull() {
		return Unknown
	}
	return Bool(Compare(left, right) == 0)
}

// Less reports whether left sorts before right, in three-valued logic.
func Less(left, right Value) Truth {
	if left.IsNull() || right.IsNull() {
		return Unknown
	}
	return Bool(Compare(left, right) < 0)
}

// NumericPrefix reads a number from the front of text and reports whether it
// was a whole number with no decimal point.
//
// WHY the second return: text coerced in arithmetic keeps its integerness.
// '12abc' + 1 is 13 in SQL, not 13.0 — the text read as an integer, so the
// addition stayed integer arithmetic.
//
// EG: "12abc" -> 12, true     no decimal point, so an integer
//
//	"2.5"   -> 2.5, false   a real
//	"abc"   -> 0, true      nothing numeric becomes integer zero
func NumericPrefix(text string) (float64, bool) {
	number, ok := parseNumericPrefix(text)
	if !ok {
		return 0, true
	}
	return number, !strings.ContainsAny(text, ".eE")
}

// parseNumericPrefix reads a number from the FRONT of text, ignoring whatever
// follows.
//
// WHY a prefix rather than requiring the whole string to be a number: SQL
// coerces text to a number by reading as far as it can and stopping. This is
// the rule behind '12abc' + 1 being 13 rather than an error.
//
// EG:
//
//	"12"      -> 12, true
//	"12abc"   -> 12, true      stops at the a
//	" 12"     -> 12, true      leading space skipped
//	"2.5"     -> 2.5, true
//	"abc"     ->  0, false     nothing numeric at the front
//	""        ->  0, false
func parseNumericPrefix(text string) (float64, bool) {
	trimmed := strings.TrimLeft(text, " \t\n\r")

	// end walks forward while the characters still form a valid number.
	end := 0
	if end < len(trimmed) && (trimmed[end] == '+' || trimmed[end] == '-') {
		end++
	}

	digitsSeen := false
	for end < len(trimmed) && isDigit(trimmed[end]) {
		end++
		digitsSeen = true
	}
	if end < len(trimmed) && trimmed[end] == '.' {
		end++
		for end < len(trimmed) && isDigit(trimmed[end]) {
			end++
			digitsSeen = true
		}
	}
	if !digitsSeen {
		return 0, false
	}

	number, err := strconv.ParseFloat(trimmed[:end], 64)
	if err != nil {
		return 0, false
	}
	return number, true
}

func isDigit(character byte) bool {
	return character >= '0' && character <= '9'
}

func compareInt(left, right int) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	}
	return 0
}

func compareInt64(left, right int64) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	}
	return 0
}

func compareFloat(left, right float64) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	}
	return 0
}
