package slt

import (
	"strings"
	"testing"
)

func TestParseEmpty(t *testing.T) {
	records, err := Parse(strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Fatalf("got %d records, want 0", len(records))
	}
}

func TestParseStatements(t *testing.T) {
	input := `statement ok
CREATE TABLE users(id INTEGER)

statement error
DROP TABLE missing
`

	records, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("got %d records, want 2", len(records))
	}

	if records[0].Kind != KindStatement {
		t.Errorf("first record kind = %v, want KindStatement", records[0].Kind)
	}
	if records[0].ExpectError {
		t.Error("first record expects an error, want success")
	}
	if records[0].SQL != "CREATE TABLE users(id INTEGER)" {
		t.Errorf("first record SQL = %q", records[0].SQL)
	}
	if records[0].Line != 1 {
		t.Errorf("first record line = %d, want 1", records[0].Line)
	}

	if !records[1].ExpectError {
		t.Error("second record expects success, want an error")
	}
	if records[1].SQL != "DROP TABLE missing" {
		t.Errorf("second record SQL = %q", records[1].SQL)
	}
	if records[1].Line != 4 {
		t.Errorf("second record line = %d, want 4", records[1].Line)
	}
}

func TestParseQueries(t *testing.T) {
	input := `query IT nosort
SELECT id,
       name
FROM users
----
1
Alice

query I rowsort
SELECT id FROM users
----
2
1

query T valuesort
SELECT name FROM users
----
Bob
Alice
`

	records, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 3 {
		t.Fatalf("got %d records, want 3", len(records))
	}

	first := records[0]
	if first.Kind != KindQuery {
		t.Errorf("first record kind = %v, want KindQuery", first.Kind)
	}
	if first.ColumnTypes != "IT" {
		t.Errorf("first record column types = %q, want %q", first.ColumnTypes, "IT")
	}
	if first.SortMode != NoSort {
		t.Errorf("first record sort mode = %v, want NoSort", first.SortMode)
	}
	wantSQL := "SELECT id,\n       name\nFROM users"
	if first.SQL != wantSQL {
		t.Errorf("first record SQL = %q, want %q", first.SQL, wantSQL)
	}
	wantExpected := []string{"1", "Alice"}
	if !slicesEqual(first.Expected, wantExpected) {
		t.Errorf("first record expected values = %q, want %q", first.Expected, wantExpected)
	}

	if records[1].SortMode != RowSort {
		t.Errorf("second record sort mode = %v, want RowSort", records[1].SortMode)
	}
	if records[2].SortMode != ValueSort {
		t.Errorf("third record sort mode = %v, want ValueSort", records[2].SortMode)
	}
}

func TestParseConditionals(t *testing.T) {
	input := `skipif mysql # MySQL differs here
onlyif sqlite # SQLite-compatible result
query I
SELECT 1
----
1
`

	records, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("got %d records, want 1", len(records))
	}
	if !slicesEqual(records[0].SkipIf, []string{"mysql"}) {
		t.Errorf("skipif = %q, want [mysql]", records[0].SkipIf)
	}
	if !slicesEqual(records[0].OnlyIf, []string{"sqlite"}) {
		t.Errorf("onlyif = %q, want [sqlite]", records[0].OnlyIf)
	}
}

func TestParseGuardedHalt(t *testing.T) {
	input := `onlyif mssql
halt

query I
SELECT 1
----
1
`

	records, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("got %d records, want 1", len(records))
	}
	if records[0].SQL != "SELECT 1" {
		t.Errorf("SQL = %q, want %q", records[0].SQL, "SELECT 1")
	}
}

func TestParseMalformedHeaders(t *testing.T) {
	for _, input := range []string{"statement\n", "query\n"} {
		t.Run(strings.TrimSpace(input), func(t *testing.T) {
			_, err := Parse(strings.NewReader(input))
			if err == nil {
				t.Fatal("Parse returned nil error for a header missing its argument")
			}
		})
	}
}

func TestParseLongLine(t *testing.T) {
	longSQL := "SELECT '" + strings.Repeat("a", 70*1024) + "'"
	input := "query T\n" + longSQL + "\n----\nvalue\n"

	records, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("got %d records, want 1", len(records))
	}
	if records[0].SQL != longSQL {
		t.Fatalf("SQL length = %d, want %d", len(records[0].SQL), len(longSQL))
	}
}

func slicesEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
