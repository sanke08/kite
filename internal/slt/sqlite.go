package slt

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// SQLite drives the real sqlite3 binary as an Engine.
//
// WHY: it exists to check the runner, not to be part of kite. If this runner
// reports that SQLite fails SQLite's own corpus, the runner is wrong — and a
// wrong runner does not give a low number, it gives an untrustworthy one in
// either direction, for as long as it goes unnoticed.
//
// It shells out rather than linking libsqlite3, which would mean cgo.
type SQLite struct {
	command *exec.Cmd
	stdin   io.WriteCloser
	stdout  *bufio.Reader
}

// NewSQLite starts one sqlite3 process against a scratch database.
//
// WHY: ONE process for the whole file, not one per statement.
//
// TRAP: spawning sqlite3 per statement looks simpler and passes most records.
// It fails only on CREATE TEMP VIEW and TEMP TABLE, which live for the
// lifetime of a CONNECTION — a new process is a new connection, so the temp
// object vanishes and a DROP sixty lines later reports "no such view". The
// failure looks like a corpus problem, not a runner problem.
func NewSQLite(path string) (*SQLite, error) {
	os.Remove(path)

	// -nullvalue matters: by default sqlite3 prints NULL as an empty line,
	// which is indistinguishable from a row that returned an empty string,
	// and from no row at all.
	command := exec.Command("sqlite3", "-batch", "-noheader",
		"-separator", "\x1f", "-nullvalue", "NULL", path)

	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("slt: sqlite3 stdin: %w", err)
	}

	// One pipe carries stdout and stderr together.
	//
	// TRAP: sqlite3 reports errors on stderr. Leaving stderr pointed at the
	// terminal means a "statement error" record sees empty output, reads it
	// as success, and fails a record the engine got exactly right.
	outputReader, outputWriter, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("slt: pipe: %w", err)
	}
	command.Stdout = outputWriter
	command.Stderr = outputWriter

	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("slt: start sqlite3: %w", err)
	}

	// The child holds its own copy of the write end. This one has to go or
	// the reader never sees EOF.
	outputWriter.Close()

	return &SQLite{command: command, stdin: stdin, stdout: bufio.NewReader(outputReader)}, nil
}

// Close ends the sqlite3 process.
func (engine *SQLite) Close() error {
	engine.stdin.Close()
	return engine.command.Wait()
}

func (engine *SQLite) Exec(sql string) error {
	_, err := engine.run(sql)
	return err
}

func (engine *SQLite) Query(sql string, columnTypes string) ([]string, error) {
	output, err := engine.run(sql)
	if err != nil {
		return nil, err
	}
	return renderSQLiteOutput(output, columnTypes), nil
}

// sentinel marks the end of one statement's output.
//
// WHY: the process stays open, so there is no EOF to read until. A SELECT
// printed after each statement gives a line that cannot be confused with
// data, and everything before it is that statement's output.
const sentinel = "--kite-end--"

// run sends one statement and reads back everything it printed.
func (engine *SQLite) run(sql string) (string, error) {
	// .print writes to stdout without going through the query machinery, so
	// it works even when the statement before it failed.
	script := fmt.Sprintf("%s;\n.print %s\n", strings.TrimSuffix(strings.TrimSpace(sql), ";"), sentinel)
	if _, err := io.WriteString(engine.stdin, script); err != nil {
		return "", fmt.Errorf("slt: write to sqlite3: %w", err)
	}

	var output strings.Builder
	for {
		line, err := engine.stdout.ReadString('\n')
		if err != nil {
			return "", fmt.Errorf("slt: read from sqlite3: %w", err)
		}
		if strings.TrimRight(line, "\n") == sentinel {
			break
		}
		output.WriteString(line)
	}

	text := output.String()
	if strings.HasPrefix(text, "Error:") || strings.Contains(text, "\nError:") ||
		strings.Contains(text, "Parse error") || strings.Contains(text, "Runtime error") {
		return "", fmt.Errorf("sqlite3: %s", strings.TrimSpace(text))
	}
	return text, nil
}

// renderSQLiteOutput turns sqlite3's separated output into the flat list of
// values the corpus compares against.
func renderSQLiteOutput(output string, columnTypes string) []string {
	var values []string
	for _, line := range strings.Split(strings.TrimRight(output, "\n"), "\n") {
		if line == "" {
			continue
		}
		for column, value := range strings.Split(line, "\x1f") {
			letter := byte('T')
			if column < len(columnTypes) {
				letter = columnTypes[column]
			}
			values = append(values, RenderValue(value, letter))
		}
	}
	return values
}
