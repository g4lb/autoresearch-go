package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/g4lb/autoresearch-go/internal/bench"
	"github.com/g4lb/autoresearch-go/internal/config"
	"github.com/g4lb/autoresearch-go/internal/gitx"
	"github.com/g4lb/autoresearch-go/internal/pipeline"
	"github.com/g4lb/autoresearch-go/internal/results"
	"github.com/g4lb/autoresearch-go/internal/state"
	"github.com/g4lb/autoresearch-go/internal/verdict"
)

// branchPrefix is the run-branch naming convention `baseline` establishes.
// eval strips it to recover the run tag, which is how it finds the run's
// state.StateDir (frozen tests, baseline record, pinned worktree).
const branchPrefix = "autoresearch-go/"

// runEval resolves the run's state from the current git branch, runs one
// pipeline.Eval, prints the result, appends a results.tsv row, and exits
// with verdict.Result.ExitCode() — the code program.md branches on.
func runEval(args []string) int {
	fs := flag.NewFlagSet("eval", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dir := fs.String("C", ".", "repository root (or a directory inside it)")
	jsonOut := fs.Bool("json", false, "print the verdict and deltas as a single JSON object and nothing else")
	desc := fs.String("desc", "", "short description of this experiment, recorded in results.tsv")
	noLog := fs.Bool("no-log", false, "write subprocess output to stdout instead of run.log")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}

	root, err := gitx.Root(*dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "autoresearch-go eval: %s is not inside a git repository: %v\n", *dir, err)
		return exitUsage
	}

	// The run tag comes from the checked-out branch, not a flag: eval must
	// run on the same run branch baseline created, so there is no way to
	// point it at the wrong tag by mistake.
	branch, err := gitx.CurrentBranch(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "autoresearch-go eval: %v\n", err)
		return exitUsage
	}
	if !strings.HasPrefix(branch, branchPrefix) {
		fmt.Fprintf(os.Stderr, "autoresearch-go eval: current branch %q is not a run branch "+
			"(expected %s<tag>). Check out your run branch first, e.g. "+
			"`git checkout %ssep4`, or run `autoresearch-go baseline` if you have not started a run yet.\n",
			branch, branchPrefix, branchPrefix)
		return exitUsage
	}
	tag := strings.TrimPrefix(branch, branchPrefix)
	// tag is derived from a branch name git itself already validated, so a
	// traversal tag cannot reach here in practice. Check anyway: this is the
	// same trust boundary FIX 1 hardens in `baseline`, and validating costs
	// nothing (state.StateDir enforces it internally regardless).
	if err := state.ValidTag(tag); err != nil {
		fmt.Fprintf(os.Stderr, "autoresearch-go eval: %v\n", err)
		return exitUsage
	}

	stateDir, err := state.StateDir(root, tag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "autoresearch-go eval: %v\n", err)
		return exitUsage
	}

	configPath := filepath.Join(root, config.Path)
	// Distinguish "no config yet" from "a config exists but is invalid":
	// init refuses to overwrite an existing config.yaml without -force, so
	// telling an agent to run init when one is already there just hands it
	// a second error and no path forward. Only a genuinely absent config
	// should point at init; an existing-but-invalid one (including a
	// count below the significance floor, now that Validate enforces it)
	// needs the human to fix the named field in place.
	if _, statErr := os.Stat(configPath); os.IsNotExist(statErr) {
		fmt.Fprintf(os.Stderr, "autoresearch-go eval: no config at %s\n", configPath)
		fmt.Fprintln(os.Stderr, "run `autoresearch-go init` first.")
		return exitUsage
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "autoresearch-go eval: %v\n", err)
		fmt.Fprintf(os.Stderr, "%s exists but is invalid; correct the named field and try again "+
			"(init will refuse to regenerate it without -force).\n", configPath)
		return exitUsage
	}

	base, err := state.LoadBaseline(filepath.Join(stateDir, state.BaselineFile))
	if err != nil {
		fmt.Fprintf(os.Stderr, "autoresearch-go eval: %v\n", err)
		return exitUsage
	}

	// baseline.json is written before the worktree pin completes, so its
	// existence alone does not prove baseline finished: a run interrupted
	// between the two would otherwise pass this check and fail confusingly
	// deep inside measure.Run instead. Verify the pinned worktree is
	// actually there before doing any work.
	worktreeDir := filepath.Join(stateDir, state.WorktreeName)
	if fi, statErr := os.Stat(worktreeDir); statErr != nil || !fi.IsDir() {
		fmt.Fprintf(os.Stderr, "autoresearch-go eval: baseline worktree missing at %s — "+
			"'autoresearch-go baseline -tag %s' did not finish pinning it (interrupted, or the "+
			"directory was later removed). Re-run `autoresearch-go baseline -tag %s -force`.\n",
			worktreeDir, tag, tag)
		return exitUsage
	}

	// Subprocess output (go build/vet/test/bench) can be large; by default
	// it is written to run.log rather than streamed, so an unattended agent
	// never has its context flooded by one experiment's output. -no-log
	// opts into seeing it live instead, for interactive debugging.
	var logWriter io.Writer
	if *noLog {
		logWriter = os.Stdout
	} else {
		logPath := filepath.Join(root, pipeline.RunLogName)
		// A caller (a stale program.md, a human's shell, a wrapper script)
		// may have already pointed the process's own stdout at run.log —
		// exactly the mistake this file exists to prevent. Opening a SECOND
		// descriptor on that same path would not append to the first: it
		// would seek to 0 and overwrite from the start, so whichever of the
		// transcript or the final verdict is written second clobbers the
		// other. os.SameFile detects that case by comparing the two open
		// files' underlying identity (device + inode), not their paths, so
		// it also catches a symlink or a bind mount pointing at run.log. When
		// they are the same file, write straight to the already-open
		// os.Stdout instead of opening our own handle, so the transcript and
		// the verdict land in one coherent stream in the order produced. A
		// failed Stat (stdout is a pipe, a socket, or already closed) is not
		// an error here — it just means they cannot be the same file, so
		// this falls back to opening run.log normally.
		if stdoutInfo, sErr := os.Stdout.Stat(); sErr == nil {
			if logInfo, lErr := os.Stat(logPath); lErr == nil && os.SameFile(stdoutInfo, logInfo) {
				logWriter = os.Stdout
			}
		}
		if logWriter == nil {
			logFile, ferr := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
			if ferr != nil {
				fmt.Fprintf(os.Stderr, "autoresearch-go eval: open %s: %v\n", logPath, ferr)
				return exitUsage
			}
			defer logFile.Close()
			logWriter = logFile
		}
	}

	res, meas, err := pipeline.Eval(context.Background(), pipeline.Options{
		Root:     root,
		StateDir: stateDir,
		Cfg:      cfg,
		Base:     base,
		Log:      logWriter,
	})
	if err != nil {
		// A non-nil error here means the harness itself malfunctioned
		// (I/O, git, a malformed baseline) — not that the candidate was
		// rejected. verdict.Result.ExitCode() would misreport that as a
		// gate failure, so this is reported and exits separately.
		fmt.Fprintf(os.Stderr, "autoresearch-go eval: %v\n", err)
		return exitUsage
	}

	// meas is nil for a gate failure before measurement; treat that the
	// same as "nothing measured" rather than special-casing it everywhere.
	var timeDeltas, allocsDeltas []bench.Delta
	if meas != nil {
		timeDeltas, allocsDeltas = meas.Time, meas.Allocs
	}
	bestName, bestPct := bestDelta(timeDeltas)

	commit, cErr := gitx.HeadCommit(root)
	if cErr != nil {
		fmt.Fprintf(os.Stderr, "autoresearch-go eval: %v\n", cErr)
		return exitUsage
	}
	resultsPath := filepath.Join(root, results.Path)
	if err := results.Append(resultsPath, results.Row{
		Commit:         commit,
		Score:          res.Score,
		BestBenchDelta: bestPct,
		// The allocs/op change for whichever benchmark had the best time
		// delta — allocations are never scored (see pipeline.Measurements),
		// this is purely the "why did it get faster" hint for the morning
		// read. 0 when there is nothing to report (a gate failure, or the
		// allocs comparison itself was unavailable).
		AllocsDelta: allocsDeltaFor(allocsDeltas, bestName),
		Status:      strings.ToLower(string(res.Status)),
		Description: *desc,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "autoresearch-go eval: append %s: %v\n", resultsPath, err)
		return exitUsage
	}

	if *jsonOut {
		printJSON(res, timeDeltas, allocsDeltas)
	} else {
		printHuman(res, timeDeltas, allocsDeltas, cfg)
	}
	return res.ExitCode()
}

// bestDelta returns the benchmark name and PctChange of the most negative
// (best-improving) time delta, or ("", 0) when there are none — a gate
// failure before measurement. That benchmark is treated as "the primary
// benchmark" for results.tsv's allocs_delta column.
func bestDelta(deltas []bench.Delta) (name string, pct float64) {
	for i, d := range deltas {
		if i == 0 || d.PctChange < pct {
			name, pct = d.Name, d.PctChange
		}
	}
	return name, pct
}

// allocsDeltaFor returns the allocs/op PctChange for the named benchmark,
// or 0 when there is nothing to report — no measurement happened, or the
// allocs comparison itself was unavailable (see pipeline.Measurements).
func allocsDeltaFor(allocsDeltas []bench.Delta, name string) float64 {
	if d, ok := allocsFor(allocsDeltas, name); ok {
		return d.PctChange
	}
	return 0
}

// jsonReport is what -json prints: the verdict plus every measured delta,
// as one object and nothing else, so program.md can `grep '"status"'` it.
// AllocsDeltas is informational only — see pipeline.Measurements — but
// program.md's idea bank points an agent at allocation counts as its main
// lead for what to try next, so it travels alongside the scored deltas.
type jsonReport struct {
	verdict.Result
	Deltas       []bench.Delta `json:"deltas"`
	AllocsDeltas []bench.Delta `json:"allocs_deltas"`
}

func printJSON(res verdict.Result, timeDeltas, allocsDeltas []bench.Delta) {
	b, err := json.MarshalIndent(jsonReport{Result: res, Deltas: timeDeltas, AllocsDeltas: allocsDeltas}, "", "  ")
	if err != nil {
		// jsonReport is plain structs, strings, floats and slices of the
		// same — it cannot fail to marshal. Fall back defensively anyway,
		// since printing nothing would break the "grep the status" contract.
		fmt.Printf("{\"status\":%q,\"reason\":\"internal_error\",\"message\":%q}\n", res.Status, err.Error())
		return
	}
	fmt.Println(string(b))
}

// gateStage names one correctness gate in the order pipeline.Eval checks
// it, paired with the verdict.Reason that means it failed.
type gateStage struct {
	label  string
	reason verdict.Reason
}

func gateStages(cfg config.Config) []gateStage {
	testLabel := "go test ./..."
	if cfg.Race {
		testLabel += " -race"
	}
	return []gateStage{
		{"checking scope", verdict.ReasonScope},
		{"checking config integrity", verdict.ReasonConfigChanged},
		{"restoring frozen tests", verdict.ReasonNewTestFile},
		{"go build ./...", verdict.ReasonBuild},
		{"go vet ./...", verdict.ReasonVet},
		{testLabel, verdict.ReasonTests},
		{"checking baseline worktree integrity", verdict.ReasonBaselineTampered},
	}
}

// printHuman writes the result in the shape a human (or an agent reading
// run.log) skims for a one-line answer: which gate failed, or the
// per-benchmark deltas (time, and allocs/op as a hint) and the final
// score, ending with the verdict.
func printHuman(res verdict.Result, timeDeltas, allocsDeltas []bench.Delta, cfg config.Config) {
	stages := gateStages(cfg)
	failedAt := -1
	for i, s := range stages {
		if s.reason == res.Reason {
			failedAt = i
			break
		}
	}
	if res.Reason == verdict.ReasonTimeout {
		// go build and go test share ReasonTimeout; the message says which.
		if strings.Contains(res.Message, "build") {
			failedAt = 3
		} else {
			failedAt = 5
		}
	}

	if failedAt >= 0 {
		for i, s := range stages {
			if i < failedAt {
				fmt.Printf("%-26s ok\n", s.label)
				continue
			}
			fmt.Printf("%-26s FAILED\n", s.label)
			fmt.Println(res.Message)
			break
		}
		fmt.Printf("\nVERDICT: %s\n", res.Status)
		return
	}

	// Every gate passed, so the candidate was measured against baseline.
	for _, s := range stages {
		fmt.Printf("%-26s ok\n", s.label)
	}
	fmt.Printf("bench x%d vs baseline x%d\n\n", cfg.Count, cfg.Count)

	sorted := append([]bench.Delta(nil), timeDeltas...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	worst := 0.0
	for _, d := range sorted {
		note := ""
		if !d.Significant {
			note = "  (not significant)"
		}
		fmt.Printf("%-24s %+6.1f%%  [p=%.3f n=%d]%s\n", d.Name, d.PctChange, d.P, d.NCand, note)
		// allocs/op is never scored (see pipeline.Measurements) — it is
		// printed purely as the "why did this get faster" hint program.md's
		// idea bank tells an agent to look for. Absent when the allocs
		// comparison itself was unavailable for this benchmark.
		if a, ok := allocsFor(allocsDeltas, d.Name); ok {
			fmt.Printf("  allocs/op             %+6.1f%%  (%.0f -> %.0f)\n", a.PctChange, a.BaseCenter, a.CandCenter)
		}
		if d.PctChange > worst {
			worst = d.PctChange
		}
	}

	guard := "OK"
	if res.Reason == verdict.ReasonGuardRegression {
		guard = "TRIPPED"
	}
	fmt.Printf("\nSCORE  %.3f  (%+.1f%%)   guard: max regress %+.1f%% < %.1f%% %s\n",
		res.Score, (res.Score-1)*100, worst, cfg.MaxRegressPct, guard)
	fmt.Printf("\nVERDICT: %s\n", res.Status)
}

// allocsFor finds the allocs/op delta for the named benchmark.
func allocsFor(allocsDeltas []bench.Delta, name string) (bench.Delta, bool) {
	for _, d := range allocsDeltas {
		if d.Name == name {
			return d, true
		}
	}
	return bench.Delta{}, false
}
