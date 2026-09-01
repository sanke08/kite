package slt

import (
	"fmt"
	"strconv"
	"strings"
)

// RenderValue turns one value into the exact text the corpus compares against.
//
// The letter comes from the query's type string — I integer, R real, T text —
// and decides the formatting, not the value's own type.
//
// EG: the same value 3 renders differently per column letter
//
//	I  ->  "3"
//	R  ->  "3.000"
//	T  ->  "3"
//
// and the two special cases
//
//	NULL         ->  "NULL"
//	empty string ->  "(empty)"
//
// WHY: these are not choices. They are what the corpus's expected values were
// generated with, so a mismatch here fails records where the engine produced
// exactly the right answer — and the failure looks like a SQL bug.
func RenderValue(value string, letter byte) string {
	if value == "NULL" {
		return "NULL"
	}
	if value == "" {
		return "(empty)"
	}

	switch letter {
	case 'I':
		number, err := strconv.ParseFloat(value, 64)
		if err != nil {
			// Not a number in an integer column. The corpus renders it as 0
			// rather than failing, since the engine under test may have
			// produced something unexpected and the comparison should say so.
			return "0"
		}
		return strconv.FormatInt(int64(number), 10)

	case 'R':
		number, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return "0.000"
		}
		return fmt.Sprintf("%.3f", number)

	default:
		// Text. Control characters are replaced with @ because the file
		// format is line-based and a newline inside a value would be read as
		// the end of the record.
		return replaceControlCharacters(value)
	}
}

func replaceControlCharacters(value string) string {
	var builder strings.Builder
	for i := 0; i < len(value); i++ {
		character := value[i]
		if character < 0x20 || character == 0x7f {
			builder.WriteByte('@')
			continue
		}
		builder.WriteByte(character)
	}
	return builder.String()
}
