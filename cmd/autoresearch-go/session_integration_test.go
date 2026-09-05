package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/g4lb/autoresearch-go/internal/config"
	"github.com/g4lb/autoresearch-go/internal/results"
)

// TestSessionMultiExperimentSequence is the integration test that would
// have caught the most serious flaw in the harness's history.
//
// Every OTHER test in this repository evaluates a single experiment
// against a fresh baseline. That shape can never exercise what happens
// across a SEQUENCE of experiments — and that is exactly where v0.1.0's
// flaw lived: the measurement baseline was pinned to the run's original
// commit forever, so once any improvement was banked, a later change that
// merely failed to regress that earlier win was scored against the same
// stale, slow original state and wrongly kept. v0.1.1 fixed this by
// advancing the measurement baseline to each kept commit (see
// internal/pipeline's advanceMeasurementBaseline and
// internal/pipeline.TestEvalDiscardsNoOpAfterPriorImprovementKept for the
// pipeline-level regression test), but nothing before this test drove the
// real command path — init, baseline, eval, report, the same functions
// main.go dispatches to — through more than one eval in a row.
//
// The sequence below mirrors a realistic overnight session: a no-op
// BEFORE any win (the v0.1.0-era control — this already worked), a real
// win, a no-op AFTER that win (the assertion that would have caught the
// flaw: v0.1.0 returned KEEP here), a second real win (proving the fix
// does not just make everything DISCARD), and finally a change that
// breaks the frozen tests.
func TestSessionMultiExperimentSequence(t *testing.T) {
	if testing.Short() {
		t.Skip("runs several real build/vet/test/bench cycles; skipped in -short")
	}

	dir := copyDemoRepo(t)

	// Swap in a deliberately much slower "before" implementation of
	// CountWords than testdata/demo ships. This test's KEEP steps (3 and
	// 5) must reliably beat Mann-Whitney significance even on a noisy,
	// shared CI runner: with n rounds per side, significance is a matter
	// of RANK ORDER across rounds, and a ~2x true effect can still let a
	// noisy baseline round come in faster than a noisy candidate round
	// (that is exactly what produced p=0.056 and a wrongly-DISCARDed
	// step 3 on GitHub Actions). Padding the "before" state with obviously
	// redundant repeated work — see stageOriginalSlow and
	// redundantStageAPasses below — pushes the true effect size to
	// roughly an order of magnitude, so no plausible amount of runner
	// noise can invert the ranking. testdata/demo itself is left
	// untouched: other tests, including the published case study, depend
	// on its exact current numbers.
	wordcountPath := filepath.Join(dir, "wordcount.go")
	if err := os.WriteFile(wordcountPath, []byte(stageOriginalSlow), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "test fixture: much slower original CountWords for CI noise robustness")

	mustInit(t, dir)

	// Shrink count/benchtime so six full eval cycles finish quickly.
	// Count must stay >= config.Validate's significance floor (4). 10
	// (config's own default) gives more rank-order headroom than the
	// minimum against ordinary measurement noise than a smaller count
	// would, at the cost of roughly double the measurement rounds.
	// Benchtime at the bottom of the documented 20-50ms window keeps each
	// round fast, which matters more now that count is doubled; the
	// enlarged true effect size (see above) more than compensates for any
	// extra per-round noise a short benchtime lets through.
	configPath := filepath.Join(dir, config.Path)
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Count = 10
	cfg.Benchtime = "20ms"
	if err := os.WriteFile(configPath, renderConfig(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "shrink count/benchtime for a fast test")

	// 1. baseline.
	if code := runBaseline([]string{"-C", dir, "-tag", "session1"}); code != exitOK {
		t.Fatalf("runBaseline = %d, want %d", code, exitOK)
	}

	testPath := filepath.Join(dir, "wordcount_test.go")
	originalTestContent, err := os.ReadFile(filepath.Join(demoRepoSrc(t), "wordcount_test.go"))
	if err != nil {
		t.Fatal(err)
	}

	// --- step 2: a no-op change BEFORE any win. The v0.1.0-era control:
	// this already worked even under the flaw, since there was no earlier
	// banked win yet to coast on.
	appendComment(t, wordcountPath, "no-op: control experiment before any win")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "no-op before any win")

	code2, out2 := runEvalCapture(t, dir, "no-op before any win")
	if code2 != exitDiscard {
		t.Fatalf("step 2 (no-op before any win) = %d, want %d (DISCARD)\n%s", code2, exitDiscard, out2)
	}

	// --- step 3: a genuine optimization. Replaces the fixture's
	// deliberately quadratic per-rune string concatenation with a
	// strings.Builder, while still tokenizing with strings.Fields. Measured
	// stand-alone (see the session log this test's report cites) this cuts
	// BenchmarkCountWords time by roughly half.
	if err := os.WriteFile(wordcountPath, []byte(stageABuilderOnly), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "optimization 1: strings.Builder replaces quadratic concatenation")

	code3, out3 := runEvalCapture(t, dir, "optimization 1: strings.Builder")
	if code3 != exitOK {
		t.Fatalf("step 3 (genuine optimization 1) = %d, want %d (KEEP)\n%s", code3, exitOK, out3)
	}

	// --- step 4: THE assertion this test exists for. A no-op change made
	// right after a real win. Under v0.1.0's pinned-forever baseline, this
	// would still be compared against the run's ORIGINAL (slow) commit and
	// would look like the same large win step 3 just banked, wrongly
	// returning KEEP. With the measurement baseline advanced to step 3's
	// commit, this is statistically indistinguishable from the state it is
	// actually measured against and must DISCARD.
	appendComment(t, wordcountPath, "no-op: control experiment after the first win")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "no-op after the first win")

	code4, out4 := runEvalCapture(t, dir, "no-op after the first win")
	if code4 != exitDiscard {
		t.Fatalf("step 4 (no-op AFTER a prior win) = %d, want %d (DISCARD) — "+
			"this is the exact flaw an advancing measurement baseline fixes: a no-op must not "+
			"coast to KEEP on the strength of an earlier, already-banked improvement\n%s",
			code4, exitDiscard, out4)
	}

	// --- step 5: a second genuine optimization, proving the fix does not
	// simply make every later experiment DISCARD. This further replaces
	// strings.Fields-based tokenization and rune-by-rune iteration with a
	// single byte-indexed pass over the whole string, and preallocates the
	// result map — the same rewrite used elsewhere in this repository as
	// the "known-good" optimized CountWords. Measured against step 3's
	// state (not the original), this is a second, independent win.
	//
	// This commit ALSO tampers with the frozen wordcount_test.go — gutting
	// it down to an empty package file — alongside the real optimization,
	// to prove the other half of the flaw's blast radius: advancing the
	// MEASUREMENT baseline must never advance what counts as a passing
	// test. If eval's frozen-test restore were ever coupled to the
	// advancing baseline instead of the baseline recorded once at `baseline`
	// time, this gutted test would stick.
	if err := os.WriteFile(wordcountPath, []byte(stageBFullyOptimized), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(testPath, []byte("package demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "optimization 2: single-pass byte loop, preallocated map (tampers with frozen test)")

	code5, out5 := runEvalCapture(t, dir, "optimization 2: single-pass byte loop")
	if code5 != exitOK {
		t.Fatalf("step 5 (genuine optimization 2) = %d, want %d (KEEP)\n%s", code5, exitOK, out5)
	}

	// The frozen test file must have been restored to EXACTLY what
	// `baseline` recorded at the run's original commit — not left gutted,
	// and not derived in any way from the now-twice-advanced measurement
	// baseline. Advancing the measurement point must never advance the
	// success criteria.
	restoredTest, err := os.ReadFile(testPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(restoredTest) != string(originalTestContent) {
		t.Fatalf("wordcount_test.go after step 5 = %q, want it restored to the ORIGINAL baseline content %q — "+
			"advancing the measurement baseline must never advance (or relax) the frozen success criteria",
			restoredTest, originalTestContent)
	}

	// --- step 6: a change that breaks the frozen tests. The
	// implementation is replaced with one that is fast but wrong (it
	// always reports no words), which the still-frozen, still-original
	// TestCountWords assertions must catch.
	if err := os.WriteFile(wordcountPath, []byte(brokenWordCount), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "break CountWords")

	code6, out6 := runEvalCapture(t, dir, "break CountWords")
	if code6 != exitFail {
		t.Fatalf("step 6 (breaks frozen tests) = %d, want %d (FAIL)\n%s", code6, exitFail, out6)
	}

	// --- assert on the session as a whole: results.tsv.
	rows, err := results.Load(filepath.Join(dir, results.Path))
	if err != nil {
		t.Fatal(err)
	}
	wantStatuses := []string{"discard", "keep", "discard", "keep", "fail"}
	if len(rows) != len(wantStatuses) {
		t.Fatalf("results.tsv has %d row(s), want %d (one per eval call)\nrows: %+v", len(rows), len(wantStatuses), rows)
	}
	for i, want := range wantStatuses {
		if rows[i].Status != want {
			t.Errorf("row %d status = %q, want %q (row: %+v)", i, rows[i].Status, want, rows[i])
		}
	}
	// Both kept rows must be genuine improvements: score < 1 (faster than
	// what they were measured against) and a negative best-benchmark delta.
	for _, i := range []int{1, 3} {
		if rows[i].Score >= 1 {
			t.Errorf("row %d (kept) score = %v, want < 1", i, rows[i].Score)
		}
		if rows[i].BestBenchDelta >= 0 {
			t.Errorf("row %d (kept) best_bench_delta = %v, want negative", i, rows[i].BestBenchDelta)
		}
	}

	// --- assert on `report`'s summary of the whole session.
	reportOut := captureStdout(t, func() {
		if code := runReport([]string{"-C", dir}); code != exitOK {
			t.Fatalf("runReport = %d, want %d", code, exitOK)
		}
	})
	if !strings.Contains(reportOut, "kept: 2") {
		t.Errorf("report = %q, want \"kept: 2\"", reportOut)
	}
	if !strings.Contains(reportOut, "discarded: 2") {
		t.Errorf("report = %q, want \"discarded: 2\"", reportOut)
	}
	if !strings.Contains(reportOut, "failed: 1") {
		t.Errorf("report = %q, want \"failed: 1\"", reportOut)
	}
	if !strings.Contains(reportOut, "crashed: 0") {
		t.Errorf("report = %q, want \"crashed: 0\"", reportOut)
	}
	t.Logf("session report:\n%s", reportOut)

	// The cumulative speedup, under an advancing baseline, is the PRODUCT
	// of the two kept scores (see cmd_report.go's printReportSummary), not
	// the latest kept score alone and not the sum of "percent off the
	// original" the flaw would have reported. Two real, independent wins
	// on the same benchmark compound to something close to end-to-end
	// (original vs. fully-optimized) speedup — pin a plausible RANGE
	// rather than an exact figure, since real measurement varies, but one
	// tight enough that the flaw's overstatement (which would additionally
	// bank the no-op in step 4 as a third "win", inflating this number
	// well past what two real optimizations alone produce) would fall
	// outside it.
	// The band is wide (and high) because both stages now carry the
	// redundantOriginalPasses/redundantStageAPasses padding described
	// above stageOriginalSlow: the real, order-of-magnitude true effect at
	// each step compounds to a cumulative speedup near (but, since real
	// measurement varies some run to run, not pinned to) the roughly
	// 99% two 10x-ish steps in a row multiply out to.
	speedup := parseCumulativeSpeedup(t, reportOut)
	const minSpeedup, maxSpeedup = 90.0, 99.99
	if speedup < minSpeedup || speedup > maxSpeedup {
		t.Errorf("cumulative speedup = %.1f%%, want in [%.1f%%, %.1f%%] — a real two-step improvement on "+
			"CountWords, not overstated by a phantom third win\nreport:\n%s", speedup, minSpeedup, maxSpeedup, reportOut)
	}
}

// runEvalCapture runs `eval` against dir with -no-log (so a busy run.log
// never obscures a failure) and a description, returning the exit code and
// captured stdout for use in a failure message.
func runEvalCapture(t *testing.T, dir, desc string) (int, string) {
	t.Helper()
	var code int
	out := captureStdout(t, func() {
		code = runEval([]string{"-C", dir, "-no-log", "-desc", desc})
	})
	return code, out
}

// appendComment appends a no-op comment line to a Go source file — a
// change that alters nothing about what the file does.
func appendComment(t *testing.T, path, comment string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	if _, err := fmt.Fprintf(f, "\n// %s\n", comment); err != nil {
		t.Fatalf("append comment to %s: %v", path, err)
	}
}

// cumulativeSpeedupRe matches printReportSummary's
// "cumulative speedup: NN.N%" line.
var cumulativeSpeedupRe = regexp.MustCompile(`cumulative speedup: (-?[0-9.]+)%`)

// parseCumulativeSpeedup extracts the percentage from `report`'s output.
func parseCumulativeSpeedup(t *testing.T, reportOut string) float64 {
	t.Helper()
	m := cumulativeSpeedupRe.FindStringSubmatch(reportOut)
	if m == nil {
		t.Fatalf("report output %q has no \"cumulative speedup: NN.N%%\" line", reportOut)
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		t.Fatalf("parse cumulative speedup %q: %v", m[1], err)
	}
	return v
}

// stageOriginalSlow replaces testdata/demo's CountWords (used verbatim for
// every other test in this repository) with an implementation that does the
// exact same deliberately-quadratic per-rune concatenation, but additionally
// repeats the whole computation redundantOriginalPasses times, keeping only
// the last (correct) result. That padding has no bearing on correctness or
// on the "real" optimization story stageABuilderOnly and
// stageBFullyOptimized tell below — it exists solely so step 3's true
// effect size is large enough (see the comment above copyDemoRepo's call in
// TestSessionMultiExperimentSequence) to survive a noisy, loaded CI runner.
const stageOriginalSlow = `// Package demo is a fixture for autoresearch-go's own integration tests.
// The implementation is intentionally suboptimal.
package demo

import "strings"

// redundantOriginalPasses inflates CountWords' cost well beyond its
// algorithmic cost alone, purely for this test's statistical robustness
// under CI noise (see stageOriginalSlow's doc comment). It has no bearing
// on correctness: every pass recomputes the same result and only the last
// is kept.
const redundantOriginalPasses = 40

// CountWords returns how many times each lowercase word appears in s.
// Words are separated by whitespace; surrounding punctuation is stripped.
func CountWords(s string) map[string]int {
	var counts map[string]int
	for pass := 0; pass < redundantOriginalPasses; pass++ {
		counts = map[string]int{}
		for _, field := range strings.Fields(s) {
			word := ""
			for _, r := range field {
				if r >= 'A' && r <= 'Z' {
					r = r + ('a' - 'A')
				}
				if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
					// Deliberately quadratic: rebuilds the string every rune.
					word = word + string(r)
				}
			}
			if word != "" {
				counts[word]++
			}
		}
	}
	return counts
}
`

// stageABuilderOnly is the fixture's CountWords with its quadratic
// per-rune string concatenation replaced by a strings.Builder, while still
// tokenizing with strings.Fields. Algorithmically this alone removes the
// overwhelming majority of stageOriginalSlow's allocations (one small
// string allocation per character across every word in the input) and cuts
// per-call time roughly in half; combined with dropping
// redundantOriginalPasses' 40x padding down to redundantStageAPasses' much
// smaller 6x (see that constant's doc comment), step 3's measured true
// effect is roughly an order of magnitude, not roughly half.
const stageABuilderOnly = `// Package demo is a fixture for autoresearch-go's own integration tests.
package demo

import "strings"

// redundantStageAPasses is the same statistical-robustness padding
// stageOriginalSlow's redundantOriginalPasses documents, just at a much
// smaller multiple. Keeping some padding here (rather than dropping it to
// 1 in this same step) is what gives step 5 — measured against THIS state,
// not the original — its own double-digit-multiple true effect size once
// stageBFullyOptimized removes it entirely.
const redundantStageAPasses = 6

// CountWords returns how many times each lowercase word appears in s.
// Words are separated by whitespace; surrounding punctuation is stripped.
func CountWords(s string) map[string]int {
	var counts map[string]int
	for pass := 0; pass < redundantStageAPasses; pass++ {
		counts = map[string]int{}
		for _, field := range strings.Fields(s) {
			var b strings.Builder
			for _, r := range field {
				if r >= 'A' && r <= 'Z' {
					r = r + ('a' - 'A')
				}
				if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
					b.WriteRune(r)
				}
			}
			if b.Len() > 0 {
				counts[b.String()]++
			}
		}
	}
	return counts
}
`

// stageBFullyOptimized further replaces strings.Fields tokenization and
// rune-by-rune iteration with a single byte-indexed pass over the whole
// string, and preallocates the result map — and drops stageABuilderOnly's
// redundantStageAPasses padding entirely (a single pass, not 6). Measured
// against stageA (not the original), this combination is a second,
// independent improvement of roughly an order of magnitude, comfortably
// clear of noise for the same rank-order reason step 3 needs to be.
const stageBFullyOptimized = `// Package demo is a fixture for autoresearch-go's own integration tests.
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

// brokenWordCount is fast but wrong: it always reports no words, which the
// still-frozen, still-original TestCountWords assertions must catch.
const brokenWordCount = `// Package demo is a fixture for autoresearch-go's own integration tests.
package demo

// CountWords is deliberately broken: it always reports no words.
func CountWords(s string) map[string]int {
	return map[string]int{}
}
`
