package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/g4lb/autoresearch-go/internal/freeze"
	"github.com/g4lb/autoresearch-go/internal/gitx"
	"github.com/g4lb/autoresearch-go/internal/results"
	"github.com/g4lb/autoresearch-go/internal/state"
)

func TestBaselineCreatesBranchAndFreezesTests(t *testing.T) {
	dir := copyDemoRepo(t)
	mustInit(t, dir)

	if code := runBaseline([]string{"-C", dir, "-tag", "sep4"}); code != exitOK {
		t.Fatalf("runBaseline = %d, want %d", code, exitOK)
	}

	br, _ := gitx.CurrentBranch(dir)
	if br != "autoresearch-go/sep4" {
		t.Errorf("branch = %q, want autoresearch-go/sep4", br)
	}
	stateDir, err := state.StateDir(dir, "sep4")
	if err != nil {
		t.Fatal(err)
	}
	b, err := state.LoadBaseline(filepath.Join(stateDir, state.BaselineFile))
	if err != nil {
		t.Fatalf("baseline.json: %v", err)
	}
	if b.Pattern != "^(BenchmarkCountWords)$" {
		t.Errorf("Pattern = %q", b.Pattern)
	}
	m, err := freeze.LoadManifest(filepath.Join(stateDir, freeze.ManifestPath))
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	if _, ok := m.Files["wordcount_test.go"]; !ok {
		t.Errorf("manifest = %v, want wordcount_test.go frozen", m.Files)
	}
}

func TestBaselineWritesNothingIntoTheRepo(t *testing.T) {
	// Every artifact the metric depends on must live outside the repository.
	// An in-repo store, manifest, baseline record or worktree would be
	// silently writable by the very agent they constrain.
	dir := copyDemoRepo(t)
	mustInit(t, dir)
	if code := runBaseline([]string{"-C", dir, "-tag", "sep4"}); code != exitOK {
		t.Fatalf("runBaseline = %d", code)
	}
	for _, rel := range []string{
		".autoresearch/frozen",
		".autoresearch/baseline.json",
		".autoresearch/worktrees",
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); !os.IsNotExist(err) {
			t.Errorf("%s exists inside the repository; harness state must be out of tree", rel)
		}
	}
}

func TestBaselineRefusesExistingBranch(t *testing.T) {
	dir := copyDemoRepo(t)
	mustInit(t, dir)
	runBaseline([]string{"-C", dir, "-tag", "sep4"})
	if code := runBaseline([]string{"-C", dir, "-tag", "sep4"}); code == exitOK {
		t.Fatal("reused an existing run tag, want refusal")
	}
}

func TestBaselineRefusesDirtyTree(t *testing.T) {
	dir := copyDemoRepo(t)
	mustInit(t, dir)
	os.WriteFile(filepath.Join(dir, "scratch.go"), []byte("package demo\n"), 0o644)
	if code := runBaseline([]string{"-C", dir, "-tag", "sep5"}); code == exitOK {
		t.Fatal("accepted a dirty tree, want refusal")
	}
}

func TestBaselineRefusesToOverwriteResultsLog(t *testing.T) {
	// results.tsv is the sole, durable record of a previous unattended run.
	// A second baseline must not silently truncate it.
	dir := copyDemoRepo(t)
	mustInit(t, dir)
	if code := runBaseline([]string{"-C", dir, "-tag", "sep4"}); code != exitOK {
		t.Fatalf("runBaseline = %d, want %d", code, exitOK)
	}

	resultsPath := filepath.Join(dir, "results.tsv")
	if err := results.Append(resultsPath, results.Row{
		Commit: "abc1234", Score: 1.1, BestBenchDelta: 5, AllocsDelta: -2, Status: "keep", Description: "prior experiment",
	}); err != nil {
		t.Fatalf("seed results.tsv: %v", err)
	}
	before, err := os.ReadFile(resultsPath)
	if err != nil {
		t.Fatal(err)
	}

	if code := runBaseline([]string{"-C", dir, "-tag", "sep5"}); code == exitOK {
		t.Fatal("baseline overwrote a results.tsv holding a previous run's rows, want refusal")
	}

	after, err := os.ReadFile(resultsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Errorf("results.tsv changed despite refusal:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestBaselineForceOverwritesResultsLog(t *testing.T) {
	dir := copyDemoRepo(t)
	mustInit(t, dir)
	if code := runBaseline([]string{"-C", dir, "-tag", "sep4"}); code != exitOK {
		t.Fatalf("runBaseline = %d, want %d", code, exitOK)
	}

	resultsPath := filepath.Join(dir, "results.tsv")
	if err := results.Append(resultsPath, results.Row{
		Commit: "abc1234", Score: 1.1, BestBenchDelta: 5, AllocsDelta: -2, Status: "keep", Description: "prior experiment",
	}); err != nil {
		t.Fatalf("seed results.tsv: %v", err)
	}

	if code := runBaseline([]string{"-C", dir, "-tag", "sep5", "-force"}); code != exitOK {
		t.Fatalf("runBaseline -force = %d, want %d", code, exitOK)
	}

	got, err := os.ReadFile(resultsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != results.Header+"\n" {
		t.Errorf("results.tsv = %q, want just the header after -force", got)
	}
}
