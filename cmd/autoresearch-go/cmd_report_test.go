package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/g4lb/autoresearch-go/internal/results"
)

// TestReportComputesCumulativeSpeedupCorrectly covers the cumulative-speedup
// arithmetic across the shapes that matter: no kept rows, one kept row,
// several kept rows (where the correct answer is the LATEST kept score, not
// the product of all of them), a hand-edited score above 1.0, and the
// real-world run against github.com/dustin/go-humanize that exposed the bug
// — kept scores 0.7936, 0.5917, 0.5981 in that order previously produced a
// wildly wrong "71.9%" (0.9 * ... as a product-of-all-kept miscalculation)
// instead of the correct ~40.2% (the last kept score, 0.5981).
func TestReportComputesCumulativeSpeedupCorrectly(t *testing.T) {
	// scoredRow is a shorthand for one logged row; status defaults to "keep"
	// when empty, since most cases below only care about kept rows.
	type scoredRow struct {
		score  float64
		status string
	}
	kept := func(scores ...float64) []scoredRow {
		rows := make([]scoredRow, len(scores))
		for i, s := range scores {
			rows[i] = scoredRow{score: s, status: "keep"}
		}
		return rows
	}

	cases := []struct {
		name string
		rows []scoredRow
		want string // substring expected in output
	}{
		{
			name: "no kept experiments (only a discarded row)",
			rows: []scoredRow{{score: 1.02, status: "discard"}},
			want: "no experiments kept",
		},
		{
			name: "single kept experiment",
			rows: kept(0.8),
			// Latest (only) kept score is 0.8: (1 - 0.8) * 100 = 20.0%.
			want: "20.0%",
		},
		{
			name: "two kept experiments: latest score wins, not the product",
			rows: kept(0.9, 0.8),
			// Old (wrong) product semantics: 0.9 * 0.8 = 0.72 -> "28.0%".
			// Correct semantics: latest kept score is 0.8 -> "20.0%".
			want: "20.0%",
		},
		{
			name: "kept score above 1.0 (e.g. a hand-edited log)",
			rows: kept(1.1),
			// (1 - 1.1) * 100 = -10.0%: a regression, reported honestly as negative.
			want: "-10.0%",
		},
		{
			name: "real run against dustin/go-humanize",
			// Third row re-measures the same (unchanged) tree as the second;
			// under the old product semantics this compounded into a further
			// "improvement" on top of itself.
			rows: kept(0.7936, 0.5917, 0.5981),
			// Latest kept score is 0.5981: (1 - 0.5981) * 100 = 40.19%.
			want: "40.2%",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := copyDemoRepo(t)
			resultsPath := filepath.Join(dir, "results.tsv")

			for i, sr := range tc.rows {
				row := results.Row{
					Commit:         fmt.Sprintf("commit%d", i),
					Score:          sr.score,
					BestBenchDelta: -5.0,
					AllocsDelta:    -10.0,
					Status:         sr.status,
					Description:    fmt.Sprintf("optimization %d", i),
				}
				if err := results.Append(resultsPath, row); err != nil {
					t.Fatalf("append result: %v", err)
				}
			}

			output := captureStdout(t, func() {
				if code := runReport([]string{"-C", dir}); code != exitOK {
					t.Fatalf("runReport = %d, want %d", code, exitOK)
				}
			})

			if !strings.Contains(output, tc.want) {
				t.Errorf("output does not contain expected %q:\n%s", tc.want, output)
			}
		})
	}
}

func TestReportHandlesMissingResultsFile(t *testing.T) {
	dir := copyDemoRepo(t)

	// Run report on a directory without results.tsv.
	output := captureStdout(t, func() {
		if code := runReport([]string{"-C", dir}); code != exitOK {
			t.Fatalf("runReport = %d, want %d", code, exitOK)
		}
	})

	// Should indicate no experiments recorded.
	if !strings.Contains(output, "no experiments") {
		t.Errorf("output does not indicate no experiments:\n%s", output)
	}
}

func TestReportIncludesBiggestWins(t *testing.T) {
	dir := copyDemoRepo(t)
	resultsPath := filepath.Join(dir, "results.tsv")

	// Write results with various scores and deltas.
	rows := []results.Row{
		{
			Commit:         "abc123",
			Score:          0.7,
			BestBenchDelta: -20.0,
			AllocsDelta:    -30.0,
			Status:         "keep",
			Description:    "big win",
		},
		{
			Commit:         "def456",
			Score:          0.95,
			BestBenchDelta: -2.0,
			AllocsDelta:    -5.0,
			Status:         "keep",
			Description:    "small win",
		},
		{
			Commit:         "ghi789",
			Score:          1.05,
			BestBenchDelta: 5.0,
			AllocsDelta:    10.0,
			Status:         "discard",
			Description:    "regressed",
		},
	}

	for _, row := range rows {
		if err := results.Append(resultsPath, row); err != nil {
			t.Fatalf("append result: %v", err)
		}
	}

	output := captureStdout(t, func() {
		if code := runReport([]string{"-C", dir}); code != exitOK {
			t.Fatalf("runReport = %d, want %d", code, exitOK)
		}
	})

	// Should mention the big win.
	if !strings.Contains(output, "big win") {
		t.Errorf("output does not mention biggest win:\n%s", output)
	}

	// Should include allocs_delta in the wins.
	if !strings.Contains(output, "allocs") {
		t.Errorf("output does not include allocs information:\n%s", output)
	}
}
