package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/g4lb/autoresearch-go/internal/gitx"
	"github.com/g4lb/autoresearch-go/internal/results"
)

// runReport summarizes results.tsv.
func runReport(args []string) int {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dir := fs.String("C", ".", "repository root (or a directory inside it)")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}

	root, err := gitx.Root(*dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "autoresearch-go report: %s is not inside a git repository: %v\n", *dir, err)
		return exitUsage
	}

	resultsPath := filepath.Join(root, results.Path)
	rows, err := results.Load(resultsPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "autoresearch-go report: %v\n", err)
		return exitUsage
	}

	printReportSummary(rows)
	return exitOK
}

// printReportSummary prints a summary of the results.
func printReportSummary(rows []results.Row) {
	if len(rows) == 0 {
		fmt.Println("autoresearch-go report: no experiments recorded")
		return
	}

	// Count by status.
	var kept, discarded, failed, crashed int
	for _, r := range rows {
		switch r.Status {
		case "keep":
			kept++
		case "discard":
			discarded++
		case "fail":
			failed++
		case "crash":
			crashed++
		}
	}

	// Calculate cumulative speedup: product of kept scores.
	cumulativeSpeedup := 1.0
	for _, r := range rows {
		if r.Status == "keep" {
			cumulativeSpeedup *= r.Score
		}
	}

	// Convert to percentage improvement.
	improvement := (1.0 - cumulativeSpeedup) * 100.0

	// Find top 5 wins (by best_bench_delta, descending).
	type win struct {
		row results.Row
		idx int
	}
	var keptRows []win
	for i, r := range rows {
		if r.Status == "keep" {
			keptRows = append(keptRows, win{r, i})
		}
	}
	sort.Slice(keptRows, func(i, j int) bool {
		return keptRows[i].row.BestBenchDelta < keptRows[j].row.BestBenchDelta
	})

	// Print summary.
	fmt.Printf("autoresearch-go report: %d total experiments\n", len(rows))
	fmt.Printf("  kept: %d\n", kept)
	fmt.Printf("  discarded: %d\n", discarded)
	fmt.Printf("  failed: %d\n", failed)
	fmt.Printf("  crashed: %d\n", crashed)
	fmt.Printf("\ncumulative speedup: %.1f%%\n", improvement)

	if len(keptRows) > 0 {
		fmt.Println("\nlargest wins:")
		maxWins := 5
		if len(keptRows) < maxWins {
			maxWins = len(keptRows)
		}
		for i := 0; i < maxWins; i++ {
			w := keptRows[i]
			fmt.Printf("  %d. %s (time: %.1f%%, allocs: %.1f%%)\n",
				i+1, w.row.Description, w.row.BestBenchDelta, w.row.AllocsDelta)
		}
	}
}
