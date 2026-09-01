package slt

import (
	"fmt"
	"io"
	"slices"
	"strings"
)

// Report prints a per-file breakdown and the two totals.
//
// WHY: both a query-count and a file-count rate, because they tell different
// stories. A few generated corpus files hold over ten thousand records each
// and dominate the query count, so a query rate can move a long way while the
// number of files that fully pass has not moved at all.
func Report(output io.Writer, results []FileResult) {
	var overall Result
	filesPassed := 0

	// Widest path, so the columns line up.
	width := 0
	for _, fileResult := range results {
		if len(fileResult.Path) > width {
			width = len(fileResult.Path)
		}
	}

	sorted := slices.Clone(results)
	slices.SortFunc(sorted, func(left, right FileResult) int {
		return strings.Compare(left.Path, right.Path)
	})

	for _, fileResult := range sorted {
		if fileResult.Err != nil {
			fmt.Fprintf(output, "%-*s  ERROR %v\n", width, fileResult.Path, fileResult.Err)
			continue
		}

		overall.Passed += fileResult.Passed
		overall.Failed += fileResult.Failed
		overall.Skipped += fileResult.Skipped

		if fileResult.Failed == 0 {
			filesPassed++
		}

		fmt.Fprintf(output, "%-*s  %5d/%-5d  %6.1f%%  skipped %d\n",
			width, fileResult.Path,
			fileResult.Passed, fileResult.Passed+fileResult.Failed,
			fileResult.Rate(), fileResult.Skipped)

		for _, failure := range fileResult.Failures {
			fmt.Fprintf(output, "    %s\n", failure)
		}
	}

	fmt.Fprintf(output, "\nqueries  %d/%d (%.1f%%)  skipped %d\n",
		overall.Passed, overall.Passed+overall.Failed, overall.Rate(), overall.Skipped)
	fmt.Fprintf(output, "files    %d/%d\n", filesPassed, len(results))
}
