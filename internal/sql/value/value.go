// Package value holds SQL's runtime values and the rules for comparing them.
package value

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Kind is which of SQL's five storage classes a Value holds.
//
// WHY five, and why the type lives on the value rather than the column: SQL as
// implemented here is dynamically typed. A column declared INTEGER can hold
// text, exactly as SQLite allows, so the value has to carry its own type. A
// column type is an affinity — a preference applied on the way in — not a
// guarantee.
//
// EG: one column, three kinds stored in it
//
//	CREATE TABLE t(a INTEGER)
//	INSERT INTO t VALUES (1)        KindInteger
//	INSERT INTO t VALUES ('x')      KindText     — allowed
//	INSERT INTO t VALUES (NULL)     KindNull
type Kind int

const (
	// KindNull is SQL's NULL: not zero, not empty, but UNKNOWN. It is the
	// zero value of Kind so that an unset Value reads as NULL, which is the
	// safest default — a missing value is genuinely unknown.
	KindNull Kind = iota

	// KindInteger is a 64-bit signed integer.
	KindInteger

	// KindReal is a 64-bit float. Separate from KindInteger because SQL
	// keeps 1 and 1.0 distinct, and because integer division and real
	// division give different answers.
	KindReal

	// KindText is a string.
	KindText

	// KindBlob is raw bytes, compared byte by byte and never coerced to a
	// number. Nothing produces one yet; it exists because the storage class
	// list is fixed by SQL and leaving a hole would mean renumbering later.
	KindBlob
)

// Value is one SQL value: a kind plus exactly one populated field.
//
// EG: the five kinds as literals
//
//	Null()            Kind KindNull
//	Integer(42)       Kind KindInteger, integer 42
//	Real(2.5)         Kind KindReal,    real 2.5
//	Text("hi")        Kind KindText,    text "hi"
//
// WHY separate typed fields rather than one `any`: an `any` would mean a type
// assertion at every arithmetic and comparison site, each one a place to get
// the assertion wrong at runtime. Named fields make the compiler check it, and
// the struct is small enough that the unused fields cost nothing worth
// measuring.
type Value struct {
	Kind Kind

	// integer holds the value when Kind is KindInteger. Reading it for any
	// other kind gives zero, which is why every operation switches on Kind
	// first rather than checking a field for a non-zero value.
	integer int64

	// real holds the value when Kind is KindReal.
	real float64

	// text holds the value when Kind is KindText or KindBlob.
	text string
}

// Null returns SQL NULL.
func Null() Value {
	return Value{Kind: KindNull}
}

// Integer returns an integer value.
func Integer(number int64) Value {
	return Value{Kind: KindInteger, integer: number}
}

// Real returns a floating-point value.
func Real(number float64) Value {
	return Value{Kind: KindReal, real: number}
}

// Text returns a text value.
func Text(characters string) Value {
	return Value{Kind: KindText, text: characters}
}

// Blob returns a raw-bytes value.
func Blob(bytes string) Value {
	return Value{Kind: KindBlob, text: bytes}
}

// IsNull reports whether this is SQL NULL.
//
// WHY a method rather than comparing Kind at each call site: it is checked
// before nearly every operation, since almost everything in SQL returns NULL
// when given NULL.
func (value Value) IsNull() bool {
	return value.Kind == KindNull
}

// Integer returns the integer, or 0 if this is not an integer.
func (value Value) Integer() int64 {
	return value.integer
}

// Real returns this value as a float, converting an integer if needed.
//
// WHY the conversion is here: arithmetic between an integer and a real
// produces a real, so the integer side has to be widened. Doing it in one
// place keeps every arithmetic operator from repeating the switch.
//
// EG: Integer(3).Real() is 3.0,  Real(2.5).Real() is 2.5
func (value Value) Real() float64 {
	if value.Kind == KindInteger {
		return float64(value.integer)
	}
	return value.real
}

// Text returns the text or blob bytes, or "" for any other kind.
func (value Value) Text() string {
	return value.text
}

// String renders a value the way the REPL prints it.
//
// EG: Integer(42) -> "42",  Real(2.5) -> "2.5",  Null() -> "NULL"
//
// This is display only. The sqllogictest comparison rules render differently —
// a real as %.3f, an empty string as "(empty)" — because they compare against
// text the corpus generated, and mixing the two would make one of them wrong.
func (value Value) String() string {
	switch value.Kind {
	case KindNull:
		return "NULL"
	case KindInteger:
		return strconv.FormatInt(value.integer, 10)
	case KindReal:
		return formatReal(value.real)
	case KindText, KindBlob:
		return value.text
	}
	return fmt.Sprintf("Value(kind %d)", int(value.Kind))
}

// formatReal prints a float the way SQLite does.
//
// WHY not %v: two separate problems with it.
//
// Go prints 3.0 as "3", losing the fact that it is a real rather than an
// integer, where SQLite prints "3.0".
//
// And Go prints every digit needed to round-trip the float exactly, so
// 0.1+0.2 comes out as 0.30000000000000004 — which is the true value of the
// sum, but not what anyone wants to read. SQLite shows 15 significant digits,
// which hides the representation error without hiding real precision.
//
// EG: 2.5 -> "2.5",  3.0 -> "3.0",  0.1+0.2 -> "0.3"
func formatReal(number float64) string {
	if number == math.Trunc(number) && !math.IsInf(number, 0) &&
		math.Abs(number) < 1e15 {
		return strconv.FormatFloat(number, 'f', 1, 64)
	}
	// 15 significant digits, matching SQLite. -1 would give the shortest
	// exact round-trip, which exposes representation error.
	text := strconv.FormatFloat(number, 'g', 15, 64)
	// 'g' switches to exponent form for large and small magnitudes, and
	// writes it as 1e+10 where SQL writes 1.0e+10.
	if exponent := strings.IndexAny(text, "eE"); exponent >= 0 && !strings.Contains(text[:exponent], ".") {
		text = text[:exponent] + ".0" + text[exponent:]
	}
	return text
}
