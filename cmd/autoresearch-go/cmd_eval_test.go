package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/g4lb/autoresearch-go/internal/bench"
	"github.com/g4lb/autoresearch-go/internal/config"
	"github.com/g4lb/autoresearch-go/internal/gitx"
	"github.com/g4lb/autoresearch-go/internal/results"
	"github.com/g4lb/autoresearch-go/internal/state"
	"github.com/g4lb/autoresearch-go/internal/verdict"
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

	// This second runEval call makes no new commit at all — it re-measures
	// the exact same candidate the first call already kept. Since a KEEP
	// advances the measurement baseline (see internal/pipeline's
	// advanceMeasurementBaseline) to that very commit, this candidate is now
	// being measured against ITSELF and must DISCARD, not KEEP: this is the
	// command-layer confirmation of the fix for a no-op experiment coasting
	// on an earlier improvement's already-banked score. It still goes
	// through full measurement (only a gate failure returns no
	// Measurements), so the JSON output still carries an allocs_deltas
	// field either way.
	var jsonCode int
	jsonOut := captureStdout(t, func() {
		jsonCode = runEval([]string{"-C", dir, "-no-log", "-json"})
	})
	if jsonCode != exitDiscard {
		t.Fatalf("runEval -json (re-measuring the just-kept commit, unchanged) = %d, want %d (DISCARD)\nstdout=%s",
			jsonCode, exitDiscard, jsonOut)
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
	if rows[0].Status != "keep" {
		t.Errorf("row 0 (a genuine improvement over the original baseline) status = %q, want keep", rows[0].Status)
	}
	if rows[0].AllocsDelta >= 0 {
		t.Errorf("row 0: AllocsDelta = %v, want negative (the rewrite allocates less)", rows[0].AllocsDelta)
	}
	if rows[1].Status != "discard" {
		t.Errorf("row 1 (re-measuring the just-kept commit, unchanged) status = %q, want discard", rows[1].Status)
	}
}

func TestEvalWritesOneCoherentStreamWhenStdoutAliasesRunLog(t *testing.T) {
	if testing.Short() {
		t.Skip("runs a real build/vet/test/bench cycle; skipped in -short")
	}
	dir := copyDemoRepo(t)
	mustInit(t, dir)

	// Shrink count/benchtime for speed; must stay >= config's significance
	// floor (4).
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

	// No source change: this only needs a valid eval run, not any
	// particular verdict. Committing an empty change is enough to give
	// eval something to check out and measure.
	runGit(t, dir, "commit", "-q", "--allow-empty", "-m", "no-op change")

	// Simulate a caller (e.g. a stale program.md, or a human's shell) that
	// pointed the process's own stdout at exactly the path eval computes
	// for run.log — the mistake FIX 1/FIX 2 exist to survive. This must be
	// a real file at that exact path, not a pipe: os.SameFile compares
	// device+inode, and the whole point is that eval's own run.log open
	// and the process's stdout now refer to the identical file.
	logPath := filepath.Join(dir, "run.log")
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	origStdout := os.Stdout
	os.Stdout = f
	var code int
	func() {
		defer func() { os.Stdout = origStdout }()
		code = runEval([]string{"-C", dir})
	}()
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	if code != exitOK && code != exitDiscard {
		t.Fatalf("runEval = %d, want %d (KEEP) or %d (DISCARD) for a no-op change", code, exitOK, exitDiscard)
	}

	got, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(got)

	// A marker unique to the real subprocess transcript, not the coincidence
	// that the human verdict summary also prints a "go build ./..." stage
	// line: runner.Go logs every command exactly as
	// "\n$ go <args>\n(dir=...", which the verdict summary never contains
	// (its stage line reads "go build ./...             ok", with no
	// leading "$" and no following "(dir=").
	const transcriptMarker = "\n$ go build ./...\n(dir="
	if !strings.Contains(content, transcriptMarker) {
		t.Errorf("run.log lacks the build transcript (no %q); got:\n%s", transcriptMarker, content)
	}
	if !strings.Contains(content, "VERDICT:") {
		t.Errorf("run.log = %q, want the final verdict line intact", content)
	}
	// Neither must have clobbered the other: the transcript is written
	// during pipeline.Eval, the verdict only after it returns, so an intact
	// file has the transcript appearing strictly before the verdict, and
	// the file as a whole must still be transcript-sized, not shrunk down
	// to roughly just the small verdict block a clobber would leave behind.
	if ti, vi := strings.Index(content, transcriptMarker), strings.Index(content, "VERDICT:"); ti < 0 || vi < 0 || ti > vi {
		t.Errorf("run.log = %q, want the transcript before the verdict, not overwritten by it", content)
	}
	const minIntactSize = 2000 // the real transcript runs several KB; a clobber truncates it to near nothing
	if len(content) < minIntactSize {
		t.Errorf("run.log is %d bytes, want at least %d: looks truncated/clobbered\n%s", len(content), minIntactSize, content)
	}
}

func TestEvalMissingConfigSuggestsInit(t *testing.T) {
	dir := copyDemoRepo(t)
	// Simulate a run branch that exists without ever having gone through
	// init/baseline (e.g. hand-created, or state corruption) — no
	// .autoresearch/config.yaml at all.
	runGit(t, dir, "checkout", "-q", "-b", "autoresearch-go/sep4")

	var code int
	stderr := captureStderr(t, func() {
		code = runEval([]string{"-C", dir, "-no-log"})
	})
	if code != exitUsage {
		t.Fatalf("runEval with no config = %d, want %d", code, exitUsage)
	}
	if !strings.Contains(stderr, "autoresearch-go init") {
		t.Errorf("stderr = %q, want it to suggest `autoresearch-go init` for a genuinely absent config", stderr)
	}
}

func TestEvalInvalidConfigDoesNotSuggestInit(t *testing.T) {
	// init refuses to overwrite an existing config.yaml without -force, so
	// telling an agent to run init when a config already exists (just an
	// invalid one) hands it a second dead-end error. It must instead be
	// told to correct the file in place.
	dir := copyDemoRepo(t)
	mustInit(t, dir)
	runGit(t, dir, "checkout", "-q", "-b", "autoresearch-go/sep4")

	configPath := filepath.Join(dir, config.Path)
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Count = 2 // below the significance floor: invalid, but the file exists
	if err := os.WriteFile(configPath, renderConfig(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	var code int
	stderr := captureStderr(t, func() {
		code = runEval([]string{"-C", dir, "-no-log"})
	})
	if code != exitUsage {
		t.Fatalf("runEval with an invalid config = %d, want %d", code, exitUsage)
	}
	if strings.Contains(stderr, "autoresearch-go init") {
		t.Errorf("stderr = %q, want no dead-end suggestion to run init (it would refuse without -force)", stderr)
	}
	if !strings.Contains(stderr, "count must be at least") {
		t.Errorf("stderr = %q, want the underlying validation error naming the bad field", stderr)
	}
	if !strings.Contains(stderr, "correct") {
		t.Errorf("stderr = %q, want guidance to correct the config in place", stderr)
	}
}

// A measurement that cannot support its own verdict must say so where the
// verdict is read. benchmath's warnings and the harness's reachability check
// are worthless if they stop at the package boundary.

func TestHumanOutputPrintsMeasurementWarnings(t *testing.T) {
	res := verdict.Result{
		Status: verdict.StatusDiscard, Reason: verdict.ReasonNoImprovement,
		Score: 0.99, Message: "score 0.9900 (-1.00%), no significant improvement",
		Warnings: []string{"need >= 6 samples for confidence interval at level 0.95"},
	}
	deltas := []bench.Delta{{
		Name: "BenchmarkCountWords", Unit: bench.UnitTime, BaseCenter: 100, CandCenter: 99,
		Ratio: 0.99, PctChange: -1, P: 0.4, Alpha: 0.05, NBase: 5, NCand: 5,
	}}
	cfg := config.Default()
	out := captureStdout(t, func() { printHuman(res, deltas, nil, cfg) })

	if !strings.Contains(out, "need >= 6 samples") {
		t.Errorf("stdout = %q, want the measurement warning printed", out)
	}
	if !strings.Contains(out, "WARNING") {
		t.Errorf("stdout = %q, want warnings labelled so they are not read as results", out)
	}
	if !strings.Contains(out, "VERDICT: DISCARD") {
		t.Errorf("stdout = %q, want the verdict line still present", out)
	}
	if strings.Index(out, "need >= 6 samples") > strings.Index(out, "VERDICT:") {
		t.Errorf("warning printed after the verdict, where a skimming reader stops:\n%s", out)
	}
}

func TestJSONOutputIncludesMeasurementWarnings(t *testing.T) {
	res := verdict.Result{
		Status: verdict.StatusDiscard, Reason: verdict.ReasonNoImprovement, Score: 0.99,
		Warnings: []string{"need >= 6 samples for confidence interval at level 0.95"},
	}
	out := captureStdout(t, func() { printJSON(res, nil, nil) })

	var got struct {
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("unmarshal %q: %v", out, err)
	}
	if len(got.Warnings) != 1 || !strings.Contains(got.Warnings[0], "need >= 6 samples") {
		t.Errorf("warnings = %v, want the measurement warning", got.Warnings)
	}
}
