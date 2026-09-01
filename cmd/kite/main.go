// Command kite is an interactive shell for a kite database.
package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
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

// handle acts on one line of input.
//
// WHY: dot-commands are checked before anything else because they are not SQL
// and never reach the parser. SQLite works the same way, and the corpus files
// assume it.
func handle(output io.Writer, line string) {
	if strings.HasPrefix(line, ".") {
		fmt.Fprintf(output, "%s: not implemented yet\n", strings.Fields(line)[0])
		return
	}

	// TEMPORARY: echoes the input. Becomes the tokenizer, then the parser,
	// then a real query.
	fmt.Fprintf(output, "%s\n", line)
}
