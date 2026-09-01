package slt

import (
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Engine runs SQL. The runner drives kite through this, and also the real
// sqlite3 binary — pointing the runner at SQLite is how the runner itself gets
// validated, since SQLite must score ~100% on its own corpus.
type Engine interface {
	// Exec runs SQL that returns no rows.
	Exec(sql string) error

	// Query runs SQL and returns the rows it produced, already rendered as
	// the strings the file compares against.
	//
	// WHY: rendered strings, not typed values. sqllogictest compares text —
	// an integer 3 is "3" but a real 3 is "3.000" — so rendering has to
	// happen where the column types are known, and columnTypes is what says
	// which is which.
	Query(sql string, columnTypes string) ([]string, error)
}

// Result counts how one file, or a whole run, came out.
type Result struct {
	Passed  int
	Failed  int
	Skipped int

	// Failures holds a line per failed record, for printing.
	Failures []string
}

// Rate is the share of ATTEMPTED records that passed — skipped records are
// left out of both halves.
//
// WHY: counting skips as passes lets the number be inflated by skipping
// anything hard. Counting them as failures penalises tests that legitimately
// do not apply. Excluding them is the only honest option, which is why Skipped
// is reported as its own bucket alongside this.
func (result Result) Rate() float64 {
	attempted := result.Passed + result.Failed
	if attempted == 0 {
		return 0
	}
	return float64(result.Passed) / float64(attempted) * 100
}

// Run executes every record against the engine and reports what happened.
func Run(engine Engine, records []Record) Result {
	var result Result

	for _, record := range records {
		if !shouldRun(record.SkipIf, record.OnlyIf) {
			result.Skipped++
			continue
		}

		if err := runRecord(engine, record); err != nil {
			result.Failed++
			result.Failures = append(result.Failures,
				fmt.Sprintf("line %d: %v", record.Line, err))
			continue
		}
		result.Passed++
	}
	return result
}

// runRecord runs one record and reports why it failed, or nil if it passed.
func runRecord(engine Engine, record Record) error {
	if record.Kind == KindStatement {
		err := engine.Exec(record.SQL)

		// A "statement error" record passes when the SQL fails, so the
		// success and failure cases are swapped here.
		if record.ExpectError {
			if err == nil {
				return fmt.Errorf("expected an error, statement succeeded")
			}
			return nil
		}
		if err != nil {
			return fmt.Errorf("statement failed: %w", err)
		}
		return nil
	}

	values, err := engine.Query(record.SQL, record.ColumnTypes)
	if err != nil {
		return fmt.Errorf("query failed: %w", err)
	}
	return compare(record, values)
}

// compare checks the engine's values against the record's expected values,
// after putting both in the order the record's sort mode asks for.
func compare(record Record, values []string) error {
	got := sortValues(values, record.SortMode, len(record.ColumnTypes))
	want := record.Expected

	if len(got) != len(want) {
		return fmt.Errorf("got %d values, want %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			return fmt.Errorf("value %d: got %q, want %q", i, got[i], want[i])
		}
	}
	return nil
}

// sortValues puts a flat list of values into the order the sort mode wants.
//
// TRAP: sorting is TEXTUAL, byte by byte, never numeric — "10" sorts before
// "9" because '1' < '9'. That is what the corpus's expected values were
// generated with, so sorting numerically here produces failures on records
// where the engine is completely correct.
func sortValues(values []string, mode SortMode, columnCount int) []string {
	switch mode {
	case NoSort:
		return values

	case ValueSort:
		sorted := slices.Clone(values)
		slices.Sort(sorted)
		return sorted

	case RowSort:
		// Values arrive flat, so they are grouped into rows, the rows are
		// sorted as units, then flattened again — a row's values must stay
		// together and in column order.
		if columnCount <= 0 {
			return values
		}
		rows := make([][]string, 0, len(values)/columnCount)
		for start := 0; start+columnCount <= len(values); start += columnCount {
			rows = append(rows, values[start:start+columnCount])
		}
		slices.SortFunc(rows, func(left, right []string) int {
			return cmp.Compare(strings.Join(left, " "), strings.Join(right, " "))
		})
		sorted := make([]string, 0, len(values))
		for _, row := range rows {
			sorted = append(sorted, row...)
		}
		return sorted
	}
	return values
}

// Manifest is the pinned list of .slt files a run covers.
//
// WHY: if the set of files changes every week the denominator changes with it,
// and the percentage stops meaning anything — progress becomes indistinguish-
// able from a shifting sample, and dropping a hard file quietly raises the
// score. The list grows one deliberate entry at a time.
type Manifest struct {
	// Files are paths relative to the manifest's own directory.
	Files []string `json:"files"`
}

// LoadManifest reads a manifest from a JSON file.
func LoadManifest(path string) (Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("slt: read manifest: %w", err)
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("slt: parse manifest %s: %w", path, err)
	}
	return manifest, nil
}

// FileResult is one file's outcome.
type FileResult struct {
	Path string
	Result
	Err error // set when the file could not be read or parsed at all
}

// NewEngine makes a fresh engine for one file.
//
// WHY: each file gets its OWN database, because corpus files all start by
// creating the same tables. Sharing one database across a manifest means the
// second file's "CREATE TABLE t1" fails with "already exists" and every
// record after it fails too — which reads as a broken engine rather than a
// broken runner.
type NewEngine func() (Engine, error)

// RunManifest runs every file the manifest names, in order, each against a
// freshly created engine.
func RunManifest(newEngine NewEngine, manifestPath string) ([]FileResult, error) {
	manifest, err := LoadManifest(manifestPath)
	if err != nil {
		return nil, err
	}
	root := filepath.Dir(manifestPath)

	results := make([]FileResult, 0, len(manifest.Files))
	for _, name := range manifest.Files {
		engine, err := newEngine()
		if err != nil {
			results = append(results, FileResult{Path: name, Err: err})
			continue
		}
		results = append(results, runFile(engine, filepath.Join(root, name), name))

		if closer, ok := engine.(interface{ Close() error }); ok {
			closer.Close()
		}
	}
	return results, nil
}

func runFile(engine Engine, path, name string) FileResult {
	file, err := os.Open(path)
	if err != nil {
		return FileResult{Path: name, Err: err}
	}
	defer file.Close()

	records, err := Parse(file)
	if err != nil {
		return FileResult{Path: name, Err: err}
	}
	return FileResult{Path: name, Result: Run(engine, records)}
}
