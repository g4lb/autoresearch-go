package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/g4lb/autoresearch-go/internal/results"
)

func TestReportComputesCumulativeSpeedupCorrectly(t *testing.T) {
	// Use copyDemoRepo to create a git repository.
	dir := copyDemoRepo(t)
	resultsPath := filepath.Join(dir, "results.tsv")

	// Write results with scores 0.9 and 0.8, both kept.
	// Expected cumulative speedup: 0.9 * 0.8 = 0.72, reported as -28.0%.
	rows := []results.Row{
		{
			Commit:         "abc123",
			Score:          0.9,
			BestBenchDelta: -5.0,
			AllocsDelta:    -10.0,
			Status:         "keep",
			Description:    "first optimization",
		},
		{
			Commit:         "def456",
			Score:          0.8,
			BestBenchDelta: -10.0,
			AllocsDelta:    -20.0,
			Status:         "keep",
			Description:    "second optimization",
		},
	}

	for _, row := range rows {
		if err := results.Append(resultsPath, row); err != nil {
			t.Fatalf("append result: %v", err)
		}
	}

	// Run report and capture output.
	output := captureStdout(t, func() {
		if code := runReport([]string{"-C", dir}); code != exitOK {
			t.Fatalf("runReport = %d, want %d", code, exitOK)
		}
	})

	// Verify the cumulative speedup is 0.72 (reported as 28.0%).
	if !strings.Contains(output, "28.0%") {
		t.Errorf("output does not contain expected speedup 28.0%%:\n%s", output)
	}

	// Verify that allocs_delta is mentioned.
	if !strings.Contains(output, "allocs") {
		t.Errorf("output does not mention allocs_delta:\n%s", output)
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
