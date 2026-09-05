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

	// Cumulative improvement is the PRODUCT of every kept score, not the
	// latest kept score alone.
	//
	// This depends entirely on the measurement baseline advancing after
	// every KEEP (see internal/pipeline's advanceMeasurementBaseline and
	// state.Baseline.MeasureCommit): each `eval` measures the candidate
	// against the immediately preceding ACCEPTED state, not against the
	// run's original commit. That means every kept row's score already
	// reflects ONLY that experiment's own incremental contribution — it is
	// NOT cumulative on its own — so the run-level speedup has to be
	// composed by multiplying every kept score together, the same way
	// compounding successive percentage changes works.
	//
	// If the measurement baseline ever stops advancing (reverting to a
	// single fixed baseline for the whole run), this reasoning inverts:
	// every kept score would once again already be cumulative on its own,
	// and multiplying them would double-count prior gains. The two must
	// always change together.
	cumulativeScore := 1.0
	haveKept := false
	for _, r := range rows {
		if r.Status == "keep" {
			cumulativeScore *= r.Score
			haveKept = true
		}
	}

	// Largest wins: every kept row, sorted by its own best-benchmark delta.
	// Under the advancing baseline, verdict.Decide only ever returns KEEP
	// for a candidate that is itself a real, significant improvement over
	// the immediately preceding accepted state (see internal/verdict), so
	// unlike under a fixed baseline there is no "re-measurement of an
	// unchanged tree" case to filter out here: every kept row is already a
	// genuine win in its own right.
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
	if haveKept {
		improvement := (1.0 - cumulativeScore) * 100.0
		fmt.Printf("\ncumulative speedup: %.1f%%\n", improvement)
	} else {
		fmt.Printf("\ncumulative speedup: no experiments kept\n")
	}

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
