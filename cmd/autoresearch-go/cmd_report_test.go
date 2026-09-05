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
// several kept rows (where the correct answer is the PRODUCT of every kept
// score, not the latest one alone), and a hand-edited score above 1.0.
//
// The product semantics depend on the measurement baseline advancing after
// every KEEP (see internal/pipeline's advanceMeasurementBaseline): each
// `eval` measures a candidate against the immediately preceding accepted
// state, so a kept row's score reflects only ITS OWN incremental
// contribution rather than the whole run's progress from the original
// commit — the run-level figure has to be composed by multiplying them,
// the same way compounding successive percentage changes works. This is the
// reverse of the (now historical) fixed-baseline design, under which every
// kept score was already cumulative on its own and the latest one alone was
// the right answer. If the measurement baseline ever stops advancing, this
// test (and the production code) must revert together.
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
			name: "two kept experiments: the product of both scores, not the latest alone",
			rows: kept(0.9, 0.8),
			// Each score is its own experiment's incremental contribution
			// (the measurement baseline advanced between them), so they
			// compose multiplicatively: 0.9 * 0.8 = 0.72 -> (1-0.72)*100 = 28.0%.
			// Taking only the latest kept score (0.8 -> "20.0%") would
			// under-report the second experiment's real, additional
			// contribution on top of the first.
			want: "28.0%",
		},
		{
			name: "kept score above 1.0 (e.g. a hand-edited log)",
			rows: kept(1.1),
			// A single kept row: product of one term is that term.
			// (1 - 1.1) * 100 = -10.0%: a regression, reported honestly as negative.
			want: "-10.0%",
		},
		{
			name: "three kept experiments, each a genuine incremental improvement",
			rows: kept(0.7936, 0.5917, 0.5981),
			// 0.7936 * 0.5917 * 0.5981 = 0.280852...  (1 - that) * 100 = 71.9%.
			want: "71.9%",
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
