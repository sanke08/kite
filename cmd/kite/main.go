// Command kite is an interactive shell for a kite database.
package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/sanke08/kite/internal/sql/scanner"
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

	// TEMPORARY: prints the tokens the line breaks into. Becomes a parse and
	// then a real query, so this loop is scaffolding for seeing the scanner
	// work — not something a database does.
	//
	// A token is one indivisible piece of the query: a keyword, a name, a
	// number, a piece of punctuation.
	//
	// EG: typing SELECT a<=5 at the prompt prints
	//
	//	KEYWORD("SELECT") IDENT("a") <=("<=") INTEGER("5")
	//
	// Four tokens. Note <= came back as one, not < followed by =, and that
	// "5" is still text — turning it into a number needs a column type the
	// scanner cannot know about.
	tokens := scanner.New(line)

	// The scanner hands over one token per call rather than a whole list, so
	// this pulls until it reports EOF.
	for {
		token, err := tokens.Next()

		// An error means the text is not scannable at all — an unterminated
		// quote, a stray character. It already names the line and column, so
		// printing it is enough, and there is nothing to recover to.
		if err != nil {
			fmt.Fprintf(output, "error: %v\n", err)
			return
		}

		// EOF means the line is used up. It is a token like any other rather
		// than a separate signal, which is why the check is on Type.
		if token.Type == scanner.EOF {
			break
		}

		// Token has a String method, so %s prints it as KEYWORD("SELECT").
		fmt.Fprintf(output, "%s ", token)
	}

	// One newline after the whole line's tokens, since each was printed with
	// a trailing space and no break of its own.
	fmt.Fprintln(output)
}
