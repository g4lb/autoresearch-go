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

	// Cumulative improvement is the score of the MOST RECENT kept experiment —
	// not a product of every kept score. Every `eval` measures the candidate
	// against the same fixed baseline (the pinned worktree at the baseline
	// commit, which never changes during a run), so each kept score is
	// already cumulative: it already reflects every improvement kept before
	// it. Multiplying successive kept scores therefore double-counts prior
	// gains and compounds re-measurements of an unchanged tree as if they
	// were further improvements. The latest kept score alone is the true
	// state of the tree relative to baseline.
	var lastKeptScore float64
	haveKept := false
	for _, r := range rows {
		if r.Status == "keep" {
			lastKeptScore = r.Score
			haveKept = true
		}
	}

	// Find top wins: rows whose score actually improved on the previous kept
	// score. Because every eval compares against the same fixed baseline (see
	// above), a kept row that merely re-measures an unchanged tree reports a
	// score close to the previous kept row's — not an improvement — yet can
	// still show a large best_bench_delta for a single noisy benchmark, which
	// would otherwise let that re-measurement masquerade as a new win.
	// prevScore starts at 1.0 (the baseline itself, i.e. no change) so the
	// first kept row is judged against "no improvement yet".
	type win struct {
		row results.Row
		idx int
	}
	var keptRows []win
	prevScore := 1.0
	for i, r := range rows {
		if r.Status != "keep" {
			continue
		}
		if r.Score < prevScore {
			keptRows = append(keptRows, win{r, i})
		}
		prevScore = r.Score
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
		improvement := (1.0 - lastKeptScore) * 100.0
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
