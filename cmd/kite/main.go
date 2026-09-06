// Command kite is an interactive shell for a kite database.
package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/sanke08/kite"
)

func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "kite:", err)
		os.Exit(1)
	}
}

// run reads a line at a time and handles it until input ends.
//
// WHY: it takes a reader and a writer rather than using os.Stdin directly, so
// a test can drive the whole loop with a string and read back what it printed.
func run(input io.Reader, output io.Writer) error {
	reader := bufio.NewReader(input)
	database := kite.Open()

	for {
		fmt.Fprint(output, "kite> ")

		line, err := reader.ReadString('\n')

		// TRAP: on the last line of a file with no trailing newline,
		// ReadString returns the text AND io.EOF together. Checking the
		// error first would silently discard that line.
		line = strings.TrimSpace(line)
		if line != "" && line != ".quit" {
			handle(output, database, line)
		}

		if errors.Is(err, io.EOF) {
			// Ctrl-D. Not a failure — print a newline so the shell prompt
			// does not land on the same line as ours.
			fmt.Fprintln(output)
			return nil
		}
		if err != nil {
			return err
		}
		if line == ".quit" {
			return nil
		}
	}
}

// handle acts on one line of input: either a dot-command or SQL.
//
// EG: the two paths
//
//	.tables       a dot-command  -> lists table names
//	SELECT 1      SQL            -> parsed, executed, printed
//
// WHY dot-commands are checked first: .tables is not SQL and would be a
// syntax error if it reached the parser. They are shell commands that happen
// to share the prompt — SQLite does the same, and the test corpus assumes it.
func handle(output io.Writer, database *kite.DB, line string) {
	if strings.HasPrefix(line, ".") {
		dotCommand(output, database, line)
		return
	}

	result, err := database.Exec(line)
	if err != nil {
		fmt.Fprintf(output, "error: %v\n", err)
		return
	}
	printResult(output, result)
}

// printResult writes a result set, one row per line, values separated by |.
//
// WHY the pipe separator: it is what sqlite3 uses in its default mode, so
// output from the two can be compared by eye without reformatting either.
// A statement with no result set — CREATE TABLE — prints nothing at all.
func printResult(output io.Writer, result *kite.Result) {
	for _, row := range result.Rows {
		rendered := make([]string, len(row))
		for index, cell := range row {
			rendered[index] = kite.Render(cell)
		}
		fmt.Fprintln(output, strings.Join(rendered, "|"))
	}
}

// dotCommand handles the non-SQL commands that share the prompt.
func dotCommand(output io.Writer, database *kite.DB, line string) {
	words := strings.Fields(line)

	switch words[0] {
	case ".tables":
		names := database.Tables()
		if len(names) == 0 {
			return
		}
		fmt.Fprintln(output, strings.Join(names, " "))

	case ".schema":
		// With no argument, print every table. With one, print that table.
		names := database.Tables()
		if len(words) > 1 {
			names = words[1:]
		}
		for _, name := range names {
			schema, ok := database.Schema(name)
			if !ok {
				fmt.Fprintf(output, "error: no such table: %s\n", name)
				continue
			}
			fmt.Fprintln(output, schema)
		}

	default:
		fmt.Fprintf(output, "%s: not implemented yet\n", words[0])
	}
}
