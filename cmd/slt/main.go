// Command slt runs a sqllogictest manifest against an engine.
package main

import (
	"fmt"
	"os"

	"github.com/sanke08/kite/internal/slt"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: slt <manifest.json>")
		os.Exit(2)
	}

	newEngine := func() (slt.Engine, error) {
		return slt.NewSQLite("/tmp/slt-scratch.db")
	}

	results, err := slt.RunManifest(newEngine, os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "slt:", err)
		os.Exit(1)
	}
	slt.Report(os.Stdout, results)
}
