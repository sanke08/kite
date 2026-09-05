package value

// Truth is a SQL truth value. There are THREE, not two.
//
// WHY not Go's bool: in SQL, NULL means "unknown", and comparing anything
// against an unknown gives an unknown answer — not false. A bool cannot hold
// that third state, so using one forces every unknown to collapse into false,
// and from then on the logic is quietly wrong rather than broken.
//
// EG: the collapse, and why it is not recoverable
//
//	1 = NULL          Unknown   correct
//	as a bool         false     the unknown is gone
//	NOT (1 = NULL)    Unknown   correct: still unknown
//	NOT false         true      wrong, and no error anywhere
//
// A WHERE clause built on that second column returns rows it should not, and
// nothing in the engine can tell afterwards that it happened.
type Truth int

const (
	// Unknown is the result of any comparison involving NULL. It is the zero
	// value so that an unset Truth is unknown rather than false — the same
	// reasoning as KindNull being the zero Kind.
	Unknown Truth = iota
	False
	True
)

// Bool converts a Go bool into a Truth. Used where a comparison genuinely has
// a definite answer, so only two of the three states can arise.
func Bool(definite bool) Truth {
	if definite {
		return True
	}
	return False
}

// And is SQL's AND over three values.
//
// EG: the table, with the two rows that make it different from Go
//
//	True  AND True     True
//	True  AND False    False
//	False AND Unknown  False     <- NOT unknown: false wins regardless
//	True  AND Unknown  Unknown
//
// WHY False AND Unknown is False: AND is true only when both sides are true,
// so one definite false settles it whatever the other side turns out to be.
// That short-circuit is why the truth table is not simply "unknown infects
// everything".
func (left Truth) And(right Truth) Truth {
	if left == False || right == False {
		return False
	}
	if left == Unknown || right == Unknown {
		return Unknown
	}
	return True
}

// Or is SQL's OR over three values.
//
// EG: the mirror of And — here it is True that settles it
//
//	False OR False     False
//	True  OR Unknown   True      <- NOT unknown: true wins regardless
//	False OR Unknown   Unknown
func (left Truth) Or(right Truth) Truth {
	if left == True || right == True {
		return True
	}
	if left == Unknown || right == Unknown {
		return Unknown
	}
	return False
}

// Not flips True and False and leaves Unknown alone.
//
// EG: NOT Unknown is Unknown — the negation of "I do not know" is still
// "I do not know", which is exactly what a bool cannot express.
func (truth Truth) Not() Truth {
	switch truth {
	case True:
		return False
	case False:
		return True
	}
	return Unknown
}

// Value converts a Truth back into a SQL Value, because SELECT 1=1 has to
// return something printable.
//
// EG: True -> Integer(1),  False -> Integer(0),  Unknown -> Null()
//
// WHY 1 and 0 rather than a dedicated boolean kind: SQL has no boolean
// storage class. SQLite returns integers from comparisons, and the test
// corpus expects 1 and 0 in exactly these places.
func (truth Truth) Value() Value {
	switch truth {
	case True:
		return Integer(1)
	case False:
		return Integer(0)
	}
	return Null()
}

// Truthy reads a Value as a condition, the way WHERE does.
//
// EG: what counts as true
//
//	Integer(1)    True
//	Integer(0)    False
//	Integer(-1)   True      any non-zero number
//	Real(0.0)     False
//	Text("abc")   False     text that is not a number is not true
//	Text("1")     True      text that looks like a number is converted
//	Null()        Unknown   <- the whole reason this returns Truth
func Truthy(value Value) Truth {
	switch value.Kind {
	case KindNull:
		return Unknown
	case KindInteger:
		return Bool(value.integer != 0)
	case KindReal:
		return Bool(value.real != 0)
	case KindText, KindBlob:
		number, ok := parseNumericPrefix(value.text)
		if !ok {
			return False
		}
		return Bool(number != 0)
	}
	return Unknown
}
