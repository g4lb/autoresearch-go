package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/g4lb/autor3search-go/internal/bench"
	"github.com/g4lb/autor3search-go/internal/config"
	"github.com/g4lb/autor3search-go/internal/gitx"
	"github.com/g4lb/autor3search-go/internal/pipeline"
	"github.com/g4lb/autor3search-go/internal/results"
	"github.com/g4lb/autor3search-go/internal/state"
	"github.com/g4lb/autor3search-go/internal/verdict"
)

// branchPrefix is the run-branch naming convention `baseline` establishes.
// resolveRunRef strips it to recover the run tag, which is how eval, status
// and stop all find a run's state.StateDir (frozen tests, baseline record,
// pinned worktree).
const branchPrefix = "autor3search-go/"

// runEval runs one experiment, wiring interrupt handling around it.
//
// The context is cancellable by SIGINT and SIGTERM, and that is load-bearing
// rather than tidy-up: internal/runner puts every `go test` in its own
// process group and kills the GROUP when the context is cancelled, because
// the compiled benchmark binary runs as a grandchild. Without a signal-aware
// context here, a Ctrl+C at the terminal — or `stop -force` — would kill
// eval and orphan that benchmark binary, which then keeps burning CPU and
// corrupts every later measurement on the machine.
func runEval(args []string) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runEvalCtx(ctx, args)
}

// runEvalCtx resolves the run's state from the current git branch, runs one
// pipeline.Eval, prints the result, appends a results.tsv row, and returns
// verdict.Result.ExitCode() — the code program.md branches on.
func runEvalCtx(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("eval", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dir := fs.String("C", ".", "repository root (or a directory inside it)")
	jsonOut := fs.Bool("json", false, "print the verdict and deltas as a single JSON object and nothing else")
	desc := fs.String("desc", "", "short description of this experiment, recorded in results.tsv")
	noLog := fs.Bool("no-log", false, "write subprocess output to stdout instead of run.log")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}

	// No -tag here, unlike `status` and `stop`: eval must run on the same
	// run branch baseline created, so there is no way to point it at the
	// wrong run by mistake.
	ref, code := resolveRunRef("eval", *dir, "")
	if code != exitOK {
		return code
	}
	root, stateDir := ref.Root, ref.StateDir

	cfg, _, code := loadConfig("eval", root)
	if code != exitOK {
		return code
	}
	base, err := state.LoadBaseline(filepath.Join(stateDir, state.BaselineFile))
	if err != nil {
		fmt.Fprintf(os.Stderr, "autor3search-go eval: %v\n", err)
		return exitUsage
	}

	// baseline.json is written before the worktree pin completes, so its
	// existence alone does not prove baseline finished: a run interrupted
	// between the two would otherwise pass this check and fail confusingly
	// deep inside measure.Run instead. Verify the pinned worktree is
	// actually there before doing any work.
	worktreeDir := ref.WorktreeDir()
	if fi, statErr := os.Stat(worktreeDir); statErr != nil || !fi.IsDir() {
		fmt.Fprintf(os.Stderr, "autor3search-go eval: baseline worktree missing at %s — "+
			"'autor3search-go baseline -tag %s' did not finish pinning it (interrupted, or the "+
			"directory was later removed). Re-run `autor3search-go baseline -tag %s -force`.\n",
			worktreeDir, ref.Tag, ref.Tag)
		return exitUsage
	}

	// Claim the run for the duration. Two evals sharing one pinned worktree
	// would measure each other's checkouts; the claim also tells `status`
	// that an experiment is in flight and gives `stop -force` a process to
	// signal. It is released on every exit path below.
	release, err := state.ClaimEval(stateDir, os.Getpid())
	if err != nil {
		fmt.Fprintf(os.Stderr, "autor3search-go eval: %v\n", err)
		return exitUsage
	}
	defer release()

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
				fmt.Fprintf(os.Stderr, "autor3search-go eval: open %s: %v\n", logPath, ferr)
				return exitUsage
			}
			defer logFile.Close()
			logWriter = logFile
		}
	}

	res, meas, err := pipeline.Eval(ctx, pipeline.Options{
		Root:     root,
		StateDir: stateDir,
		Cfg:      cfg,
		Base:     base,
		Log:      logWriter,
	})

	// A cancelled context means the human reached for the brake mid-flight
	// (Ctrl+C, or `stop -force`) — NOT that the candidate was rejected. It
	// is checked before err, because a cancellation surfaces as an ordinary
	// subprocess failure deeper down and would otherwise be reported as a
	// build CRASH the agent might act on.
	if ctx.Err() != nil {
		return reportAbort(ref, base, *jsonOut)
	}
	if err != nil {
		// A non-nil error here means the harness itself malfunctioned
		// (I/O, git, a malformed baseline) — not that the candidate was
		// rejected. verdict.Result.ExitCode() would misreport that as a
		// gate failure, so this is reported and exits separately.
		fmt.Fprintf(os.Stderr, "autor3search-go eval: %v\n", err)
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
		fmt.Fprintf(os.Stderr, "autor3search-go eval: %v\n", cErr)
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
		fmt.Fprintf(os.Stderr, "autor3search-go eval: append %s: %v\n", resultsPath, err)
		return exitUsage
	}

	// Read the stop request only now that the experiment is scored and
	// recorded. eval never REFUSES to run because a stop is pending:
	// refusing would throw away work the agent has already committed and
	// leave that commit with no verdict. The graceful stop is the agent's
	// to act on, after it has applied this verdict.
	ctxRun := runContext(ref, base, experimentNumber(resultsPath))

	if *jsonOut {
		printJSON(res, timeDeltas, allocsDeltas, ctxRun)
	} else {
		printHuman(res, timeDeltas, allocsDeltas, cfg, ctxRun)
	}
	return res.ExitCode()
}

// reportAbort renders an eval whose context was cancelled mid-experiment.
//
// No results.tsv row is written, deliberately: nothing was measured, and a
// row there is the human's record of an experiment that actually ran. The
// exit code is exitFail rather than a new code of its own, so no existing
// contract changes — program.md already tells the agent to treat a status it
// does not recognize the way it treats FAIL, which is exactly right here:
// drop the commit, and (seeing stop_requested) leave the loop.
func reportAbort(ref runRef, base *state.Baseline, jsonOut bool) int {
	res := verdict.Result{
		Status:  "ABORTED",
		Reason:  "stop_forced",
		Score:   0,
		Message: "eval was interrupted before the experiment could be measured; nothing was recorded",
	}
	rc := runContext(ref, base, experimentNumber(filepath.Join(ref.Root, results.Path)))
	// An abort IS a stop, whether it came from `stop -force` or a bare
	// Ctrl+C. Reporting it as one means an agent interrupted by hand exits
	// its loop cleanly instead of starting another experiment.
	rc.StopRequested = true

	if jsonOut {
		printJSON(res, nil, nil, rc)
	} else {
		fmt.Printf("\n%s\n", res.Message)
		fmt.Printf("\nVERDICT: %s\n", res.Status)
	}
	return exitFail
}

// runCtx is the "where am I" half of eval's output: which run, which branch,
// which pinned worktree, how far into the loop, and whether the human has
// asked for a stop.
//
// It travels in the JSON because that is the ONLY channel the loop reads.
// `eval --json` prints one object and nothing else by contract, so a
// human-readable header would never reach the agent — and the human watching
// the transcript learns where the run is only if the agent can restate it.
type runCtx struct {
	Tag            string `json:"tag"`
	Branch         string `json:"branch"`
	BaselineCommit string `json:"baseline_commit"`
	MeasureCommit  string `json:"measure_commit"`
	Worktree       string `json:"worktree"`
	Experiment     int    `json:"experiment"`
	StopRequested  bool   `json:"-"`
}

func runContext(ref runRef, base *state.Baseline, experiment int) runCtx {
	return runCtx{
		Tag:            ref.Tag,
		Branch:         ref.Branch,
		BaselineCommit: base.Commit,
		MeasureCommit:  base.MeasureCommit,
		Worktree:       ref.WorktreeDir(),
		Experiment:     experiment,
		StopRequested:  state.StopRequested(ref.StateDir),
	}
}

// experimentNumber is the 1-based index of the experiment just recorded, or
// the one about to run when nothing was recorded. An unreadable results.tsv
// yields 0, meaning "unknown" — a wrong number would be worse than none.
func experimentNumber(resultsPath string) int {
	rows, err := results.Load(resultsPath)
	if err != nil {
		return 0
	}
	return len(rows)
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
	// StopRequested says the human has asked this run to end. It is a
	// SIBLING of status, never a replacement for it: the verdict still
	// stands and the agent must still apply it, then leave the loop.
	StopRequested bool          `json:"stop_requested"`
	Run           runCtx        `json:"run"`
	Deltas        []bench.Delta `json:"deltas"`
	AllocsDeltas  []bench.Delta `json:"allocs_deltas"`
}

func printJSON(res verdict.Result, timeDeltas, allocsDeltas []bench.Delta, rc runCtx) {
	b, err := json.MarshalIndent(jsonReport{
		Result:        res,
		StopRequested: rc.StopRequested,
		Run:           rc,
		Deltas:        timeDeltas,
		AllocsDeltas:  allocsDeltas,
	}, "", "  ")
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

// gateColumn is the width the gate labels are padded to: the longest label
// plus a space. Derived rather than hard-coded, because a hard-coded width
// is silently wrong the moment a gate is added or renamed past it — which is
// exactly what happened to "checking baseline worktree integrity".
func gateColumn(stages []gateStage) int {
	widest := 0
	for _, s := range stages {
		if len(s.label) > widest {
			widest = len(s.label)
		}
	}
	return widest + 1
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
func printHuman(res verdict.Result, timeDeltas, allocsDeltas []bench.Delta, cfg config.Config, rc runCtx) {
	printRunHeader(rc)
	stages := gateStages(cfg)
	column := gateColumn(stages)
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
				fmt.Printf("%-*s ok\n", column, s.label)
				continue
			}
			fmt.Printf("%-*s FAILED\n", column, s.label)
			fmt.Println(res.Message)
			break
		}
		fmt.Printf("\nVERDICT: %s\n", res.Status)
		printStopNotice(rc)
		return
	}

	// Every gate passed, so the candidate was measured against baseline.
	for _, s := range stages {
		fmt.Printf("%-*s ok\n", column, s.label)
	}
	fmt.Printf("bench x%d vs baseline x%d\n\n", cfg.Count, cfg.Count)

	sorted := append([]bench.Delta(nil), timeDeltas...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	// k mirrors verdict.Decide's Bonferroni family size: the number of
	// benchmarks compared in this experiment.
	k := len(sorted)
	worst := 0.0
	for _, d := range sorted {
		note := ""
		switch {
		case !d.Significant:
			// Significant is always "at the raw, uncorrected alpha" — see
			// internal/verdict.Decide's doc comment.
			note = "  (not significant)"
		case k > 1 && d.P >= d.Alpha/float64(k):
			// Truthful even though it looks odd: this benchmark IS
			// significant at alpha, just not at the stricter
			// Bonferroni-corrected bar verdict.Decide requires for a KEEP.
			note = fmt.Sprintf("  (significant at alpha, not at corrected alpha/%d)", k)
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
	fmt.Printf("\nSCORE  %.3f  (%+.1f%%)   min effect: %.1f%%   guard: max regress %+.1f%% < %.1f%% %s\n",
		res.Score, (res.Score-1)*100, cfg.MinEffectPct, worst, cfg.MaxRegressPct, guard)
	printWarnings(res.Warnings)
	fmt.Printf("\nVERDICT: %s\n", res.Status)
	printStopNotice(rc)
}

// printRunHeader opens the human-readable output with the same run context
// -json carries, so an interactive `eval` (or a reader of run.log) can see
// which run and which experiment produced what follows.
func printRunHeader(rc runCtx) {
	fmt.Printf("experiment %d on %s (measuring vs %s)\n\n", rc.Experiment, rc.Branch, rc.MeasureCommit)
}

// printStopNotice goes AFTER the verdict for the same reason warnings go
// before it: the verdict is where a skimming reader stops, and this is the
// one thing they should read past it for.
func printStopNotice(rc runCtx) {
	if !rc.StopRequested {
		return
	}
	fmt.Println("\nSTOP REQUESTED: apply this verdict, then exit the loop.")
	fmt.Println("To resume instead: autor3search-go stop -clear")
}

// printWarnings renders what qualifies the numbers above it: benchmath's
// warnings about underpowered comparisons, and the harness's own check that
// a KEEP was reachable at all (see verdict.Result.Warnings). They go after
// the score they are about but BEFORE the verdict line, because that is
// where a skimming reader — or an agent grepping run.log — stops.
func printWarnings(warnings []string) {
	if len(warnings) == 0 {
		return
	}
	fmt.Println()
	for _, w := range warnings {
		fmt.Printf("WARNING: %s\n", w)
	}
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
