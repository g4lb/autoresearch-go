package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/g4lb/autoresearch-go/internal/config"
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

func TestEvalReportsAllocsHintInHumanAndJSONOutput(t *testing.T) {
	if testing.Short() {
		t.Skip("runs a real build/vet/test/bench cycle; skipped in -short")
	}
	dir := copyDemoRepo(t)
	mustInit(t, dir)

	// Shrink count/benchtime so measurement finishes quickly. Count must
	// stay >= config's significance floor (4); the pipeline package's own
	// eval tests settled on 5 rounds / 50ms as fast and stable.
	configPath := filepath.Join(dir, config.Path)
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Count = 5
	cfg.Benchtime = "50ms"
	if err := os.WriteFile(configPath, renderConfig(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "shrink count/benchtime for a fast test")

	if code := runBaseline([]string{"-C", dir, "-tag", "sep4"}); code != exitOK {
		t.Fatalf("runBaseline = %d, want %d", code, exitOK)
	}

	// The strings.Builder rewrite from internal/pipeline's eval tests:
	// removes the per-rune string concatenation, which cuts both time and
	// allocations substantially.
	const optimized = `// Package demo is a fixture for autoresearch-go's own integration tests.
package demo

import "strings"

// CountWords returns how many times each lowercase word appears in s.
// Words are separated by whitespace; surrounding punctuation is stripped.
func CountWords(s string) map[string]int {
	counts := make(map[string]int, 64)
	var b strings.Builder
	flush := func() {
		if b.Len() > 0 {
			counts[b.String()]++
			b.Reset()
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z':
			b.WriteByte(c + ('a' - 'A'))
		case (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9'):
			b.WriteByte(c)
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f':
			flush()
		}
	}
	flush()
	return counts
}
`
	if err := os.WriteFile(filepath.Join(dir, "wordcount.go"), []byte(optimized), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "use strings.Builder")

	var humanCode int
	humanOut := captureStdout(t, func() {
		humanCode = runEval([]string{"-C", dir, "-no-log", "-desc", "use strings.Builder"})
	})
	if humanCode != exitOK {
		t.Fatalf("runEval = %d, want %d (KEEP)\nstdout=%s", humanCode, exitOK, humanOut)
	}
	if !strings.Contains(humanOut, "allocs/op") {
		t.Errorf("human stdout = %q, want an allocs/op line under the benchmark delta", humanOut)
	}

	var jsonCode int
	jsonOut := captureStdout(t, func() {
		jsonCode = runEval([]string{"-C", dir, "-no-log", "-json"})
	})
	if jsonCode != exitOK {
		t.Fatalf("runEval -json = %d, want %d (KEEP)\nstdout=%s", jsonCode, exitOK, jsonOut)
	}
	if !strings.Contains(jsonOut, `"allocs_deltas"`) {
		t.Errorf("json stdout = %q, want an allocs_deltas field", jsonOut)
	}

	rows, err := results.Load(filepath.Join(dir, results.Path))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("results.tsv has %d row(s), want 2 (one per runEval call)", len(rows))
	}
	for _, row := range rows {
		if row.AllocsDelta >= 0 {
			t.Errorf("row %+v: AllocsDelta = %v, want negative (the rewrite allocates less)", row, row.AllocsDelta)
		}
		if row.Status != "keep" {
			t.Errorf("row %+v: Status = %q, want keep", row, row.Status)
		}
	}
}
