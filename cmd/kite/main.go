// Command kite is an interactive shell for a kite database.
package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/sanke08/kite/internal/sql/eval"
	"github.com/sanke08/kite/internal/sql/parser"
	"github.com/sanke08/kite/internal/sql/value"
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

	for {
		fmt.Fprint(output, "kite> ")

		line, err := reader.ReadString('\n')

		// TRAP: on the last line of a file with no trailing newline,
		// ReadString returns the text AND io.EOF together. Checking the
		// error first would silently discard that line.
		line = strings.TrimSpace(line)
		if line != "" && line != ".quit" {
			handle(output, line)
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
//	.tables       a dot-command  -> "not implemented yet"
//	SELECT 1      SQL            -> scanned into tokens
//
// WHY dot-commands are checked first: .tables is not SQL and would be a
// syntax error if it reached the scanner. They are shell commands that
// happen to share the prompt — SQLite does the same, and the test corpus
// assumes it.
func handle(output io.Writer, line string) {
	if strings.HasPrefix(line, ".") {
		fmt.Fprintf(output, "%s: not implemented yet\n", strings.Fields(line)[0])
		return
	}

	// TEMPORARY: only SELECT with no FROM works, so the whole statement is
	// "SELECT" followed by one expression. A real statement parser replaces
	// this once there are tables to select from.
	//
	// EG: SELECT 1+2*3   ->  7
	//	SELECT 1 = NULL ->  NULL
	rest, isSelect := selectExpression(line)
	if !isSelect {
		fmt.Fprintf(output, "error: only SELECT is supported so far\n")
		return
	}

	expression, err := parseAndEvaluate(rest)
	if err != nil {
		fmt.Fprintf(output, "error: %v\n", err)
		return
	}
	fmt.Fprintln(output, expression)
}

// selectExpression strips a leading SELECT and returns what follows.
//
// EG: "SELECT 1+2"  -> "1+2", true
//
//	"select  1"   -> "1",   true    keyword is case-insensitive
//	"INSERT ..."  -> "",    false
func selectExpression(line string) (string, bool) {
	words := strings.Fields(line)
	if len(words) == 0 || !strings.EqualFold(words[0], "SELECT") {
		return "", false
	}
	return strings.TrimSpace(line[len(words[0]):]), true
}

// parseAndEvaluate runs one expression through the parser and the evaluator.
//
// WHY they are separate steps rather than one pass: the parse decides SHAPE
// (what binds to what) and the evaluation decides VALUE. Keeping them apart is
// what lets the same tree later be checked against a catalog, printed by
// EXPLAIN, or evaluated once per row without re-parsing.
func parseAndEvaluate(text string) (value.Value, error) {
	expressionParser, err := parser.New(text)
	if err != nil {
		return value.Null(), err
	}
	tree, err := expressionParser.ParseExpr()
	if err != nil {
		return value.Null(), err
	}
	return eval.Expr(tree)
}
