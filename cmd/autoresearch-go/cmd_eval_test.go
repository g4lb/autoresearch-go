package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/g4lb/autoresearch-go/internal/gitx"
	"github.com/g4lb/autoresearch-go/internal/results"
	"github.com/g4lb/autoresearch-go/internal/state"
)

// These cover the command layer's own responsibilities — deriving the run
// tag from the branch, verifying the pinned worktree, JSON formatting and
// the results.tsv row — using fast-failing gates (a go.mod edit) so they
// don't pay for a real build/vet/test/bench cycle. The gate-ordering and
// scoring logic itself is exercised exhaustively in internal/pipeline.

func TestEvalRequiresRunBranch(t *testing.T) {
	dir := copyDemoRepo(t)
	original, err := gitx.CurrentBranch(dir)
	if err != nil {
		t.Fatal(err)
	}
	mustInit(t, dir)
	if code := runBaseline([]string{"-C", dir, "-tag", "sep4"}); code != exitOK {
		t.Fatalf("runBaseline = %d, want %d", code, exitOK)
	}
	if err := gitx.Checkout(dir, original); err != nil {
		t.Fatal(err)
	}

	var code int
	stderr := captureStderr(t, func() {
		code = runEval([]string{"-C", dir, "-no-log"})
	})
	if code != exitUsage {
		t.Fatalf("runEval on %q = %d, want %d", original, code, exitUsage)
	}
	if !strings.Contains(stderr, "run branch") {
		t.Errorf("stderr = %q, want it to mention a run branch", stderr)
	}
}

func TestEvalRequiresPinnedWorktree(t *testing.T) {
	dir := copyDemoRepo(t)
	mustInit(t, dir)
	if code := runBaseline([]string{"-C", dir, "-tag", "sep4"}); code != exitOK {
		t.Fatalf("runBaseline = %d, want %d", code, exitOK)
	}

	stateDir, err := state.StateDir(dir, "sep4")
	if err != nil {
		t.Fatal(err)
	}
	worktree := filepath.Join(stateDir, state.WorktreeName)
	// baseline.json is written before the worktree pin completes, so its
	// mere existence must not be enough: simulate an interrupted or later
	// tampered-with baseline by removing the pinned worktree.
	if err := gitx.RemoveWorktree(dir, worktree); err != nil {
		t.Fatal(err)
	}

	var code int
	stderr := captureStderr(t, func() {
		code = runEval([]string{"-C", dir, "-no-log"})
	})
	if code != exitUsage {
		t.Fatalf("runEval with no pinned worktree = %d, want %d", code, exitUsage)
	}
	if !strings.Contains(stderr, "worktree") {
		t.Errorf("stderr = %q, want it to mention the missing worktree", stderr)
	}
}

func TestEvalJSONReportsScopeViolation(t *testing.T) {
	dir := copyDemoRepo(t)
	mustInit(t, dir)
	if code := runBaseline([]string{"-C", dir, "-tag", "sep4"}); code != exitOK {
		t.Fatalf("runBaseline = %d, want %d", code, exitOK)
	}

	goMod := filepath.Join(dir, "go.mod")
	b, err := os.ReadFile(goMod)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(goMod, append(b, []byte("\n// touched\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "touch go.mod")

	var code int
	stdout := captureStdout(t, func() {
		code = runEval([]string{"-C", dir, "-json", "-desc", "touch go.mod"})
	})
	if code != exitFail {
		t.Fatalf("runEval -json on a go.mod edit = %d, want %d\nstdout=%s", code, exitFail, stdout)
	}
	if !strings.Contains(stdout, `"status"`) || !strings.Contains(stdout, "FAIL") {
		t.Errorf("stdout = %q, want a JSON object naming FAIL status", stdout)
	}
	if !strings.Contains(stdout, "scope_violation") {
		t.Errorf("stdout = %q, want reason scope_violation", stdout)
	}

	rows, err := results.Load(filepath.Join(dir, results.Path))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("results.tsv has %d row(s), want 1", len(rows))
	}
	if rows[0].Status != "fail" {
		t.Errorf("row status = %q, want %q", rows[0].Status, "fail")
	}
	if rows[0].Description != "touch go.mod" {
		t.Errorf("row description = %q, want %q", rows[0].Description, "touch go.mod")
	}
}

func TestEvalHumanOutputReportsFailedGate(t *testing.T) {
	dir := copyDemoRepo(t)
	mustInit(t, dir)
	if code := runBaseline([]string{"-C", dir, "-tag", "sep4"}); code != exitOK {
		t.Fatalf("runBaseline = %d, want %d", code, exitOK)
	}

	goMod := filepath.Join(dir, "go.mod")
	b, err := os.ReadFile(goMod)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(goMod, append(b, []byte("\n// touched\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "touch go.mod")

	var code int
	stdout := captureStdout(t, func() {
		code = runEval([]string{"-C", dir, "-no-log"})
	})
	if code != exitFail {
		t.Fatalf("runEval on a go.mod edit = %d, want %d\nstdout=%s", code, exitFail, stdout)
	}
	if !strings.Contains(stdout, "checking scope") || !strings.Contains(stdout, "FAILED") {
		t.Errorf("stdout = %q, want the scope stage reported as FAILED", stdout)
	}
	if !strings.Contains(stdout, "VERDICT: FAIL") {
		t.Errorf("stdout = %q, want a VERDICT: FAIL line", stdout)
	}
}
